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
// 细节：先停掉上一代钩子（卸钩 + WM_QUIT），再起新线程；
// 旧线程退出时只关闭它自己那一代的 threadDone，绝不触碰新线程的字段。
func (r *Recorder) install() error {
	r.mu.Lock()
	r.stopLocked()
	done := make(chan struct{})
	r.threadDone = done
	r.mu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		defer close(done)

		tid, _, _ := getCurrentTID.Call()

		kbCb := syscall.NewCallback(r.kbdProc)
		kh, _, kerr := setHook.Call(whKeyboardLL, kbCb, 0, 0)
		if kh == 0 {
			errCh <- fmt.Errorf("SetWindowsHookEx(键盘): %v", kerr)
			return
		}
		msCb := syscall.NewCallback(r.mouseProc)
		mh, _, merr := setHook.Call(whMouseLL, msCb, 0, 0)
		if mh == 0 {
			unhook.Call(kh) // 键盘钩子已装上但整体失败，卸掉
			errCh <- fmt.Errorf("SetWindowsHookEx(鼠标): %v", merr)
			return
		}

		r.mu.Lock()
		r.threadID = tid
		r.keyboard = windows.Handle(kh)
		r.mouse = windows.Handle(mh)
		r.mu.Unlock()
		r.lastActivity.Store(time.Now().Unix())

		errCh <- nil

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
		return fmt.Errorf("安装钩子超时")
	}
	return nil
}

// stopLocked 反注册钩子并结束消息泵线程；调用方需持锁。
// 先卸钩再发 WM_QUIT，保证新旧两代钩子不会短暂并存重复计数。
func (r *Recorder) stopLocked() {
	if r.keyboard != 0 {
		unhook.Call(uintptr(r.keyboard))
		r.keyboard = 0
	}
	if r.mouse != 0 {
		unhook.Call(uintptr(r.mouse))
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
func (r *Recorder) Drain() map[string]int {
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
