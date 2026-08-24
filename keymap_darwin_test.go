package main

import "testing"

// TestCapsLockUsesPhysicalDownBit 回归 review 指出的 bug：
// 曾用 NX_ALPHASHIFTMASK(0x00010000) —— 那是 LED 锁定态而非物理按下位，
// 导致「开 Caps」一次按下计两次（按下与松开时该位都是 1）、
// 「关 Caps」一次都不计（两次都是 0）。
// 现用 NX_DEVICE_ALPHASHIFT_STATELESS_MASK(0x00000080)，语义与其它修饰键一致。
func TestCapsLockUsesPhysicalDownBit(t *testing.T) {
	const caps = 0x39
	const ledOn = 0x00010000 // 锁定态：开灯期间一直为 1

	// 开灯的那次按下：物理位 1 + LED 位 1 -> 计一次
	if !modifierPressed(caps, maskCaps|ledOn) {
		t.Error("开 Caps 的按下应计数")
	}
	// 紧随其后的松开：LED 仍亮但物理位已清零 -> 不能再计
	if modifierPressed(caps, ledOn) {
		t.Error("开 Caps 后的松开不应计数（旧 bug：这里会重复计数）")
	}
	// 关灯的那次按下：LED 位已是 0，但物理位为 1 -> 必须计数
	if !modifierPressed(caps, maskCaps) {
		t.Error("关 Caps 的按下应计数（旧 bug：这里会漏计）")
	}
	// 关灯后的松开：两位都为 0 -> 不计
	if modifierPressed(caps, 0) {
		t.Error("关 Caps 后的松开不应计数")
	}
	if maskCaps != 0x00000080 {
		t.Errorf("maskCaps = 0x%08X，应为 NX_DEVICE_ALPHASHIFT_STATELESS_MASK", maskCaps)
	}
}

func TestModifierPressedDedup(t *testing.T) {
	const (
		lshift = 0x38
		rshift = 0x3C
		lcmd   = 0x37
		fn     = 0x3F
	)
	// 按下：对应位为 1 -> 计数；松开：位清零 -> 不计（否则一次按键计两下）
	if !modifierPressed(lshift, maskLShift) {
		t.Error("按下左 Shift 应计数")
	}
	if modifierPressed(lshift, 0) {
		t.Error("松开左 Shift 不应计数")
	}
	// 左右不能串台
	if modifierPressed(rshift, maskLShift) {
		t.Error("左 Shift 的 flags 不应让右 Shift 计数")
	}
	if !modifierPressed(rshift, maskRShift) {
		t.Error("按下右 Shift 应计数")
	}
	// 组合键：Shift 按着时再按 Cmd，Cmd 自己该计数
	if !modifierPressed(lcmd, maskLShift|maskLCmd) {
		t.Error("Shift 已按下时再按 Cmd，Cmd 应计数")
	}
	// fn 不统计
	if modifierPressed(fn, 0xFFFFFFFF) {
		t.Error("fn 不该计数")
	}
}

// TestDarwinLabelsMatchPanelLayout 锁住热力图需要的键名：
// 两平台的 vkLabel 必须产出同一套词表，否则 dashboard 认不出键位。
func TestDarwinLabelsMatchPanelLayout(t *testing.T) {
	need := []string{
		"esc", "f1", "f12", "`", "1", "0", "-", "=", "backspace",
		"tab", "q", "p", "[", "]", "\\", "caps_lock", "a", "l", ";", "'", "enter",
		"shift", "z", "/", "shift_r", "ctrl", "ctrl_r", "cmd", "cmd_r",
		"alt", "alt_r", "space",
	}
	produced := map[string]bool{}
	for _, l := range darwinVK {
		produced[l] = true
	}
	for _, n := range need {
		if !produced[n] {
			t.Errorf("面板需要 %q，但 darwin 键码表产不出来", n)
		}
	}
}

// mac 最容易踩的坑：kVK_Delete(0x33) 是退格，kVK_ForwardDelete(0x75) 才是 Delete。
func TestDarwinBackspaceVsDelete(t *testing.T) {
	cases := map[uint32]string{
		0x33: "backspace", 0x75: "delete", 0x24: "enter",
		0x00: "a", 0x1D: "0", 0x7A: "f1", 0x52: "num_0",
	}
	for vk, want := range cases {
		if got := vkLabel(vk); got != want {
			t.Errorf("vkLabel(0x%02X) = %q, want %q", vk, got, want)
		}
	}
	if got := vkLabel(0xFF); got != "<vk_255>" {
		t.Errorf("未知键应回退为 <vk_N>, got %q", got)
	}
}

// 键码表是手写的 100+ 条，同一标签由两个键码产出编译器不会报
func TestNoDuplicateDarwinLabels(t *testing.T) {
	seen := map[string]uint32{}
	for vk, label := range darwinVK {
		if prev, dup := seen[label]; dup {
			t.Errorf("标签 %q 同时由 0x%02X 和 0x%02X 产出", label, prev, vk)
		}
		seen[label] = vk
	}
}

func TestMouseLabels(t *testing.T) {
	want := map[uint32]string{
		0: "mouse_left", 1: "mouse_right", 2: "mouse_middle",
		3: "mouse_x1", 4: "mouse_x2", 9: "",
	}
	for btn, exp := range want {
		if got := mouseLabel(btn); got != exp {
			t.Errorf("mouseLabel(%d) = %q, want %q", btn, got, exp)
		}
	}
}
