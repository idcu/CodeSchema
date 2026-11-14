// Package fsperm 统一数据落盘权限：目录 0700、文件 0600。
//
// 仓库内所有持久化写入（index 目录、模型文件、SQLite 文件、搜索索引等）
// 统一经本包，避免各模块各自使用 0755/0644 造成权限扩散。底层复用
// gitee.com/idcu-go/pathsafe 的权限原语，并补充对既有文件的权限收敛
// （旧版本数据可能是 0644，读取/重写时顺手 chmod 到 0600）。
package fsperm

import (
	"os"
	"path/filepath"

	"gitee.com/idcu-go/pathsafe"
)

const (
	// DirMode 数据目录权限：仅属主可读写执行。
	DirMode = pathsafe.DirMode
	// FileMode 数据文件权限：仅属主可读写。
	FileMode = pathsafe.FileMode
)

// MkdirAll 创建目录（含父级）并收敛权限到 0700；已存在的目录同样收敛，
// 用于旧数据迁移。
func MkdirAll(path string) error {
	return pathsafe.MkdirAll(path)
}

// WriteFile 写入文件并保证 0600；已存在的文件权限也会被收敛。
func WriteFile(path string, data []byte) error {
	return pathsafe.WriteFile(path, data)
}

// OpenFile 打开（可创建）文件并保证 0600。自动创建父目录（0700）；
// 对已存在的文件强制执行 chmod，覆盖旧版本遗留的 0644。
func OpenFile(path string, flag int, perm os.FileMode) (*os.File, error) {
	if dir := filepath.Dir(path); dir != "" && dir != "." {
		if err := MkdirAll(dir); err != nil {
			return nil, err
		}
	}
	f, err := os.OpenFile(path, flag, perm&FileMode)
	if err != nil {
		return nil, err
	}
	if err := f.Chmod(FileMode); err != nil {
		_ = f.Close()
		return nil, err
	}
	return f, nil
}

// Chmod 将已存在文件收敛到 0600（如 SQLite 打开后对 .db 文件的迁移）。
func Chmod(path string) error {
	return os.Chmod(path, FileMode)
}
