package main

import (
	"os"
	"regexp"
	"testing"
)

// TestMainSourceUsesFspermForDataDirs 负控（P3-1）：
// 生产代码不得用 os.MkdirAll(..., 0755) 创建数据目录，落盘统一经 fsperm（0700）。
func TestMainSourceUsesFspermForDataDirs(t *testing.T) {
	src, err := os.ReadFile("main.go")
	if err != nil {
		t.Fatalf("read main.go: %v", err)
	}
	loose := regexp.MustCompile(`os\.MkdirAll\([^\n]*0?755`)
	if m := loose.FindString(string(src)); m != "" {
		t.Fatalf("数据目录必须以 0700 创建（经 fsperm.MkdirAll），发现: %s", m)
	}
}
