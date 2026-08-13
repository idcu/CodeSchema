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

	"codeschema/internal/config"
	"codeschema/internal/parser"
	"codeschema/internal/scheduler"
	"codeschema/internal/scanner"
	"codeschema/internal/search"
	"codeschema/internal/server"
	"codeschema/internal/service"
	"codeschema/internal/store"
	"codeschema/internal/vector"
	"codeschema/internal/watcher"
)

var (
	version = "0.1.0"
)

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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 捕获退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
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
			defer cw.Stop()

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
	st := store.NewStore(cfg.Storage.Driver)
	if err := st.Open(ctx, *storeDir); err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// 健康检查
	if err := st.HealthCheck(ctx); err != nil {
		return fmt.Errorf("health check: %w", err)
	}

	// 初始化注册中心
	reg := parser.NewRegistry()

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
	st := store.NewStore(cfg.Storage.Driver)
	if err := st.Open(ctx, *storeDir); err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// 初始化注册中心
	reg := parser.NewRegistry()

	// 创建 Scanner
	s := scanner.NewScanner(st, reg, *workers)

	// 创建调度器
	sched := scheduler.NewScheduler(*debounceMs, 1000)

	// 创建搜索组件
	searcher, builder := newSearcher(cfg)
	svc := service.NewService(st)
	svc.WithSearcher(searcher).WithIndexBuilder(builder)

	// 启动异步索引队列
	builder.StartAsync(ctx, 64)
	defer builder.StopAsync()

	// 启动时全量构建索引（如果持久化索引存在则跳过）
	if result, err := svc.BuildIndex(ctx); err != nil {
		fmt.Printf("WARN: build index: %v\n", err)
	} else {
		fmt.Printf("index built: %d docs indexed in %s\n", result.IndexedDocs, result.Duration.Round(time.Millisecond))
		// 持久化 IDF 词典
		idfFile := filepath.Join(cfg.Storage.Search.IDFDir, "idf.json")
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
	fs.Parse(args)

	st := store.NewStore(cfg.Storage.Driver)
	if err := st.Open(ctx, *storeDir); err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	svc := service.NewService(st)
	s, _ := newSearcher(cfg)
	svc.WithSearcher(s)
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

	st := store.NewStore(cfg.Storage.Driver)
	if err := st.Open(ctx, *storeDir); err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	svc := service.NewService(st)
	s, _ := newSearcher(cfg)
	svc.WithSearcher(s)
	httpSrv := server.NewHTTPServer(svc, *addr)
	if *authToken != "" {
		httpSrv.SetAuthToken(*authToken)
	}

	fmt.Printf("HTTP API Server listening on %s\n", *addr)
	return httpSrv.Start(ctx)
}

// newSearcher 创建 P8.3 双路检索器 + 索引构建器，使用持久化存储 + 本地 Embedder。
//
// 返回 (searcher, indexBuilder)，两者共享同一份 FTS 和向量索引。
func newSearcher(cfg *config.Config) (*search.Searcher, *search.IndexBuilder) {
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

	model := vector.NewLocalEmbedder(cfg.Storage.Search.VectorDim)
	// 加载持久化的 IDF 词典（如果存在）
	idfFile := filepath.Join(cfg.Storage.Search.IDFDir, "idf.json")
	if err := model.LoadIDF(idfFile); err != nil {
		log.Printf("WARN: load IDF dictionary (%s): %v, will rebuild on build", idfFile, err)
	}

	indexer := vector.NewIndexer(store, model, 2)
	adapter := search.NewVectorAdapter(indexer)
	searcher := search.NewSearcher(fts, adapter, nil)
	builder := search.NewIndexBuilder(fts, indexer, model)
	return searcher, builder
}