// 离线标签覆盖率统计命令（P21 质量评估基线，无需 LLM API key）。
//
// 用法：codeschema tags-stats [--store=<dir>]
// 扫描已索引库中全部实体（类 + 方法），输出六类标签（layer/biz/tech/risk/test/lang）
// 的覆盖统计——实体总数 / 已打标签数 / 覆盖率 / 分类分布 / TopTags。
// 作为接入 LLM 标签（Enhancer）后的质量对比基准：覆盖率应随 LLM 补全上升。
package main

import (
	"context"
	"flag"
	"fmt"

	"github.com/idcu/codeschema/internal/ai"
	"github.com/idcu/codeschema/internal/config"
)

func tagsStatsCmd(ctx context.Context, cfg *config.Config, args []string) error {
	fs := flag.NewFlagSet("tags-stats", flag.ExitOnError)
	storeDir := fs.String("store", cfg.Storage.DSN, "存储目录")
	topN := fs.Int("top", 10, "TopTags 展示条数")
	fs.Parse(args)

	st, err := newStore(ctx, cfg, *storeDir)
	if err != nil {
		return fmt.Errorf("open store: %w", err)
	}
	defer st.Close()

	if err := st.HealthCheck(ctx); err != nil {
		return fmt.Errorf("health check: %w", err)
	}

	stats, err := ai.ComputeTagCoverage(ctx, st)
	if err != nil {
		return fmt.Errorf("compute tag coverage: %w", err)
	}

	// 展示分类分布（固定顺序：layer/biz/tech/risk/test/lang）。
	catOrder := []string{"layer", "biz", "tech", "risk", "test", "lang"}
	fmt.Printf("实体标签覆盖率统计（store=%s）\n", *storeDir)
	fmt.Printf("  实体总数    : %d（类 %d + 方法 %d）\n", stats.TotalEntities, stats.ClassTotal, stats.MethodTotal)
	fmt.Printf("  已打标签实体: %d（类 %d + 方法 %d）\n", stats.TaggedEntities, stats.ClassTagged, stats.MethodTagged)
	fmt.Printf("  覆盖率      : %.1f%%\n", stats.CoverageRate*100)
	fmt.Println("  分类分布（标签出现次数 / 命中实体数）：")
	for _, cat := range catOrder {
		cnt := stats.CategoryCounts[cat]
		tagged := stats.CategoryTagged[cat]
		fmt.Printf("    %-10s 出现 %4d 次，命中 %4d 个实体\n", cat, cnt, tagged)
	}

	fmt.Printf("  高频标签 Top%d：\n", *topN)
	for i, tf := range stats.TopTags {
		if i >= *topN {
			break
		}
		fmt.Printf("    %-20s %-6s x%d\n", tf.Tag, tf.Cat, tf.Freq)
	}

	return nil
}
