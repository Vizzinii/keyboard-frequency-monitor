package main

import (
	"time"
	"unsafe"
)

var (
	getLastInputInfo = user32.NewProc("GetLastInputInfo")
	getTickCount64P  = kernel32.NewProc("GetTickCount64")
)

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

// lastInputAgo 返回系统范围内最近一次用户输入距今的时间。
// dwTime 是 32 位 tick，uint32 减法天然处理回绕；调用失败时按"很久以前"处理（视为空闲）。
func lastInputAgo() time.Duration {
	var lii lastInputInfo
	lii.cbSize = uint32(unsafe.Sizeof(lii))
	ret, _, _ := getLastInputInfo.Call(uintptr(unsafe.Pointer(&lii)))
	if ret == 0 {
		return time.Hour
	}
	now, _, _ := getTickCount64P.Call()
	return time.Duration(uint32(now)-lii.dwTime) * time.Millisecond
}
