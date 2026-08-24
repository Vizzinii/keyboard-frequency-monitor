//go:build windows

package main

import (
	"log"
	"sync"
	"time"
	"unsafe"
)

var (
	getLastInputInfo = user32.NewProc("GetLastInputInfo")
	getTickCount64P  = kernel32.NewProc("GetTickCount64")
)

type lastInputInfo struct {
	cbSize uint32
	dwTime uint32
}

// lastInputAgo 返回系统范围内最近一次用户输入距今的时间。
// dwTime 是 32 位 tick，uint32 减法天然处理回绕；调用失败时按"很久以前"处理（视为空闲）。
func lastInputAgo() time.Duration {
	var lii lastInputInfo
	lii.cbSize = uint32(unsafe.Sizeof(lii))
	ret, _, _ := getLastInputInfo.Call(uintptr(unsafe.Pointer(&lii)))
	if ret == 0 {
		return time.Hour
	}
	now, _, _ := getTickCount64P.Call()
	return time.Duration(uint32(now)-lii.dwTime) * time.Millisecond
}

// Health 是暴露给 /api/health、面板与托盘的状态快照。
// 锁用指针持有，避免 Snapshot/swap 时值拷贝触发 vet 的 copylocks 检查。
type Health struct {
	mu           *sync.Mutex
	Status       string    `json:"status"` // ok | healed | degraded | paused | off
	Msg          string    `json:"msg"`
	LastEvent    time.Time `json:"lastEvent"`    // 最近一次落盘事件时间
	LastActivity time.Time `json:"lastActivity"` // 钩子最近一次看到输入的时间
	LastInput    time.Time `json:"lastInput"`    // 系统最近一次输入时间（GetLastInputInfo）
	ChannelDepth int       `json:"channelDepth"`
	HealCount    int       `json:"healCount"`
	LastHeal     time.Time `json:"lastHeal"`
	Paused       bool      `json:"paused"`
	Uptime       time.Time `json:"uptime"`
}

func NewHealth() *Health {
	return &Health{mu: &sync.Mutex{}, Status: "ok", Uptime: time.Now()}
}

func (h *Health) Snapshot() Health {
	h.mu.Lock()
	defer h.mu.Unlock()
	return *h
}

func (h *Health) swap(n Health) {
	h.mu.Lock()
	defer h.mu.Unlock()
	n.Uptime = h.Uptime
	n.mu = h.mu // publish 构造的副本 mu 为 nil，不能覆盖真实的锁指针
	*h = n
}

// Watchdog 每秒检查钩子健康，失效即自愈（重装钩子）；
// 窗口内自愈次数超限则升级为自动重启（由调用方决定具体动作）。
type Watchdog struct {
	rec         *Recorder
	health      *Health
	win         time.Duration // 事件流判定窗口
	minHealGap  time.Duration // 连续两次自愈的最小间隔（防刷）
	escalateWin time.Duration // 自愈计数窗口
	maxHeals    int           // escalateWin 内允许的自愈次数上限

	mu             sync.Mutex
	healTimes      []time.Time
	lastHealAt     time.Time
	restartAt      time.Time // 上次自动重启时间（0 = 非重启模式），用于冷却
	manualRequired bool      // 冷却期内仍反复失败 → 永久停用自动重启，等用户手动重启

	// now 是时钟源，测试注入假时钟以便确定性验证 10s/5min 的时间窗；默认 time.Now。
	now func() time.Time
}

// NewWatchdog win<=0 时按 60 秒处理；restartAt 为上次自动重启时间（由旧实例通过
// KFM_RESTART_AT 注入），冷却期内不再次自动重启，防止崩溃循环。
func NewWatchdog(rec *Recorder, health *Health, win time.Duration, restartAt time.Time) *Watchdog {
	if win <= 0 {
		win = 60 * time.Second
	}
	w := &Watchdog{
		rec:         rec,
		health:      health,
		win:         win,
		minHealGap:  10 * time.Second,
		escalateWin: 5 * time.Minute,
		maxHeals:    3,
		restartAt:   restartAt,
		now:         time.Now,
	}
	if !restartAt.IsZero() && w.now().Sub(restartAt) < w.escalateWin {
		log.Printf("[看门狗] 距上次自动重启 %s，冷却期内不再自动重启", w.now().Sub(restartAt).Round(time.Second))
	}
	return w
}

// Run 启动看门狗循环；onRestart 在自愈超限时被调用（通常由 main 传入 selfRestart）。
// 循环内 panic 会打日志后终止，不影响主程序。
func (w *Watchdog) Run(onRestart func()) {
	defer func() {
		if r := recover(); r != nil {
			log.Printf("[看门狗] 异常退出: %v", r)
		}
	}()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		w.tick(onRestart)
	}
}

func (w *Watchdog) tick(onRestart func()) {
	rec := w.rec
	now := w.now()
	lastEvt := rec.LastEvent()
	lastAct := rec.LastActivity()
	sysAgo := lastInputAgo()
	paused := rec.Paused()

	switch {
	case !rec.Alive():
		w.heal(now, "钩子线程已退出，重新安装", onRestart)
	case needsReinstall(now.Sub(lastAct), sysAgo, w.win, paused):
		w.heal(now, "长时间无钩子事件但系统持续有输入，重新安装", onRestart)
	}
	w.publish(now, lastEvt, lastAct, sysAgo, paused)
}

// heal 执行一次自愈：记录时间、重装钩子，达到上限时升级为自动重启。
// 冷却期内反复失败会转成"需手动重启"（manualRequired），此后不再自动重启。
func (w *Watchdog) heal(now time.Time, reason string, onRestart func()) {
	w.mu.Lock()
	gap := w.minHealGap
	if w.manualRequired {
		gap = time.Minute // 已确定救不回时降频，避免无谓刷屏
	}
	if now.Sub(w.lastHealAt) < gap {
		w.mu.Unlock()
		return
	}
	w.lastHealAt = now
	w.healTimes = append(w.healTimes, now)
	cutoff := now.Add(-w.escalateWin)
	k := 0
	for _, t := range w.healTimes {
		if t.After(cutoff) {
			w.healTimes[k] = t
			k++
		}
	}
	w.healTimes = w.healTimes[:k]
	count := len(w.healTimes)
	escalate := count >= w.maxHeals
	w.mu.Unlock()

	log.Printf("[自愈] %s（%s 内第 %d 次）", reason, w.escalateWin, count)
	if err := w.rec.Reinstall(); err != nil {
		log.Printf("[自愈] 重装钩子失败: %v", err)
	}

	if escalate {
		w.mu.Lock()
		canRestart := !w.manualRequired && (w.restartAt.IsZero() || w.now().Sub(w.restartAt) >= w.escalateWin)
		if canRestart {
			w.mu.Unlock()
			log.Printf("[自愈] %s 内自愈 %d 次仍未稳定，触发自动重启", w.escalateWin, count)
			onRestart()
			// onRestart 正常会 os.Exit；走到这里说明拉起失败，同样转为需手动重启
			w.mu.Lock()
			w.manualRequired = true
			w.mu.Unlock()
			log.Printf("[自愈] 自动重启拉起失败，停止自动重启，请手动重启应用")
			return
		}
		w.manualRequired = true
		w.mu.Unlock()
		log.Printf("[自愈] %s 内自愈 %d 次仍不稳定且处于冷却期，停止自动重启，请手动重启应用", w.escalateWin, count)
	}
}

// publish 把当前状态写入 /api/health 可读的 Health。
func (w *Watchdog) publish(now time.Time, lastEvt, lastAct time.Time, sysAgo time.Duration, paused bool) {
	h := Health{
		LastEvent:    lastEvt,
		LastActivity: lastAct,
		LastInput:    now.Add(-sysAgo),
		ChannelDepth: w.rec.ChannelDepth(),
		Paused:       paused,
	}
	w.mu.Lock()
	h.HealCount = len(w.healTimes)
	h.LastHeal = w.lastHealAt
	switch {
	case w.manualRequired:
		h.Status, h.Msg = "degraded", "多次自动修复失败，已停止自动重启，请手动重启应用"
	case paused:
		h.Status, h.Msg = "paused", "记录已暂停"
	case now.Sub(w.lastHealAt) < 10*time.Minute:
		h.Status, h.Msg = "healed", "最近自愈过一次（"+w.lastHealAt.Format("15:04:05")+"），记录已恢复"
	default:
		h.Status, h.Msg = "ok", ""
	}
	w.mu.Unlock()
	w.health.swap(h)
}
