// Package fsperm 提供索引数据目录/文件的权限加固工具。
//
// 背景：索引数据（store.json、vector.json、FTS、IDF、SQLite、锁文件）含仓库语义信息，
// 默认 0755/0644 在 umask 放宽时可能被同机其他用户读到。本包统一将归属目录收紧为
// 0700、文件为 0600（仅属主可读/写），供 store / vector / search 等跨包复用，避免重复实现。
package fsperm

import (
	"os"
	"path/filepath"
)

// DirMode 索引数据目录权限：仅属主可读/写/执行。
const DirMode os.FileMode = 0o700

// FileMode 索引数据文件权限：仅属主可读/写。
const FileMode os.FileMode = 0o600

// MkdirAll 创建目录树并把末端目录权限收紧为 0700。
//
// 中间目录仅在新建时按 0700 创建（不受 umask 放宽影响）；对已存在的末端目录
// 也会 chmod 0700，确保即使 umask 放宽或历史遗留了过宽权限也能收敛到最小。
func MkdirAll(path string) error {
	if path == "" {
		return nil
	}
	if err := os.MkdirAll(path, DirMode); err != nil {
		return err
	}
	return os.Chmod(path, DirMode)
}

// WriteFile 以 0600 写入数据并确保父目录为 0700。
//
// WriteFile 的 mode 仅在新建文件时生效；对已存在但权限过宽的文件，随后 chmod 0600
// 收敛，保证索引文件始终最小权限（覆盖 umask 与历史残留）。
func WriteFile(path string, data []byte) error {
	if err := MkdirAll(filepath.Dir(path)); err != nil {
		return err
	}
	if err := os.WriteFile(path, data, FileMode); err != nil {
		return err
	}
	return os.Chmod(path, FileMode)
}