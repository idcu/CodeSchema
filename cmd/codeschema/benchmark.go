package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/idcu/codeschema/internal/benchmark"
	"github.com/idcu/codeschema/internal/config"
)

// benchmarkCmd 实现 `codeschema benchmark` 子命令（技术路线红灯笼①落地）。
//
// 用法：
//   codeschema benchmark [--repos="path1;path2"] [--out=build/bench-compare.json] [--workers=N] [repo...]
//
// 未指定仓库时：优先取位置参数（可多个），其次 CODESCHEMA_BENCH_REPOS 环境变量，
// 最后回退到当前目录。输出 Markdown 对比到 stdout，JSON 落盘到 --out。
func benchmarkCmd(ctx context.Context, cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("benchmark", flag.ExitOnError)
	reposFlag := fs.String("repos", "", "分号分隔的仓库路径列表（可选，也可用位置参数）")
	outFlag := fs.String("out", filepath.Join("build", "bench-compare.json"), "对比报告 JSON 输出路径")
	workers := fs.Int("workers", cfg.Scanner.Workers, "并发解析 worker 数")
	configDesc := fs.String("config-desc", "codeschema benchmark CLI", "报告配置描述")
	fs.Parse(args)

	// 解析仓库列表：--repos > 位置参数 > 环境变量 > 当前目录
	var repos []string
	if *reposFlag != "" {
		repos = splitRepos(*reposFlag)
	} else if fs.NArg() > 0 {
		repos = fs.Args()
	} else if env := os.Getenv("CODESCHEMA_BENCH_REPOS"); env != "" {
		repos = splitRepos(env)
	} else {
		wd, err := os.Getwd()
		if err != nil {
			return fmt.Errorf("getwd: %w", err)
		}
		repos = []string{wd}
	}

	// 校验仓库存在
	var valid []string
	for _, r := range repos {
		info, err := os.Stat(r)
		if err != nil {
			fmt.Printf("WARN: skip repo %s: %v\n", r, err)
			continue
		}
		if !info.IsDir() {
			fmt.Printf("WARN: skip repo %s: not a directory\n", r)
			continue
		}
		valid = append(valid, r)
	}
	if len(valid) == 0 {
		return fmt.Errorf("no valid repositories to benchmark")
	}

	fmt.Printf("benchmarking %d repo(s):\n", len(valid))
	for _, r := range valid {
		fmt.Printf("  - %s\n", r)
	}

	results, err := benchmark.Run(ctx, valid, benchmark.Options{
		Workers:    *workers,
		ConfigDesc: *configDesc,
	})
	if err != nil {
		return fmt.Errorf("benchmark: %w", err)
	}
	if len(results) == 0 {
		return fmt.Errorf("benchmark: no results produced")
	}

	// Markdown 输出到 stdout
	baseline := results[0].RepoName
	md := benchmark.GenerateComparisonMarkdown(results, baseline)
	fmt.Printf("\n%s\n", md)

	// JSON 落盘
	jsonData, err := benchmark.GenerateComparisonJSON(results, baseline, *configDesc)
	if err != nil {
		return fmt.Errorf("generate json: %w", err)
	}
	if err := os.MkdirAll(filepath.Dir(*outFlag), 0o755); err != nil {
		return fmt.Errorf("mkdir out dir: %w", err)
	}
	if err := os.WriteFile(*outFlag, jsonData, 0o644); err != nil {
		return fmt.Errorf("write %s: %w", *outFlag, err)
	}
	fmt.Printf("comparison report saved to: %s\n", *outFlag)

	return nil
}

// splitRepos 按分号拆分仓库路径列表，去除空白项。
func splitRepos(s string) []string {
	var out []string
	for _, p := range strings.Split(s, ";") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}
