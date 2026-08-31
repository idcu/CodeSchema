package search

import (
	"context"

	"github.com/idcu/codeschema/internal/retrieval"
)

// 本文件为兼容层：将通用双路检索器下沉到 internal/retrieval，
// 通过投影函数与适配层保持既有调用方零改动。

// SearchMode 双路检索模式（别名到 retrieval.SearchMode）。
type SearchMode = retrieval.SearchMode

const (
	SearchModeExact    = retrieval.SearchModeExact
	SearchModeSemantic = retrieval.SearchModeSemantic
	SearchModeBoth     = retrieval.SearchModeBoth
)

// SearchOptions 检索选项（别名到 retrieval.SearchOptions）。
type SearchOptions = retrieval.SearchOptions

// VectorSearcher 向量搜索接口，适配各领域的向量索引器。
//
// 返回领域结果 SearchResult（Source="vector"）。
type VectorSearcher interface {
	Search(ctx context.Context, query string, k int) ([]SearchResult, error)
}

// Searcher 双路检索器（领域适配层）。
//
// 内部委托 retrieval.Searcher 执行中性检索，再把结果投影回 SearchResult。
type Searcher struct {
	inner *retrieval.Searcher
}

// vsAdapter 将领域 VectorSearcher 适配为检索包中性 VectorSearcher。
type vsAdapter struct {
	v VectorSearcher
}

func (a *vsAdapter) Search(ctx context.Context, query string, k int) ([]retrieval.Result, error) {
	rs, err := a.v.Search(ctx, query, k)
	if err != nil {
		return nil, err
	}
	return projectTo(rs), nil
}

// NewSearcher 创建双路检索器。
func NewSearcher(fts FTSEngine, vector VectorSearcher, reranker *Reranker) *Searcher {
	var innerR *retrieval.Reranker
	if reranker != nil {
		innerR = reranker.inner
	}
	var vs retrieval.VectorSearcher
	if vector != nil {
		vs = &vsAdapter{v: vector}
	}
	return &Searcher{
		inner: retrieval.NewSearcher(fts, vs, innerR),
	}
}

// Search 执行双路检索（MinScore=0，不启用低置信度过滤，向后兼容）。
func (s *Searcher) Search(ctx context.Context, query string, mode SearchMode, limit int) ([]SearchResult, error) {
	out, err := s.inner.Search(ctx, query, retrieval.SearchMode(mode), limit)
	if err != nil {
		return nil, err
	}
	return projectFrom(out), nil
}

// SearchWithOptions 执行双路检索并支持低置信度过滤。
//
// MinScore>0 时过滤掉 Confidence < MinScore 的结果，返回被过滤条数 filtered；
// MinScore<=0 时不启用过滤。
func (s *Searcher) SearchWithOptions(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, int, error) {
	out, filtered, err := s.inner.SearchWithOptions(ctx, query, retrieval.SearchOptions{
		Mode:     retrieval.SearchMode(opts.Mode),
		Limit:    opts.Limit,
		MinScore: opts.MinScore,
	})
	if err != nil {
		return nil, 0, err
	}
	return projectFrom(out), filtered, nil
}
