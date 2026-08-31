// Package hash 提供文件内容哈希等通用辅助，作为 code-schema 内部多包共享的单一事实来源，
// 避免 scanner / vector 等包各自重复实现 SHA-256 计算。
package hash

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"os"
)

// File 计算文件的 SHA-256 哈希值，返回小写 hex 编码字符串。
// 采用流式读取（io.Copy），对大文件（如模型归档）友好。
func File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", fmt.Errorf("open file: %w", err)
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", fmt.Errorf("hash file: %w", err)
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}
