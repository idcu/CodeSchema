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
	"syscall"
	"time"

	"codeschema/internal/parser"
	"codeschema/internal/scheduler"
	"codeschema/internal/scanner"
	"codeschema/internal/server"
	"codeschema/internal/service"
	"codeschema/internal/store"
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
	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, `CodeSchema v%s — 代码元数据 KV/DB 系统

Usage:
  codeschema <command> [options]

Commands:
  scan <path>       扫描仓库并入库
  watch <path>      文件监听增量（P0，轮询模式）
  rebuild-kv        重建 KV 缓存（P2）
  mcp               启动 MCP Server（P0）
  serve             启动 HTTP API Server（P0）
  version           显示版本信息

Use "codeschema <command> -h" for more information about a command.
`, version)
	}

	flag.Parse()
	args := flag.Args()
	if len(args) == 0 {
		flag.Usage()
		return nil
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
		return scanCmd(ctx, args[1:])

	case "watch":
		return watchCmd(ctx, args[1:])

	case "mcp":
		return mcpCmd(ctx, args[1:])

	case "serve":
		return serveCmd(ctx, args[1:])

	default:
		return fmt.Errorf("unknown command: %s", args[0])
	}
}

func scanCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("scan", flag.ExitOnError)
	workers := fs.Int("workers", 4, "并发解析 worker 数")
	storeDir := fs.String("store", "./data", "存储目录")
	fs.Parse(args)

	repoPath := fs.Arg(0)
	if repoPath == "" {
		return fmt.Errorf("usage: codeschema scan [--workers=4] [--store=./data] <path>")
	}

	fmt.Printf("scanning repository: %s (workers=%d)\n", repoPath, *workers)

	// 初始化存储
	st := store.NewStore("file")
	if err := st.Open(ctx, *storeDir); err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// 健康检查
	if err := st.HealthCheck(ctx); err != nil {
		return fmt.Errorf("health check: %w", err)
	}

	// 初始化注册中心（P0 暂无实际适配器，仅扫描能识别语言的文件）
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

func watchCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("watch", flag.ExitOnError)
	workers := fs.Int("workers", 4, "并发解析 worker 数")
	storeDir := fs.String("store", "./data", "存储目录")
	debounceMs := fs.Int("debounce", 300, "防抖窗口（毫秒）")
	fs.Parse(args)

	repoPath := fs.Arg(0)
	if repoPath == "" {
		return fmt.Errorf("usage: codeschema watch [--workers=4] [--store=./data] [--debounce=300] <path>")
	}

	fmt.Printf("watching repository: %s (workers=%d, debounce=%dms)\n", repoPath, *workers, *debounceMs)

	// 初始化存储
	st := store.NewStore("file")
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
	pw := watcher.NewPollWatcher(repoPath, s, sched, 1*time.Second, nil)

	// 启动调度器
	go sched.Start(ctx, func(ctx context.Context, path string) error {
		return s.ProcessFile(ctx, path)
	})

	// 启动监听器（阻塞）
	fmt.Println("watcher started, press Ctrl+C to stop")
	return pw.Start(ctx)
}

func mcpCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("mcp", flag.ExitOnError)
	addr := fs.String("addr", ":8080", "监听地址")
	storeDir := fs.String("store", "./data", "存储目录")
	fs.Parse(args)

	st := store.NewStore("file")
	if err := st.Open(ctx, *storeDir); err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	svc := service.NewService(st)
	mcpSrv := server.NewMCPServer(svc, *addr)

	fmt.Printf("MCP Server listening on %s\n", *addr)
	return mcpSrv.Start(ctx)
}

func serveCmd(ctx context.Context, args []string) error {
	fs := flag.NewFlagSet("serve", flag.ExitOnError)
	addr := fs.String("http", ":8081", "监听地址")
	storeDir := fs.String("store", "./data", "存储目录")
	fs.Parse(args)

	st := store.NewStore("file")
	if err := st.Open(ctx, *storeDir); err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	svc := service.NewService(st)
	httpSrv := server.NewHTTPServer(svc, *addr)

	fmt.Printf("HTTP API Server listening on %s\n", *addr)
	return httpSrv.Start(ctx)
}