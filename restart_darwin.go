package main

import (
	"os/exec"
	"syscall"
)

// detach 让重启拉起的新实例独立于本进程：新会话，父进程退出不带走它。
func detach(cmd *exec.Cmd) {
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
}
