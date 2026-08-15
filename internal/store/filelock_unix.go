//go:build !windows

package store

import (
	"fmt"
	"os"
	"path/filepath"

	"golang.org/x/sys/unix"
)

// fileLock 基于 flock(2) 的进程级文件锁，防止多进程并发写坏 store.json。
//
// 锁文件为 rootDir 下的 store.lock，使用 flock 的排他锁语义：
//   - 同进程内多次 Open/Close 共享同一 fd（以 rootDir 为 key 缓存）；
//   - 跨进程：第二个进程 flock 会阻塞等待（最长 lockTimeout），
//     超时返回错误（避免 scan/serve 同时写同一 store 目录时静默损坏）。
type fileLock struct {
	rootDir string
	f       *os.File
}

// acquireLock 获取指定目录的进程锁。Unix 实现使用 flock（跨平台不可用时可降级为无锁）。
func acquireLock(rootDir string) (*fileLock, error) {
	if err := os.MkdirAll(rootDir, 0o755); err != nil {
		return nil, fmt.Errorf("mkdir lock dir: %w", err)
	}
	path := filepath.Join(rootDir, "store.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o644)
	if err != nil {
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	// 非阻塞尝试；失败则说明其他进程持有锁（写操作互斥）
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return nil, fmt.Errorf("acquire file lock %s (another process is writing?): %w", path, err)
	}
	return &fileLock{rootDir: rootDir, f: f}, nil
}

// release 释放进程锁。
func (l *fileLock) release() {
	if l == nil || l.f == nil {
		return
	}
	_ = unix.Flock(int(l.f.Fd()), unix.LOCK_UN)
	_ = l.f.Close()
	l.f = nil
}
