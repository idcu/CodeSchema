// Package integration 提供多仓库 benchmark 对比测试。
//
// 运行方式：
//   go test -run=TestMultiRepo_CollectMetrics -timeout 600s ./internal/integration/
//   CODESCHEMA_BENCH_REPOS="C:\repo1;C:\repo2" go test -run=TestMultiRepo_CollectMetrics -timeout 600s ./internal/integration/
//
// 输出文件：build/bench-compare.json（JSON 对比报告）
package integration

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"codeschema/internal/search"
)

// TestMultiRepo_CollectMetrics 多仓库基准测试：采集每个仓库的 scan→index→search 指标并输出对比报告。
func TestMultiRepo_CollectMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-repo benchmark in short mode")
	}

	repos := GetBenchRepos(t)
	t.Logf("Benchmark targets: %d repos", len(repos))
	for _, r := range repos {
		t.Logf("  - %s", r)
	}

	var results []BenchResult

	for _, repoPath := range repos {
		repoName := RepoName(repoPath)
		t.Run(repoName, func(t *testing.T) {
			// 检查仓库是否存在
			if _, err := os.Stat(repoPath); err != nil {
				t.Skipf("repo path not found: %s", repoPath)
				return
			}
			if _, err := os.Stat(filepath.Join(repoPath, "go.mod")); err != nil {
				t.Skipf("no go.mod found in %s, skipping", repoPath)
				return
			}

			goFiles := DiscoverGoFiles(t, repoPath)
			t.Logf("Go source files: %d", len(goFiles))

			// 初始化组件
			setup, cleanup := NewBenchSetup(t, repoPath)
			defer cleanup()
			ctx := context.Background()

			// 记录内存基准
			var m0 runtime.MemStats
			runtime.ReadMemStats(&m0)

			// 扫描
			scanStart := time.Now()
			if err := setup.Scanner.ScanAll(ctx, repoPath); err != nil {
				t.Fatalf("ScanAll failed: %v", err)
			}
			scanTime := time.Since(scanStart)

			// 构建索引
			idxStart := time.Now()
			if _, err := setup.Builder.BuildFromStore(ctx, setup.Store); err != nil {
				t.Fatalf("BuildFromStore failed: %v", err)
			}
			idxTime := time.Since(idxStart)

			// 内存峰值
			var m1 runtime.MemStats
			runtime.ReadMemStats(&m1)
			heapMB := float64(m1.HeapInuse-m0.HeapInuse) / 1024 / 1024

			// 搜索延迟分布
			queries := []string{
				"Scanner", "BuildAll", "Parse", "Store", "Vector",
				"Search", "Adapter", "config", "analyzer", "scheduler",
				"IRDocument", "ClassIR", "MethodIR", "FileStore",
				"MemoryStore", "LocalEmbedder", "IndexBuilder", "Searcher",
			}

			var latencies []float64
			for _, q := range queries {
				start := time.Now()
				_, err := setup.Searcher.Search(ctx, q, search.SearchModeBoth, 10)
				latency := time.Since(start)
				if err != nil {
					t.Logf("Search(%q) failed: %v (skipped)", q, err)
					continue
				}
				latencies = append(latencies, float64(latency.Microseconds()))
			}

			if len(latencies) == 0 {
				t.Fatal("no successful search queries")
			}

			sort.Float64s(latencies)
			n := len(latencies)
			p50 := latencies[n*50/100] / 1000
			p95 := latencies[n*95/100] / 1000
			p99 := latencies[n*99/100] / 1000
			avg := 0.0
			for _, v := range latencies {
				avg += v
			}
			avg = avg / float64(n) / 1000

			result := BenchResult{
				RepoName:    repoName,
				RepoPath:    repoPath,
				FileCount:   len(goFiles),
				ScanTimeMs:  scanTime.Milliseconds(),
				IndexTimeMs: idxTime.Milliseconds(),
				HeapMB:      heapMB,
				SearchP50Ms: p50,
				SearchP95Ms: p95,
				SearchP99Ms: p99,
				SearchAvgMs: avg,
			}

			// 输出单仓库结果
			data, _ := json.MarshalIndent(result, "", "  ")
			t.Logf("Result for %s:\n%s", repoName, string(data))

			results = append(results, result)
		})
	}

	// 所有子测试完成后，输出对比报告
	if len(results) > 0 {
		SortBenchResults(results)

		// 以第一个仓库为 baseline
		baseline := results[0].RepoName

		// Markdown 报告
		md := GenerateComparisonMarkdown(results, baseline)
		t.Logf("Comparison Report:\n%s", md)

		// JSON 报告
		jsonData, err := GenerateComparisonJSON(results, baseline, "default config (workers=2, dim=128, memory store)")
		if err != nil {
			t.Fatalf("GenerateComparisonJSON failed: %v", err)
		}

		// 输出到文件
		repoRoot := FindRepoRoot(t)
		outPath := filepath.Join(repoRoot, "build", "bench-compare.json")
		os.MkdirAll(filepath.Dir(outPath), 0755)
		if err := os.WriteFile(outPath, jsonData, 0644); err != nil {
			t.Logf("Warning: could not write comparison to %s: %v", outPath, err)
		}
		t.Logf("Comparison saved to: %s", outPath)

		fmt.Printf("\n%s\n", md)
	}
}