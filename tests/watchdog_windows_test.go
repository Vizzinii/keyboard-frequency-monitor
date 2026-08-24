//go:build windows

package main

import (
	"sync"
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

// TestPublishStatus 覆盖 publish 的状态机：ok / paused / degraded 三种状态发布正确。
func TestPublishStatus(t *testing.T) {
	rec := NewRecorder()
	h := NewHealth()
	w := NewWatchdog(rec, h, 60*time.Second, time.Time{})
	now := time.Now()

	w.publish(now, now, now, time.Second, false)
	if s := h.Snapshot().Status; s != "ok" {
		t.Fatalf("初始状态 = %q, want ok", s)
	}
	w.publish(now, now, now, time.Second, true)
	if s := h.Snapshot().Status; s != "paused" {
		t.Fatalf("暂停状态 = %q, want paused", s)
	}
	w.mu.Lock()
	w.manualRequired = true
	w.mu.Unlock()
	w.publish(now, now, now, time.Second, false)
	if s := h.Snapshot().Status; s != "degraded" {
		t.Fatalf("降级状态 = %q, want degraded", s)
	}
}

// fakeClock 是 Watchdog.now 的注入时钟，可按测试需要推进。
type fakeClock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *fakeClock) now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.t
}

func (c *fakeClock) advance(d time.Duration) {
	c.mu.Lock()
	c.t = c.t.Add(d)
	c.mu.Unlock()
}

// newTestWatchdog 构造一个不碰真实钩子的看门狗（假安装实现 + 注入时钟）。
func newTestWatchdog(t *testing.T) (*Watchdog, *fakeClock) {
	t.Helper()
	rec := NewRecorder()
	rec.setHookFn = func(int, uintptr) (uintptr, error) { return 0x1234, nil }
	rec.unhookFn = func(uintptr) {}
	clock := &fakeClock{t: time.Now()}
	w := NewWatchdog(rec, NewHealth(), 60*time.Second, time.Time{})
	w.now = clock.now
	return w, clock
}

// TestHealThrottle 两次自愈间隔小于 minHealGap 时，第二次被节流跳过。
func TestHealThrottle(t *testing.T) {
	w, clock := newTestWatchdog(t)
	restarts := 0
	onRestart := func() { restarts++ }

	w.heal(clock.now(), "第一次", onRestart)
	w.heal(clock.now(), "紧接着第二次", onRestart) // 间隔 0 < 10s，应被节流

	w.mu.Lock()
	n := len(w.healTimes)
	w.mu.Unlock()
	if n != 1 {
		t.Fatalf("节流失败：healTimes = %d, want 1", n)
	}
	if restarts != 0 {
		t.Fatal("节流期间不应触发重启")
	}
}

// TestHealEscalation 5 分钟内 3 次自愈 → 触发一次自动重启 → 之后进入 manualRequired。
func TestHealEscalation(t *testing.T) {
	w, clock := newTestWatchdog(t)
	restarts := 0
	onRestart := func() { restarts++ }

	for i := 0; i < 3; i++ {
		w.heal(clock.now(), "自愈", onRestart)
		clock.advance(11 * time.Second) // 超过 minHealGap，仍在 escalateWin 内
	}
	if restarts != 1 {
		t.Fatalf("3 次自愈后应触发一次重启, got %d", restarts)
	}

	// manualRequired 后（冷却或拉起失败）：再自愈也不自动重启
	clock.advance(61 * time.Second) // 越过 manualRequired 下的 1min 节流
	w.heal(clock.now(), "再自愈", onRestart)
	if restarts != 1 {
		t.Fatalf("manualRequired 后不应再自动重启, got %d", restarts)
	}
	w.mu.Lock()
	mr := w.manualRequired
	w.mu.Unlock()
	if !mr {
		t.Fatal("应进入 manualRequired 状态")
	}
}

// TestHealCooldown 距上次自动重启不足 escalateWin 时，升级只置 manualRequired、不重启。
func TestHealCooldown(t *testing.T) {
	rec := NewRecorder()
	rec.setHookFn = func(int, uintptr) (uintptr, error) { return 0x1234, nil }
	rec.unhookFn = func(uintptr) {}
	clock := &fakeClock{t: time.Now()}
	w := NewWatchdog(rec, NewHealth(), 60*time.Second, clock.now()) // restartAt=现在 → 冷却期内
	w.now = clock.now

	restarts := 0
	onRestart := func() { restarts++ }
	for i := 0; i < 3; i++ {
		w.heal(clock.now(), "自愈", onRestart)
		clock.advance(11 * time.Second)
	}
	if restarts != 0 {
		t.Fatalf("冷却期内不应自动重启, got %d", restarts)
	}
	w.mu.Lock()
	mr := w.manualRequired
	w.mu.Unlock()
	if !mr {
		t.Fatal("冷却期内升级应转入 manualRequired")
	}
}

// TestPublishHealed 自愈后 10 分钟内发布 healed 状态。
func TestPublishHealed(t *testing.T) {
	w, clock := newTestWatchdog(t)
	now := clock.now()
	w.mu.Lock()
	w.lastHealAt = now.Add(-time.Minute)
	w.mu.Unlock()
	w.publish(now, now, now, time.Second, false)
	if s := w.health.Snapshot().Status; s != "healed" {
		t.Fatalf("状态 = %q, want healed", s)
	}
}
