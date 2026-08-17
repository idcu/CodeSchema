package search

import (
	"context"
	"fmt"
)

// SearchMode 搜索模式。
type SearchMode string

const (
	SearchModeExact   SearchMode = "exact"   // 仅 FTS 精确搜索
	SearchModeSemantic SearchMode = "semantic" // 仅向量语义搜索
	SearchModeBoth    SearchMode = "both"    // 双路融合检索（默认）
)

// Searcher 双路检索器。
//
// 整合 FTS5 全文搜索与向量语义搜索，通过 Reranker 融合重排。
type Searcher struct {
	fts      FTSEngine
	vector   VectorSearcher
	reranker *Reranker
}

// VectorSearcher 向量搜索接口，适配 vector 包。
type VectorSearcher interface {
	Search(ctx context.Context, query string, k int) ([]SearchResult, error)
}

// NewSearcher 创建双路检索器。
func NewSearcher(fts FTSEngine, vector VectorSearcher, reranker *Reranker) *Searcher {
	if reranker == nil {
		reranker = DefaultReranker()
	}
	return &Searcher{
		fts:      fts,
		vector:   vector,
		reranker: reranker,
	}
}

// Search 执行双路检索。
//
// mode 参数：
//   - "exact": 仅 FTS 精确搜索
//   - "semantic": 仅向量语义搜索
//   - "both"（默认）: FTS + 向量融合检索
//
// limit 控制返回结果数上限，传 0 使用默认值 20。
func (s *Searcher) Search(ctx context.Context, query string, mode SearchMode, limit int) ([]SearchResult, error) {
	if query == "" {
		return nil, fmt.Errorf("query must not be empty")
	}

	if limit <= 0 {
		limit = 20
	}

	switch mode {
	case SearchModeExact:
		return s.fts.Search(ctx, query, FTSModeFuzzy, limit)

	case SearchModeSemantic:
		if s.vector == nil {
			return nil, fmt.Errorf("vector search not available")
		}
		return s.vector.Search(ctx, query, limit)

	default: // SearchModeBoth
		ftsResults, err := s.fts.Search(ctx, query, FTSModeFuzzy, limit*2)
		if err != nil {
			return nil, fmt.Errorf("fts search: %w", err)
		}

		var vectorResults []SearchResult
		if s.vector != nil {
			vecResults, err := s.vector.Search(ctx, query, limit*2)
			if err == nil {
				vectorResults = vecResults
			}
		}

		// 融合重排
		return s.reranker.Rerank(ftsResults, vectorResults, limit), nil
	}
}

