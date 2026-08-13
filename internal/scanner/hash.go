package scanner

import (
	"crypto/sha256"
	"fmt"
	"os"
)

// sha256sum 计算文件的 SHA-256 哈希值，返回 hex 编码字符串。
// 空文件、大文件、二进制文件均可正确处理。
func sha256sum(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", fmt.Errorf("read file: %w", err)
	}
	h := sha256.Sum256(data)
	return fmt.Sprintf("%x", h), nil
}