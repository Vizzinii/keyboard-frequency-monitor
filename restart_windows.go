package main

import (
	"os/exec"
	"syscall"

	"golang.org/x/sys/windows"
)

// detach 让重启拉起的新实例独立于本进程：新控制台，旧进程退出不带走它。
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{CreationFlags: windows.CREATE_NEW_CONSOLE}
}
