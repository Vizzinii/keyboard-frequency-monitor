//go:build windows

package main

import (
	"testing"
	"time"
)

// TestHealthSwapKeepsMutex 回归：publish 构造的副本 mu 为 nil，
// swap 若覆盖真实锁指针，第二次 swap 会空指针崩溃。
func TestHealthSwapKeepsMutex(t *testing.T) {
	h := NewHealth()
	bare := Health{Status: "healed", LastHeal: time.Now()}
	for i := 0; i < 3; i++ {
		h.swap(bare) // 连续 swap，每次都不应 panic
	}
	if got := h.Snapshot().Status; got != "healed" {
		t.Fatalf("status = %q, want healed", got)
	}
}
