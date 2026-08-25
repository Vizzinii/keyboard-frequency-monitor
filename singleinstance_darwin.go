package main

import (
	"os"
	"path/filepath"
	"syscall"
)

// singleInstance 用可执行文件目录下的锁文件 + flock 仲裁"谁先启动"：
// 独占锁被内核持有，进程退出（含 kill -9、崩溃）时自动释放，语义与
// Windows 的命名互斥体一致，不会留下需要手工清理的陈旧锁。
type singleInstance struct {
	f *os.File
}

// acquireSingleInstance 尝试获得实例独占权；返回 (nil, nil) 表示已有实例在跑。
// 持有的锁需在正常退出时 release()。
func acquireSingleInstance() (*singleInstance, error) {
	dir := "."
	if exe, err := os.Executable(); err == nil {
		dir = filepath.Dir(exe)
	}
	f, err := os.OpenFile(filepath.Join(dir, ".kfm.lock"), os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		f.Close()
		return nil, nil // EWOULDBLOCK：另一实例正持有锁
	}
	return &singleInstance{f: f}, nil
}

// release 正常退出时解锁；进程被杀时内核自动释放，锁文件本身留着复用。
func (s *singleInstance) release() {
	syscall.Flock(int(s.f.Fd()), syscall.LOCK_UN)
	s.f.Close()
}
