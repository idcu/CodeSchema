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

	"codeschema/internal/store"
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
  watch <path>      文件监听增量（P0）
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
	if len(args) < 1 {
		return fmt.Errorf("usage: codeschema scan <path>")
	}
	repoPath := args[0]

	fmt.Printf("scanning repository: %s\n", repoPath)

	// 初始化存储
	st := store.NewStore("file")
	if err := st.Open(ctx, "./data"); err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	// 健康检查
	if err := st.HealthCheck(ctx); err != nil {
		return fmt.Errorf("health check: %w", err)
	}

	fmt.Printf("store initialized, ready to scan %s\n", repoPath)
	fmt.Println("扫描功能将在后续迭代中实现完整 parser 适配器后启用")
	return nil
}

func watchCmd(ctx context.Context, args []string) error {
	if len(args) < 1 {
		return fmt.Errorf("usage: codeschema watch <path>")
	}
	fmt.Printf("watch mode for %s — 将在 P0 MVP 迭代中实现\n", args[0])
	return nil
}

func mcpCmd(ctx context.Context, args []string) error {
	fmt.Println("MCP Server — 将在 P0 MVP 迭代中实现")
	return nil
}

func serveCmd(ctx context.Context, args []string) error {
	fmt.Println("HTTP API Server — 将在 P0 MVP 迭代中实现")
	return nil
}