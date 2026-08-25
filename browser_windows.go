package main

import (
	"os/exec"
	"syscall"
)

// openInBrowser 用系统默认浏览器打开面板；
// rundll32 方式不会闪黑框，也不会把父进程绑在浏览器上。
func openInBrowser(url string) {
	cmd := exec.Command("rundll32", "url.dll,FileProtocolHandler", url)
	cmd.SysProcAttr = &syscall.SysProcAttr{HideWindow: true}
	_ = cmd.Start()
}
