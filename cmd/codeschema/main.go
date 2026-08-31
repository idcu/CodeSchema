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

	gcm "gitee.com/idcu-go/graceful"
	"github.com/idcu/codeschema/internal/config"
	rt "github.com/idcu/codeschema/internal/runtime"
	"github.com/idcu/codeschema/internal/scanner"
	"github.com/idcu/codeschema/internal/scheduler"
	"github.com/idcu/codeschema/internal/search"
	"github.com/idcu/codeschema/internal/server"
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/tenant"
	"github.com/idcu/codeschema/internal/vector"
	"github.com/idcu/codeschema/internal/watcher"
)

// withImpactAnalyzer / newAIEnhancer / withAIEnhancer / runTagAll 等运行期装配函数
// 已迁移至 internal/runtime 与 internal/tenant 包，供单项目与多租户路径统一复用。

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
	graceful := gcm.NewGracefulManager(30 * time.Second)
	gcm.ForceExitOnSecondSignal(graceful)

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
	var cfgWatcher *config.ConfigWatcher
	if *configPath != "" {
		switch args[0] {
		case "watch", "mcp", "serve":
			cfgWatcher = config.NewConfigWatcher(*configPath, cfg, nil)
			cfgWatcher.SetPollInterval(2 * time.Second)
			go func() {
				if err := cfgWatcher.Start(ctx); err != nil && err != context.Canceled {
					log.Printf("config watcher stopped: %v", err)
				}
			}()
			graceful.RegisterFunc("config_watcher", func(ctx context.Context) error {
				cfgWatcher.Stop()
				return nil
			})

			// 将配置实例替换为可热更新的配置
			origCfg := cfg
			cfg = cfgWatcher.GetConfig()
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

	case "agent-bench":
		return agentBenchCmd(ctx, cfg, args[1:])

	case "rebuild-kv":
		return rebuildKVCmd(ctx, cfg, args[1:])

	case "mcp":
		return mcpCmd(ctx, cfg, cfgWatcher, args[1:])

	case "serve":
		return serveCmd(ctx, cfg, cfgWatcher, args[1:])

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
	reg := rt.NewParserRegistry(ctx, cfg, repoPath)

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
	searcher, builder := rt.NewSearcher(cfg)
	svc := service.NewService(st)
	svc.WithSearcher(searcher).WithIndexBuilder(builder)
	rt.WithImpactAnalyzer(svc, st)
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
	if err := rt.RunTagAll(ctx, st, cfg); err != nil {
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
	reg := rt.NewParserRegistry(ctx, cfg, repoPath)

	// 创建 Scanner
	s := scanner.NewScanner(st, reg, *workers)

	// 创建调度器
	sched := scheduler.NewScheduler[string](*debounceMs, 1000)

	// 创建搜索组件
	searcher, builder := rt.NewSearcher(cfg)
	svc := service.NewService(st)
	svc.WithSearcher(searcher).WithIndexBuilder(builder)
	rt.WithImpactAnalyzer(svc, st)

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

func mcpCmd(ctx context.Context, cfg *config.Config, cfgWatcher *config.ConfigWatcher, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	addr := fs.String("addr", cfg.Server.MCPAddr, "监听地址")
	storeDir := fs.String("store", cfg.Storage.DSN, "存储目录")
	authToken := fs.String("auth-token", cfg.Server.AuthToken, "Bearer token 认证")
	printCfg := fs.Bool("print-config", false, "打印各客户端 MCP 接入配置片段并退出（不启动服务）")
	stdioMode := fs.Bool("stdio", false, "以 stdio 传输模式启动（供仅支持 stdio 的 MCP 客户端直连，替代 SSE）")
	fs.Parse(args)

	// T2-5：一键打印客户端接入配置（无需启动服务即可获取）
	if *printCfg {
		printMCPClientConfigs(*addr, *authToken)
		return nil
	}

	if *storeDir != "" {
		cfg.Storage.DSN = *storeDir
	}

	// 多租户：单个进程按配置服务多个隔离仓库（无 tenants 配置时退回单项目模式）。
	mgr, err := tenant.NewManager(ctx, cfg, newStore)
	if err != nil {
		return fmt.Errorf("init tenant manager: %w", err)
	}
	defer mgr.Close()

	mcpSrv := server.NewMCPServer(nil, *addr)
	mcpSrv.SetTenantManager(mgr)
	mcpSrv.SetContextDefaults(rt.ContextOptionsFromConfig(cfg))
	if *authToken != "" {
		mcpSrv.SetAuthToken(*authToken)
	}

	// 全局能力热重载（单一回调，避免覆盖）：配置文件变更时，无需重启进程，
	// 自动增量应用租户集合 + 认证令牌（preset 变更经 config.Load 重新应用，
	// 其影响的服务端字段在此连带生效；以配置文件为热重载期的唯一权威来源）。
	if cfgWatcher != nil {
		cfgWatcher.SetOnReload(func(oldCfg, newCfg *config.Config) {
			if err := mgr.Apply(ctx, newCfg); err != nil {
				log.Printf("tenant hot-reload error: %v", err)
			}
			mcpSrv.SetAuthToken(newCfg.Server.AuthToken)
			log.Printf("MCP server hot-reload: authToken updated")
		})
	}

	// T4-1：stdio 传输模式（供仅支持 stdio 的客户端直连）
	if *stdioMode {
		return mcpSrv.StartStdio(ctx)
	}

	fmt.Printf("MCP Server listening on %s\n", *addr)
	return mcpSrv.Start(ctx)
}

func serveCmd(ctx context.Context, cfg *config.Config, cfgWatcher *config.ConfigWatcher, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("http", cfg.Server.HTTPAddr, "监听地址")
	storeDir := fs.String("store", cfg.Storage.DSN, "存储目录")
	authToken := fs.String("auth-token", cfg.Server.AuthToken, "Bearer token 认证")
	fs.Parse(args)

	if *storeDir != "" {
		cfg.Storage.DSN = *storeDir
	}

	// 多租户：单个进程按配置服务多个隔离仓库（无 tenants 配置时退回单项目模式）。
	mgr, err := tenant.NewManager(ctx, cfg, newStore)
	if err != nil {
		return fmt.Errorf("init tenant manager: %w", err)
	}
	defer mgr.Close()

	httpSrv := server.NewHTTPServer(nil, *addr)
	httpSrv.SetTenantManager(mgr)
	httpSrv.SetContextDefaults(rt.ContextOptionsFromConfig(cfg))
	if *authToken != "" {
		httpSrv.SetAuthToken(*authToken)
	}
	if cfg.Server.RateLimit > 0 {
		httpSrv.SetRateLimit(cfg.Server.RateLimit)
	}

	// 全局能力热重载（单一回调，避免覆盖）：配置文件变更时，无需重启进程，
	// 自动增量应用租户集合 + 监听地址 / 认证令牌 / 限流（preset 变更经
	// config.Load 重新应用，其影响的服务端字段在此连带生效；以配置文件为
	// 热重载期的唯一权威来源）。
	if cfgWatcher != nil {
		cfgWatcher.SetOnReload(func(oldCfg, newCfg *config.Config) {
			if err := mgr.Apply(ctx, newCfg); err != nil {
				log.Printf("tenant hot-reload error: %v", err)
			}
			if err := httpSrv.UpdateRuntime(newCfg.Server.HTTPAddr, newCfg.Server.AuthToken, newCfg.Server.RateLimit); err != nil {
				log.Printf("server hot-reload error: %v", err)
			}
		})
	}

	// 向量索引可视化工具（默认栈），挂到默认租户的运行期组件上。
	if rt0, rerr := mgr.Runtime(""); rerr == nil && rt0.VecStore != nil {
		if cfg0, cerr := mgr.Config(""); cerr == nil {
			vizHandler := server.NewVizHandler(
				&vectorVizStore{VectorStore: rt0.VecStore},
				&vectorVizSearcher{Searcher: rt0.Searcher},
				cfg0.Storage.Search.VectorDim,
				cfg0.Storage.Search.VectorDir,
			)
			httpSrv.SetVizHandler(vizHandler)
			fmt.Println("vector index visualization enabled at /viz")

			// 向量索引健康检查：真实探测当前向量存储（Size 返回文档数，
			// 用其探活；后端不可用视为 degraded）。
			vs := rt0.VecStore
			httpSrv.SetVectorHealthCheck(func(ctx context.Context) error {
				_ = vs.Size()
				return nil
			})
		}
	}
	// KV 缓存健康检查：探测默认租户 store 是否叠加了 Redis L2 缓存。
	if rt0, rerr := mgr.Runtime(""); rerr == nil && rt0.Store != nil {
		if cr, ok := rt0.Store.(store.CacheReader); ok {
			// redisCacheStore 内嵌 store.Store（含 HealthCheck），
			// 经可选接口断言取其健康检查函数。
			if hc, ok := cr.(interface {
				HealthCheck(ctx context.Context) error
			}); ok {
				httpSrv.SetKVHealthCheck(func(ctx context.Context) error { return hc.HealthCheck(ctx) })
			}
		}
	}

	fmt.Printf("HTTP API Server listening on %s\n", *addr)
	return httpSrv.Start(ctx)
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
