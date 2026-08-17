// Package runtime 提供单项目运行期组件的装配逻辑（解析适配器注册、双路检索器、
// AI 增强、Service 装配、全量扫描、后台监听），供 cmd/codeschema 与各租户运行实例复用。
//
// 设计要点：
//   - 本包只依赖纯 Go 内部包（不含 build-tagged 的 pg/redis 存储分发），因此可被
//     cmd 与 internal/tenant 共同引用，避免逻辑重复。
//   - 存储后端的打开（含 pg/redis 分发）由 cmd 层通过 OpenStoreFunc 注入，维持
//     internal/store 的循环依赖隔离约束。
package runtime

import (
	"context"
	"log"
	"os"
	"os/exec"
	"path/filepath"
	"time"

	"github.com/idcu/codeschema/internal/ai"
	"github.com/idcu/codeschema/internal/analyzer"
	"github.com/idcu/codeschema/internal/config"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/parser/adapter/codegraph"
	lspadapter "github.com/idcu/codeschema/internal/parser/adapter/lsp"
	"github.com/idcu/codeschema/internal/parser/adapter/scip"
	treesitter "github.com/idcu/codeschema/internal/parser/adapter/treesitter"
	"github.com/idcu/codeschema/internal/scanner"
	"github.com/idcu/codeschema/internal/scheduler"
	"github.com/idcu/codeschema/internal/search"
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/vector"
	"github.com/idcu/codeschema/internal/watcher"
)

// NewParserRegistry 构建统一的解析适配器注册中心（编排主路入口）。
//
// 注册策略：
//  1. tree-sitter 适配器始终注册（30 语言正则，零依赖兜底）；
//  2. 配置 parser.lsp.enabled=true 且系统存在对应语言服务器时，注册
//     gopls/jdtls/clangd 适配器（FallbackParser 包装：LSP 失败自动回退 tree-sitter）；
//  3. SCIP / CodeGraph 适配器按配置注册（可选），同样是「高精度优先、失败回退」。
//
// 语言优先级（高精度优先）：LSP(go/java/cpp) > SCIP > CodeGraph > tree-sitter。
func NewParserRegistry(ctx context.Context, cfg *config.Config, rootDir string) *parser.Registry {
	reg := parser.NewRegistry()

	// ① 兜底：tree-sitter 始终注册（默认构建为 30 语言正则，零 CGO）
	ts := treesitter.NewTreeSitterAdapter()
	reg.Register(ts)

	// ② LSP 适配器（可选，配置启用 + 工具存在时注册）
	if cfg.Parser.LSP.Enabled {
		lspAdapters := []parser.ParserPlugin{
			lspadapter.NewGoplsAdapter(),
			lspadapter.NewJDTLSAdapter(),
			lspadapter.NewClangdAdapter(),
		}
		registered := 0
		for _, la := range lspAdapters {
			if !commandAvailable(la.Name()) {
				log.Printf("parser.lsp: %s not found in PATH, skipping (fallback to tree-sitter)", la.Name())
				continue
			}
			if err := la.Init(ctx, map[string]any{"rootUri": "file://" + rootDir}); err != nil {
				log.Printf("parser.lsp: %s init failed (%v), skipping (fallback to tree-sitter)", la.Name(), err)
				continue
			}
			reg.Register(parser.NewFallbackParser(la, ts))
			registered++
		}
		if registered == 0 {
			log.Printf("parser.lsp: enabled but no language server available, using tree-sitter only")
		}
	}

	// ③ SCIP 适配器（可选：配置了 index_dir 才注册）
	if dir := cfg.Parser.SCIP.IndexDir; dir != "" && dir != "./scipout" {
		sc := scip.NewSCIPAdapter(dir)
		if err := sc.Init(ctx, map[string]any{"index_dir": dir}); err != nil {
			log.Printf("parser.scip: init failed (%v), skipping", err)
		} else {
			reg.Register(parser.NewFallbackParser(sc, ts))
		}
	}

	// ④ CodeGraph 适配器（可选：配置了 db 才注册）
	if db := cfg.Parser.CodeGraph.DB; db != "" && db != "./codegraph.db" {
		cg := codegraph.NewCodeGraphAdapter(db)
		if err := cg.Init(ctx, map[string]any{"db_path": db}); err != nil {
			log.Printf("parser.codegraph: init failed (%v), skipping", err)
		} else {
			reg.Register(parser.NewFallbackParser(cg, ts))
		}
	}

	reg.SetPriority("go", []string{"gopls", "codegraph", "scip", "treesitter"})
	reg.SetPriority("java", []string{"jdtls", "codegraph", "scip", "treesitter"})
	reg.SetPriority("cpp", []string{"clangd", "codegraph", "scip", "treesitter"})
	reg.SetPriority("ts", []string{"codegraph", "scip", "treesitter"})
	reg.SetPriority("py", []string{"codegraph", "scip", "treesitter"})
	reg.SetPriority("rust", []string{"codegraph", "scip", "treesitter"})

	return reg
}

// commandAvailable 检查 PATH 中是否存在指定命令。
func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}

// WithImpactAnalyzer 注入代码图分析器，启用真实调用图影响面分析（含关联单测）。
func WithImpactAnalyzer(svc *service.Service, st store.Store) *service.Service {
	an := analyzer.NewAnalyzer(st)
	return svc.WithImpactAnalyzer(an)
}

// NewAIEnhancer 按 config.ai 构造 AI 增强层（可选）。
//
// 配置不完整（缺 BaseURL/APIKey/Model 任一）时返回 nil——AI 增强被禁用，
// 主流程零影响（规则标签 / 索引始终可用）。
func NewAIEnhancer(cfg *config.Config) *ai.Enhancer {
	client := ai.NewOpenAICompatClient(ai.HTTPClientConfig{
		BaseURL: cfg.AI.BaseURL,
		APIKey:  cfg.AI.APIKey,
		Model:   cfg.AI.Model,
	})
	if client == nil {
		log.Printf("ai: enhancement disabled (set ai.base_url/api_key/model to enable)")
		return nil
	}
	budget := ai.NewBudget(cfg.AI.BudgetPerScan, cfg.AI.BudgetPerQuery)
	return ai.NewEnhancer(client, budget)
}

// WithAIEnhancer 将 AI 增强层注入 Service（查询期同名方法消歧）与 Analyzer（标签补全）。
// 未配置 LLM 时返回 nil（调用方自行处理禁用），不视为错误。
func WithAIEnhancer(svc *service.Service, cfg *config.Config) *ai.Enhancer {
	enh := NewAIEnhancer(cfg)
	if enh == nil {
		return nil
	}
	svc.WithAIEnhancer(enh)
	return enh
}

// RunTagAll 对已入库数据执行标签推导（规则 + 可选 AI 增强）。
func RunTagAll(ctx context.Context, st store.Store, cfg *config.Config) error {
	an := analyzer.NewAnalyzer(st)
	if enh := NewAIEnhancer(cfg); enh != nil {
		an.SetEnhancer(enh)
		log.Printf("ai: enhancement enabled (provider=%s model=%s budget_scan=%d)",
			cfg.AI.Provider, cfg.AI.Model, cfg.AI.BudgetPerScan)
	}
	if err := an.TagAll(ctx); err != nil {
		return err
	}
	return nil
}

// NewSearcherWithStore 创建双路检索器 + 索引构建器，并返回底层向量存储。
//
// 优先使用 ONNX 模型（bge-small-zh-v1.5），模型文件不存在时降级到 LocalEmbedder。
func NewSearcherWithStore(cfg *config.Config) (*search.Searcher, *search.IndexBuilder, vector.VectorStore) {
	ftsFile := filepath.Join(cfg.Storage.Search.FTSDir, "fts.json")
	var fts search.FTSEngine
	pfts, err := search.NewPersistentFTS(ftsFile)
	if err != nil {
		log.Printf("WARN: new persistent FTS (%s): %v, fallback to memory", ftsFile, err)
		fts = search.NewMemoryFTS()
	} else {
		fts = pfts
	}

	modelDir := cfg.Storage.Vector.ModelDir
	if modelDir == "" {
		modelDir = filepath.Join("down", "models", cfg.Storage.Vector.EmbeddingModel)
	}
	libDir := filepath.Join("down", "onnxruntime")
	var em vector.Embedder

	if dl := vector.NewModelDownloader(modelDir, cfg.Storage.Vector.ModelDownloadURL, cfg.Storage.Vector.ModelSHA256); dl != nil {
		if ok, err := dl.Ensure(context.Background(), cfg.Storage.Vector.EmbeddingModel); err != nil {
			log.Printf("WARN: ONNX model remote fetch failed (%v), falling back to LocalEmbedder", err)
		} else if ok {
			log.Printf("semantic: ONNX model ensured at %s", modelDir)
		}
	}

	onnxEm := vector.NewONNXEmbedderOrFallback(modelDir, 512, libDir)
	if onnxEm != nil {
		log.Printf("semantic: using ONNX embedder (%s, dim=%d)", cfg.Storage.Vector.EmbeddingModel, onnxEm.Dim())
		em = onnxEm
	} else {
		log.Printf("semantic: ONNX model not found, falling back to LocalEmbedder (dim=%d)",
			cfg.Storage.Search.VectorDim)
		model := vector.NewLocalEmbedder(cfg.Storage.Search.VectorDim)
		idfFile := filepath.Join(cfg.Storage.Search.IDFDir, "idf.json")
		if err := model.LoadIDF(idfFile); err != nil {
			log.Printf("WARN: load IDF dictionary (%s): %v, will rebuild on build", idfFile, err)
		}
		em = model
	}

	// 向量存储：显式 driver=chromem 时启用持久化 chromem 后端；否则（默认/空）用文件 PersistentStore，保持既有行为。
	vecFile := filepath.Join(cfg.Storage.Search.VectorDir, "vector.json")
	var store vector.VectorStore
	if cfg.Storage.Vector.Driver == "chromem" {
		persist := cfg.Storage.Vector.DSN
		if persist == "" {
			persist = filepath.Join(cfg.Storage.Search.VectorDir, "chromem.db")
		}
		cs, cerr := vector.NewPersistentChromemStore("codeschema", persist, em.Dim(), vector.NewEmbeddingFunc(em))
		if cerr != nil {
			log.Printf("WARN: new persistent chromem store (%s): %v, fallback to file persistent store", persist, cerr)
		} else {
			log.Printf("semantic: using chromem vector store (persist=%s, dim=%d)", persist, em.Dim())
			store = cs
		}
	}
	if store == nil {
		pstore, err := vector.NewPersistentStore(vecFile)
		if err != nil {
			log.Printf("WARN: new persistent vector store (%s): %v, fallback to memory", vecFile, err)
			store = vector.NewMemoryStore()
		} else {
			store = pstore
		}
	}

	indexer := vector.NewIndexer(store, em, 2)
	adapter := search.NewVectorAdapter(indexer)
	searcher := search.NewSearcher(fts, adapter, nil)
	builder := search.NewIndexBuilder(fts, indexer, em)
	return searcher, builder, store
}

// NewSearcher 兼容旧签名的委托：仅返回 searcher 与 indexBuilder。
func NewSearcher(cfg *config.Config) (*search.Searcher, *search.IndexBuilder) {
	s, b, _ := NewSearcherWithStore(cfg)
	return s, b
}

// Runtime 汇聚单个项目运行期所需的全部组件。
type Runtime struct {
	Store    store.Store
	Svc      *service.Service
	Searcher *search.Searcher
	Builder  *search.IndexBuilder
	VecStore vector.VectorStore
	Analyzer *analyzer.Analyzer
	Enhancer *ai.Enhancer
}

// BuildRuntime 基于已打开的 store 装配 Service + 检索 + 分析器，并执行首轮索引构建。
func BuildRuntime(ctx context.Context, st store.Store, cfg *config.Config) (*Runtime, error) {
	searcher, builder, vecStore := NewSearcherWithStore(cfg)
	svc := service.NewService(st)
	svc.WithSearcher(searcher).WithIndexBuilder(builder)
	WithImpactAnalyzer(svc, st)
	WithAIEnhancer(svc, cfg)
	if _, err := svc.BuildIndex(ctx); err != nil {
		return nil, err
	}
	return &Runtime{
		Store:    st,
		Svc:      svc,
		Searcher: searcher,
		Builder:  builder,
		VecStore: vecStore,
		Analyzer: analyzer.NewAnalyzer(st),
		Enhancer: NewAIEnhancer(cfg),
	}, nil
}

// ScanRepository 对该仓库执行一次全量扫描 + 入库 + 标签推导（相当于 scan 子命令）。
// 索引构建交由后续 BuildRuntime 统一完成，本函数只负责灌库与标签。
func ScanRepository(ctx context.Context, st store.Store, cfg *config.Config, root string) error {
	reg := NewParserRegistry(ctx, cfg, root)
	s := scanner.NewScanner(st, reg, cfg.Scanner.Workers)
	s.SetLimits(int64(cfg.Scanner.FileSizeLimitMB)*1024*1024, cfg.Scanner.LineCountLimit)
	if err := s.ScanAll(ctx, root); err != nil {
		return err
	}
	searcher, builder := NewSearcher(cfg)
	svc := service.NewService(st)
	svc.WithSearcher(searcher).WithIndexBuilder(builder)
	WithImpactAnalyzer(svc, st)
	idfFile := filepath.Join(cfg.Storage.Search.IDFDir, "idf.json")
	if err := os.MkdirAll(filepath.Dir(idfFile), 0755); err == nil {
		if err := builder.SaveIDF(idfFile); err != nil {
			log.Printf("WARN: save IDF dictionary: %v", err)
		}
	}
	if err := RunTagAll(ctx, st, cfg); err != nil {
		log.Printf("WARN: tag all: %v", err)
	}
	return nil
}

// StartWatchBackground 后台启动该仓库的增量监听；返回的 stop 函数用于优雅停止。
func StartWatchBackground(ctx context.Context, st store.Store, cfg *config.Config, root string) (func(), error) {
	reg := NewParserRegistry(ctx, cfg, root)
	s := scanner.NewScanner(st, reg, cfg.Scanner.Workers)
	s.SetLimits(int64(cfg.Scanner.FileSizeLimitMB)*1024*1024, cfg.Scanner.LineCountLimit)
	searcher, builder := NewSearcher(cfg)
	svc := service.NewService(st)
	svc.WithSearcher(searcher).WithIndexBuilder(builder)
	WithImpactAnalyzer(svc, st)

	sched := scheduler.NewScheduler(cfg.Watcher.DebounceMs, 1000)

	builder.StartAsync(ctx, 64, 2)
	stopAutoSave := builder.AutoSaveIDF(filepath.Join(cfg.Storage.Search.IDFDir, "idf.json"), 60*time.Second)

	if _, err := svc.BuildIndex(ctx); err != nil {
		log.Printf("WARN: build index: %v", err)
	}
	s.SetOnIndex(func(ctx context.Context, filePath string) error {
		return builder.BuildAndIndex(ctx, st, filePath)
	})
	s.SetOnDelete(func(ctx context.Context, filePath string) error {
		return builder.BuildAndRemove(ctx, st, filePath)
	})

	wctx, wcancel := context.WithCancel(ctx)
	var w watcher.Watcher
	var err error
	if cfg.Watcher.UseFsnotify {
		w, err = watcher.NewFsWatcher(root, s, sched, cfg.Watcher.IgnoreDirs)
	} else {
		w = watcher.NewPollWatcher(root, s, sched, 1*time.Second, cfg.Watcher.IgnoreDirs)
	}
	if err != nil {
		wcancel()
		stopAutoSave()
		return nil, err
	}
	go sched.Start(wctx, func(ctx context.Context, path string) error {
		return s.ProcessFile(ctx, path)
	})
	go func() {
		_ = w.Start(wctx)
	}()
	return func() {
		wcancel()
		stopAutoSave()
	}, nil
}
