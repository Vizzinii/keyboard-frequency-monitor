package main

/*
#cgo CFLAGS: -x objective-c -Wno-deprecated-declarations
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>

// goEmit / goTouch 由 Go 侧导出，回调里只做一次投递就返回。
extern void goEmit(unsigned int kind, unsigned long long code, unsigned long long flags);
extern void goTouch(void);

static CFMachPortRef gTap = NULL;
static CFRunLoopRef  gLoop = NULL;

// kind 取值：0=按键, 1=修饰键状态变化, 2=鼠标按下
static CGEventRef tapCallback(CGEventTapProxy proxy, CGEventType type,
                              CGEventRef event, void *refcon) {
    goTouch(); // 任何事件都算“tap 还活着”，交给看门狗判定

    // tap 被系统禁用（回调超时或休眠唤醒）后必须重新启用，
    // 否则统计会从此静默停止。
    if (type == kCGEventTapDisabledByTimeout ||
        type == kCGEventTapDisabledByUserInput) {
        if (gTap != NULL) {
            CGEventTapEnable(gTap, true);
        }
        return event;
    }

    switch (type) {
    case kCGEventKeyDown:
        goEmit(0, CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode), 0);
        break;
    case kCGEventFlagsChanged:
        // 修饰键按下与松开触发同一个事件，靠 flags 里的
        // device-dependent 位判断“现在是按下状态”，避免计两次。
        goEmit(1, CGEventGetIntegerValueField(event, kCGKeyboardEventKeycode),
               CGEventGetFlags(event));
        break;
    case kCGEventLeftMouseDown:
        goEmit(2, 0, 0);
        break;
    case kCGEventRightMouseDown:
        goEmit(2, 1, 0);
        break;
    case kCGEventOtherMouseDown:
        goEmit(2, CGEventGetIntegerValueField(event, kCGMouseEventButtonNumber), 0);
        break;
    default:
        break;
    }
    return event;
}

// startTap 装 listen-only tap 并挂到当前线程的 run loop 上。返回 0 表示失败。
static int startTap(void) {
    CGEventMask mask = CGEventMaskBit(kCGEventKeyDown) |
                       CGEventMaskBit(kCGEventFlagsChanged) |
                       CGEventMaskBit(kCGEventLeftMouseDown) |
                       CGEventMaskBit(kCGEventRightMouseDown) |
                       CGEventMaskBit(kCGEventOtherMouseDown);
    // ListenOnly：只观察不改写事件流，比 Default 更不容易被系统判定为拖慢输入
    gTap = CGEventTapCreate(kCGSessionEventTap, kCGHeadInsertEventTap,
                            kCGEventTapOptionListenOnly, mask, tapCallback, NULL);
    if (gTap == NULL) {
        return 0;
    }
    CFRunLoopSourceRef src = CFMachPortCreateRunLoopSource(kCFAllocatorDefault, gTap, 0);
    gLoop = CFRunLoopGetCurrent();
    CFRunLoopAddSource(gLoop, src, kCFRunLoopCommonModes);
    CGEventTapEnable(gTap, true);
    CFRelease(src);
    return 1;
}

static void runTapLoop(void) { CFRunLoopRun(); }

// tapEnabled 直接问系统这个 tap 现在是否启用；tap 被移除时返回 0。
static int tapEnabled(void) {
    return (gTap != NULL && CGEventTapIsEnabled(gTap)) ? 1 : 0;
}

static void stopTap(void) {
    if (gTap != NULL) {
        CGEventTapEnable(gTap, false);
    }
    if (gLoop != NULL) {
        CFRunLoopStop(gLoop);
    }
}

// releaseTap 释放上一代 tap 的资源，供重装前调用。
static void releaseTap(void) {
    if (gLoop != NULL) {
        CFRunLoopStop(gLoop);
        gLoop = NULL;
    }
    if (gTap != NULL) {
        CGEventTapEnable(gTap, false);
        CFRelease(gTap);
        gTap = NULL;
    }
}

static int hasInputMonitoring(void) { return CGPreflightListenEventAccess() ? 1 : 0; }
static void askInputMonitoring(void) { CGRequestListenEventAccess(); }
*/
import "C"

import (
	"fmt"
	"runtime"
	"sync"
	"time"
)

// activeRecorder 让 C 回调找回 Recorder。
// ponytail: 进程里只会有一个 Recorder，用包级变量代替 refcon 传指针；
// 要支持多实例再换成 handle 表。
var activeRecorder *Recorder

//export goEmit
func goEmit(kind C.uint, code, flags C.ulonglong) {
	r := activeRecorder
	if r == nil {
		return
	}
	switch kind {
	case 0:
		r.emit(vkLabel(uint32(code)))
	case 1:
		if modifierPressed(uint32(code), uint64(flags)) {
			r.emit(vkLabel(uint32(code)))
		}
	case 2:
		r.emit(mouseLabel(uint32(code)))
	}
}

//export goTouch
func goTouch() {
	if r := activeRecorder; r != nil {
		r.touch()
	}
}

// tapState 记录 run loop 这一代的存活情况，语义与 Windows 的消息泵一致。
type tapState struct {
	mu       sync.Mutex
	loopDone chan struct{} // run loop 退出时关闭
}

var taps tapState

type Hooks struct{ r *Recorder }

func (h *Hooks) Stop() { C.stopTap() }

// StartHooks 申请“输入监控”权限后安装 CGEventTap。
func (r *Recorder) StartHooks() (*Hooks, error) {
	if C.hasInputMonitoring() == 0 {
		C.askInputMonitoring() // 首次会弹系统授权框
		return nil, fmt.Errorf(`缺少“输入监控”权限。
请在 系统设置 → 隐私与安全性 → 输入监控 中勾选本程序，然后重新启动它。
（macOS 要求全局键盘监听必须显式授权，这是系统限制，不是程序能绕过的。）`)
	}
	if err := r.install(); err != nil {
		return nil, err
	}
	return &Hooks{r}, nil
}

// Reinstall 停掉旧 tap 后重新安装，供看门狗自愈或托盘手动触发。
func (r *Recorder) Reinstall() error { return r.install() }

// Alive 报告 tap 是否仍然健康：run loop 线程未退出，且系统认为 tap 仍启用。
// 后半条覆盖“线程活着但 tap 被系统移除”——正是 Windows 端第 2 层要解决的情况。
func (r *Recorder) Alive() bool {
	taps.mu.Lock()
	done := taps.loopDone
	taps.mu.Unlock()
	return done != nil && !isClosed(done) && C.tapEnabled() == 1
}

// install 装 tap 并在锁定的 OS 线程上跑 CFRunLoop；可重复调用。
// run loop 退出即关闭这一代的 loopDone，让看门狗看得见（旧版这里是静默死亡）。
func (r *Recorder) install() error {
	activeRecorder = r

	taps.mu.Lock()
	C.releaseTap() // 停掉上一代，避免新旧 tap 并存重复计数
	done := make(chan struct{})
	taps.loopDone = done
	taps.mu.Unlock()

	errCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // CFRunLoop 必须绑在固定线程上
		defer close(done)      // run loop 退出 -> 看门狗判定为失效并重装

		if C.startTap() == 0 {
			errCh <- fmt.Errorf("CGEventTapCreate 失败（权限可能已被撤销）")
			return
		}
		r.touch()
		errCh <- nil

		C.runTapLoop() // 阻塞直到 stopTap / releaseTap
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return err
		}
	case <-time.After(3 * time.Second):
		return fmt.Errorf("安装事件 tap 超时")
	}
	return nil
}

func mouseLabel(button uint32) string {
	switch button {
	case 0:
		return "mouse_left"
	case 1:
		return "mouse_right"
	case 2:
		return "mouse_middle"
	case 3:
		return "mouse_x1"
	case 4:
		return "mouse_x2"
	}
	return ""
}
