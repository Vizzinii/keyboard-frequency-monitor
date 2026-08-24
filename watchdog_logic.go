package main

import "time"

// needsReinstall 判定钩子是否失效（纯函数，便于单测）：
//   - activityAgo：我方钩子最后一次"看到输入"距今
//   - systemAgo ：系统范围内最后一次用户输入距今（GetLastInputInfo，不依赖我们的钩子）
//   - win        ：判定窗口
//
// 钩子长时间收不到任何输入、而系统却一直在收到输入（且未暂停）→ 钩子失效。
// 用户真正空闲时两边都静默，不会误判。
func needsReinstall(activityAgo, systemAgo, win time.Duration, paused bool) bool {
	return !paused && activityAgo > win && systemAgo < win
}
