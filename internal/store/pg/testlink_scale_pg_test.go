//go:build pg && redis

// PG 后端测试关联规模基准（/tests 与 /affected）。
//
// 门控：
//
//	CODESCHEMA_TESTLINK_SCALE=1 CODESCHEMA_BENCH_TAG=before|after \
//	  CODESCHEMA_REDIS_ADDR=redis://127.0.0.1:6379/0 \
//	  go test -tags 'pg redis' -run '^TestFindTestLinksScalePG$' -v -timeout 900s ./internal/store/pg
//
// 隔离 PG 库写入 1,000 文件（500 生产 + 500 测试）；测量冷启动与 10 次调用的
// P50/P95。改动前每次调用都会全量扫描（约 12 s），改动后热路径应低于 500 ms。
// 产物：docs/bench/2026-11-14/testlink-scale-pg1000-<tag>.json。
package pg

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

	"github.com/idcu/codeschema/internal/analyzer"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/service"
)

const testLinkScaleGate = "CODESCHEMA_TESTLINK_SCALE"

type pgTestLinkLatency struct {
	ColdMS float64 `json:"cold_ms"`
	N      int     `json:"n"`
	P50MS  float64 `json:"p50_ms"`
	P95MS  float64 `json:"p95_ms"`
	P99MS  float64 `json:"p99_ms"`
	MaxMS  float64 `json:"max_ms"`
}

type pgTestLinkScaleResult struct {
	Corpus         string            `json:"corpus"`
	Store          string            `json:"store"`
	Tag            string            `json:"tag"`
	GoVersion      string            `json:"go_version"`
	Target         string            `json:"target"`
	Files          int               `json:"files"`
	Links          int               `json:"links"`
	LinksSHA256    string            `json:"links_sha256"`
	Affected       int               `json:"affected"`
	AffectedSHA256 string            `json:"affected_sha256"`
	Tests          pgTestLinkLatency `json:"tests"`
	AffectedLat    pgTestLinkLatency `json:"affected_latency"`
	GeneratedAt    string            `json:"generated_at"`
}

// TestFindTestLinksScalePG 测量 PG 后端下 1,000 文件的 /tests 与 /affected 延迟。
func TestFindTestLinksScalePG(t *testing.T) {
	if os.Getenv(testLinkScaleGate) == "" {
		t.Skip("set CODESCHEMA_TESTLINK_SCALE=1 to run the PG test-link scale benchmark")
	}
	tag := os.Getenv("CODESCHEMA_BENCH_TAG")
	if tag == "" {
		tag = "current"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Second)
	defer cancel()
	dsn, cleanup := withIsolatedPG(t, ctx)
	defer cleanup()

	st := NewPGStore()
	if err := st.Open(ctx, dsn); err != nil {
		t.Fatalf("PG open: %v", err)
	}
	defer st.Close()
	if err := st.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	root := t.TempDir()
	const pairs = 500
	for i := 0; i < pairs; i++ {
		pkg := fmt.Sprintf("benchpkg%d", i/5)
		prodClass := fmt.Sprintf("%s.Svc%d", pkg, i)
		testMethod := fmt.Sprintf("%s.TestHandle%d", pkg, i)
		prodDoc := &parser.IRDocument{
			Source: "scale-pg", Language: "go",
			FilePath: filepath.Join(root, pkg, fmt.Sprintf("f%d.go", i)), FileHash: fmt.Sprintf("prod-%d", i),
			Classes: []parser.ClassIR{{Name: fmt.Sprintf("Svc%d", i), FullName: prodClass, Type: "CLASS"}},
			Methods: []parser.MethodIR{
				{Name: "handle", ClassFQN: prodClass},
				{Name: "helper", ClassFQN: prodClass},
			},
		}
		testDoc := &parser.IRDocument{
			Source: "scale-pg", Language: "go",
			FilePath: filepath.Join(root, pkg, fmt.Sprintf("f%d_test.go", i)), FileHash: fmt.Sprintf("test-%d", i),
			Classes: []parser.ClassIR{{Name: pkg, FullName: pkg, Type: "PACKAGE"}},
			Methods: []parser.MethodIR{{Name: fmt.Sprintf("TestHandle%d", i), ClassFQN: pkg}},
			Calls: []parser.CallIR{{
				CallerFQN: testMethod, CalleeFQN: prodClass + ".handle", CallType: "direct", LineNumber: 10,
			}},
		}
		if err := st.UpsertIR(ctx, prodDoc); err != nil {
			t.Fatalf("upsert prod %d: %v", i, err)
		}
		if err := st.UpsertIR(ctx, testDoc); err != nil {
			t.Fatalf("upsert test %d: %v", i, err)
		}
	}

	svc := service.NewService(st).WithImpactAnalyzer(analyzer.NewAnalyzer(st))
	target := "benchpkg0.Svc0.handle"

	start := time.Now()
	links, err := svc.FindTestLinks(ctx, target, 60)
	if err != nil {
		t.Fatalf("cold FindTestLinks: %v", err)
	}
	coldTests := float64(time.Since(start)) / float64(time.Millisecond)

	start = time.Now()
	affected, err := svc.GetAffected(ctx, target, false)
	if err != nil {
		t.Fatalf("cold GetAffected: %v", err)
	}
	coldAffected := float64(time.Since(start)) / float64(time.Millisecond)

	samples := pgEnvInt("CODESCHEMA_TESTLINK_SAMPLES", 10)
	warmup := pgEnvInt("CODESCHEMA_TESTLINK_WARMUP", 2)
	tests := measurePGTestLinkLatency(samples, warmup, func() {
		if _, err := svc.FindTestLinks(ctx, target, 60); err != nil {
			t.Fatalf("FindTestLinks: %v", err)
		}
	})
	tests.ColdMS = coldTests
	affectedLat := measurePGTestLinkLatency(samples, warmup, func() {
		if _, err := svc.GetAffected(ctx, target, false); err != nil {
			t.Fatalf("GetAffected: %v", err)
		}
	})
	affectedLat.ColdMS = coldAffected

	res := pgTestLinkScaleResult{
		Corpus: "pg1000", Store: "postgres", Tag: tag, GoVersion: runtime.Version(),
		Target: target, Files: pairs * 2,
		Links: len(links), LinksSHA256: pgHashJSON(links),
		Affected: len(affected), AffectedSHA256: pgHashStrings(affected),
		Tests: tests, AffectedLat: affectedLat,
		GeneratedAt: time.Now().UTC().Format(time.RFC3339),
	}
	writeBenchJSON(t, "testlink-scale-pg1000-"+tag+".json", res)

	t.Logf("pg1000: files=%d links=%d affected=%d tests cold=%.0fms p50=%.0fms p95=%.0fms affected p50=%.0fms p95=%.0fms",
		res.Files, res.Links, res.Affected,
		tests.ColdMS, tests.P50MS, tests.P95MS, affectedLat.P50MS, affectedLat.P95MS)

	// 红→绿门禁：热路径 P95 必须低于 500 ms。改动前每次调用约 12 s。
	if tests.P95MS >= 500 {
		t.Fatalf("FindTestLinks P95=%.0fms，超过 500ms 上限（改动前约 12000ms）", tests.P95MS)
	}
}

func measurePGTestLinkLatency(samples, warmup int, fn func()) pgTestLinkLatency {
	for i := 0; i < warmup; i++ {
		fn()
	}
	durs := make([]time.Duration, 0, samples)
	for i := 0; i < samples; i++ {
		start := time.Now()
		fn()
		durs = append(durs, time.Since(start))
	}
	sort.Slice(durs, func(i, j int) bool { return durs[i] < durs[j] })
	at := func(p int) float64 {
		return float64(durs[(len(durs)-1)*p/100]) / float64(time.Millisecond)
	}
	return pgTestLinkLatency{
		N: samples, P50MS: at(50), P95MS: at(95), P99MS: at(99),
		MaxMS: float64(durs[len(durs)-1]) / float64(time.Millisecond),
	}
}

func pgHashJSON(v any) string {
	buf, _ := json.Marshal(v)
	sum := sha256.Sum256(buf)
	return hex.EncodeToString(sum[:])
}

func pgHashStrings(items []string) string {
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

// pgEnvInt 读取正整数环境变量，未设置或非法时返回 def。
func pgEnvInt(key string, def int) int {
	v, err := strconv.Atoi(os.Getenv(key))
	if err != nil || v <= 0 {
		return def
	}
	return v
}
