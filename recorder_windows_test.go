//go:build windows

package main

import (
	"testing"
	"time"
)

// TestReinstallAfterThreadDeath 端到端验证自愈核心：钩子线程退出后
// Alive 能检测到，Reinstall 能把钩子重新救活。
func TestReinstallAfterThreadDeath(t *testing.T) {
	rec := NewRecorder()
	h, err := rec.StartHooks()
	if err != nil {
		t.Fatalf("StartHooks: %v", err)
	}
	defer h.Stop()

	// 模拟故障：向钩子线程的消息泵发 WM_QUIT，使其退出
	hooks.mu.Lock()
	tid := hooks.threadID
	hooks.mu.Unlock()
	if tid == 0 {
		t.Fatal("钩子线程未记录 threadID")
	}
	postThreadMsg.Call(tid, wmQuit, 0, 0)

	deadline := time.Now().Add(3 * time.Second)
	for rec.Alive() {
		if time.Now().After(deadline) {
			t.Fatal("钩子线程未在预期时间内退出")
		}
		time.Sleep(20 * time.Millisecond)
	}

	if err := rec.Reinstall(); err != nil {
		t.Fatalf("Reinstall: %v", err)
	}
	if !rec.Alive() {
		t.Fatal("Reinstall 后钩子仍未恢复")
	}
}
