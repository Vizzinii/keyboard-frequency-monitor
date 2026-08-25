package main

import (
	"sync"
	"sync/atomic"
	"time"
)

// eventBuffer 溢出直接丢弃，绝不能拖慢系统输入。
const eventBuffer = 16384

// Recorder 是跨平台的事件缓冲：平台相关的钩子只负责调用 emit/touch，
// 具体安装/卸载见 recorder_windows.go / recorder_darwin.go 的 StartHooks。
type Recorder struct {
	ch     chan string
	paused atomic.Bool
	// lastEvent 是"有事件落盘"的时间；lastActivity 是钩子最近一次看到输入的时间
	// （由回调直接写，不加锁）。两者都给看门狗判定用。
	lastEvent    atomic.Int64
	lastActivity atomic.Int64

	drainMu sync.Mutex // 串行化 Drain，防止退出收尾与 flushLoop 并发争抢事件

	pendingMu sync.Mutex
	pending   map[string]int // 落盘失败暂存的事件，下轮重试
}

func NewRecorder() *Recorder {
	r := &Recorder{ch: make(chan string, eventBuffer)}
	now := time.Now().Unix()
	r.lastEvent.Store(now)
	r.lastActivity.Store(now)
	return r
}

func (r *Recorder) SetPaused(p bool) { r.paused.Store(p) }
func (r *Recorder) Paused() bool     { return r.paused.Load() }

func (r *Recorder) LastEvent() time.Time    { return time.Unix(r.lastEvent.Load(), 0) }
func (r *Recorder) LastActivity() time.Time { return time.Unix(r.lastActivity.Load(), 0) }
func (r *Recorder) ChannelDepth() int       { return len(r.ch) }

// touch 由平台回调在"看到任何输入"时调用（含未被统计的键、鼠标移动）。
// 与 emit 分开是因为看门狗要判断的是"钩子还活着吗"，而不是"有没有产生统计"：
// 暂停期间钩子依然应该被认定为存活。
func (r *Recorder) touch() { r.lastActivity.Store(time.Now().Unix()) }

// isClosed 判断某一代钩子/tap 的存活 channel 是否已关闭（两平台共用）。
func isClosed(ch chan struct{}) bool {
	select {
	case <-ch:
		return true
	default:
		return false
	}
}

// emit 在钩子回调里被调用，必须立刻返回：缓冲满时丢弃而不是阻塞输入。
func (r *Recorder) emit(label string) {
	if label == "" || r.paused.Load() {
		return
	}
	select {
	case r.ch <- label:
	default:
	}
}

// Drain 取走缓冲里的全部事件并聚合成 键名->次数。
// 串行化保证：退出收尾（selfRestart / 托盘退出）与每秒 flushLoop 并发调用时，
// 不会出现一方取走事件、另一方永久阻塞等待或重复计数的竞态。
func (r *Recorder) Drain() map[string]int {
	r.drainMu.Lock()
	defer r.drainMu.Unlock()
	n := len(r.ch)
	out := make(map[string]int, min(n, 128))
	for range n {
		out[<-r.ch]++
	}
	if n > 0 {
		r.lastEvent.Store(time.Now().Unix())
	}
	return out
}

// HasPending 报告是否有落盘失败暂存的事件。
func (r *Recorder) HasPending() bool {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	return len(r.pending) > 0
}

// mergePending 把暂存事件并入本次待写批次并清空暂存。
func (r *Recorder) mergePending(labels map[string]int) map[string]int {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	if len(r.pending) == 0 {
		return labels
	}
	if labels == nil {
		labels = make(map[string]int, len(r.pending))
	}
	for k, v := range r.pending {
		labels[k] += v
	}
	r.pending = nil
	return labels
}

// retainPending 落盘失败时暂存整批事件，下一轮 flushOnce 重试。
// 按键名归并，长时间故障下暂存量也有界。
func (r *Recorder) retainPending(labels map[string]int) {
	r.pendingMu.Lock()
	defer r.pendingMu.Unlock()
	if r.pending == nil {
		r.pending = labels
		return
	}
	for k, v := range labels {
		r.pending[k] += v
	}
}
