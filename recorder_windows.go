package main

import (
	"fmt"
	"runtime"
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

// Hooks 持有钩子生命周期相关的句柄与消息泵线程 ID。
type Hooks struct {
	keyboard windows.Handle
	mouse    windows.Handle
	threadID uintptr
}

// Stop 反注册钩子并结束消息循环线程。
func (h *Hooks) Stop() {
	if h.keyboard != 0 {
		unhook.Call(uintptr(h.keyboard))
	}
	if h.mouse != 0 {
		unhook.Call(uintptr(h.mouse))
	}
	if h.threadID != 0 {
		postThreadMsg.Call(h.threadID, wmQuit, 0, 0)
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

// StartHooks 在锁定的 OS 线程上安装键盘 + 鼠标低级钩子，
// 回调只做“投递一个字符串”然后立刻放行，保证不影响系统输入响应；
// 随后在该线程跑消息泵维持钩子存活，直到 Stop 发送 WM_QUIT。
func (r *Recorder) StartHooks() (*Hooks, error) {
	h := &Hooks{}
	errCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread()
		tid, _, _ := getCurrentTID.Call()
		h.threadID = tid

		kbCb := syscall.NewCallback(r.kbdProc)
		kh, _, kerr := setHook.Call(whKeyboardLL, kbCb, 0, 0)
		if kh == 0 {
			errCh <- fmt.Errorf("SetWindowsHookEx(键盘): %v", kerr)
			return
		}
		h.keyboard = windows.Handle(kh)

		msCb := syscall.NewCallback(r.mouseProc)
		mh, _, merr := setHook.Call(whMouseLL, msCb, 0, 0)
		if mh == 0 {
			errCh <- fmt.Errorf("SetWindowsHookEx(鼠标): %v", merr)
			return
		}
		h.mouse = windows.Handle(mh)

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
			return nil, err
		}
	case <-time.After(3 * time.Second):
		return nil, fmt.Errorf("安装钩子超时")
	}
	return h, nil
}

// 注：回调里的 lparam 指向系统提供的 KBDLLHOOKSTRUCT / MSLLHOOKSTRUCT，
// 来自操作系统而非 Go 内存，转成结构体指针是安全的；
// 该写法会触发 go vet 的 unsafeptr 保守告警，项目统一用
// `go vet -unsafeptr=false ./...` 跳过此项。
func (r *Recorder) kbdProc(ncode, wparam, lparam uintptr) uintptr {
	if int32(ncode) >= 0 && (wparam == wmKeyDown || wparam == wmSysKeyDown) {
		r.emit(vkLabel((*kbdLLHookStruct)(unsafe.Pointer(lparam)).VKCode))
	}
	ret, _, _ := callNextHook.Call(0, ncode, wparam, lparam)
	return ret
}

func (r *Recorder) mouseProc(ncode, wparam, lparam uintptr) uintptr {
	if int32(ncode) >= 0 {
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
