package search

import (
	"github.com/idcu/codeschema/internal/retrieval"
)

// Reranker 融合重排器（领域适配层）。
//
// 通用融合算法在 internal/retrieval，本层把领域结果 SearchResult 投影为
// 中性 retrieval.Result 后委托执行，再投影回 SearchResult，保持对外签名不变。
type Reranker struct {
	FTSWeight    float64
	VectorWeight float64
	inner        *retrieval.Reranker
}

// DefaultReranker 返回默认权重的重排器。
func DefaultReranker() *Reranker {
	r := retrieval.DefaultReranker()
	return &Reranker{FTSWeight: r.FTSWeight, VectorWeight: r.VectorWeight, inner: r}
}

// NewReranker 创建自定义权重重排器。
func NewReranker(ftsWeight, vectorWeight float64) *Reranker {
	return &Reranker{
		FTSWeight:    ftsWeight,
		VectorWeight: vectorWeight,
		inner:        retrieval.NewReranker(ftsWeight, vectorWeight),
	}
}

// Rerank 融合 FTS 和向量检索结果，按融合得分降序排列。
//
// ftsResults: 全文搜索结果
// vectorResults: 向量语义搜索结果
// limit: 返回结果数上限（0 表示不限制）
func (r *Reranker) Rerank(ftsResults, vectorResults []SearchResult, limit int) []SearchResult {
	out := r.inner.Rerank(projectTo(ftsResults), projectTo(vectorResults), limit)
	return projectFrom(out)
}
