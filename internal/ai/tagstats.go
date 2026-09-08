// Package ai 的离线标签覆盖率统计（P21 AI 标签生产化的质量评估基线）。
//
// 背景：P21 的 Tagger/DocEnhancer 在缺 LLM API key 时优雅降级（`ai: enhancement disabled`），
// 真实质量端到端验证依赖 key。本文件提供**不依赖 key 的离线评估基线**：扫描已索引库中
// 全部实体（类 + 方法），统计六类标签（layer/biz/tech/risk/test/lang）的覆盖情况，作为
// 后续接入 LLM 标签后的质量对比基准（覆盖率应随 LLM 补全上升、分类分布应更均衡）。
package ai

import (
	"context"
	"sort"

	"github.com/idcu/codeschema/internal/store"
)

// TagFreq 单个标签的出现频次（用于 TopTags 排名）。
type TagFreq struct {
	Tag  string // 标签名
	Cat  string // 分类（DeriveTagCategory）
	Freq int    // 出现次数（类或方法实体上出现的总次数）
}

// TagCoverageStats 标签覆盖率统计结果。
type TagCoverageStats struct {
	TotalEntities  int              // 类 + 方法实体总数
	TaggedEntities int              // 至少携带一个标签的实体数
	ClassTotal     int              // 类总数
	ClassTagged    int              // 已打标签的类数
	MethodTotal    int              // 方法总数
	MethodTagged   int              // 已打标签的方法数
	CoverageRate   float64          // TaggedEntities / TotalEntities（0-1）
	CategoryCounts map[string]int   // 每分类标签总出现次数（含同一实体多标签）
	CategoryTagged map[string]int   // 每分类至少命中一个标签的实体数
	TopTags        []TagFreq        // 最常见的标签（按 Freq 降序）
}

// ComputeTagCoverage 遍历 store 中全部文件/类/方法，计算标签覆盖率与分类分布。
//
// 复用 store 的既有枚举接口（GetAllFiles / GetClassesByFileID / GetMethodsByClassID /
// GetTagsByClassID / GetTagsByMethodID）与分类口径（store.DeriveTagCategory），
// 保证与运行时标签口径一致。单实体失败不中断整体（延续 tagger 的容错范式）。
func ComputeTagCoverage(ctx context.Context, st store.Store) (*TagCoverageStats, error) {
	files, err := st.GetAllFiles(ctx)
	if err != nil {
		return nil, err
	}

	stats := &TagCoverageStats{
		CategoryCounts: make(map[string]int),
		CategoryTagged: make(map[string]int),
	}

	tagFreq := make(map[string]int)

	// 记录一个实体命中某分类：合并进 CategoryCounts / CategoryTagged 并按实体去重。
	record := func(tags []string) {
		stats.TaggedEntities++
		seenCat := make(map[string]bool)
		for _, t := range tags {
			tagFreq[t]++
			cat := store.DeriveTagCategory(t)
			stats.CategoryCounts[cat]++
			if !seenCat[cat] {
				seenCat[cat] = true
				stats.CategoryTagged[cat]++
			}
		}
	}

	for _, f := range files {
		classes, err := st.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			continue
		}
		for _, cls := range classes {
			stats.ClassTotal++
			stats.TotalEntities++
			if ct, err := st.GetTagsByClassID(ctx, cls.ID); err == nil && len(ct) > 0 {
				record(ct)
				stats.ClassTagged++
			}
			methods, err := st.GetMethodsByClassID(ctx, cls.ID)
			if err != nil {
				continue
			}
			for _, m := range methods {
				stats.MethodTotal++
				stats.TotalEntities++
				if mt, err := st.GetTagsByMethodID(ctx, m.ID); err == nil && len(mt) > 0 {
					record(mt)
					stats.MethodTagged++
				}
			}
		}
	}

	if stats.TotalEntities > 0 {
		stats.CoverageRate = float64(stats.TaggedEntities) / float64(stats.TotalEntities)
	}

	// TopTags：按频次降序，频次相同按标签名。
	top := make([]TagFreq, 0, len(tagFreq))
	for tag, freq := range tagFreq {
		top = append(top, TagFreq{Tag: tag, Cat: store.DeriveTagCategory(tag), Freq: freq})
	}
	sort.Slice(top, func(i, j int) bool {
		if top[i].Freq != top[j].Freq {
			return top[i].Freq > top[j].Freq
		}
		return top[i].Tag < top[j].Tag
	})
	stats.TopTags = top

	return stats, nil
}