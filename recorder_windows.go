package main

import (
	"fmt"
	"runtime"
	"sync"
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
)

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

// hookState 是钩子的一代生命周期。看门狗要能重装，所以它是包级状态
// 而不是 Hooks 的字段——Hooks 只是给 main 的 defer 用的把手。
type hookState struct {
	mu         sync.Mutex
	keyboard   windows.Handle
	mouse      windows.Handle
	threadID   uintptr
	threadDone chan struct{} // 消息泵线程退出时关闭

	// abortGen 是安装代际号：每次 install 递增；超时或被并发重装取代时也递增，
	// 迟到的安装线程凭它判断自己是否已作废——作废则当场卸钩退出，绝不发布句柄，
	// 杜绝"新旧两代钩子并存 → 每个按键被计多次"。
	abortGen uint64

	// 测试注入：钩子安装/卸载的底层调用，默认走真实 syscall；
	// 单测用假实现模拟"SetWindowsHookEx 卡住后迟到返回"，验证代际作废路径。
	setHookFn func(hookType int, cb uintptr) (uintptr, error)
	unhookFn  func(handle uintptr)
}

var hooks = hookState{
	setHookFn: func(hookType int, cb uintptr) (uintptr, error) {
		h, _, e := setHook.Call(uintptr(hookType), cb, 0, 0)
		return h, e
	},
	unhookFn: func(h uintptr) { unhook.Call(h) },
}

type Hooks struct{ r *Recorder }

// Stop 反注册钩子并结束消息循环线程。
func (h *Hooks) Stop() {
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	stopLocked()
}

// StartHooks 安装键盘 + 鼠标低级钩子并启动消息泵线程。
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
	hooks.mu.Lock()
	defer hooks.mu.Unlock()
	return hooks.threadDone != nil && !isClosed(hooks.threadDone) &&
		hooks.keyboard != 0 && hooks.mouse != 0
}

// install 安装钩子并启动消息泵线程；可重复调用。
// 细节：先停掉上一代钩子（卸钩 + WM_QUIT），等上一代线程真正退出后再起新线程；
// 通过 abortGen 代际号保证：超时或并发重装时，迟到的安装完成会被作废并自卸钩，
// 绝不与新一代钩子并存（并存会让每个按键被计多次）。
func (r *Recorder) install() error {
	hooks.mu.Lock()
	stopLocked()
	hooks.abortGen++ // 作废上一代可能迟到的安装完成
	gen := hooks.abortGen
	prevDone := hooks.threadDone
	done := make(chan struct{})
	hooks.threadDone = done
	hooks.mu.Unlock()

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
		kh, kerr := hooks.setHookFn(whKeyboardLL, kbCb)
		if kh == 0 {
			errCh <- fmt.Errorf("SetWindowsHookEx(键盘): %v", kerr)
			return
		}
		msCb := syscall.NewCallback(r.mouseProc)
		mh, merr := hooks.setHookFn(whMouseLL, msCb)
		if mh == 0 {
			hooks.unhookFn(kh) // 键盘钩子已装上但整体失败，卸掉
			errCh <- fmt.Errorf("SetWindowsHookEx(鼠标): %v", merr)
			return
		}

		hooks.mu.Lock()
		keep := gen == hooks.abortGen // 未被超时/并发重装作废才发布句柄
		if keep {
			hooks.threadID = tid
			hooks.keyboard = windows.Handle(kh)
			hooks.mouse = windows.Handle(mh)
		}
		hooks.mu.Unlock()
		if !keep { // 迟到的完成：卸掉自己的钩子，不留孤儿代
			hooks.unhookFn(kh)
			hooks.unhookFn(mh)
			errCh <- fmt.Errorf("安装已被更新的请求取代")
			return
		}
		r.touch()

		errCh <- nil

		hooks.mu.Lock()
		alive := gen == hooks.abortGen // 若超时已作废本代，则不再进消息泵
		hooks.mu.Unlock()
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
		hooks.mu.Lock()
		hooks.abortGen++
		stopLocked()
		hooks.mu.Unlock()
		return fmt.Errorf("安装钩子超时")
	}
	return nil
}

// stopLocked 反注册钩子并结束消息泵线程；调用方需持锁。
// 先卸钩再发 WM_QUIT，保证新旧两代钩子不会短暂并存重复计数。
func stopLocked() {
	if hooks.keyboard != 0 {
		hooks.unhookFn(uintptr(hooks.keyboard))
		hooks.keyboard = 0
	}
	if hooks.mouse != 0 {
		hooks.unhookFn(uintptr(hooks.mouse))
		hooks.mouse = 0
	}
	if hooks.threadID != 0 {
		postThreadMsg.Call(hooks.threadID, wmQuit, 0, 0)
		hooks.threadID = 0
	}
}

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

// 注：回调里的 lparam 指向系统提供的 KBDLLHOOKSTRUCT / MSLLHOOKSTRUCT，
// 来自操作系统而非 Go 内存，转成结构体指针是安全的；
// 该写法会触发 go vet 的 unsafeptr 保守告警，项目统一用
// `go vet -unsafeptr=false ./...` 跳过此项。
func (r *Recorder) kbdProc(ncode, wparam, lparam uintptr) uintptr {
	if int32(ncode) >= 0 && (wparam == wmKeyDown || wparam == wmSysKeyDown) {
		r.touch()
		r.emit(vkLabel((*kbdLLHookStruct)(unsafe.Pointer(lparam)).VKCode))
	}
	ret, _, _ := callNextHook.Call(0, ncode, wparam, lparam)
	return ret
}

func (r *Recorder) mouseProc(ncode, wparam, lparam uintptr) uintptr {
	if int32(ncode) >= 0 {
		r.touch() // 含鼠标移动：看门狗要的是"钩子还活着"
		var lbl string
		switch wparam {
		case wmLButtonDown:
			lbl = "mouse_left"
		case wmRButtonDown:
			lbl = "mouse_right"
		case wmMButtonDown:
			lbl = "mouse_middle"
		case wmXButtonDown:
			if (*mouseLLHookStruct)(unsafe.Pointer(lparam)).MouseData>>16 == 2 {
				lbl = "mouse_x2"
			} else {
				lbl = "mouse_x1"
			}
		}
		r.emit(lbl)
	}
	ret, _, _ := callNextHook.Call(0, ncode, wparam, lparam)
	return ret
}
