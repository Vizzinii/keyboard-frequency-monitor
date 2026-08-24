//go:build windows

package main

import (
	_ "embed"
	"fmt"
	"syscall"

	"github.com/energye/systray"
	"golang.org/x/sys/windows/registry"
)

//go:embed assets/icon.ico
var iconICO []byte

const (
	runKeyPath   = `Software\Microsoft\Windows\CurrentVersion\Run`
	runValueName = "KeyboardFrequencyMonitor"
)

// runTray 阻塞运行托盘，直到用户点击“退出”。
// 左键单击图标直接打开面板；右键弹出菜单。
func runTray(panelURL string, rec *Recorder, exePath string) {
	systray.Run(func() {
		systray.SetIcon(iconICO)
		systray.SetTooltip("键盘频率监视器")
		systray.SetOnClick(func(_ systray.IMenu) {
			openInBrowser(panelURL)
		})

		mOpen := systray.AddMenuItem("打开面板", "在浏览器中查看统计")
		mPause := systray.AddMenuItem("暂停记录", "暂停/恢复统计（已存数据不受影响）")
		mAuto := systray.AddMenuItemCheckbox("开机自启", "登录 Windows 后自动开始记录", autostartEnabled())
		systray.AddSeparator()
		mQuit := systray.AddMenuItem("退出", "停止记录并退出")

		mOpen.Click(func() { openInBrowser(panelURL) })

		mPause.Click(func() {
			p := !rec.Paused()
			rec.SetPaused(p)
			if p {
				mPause.SetTitle("恢复记录")
			} else {
				mPause.SetTitle("暂停记录")
			}
		})

		mAuto.Click(func() {
			on := !autostartEnabled()
			if err := setAutostart(on, exePath); err != nil {
				fmt.Println("[警告] 修改开机自启失败:", err)
				return
			}
			if on {
				mAuto.Check()
			} else {
				mAuto.Uncheck()
			}
		})

		mQuit.Click(systray.Quit)
	}, func() {})
}

func autostartEnabled() bool {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath, registry.QUERY_VALUE)
	if err != nil {
		return false
	}
	defer k.Close()
	_, _, err = k.GetStringValue(runValueName)
	return err == nil
}

func setAutostart(on bool, exePath string) error {
	k, err := registry.OpenKey(registry.CURRENT_USER, runKeyPath,
		registry.QUERY_VALUE|registry.SET_VALUE)
	if err != nil {
		return err
	}
	defer k.Close()
	if on {
		return k.SetStringValue(runValueName, `"`+exePath+`"`)
	}
	if err := k.DeleteValue(runValueName); err != nil && err != syscall.ERROR_FILE_NOT_FOUND {
		return err
	}
	return nil
}
