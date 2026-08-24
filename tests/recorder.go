//go:build windows

package main

import (
	"fmt"
	"runtime"
	"sync"
	"sync/atomic"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const (
	whKeyboardLL  = 13
	whMouseLL     = 14
	wmKeyDown     = 0x0100
	wmSysKeyDown  = 0x0104
	wmLButtonDown = 0x0201
	wmRButtonDown = 0x0204
	wmMButtonDown = 0x0207
	wmXButtonDown = 0x020B
	wmQuit        = 0x0012
	eventBuffer   = 16384 // 溢出直接丢弃，绝不能拖慢系统输入
)

type Recorder struct {
	ch        chan string
	paused    atomic.Bool
	lastEvent atomic.Int64 // 最近一次"有事件落盘"的时间（unix 秒）
	// 钩子线程的存活状态；字段写读都在 mu 保护下，
	// lastActivity 是钩子回调自己写的原子值，不走 mu。
	mu           sync.Mutex
	keyboard     windows.Handle
	mouse        windows.Handle
	threadID     uintptr
	threadDone   chan struct{} // 消息泵线程退出时关闭
	lastActivity atomic.Int64  // 钩子最近一次"看到输入"的时间（含鼠标移动/滚轮，仅用于看门狗）

	// abortGen 是安装代际号：每次 install 递增；超时或被并发重装取代时也递增，
	// 迟到的安装线程凭它判断自己是否已作废——作废则当场卸钩退出，绝不发布句柄，
	// 杜绝"新旧两代钩子并存 → 每个按键被计多次"。
	abortGen uint64

	drainMu sync.Mutex // 串行化 Drain，防止退出收尾与 flushLoop 并发争抢事件
	pendingMu sync.Mutex
	pending   map[string]int // 落盘失败暂存的事件，下轮重试

	// 测试注入：钩子安装/卸载的底层调用，默认走真实 syscall；
	// 单测用假实现模拟"SetWindowsHookEx 卡住后迟到返回"，验证代际作废路径。
	setHookFn func(hookType int, cb uintptr) (uintptr, error)
	unhookFn  func(handle uintptr)
}

type Hooks struct{ r *Recorder }

// Stop 反注册钩子并结束消息循环线程。
func (h *Hooks) Stop() {
	h.r.mu.Lock()
	defer h.r.mu.Unlock()
	h.r.stopLocked()
}

func NewRecorder() *Recorder {
	r := &Recorder{ch: make(chan string, eventBuffer)}
	r.setHookFn = func(hookType int, cb uintptr) (uintptr, error) {
		h, _, e := setHook.Call(uintptr(hookType), cb, 0, 0)
		return h, e
	}
	r.unhookFn = func(h uintptr) { unhook.Call(h) }
	now := time.Now().Unix()
	r.lastEvent.Store(now)
	r.lastActivity.Store(now)
	return r
}

func (r *Recorder) SetPaused(p bool) { r.paused.Store(p) }
func (r *Recorder) Paused() bool     { return r.paused.Load() }

func (r *Recorder) LastEvent() time.Time    { return time.Unix(r.lastEvent.Load(), 0) }
func (r *Recorder) LastActivity() time.Time { return time.Unix(r.lastActivity.Load(), 0) }
func (r *Recorder) ChannelDepth() int       { return len(r.ch) }

var (
	user32        = windows.NewLazySystemDLL("user32.dll")
	setHook       = user32.NewProc("SetWindowsHookExW")
	unhook        = user32.NewProc("UnhookWindowsHookEx")
	callNextHook  = user32.NewProc("CallNextHookEx")
	getMessage    = user32.NewProc("GetMessageW")
	postThreadMsg = user32.NewProc("PostThreadMessageW")
	kernel32      = windows.NewLazySystemDLL("kernel32.dll")
	getCurrentTID = kernel32.NewProc("GetCurrentThreadId")
)

type point struct{ X, Y int32 }

type kbdLLHookStruct struct {
	VKCode    uint32
	ScanCode  uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type mouseLLHookStruct struct {
	PT        point
	MouseData uint32
	Flags     uint32
	Time      uint32
	ExtraInfo uintptr
}

type winMsg struct {
	hwnd    windows.HWND
	message uint32
	wParam  uintptr
	lParam  uintptr
	time    uint32
	pt      point
}

// StartHooks 安装键盘 + 鼠标低级钩子并启动消息泵线程（与旧实现等价，可被 Reinstall 复用）。
func (r *Recorder) StartHooks() (*Hooks, error) {
	if err := r.install(); err != nil {
		return nil, err
	}
	return &Hooks{r}, nil
}

// Reinstall 卸载旧钩子后重新安装，供看门狗自愈或托盘手动触发。
func (r *Recorder) Reinstall() error { return r.install() }

// Alive 报告钩子是否仍然健康：消息泵线程未退出且两组钩子都在。
func (r *Recorder) Alive() bool {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.threadDone != nil && !isClosed(r.threadDone) && r.keyboard != 0 && r.mouse != 0
}

func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// install 安装钩子并启动消息泵线程；可重复调用。
// 细节：先停掉上一代钩子（卸钩 + WM_QUIT），等上一代线程真正退出后再起新线程；
// 通过 abortGen 代际号保证：超时或并发重装时，迟到的安装完成会被作废并自卸钩，
// 绝不与新一代钩子并存（并存会让每个按键被计多次）。
func (r *Recorder) install() error {
	r.mu.Lock()
	r.stopLocked()
	r.abortGen++ // 作废上一代可能迟到的安装完成
	gen := r.abortGen
	prevDone := r.threadDone
	done := make(chan struct{})
	r.threadDone = done
	r.mu.Unlock()

	if prevDone != nil {
		// 等上一代消息泵线程真正退出再装新钩子：旧线程队列里残留的钩子消息
		// 不会再派发到旧回调，避免新旧两代短暂并存重复计数。限时以防卡死。
		select {
		case <-prevDone:
		case <-time.After(500 * time.Millisecond):
		}
	}

	errCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer close(done)

		tid, _, _ := getCurrentTID.Call()

		kbCb := syscall.NewCallback(r.kbdProc)
		kh, kerr := r.setHookFn(whKeyboardLL, uintptr(kbCb))
		if kh == 0 {
			errCh <- fmt.Errorf("SetWindowsHookEx(键盘): %v", kerr)
			return
		}
		msCb := syscall.NewCallback(r.mouseProc)
		mh, merr := r.setHookFn(whMouseLL, uintptr(msCb))
		if mh == 0 {
			r.unhookFn(kh) // 键盘钩子已装上但整体失败，卸掉
			errCh <- fmt.Errorf("SetWindowsHookEx(鼠标): %v", merr)
			return
		}

		r.mu.Lock()
		keep := gen == r.abortGen // 未被超时/并发重装作废才发布句柄
		if keep {
			r.threadID = tid
			r.keyboard = windows.Handle(kh)
			r.mouse = windows.Handle(mh)
		}
		r.mu.Unlock()
		if !keep { // 迟到的完成：卸掉自己的钩子，不留孤儿代
			r.unhookFn(kh)
			r.unhookFn(mh)
			errCh <- fmt.Errorf("安装已被更新的请求取代")
			return
		}
		r.lastActivity.Store(time.Now().Unix())

		errCh <- nil

		r.mu.Lock()
		alive := gen == r.abortGen // 若超时已作废本代，则不再进消息泵
		r.mu.Unlock()
		if !alive {
			return
		}

		var m winMsg
		for {
			ret, _, _ := getMessage.Call(uintptr(unsafe.Pointer(&m)), 0, 0, 0)
			if ret == 0 || ret == ^uintptr(0) { // WM_QUIT 或错误
				break
			}
		}
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-time.After(3 * time.Second):
		// 超时：作废这代安装并清掉已发布的部分句柄；迟到的 goroutine
		// 恢复后会自查代际号、自卸钩退出，不会留下孤儿钩子。
		r.mu.Lock()
		r.abortGen++
		r.stopLocked()
		r.mu.Unlock()
		return fmt.Errorf("安装钩子超时")
	}
	return nil
}

// stopLocked 反注册钩子并结束消息泵线程；调用方需持锁。
// 先卸钩再发 WM_QUIT，保证新旧两代钩子不会短暂并存重复计数。
func (r *Recorder) stopLocked() {
	if r.keyboard != 0 {
		r.unhookFn(uintptr(r.keyboard))
		r.keyboard = 0
	}
	if r.mouse != 0 {
		r.unhookFn(uintptr(r.mouse))
		r.mouse = 0
	}
	if r.threadID != 0 {
		postThreadMsg.Call(r.threadID, wmQuit, 0, 0)
		r.threadID = 0
	}
}

// 注：回调里的 lparam 指向系统提供的 KBDLLHOOKSTRUCT / MSLLHOOKSTRUCT，
// 来自操作系统而非 Go 内存，转成结构体指针是安全的；
// 该写法会触发 go vet 的 unsafeptr 保守告警，项目统一用
// `go vet -unsafeptr=false ./...` 跳过此项。
func (r *Recorder) kbdProc(ncode, wparam, lparam uintptr) uintptr {
	if int32(ncode) >= 0 && (wparam == wmKeyDown || wparam == wmSysKeyDown) {
		r.lastActivity.Store(time.Now().Unix())
		vk := (*kbdLLHookStruct)(unsafe.Pointer(lparam)).VKCode
		if lbl := vkLabel(vk); lbl != "" && !r.paused.Load() {
			select {
			case r.ch <- lbl:
			default:
			}
		}
	}
	ret, _, _ := callNextHook.Call(0, ncode, wparam, lparam)
	return ret
}

func (r *Recorder) mouseProc(ncode, wparam, lparam uintptr) uintptr {
	if int32(ncode) >= 0 {
		r.lastActivity.Store(time.Now().Unix())
		var lbl string
		switch wparam {
		case wmLButtonDown:
			lbl = "mouse_left"
		case wmRButtonDown:
			lbl = "mouse_right"
		case wmMButtonDown:
			lbl = "mouse_middle"
		case wmXButtonDown:
			data := (*mouseLLHookStruct)(unsafe.Pointer(lparam)).MouseData >> 16
			if data == 2 {
				lbl = "mouse_x2"
			} else {
				lbl = "mouse_x1"
			}
		}
		if lbl != "" && !r.paused.Load() {
			select {
			case r.ch <- lbl:
			default:
			}
		}
	}
	ret, _, _ := callNextHook.Call(0, ncode, wparam, lparam)
	return ret
}

// Drain 取走缓冲里的全部事件并聚合成 键名->次数。
// 串行化保证：退出收尾（selfRestart / 托盘退出）与每秒 flushLoop 并发调用时，
// 不会出现一方取走事件、另一方永久阻塞等待或重复计数的竞态。
func (r *Recorder) Drain() map[string]int {
	r.drainMu.Lock()
	defer r.drainMu.Unlock()
	n := len(r.ch)
	out := make(map[string]int, min(n, 128))
	for range n {
		out[<-r.ch]++
	}
	if n > 0 {
		r.lastEvent.Store(time.Now().Unix())
	}
	return out
}

// HasPending 报告是否有落盘失败暂存的事件。
func (r *Recorder) HasPending() bool {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	return len(r.pending) > 0
}

// mergePending 把暂存事件并入本次待写批次并清空暂存。
func (r *Recorder) mergePending(labels map[string]int) map[string]int {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	if len(r.pending) == 0 {
		return labels
	}
	if labels == nil {
		labels = make(map[string]int, len(r.pending))
	}
	for k, v := range r.pending {
		labels[k] += v
	}
	r.pending = nil
	return labels
}

// retainPending 落盘失败时暂存整批事件，下一轮 flushOnce 重试。
// 按键名归并，长时间故障下暂存量也有界。
func (r *Recorder) retainPending(labels map[string]int) {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	if r.pending == nil {
		r.pending = labels
		return
	}
	for k, v := range labels {
		r.pending[k] += v
	}
}
