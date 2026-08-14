// Package benchmark 单元测试：报告生成 + 文件统计。
package benchmark

import (
	"os"
	"path/filepath"
	"testing"
)

// TestGenerateComparisonMarkdown 验证 Markdown 对比表生成（单仓/多仓）。
func TestGenerateComparisonMarkdown(t *testing.T) {
	results := []BenchResult{
		{RepoName: "b", FileCount: 10, ScanTimeMs: 100, IndexTimeMs: 50, HeapMB: 10, SearchP95Ms: 2.0},
		{RepoName: "a", FileCount: 5, ScanTimeMs: 50, IndexTimeMs: 25, HeapMB: 5, SearchP95Ms: 1.0},
	}
	md := GenerateComparisonMarkdown(results, "a")

	// 单仓（结果非空）时也应输出表头
	if !contains(md, "多仓库 Benchmark 对比") {
		t.Fatalf("markdown missing header")
	}
	if !contains(md, "a") || !contains(md, "b") {
		t.Fatalf("markdown missing repo names")
	}
	// 多仓应含相对性能段
	if !contains(md, "相对性能") {
		t.Fatalf("markdown missing relative performance section")
	}
	// 空结果
	if got := GenerateComparisonMarkdown(nil, "x"); got == "" {
		t.Fatalf("empty results should produce non-empty fallback")
	}
}

// TestGenerateComparisonJSON 验证 JSON 序列化结构。
func TestGenerateComparisonJSON(t *testing.T) {
	results := []BenchResult{{RepoName: "a", FileCount: 1}}
	data, err := GenerateComparisonJSON(results, "a", "test-config")
	if err != nil {
		t.Fatalf("GenerateComparisonJSON: %v", err)
	}
	s := string(data)
	for _, want := range []string{`"repo_name"`, `"file_count"`, `"baseline"`, `"config_desc"`, "test-config"} {
		if !contains(s, want) {
			t.Fatalf("json missing %q: %s", want, s)
		}
	}
}

// TestSortBenchResults 验证按仓库名排序。
func TestSortBenchResults(t *testing.T) {
	results := []BenchResult{{RepoName: "z"}, {RepoName: "a"}, {RepoName: "m"}}
	SortBenchResults(results)
	if results[0].RepoName != "a" || results[1].RepoName != "m" || results[2].RepoName != "z" {
		t.Fatalf("sort failed: %+v", results)
	}
}

// TestCountFiles 验证文件总数统计（与 scanner.listFiles 同口径：忽略依赖/构建目录）。
func TestCountFiles(t *testing.T) {
	dir := t.TempDir()
	// 普通文件
	for _, f := range []string{"a.go", "b.java", "c.py", "d.ts", "readme.txt", "Dockerfile"} {
		if err := os.WriteFile(filepath.Join(dir, f), []byte("// x"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	// 依赖目录跳过
	if err := os.MkdirAll(filepath.Join(dir, "vendor", "x"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "vendor", "x", "v.go"), []byte("// v"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "node_modules", "y"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "node_modules", "y", "n.js"), []byte("// n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "build"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "build", "b.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}
	// 子目录正常统计
	if err := os.MkdirAll(filepath.Join(dir, "sub"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "sub", "s.go"), []byte("// s"), 0o644); err != nil {
		t.Fatal(err)
	}

	got := countFiles(dir)
	// 期望：a.go b.java c.py d.ts readme.txt Dockerfile sub/s.go = 7（vendor/node_modules/build 内不计）
	if got != 7 {
		t.Fatalf("countFiles = %d, want 7", got)
	}
}

// TestIsSupportedSource 验证语言判定。
func TestIsSupportedSource(t *testing.T) {
	for _, f := range []string{"a.go", "A.JAVA", "b.yml", "c.markdown", "Dockerfile"} {
		if !isSupportedSource(f) {
			t.Fatalf("%s should be supported", f)
		}
	}
	for _, f := range []string{"a.txt", "b.zip", "c.exe", "noext"} {
		if isSupportedSource(f) {
			t.Fatalf("%s should NOT be supported", f)
		}
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (func() bool {
		for i := 0; i+len(sub) <= len(s); i++ {
			if s[i:i+len(sub)] == sub {
				return true
			}
		}
		return false
	})()
}
