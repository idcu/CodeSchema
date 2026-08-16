// Package testutil 提供测试共享工具，重点解决 benchmark 快照误提交问题。
package testutil

import (
	"os"
	"path/filepath"
	"testing"
)

// BenchOutPath 返回 benchmark 报告 JSON 的写入路径。
//
// 默认（不设置环境变量）写入 t.TempDir() 下的临时文件：go test ./... 运行后由
// testing 框架自动清理，绝不触碰仓库 build/，从根本上杜绝「基准快照被测试误改后
// 误提交」的风险（此前 build/bench-compare.json、build/realrepo-bench.json 为已跟踪
// 文件，go test 一跑就会误改、误提交）。
//
// 仅当显式设置 CODESCHEMA_UPDATE_BENCH=1 时，才写回仓库根的 build/<name>，
// 供开发者在本地主动刷新需要提交到仓库的基准快照（刷新后需人工 review 再 commit）。
func BenchOutPath(tb testing.TB, name string) string {
	tb.Helper()
	if os.Getenv("CODESCHEMA_UPDATE_BENCH") == "1" {
		root := repoRoot(tb)
		dir := filepath.Join(root, "build")
		if err := os.MkdirAll(dir, 0o755); err != nil {
			tb.Fatalf("创建 build 目录失败: %v", err)
		}
		return filepath.Join(dir, name)
	}
	return filepath.Join(tb.TempDir(), name)
}

// repoRoot 定位仓库根（优先 GOMOD 注入，回退向上查找 go.mod）。
func repoRoot(tb testing.TB) string {
	tb.Helper()
	if m := os.Getenv("GOMOD"); m != "" {
		return filepath.Dir(m)
	}
	dir, err := os.Getwd()
	if err != nil {
		tb.Fatalf("Getwd failed: %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			tb.Fatal("go.mod not found in parent directories")
		}
		dir = parent
	}
}
