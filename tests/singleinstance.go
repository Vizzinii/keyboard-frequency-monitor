//go:build windows

package main

import (
	"fmt"
	"syscall"
	"unsafe"
)

const singleInstanceMutexName = `Local\KFM_SingleInstance`

// singleInstance 借助内核命名互斥体仲裁"谁先启动"：两个实例同时启动时只有
// 第一个能持有互斥体，另一个 WaitForSingleObject(0) 返回 WAIT_TIMEOUT 得知
// 已有实例在跑。崩溃遗留（abandoned）由下一个实例自动接管；进程退出
// （含 os.Exit）时句柄由系统回收，互斥体随之释放。
type singleInstance struct {
	h uintptr
}

var (
	createMutexProc  = kernel32.NewProc("CreateMutexW")
	waitSingleProc   = kernel32.NewProc("WaitForSingleObject")
	releaseMutexProc = kernel32.NewProc("ReleaseMutex")
	closeHandleProc  = kernel32.NewProc("CloseHandle")
)

// acquireSingleInstance 尝试获得实例独占权；返回 (nil, nil) 表示已有实例在跑。
// 持有的互斥体需在正常退出时 release()。
func acquireSingleInstance() (*singleInstance, error) {
	name, err := syscall.UTF16PtrFromString(singleInstanceMutexName)
	if err != nil {
		return nil, err
	}
	h, _, _ := createMutexProc.Call(0, 0, uintptr(unsafe.Pointer(name)))
	if h == 0 {
		return nil, fmt.Errorf("CreateMutexW(%s) 失败", singleInstanceMutexName)
	}
	ret, _, _ := waitSingleProc.Call(h, 0)
	switch ret {
	case 0x102: // WAIT_TIMEOUT：另一实例正持有互斥体
		closeHandleProc.Call(h)
		return nil, nil
	case 0, 0x80: // WAIT_OBJECT_0 / WAIT_ABANDONED：归本进程
		return &singleInstance{h: h}, nil
	default:
		closeHandleProc.Call(h)
		return nil, fmt.Errorf("WaitForSingleObject 返回 %#x", ret)
	}
}

// release 正常退出时释放；进程被杀时句柄由系统回收，互斥体自动作废给下一个实例。
func (s *singleInstance) release() {
	releaseMutexProc.Call(s.h)
	closeHandleProc.Call(s.h)
}
