package main

import (
	"log"
	"time"

	"github.com/energye/systray"
)

// runTray 阻塞运行托盘/菜单栏图标，直到用户点击“退出”。
// 左键单击图标直接打开面板；右键弹出菜单。
// 图标字节由 icon_windows.go / icon_darwin.go 提供（mac 的 NSImage 不吃 ICO）。
func runTray(panelURL string, rec *Recorder, health *Health) {
	systray.Run(func() {
		systray.SetIcon(trayIcon)
		systray.SetTooltip("键盘频率监视器")
		systray.SetOnClick(func(_ systray.IMenu) {
			openInBrowser(panelURL)
		})

		mStatus := systray.AddMenuItem("记录：正常", "钩子与统计状态")
		mStatus.Disable()
		systray.AddSeparator()
		mOpen := systray.AddMenuItem("打开面板", "在浏览器中查看统计")
		mPause := systray.AddMenuItem("暂停记录", "暂停/恢复统计（已存数据不受影响）")
		mReinstall := systray.AddMenuItem("重新安装钩子", "手动重装钩子（看门狗自愈用）")
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

		mReinstall.Click(func() {
			log.Println("[手动] 用户请求重新安装钩子")
			if err := rec.Reinstall(); err != nil {
				log.Println("[手动] 重装钩子失败:", err)
			}
		})

		mQuit.Click(systray.Quit)

		go func() {
			titles := map[string]string{
				"ok":       "记录：正常",
				"healed":   "记录：正常（曾自愈）",
				"paused":   "记录：已暂停",
				"degraded": "记录：异常（需手动重启）",
			}
			for {
				h := health.Snapshot()
				title := titles[h.Status]
				if title == "" {
					title = "记录：正常"
				}
				mStatus.SetTitle(title)
				systray.SetTooltip("键盘频率监视器 · " + title)
				time.Sleep(time.Second)
			}
		}()
	}, func() {})
}
