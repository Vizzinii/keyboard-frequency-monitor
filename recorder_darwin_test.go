package main

// 验证 darwin 看门狗：把 tap 强行停掉，看 Alive() 是否转 false、Reinstall 能否救回。
// 这是 recorder_windows_test.go 的 darwin 对应物（那边发 WM_QUIT 杀消息泵）。

import (
	"testing"
	"time"
)

func TestDarwinTapDeathDetectedAndHealed(t *testing.T) {
	rec := NewRecorder()
	h, err := rec.StartHooks()
	if err != nil {
		t.Skipf("跳过：需要输入监控权限（%v）", err)
	}
	defer h.Stop()

	if !rec.Alive() {
		t.Fatal("刚装好的 tap 应为 Alive")
	}

	// 模拟故障：停掉 run loop，等价于消息泵退出
	h.Stop()

	deadline := time.Now().Add(3 * time.Second)
	for rec.Alive() {
		if time.Now().After(deadline) {
			t.Fatal("tap 已停但 Alive 仍为 true —— 看门狗看不见故障，就是旧版静默失效的病")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// 自愈
	if err := rec.Reinstall(); err != nil {
		t.Fatalf("Reinstall: %v", err)
	}
	if !rec.Alive() {
		t.Fatal("Reinstall 后 tap 仍未恢复")
	}
}
