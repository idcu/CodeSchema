package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/idcu/codeschema/internal/config"
	"github.com/idcu/codeschema/internal/releasebench"
)

// releaseBenchCmd 公开基准：测试关联 P/R/F1 + 调用图结构指标（只报告，不设阈值）。
func releaseBenchCmd(ctx context.Context, _ *config.Config, args []string) error {
	fs := flag.NewFlagSet("release-bench", flag.ContinueOnError)
	repo := fs.String("repo", "", "仓库路径（必填）")
	lang := fs.String("lang", "go", "语言过滤：go/java/ts/py/rust/cpp/kotlin（空=全部）")
	maxPairs := fs.Int("max-pairs", 200, "测试关联真值 pair 采样上限")
	minConf := fs.Int("min-confidence", 60, "测试关联置信度下限")
	workers := fs.Int("workers", 4, "扫描并发")
	out := fs.String("out", "", "输出目录（写 report.json / report.md）")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *repo == "" {
		return fmt.Errorf("release-bench: --repo is required")
	}

	report, err := releasebench.Run(ctx, *repo, releasebench.Options{
		Language:      *lang,
		MaxPairs:      *maxPairs,
		MinConfidence: *minConf,
		Workers:       *workers,
	})
	if err != nil {
		return fmt.Errorf("release-bench: %w", err)
	}

	fmt.Printf("\n%s\n", releasebench.GenerateMarkdown(report))
	if *out != "" {
		paths, err := releasebench.WriteOutput(report, *out)
		if err != nil {
			return fmt.Errorf("release-bench write output: %w", err)
		}
		for _, p := range paths {
			fmt.Printf("saved: %s\n", p)
		}
	}
	return nil
}
