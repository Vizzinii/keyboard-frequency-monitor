package main

import (
	"testing"
	"time"
)

func TestNeedsReinstall(t *testing.T) {
	win := 60 * time.Second
	cases := []struct {
		name                   string
		activityAgo, systemAgo time.Duration
		paused                 bool
		want                   bool
	}{
		{"用户空闲：钩子无事件且系统也无输入", 5 * time.Minute, 5 * time.Minute, false, false},
		{"钩子正常：刚有事件", 2 * time.Second, 2 * time.Second, false, false},
		{"钩子正常：只有鼠标移动也算看到输入", 3 * time.Second, 1 * time.Second, false, false},
		{"钩子失效：无事件但系统一直在输入", 2 * time.Minute, 2 * time.Second, false, true},
		{"暂停时不判定", 2 * time.Minute, 2 * time.Second, true, false},
		{"边界：活动恰好等于窗口不触发", win, 2 * time.Second, false, false},
		{"边界：系统输入恰好到窗口不触发", 2 * time.Minute, win, false, false},
	}
	for _, c := range cases {
		if got := needsReinstall(c.activityAgo, c.systemAgo, win, c.paused); got != c.want {
			t.Errorf("%s: needsReinstall = %v, want %v", c.name, got, c.want)
		}
	}
}

// TestHealthSwapKeepsMutex 回归：publish 构造的副本 mu 为 nil，
// swap 若覆盖真实锁指针，第二次 swap 会空指针崩溃。
func TestHealthSwapKeepsMutex(t *testing.T) {
	h := NewHealth()
	bare := Health{Status: "healed", LastHeal: time.Now()}
	for range 3 {
		h.swap(bare) // 连续 swap，每次都不应 panic
	}
	if got := h.Snapshot().Status; got != "healed" {
		t.Fatalf("status = %q, want healed", got)
	}
}
