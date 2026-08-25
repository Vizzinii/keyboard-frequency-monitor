package main

import "fmt"

// vkLabel 把 Windows 虚拟键码转成统计用的统一键名。
// 输出词表必须与 keymap_darwin.go 完全一致，否则 dashboard.html 的热力图认不出来。
// 与网页热力图的命名保持一致；一律按物理键统计（Shift+A 计入 a，
// 这样才能反映“哪个键敲得多”，也避免 shifted 符号散落到布局之外）。
func vkLabel(vk uint32) string {
	switch {
	case vk >= 0x41 && vk <= 0x5A: // A-Z，无论大小写都归为小写字母
		return string(rune('a' + vk - 0x41))
	case vk >= 0x30 && vk <= 0x39: // 主键盘数字
		return string(rune(vk))
	case vk >= 0x60 && vk <= 0x69: // 小键盘数字
		return "num_" + string(rune('0'+vk-0x60))
	case vk >= 0x70 && vk <= 0x87: // F1-F24
		return fmt.Sprintf("f%d", vk-0x70+1)
	}
	if s, ok := specialVK[vk]; ok {
		return s
	}
	return fmt.Sprintf("<vk_%d>", vk)
}

var specialVK = map[uint32]string{
	0x08: "backspace", 0x09: "tab", 0x0D: "enter", 0x13: "pause",
	0x14: "caps_lock", 0x1B: "esc", 0x20: "space",
	0x21: "page_up", 0x22: "page_down", 0x23: "end", 0x24: "home",
	0x25: "left", 0x26: "up", 0x27: "right", 0x28: "down",
	0x2C: "print_screen", 0x2D: "insert", 0x2E: "delete",
	0x5B: "cmd", 0x5C: "cmd_r", 0x5D: "menu",
	0x90: "num_lock", 0x91: "scroll_lock",
	0xA0: "shift", 0xA1: "shift_r", 0xA2: "ctrl", 0xA3: "ctrl_r",
	0xA4: "alt", 0xA5: "alt_r",
	0xA6: "browser_back", 0xA7: "browser_forward",
	0xAA: "browser_search", 0xAB: "browser_favorites", 0xAC: "browser_home",
	0xAD: "volume_mute", 0xAE: "volume_down", 0xAF: "volume_up",
	0xB0: "media_next", 0xB1: "media_prev", 0xB2: "media_stop", 0xB3: "media_play",
	0xBA: ";", 0xBB: "=", 0xBC: ",", 0xBD: "-", 0xBE: ".", 0xBF: "/", 0xC0: "`",
	0xDB: "[", 0xDC: "\\", 0xDD: "]", 0xDE: "'",
	0x6A: "num_*", 0x6B: "num_+", 0x6D: "num_-", 0x6E: "num_.", 0x6F: "num_/",
}
