// CodeSchema — 代码元数据 KV/DB 系统
//
// 面向 AI 辅助开发的代码元数据索引与上下文裁剪服务。
// 将仓库中的类、方法、接口、继承关系、调用关系等结构化数据，
// 沉淀为三层存储，通过 MCP Server 向 AI Agent 供给精准裁剪后的代码上下文。
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/idcu/codeschema/internal/ai"
	"github.com/idcu/codeschema/internal/analyzer"
	"github.com/idcu/codeschema/internal/config"
	"github.com/idcu/codeschema/internal/robust"
	"github.com/idcu/codeschema/internal/scanner"
	"github.com/idcu/codeschema/internal/scheduler"
	"github.com/idcu/codeschema/internal/search"
	"github.com/idcu/codeschema/internal/server"
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/vector"
	"github.com/idcu/codeschema/internal/watcher"
)

// withImpactAnalyzer 注入代码图分析器，启用真实调用图影响面分析（含关联单测）。
func withImpactAnalyzer(svc *service.Service, st store.Store) *service.Service {
	an := analyzer.NewAnalyzer(st)
	return svc.WithImpactAnalyzer(an)
}

// newAIEnhancer 按 config.ai 构造 AI 增强层（可选）。
//
// 配置不完整（缺 BaseURL/APIKey/Model 任一）时返回 nil——AI 增强被禁用，
// 主流程零影响（规则标签 / 索引始终可用）。
func newAIEnhancer(cfg *config.Config) *ai.Enhancer {
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

// withAIEnhancer 将 AI 增强层注入 Service（查询期同名方法消歧）与 Analyzer（标签补全）。
// 未配置 LLM 时返回 nil（调用方自行处理禁用），不视为错误。
func withAIEnhancer(svc *service.Service, cfg *config.Config) *ai.Enhancer {
	enh := newAIEnhancer(cfg)
	if enh == nil {
		return nil
	}
	svc.WithAIEnhancer(enh)
	return enh
}

// runTagAll 对已入库数据执行标签推导（规则 + 可选 AI 增强）。
func runTagAll(ctx context.Context, st store.Store, cfg *config.Config) error {
	an := analyzer.NewAnalyzer(st)
	if enh := newAIEnhancer(cfg); enh != nil {
		an.SetEnhancer(enh)
		log.Printf("ai: enhancement enabled (provider=%s model=%s budget_scan=%d)",
			cfg.AI.Provider, cfg.AI.Model, cfg.AI.BudgetPerScan)
	}
	if err := an.TagAll(ctx); err != nil {
		return fmt.Errorf("tag all: %w", err)
	}
	return nil
}

var (
	version = "0.1.0"
)

// 存储后端的统一分发（含 pg/redis 的 build-tagged 接线）见 store_dispatch.go。
// 因 sqlite/pg 子包反向依赖 internal/store（实现其接口），分发必须落在 cmd 层，
// 不能在 internal/store.NewStore 内联（否则形成循环依赖）。

func main() {
	if err := run(); err != nil {
		log.Fatalf("error: %v", err)
	}
}

func run() error {
	// 全局配置标志
	configPath := flag.String("config", "", "配置文件路径（.yaml/.yml/.json）")
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `CodeSchema v%s — 代码元数据 KV/DB 系统

Usage:
  codeschema [--config=<path>] <command> [options]

Commands:
  scan <path>       扫描仓库并入库
  watch <path>      文件监听增量（P0，轮询模式；--fsnotify 切换为原生监听）
  rebuild-kv        重建 KV 缓存（P2）
  benchmark         全链路基准（扫描/索引/检索指标，多仓库对比）
  mcp               启动 MCP Server（P0）
  serve             启动 HTTP API Server（P0）
  version           显示版本信息

Global Options:
  --config <path>   配置文件路径（支持 .yaml/.yml/.json）

Use "codeschema <command> -h" for more information about a command.
`, version)
	}

	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		return nil
	}

	// 加载配置
	cfg, err := config.Load(*configPath)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	// P9: 环境变量覆盖（优先级高于配置文件，但低于 CLI 参数）
	config.LoadFromEnv(cfg)

	// 优雅关闭管理器（30s 全局超时）
	graceful := robust.NewGracefulManager(30 * time.Second)
	robust.ForceExitOnSecondSignal(graceful)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 注册优雅关闭钩子：先取消 context
	graceful.RegisterFunc("context_cancel", func(ctx context.Context) error {
		cancel()
		return nil
	})

	// 捕获退出信号并启动优雅关闭
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
		select {
		case <-sigCh:
			_ = graceful.Shutdown(context.Background())
		case <-ctx.Done():
		}
	}()

	// P9: 配置热重载（watch/mcp/serve 命令支持）
	// 仅在指定了配置文件路径时启动
	if *configPath != "" {
		switch args[0] {
		case "watch", "mcp", "serve":
			cw := config.NewConfigWatcher(*configPath, cfg, nil)
			cw.SetPollInterval(2 * time.Second)
			go func() {
				if err := cw.Start(ctx); err != nil && err != context.Canceled {
					log.Printf("config watcher stopped: %v", err)
				}
			}()
			graceful.RegisterFunc("config_watcher", func(ctx context.Context) error {
				cw.Stop()
				return nil
			})

			// 将配置实例替换为可热更新的配置
			origCfg := cfg
			cfg = cw.GetConfig()
			_ = origCfg // 保留原引用，后续使用 cfg 时会自动获取最新配置
		}
	}

	switch args[0] {
	case "version":
		fmt.Printf("CodeSchema v%s\n", version)
		return nil

	case "scan":
		return scanCmd(ctx, cfg, args[1:])

	case "watch":
		return watchCmd(ctx, cfg, args[1:])

	case "benchmark":
		return benchmarkCmd(ctx, cfg, args[1:])

	case "mcp":
		return mcpCmd(ctx, cfg, args[1:])

	case "serve":
		return serveCmd(ctx, cfg, args[1:])

	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func scanCmd(ctx context.Context, cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	workers := fs.Int("workers", cfg.Scanner.Workers, "并发解析 worker 数")
	storeDir := fs.String("store", cfg.Storage.DSN, "存储目录")
	fs.Parse(args)

	repoPath := fs.Arg(0)
	if repoPath == "" {
		return fmt.Errorf("usage: codeschema scan [--workers=%d] [--store=%s] <path>", cfg.Scanner.Workers, cfg.Storage.DSN)
	}

	fmt.Printf("scanning repository: %s (workers=%d)\n", repoPath, *workers)

	// 初始化存储
	st, err := newStore(ctx, cfg, *storeDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// 健康检查
	if err := st.HealthCheck(ctx); err != nil {
		return fmt.Errorf("health check: %w", err)
	}

	// 初始化解析适配器注册中心（tree-sitter 兜底 + 可选 LSP/SCIP/CodeGraph 高精度优先）
	// 注：T1-3 修复——此前此处创建空 Registry 导致 CLI 扫描从未真正解析符号。
	reg := newParserRegistry(ctx, cfg, repoPath)

	// 创建 Scanner
	s := scanner.NewScanner(st, reg, *workers)

	// 执行全量扫描
	start := time.Now()
	fmt.Printf("scanning started at %s\n", start.Format(time.RFC3339))

	if err := s.ScanAll(ctx, repoPath); err != nil {
		return fmt.Errorf("scan: %w", err)
	}

	elapsed := time.Since(start)
	fmt.Printf("scan completed in %s\n", elapsed.Round(time.Millisecond))

	// 扫描完成后构建搜索索引
	searcher, builder := newSearcher(cfg)
	svc := service.NewService(st)
	svc.WithSearcher(searcher).WithIndexBuilder(builder)
	withImpactAnalyzer(svc, st)
	if result, err := svc.BuildIndex(ctx); err != nil {
		fmt.Printf("WARN: build index: %v\n", err)
	} else {
		fmt.Printf("index built: %d docs indexed in %s\n", result.IndexedDocs, result.Duration.Round(time.Millisecond))
	}

	// 持久化 IDF 词典
	idfFile := filepath.Join(cfg.Storage.Search.IDFDir, "idf.json")
	if err := os.MkdirAll(filepath.Dir(idfFile), 0755); err == nil {
		if err := builder.SaveIDF(idfFile); err != nil {
			fmt.Printf("WARN: save IDF dictionary: %v\n", err)
		}
	}

	// 标签推导（规则 + 可选 AI 增强）
	if err := runTagAll(ctx, st, cfg); err != nil {
		fmt.Printf("WARN: tag all: %v\n", err)
	}

	return nil
}

func watchCmd(ctx context.Context, cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	workers := fs.Int("workers", cfg.Scanner.Workers, "并发解析 worker 数")
	storeDir := fs.String("store", cfg.Storage.DSN, "存储目录")
	debounceMs := fs.Int("debounce", cfg.Watcher.DebounceMs, "防抖窗口（毫秒）")
	useFsnotify := fs.Bool("fsnotify", cfg.Watcher.UseFsnotify, "使用 fsnotify 原生监听（替代轮询）")
	fs.Parse(args)

	repoPath := fs.Arg(0)
	if repoPath == "" {
		return fmt.Errorf("usage: codeschema watch [--workers=%d] [--store=%s] [--debounce=%d] [--fsnotify] <path>", cfg.Scanner.Workers, cfg.Storage.DSN, cfg.Watcher.DebounceMs)
	}

	mode := "polling"
	if *useFsnotify {
		mode = "fsnotify"
	}
	fmt.Printf("watching repository: %s (workers=%d, debounce=%dms, mode=%s)\n", repoPath, *workers, *debounceMs, mode)

	// 初始化存储
	st, err := newStore(ctx, cfg, *storeDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// 初始化解析适配器注册中心（tree-sitter 兜底 + 可选 LSP/SCIP/CodeGraph 高精度优先）
	reg := newParserRegistry(ctx, cfg, repoPath)

	// 创建 Scanner
	s := scanner.NewScanner(st, reg, *workers)

	// 创建调度器
	sched := scheduler.NewScheduler(*debounceMs, 1000)

	// 创建搜索组件
	searcher, builder := newSearcher(cfg)
	svc := service.NewService(st)
	svc.WithSearcher(searcher).WithIndexBuilder(builder)
	withImpactAnalyzer(svc, st)

	// 启动异步索引队列（2 个 worker，64 缓冲区）
	builder.StartAsync(ctx, 64, 2)
	defer builder.StopAsync()

	// 启动 IDF 自动持久化（60 秒间隔，仅 watch/mcp/serve 长时运行命令）
	idfFile := filepath.Join(cfg.Storage.Search.IDFDir, "idf.json")
	stopAutoSave := builder.AutoSaveIDF(idfFile, 60*time.Second)
	defer stopAutoSave()

	// 启动时全量构建索引（如果持久化索引存在则跳过）
	if result, err := svc.BuildIndex(ctx); err != nil {
		fmt.Printf("WARN: build index: %v\n", err)
	} else {
		fmt.Printf("index built: %d docs indexed in %s\n", result.IndexedDocs, result.Duration.Round(time.Millisecond))
		// 持久化 IDF 词典（全量构建后立即保存一次）
		if err := os.MkdirAll(filepath.Dir(idfFile), 0755); err == nil {
			if err := builder.SaveIDF(idfFile); err != nil {
				fmt.Printf("WARN: save IDF dictionary: %v\n", err)
			}
		}
	}

	// 设置增量索引回调（异步，非阻塞）
	s.SetOnIndex(func(ctx context.Context, filePath string) error {
		return builder.BuildAndIndex(ctx, st, filePath)
	})

	// 设置删除回调，文件被删除时自动清理索引
	s.SetOnDelete(func(ctx context.Context, filePath string) error {
		return builder.BuildAndRemove(ctx, st, filePath)
	})

	// 创建监听器
	var w watcher.Watcher
	if *useFsnotify {
		fw, err := watcher.NewFsWatcher(repoPath, s, sched, cfg.Watcher.IgnoreDirs)
		if err != nil {
			return fmt.Errorf("new fsnotify watcher: %w", err)
		}
		w = fw
	} else {
		w = watcher.NewPollWatcher(repoPath, s, sched, 1*time.Second, cfg.Watcher.IgnoreDirs)
	}

	// 启动调度器
	go sched.Start(ctx, func(ctx context.Context, path string) error {
		return s.ProcessFile(ctx, path)
	})

	// 启动监听器（阻塞）
	fmt.Println("watcher started, press Ctrl+C to stop")
	return w.Start(ctx)
}

func mcpCmd(ctx context.Context, cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	addr := fs.String("addr", cfg.Server.MCPAddr, "监听地址")
	storeDir := fs.String("store", cfg.Storage.DSN, "存储目录")
	authToken := fs.String("auth-token", cfg.Server.AuthToken, "Bearer token 认证")
	printCfg := fs.Bool("print-config", false, "打印各客户端 MCP 接入配置片段并退出（不启动服务）")
	fs.Parse(args)

	// T2-5：一键打印客户端接入配置（无需启动服务即可获取）
	if *printCfg {
		printMCPClientConfigs(*addr, *authToken)
		return nil
	}

	st, err := newStore(ctx, cfg, *storeDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	svc := service.NewService(st)
	s, builder := newSearcher(cfg)
	svc.WithSearcher(s).WithIndexBuilder(builder)
	withImpactAnalyzer(svc, st)
	withAIEnhancer(svc, cfg) // 查询期同名方法消歧（可选）

	// 启动时全量构建索引
	if result, err := svc.BuildIndex(ctx); err != nil {
		fmt.Printf("WARN: build index: %v\n", err)
	} else {
		fmt.Printf("index built: %d docs indexed in %s\n", result.IndexedDocs, result.Duration.Round(time.Millisecond))
		idfFile := filepath.Join(cfg.Storage.Search.IDFDir, "idf.json")
		if err := os.MkdirAll(filepath.Dir(idfFile), 0755); err == nil {
			if err := builder.SaveIDF(idfFile); err != nil {
				fmt.Printf("WARN: save IDF dictionary: %v\n", err)
			}
		}
	}

	// 启动 IDF 自动持久化（60 秒间隔）
	idfFile := filepath.Join(cfg.Storage.Search.IDFDir, "idf.json")
	stopAutoSave := builder.AutoSaveIDF(idfFile, 60*time.Second)
	defer stopAutoSave()

	mcpSrv := server.NewMCPServer(svc, *addr)
	if *authToken != "" {
		mcpSrv.SetAuthToken(*authToken)
	}

	fmt.Printf("MCP Server listening on %s\n", *addr)
	return mcpSrv.Start(ctx)
}

func serveCmd(ctx context.Context, cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("http", cfg.Server.HTTPAddr, "监听地址")
	storeDir := fs.String("store", cfg.Storage.DSN, "存储目录")
	authToken := fs.String("auth-token", cfg.Server.AuthToken, "Bearer token 认证")
	fs.Parse(args)

	st, err := newStore(ctx, cfg, *storeDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	svc := service.NewService(st)
	s, builder, vecStore := newSearcherWithStore(cfg)
	svc.WithSearcher(s).WithIndexBuilder(builder)
	withImpactAnalyzer(svc, st)
	withAIEnhancer(svc, cfg) // 查询期同名方法消歧（可选）

	// 启动时全量构建索引
	if result, err := svc.BuildIndex(ctx); err != nil {
		fmt.Printf("WARN: build index: %v\n", err)
	} else {
		fmt.Printf("index built: %d docs indexed in %s\n", result.IndexedDocs, result.Duration.Round(time.Millisecond))
		idfFile := filepath.Join(cfg.Storage.Search.IDFDir, "idf.json")
		if err := os.MkdirAll(filepath.Dir(idfFile), 0755); err == nil {
			if err := builder.SaveIDF(idfFile); err != nil {
				fmt.Printf("WARN: save IDF dictionary: %v\n", err)
			}
		}
	}

	// 启动 IDF 自动持久化（60 秒间隔）
	idfFile := filepath.Join(cfg.Storage.Search.IDFDir, "idf.json")
	stopAutoSave := builder.AutoSaveIDF(idfFile, 60*time.Second)
	defer stopAutoSave()

	httpSrv := server.NewHTTPServer(svc, *addr)
	if *authToken != "" {
		httpSrv.SetAuthToken(*authToken)
	}

	// 向量索引可视化工具（默认栈 Persistent/Memory 与 chromem 共用同一向量索引，避免 embedding 不一致）
	if vecStore != nil {
		vizHandler := server.NewVizHandler(
			&vectorVizStore{VectorStore: vecStore},
			&vectorVizSearcher{Searcher: s},
			cfg.Storage.Search.VectorDim,
			cfg.Storage.Search.VectorDir,
		)
		httpSrv.SetVizHandler(vizHandler)
		fmt.Println("vector index visualization enabled at /viz")
	}

	fmt.Printf("HTTP API Server listening on %s\n", *addr)
	return httpSrv.Start(ctx)
}

// newSearcher 创建 P8.3 双路检索器 + 索引构建器，使用持久化存储 + 语义 Embedder。
//
// 优先使用 ONNX 模型（bge-small-zh-v1.5），模型文件不存在时降级到 LocalEmbedder。
// 返回 (searcher, indexBuilder)，两者共享同一份 FTS 和向量索引。
// newSearcherWithStore 创建 P8.3 双路检索器 + 索引构建器，并返回底层向量存储。
//
// 优先使用 ONNX 模型（bge-small-zh-v1.5），模型文件不存在时降级到 LocalEmbedder。
// 返回 (searcher, indexBuilder, store)，store 供 /viz 可视化复用同一份索引（统一 embedding）。
func newSearcherWithStore(cfg *config.Config) (*search.Searcher, *search.IndexBuilder, vector.VectorStore) {
	ftsFile := filepath.Join(cfg.Storage.Search.FTSDir, "fts.json")
	var fts search.FTSEngine
	pfts, err := search.NewPersistentFTS(ftsFile)
	if err != nil {
		log.Printf("WARN: new persistent FTS (%s): %v, fallback to memory", ftsFile, err)
		fts = search.NewMemoryFTS()
	} else {
		fts = pfts
	}

	vecFile := filepath.Join(cfg.Storage.Search.VectorDir, "vector.json")
	var store vector.VectorStore
	pstore, err := vector.NewPersistentStore(vecFile)
	if err != nil {
		log.Printf("WARN: new persistent vector store (%s): %v, fallback to memory", vecFile, err)
		store = vector.NewMemoryStore()
	} else {
		store = pstore
	}

	// 优先使用 ONNX Embedder（bge-small-zh），模型在 down/ 目录下；
	// 模型缺失且配置了 model_download_url 时自动下载（远程分发，见 vector.ModelDownloader）
	modelDir := cfg.Storage.Vector.ModelDir
	if modelDir == "" {
		modelDir = filepath.Join("down", "models", cfg.Storage.Vector.EmbeddingModel)
	}
	libDir := filepath.Join("down", "onnxruntime")
	var em vector.Embedder

	// 远程分发：模型缺失时尝试下载（幂等；URL 未配置则查内置模型注册表回填），
	// 失败则降级到 LocalEmbedder
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
		// 加载持久化的 IDF 词典（如果存在）
		idfFile := filepath.Join(cfg.Storage.Search.IDFDir, "idf.json")
		if err := model.LoadIDF(idfFile); err != nil {
			log.Printf("WARN: load IDF dictionary (%s): %v, will rebuild on build", idfFile, err)
		}
		em = model
	}

	indexer := vector.NewIndexer(store, em, 2)
	adapter := search.NewVectorAdapter(indexer)
	searcher := search.NewSearcher(fts, adapter, nil)
	builder := search.NewIndexBuilder(fts, indexer, em)
	return searcher, builder, store
}

// newSearcher 兼容旧签名的委托：仅返回 searcher 与 indexBuilder。
func newSearcher(cfg *config.Config) (*search.Searcher, *search.IndexBuilder) {
	s, b, _ := newSearcherWithStore(cfg)
	return s, b
}

// vectorVizStore 适配 vector.VectorStore（默认栈 Persistent/Memory）到 server.VizStore 接口。
//
// 文档原文来自向量索引的 DocContentStore 可选能力（IndexBuilder 写入）；
// 不支持的后端（chromem 等）Content 为空，以索引元数据 + 文本检索为主。
type vectorVizStore struct {
	vector.VectorStore
}

func (s *vectorVizStore) ListDocuments(ctx context.Context) ([]server.VizDocInfo, error) {
	ids, err := s.VectorStore.ListIDs(ctx)
	if err != nil {
		return nil, err
	}
	docs := make([]server.VizDocInfo, len(ids))
	for i, id := range ids {
		content := ""
		if cs, ok := s.VectorStore.(vector.DocContentStore); ok {
			content, _ = cs.Content(ctx, id)
		}
		docs[i] = server.VizDocInfo{ID: id, Content: content}
	}
	return docs, nil
}

// vectorVizSearcher 适配 *search.Searcher（默认栈 FTS 双路）到 server.VizSearcher 接口。
//
// 使用 SearchModeExact（仅 FTS），避免 SearchModeBoth 在 reranker 为 nil 时 panic。
type vectorVizSearcher struct {
	*search.Searcher
}

func (s *vectorVizSearcher) QueryText(ctx context.Context, query string, k int) ([]server.VizSearchResult, error) {
	results, err := s.Searcher.Search(ctx, query, search.SearchModeExact, k)
	if err != nil {
		return nil, err
	}
	sr := make([]server.VizSearchResult, len(results))
	for i, r := range results {
		sr[i] = server.VizSearchResult{ID: r.Symbol, Score: r.Score}
	}
	return sr, nil
}
