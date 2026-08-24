package main

import (
	"fmt"
	"os"
	"regexp"
	"sort"
	"strings"
	"testing"
)

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

// TestVKLabelFullDomain 全 VK 域回归：0-255 任何虚拟键码都返回非空规范名且不 panic。
func TestVKLabelFullDomain(t *testing.T) {
	for vk := uint32(0); vk <= 255; vk++ {
		if lbl := vkLabel(vk); lbl == "" {
			t.Fatalf("vkLabel(0x%02X) 返回空字符串", vk)
		}
	}
}

// TestLayoutNamesReachable 前端/后端契约：dashboard.html 键位布局里出现的所有统计键名，
// 都必须能被 vkLabel 从某个虚拟键码产生——防止"新增了映射却漏画"或"画了却永远计不到"。
func TestLayoutNamesReachable(t *testing.T) {
	html, err := os.ReadFile("dashboard.html")
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`K\("([^"]*)","([^"]*)"`)
	names := map[string]bool{}
	for _, m := range re.FindAllStringSubmatch(string(html), -1) {
		if m[2] != "" {
			// JS 字符串源码里的 \\ 求值后是单个反斜杠（如 K("\\","\\")）
			names[strings.ReplaceAll(m[2], `\\`, `\`)] = true
		}
	}
	// 布局中以 .map 动态生成的键名：字母、数字、F 键
	for c := 'a'; c <= 'z'; c++ {
		names[string(c)] = true
	}
	for c := '0'; c <= '9'; c++ {
		names[string(c)] = true
	}
	for i := 1; i <= 24; i++ {
		names[fmt.Sprintf("f%d", i)] = true
	}

	reachable := map[string]bool{
		"mouse_left": true, "mouse_right": true, "mouse_middle": true,
		"mouse_x1": true, "mouse_x2": true,
	}
	for vk := uint32(0); vk <= 255; vk++ {
		reachable[vkLabel(vk)] = true
	}
	var missing []string
	for n := range names {
		if !reachable[n] {
			missing = append(missing, n)
		}
	}
	if len(missing) > 0 {
		sort.Strings(missing)
		t.Fatalf("布局中 %v 无法由 vkLabel 产生（漏映射或漏画）", missing)
	}
}
