// 性能基线基准（S-3）：真实仓库端到端索引 + HTTP 查询延迟 + 多租户并发隔离。
//
// 门控（默认不参与 make test）：
//
//	CODESCHEMA_PERF_BASELINE=1 go test -run '^TestPerfBaseline_IndexAndQuery$' -v -timeout 1800s ./internal/scalebench
//	CODESCHEMA_PERF_BASELINE=1 go test -run '^TestPerfBaseline_MultiTenantConcurrency$' -v -timeout 1800s ./internal/scalebench
//
// 语料（可用环境变量覆盖）：
//
//	PERF_REPO_GO   默认 /Volumes/Data/code-schema
//	PERF_REPO_JAVA 默认 /tmp/codeschema-perf-corpus/spring-petclinic（git clone --depth 1，HEAD=500158f）
//	PERF_REPO_TS   默认 /Volumes/Data/lytd
//	PERF_REPO_LARGE 默认 $(go env GOROOT)/src
//
// 输出：docs/bench/2026-11-14/perf-<corpus>.json 与 perf-multitenant.json。
// 数值仅作基线记录，不构成优化结论。
package scalebench

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/config"
	"github.com/idcu/codeschema/internal/ignore"
	"github.com/idcu/codeschema/internal/parser/adapter"
	rt "github.com/idcu/codeschema/internal/runtime"
	"github.com/idcu/codeschema/internal/server"
	"github.com/idcu/codeschema/internal/store"
	sqlitestore "github.com/idcu/codeschema/internal/store/sqlite"
	"github.com/idcu/codeschema/internal/tenant"
)

const (
	perfGate       = "CODESCHEMA_PERF_BASELINE"
	perfOutSubdir  = "docs/bench/2026-11-14"
	perfSamples    = 200
	perfWarmup     = 10
	perfVectorDim  = 256
	perfSampleTick = 20 * time.Millisecond
)

// perfCorpus 一个待测语料。
type perfCorpus struct {
	Key    string
	Name   string
	Lang   string
	Root   string
	Commit string
	Query  string   // 用于选取真实符号的检索词
	Exts   []string // 主语言扩展名：符号计数与端点选样的过滤口径
}

// perfCorpora 返回四份语料：三个既有语料 + GOROOT 标准库源码（明显更大的真实仓库）。
func perfCorpora() []perfCorpus {
	env := func(k, def string) string {
		if v := os.Getenv(k); v != "" {
			return v
		}
		return def
	}
	return []perfCorpus{
		{Key: "go", Name: "code-schema", Lang: "go", Root: env("PERF_REPO_GO", "/Volumes/Data/code-schema"), Query: "Get", Exts: []string{".go"}},
		{Key: "java", Name: "s2-petclinic", Lang: "java", Root: env("PERF_REPO_JAVA", "/tmp/codeschema-perf-corpus/spring-petclinic"), Commit: "500158f", Query: "Owner", Exts: []string{".java"}},
		{Key: "ts", Name: "lytd", Lang: "ts", Root: env("PERF_REPO_TS", "/Volumes/Data/lytd"), Commit: "f5e6604", Query: "get", Exts: []string{".ts", ".tsx", ".js", ".jsx"}},
		{Key: "large", Name: "go-stdlib", Lang: "go", Root: env("PERF_REPO_LARGE", filepath.Join(runtime.GOROOT(), "src")), Query: "Get", Exts: []string{".go"}},
	}
}

type memSample struct {
	PeakHeapAlloc uint64 `json:"peak_heap_alloc_bytes"`
	PeakHeapInuse uint64 `json:"peak_heap_inuse_bytes"`
	PeakHeapSys   uint64 `json:"peak_heap_sys_bytes"`
	TotalAlloc    uint64 `json:"total_alloc_bytes"`
	LiveHeapAlloc uint64 `json:"live_heap_alloc_bytes"`
}

type phaseResult struct {
	DurationMS    float64 `json:"duration_ms"`
	Files         int     `json:"files"`
	Classes       int     `json:"classes"`
	Methods       int     `json:"methods"`
	Calls         int     `json:"calls"`
	Symbols       int     `json:"symbols"`
	FilesPerSec   float64 `json:"files_per_sec"`
	SymbolsPerSec float64 `json:"symbols_per_sec"`
	Error         string  `json:"error,omitempty"`
}

type endpointResult struct {
	N      int     `json:"n"`
	Errors int     `json:"errors"`
	P50MS  float64 `json:"p50_ms"`
	P95MS  float64 `json:"p95_ms"`
	P99MS  float64 `json:"p99_ms"`
	MaxMS  float64 `json:"max_ms"`
	MeanMS float64 `json:"mean_ms"`
}

type corpusResult struct {
	Corpus         string                    `json:"corpus"`
	Root           string                    `json:"root"`
	Commit         string                    `json:"commit,omitempty"`
	Language       string                    `json:"language"`
	SupportedFiles int                       `json:"supported_files"`
	CodeFiles      int                       `json:"code_files"`
	LoC            int                       `json:"loc"`
	ByLang         map[string]int            `json:"by_lang"`
	Scan           phaseResult               `json:"scan"`
	Index          phaseResult               `json:"index"`
	Memory         memSample                 `json:"memory"`
	MemoryNote     string                    `json:"memory_note"`
	Endpoints      map[string]endpointResult `json:"endpoints"`
	Symbols        map[string]string         `json:"symbols_used"`
	Notes          []string                  `json:"notes,omitempty"`
}

// TestPerfBaseline_IndexAndQuery 单租户逐语料测索引吞吐、内存峰值与三端点延迟。
func TestPerfBaseline_IndexAndQuery(t *testing.T) {
	if os.Getenv(perfGate) == "" {
		t.Skip("set CODESCHEMA_PERF_BASELINE=1 to run the real-repo performance baseline")
	}
	outDir := filepath.Join(repoRoot(), perfOutSubdir)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}
	for _, c := range perfCorpora() {
		c := c
		t.Run(c.Key, func(t *testing.T) {
			res, ok := runPerfCorpus(t, c)
			if !ok {
				t.Skipf("corpus not found: %s", c.Root)
			}
			writePerfJSON(t, filepath.Join(outDir, "perf-"+c.Key+".json"), res)
		})
	}
}

// runPerfCorpus 对一个语料执行 scan → index → HTTP 延迟测量。
func runPerfCorpus(t *testing.T, c perfCorpus) (corpusResult, bool) {
	t.Helper()
	if _, err := os.Stat(c.Root); err != nil {
		return corpusResult{}, false
	}
	scanRoot := perfScanRoot(t, c.Root)
	files, codeFiles, loc, byLang := statPerfCorpus(scanRoot, c.Exts)
	res := corpusResult{
		Corpus: c.Key, Root: c.Root, Commit: c.Commit, Language: c.Lang,
		SupportedFiles: files, CodeFiles: codeFiles, LoC: loc, ByLang: byLang,
		Endpoints: map[string]endpointResult{},
		Symbols:   map[string]string{},
		MemoryNote: fmt.Sprintf("采样口径：%s 间隔读取 runtime.MemStats；索引结束后强制 GC 记录 live heap；非 cgroup 峰值",
			perfSampleTick),
	}
	if scanRoot != c.Root {
		res.Notes = append(res.Notes, "本仓语料以 git archive HEAD 导出到临时目录后扫描，排除未跟踪输入目录")
	}

	dir := t.TempDir()
	cfg := perfConfig(c, dir)
	cfg.Project.Root = scanRoot
	st := store.NewStore("file")
	if err := st.Open(context.Background(), cfg.Storage.DSN); err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	ctx := context.Background()
	stopMem := startMemSampler(perfSampleTick)

	scanStart := time.Now()
	scanErr := rt.ScanRepository(ctx, st, cfg, scanRoot)
	scanDur := time.Since(scanStart)
	classes, methods, calls := collectStoreStats(ctx, st, c.Exts)
	res.Scan = phaseResult{
		DurationMS: ms(scanDur), Files: files, Classes: classes, Methods: methods,
		Calls: calls, Symbols: classes + methods,
		FilesPerSec:   rate(files, scanDur),
		SymbolsPerSec: rate(classes+methods, scanDur),
	}
	if scanErr != nil {
		res.Scan.Error = scanErr.Error()
		res.Notes = append(res.Notes, "scan 返回非 nil（逐文件错误聚合），吞吐仍按已处理文件计")
	}

	idxStart := time.Now()
	run, err := rt.BuildRuntime(ctx, st, cfg)
	idxDur := time.Since(idxStart)
	res.Memory = stopMem()
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	res.Index = phaseResult{
		DurationMS: ms(idxDur), Files: files, Classes: classes, Methods: methods,
		Calls: calls, Symbols: classes + methods,
		FilesPerSec:   rate(files, idxDur),
		SymbolsPerSec: rate(classes+methods, idxDur),
	}

	// HTTP 端点延迟：真实进程内监听 + 真实 HTTP 请求（非直接调函数）。
	addr, err := freeAddr()
	if err != nil {
		t.Fatalf("free addr: %v", err)
	}
	srv := server.NewHTTPServer(run.Svc, addr)
	srvCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	defer func() { _ = srv.Stop() }()
	go func() { _ = srv.Start(srvCtx) }()
	if err := waitHTTPReady(addr, 5*time.Second); err != nil {
		t.Fatalf("http server not ready: %v", err)
	}

	ctxSym, impactSym, query := pickPerfSymbols(ctx, st, c.Exts)
	res.Symbols["context"] = ctxSym
	res.Symbols["impact"] = impactSym
	res.Symbols["search_query"] = query
	t.Logf("%s symbols: context=%s impact=%s query=%s", c.Key, ctxSym, impactSym, query)
	if ctxSym == "" {
		res.Notes = append(res.Notes, "检索未选出可用符号，端点延迟标记为 skipped")
	} else {
		client := &http.Client{Timeout: 20 * time.Second}
		base := "http://" + addr
		res.Endpoints["context"] = benchEndpoint(client, base+"/context?symbol="+url.QueryEscape(ctxSym)+"&max_bytes=4096")
		res.Endpoints["impact"] = benchEndpoint(client, base+"/impact?method="+url.QueryEscape(impactSym)+"&depth=2")
		res.Endpoints["search"] = benchEndpoint(client, base+"/search?q="+url.QueryEscape(query)+"&mode=exact&limit=20")
	}

	t.Logf("%s: files=%d loc=%d scan=%.0fms (%.1f files/s) index=%.0fms heapPeak=%.1fMB",
		c.Key, files, loc, res.Scan.DurationMS, res.Scan.FilesPerSec,
		res.Index.DurationMS, float64(res.Memory.PeakHeapAlloc)/1024/1024)
	return res, true
}

// perfConfig 构造基准配置：file 后端（生产默认），FTS/向量/IDF 全部落在临时目录。
// 向量维度显式降到 256，使 GOROOT 2.87M LoC 可在单机完成；报告需注明该口径。
func perfConfig(c perfCorpus, dir string) *config.Config {
	cfg := config.DefaultConfig()
	cfg.Project.Name = c.Key
	cfg.Project.Root = c.Root
	cfg.Project.Languages = []string{c.Lang}
	cfg.Storage.Driver = "file"
	cfg.Storage.DSN = filepath.Join(dir, "store")
	cfg.Storage.Vector.Driver = ""
	cfg.Storage.Search.FTSDir = filepath.Join(dir, "fts")
	cfg.Storage.Search.VectorDir = filepath.Join(dir, "vector")
	cfg.Storage.Search.IDFDir = filepath.Join(dir, "idf")
	cfg.Storage.Search.VectorDim = perfVectorDim
	cfg.Parser.LSP.Enabled = false
	prepareLocalModel(cfg, dir)
	return cfg
}

// prepareLocalModel 预置占位模型文件：默认构建未编译 ONNX（-tags onnx），但仍会
// 尝试下载模型；网络耗时若计入 scan 口径会污染吞吐。占位文件使 Ensure 直接短路，
// 稳定回退 LocalEmbedder（报告注明该口径）。
func prepareLocalModel(cfg *config.Config, dir string) {
	modelDir := filepath.Join(dir, "model")
	_ = os.MkdirAll(filepath.Join(modelDir, "onnx"), 0o755)
	_ = os.WriteFile(filepath.Join(modelDir, "onnx", "model.onnx"), []byte("placeholder"), 0o644)
	_ = os.WriteFile(filepath.Join(modelDir, "tokenizer.json"), []byte("{}"), 0o644)
	cfg.Storage.Vector.ModelDir = modelDir
}

// perfScanRoot 对本仓语料用 `git archive HEAD` 导出干净工作树，避免把 untracked
// 复审输入目录（clone 的第三方仓库）算进语料；其他语料原样返回。
func perfScanRoot(t *testing.T, root string) string {
	t.Helper()
	absRoot, err1 := filepath.Abs(root)
	absRepo, err2 := filepath.Abs(repoRoot())
	if err1 != nil || err2 != nil || absRoot != absRepo {
		return root
	}
	if _, err := os.Stat(filepath.Join(root, ".git")); err != nil {
		return root
	}
	dst := t.TempDir()
	cmd := exec.Command("sh", "-c", "git archive HEAD | tar -x -C "+dst)
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("export clean tree: %v (%s)", err, strings.TrimSpace(string(out)))
	}
	return dst
}

// statPerfCorpus 按 scanner 的忽略清单统计受支持文件、主语言源码文件与行数。
func statPerfCorpus(root string, primaryExts []string) (files, codeFiles, loc int, byLang map[string]int) {
	byLang = map[string]int{}
	isPrimary := func(path string) bool {
		ext := filepath.Ext(path)
		for _, e := range primaryExts {
			if ext == e {
				return true
			}
		}
		return false
	}
	dirs := ignore.DirSet("cs_ab_tmp")
	fileSet := ignore.FileSet()
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			if dirs[info.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		if fileSet[info.Name()] {
			return nil
		}
		lang := adapter.ExtToLang(filepath.Ext(path))
		if lang == "" || lang == "unknown" {
			return nil // scanner 对 unknown 只记 parse_skipped，不计入有效吞吐
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		files++
		byLang[lang]++
		if isPrimary(path) {
			codeFiles++
			loc += strings.Count(string(data), "\n")
			if len(data) > 0 && data[len(data)-1] != '\n' {
				loc++
			}
		}
		return nil
	})
	return files, codeFiles, loc, byLang
}

// collectStoreStats 统计主语言源码文件的类/方法/调用数（file 后端为内存查询）。
// 文档/配置类文件也会被通用适配器入库，但符号口径只认主语言，避免 Markdown
// 标题被当作类污染吞吐与端点选样。
func collectStoreStats(ctx context.Context, st store.Store, primaryExts []string) (classes, methods, calls int) {
	files, err := st.GetAllFiles(ctx)
	if err != nil {
		return 0, 0, 0
	}
	for _, f := range files {
		if !hasAnyExt(f.AbsolutePath, primaryExts) {
			continue
		}
		cls, err := st.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			continue
		}
		classes += len(cls)
		for _, c := range cls {
			ms, err := st.GetMethodsByClassID(ctx, c.ID)
			if err == nil {
				methods += len(ms)
			}
		}
		cs, err := st.GetCallsByFileID(ctx, f.ID)
		if err == nil {
			calls += len(cs)
		}
	}
	return classes, methods, calls
}

// startMemSampler 周期采样堆内存；返回的函数停止采样并给出峰值。
func startMemSampler(interval time.Duration) func() memSample {
	stop := make(chan struct{})
	done := make(chan struct{})
	var sample memSample
	go func() {
		defer close(done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > sample.PeakHeapAlloc {
				sample.PeakHeapAlloc = m.HeapAlloc
			}
			if m.HeapInuse > sample.PeakHeapInuse {
				sample.PeakHeapInuse = m.HeapInuse
			}
			if m.HeapSys > sample.PeakHeapSys {
				sample.PeakHeapSys = m.HeapSys
			}
			if m.TotalAlloc > sample.TotalAlloc {
				sample.TotalAlloc = m.TotalAlloc
			}
			select {
			case <-stop:
				runtime.GC()
				runtime.ReadMemStats(&m)
				sample.LiveHeapAlloc = m.HeapAlloc
				return
			case <-ticker.C:
			}
		}
	}()
	return func() memSample {
		close(stop)
		<-done
		return sample
	}
}

// pickPerfSymbols 从入库数据选取 context/impact 符号：检索结果里混有 Markdown
// 标题等通用适配器实体，直接按主语言扩展名从 store 取。选**最小源码文件**里的
// 第一个非构造方法，避免误选跨仓 hub 方法把 P95 变成单个极端符号的画像；
// 返回的 query 为方法短名，保证 /search 命中该语料。
func pickPerfSymbols(ctx context.Context, st store.Store, primaryExts []string) (ctxSym, impactSym, query string) {
	files, err := st.GetAllFiles(ctx)
	if err != nil {
		return "", "", ""
	}
	sort.Slice(files, func(i, j int) bool {
		if files[i].LineCount != files[j].LineCount {
			return files[i].LineCount < files[j].LineCount
		}
		return files[i].AbsolutePath < files[j].AbsolutePath
	})
	var fallbackClass string
	for _, f := range files {
		if !hasAnyExt(f.AbsolutePath, primaryExts) {
			continue
		}
		classes, err := st.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			continue
		}
		for _, c := range classes {
			if c.FullName == "" {
				continue
			}
			if fallbackClass == "" {
				fallbackClass = c.FullName
			}
			methods, err := st.GetMethodsByClassID(ctx, c.ID)
			if err != nil {
				continue
			}
			for _, m := range methods {
				fqn := c.FullName + "." + m.Name
				if !isCallableFQN(fqn) || len(m.Name) < 3 {
					continue // 单字母方法名无法作为有代表性的 /search 词
				}
				return c.FullName, fqn, m.Name
			}
		}
	}
	if fallbackClass != "" {
		short := fallbackClass[strings.LastIndex(fallbackClass, ".")+1:]
		return fallbackClass, fallbackClass, short
	}
	return "", "", ""
}

// isCallableFQN 排除构造器式 "Type.Type"：同名首尾段不是可独立命中的方法。
func isCallableFQN(fqn string) bool {
	parts := strings.Split(fqn, ".")
	if len(parts) < 2 {
		return false
	}
	return parts[len(parts)-1] != parts[len(parts)-2]
}

// benchEndpoint 对一个 URL 做 warmup + 固定样本量测量，返回 nearest-rank 百分位。
func benchEndpoint(client *http.Client, rawURL string) endpointResult {
	res := endpointResult{N: perfSamples}
	for i := 0; i < perfWarmup; i++ {
		if resp, err := client.Get(rawURL); err == nil {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
		}
	}
	durations := make([]float64, 0, perfSamples)
	var total float64
	for i := 0; i < perfSamples; i++ {
		start := time.Now()
		resp, err := client.Get(rawURL)
		d := ms(time.Since(start))
		if err != nil {
			res.Errors++
		} else {
			_, _ = io.Copy(io.Discard, resp.Body)
			resp.Body.Close()
			if resp.StatusCode != http.StatusOK {
				res.Errors++
			}
		}
		durations = append(durations, d)
		total += d
	}
	sort.Float64s(durations)
	res.MeanMS = round3(total / float64(perfSamples))
	res.P50MS = percentile(durations, 0.50)
	res.P95MS = percentile(durations, 0.95)
	res.P99MS = percentile(durations, 0.99)
	res.MaxMS = round3(durations[len(durations)-1])
	return res
}

func percentile(sorted []float64, q float64) float64 {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(q*float64(len(sorted)-1) + 0.5)
	if idx >= len(sorted) {
		idx = len(sorted) - 1
	}
	return round3(sorted[idx])
}

func hasAnyExt(path string, exts []string) bool {
	ext := filepath.Ext(path)
	for _, e := range exts {
		if ext == e {
			return true
		}
	}
	return false
}

func rate(n int, d time.Duration) float64 {
	if d <= 0 {
		return 0
	}
	return round3(float64(n) / d.Seconds())
}

func ms(d time.Duration) float64 { return round3(float64(d.Microseconds()) / 1000) }

func round3(v float64) float64 { return mathRound(v*1000) / 1000 }

func mathRound(v float64) float64 {
	if v < 0 {
		return float64(int64(v - 0.5))
	}
	return float64(int64(v + 0.5))
}

func freeAddr() (string, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return "", err
	}
	addr := ln.Addr().String()
	_ = ln.Close()
	return addr, nil
}

func waitHTTPReady(addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	client := &http.Client{Timeout: 500 * time.Millisecond}
	for time.Now().Before(deadline) {
		resp, err := client.Get("http://" + addr + "/health")
		if err == nil {
			resp.Body.Close()
			if resp.StatusCode == http.StatusOK {
				return nil
			}
		}
		time.Sleep(50 * time.Millisecond)
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}

func writePerfJSON(t *testing.T, path string, v any) {
	t.Helper()
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", path, err)
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
	t.Logf("wrote %s", path)
}

// ---- 多租户并发与隔离 ----

type tenantRun struct {
	ID            string  `json:"id"`
	Root          string  `json:"root"`
	ScanMS        float64 `json:"scan_ms"`
	IndexMS       float64 `json:"index_ms"`
	Files         int     `json:"files"`
	Classes       int     `json:"classes"`
	Methods       int     `json:"methods"`
	Error         string  `json:"error,omitempty"`
	PoolMaxOpen   int     `json:"pool_max_open"`
	PoolExpected  int     `json:"pool_expected"`
	FTSDir        string  `json:"fts_dir"`
	FTSFileExists bool    `json:"fts_file_exists"`
	Namespace     string  `json:"redis_namespace"`
}

type isolationReport struct {
	Pass               bool              `json:"pass"`
	StoreFiles         map[string]int    `json:"store_files"`
	CrossTenantPaths   int               `json:"cross_tenant_paths"`
	FTSDistinct        bool              `json:"fts_dirs_distinct"`
	PoolOK             bool              `json:"pool_limits_ok"`
	NamespaceDistinct  bool              `json:"redis_namespace_distinct"`
	Failures           []string          `json:"failures,omitempty"`
	RedisLiveVerified  bool              `json:"redis_live_verified"`
	RedisLiveReason    string            `json:"redis_live_reason"`
	AdditionalEvidence map[string]string `json:"additional_evidence,omitempty"`
}

type multiTenantResult struct {
	Timestamp       string          `json:"timestamp"`
	Storage         string          `json:"storage"`
	VectorDim       int             `json:"vector_dim"`
	SequentialMS    float64         `json:"sequential_total_ms"`
	ConcurrentWall  float64         `json:"concurrent_wall_ms"`
	ContentionRatio float64         `json:"contention_ratio"`
	PeakGoroutines  int             `json:"peak_goroutines"`
	Queries         int64           `json:"queries"`
	QueryErrors     int64           `json:"query_errors"`
	QueryLoopMS     int             `json:"query_loop_interval_ms"`
	Tenants         []tenantRun     `json:"tenants"`
	Isolation       isolationReport `json:"isolation"`
	Notes           []string        `json:"notes,omitempty"`
}

// TestPerfBaseline_MultiTenantConcurrency 多租户同时索引与查询，验证隔离与争用。
func TestPerfBaseline_MultiTenantConcurrency(t *testing.T) {
	if os.Getenv(perfGate) == "" {
		t.Skip("set CODESCHEMA_PERF_BASELINE=1 to run the multi-tenant performance baseline")
	}
	outDir := filepath.Join(repoRoot(), perfOutSubdir)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", outDir, err)
	}
	all := perfCorpora()
	corpora := make([]perfCorpus, 0, 3)
	for _, c := range all {
		if c.Key == "large" {
			continue // 多租户压测用三个既有语料，控制时长
		}
		if _, err := os.Stat(c.Root); err != nil {
			t.Fatalf("corpus missing: %s (%v)", c.Root, err)
		}
		c.Root = perfScanRoot(t, c.Root)
		corpora = append(corpora, c)
	}

	res := multiTenantResult{
		Timestamp:   time.Now().UTC().Format(time.RFC3339),
		Storage:     "sqlite",
		VectorDim:   perfVectorDim,
		QueryLoopMS: 1,
		Notes: []string{
			"连接池/缓存前缀/FTS 目录均按租户配置断言；Redis 活实例未启用时只做命名空间派生一致性校验",
			"sequential_total_ms 为同一语料顺序执行 scan+BuildIndex 的总和；contention_ratio = concurrent_wall / sequential_total",
		},
	}

	// 顺序基线（独立临时目录）。
	seqMgr := newPerfManager(t, corpora, t.TempDir())
	seqStart := time.Now()
	for _, c := range corpora {
		cfg, err := seqMgr.Config(c.Key)
		if err != nil {
			t.Fatalf("sequential config %s: %v", c.Key, err)
		}
		run, err := seqMgr.Runtime(c.Key)
		if err != nil {
			t.Fatalf("sequential runtime %s: %v", c.Key, err)
		}
		if err := rt.ScanRepository(context.Background(), run.Store, cfg, c.Root); err != nil {
			t.Fatalf("sequential scan %s: %v", c.Key, err)
		}
		if _, err := run.Svc.BuildIndex(context.Background()); err != nil {
			t.Fatalf("sequential index %s: %v", c.Key, err)
		}
	}
	res.SequentialMS = ms(time.Since(seqStart))
	seqMgr.Close()

	// 并发：三个租户同时 scan + BuildIndex，同时持续查询。
	conMgr := newPerfManager(t, corpora, t.TempDir())
	defer conMgr.Close()
	var (
		qTotal  atomic.Int64
		qErrors atomic.Int64
		wg      sync.WaitGroup
		mu      sync.Mutex
		runs    []tenantRun
	)
	qStop := make(chan struct{})
	var qWG sync.WaitGroup
	for _, c := range corpora {
		c := c
		svc, err := conMgr.Service(context.Background(), c.Key)
		if err != nil {
			t.Fatalf("service %s: %v", c.Key, err)
		}
		qWG.Add(1)
		go func() {
			defer qWG.Done()
			for {
				select {
				case <-qStop:
					return
				default:
				}
				qTotal.Add(1)
				if _, err := svc.Search(context.Background(), c.Query, "exact", 10); err != nil {
					qErrors.Add(1)
				}
				time.Sleep(time.Millisecond) // 每租户约 1k QPS，避免空索引期百万级空转
			}
		}()
	}

	stopG := make(chan struct{})
	peakG := make(chan int, 1)
	go func() {
		max := 0
		ticker := time.NewTicker(perfSampleTick)
		defer ticker.Stop()
		for {
			if n := runtime.NumGoroutine(); n > max {
				max = n
			}
			select {
			case <-stopG:
				peakG <- max
				return
			case <-ticker.C:
			}
		}
	}()

	wallStart := time.Now()
	for _, c := range corpora {
		c := c
		wg.Add(1)
		go func() {
			defer wg.Done()
			tr := tenantRun{ID: c.Key, Root: c.Root}
			cfg, err := conMgr.Config(c.Key)
			if err != nil {
				tr.Error = err.Error()
				appendRun(&mu, &runs, tr)
				return
			}
			run, err := conMgr.Runtime(c.Key)
			if err != nil {
				tr.Error = err.Error()
				appendRun(&mu, &runs, tr)
				return
			}
			scanStart := time.Now()
			if err := rt.ScanRepository(context.Background(), run.Store, cfg, c.Root); err != nil {
				tr.Error = "scan: " + err.Error()
			}
			tr.ScanMS = ms(time.Since(scanStart))
			idxStart := time.Now()
			if _, err := run.Svc.BuildIndex(context.Background()); err != nil {
				if tr.Error != "" {
					tr.Error += "; "
				}
				tr.Error += "index: " + err.Error()
			}
			tr.IndexMS = ms(time.Since(idxStart))
			tr.Classes, tr.Methods, _ = collectStoreStats(context.Background(), run.Store, c.Exts)
			tr.Files = countStoreFiles(context.Background(), run.Store)
			tr.PoolExpected = cfg.Storage.MaxOpenConns
			if ps, ok := run.Store.(interface{ PoolStats() sql.DBStats }); ok {
				tr.PoolMaxOpen = ps.PoolStats().MaxOpenConnections
			}
			tr.FTSDir = cfg.Storage.Search.FTSDir
			_, statErr := os.Stat(filepath.Join(tr.FTSDir, "fts.json"))
			tr.FTSFileExists = statErr == nil
			tr.Namespace = redisNamespaceFor(cfg)
			appendRun(&mu, &runs, tr)
		}()
	}
	wg.Wait()
	res.ConcurrentWall = ms(time.Since(wallStart))
	close(stopG)
	res.PeakGoroutines = <-peakG
	close(qStop)
	qWG.Wait()
	res.Queries = qTotal.Load()
	res.QueryErrors = qErrors.Load()
	res.Tenants = runs
	if res.SequentialMS > 0 {
		res.ContentionRatio = round3(res.ConcurrentWall / res.SequentialMS)
	}

	// 隔离断言：store 路径、FTS 目录、连接池、Redis 命名空间派生。
	iso := isolationReport{
		StoreFiles:         map[string]int{},
		RedisLiveVerified:  false,
		RedisLiveReason:    "未设置 CODESCHEMA_REDIS_ADDR；仅校验命名空间派生（go test ./internal/store/redis -run Namespace 已覆盖读写隔离）",
		AdditionalEvidence: map[string]string{},
	}
	seenFTS := map[string]string{}
	seenNS := map[string]string{}
	for _, tr := range runs {
		run, err := conMgr.Runtime(tr.ID)
		if err != nil {
			iso.Failures = append(iso.Failures, fmt.Sprintf("%s runtime: %v", tr.ID, err))
			continue
		}
		files, err := run.Store.GetAllFiles(context.Background())
		if err != nil {
			iso.Failures = append(iso.Failures, fmt.Sprintf("%s list files: %v", tr.ID, err))
			continue
		}
		iso.StoreFiles[tr.ID] = len(files)
		if len(files) == 0 {
			iso.Failures = append(iso.Failures, tr.ID+" has no indexed files")
		}
		for _, f := range files {
			rel, rerr := filepath.Rel(tr.Root, f.AbsolutePath)
			if rerr != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				iso.CrossTenantPaths++
				break
			}
		}
		if tr.FTSDir == "" {
			iso.Failures = append(iso.Failures, tr.ID+" fts dir empty")
		} else if prev, ok := seenFTS[tr.FTSDir]; ok {
			iso.Failures = append(iso.Failures, fmt.Sprintf("fts dir shared by %s and %s: %s", prev, tr.ID, tr.FTSDir))
		} else {
			seenFTS[tr.FTSDir] = tr.ID
		}
		if !tr.FTSFileExists {
			iso.Failures = append(iso.Failures, tr.ID+" fts.json missing")
		}
		if tr.PoolExpected > 0 && tr.PoolMaxOpen != tr.PoolExpected {
			iso.Failures = append(iso.Failures, fmt.Sprintf("%s pool maxOpen=%d want %d", tr.ID, tr.PoolMaxOpen, tr.PoolExpected))
		}
		if prev, ok := seenNS[tr.Namespace]; ok {
			iso.Failures = append(iso.Failures, fmt.Sprintf("redis namespace shared by %s and %s: %s", prev, tr.ID, tr.Namespace))
		} else {
			seenNS[tr.Namespace] = tr.ID
		}
	}
	iso.FTSDistinct = len(seenFTS) == len(runs)
	iso.NamespaceDistinct = len(seenNS) == len(runs)
	iso.PoolOK = true
	for _, f := range iso.Failures {
		if strings.Contains(f, "pool maxOpen") {
			iso.PoolOK = false
		}
	}
	if iso.CrossTenantPaths != 0 {
		iso.Failures = append(iso.Failures, fmt.Sprintf("%d tenant(s) have files outside their root", iso.CrossTenantPaths))
	}
	iso.Pass = len(iso.Failures) == 0
	res.Isolation = iso

	writePerfJSON(t, filepath.Join(outDir, "perf-multitenant.json"), res)
	if !iso.Pass {
		t.Errorf("isolation failures: %v", iso.Failures)
	}
}

func appendRun(mu *sync.Mutex, runs *[]tenantRun, tr tenantRun) {
	mu.Lock()
	*runs = append(*runs, tr)
	mu.Unlock()
}

func countStoreFiles(ctx context.Context, st store.Store) int {
	files, err := st.GetAllFiles(ctx)
	if err != nil {
		return 0
	}
	return len(files)
}

// newPerfManager 构造 sqlite 三租户管理器（独立 DSN + 派生索引目录 + 不同池上限）。
func newPerfManager(t *testing.T, corpora []perfCorpus, baseDir string) *tenant.Manager {
	t.Helper()
	cfg := config.DefaultConfig()
	cfg.Storage.Driver = "sqlite"
	cfg.Storage.Vector.Driver = ""
	cfg.Storage.Search.VectorDim = perfVectorDim
	cfg.Scanner.Workers = 4
	cfg.Project.Root = corpora[0].Root
	prepareLocalModel(cfg, baseDir)
	pool := map[string][2]int{"go": {4, 2}, "java": {6, 3}, "ts": {8, 4}}
	for _, c := range corpora {
		p := pool[c.Key]
		cfg.Tenants = append(cfg.Tenants, config.TenantConfig{
			ID:   c.Key,
			Name: c.Name,
			Root: c.Root,
			Storage: config.StorageConfig{
				Driver:       "sqlite",
				DSN:          filepath.Join(baseDir, c.Key),
				MaxOpenConns: p[0],
				MaxIdleConns: p[1],
			},
		})
	}
	mgr, err := tenant.NewManager(context.Background(), cfg, openPerfStore)
	if err != nil {
		t.Fatalf("tenant manager: %v", err)
	}
	return mgr
}

// openPerfStore 注入 sqlite 存储并应用每租户连接池上限（与 cmd 层 store.ApplyPoolLimits 一致）。
func openPerfStore(ctx context.Context, cfg *config.Config, target string) (store.Store, error) {
	st := sqlitestore.NewSQLiteStore()
	if err := st.Open(ctx, target); err != nil {
		return nil, err
	}
	if err := store.ApplyPoolLimits(st, cfg.Storage.MaxOpenConns, cfg.Storage.MaxIdleConns); err != nil {
		_ = st.Close()
		return nil, err
	}
	return st, nil
}

// redisNamespaceFor 复现 cmd/codeschema/store_redis.go 的命名空间派生公式，
// 用于在无 Redis 活实例时校验多租户前缀互不重叠。
func redisNamespaceFor(cfg *config.Config) string {
	sum := sha256.Sum256([]byte(cfg.Storage.Driver + "|" + cfg.Storage.DSN))
	return "codeschema-" + hex.EncodeToString(sum[:6])
}
