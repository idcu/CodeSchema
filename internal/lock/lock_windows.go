//go:build windows

package lock

import (
	"fmt"
	"os"
	"path/filepath"

	"gitee.com/idcu-go/pathsafe"
)

// handle 平台特定的锁句柄（Windows：锁文件路径）。
type handle struct {
	path string
}

// acquire 获取 dir/name 的排他锁。Windows 下 flock 不可用，
// 用「独占创建锁文件」近似互斥：O_CREATE|O_EXCL 保证同一时刻只有一个进程创建成功；
// 进程退出后锁文件残留，需手动删除（见文档说明）。
func acquire(dir, name string) (handle, error) {
	if err := pathsafe.MkdirAll(dir); err != nil {
		return handle{}, fmt.Errorf("mkdir lock dir: %w", err)
	}
	path := filepath.Join(dir, name)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0o600)
	if err != nil {
		if os.IsExist(err) {
			return handle{}, fmt.Errorf("acquire lock %s (another process is writing? remove stale lock if no writer exists)", path)
		}
		return handle{}, fmt.Errorf("open lock file: %w", err)
	}
	_ = os.Chmod(path, 0o600)
	_, _ = f.WriteString("code-schema-file-lock\n")
	_ = f.Close()
	return handle{path: path}, nil
}

func (h handle) release() error {
	if h.path == "" {
		return nil
	}
	err := os.Remove(h.path)
	h.path = ""
	return err
}
