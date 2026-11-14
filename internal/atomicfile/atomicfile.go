// Package atomicfile 提供原子文件替换：先写 <path>.tmp，成功后 rename 覆盖目标。
//
// 落盘顺序：写临时文件 → fsync 内容 → 原子 rename → fsync 目录（best-effort）。
// 进程被强杀或写入中途失败时，目标文件要么是上一完整版本，要么是新完整版本，
// 不会出现截断或半份内容；失败分支会清理临时文件。
//
// 本包只保证**同进程单写者**（调用方需自行串行化写入，例如持有存储写锁）；
// 跨进程并发写同一路径不在承诺范围内。
package atomicfile

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/idcu/codeschema/internal/fsperm"
)

const bufSize = 256 * 1024

// Write 原子写入 path：由 write 负责把完整内容写入 w。
//
// write 返回错误、Flush/Sync/Close/Rename 任一步失败时，目标文件保持原样，
// 临时文件被删除。write 可以分多次写，禁止在 write 之外持有临时文件句柄。
func Write(path string, write func(io.Writer) error) error {
	if path == "" {
		return errors.New("atomicfile: empty path")
	}
	tmp := path + ".tmp"
	f, err := fsperm.OpenFile(tmp, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, fsperm.FileMode)
	if err != nil {
		return fmt.Errorf("atomicfile: create temp: %w", err)
	}
	abort := func(cause error) error {
		_ = f.Close()
		_ = os.Remove(tmp)
		return cause
	}

	bw := bufio.NewWriterSize(f, bufSize)
	if err := write(bw); err != nil {
		return abort(fmt.Errorf("atomicfile: write temp: %w", err))
	}
	if err := bw.Flush(); err != nil {
		return abort(fmt.Errorf("atomicfile: flush temp: %w", err))
	}
	if err := f.Sync(); err != nil {
		return abort(fmt.Errorf("atomicfile: sync temp: %w", err))
	}
	if err := f.Close(); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("atomicfile: close temp: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("atomicfile: rename: %w", err)
	}
	if dir, derr := os.Open(filepath.Dir(path)); derr == nil {
		_ = dir.Sync()
		_ = dir.Close()
	}
	return nil
}
