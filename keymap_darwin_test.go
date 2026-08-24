package main

import "testing"

// TestCapsLockCountsOnLEDEdge 用真机实测数据锁住 Caps Lock 的计数方式。
//
// 观测记录（Apple 内置键盘 + CGEventTap，按 Caps Lock 三次）：
//
//	开 -> flags=0x00010100  LED=1  INDEP_SL=0  DEV_SL=0
//	关 -> flags=0x00000100  LED=0  INDEP_SL=0  DEV_SL=0
//	开 -> flags=0x00010100 ...（严格交替）
//
// 两个 STATELESS 位头文件里有、系统却从不设置，所以不能用它们判断按下
// （曾用 0x00000080，结果永远为假 —— Caps Lock 一次都不计）。
// LED 位每次物理按下翻转一次，因此按翻转沿计数。
func TestCapsLockCountsOnLEDEdge(t *testing.T) {
	const caps = 0x39
	const ledOn = 0x00010100  // 实测：开灯事件
	const ledOff = 0x00000100 // 实测：关灯事件（0x100 是 NonCoalesced 基线位）

	capsLED.Store(false) // 从"灯灭"起算

	// 连按三次：每次都应恰好计一次
	for i, flags := range []uint64{ledOn, ledOff, ledOn} {
		if !modifierPressed(caps, flags) {
			t.Errorf("第 %d 次按 Caps Lock 应计数（flags=0x%08X）", i+1, flags)
		}
	}

	// 同一状态重复上报（休眠唤醒、多键盘同步）不该重复计数
	if modifierPressed(caps, ledOn) {
		t.Error("锁定态未变化时不应计数")
	}

	// 若系统某天改成按下/松开各发一次事件，翻转沿计数依然只计一次：
	// 一对事件里 LED 保持不变，第二次被上面这条规则挡掉。
}

// TestCapsLockRegressionStatelessBitsUnused 固定住"不能用 STATELESS 位"这个教训。
func TestCapsLockRegressionStatelessBitsUnused(t *testing.T) {
	const caps = 0x39
	capsLED.Store(false)
	// 实测的开灯 flags 里，两个 STATELESS 位都是 0。
	// 若有人把判定改回读这些位，这里会失败。
	const measuredOn = 0x00010100
	if measuredOn&0x00000080 != 0 || measuredOn&0x01000000 != 0 {
		t.Fatal("测试数据写错了：实测 flags 里 STATELESS 位应为 0")
	}
	if !modifierPressed(caps, measuredOn) {
		t.Error("按实测 flags 应能计数 —— 说明判定又依赖了系统不设置的位")
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
	// 实测数据：左 Shift 按下 flags=0x00020102，松开 0x00000100
	capsLED.Store(false)
	if !modifierPressed(lshift, 0x00020102) {
		t.Error("实测的左 Shift 按下 flags 应计数")
	}
	if modifierPressed(lshift, 0x00000100) {
		t.Error("实测的左 Shift 松开 flags 不应计数")
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
