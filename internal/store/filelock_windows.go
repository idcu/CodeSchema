//go:build windows

package store

import (
	"fmt"
	"os"
	"path/filepath"

	"github.com/idcu/codeschema/internal/fsperm"
)

// fileLock Windows 平台的进程锁（简化实现：基于独占创建锁文件的原子性）。
//
// Windows 下 flock 不可用，用「独占创建 store.lock」近似互斥：
// O_CREATE|O_EXCL 保证同一时刻只有一个进程能创建成功；进程退出后锁文件残留，
// 通过记录 PID + 探测进程存活来清理（简化：残留锁文件需手动删除，见文档说明）。
type fileLock struct {
	rootDir string
	path    string
}

// acquireLock 获取指定目录的进程锁。Windows 简化实现。
func acquireLock(rootDir string) (*fileLock, error) {
	if err := fsperm.MkdirAll(rootDir); err != nil {
		return nil, fmt.Errorf("mkdir lock dir: %w", err)
	}
	path := filepath.Join(rootDir, "store.lock")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return nil, fmt.Errorf("acquire file lock %s (another process is writing? remove stale store.lock if no writer exists)", path)
		}
		return nil, fmt.Errorf("open lock file: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	_, _ = f.WriteString("codeschema-file-lock\n")
	_ = f.Close()
	return &fileLock{rootDir: rootDir, path: path}, nil
}

// release 释放进程锁（删除锁文件）。
func (l *fileLock) release() {
	if l == nil || l.path == "" {
		return
	}
	_ = os.Remove(l.path)
	l.path = ""
}
