package main

import "fmt"

// 修饰键的 device-dependent 掩码，取自 SDK 的 IOLLEvent.h（NX_DEVICE*KEYMASK）。
// CGEventFlags 里这些位区分左右，因此不必自己记住上一次状态：
// flags 里对应位为 1 就是“此刻按下”，为 0 就是松开。
const (
	maskLCtrl  = 0x00000001
	maskLShift = 0x00000002
	maskRShift = 0x00000004
	maskLCmd   = 0x00000008
	maskRCmd   = 0x00000010
	maskLAlt   = 0x00000020
	maskRAlt   = 0x00000040
	maskRCtrl  = 0x00002000
	// Caps Lock 必须用 STATELESS（物理按下）位，不能用 NX_ALPHASHIFTMASK：
	// 后者是 LED 锁定态，开灯后按键松开时仍为 1（一次按下计两次），
	// 而关灯的那次按下它是 0（漏计）。STATELESS 位与其它修饰键语义一致。
	maskCaps = 0x00000080 // NX_DEVICE_ALPHASHIFT_STATELESS_MASK
)

// modifierPressed 判断这次 flagsChanged 是“按下”而非“松开”。
// 少了这一步每按一次 Shift 会计成 2 次。
func modifierPressed(keycode uint32, flags uint64) bool {
	var mask uint64
	switch keycode {
	case 0x3B:
		mask = maskLCtrl
	case 0x3E:
		mask = maskRCtrl
	case 0x38:
		mask = maskLShift
	case 0x3C:
		mask = maskRShift
	case 0x37:
		mask = maskLCmd
	case 0x36:
		mask = maskRCmd
	case 0x3A:
		mask = maskLAlt
	case 0x3D:
		mask = maskRAlt
	case 0x39:
		mask = maskCaps
	default:
		return false // fn 等不统计
	}
	return flags&mask != 0
}

// vkLabel 把 macOS 的 CGKeyCode 转成统计用的统一键名。
// 输出词表必须与 keymap_windows.go 完全一致，否则 dashboard.html 的热力图认不出来。
// 同样一律按物理键统计（Shift+A 计入 a）。
func vkLabel(vk uint32) string {
	if s, ok := darwinVK[vk]; ok {
		return s
	}
	return fmt.Sprintf("<vk_%d>", vk)
}

// 键码取自 Carbon Events.h 的 kVK_* 常量。
// 注意 mac 的 kVK_Delete(0x33) 是退格键，kVK_ForwardDelete(0x75) 才是 Delete。
var darwinVK = map[uint32]string{
	// 字母（mac 键码不连续，只能逐个列）
	0x00: "a", 0x0B: "b", 0x08: "c", 0x02: "d", 0x0E: "e", 0x03: "f",
	0x05: "g", 0x04: "h", 0x22: "i", 0x26: "j", 0x28: "k", 0x25: "l",
	0x2E: "m", 0x2D: "n", 0x1F: "o", 0x23: "p", 0x0C: "q", 0x0F: "r",
	0x01: "s", 0x11: "t", 0x20: "u", 0x09: "v", 0x0D: "w", 0x07: "x",
	0x10: "y", 0x06: "z",

	// 主键盘数字
	0x1D: "0", 0x12: "1", 0x13: "2", 0x14: "3", 0x15: "4",
	0x17: "5", 0x16: "6", 0x1A: "7", 0x1C: "8", 0x19: "9",

	// 符号
	0x18: "=", 0x1B: "-", 0x1E: "]", 0x21: "[", 0x27: "'",
	0x29: ";", 0x2A: "\\", 0x2B: ",", 0x2C: "/", 0x2F: ".", 0x32: "`",

	// 主要控制键
	0x24: "enter", 0x30: "tab", 0x31: "space", 0x33: "backspace",
	0x35: "esc", 0x39: "caps_lock", 0x75: "delete",
	0x6E: "menu", 0x72: "help",

	// 修饰键（左右分开，与 Windows 版命名对齐）
	0x37: "cmd", 0x36: "cmd_r",
	0x38: "shift", 0x3C: "shift_r",
	0x3A: "alt", 0x3D: "alt_r",
	0x3B: "ctrl", 0x3E: "ctrl_r",

	// 方向与翻页
	0x7B: "left", 0x7C: "right", 0x7D: "down", 0x7E: "up",
	0x73: "home", 0x77: "end", 0x74: "page_up", 0x79: "page_down",

	// 功能键
	0x7A: "f1", 0x78: "f2", 0x63: "f3", 0x76: "f4", 0x60: "f5", 0x61: "f6",
	0x62: "f7", 0x64: "f8", 0x65: "f9", 0x6D: "f10", 0x67: "f11", 0x6F: "f12",
	0x69: "f13", 0x6B: "f14", 0x71: "f15", 0x6A: "f16", 0x40: "f17",
	0x4F: "f18", 0x50: "f19", 0x5A: "f20",

	// 小键盘
	0x52: "num_0", 0x53: "num_1", 0x54: "num_2", 0x55: "num_3", 0x56: "num_4",
	0x57: "num_5", 0x58: "num_6", 0x59: "num_7", 0x5B: "num_8", 0x5C: "num_9",
	0x41: "num_.", 0x43: "num_*", 0x45: "num_+", 0x4B: "num_/",
	0x4E: "num_-", 0x51: "num_=", 0x4C: "num_enter", 0x47: "num_clear",

	// 媒体键
	0x48: "volume_up", 0x49: "volume_down", 0x4A: "volume_mute",
}
