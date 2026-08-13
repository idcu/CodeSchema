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
  watch <path>      文件监听增量（P0，轮询模式）
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

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// 捕获退出信号
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sigCh
		cancel()
	}()

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
	return nil
}

func watchCmd(ctx context.Context, cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	workers := fs.Int("workers", cfg.Scanner.Workers, "并发解析 worker 数")
	storeDir := fs.String("store", cfg.Storage.DSN, "存储目录")
	debounceMs := fs.Int("debounce", cfg.Watcher.DebounceMs, "防抖窗口（毫秒）")
	fs.Parse(args)

	repoPath := fs.Arg(0)
	if repoPath == "" {
		return fmt.Errorf("usage: codeschema watch [--workers=%d] [--store=%s] [--debounce=%d] <path>", cfg.Scanner.Workers, cfg.Storage.DSN, cfg.Watcher.DebounceMs)
	}

	fmt.Printf("watching repository: %s (workers=%d, debounce=%dms)\n", repoPath, *workers, *debounceMs)

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

	// 创建监听器
	pw := watcher.NewPollWatcher(repoPath, s, sched, 1*time.Second, cfg.Watcher.IgnoreDirs)

	// 启动调度器
	go sched.Start(ctx, func(ctx context.Context, path string) error {
		return s.ProcessFile(ctx, path)
	})

	// 启动监听器（阻塞）
	fmt.Println("watcher started, press Ctrl+C to stop")
	return pw.Start(ctx)
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
	svc.WithSearcher(newSearcher(cfg))
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
	svc.WithSearcher(newSearcher(cfg))
	httpSrv := server.NewHTTPServer(svc, *addr)
	if *authToken != "" {
		httpSrv.SetAuthToken(*authToken)
	}

	fmt.Printf("HTTP API Server listening on %s\n", *addr)
	return httpSrv.Start(ctx)
}

// newSearcher 创建 P8.2 双路检索器，使用持久化存储 + 本地 Embedder。
//
// 组成：
//   - FTS: PersistentFTS（磁盘持久化全文搜索，路径由 cfg.Storage.Search.FTSDir 决定）
//   - 向量: LocalEmbedder（1024 维 TF-IDF 哈希）+ PersistentStore + Indexer + VectorAdapter
//   - 融合: 默认权重 Reranker（FTS 0.3 / 向量 0.7）
//
// 当网络恢复后，可切换为 chromem-go + SQLite FTS5。
func newSearcher(cfg *config.Config) *search.Searcher {
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
	indexer := vector.NewIndexer(store, model, 2)
	adapter := search.NewVectorAdapter(indexer)
	return search.NewSearcher(fts, adapter, nil)
}