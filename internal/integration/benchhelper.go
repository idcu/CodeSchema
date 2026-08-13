// Package integration 提供 benchmark 共享工具函数。
package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"codeschema/internal/parser"
	adapter "codeschema/internal/parser/adapter/treesitter"
	"codeschema/internal/scanner"
	"codeschema/internal/search"
	"codeschema/internal/store"
	"codeschema/internal/vector"
)

// BenchSetup 包含 benchmark 所需的全部组件。
type BenchSetup struct {
	Store    store.Store
	Scanner  *scanner.Scanner
	Builder  *search.IndexBuilder
	Searcher *search.Searcher
}

// NewBenchSetup 创建 benchmark 组件集合。
// 所有组件使用临时目录和内存存储，测试结束后通过 cleanup 清理。
func NewBenchSetup(tb testing.TB, repoRoot string) (*BenchSetup, func()) {
	tb.Helper()

	// 存储（使用临时目录）
	st := store.NewStore("file")
	storeDir := tb.TempDir()
	if err := st.Open(context.Background(), storeDir); err != nil {
		tb.Fatalf("open store: %v", err)
	}

	// 注册 tree-sitter 适配器
	reg := parser.NewRegistry()
	reg.Register(adapter.NewTreeSitterAdapter())

	// 扫描器
	sc := scanner.NewScanner(st, reg, 2)

	// 搜索组件
	fts := search.NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	builder := search.NewIndexBuilder(fts, idx, em)
	searcher := search.NewSearcher(fts, search.NewVectorAdapter(idx), nil)

	setup := &BenchSetup{
		Store:    st,
		Scanner:  sc,
		Builder:  builder,
		Searcher: searcher,
	}

	cleanup := func() {
		st.Close()
	}

	return setup, cleanup
}

// FindRepoRoot 从测试文件所在目录向上查找，直到找到 go.mod。
func FindRepoRoot(tb testing.TB) string {
	tb.Helper()
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

// DiscoverGoFiles 递归查找目录下的所有 .go 文件。
func DiscoverGoFiles(tb testing.TB, root string) []string {
	tb.Helper()
	var files []string
	filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			base := info.Name()
			if base == "vendor" || base == ".git" || base == "node_modules" || base == "down" {
				return filepath.SkipDir
			}
			return nil
		}
		if filepath.Ext(path) == ".go" {
			files = append(files, path)
		}
		return nil
	})
	return files
}

// GetBenchRepos 从环境变量获取要 benchmark 的仓库路径列表。
// CODESCHEMA_BENCH_REPOS 环境变量使用分号分隔多个路径。
// 若未设置，默认返回当前仓库根目录。
func GetBenchRepos(tb testing.TB) []string {
	tb.Helper()

	env := os.Getenv("CODESCHEMA_BENCH_REPOS")
	if env == "" {
		return []string{FindRepoRoot(tb)}
	}

	var paths []string
	start := 0
	for i := 0; i <= len(env); i++ {
		if i == len(env) || env[i] == ';' {
			p := strings.TrimSpace(env[start:i])
			if p != "" {
				paths = append(paths, p)
			}
			start = i + 1
		}
	}
	if len(paths) == 0 {
		return []string{FindRepoRoot(tb)}
	}
	return paths
}

// RepoName 从路径中提取仓库名（最后一级目录名）。
func RepoName(repoPath string) string {
	return filepath.Base(repoPath)
}