// Package retrieval 通用双路检索与融合重排能力（领域无关）。
//
// 本包只提供中性机制：FTS 全文搜索引擎、向量语义检索适配接口、融合
// 重排器、双路检索器，结果统一为中性 Result（领域字段放入 Meta）。
// 不引用任何代码模式 / 符号 / 租户等业务语义，可作为独立公共模块被
// 多个领域复用。领域侧（search 包）负责把领域结果投影到 Result / 反向。
package retrieval

import "math"

// Result 中性检索结果。
//
// 领域相关字段（kind / file / snippet / source 等）统一放入 Meta，
// 使本包不绑定任何具体业务语义。
type Result struct {
	// ID 文档唯一 ID（领域中的 symbol / doc id）。
	ID string `json:"id"`
	// Score 融合或原始得分。
	Score float64 `json:"score"`
	// Confidence 绝对置信度 [0,1]：语义=余弦相似度，FTS=BM25 映射。
	Confidence float64 `json:"confidence,omitempty"`
	// Meta 任意领域字段（kind / file / source / snippet 等）。
	Meta map[string]string `json:"meta,omitempty"`
}

// metaGet 安全读取 Meta 字段（Meta 为 nil 时返回空串）。
func metaGet(r Result, key string) string {
	if r.Meta == nil {
		return ""
	}
	return r.Meta[key]
}

// FTSMode 全文搜索模式。
type FTSMode string

const (
	FTSModeExact   FTSMode = "exact"   // 精确匹配
	FTSModeFuzzy   FTSMode = "fuzzy"   // 模糊匹配（前缀 + 子串）
	FTSModeBoolean FTSMode = "boolean" // 布尔查询
)

// BM25 参数（绝对相关度量纲）。
const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

// SearchMode 双路检索模式。
type SearchMode string

const (
	SearchModeExact    SearchMode = "exact"    // 仅 FTS 精确搜索
	SearchModeSemantic SearchMode = "semantic" // 仅向量语义搜索
	SearchModeBoth     SearchMode = "both"     // 双路融合检索（默认）
)

// SearchOptions 检索选项。
//
// MinScore 为绝对置信度阈值（[0,1]）：低于阈值的结果被过滤。
// MinScore<=0 表示不启用过滤（向后兼容）。
type SearchOptions struct {
	Mode     SearchMode
	Limit    int
	MinScore float64
}

// exactConfidenceTau 绝对置信度标定常数：纯 FTS 模式 Confidence = 1 - exp(-BM25/tau)。
const exactConfidenceTau = 0.3

// withExactConfidence 将纯 FTS（BM25）结果映射为绝对置信度 [0,1)。
//
// 与结果集大小无关：同一文档+查询始终得到同一置信度，使 MinScore 可作
// 绝对阈值而不受结果集强弱影响。
func withExactConfidence(results []Result) []Result {
	for i := range results {
		results[i].Confidence = 1 - math.Exp(-results[i].Score/exactConfidenceTau)
	}
	return results
}
