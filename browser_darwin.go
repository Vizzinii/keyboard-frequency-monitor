package main

import "os/exec"

// openInBrowser 用系统默认浏览器打开面板。
func openInBrowser(url string) {
	_ = exec.Command("open", url).Start()
}
