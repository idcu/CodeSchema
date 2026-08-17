package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/idcu/codeschema/internal/agentbench"
	"github.com/idcu/codeschema/internal/benchmark"
	"github.com/idcu/codeschema/internal/config"
)

// benchmarkCmd 实现 `codeschema benchmark` 子命令（技术路线红灯笼①落地）。
//
// 用法：
//
//	codeschema benchmark [--repos="path1;path2"] [--out=build/bench-compare.json] [--workers=N] [repo...]
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

// agentBenchCmd 实现 `codeschema agent-bench` 子命令（对外可信基准，对标
// Sverklo 90-task bench / grepai token 节省）。
//
// 用法：
//
//	codeschema agent-bench [--repo=path] [--repos="p1;p2"] [--out=build/agent-task-bench] [--workers=N] [--context-lines=10]
//
// 评测对象：单仓库（默认 CodeSchema 自身）或多仓库（--repos 分号分隔）+ 内置
// Agent 任务集（bug 修复/特性实现/重构，按 RepoHint 匹配仓库），对比 none/full/
// minimal 三档上下文供给的「任务可完成率 × token 消耗」，输出 Markdown 到
// stdout、md+json 落盘到 --out。
func agentBenchCmd(ctx context.Context, cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("agent-bench", flag.ExitOnError)
	repoFlag := fs.String("repo", "", "评测仓库路径（默认 CodeSchema 自身，与 --repos 二选一）")
	reposFlag := fs.String("repos", "", "分号分隔的多仓库路径列表（多仓库评测）")
	outFlag := fs.String("out", filepath.Join("build", "agent-task-bench"), "报告输出目录（md + json）")
	workers := fs.Int("workers", cfg.Scanner.Workers, "并发解析 worker 数")
	contextLines := fs.Int("context-lines", 10, "full 档位注入上下文行数（0 表示不裁剪）")
	fs.Parse(args)

	var repos []string
	switch {
	case *reposFlag != "":
		repos = splitRepos(*reposFlag)
	case *repoFlag != "":
		repos = []string{*repoFlag}
	default:
		repos = []string{agentbench.DefaultRepo()}
	}
	for _, r := range repos {
		info, err := os.Stat(r)
		if err != nil || !info.IsDir() {
			return fmt.Errorf("agent-bench repo %s: not a directory or unreadable", r)
		}
	}

	tasks := agentbench.DefaultTasks()
	agentbench.SortTasks(tasks)
	fmt.Printf("agent task bench on %d repo(s) (%d tasks, workers=%d, context_lines=%d)\n",
		len(repos), len(tasks), *workers, *contextLines)

	opts := agentbench.Options{Workers: *workers, ContextLines: *contextLines}

	if len(repos) == 1 {
		report, err := agentbench.Run(ctx, repos[0], tasks, opts)
		if err != nil {
			return fmt.Errorf("agent-bench: %w", err)
		}
		md := agentbench.GenerateMarkdown(report)
		fmt.Printf("\n%s\n", md)
		paths, err := agentbench.WriteOutput(report, *outFlag)
		if err != nil {
			return fmt.Errorf("agent-bench write output: %w", err)
		}
		for _, p := range paths {
			fmt.Printf("agent task bench report saved to: %s\n", p)
		}
		return nil
	}

	// 多仓库：RunMulti + 跨仓对比 Markdown。
	reports, err := agentbench.RunMulti(ctx, repos, tasks, opts)
	if err != nil {
		return fmt.Errorf("agent-bench: %w", err)
	}
	md := agentbench.GenerateMultiMarkdown(reports)
	fmt.Printf("\n%s\n", md)
	for _, r := range reports {
		sub := filepath.Join(*outFlag, filepath.Base(r.RepoPath))
		if _, err := agentbench.WriteOutput(r, sub); err != nil {
			return fmt.Errorf("agent-bench write output %s: %w", r.RepoPath, err)
		}
		fmt.Printf("agent task bench report saved to: %s\n", sub)
	}
	return nil
}
