// 测试关联规模基准（/tests 与 /affected）。
//
// 门控（默认不参与 make test）：
//
//	CODESCHEMA_TESTLINK_SCALE=1 CODESCHEMA_BENCH_TAG=before|after \
//	  go test -run '^TestTestLinkScale$' -v -timeout 1800s ./internal/scalebench
//
// 语料：
//   - 1000：1,000 个合成 Go 文件（500 生产 + 500 测试，直接写入 IR，隔离解析开销）；
//   - goroot：$(go env GOROOT)/src 真实标准库源码（与 perf-baseline 的 large 语料一致）。
//
// 输出：docs/bench/2026-11-14/testlink-scale-<corpus>-<tag>.json。
// 每条记录包含冷启动、热路径 P50/P95/P99，以及结果哈希（用于前后逐条一致比对）。
package scalebench

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/parser"
	rt "github.com/idcu/codeschema/internal/runtime"
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
)

const testLinkScaleGate = "CODESCHEMA_TESTLINK_SCALE"

type testLinkLatency struct {
	ColdMS float64 `json:"cold_ms"`
	N      int     `json:"n"`
	P50MS  float64 `json:"p50_ms"`
	P95MS  float64 `json:"p95_ms"`
	P99MS  float64 `json:"p99_ms"`
	MaxMS  float64 `json:"max_ms"`
	// TotalAllocBytes 是采样窗口内 runtime.MemStats.TotalAlloc 的增量，
	// 只统计 N 次采样（不含 warmup），用于衡量每符号的分配放大。
	TotalAllocBytes uint64  `json:"total_alloc_bytes"`
	AllocPerOpBytes float64 `json:"alloc_per_op_bytes"`
}

type testLinkScaleResult struct {
	Corpus         string          `json:"corpus"`
	Store          string          `json:"store"`
	Tag            string          `json:"tag"`
	GoVersion      string          `json:"go_version"`
	Target         string          `json:"target"`
	Files          int             `json:"files"`
	Links          int             `json:"links"`
	LinksSHA256    string          `json:"links_sha256"`
	Affected       int             `json:"affected"`
	AffectedSHA256 string          `json:"affected_sha256"`
	Tests          testLinkLatency `json:"tests"`
	AffectedLat    testLinkLatency `json:"affected_latency"`
	GeneratedAt    string          `json:"generated_at"`
}

// TestTestLinkScale 测量 /tests 与 /affected 在两种语料上的冷/热延迟。
func TestTestLinkScale(t *testing.T) {
	if os.Getenv(testLinkScaleGate) == "" {
		t.Skip("set CODESCHEMA_TESTLINK_SCALE=1 to run test-link scale benchmarks")
	}
	tag := os.Getenv("CODESCHEMA_BENCH_TAG")
	if tag == "" {
		tag = "current"
	}
	outDir := filepath.Join(repoRoot(), perfOutSubdir)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}

	t.Run("1000", func(t *testing.T) {
		st, svc := newSyntheticTestLinkStore(t)
		defer st.Close()
		files, err := st.GetAllFiles(context.Background())
		if err != nil {
			t.Fatalf("get files: %v", err)
		}
		res := measureTestLinkScale(t, svc, "1000", "file", tag, "benchpkg0.Svc0.handle", len(files),
			envInt("CODESCHEMA_TESTLINK_SAMPLES", 200), envInt("CODESCHEMA_TESTLINK_WARMUP", 5))
		writeTestLinkScaleJSON(t, filepath.Join(outDir, "testlink-scale-1000-"+tag+".json"), res)
	})

	t.Run("goroot", func(t *testing.T) {
		root := filepath.Join(runtime.GOROOT(), "src")
		if _, err := os.Stat(root); err != nil {
			t.Skipf("GOROOT src not found: %s", root)
		}
		st, svc := newGorootTestLinkStore(t, root)
		defer st.Close()
		files, err := st.GetAllFiles(context.Background())
		if err != nil {
			t.Fatalf("get files: %v", err)
		}
		target := pickGorootTestLinkTarget(t, svc)
		res := measureTestLinkScale(t, svc, "goroot", "file", tag, target, len(files),
			envInt("CODESCHEMA_TESTLINK_SAMPLES", 10), envInt("CODESCHEMA_TESTLINK_WARMUP", 2))
		writeTestLinkScaleJSON(t, filepath.Join(outDir, "testlink-scale-goroot-"+tag+".json"), res)
	})
}

// measureTestLinkScale 先测冷启动（首次调用），再做 warmup + N 次采样。
func measureTestLinkScale(t *testing.T, svc *service.Service, corpus, storeType, tag, target string, files, samples, warmup int) testLinkScaleResult {
	t.Helper()
	ctx := context.Background()
	res := testLinkScaleResult{
		Corpus: corpus, Store: storeType, Tag: tag, GoVersion: runtime.Version(),
		Target: target, Files: files,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}

	start := time.Now()
	links, err := svc.FindTestLinks(ctx, target, 60)
	if err != nil {
		t.Fatalf("cold FindTestLinks: %v", err)
	}
	coldTests := msSince(start)
	res.Links = len(links)
	res.LinksSHA256 = hashJSON(links)

	start = time.Now()
	affected, err := svc.GetAffected(ctx, target, false)
	if err != nil {
		t.Fatalf("cold GetAffected: %v", err)
	}
	coldAffected := msSince(start)
	res.Affected = len(affected)
	res.AffectedSHA256 = hashStrings(affected)

	res.Tests = measureTestLinkLatency(samples, warmup, func() {
		if _, err := svc.FindTestLinks(ctx, target, 60); err != nil {
			t.Fatalf("FindTestLinks: %v", err)
		}
	})
	res.Tests.ColdMS = coldTests
	res.AffectedLat = measureTestLinkLatency(samples, warmup, func() {
		if _, err := svc.GetAffected(ctx, target, false); err != nil {
			t.Fatalf("GetAffected: %v", err)
		}
	})
	res.AffectedLat.ColdMS = coldAffected

	t.Logf("%s: files=%d target=%s links=%d affected=%d tests cold=%.0fms p50=%.1fms p95=%.1fms alloc=%.0fB/op affected p50=%.1fms p95=%.1fms alloc=%.0fB/op",
		corpus, files, target, res.Links, res.Affected,
		res.Tests.ColdMS, res.Tests.P50MS, res.Tests.P95MS, res.Tests.AllocPerOpBytes,
		res.AffectedLat.P50MS, res.AffectedLat.P95MS, res.AffectedLat.AllocPerOpBytes)
	return res
}

// measureTestLinkLatency 做 warmup 后采样，返回 nearest-rank 分位与采样期分配量。
func measureTestLinkLatency(samples, warmup int, fn func()) testLinkLatency {
	for i := 0; i < warmup; i++ {
		fn()
	}
	var memBefore, memAfter runtime.MemStats
	runtime.ReadMemStats(&memBefore)
	durs := make([]time.Duration, 0, samples)
	for i := 0; i < samples; i++ {
		start := time.Now()
		fn()
		durs = append(durs, time.Since(start))
	}
	runtime.ReadMemStats(&memAfter)
	alloc := memAfter.TotalAlloc - memBefore.TotalAlloc
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	return testLinkLatency{
		N:               samples,
		P50MS:           durationMS(durs[(len(durs)-1)*50/100]),
		P95MS:           durationMS(durs[(len(durs)-1)*95/100]),
		P99MS:           durationMS(durs[(len(durs)-1)*99/100]),
		MaxMS:           durationMS(durs[len(durs)-1]),
		TotalAllocBytes: alloc,
		AllocPerOpBytes: float64(alloc) / float64(samples),
	}
}

func durationMS(d time.Duration) float64 { return float64(d) / float64(time.Millisecond) }
func msSince(start time.Time) float64    { return durationMS(time.Since(start)) }

// envInt 读取正整数环境变量，未设置或非法时返回 def。
func envInt(key string, def int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v <= 0 {
		return def
	}
	return v
}

// hashJSON 生成关联结果的内容哈希。
//
// file 后端的 GetAllFiles 按 map 遍历返回文件，等置信度关联项的先后顺序跨进程
// 不稳定；这里按（置信度、测试方法、策略、方法名）规范化后再哈希，
// 使哈希只反映「逐条内容」是否一致，而不是不确定的顺序。
func hashJSON(v any) string {
	if links, ok := v.([]service.TestLink); ok {
		cp := append([]service.TestLink(nil), links...)
		sort.Slice(cp, func(i, j int) bool {
			if cp[i].Confidence != cp[j].Confidence {
				return cp[i].Confidence > cp[j].Confidence
			}
			if cp[i].TestMethod != cp[j].TestMethod {
				return cp[i].TestMethod < cp[j].TestMethod
			}
			if cp[i].Strategy != cp[j].Strategy {
				return cp[i].Strategy < cp[j].Strategy
			}
			return cp[i].MethodName < cp[j].MethodName
		})
		v = cp
	}
	buf, _ := json.Marshal(v)
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func hashStrings(items []string) string {
	cp := append([]string(nil), items...)
	sort.Strings(cp)
	buf := make([]byte, 0, len(cp)*32)
	for _, s := range cp {
		buf = append(buf, s...)
		buf = append(buf, '\n')
	}
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func writeTestLinkScaleJSON(t *testing.T, path string, v any) {
	t.Helper()
	buf, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	if err := os.WriteFile(path, append(buf, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

// newSyntheticTestLinkStore 构造 1,000 文件（500 生产 + 500 测试）的 Go 语料。
//
// 每 5 对文件一个包；测试方法显式调用同包生产方法，覆盖 naming + call_graph +
// same-dir dependency。直接写 IR，排除解析开销，只测关联扫描本身。
func newSyntheticTestLinkStore(t *testing.T) (store.Store, *service.Service) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	cfg := perfConfig(perfCorpus{Key: "testlink-1000", Root: dir, Lang: "go", Exts: []string{".go"}}, dir)
	st := store.NewStore("file")
	if err := st.Open(ctx, cfg.Storage.DSN); err != nil {
		t.Fatalf("open store: %v", err)
	}

	const pairs = 500
	for i := 0; i < pairs; i++ {
		pkg := fmt.Sprintf("benchpkg%d", i/5)
		prodClass := fmt.Sprintf("%s.Svc%d", pkg, i)
		testMethod := fmt.Sprintf("%s.TestHandle%d", pkg, i)
		prodPath := filepath.Join(dir, pkg, fmt.Sprintf("f%d.go", i))
		testPath := filepath.Join(dir, pkg, fmt.Sprintf("f%d_test.go", i))

		if err := st.UpsertIR(ctx, &parser.IRDocument{
			Source: "synthetic", Language: "go", FilePath: prodPath, FileHash: fmt.Sprintf("prod-%d", i),
			Classes: []parser.ClassIR{{
				Name: fmt.Sprintf("Svc%d", i), FullName: prodClass, Type: "CLASS",
			}},
			Methods: []parser.MethodIR{
				{Name: "handle", ClassFQN: prodClass},
				{Name: "helper", ClassFQN: prodClass},
			},
		}); err != nil {
			t.Fatalf("upsert prod %d: %v", i, err)
		}
		if err := st.UpsertIR(ctx, &parser.IRDocument{
			Source: "synthetic", Language: "go", FilePath: testPath, FileHash: fmt.Sprintf("test-%d", i),
			Classes: []parser.ClassIR{{
				Name: pkg, FullName: pkg, Type: "PACKAGE",
			}},
			Methods: []parser.MethodIR{
				{Name: fmt.Sprintf("TestHandle%d", i), ClassFQN: pkg},
			},
			Calls: []parser.CallIR{{
				CallerFQN: testMethod, CalleeFQN: prodClass + ".handle", CallType: "direct", LineNumber: 10,
			}},
		}); err != nil {
			t.Fatalf("upsert test %d: %v", i, err)
		}
	}

	run, err := rt.BuildRuntime(ctx, st, cfg)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	return st, run.Svc
}

// newGorootTestLinkStore 扫描 GOROOT/src 并构建运行时。
func newGorootTestLinkStore(t *testing.T, root string) (store.Store, *service.Service) {
	t.Helper()
	ctx := context.Background()
	dir := t.TempDir()
	cfg := perfConfig(perfCorpus{Key: "goroot", Root: root, Lang: "go", Exts: []string{".go"}}, dir)
	cfg.Project.Root = root
	st := store.NewStore("file")
	if err := st.Open(ctx, cfg.Storage.DSN); err != nil {
		t.Fatalf("open store: %v", err)
	}
	if err := rt.ScanRepository(ctx, st, cfg, root); err != nil {
		// ScanRepository 逐文件聚合错误，返回非 nil 仍可能有可用数据。
		t.Logf("scan repository: %v", err)
	}
	run, err := rt.BuildRuntime(ctx, st, cfg)
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	return st, run.Svc
}

// pickGorootTestLinkTarget 选一个确有测试关联的标准库方法。
func pickGorootTestLinkTarget(t *testing.T, svc *service.Service) string {
	t.Helper()
	ctx := context.Background()
	candidates := []string{
		"path/filepath.Clean",
		"strings.TrimSpace",
		"sort.Search",
		"net/url.Parse",
	}
	for _, target := range candidates {
		links, err := svc.FindTestLinks(ctx, target, 60)
		if err == nil && len(links) > 0 {
			return target
		}
	}
	return candidates[0]
}
