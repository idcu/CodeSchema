// Package benchmark 提供 codeschema benchmark 子命令的核心执行逻辑：
// 对单个或多个仓库执行「扫描 → 建索引 → 检索」全链路指标采集并输出对比报告。
//
// 与 internal/integration 中基于 testing.TB 的基准测试不同，本包为纯生产代码
// （不依赖 testing），供 cmd/codeschema 直接调用，是 `codeschema benchmark`
// 子命令的落地实现（技术路线红灯笼①：benchmark 子命令从"规划中"变为"可用"）。
package benchmark

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"time"

	"github.com/idcu/codeschema/internal/parser"
	adapter "github.com/idcu/codeschema/internal/parser/adapter/treesitter"
	"github.com/idcu/codeschema/internal/scanner"
	"github.com/idcu/codeschema/internal/search"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/vector"
)

// BenchResult 单个仓库的基准测试结果（JSON tag 与 internal/integration 保持一致，
// 便于与既有 build/bench-compare.json 产物互通）。
type BenchResult struct {
	RepoName    string  `json:"repo_name"`
	RepoPath    string  `json:"repo_path"`
	FileCount   int     `json:"file_count"`
	ScanTimeMs  int64   `json:"scan_time_ms"`
	IndexTimeMs int64   `json:"index_time_ms"`
	HeapMB      float64 `json:"heap_mb"`
	SearchP50Ms float64 `json:"search_p50_ms"`
	SearchP95Ms float64 `json:"search_p95_ms"`
	SearchP99Ms float64 `json:"search_p99_ms"`
	SearchAvgMs float64 `json:"search_avg_ms"`
}

// Options 控制 benchmark 运行参数。
type Options struct {
	// Workers 并发解析 worker 数（默认 2）。
	Workers int
	// ConfigDesc 写入报告的描述信息。
	ConfigDesc string
}

// benchComponents 一次单仓 benchmark 运行所需的组件集合。
type benchComponents struct {
	store    store.Store
	scanner  *scanner.Scanner
	builder  *search.IndexBuilder
	searcher *search.Searcher
	storeDir string // 临时存储目录（close 时清理）
}

// newBenchComponents 创建组件集合：临时目录 + 内存 FTS/向量（与
// integration.NewBenchSetup 同构，但不依赖 testing.TB）。
func newBenchComponents(ctx context.Context, workers int) (*benchComponents, error) {
	st := store.NewStore("file")
	storeDir, err := os.MkdirTemp("", "codeschema-bench-*")
	if err != nil {
		return nil, err
	}
	if err := st.Open(ctx, storeDir); err != nil {
		return nil, err
	}

	reg := parser.NewRegistry()
	reg.Register(adapter.NewTreeSitterAdapter())
	sc := scanner.NewScanner(st, reg, workers)

	fts := search.NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	builder := search.NewIndexBuilder(fts, idx, em)
	searcher := search.NewSearcher(fts, search.NewVectorAdapter(idx), nil)

	return &benchComponents{
		store:    st,
		scanner:  sc,
		builder:  builder,
		searcher: searcher,
		storeDir: storeDir,
	}, nil
}

// close 关闭组件并清理临时目录。
func (c *benchComponents) close() {
	if c.store != nil {
		_ = c.store.Close()
	}
	if c.storeDir != "" {
		_ = os.RemoveAll(c.storeDir)
	}
}

// Run 对一组仓库执行全链路 benchmark，返回排序后的结果。
// repos 为绝对或相对路径列表；每个仓库须存在（无 go.mod 也会尝试扫描，
// 与 scan 命令行为一致）。
func Run(ctx context.Context, repos []string, opts Options) ([]BenchResult, error) {
	if opts.Workers <= 0 {
		opts.Workers = 2
	}
	var results []BenchResult
	for _, repoPath := range repos {
		res, err := runOne(ctx, repoPath, opts)
		if err != nil {
			return results, err
		}
		results = append(results, res)
	}
	SortBenchResults(results)
	return results, nil
}

// runOne 对单个仓库执行 scan → index → search 指标采集。
func runOne(ctx context.Context, repoPath string, opts Options) (BenchResult, error) {
	res := BenchResult{
		RepoName: filepath.Base(repoPath),
		RepoPath: repoPath,
	}

	// 仓库存在性校验
	if _, err := os.Stat(repoPath); err != nil {
		return res, err
	}

	comps, err := newBenchComponents(ctx, opts.Workers)
	if err != nil {
		return res, err
	}
	defer comps.close()

	// 统计文件数（口径与 scanner.listFiles 一致：忽略 .git/vendor 等目录，统计全部文件）
	res.FileCount = countFiles(repoPath)

	// 记录内存基准
	var m0 runtime.MemStats
	runtime.ReadMemStats(&m0)

	// 扫描
	scanStart := time.Now()
	if err := comps.scanner.ScanAll(ctx, repoPath); err != nil {
		return res, err
	}
	res.ScanTimeMs = time.Since(scanStart).Milliseconds()

	// 构建索引
	idxStart := time.Now()
	if _, err := comps.builder.BuildFromStore(ctx, comps.store); err != nil {
		return res, err
	}
	res.IndexTimeMs = time.Since(idxStart).Milliseconds()

	// 内存增量
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)
	res.HeapMB = float64(m1.HeapInuse-m0.HeapInuse) / 1024 / 1024

	// 检索延迟分布（与 integration 测试相同的查询集）
	queries := []string{
		"Scanner", "BuildAll", "Parse", "Store", "Vector",
		"Search", "Adapter", "config", "analyzer", "scheduler",
		"IRDocument", "ClassIR", "MethodIR", "FileStore",
		"MemoryStore", "LocalEmbedder", "IndexBuilder", "Searcher",
	}
	var latencies []float64
	for _, q := range queries {
		start := time.Now()
		if _, err := comps.searcher.Search(ctx, q, search.SearchModeBoth, 10); err != nil {
			continue
		}
		latencies = append(latencies, float64(time.Since(start).Microseconds()))
	}

	if len(latencies) > 0 {
		sort.Float64s(latencies)
		n := len(latencies)
		res.SearchP50Ms = latencies[n*50/100] / 1000
		res.SearchP95Ms = latencies[n*95/100] / 1000
		res.SearchP99Ms = latencies[n*99/100] / 1000
		avg := 0.0
		for _, v := range latencies {
			avg += v
		}
		res.SearchAvgMs = avg / float64(n) / 1000
	}

	return res, nil
}

// countFiles 递归统计仓库文件总数，忽略目录与 scanner.listFiles 保持一致
// （.git/node_modules/target/build/vendor/.idea/.vscode/__pycache__），
// 使 benchmark 报告的 file_count 与扫描日志 "files discovered" 口径一致。
func countFiles(root string) int {
	count := 0
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "target", "build", "vendor", ".idea", ".vscode", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		count++
		return nil
	})
	return count
}

// isSupportedSource 判断文件名是否属于 scanner 支持的 30 语言。
func isSupportedSource(name string) bool {
	ext := strings.ToLower(filepath.Ext(name))
	switch ext {
	case ".go", ".java", ".ts", ".js", ".py", ".rs", ".cpp", ".cc", ".cxx", ".hpp", ".c",
		".kt", ".kts", ".swift", ".php", ".cs", ".rb", ".sh", ".bash", ".scala", ".sc",
		".sql", ".ex", ".exs", ".ml", ".mli", ".lua", ".groovy", ".css", ".toml",
		".yml", ".yaml", ".proto", ".html", ".htm", ".hcl", ".tf", ".svelte",
		".md", ".markdown", ".elm", ".cue":
		return true
	}
	// 无扩展名按文件名识别（如 Dockerfile）
	return name == "Dockerfile"
}
