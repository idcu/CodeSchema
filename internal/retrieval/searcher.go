package retrieval

import (
	"context"
	"fmt"
)

// VectorSearcher 向量搜索接口，适配各领域的向量索引器。
//
// Search 返回中性 Result 切片（ID + Score + Meta["source"]="vector"）。
type VectorSearcher interface {
	Search(ctx context.Context, query string, k int) ([]Result, error)
}

// Searcher 双路检索器。
//
// 整合 FTS 全文搜索与向量语义搜索，通过 Reranker 融合重排。
type Searcher struct {
	fts      FTSEngine
	vector   VectorSearcher
	reranker *Reranker
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

// Search 执行双路检索（MinScore=0，不启用低置信度过滤，向后兼容）。
func (s *Searcher) Search(ctx context.Context, query string, mode SearchMode, limit int) ([]Result, error) {
	results, _, err := s.searchWithThreshold(ctx, query, mode, limit, 0)
	return results, err
}

// SearchWithOptions 执行双路检索并支持低置信度过滤。
//
// MinScore>0 时过滤掉 Confidence < MinScore 的结果，返回被过滤条数 filtered；
// MinScore<=0 时不启用过滤。
func (s *Searcher) SearchWithOptions(ctx context.Context, query string, opts SearchOptions) ([]Result, int, error) {
	return s.searchWithThreshold(ctx, query, opts.Mode, opts.Limit, opts.MinScore)
}

// searchWithThreshold 统一检索 + 置信度赋值 + 低置信度过滤。
func (s *Searcher) searchWithThreshold(ctx context.Context, query string, mode SearchMode, limit int, minScore float64) ([]Result, int, error) {
	if query == "" {
		return nil, 0, fmt.Errorf("query must not be empty")
	}

	if limit <= 0 {
		limit = 20
	}

	var (
		results []Result
		err     error
	)

	switch mode {
	case SearchModeExact:
		results, err = s.fts.Search(ctx, query, FTSModeFuzzy, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("fts search: %w", err)
		}
		// 纯 FTS：Score 为 BM25 绝对相关度，映射为绝对置信度 [0,1)。
		results = withExactConfidence(results)

	case SearchModeSemantic:
		if s.vector == nil {
			return nil, 0, fmt.Errorf("vector search not available")
		}
		results, err = s.vector.Search(ctx, query, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("vector search: %w", err)
		}
		// 语义模式：Score 即余弦相似度（绝对 [0,1]），直接作为置信度。
		for i := range results {
			results[i].Confidence = results[i].Score
		}

	default: // SearchModeBoth
		ftsResults, err := s.fts.Search(ctx, query, FTSModeFuzzy, limit*2)
		if err != nil {
			return nil, 0, fmt.Errorf("fts search: %w", err)
		}

		var vectorResults []Result
		if s.vector != nil {
			vecResults, verr := s.vector.Search(ctx, query, limit*2)
			if verr == nil {
				vectorResults = vecResults
			}
		}

		// 融合重排（Rerank 已为每条结果计算绝对 Confidence）
		results = s.reranker.Rerank(ftsResults, vectorResults, limit)
	}

	// 低置信度过滤（空结果优于误导结果）
	filtered := 0
	if minScore > 0 {
		kept := results[:0]
		for _, r := range results {
			if r.Confidence >= minScore {
				kept = append(kept, r)
			} else {
				filtered++
			}
		}
		results = kept
	}

	return results, filtered, nil
}
