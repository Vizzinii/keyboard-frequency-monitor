package main

/*
#cgo LDFLAGS: -framework CoreGraphics
#include <CoreGraphics/CoreGraphics.h>

// kCGAnyInputEventType 是 (CGEventType)(~0)，宏在 cgo 里不可直接引用，包一层。
static double secondsSinceAnyInput(void) {
	return (double)CGEventSourceSecondsSinceLastEventType(
		kCGEventSourceStateCombinedSessionState, kCGAnyInputEventType);
}
*/
import "C"

import "time"

// lastInputAgo 返回系统范围内最近一次用户输入距今的时间，
// 是 Windows 的 GetLastInputInfo 在 macOS 上的等价物：它由系统事件源统计，
// 不经过我们自己的 tap，所以 tap 失效时它照样准确。
func lastInputAgo() time.Duration {
	return time.Duration(float64(C.secondsSinceAnyInput()) * float64(time.Second))
}
