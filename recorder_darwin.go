package main

/*
#cgo CFLAGS: -x objective-c -Wno-deprecated-declarations
#cgo LDFLAGS: -framework CoreGraphics -framework CoreFoundation

#include <CoreGraphics/CoreGraphics.h>
#include <CoreFoundation/CoreFoundation.h>

// goEmit 由 Go 侧导出，回调里只做一次投递就返回。
extern void goEmit(unsigned int kind, unsigned long long code, unsigned long long flags);

static CFMachPortRef gTap = NULL;
static CFRunLoopRef  gLoop = NULL;

// kind 取值：0=按键, 1=修饰键状态变化, 2=鼠标按下
static CGEventRef tapCallback(CGEventTapProxy proxy, CGEventType type,
                              CGEventRef event, void *refcon) {
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

// startTap 装 listen-only tap 并把它挂到当前线程的 run loop 上。
// 返回 0 表示失败（通常是权限被撤销）。
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

static void stopTap(void) {
    if (gTap != NULL) {
        CGEventTapEnable(gTap, false);
    }
    if (gLoop != NULL) {
        CFRunLoopStop(gLoop);
    }
}

static int hasInputMonitoring(void) { return CGPreflightListenEventAccess() ? 1 : 0; }
static void askInputMonitoring(void) { CGRequestListenEventAccess(); }
*/
import "C"

import (
	"fmt"
	"runtime"
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

// Hooks 持有 tap 的生命周期；实际句柄在 C 侧的包级变量里。
type Hooks struct{}

func (h *Hooks) Stop() { C.stopTap() }

// StartHooks 申请“输入监控”权限后安装 CGEventTap，
// 并在锁定的 OS 线程上跑 CFRunLoop 维持 tap 存活，直到 Stop 停止 run loop。
func (r *Recorder) StartHooks() (*Hooks, error) {
	if C.hasInputMonitoring() == 0 {
		C.askInputMonitoring() // 首次会弹系统授权框
		return nil, fmt.Errorf(`缺少“输入监控”权限。
请在 系统设置 → 隐私与安全性 → 输入监控 中勾选本程序，然后重新启动它。
（macOS 要求全局键盘监听必须显式授权，这是系统限制，不是程序能绕过的。）`)
	}

	activeRecorder = r
	errCh := make(chan error, 1)
	go func() {
		runtime.LockOSThread() // CFRunLoop 必须绑在固定线程上
		if C.startTap() == 0 {
			errCh <- fmt.Errorf("CGEventTapCreate 失败（权限已授予但 tap 装不上）")
			return
		}
		errCh <- nil
		C.runTapLoop() // 阻塞直到 stopTap
	}()

	select {
	case err := <-errCh:
		if err != nil {
			return nil, err
		}
	case <-time.After(3 * time.Second):
		return nil, fmt.Errorf("安装事件 tap 超时")
	}
	return &Hooks{}, nil
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
