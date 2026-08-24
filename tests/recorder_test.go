//go:build windows

package main

import (
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"
)

// TestReinstallAfterThreadDeath 端到端验证自愈核心：钩子线程退出后
// Alive 能检测到，Reinstall 能把钩子重新救活。
func TestReinstallAfterThreadDeath(t *testing.T) {
	rec := NewRecorder()
	hooks, err := rec.StartHooks()
	if err != nil {
		t.Fatalf("StartHooks: %v", err)
	}
	defer hooks.Stop()

	// 模拟故障：向钩子线程的消息泵发 WM_QUIT，使其退出
	rec.mu.Lock()
	tid := rec.threadID
	rec.mu.Unlock()
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

// TestDrainConcurrent 回归：Drain 被多个 goroutine（每秒 flushLoop + 退出收尾）
// 并发调用时，事件既不丢失也不重复。旧实现（先读 len 再逐个取）会让另一方
// 永久阻塞，测试直接超时。
func TestDrainConcurrent(t *testing.T) {
	r := NewRecorder()
	const total = 5000
	for i := 0; i < total; i++ {
		r.ch <- fmt.Sprintf("k%d", i%10)
	}
	const workers = 8
	got := make([]map[string]int, workers)
	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			for j := 0; j < 200; j++ {
				got[idx] = mergeCounts(got[idx], r.Drain())
			}
		}(w)
	}
	wg.Wait()

	sum := 0
	perKey := make(map[string]int)
	for _, m := range got {
		for k, v := range m {
			perKey[k] += v
			sum += v
		}
	}
	if sum != total {
		t.Fatalf("并发 Drain 事件丢失/重复：sum=%d, want %d", sum, total)
	}
	for i := 0; i < 10; i++ {
		if perKey[fmt.Sprintf("k%d", i)] != total/10 {
			t.Fatalf("k%d = %d, want %d", i, perKey[fmt.Sprintf("k%d", i)], total/10)
		}
	}
}

func mergeCounts(a, b map[string]int) map[string]int {
	if a == nil {
		return b
	}
	for k, v := range b {
		a[k] += v
	}
	return a
}

// TestFlushOnceRetainsOnFailure 回归：落盘失败时事件应暂存下轮重试，
// 而不是在写入尝试前就被丢弃。
func TestFlushOnceRetainsOnFailure(t *testing.T) {
	s := newTestStore(t)
	rec := NewRecorder()
	rec.ch <- "a"
	rec.ch <- "space"
	rec.ch <- "a"

	s.Close() // 注入失败：库已关闭，Add 必然报错
	flushOnce(s, rec)
	if !rec.HasPending() {
		t.Fatal("写失败后应有暂存事件")
	}

	s2 := newTestStore(t) // 健康库：下一轮 flushOnce 应把暂存连同新事件一起落库
	rec.ch <- "b"
	flushOnce(s2, rec)
	if rec.HasPending() {
		t.Fatal("重试成功后暂存应清空")
	}
	keys, err := s2.Keys("")
	if err != nil {
		t.Fatal(err)
	}
	total := int64(0)
	for _, kc := range keys {
		total += kc.Count
	}
	if total != 4 { // a×2 + space + b
		t.Fatalf("total=%d, want 4", total)
	}
}

// TestInstallTimeoutAbort 回归（P1 修复）：install 超时后，迟到的"成功"完成
// 必须自查代际号作废——不发布句柄、自卸钩退出，绝不留下与后续安装并存的孤儿钩子。
// 通过假 setHookFn 模拟"键盘钩子卡住直到越过 3s 超时，然后才返回成功"。
func TestInstallTimeoutAbort(t *testing.T) {
	rec := NewRecorder()
	unhooked := make(chan uintptr, 4)
	rec.setHookFn = func(hookType int, cb uintptr) (uintptr, error) {
		if hookType == whKeyboardLL {
			time.Sleep(4 * time.Second) // 卡住直到超过 install 的 3s 超时
		}
		return 0x1111 + uintptr(hookType), nil
	}
	rec.unhookFn = func(h uintptr) { unhooked <- h }

	start := time.Now()
	err := rec.install()
	if err == nil || !strings.Contains(err.Error(), "超时") {
		t.Fatalf("应返回超时错误, got %v", err)
	}
	if time.Since(start) < 3*time.Second {
		t.Fatal("超时不应提前返回")
	}

	// 等迟到的 goroutine 恢复：自查作废 → 卸掉自己的两个钩子 → 退出
	deadline := time.Now().Add(3 * time.Second)
	var got []uintptr
	for len(got) < 2 && time.Now().Before(deadline) {
		select {
		case h := <-unhooked:
			got = append(got, h)
		case <-time.After(50 * time.Millisecond):
		}
	}
	if len(got) != 2 {
		t.Fatalf("迟到的安装应自卸两个钩子, got %d", len(got))
	}
	// 句柄从未发布：Alive 为 false，可再次正常安装
	if rec.Alive() {
		t.Fatal("作废的安装不应留下存活状态")
	}
}

// TestConcurrentReinstall 并发重装（托盘手动 + 看门狗同时触发）由代际号仲裁，
// 收敛到单代存活钩子，且之后能正常停止。
// 注：用假安装实现（立即返回假句柄）而非真实钩子——真实系统对并发低级钩子安装
// 存在偶发阻塞（正是本程序看门狗要应对的场景），真钩子的串行自愈由
// TestReinstallAfterThreadDeath 覆盖。
func TestConcurrentReinstall(t *testing.T) {
	rec := NewRecorder()
	rec.setHookFn = func(int, uintptr) (uintptr, error) { return 0x1234, nil }
	rec.unhookFn = func(uintptr) {}

	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := rec.Reinstall(); err != nil && !strings.Contains(err.Error(), "取代") {
				t.Error(err)
			}
		}()
	}
	wg.Wait()

	if !rec.Alive() {
		t.Fatal("并发重装后应有存活状态")
	}
	// 停止：卸钩 + WM_QUIT，消息泵线程退出后 Alive 变 false
	rec.stopLocked()
	deadline := time.Now().Add(2 * time.Second)
	for rec.Alive() {
		if time.Now().After(deadline) {
			t.Fatal("停止后消息泵线程未退出")
		}
		time.Sleep(20 * time.Millisecond)
	}
}
