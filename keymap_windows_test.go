package main

import "testing"

func TestVKLabelLettersAndDigits(t *testing.T) {
	cases := map[uint32]string{
		0x41: "a", 0x5A: "z", // 字母一律小写
		0x30: "0", 0x39: "9",
		0x60: "num_0", 0x69: "num_9",
		0x70: "f1", 0x87: "f24",
	}
	for vk, want := range cases {
		if got := vkLabel(vk); got != want {
			t.Errorf("vkLabel(0x%X) = %q, want %q", vk, got, want)
		}
	}
}

func TestVKLabelSpecials(t *testing.T) {
	cases := map[uint32]string{
		0x20: "space", 0x0D: "enter", 0x09: "tab", 0x1B: "esc",
		0x08: "backspace", 0x14: "caps_lock",
		0xA0: "shift", 0xA1: "shift_r",
		0xA2: "ctrl", 0xA3: "ctrl_r",
		0xA4: "alt", 0xA5: "alt_r",
		0x5B: "cmd", 0x5C: "cmd_r", 0x5D: "menu",
		0x25: "left", 0x26: "up", 0x27: "right", 0x28: "down",
		0xBA: ";", 0xBB: "=", 0xBC: ",", 0xBD: "-", 0xBE: ".", 0xBF: "/",
		0xC0: "`", 0xDB: "[", 0xDC: "\\", 0xDD: "]", 0xDE: "'",
		0x6A: "num_*", 0x6E: "num_.", 0x6F: "num_/",
	}
	for vk, want := range cases {
		if got := vkLabel(vk); got != want {
			t.Errorf("vkLabel(0x%X) = %q, want %q", vk, got, want)
		}
	}
}

func TestVKLabelUnknown(t *testing.T) {
	if got := vkLabel(0xFF); got != "<vk_255>" {
		t.Errorf("未知键应回退为 <vk_N> 形式, got %q", got)
	}
}
