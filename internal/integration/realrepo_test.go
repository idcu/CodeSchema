// Package integration 提供真实仓库基准测试。
//
// 以 CodeSchema 自身仓库为测试目标，运行完整 scan→index→search 流水线，
// 采集构建耗时、内存峰值、检索延迟等关键指标。
//
// 运行方式：go test -bench=RealRepo -benchmem -timeout 300s ./internal/integration/
package integration

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"codeschema/internal/search"
)

// BenchmarkRealRepo_ScanAndIndex 基准测试：扫描真实仓库并构建索引。
func BenchmarkRealRepo_ScanAndIndex(b *testing.B) {
	repoRoot := FindRepoRoot(b)
	b.Logf("Repo root: %s", repoRoot)

	goFiles := DiscoverGoFiles(b, repoRoot)
	b.Logf("Go source files: %d", len(goFiles))

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		setup, cleanup := NewBenchSetup(b, repoRoot)

		var m0 runtime.MemStats
		runtime.ReadMemStats(&m0)

		b.StartTimer()

		// 扫描
		scanStart := time.Now()
		if err := setup.Scanner.ScanAll(context.Background(), repoRoot); err != nil {
			b.Fatalf("ScanAll failed: %v", err)
		}
		scanTime := time.Since(scanStart)

		// 构建索引
		idxStart := time.Now()
		if _, err := setup.Builder.BuildFromStore(context.Background(), setup.Store); err != nil {
			b.Fatalf("BuildFromStore failed: %v", err)
		}
		idxTime := time.Since(idxStart)

		// 内存增量
		var m1 runtime.MemStats
		runtime.ReadMemStats(&m1)
		heapMB := float64(m1.HeapInuse-m0.HeapInuse) / 1024 / 1024

		b.ReportMetric(float64(scanTime.Milliseconds()), "scan_ms")
		b.ReportMetric(float64(idxTime.Milliseconds()), "index_ms")
		b.ReportMetric(heapMB, "heap_MB")

		cleanup()
	}
}

// BenchmarkRealRepo_Search 基准测试：对真实仓库索引执行搜索。
func BenchmarkRealRepo_Search(b *testing.B) {
	repoRoot := FindRepoRoot(b)
	setup, cleanup := NewBenchSetup(b, repoRoot)
	defer cleanup()

	// 一次性扫描并构建索引
	if err := setup.Scanner.ScanAll(context.Background(), repoRoot); err != nil {
		b.Fatalf("ScanAll failed: %v", err)
	}
	if _, err := setup.Builder.BuildFromStore(context.Background(), setup.Store); err != nil {
		b.Fatalf("BuildFromStore failed: %v", err)
	}

	queries := []string{
		"Scanner", "BuildAll", "Parse",
		"Store", "Vector", "Search",
		"Adapter", "Graceful", "config",
		"analyzer", "scheduler", "IRDocument",
	}

	b.ResetTimer()

	var latencies []float64
	for i := 0; i < b.N; i++ {
		query := queries[i%len(queries)]
		start := time.Now()
		_, err := setup.Searcher.Search(context.Background(), query, search.SearchModeBoth, 10)
		latency := time.Since(start)
		if err != nil {
			b.Fatalf("Search failed: %v", err)
		}
		latencies = append(latencies, float64(latency.Microseconds()))
	}

	sort.Float64s(latencies)
	n := len(latencies)
	p50 := latencies[n*50/100]
	p95 := latencies[n*95/100]
	p99 := latencies[n*99/100]
	avg := 0.0
	for _, v := range latencies {
		avg += v
	}
	avg /= float64(n)

	b.ReportMetric(p50/1000, "p50_ms")
	b.ReportMetric(p95/1000, "p95_ms")
	b.ReportMetric(p99/1000, "p99_ms")
	b.ReportMetric(avg/1000, "avg_ms")
}

// BenchmarkRealRepo_FullPipeline 基准测试：完整流水线（扫描+索引+搜索）。
func BenchmarkRealRepo_FullPipeline(b *testing.B) {
	repoRoot := FindRepoRoot(b)

	b.ResetTimer()

	for i := 0; i < b.N; i++ {
		b.StopTimer()
		setup, cleanup := NewBenchSetup(b, repoRoot)
		b.StartTimer()

		// 扫描
		if err := setup.Scanner.ScanAll(context.Background(), repoRoot); err != nil {
			b.Fatalf("ScanAll failed: %v", err)
		}

		// 构建索引
		if _, err := setup.Builder.BuildFromStore(context.Background(), setup.Store); err != nil {
			b.Fatalf("BuildFromStore failed: %v", err)
		}

		// 搜索（3 个查询取平均）
		queries := []string{"Scanner", "BuildAll", "Parse"}
		var totalLatency float64
		for _, q := range queries {
			start := time.Now()
			_, err := setup.Searcher.Search(context.Background(), q, search.SearchModeBoth, 10)
			totalLatency += float64(time.Since(start).Microseconds())
			if err != nil {
				b.Fatalf("Search(%q) failed: %v", q, err)
			}
		}
		avgLatency := totalLatency / float64(len(queries)) / 1000

		b.ReportMetric(avgLatency, "search_avg_ms")

		cleanup()
	}
}

// TestRealRepo_CollectMetrics 集成测试：采集真实仓库性能指标并输出 JSON。
func TestRealRepo_CollectMetrics(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping real-repo benchmark in short mode")
	}

	repoRoot := FindRepoRoot(t)
	t.Logf("Target repo: %s", repoRoot)

	goFiles := DiscoverGoFiles(t, repoRoot)
	t.Logf("Go source files: %d", len(goFiles))

	// 初始化组件
	setup, cleanup := NewBenchSetup(t, repoRoot)
	defer cleanup()
	ctx := context.Background()

	// 记录内存基准
	var m0 runtime.MemStats
	runtime.ReadMemStats(&m0)

	// 扫描
	scanStart := time.Now()
	if err := setup.Scanner.ScanAll(ctx, repoRoot); err != nil {
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
		"GracefulManager", "RecoveryHandler", "SCIPAdapter", "LSPAdapter",
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
		RepoName:    "codeschema",
		RepoPath:    repoRoot,
		FileCount:   len(goFiles),
		ScanTimeMs:  scanTime.Milliseconds(),
		IndexTimeMs: idxTime.Milliseconds(),
		HeapMB:      heapMB,
		SearchP50Ms: p50,
		SearchP95Ms: p95,
		SearchP99Ms: p99,
		SearchAvgMs: avg,
	}

	// 输出 JSON 结果
	data, _ := json.MarshalIndent(result, "", "  ")
	t.Logf("Benchmark Results:\n%s", string(data))

	// 同时输出到文件
	outPath := filepath.Join(repoRoot, "build", "realrepo-bench.json")
	os.MkdirAll(filepath.Dir(outPath), 0755)
	if err := os.WriteFile(outPath, data, 0644); err != nil {
		t.Logf("Warning: could not write results to %s: %v", outPath, err)
	}

	t.Logf("Results saved to: %s", outPath)

	// 阈值验证
	if scanTime.Minutes() > 5 {
		t.Errorf("Scan took too long: %v (expected < 5min)", scanTime)
	}
	if idxTime.Minutes() > 5 {
		t.Errorf("Index build took too long: %v (expected < 5min)", idxTime)
	}
	if p95 > 500 {
		t.Logf("Warning: P95 search latency %.1fms exceeds 500ms threshold", p95)
	}
}