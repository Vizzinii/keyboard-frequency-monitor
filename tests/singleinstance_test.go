//go:build windows

package main

import "testing"

// TestSingleInstanceReleaseAndReacquire 同一进程内 release 后可再次获取。
// 进程间的互斥（双实例、崩溃接管）由 e2e 的 TestE2E_SingleInstance /
// TestE2E_CrashTakeover 覆盖。
func TestSingleInstanceReleaseAndReacquire(t *testing.T) {
	si, err := acquireSingleInstance()
	if err != nil {
		t.Fatal(err)
	}
	if si == nil {
		t.Fatal("首次获取应成功")
	}
	si.release()

	si2, err := acquireSingleInstance()
	if err != nil {
		t.Fatal(err)
	}
	if si2 == nil {
		t.Fatal("release 后应可重新获取")
	}
	si2.release()
}
