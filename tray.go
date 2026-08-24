package main

import "github.com/energye/systray"

// runTray 阻塞运行托盘/菜单栏图标，直到用户点击“退出”。
// 左键单击图标直接打开面板；右键弹出菜单。
// 图标字节由 icon_windows.go / icon_darwin.go 提供（mac 的 NSImage 不吃 ICO）。
func runTray(panelURL string, rec *Recorder) {
	systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("键盘频率监视器")
		systray.SetOnClick(func(_ systray.IMenu) {
			openInBrowser(panelURL)
		})

		mOpen := systray.AddMenuItem("打开面板", "在浏览器中查看统计")
		mPause := systray.AddMenuItem("暂停记录", "暂停/恢复统计（已存数据不受影响）")
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

		mQuit.Click(systray.Quit)
	}, func() {})
}
