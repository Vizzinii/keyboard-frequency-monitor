package main

import "sync/atomic"

// eventBuffer 溢出直接丢弃，绝不能拖慢系统输入。
const eventBuffer = 16384

// Recorder 是跨平台的事件缓冲：平台相关的钩子只负责调用 emit，
// 具体安装/卸载见 recorder_windows.go / recorder_darwin.go 的 StartHooks。
type Recorder struct {
	ch     chan string
	paused atomic.Bool
}

func NewRecorder() *Recorder {
	return &Recorder{ch: make(chan string, eventBuffer)}
}

func (r *Recorder) SetPaused(p bool) { r.paused.Store(p) }
func (r *Recorder) Paused() bool     { return r.paused.Load() }

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
func (r *Recorder) Drain() map[string]int {
	n := len(r.ch)
	out := make(map[string]int, min(n, 128))
	for range n {
		out[<-r.ch]++
	}
	return out
}
