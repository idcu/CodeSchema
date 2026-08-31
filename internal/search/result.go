// Package search 双路检索领域适配层（代码模式 / 符号语义）。
//
// 通用检索机制（FTS 引擎、向量适配接口、融合重排、双路检索器）已下沉到
// internal/retrieval，结果统一为中性 retrieval.Result。本包保留领域结果
// SearchResult（含 symbol / kind / file / snippet / source 等代码语义字段），
// 并通过投影函数在 retrieval.Result 与 SearchResult 之间转换，使既有调用方
// 零改动。
package search

import (
	"github.com/idcu/codeschema/internal/retrieval"
)

// SearchResult 检索结果（领域结构）。
type SearchResult struct {
	Symbol     string  `json:"symbol"`
	Kind       string  `json:"kind"`
	File       string  `json:"file"`
	Score      float64 `json:"score"`
	Snippet    string  `json:"snippet,omitempty"`
	Source     string  `json:"source"`               // "fts" 或 "vector" 或 "fused"
	Confidence float64 `json:"confidence,omitempty"` // 绝对置信度 [0,1]
}

// projectTo 将领域结果投影为中性 retrieval.Result。
func projectTo(in []SearchResult) []retrieval.Result {
	out := make([]retrieval.Result, len(in))
	for i, r := range in {
		out[i] = retrieval.Result{
			ID:         r.Symbol,
			Score:      r.Score,
			Confidence: r.Confidence,
			Meta: map[string]string{
				"kind":    r.Kind,
				"file":    r.File,
				"snippet": r.Snippet,
				"source":  r.Source,
			},
		}
	}
	return out
}

// projectFrom 将中性 retrieval.Result 投影回领域结果。
func projectFrom(in []retrieval.Result) []SearchResult {
	out := make([]SearchResult, len(in))
	for i, r := range in {
		out[i] = SearchResult{
			Symbol:     r.ID,
			Score:      r.Score,
			Confidence: r.Confidence,
			Kind:       metaOf(r, "kind"),
			File:       metaOf(r, "file"),
			Snippet:    metaOf(r, "snippet"),
			Source:     metaOf(r, "source"),
		}
	}
	return out
}

func metaOf(r retrieval.Result, key string) string {
	if r.Meta == nil {
		return ""
	}
	return r.Meta[key]
}
