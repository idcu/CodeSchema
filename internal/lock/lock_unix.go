//go:build !windows

package lock

import (
	"fmt"
	"os"
	"path/filepath"

	"gitee.com/idcu-go/pathsafe"
	"golang.org/x/sys/unix"
)

// handle 平台特定的锁句柄（Unix：持有锁文件 fd）。
type handle struct {
	f *os.File
}

// acquire 获取 dir/name 的排他锁。Unix 实现使用 flock 的排他锁语义：
// 同进程内多次 Acquire/Release 各持独立 fd；跨进程第二个 Acquire 会因
// LOCK_NB 立即失败（写操作互斥）。
func acquire(dir, name string) (handle, error) {
	if err := pathsafe.MkdirAll(dir); err != nil {
		return handle{}, fmt.Errorf("mkdir lock dir: %w", err)
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return handle{}, fmt.Errorf("open lock file: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	// 非阻塞排他锁：失败说明其他进程持有锁（写操作互斥）
	if err := unix.Flock(int(f.Fd()), unix.LOCK_EX|unix.LOCK_NB); err != nil {
		f.Close()
		return handle{}, fmt.Errorf("acquire lock %s (another process is writing?): %w", path, err)
	}
	return handle{f: f}, nil
}

func (h handle) release() error {
	if h.f == nil {
		return nil
	}
	_ = unix.Flock(int(h.f.Fd()), unix.LOCK_UN)
	return h.f.Close()
}
