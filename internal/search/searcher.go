package search

import (
	"context"
	"fmt"
	"math"
)

// SearchMode 搜索模式。
type SearchMode string

const (
	SearchModeExact    SearchMode = "exact"    // 仅 FTS 精确搜索
	SearchModeSemantic SearchMode = "semantic" // 仅向量语义搜索
	SearchModeBoth     SearchMode = "both"     // 双路融合检索（默认）
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

// SearchOptions 检索选项。
//
// MinScore 为绝对置信度阈值（[0,1]），B8「空结果优于误导结果」使用：
// 低于阈值的结果被过滤。MinScore<=0 表示不启用过滤（向后兼容）。
type SearchOptions struct {
	Mode     SearchMode
	Limit    int
	MinScore float64
}

// Search 执行双路检索（MinScore=0，不启用低置信度过滤，向后兼容）。
func (s *Searcher) Search(ctx context.Context, query string, mode SearchMode, limit int) ([]SearchResult, error) {
	results, _, err := s.searchWithThreshold(ctx, query, mode, limit, 0)
	return results, err
}

// SearchWithOptions 执行双路检索并支持低置信度过滤（B8）。
//
// MinScore>0 时过滤掉 Confidence < MinScore 的结果，返回被过滤条数 filtered；
// MinScore<=0 时不启用过滤。
func (s *Searcher) SearchWithOptions(ctx context.Context, query string, opts SearchOptions) ([]SearchResult, int, error) {
	return s.searchWithThreshold(ctx, query, opts.Mode, opts.Limit, opts.MinScore)
}

// searchWithThreshold 统一检索 + 置信度赋值 + 低置信度过滤。
func (s *Searcher) searchWithThreshold(ctx context.Context, query string, mode SearchMode, limit int, minScore float64) ([]SearchResult, int, error) {
	if query == "" {
		return nil, 0, fmt.Errorf("query must not be empty")
	}

	if limit <= 0 {
		limit = 20
	}

	var (
		results []SearchResult
		err     error
	)

	switch mode {
	case SearchModeExact:
		results, err = s.fts.Search(ctx, query, FTSModeFuzzy, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("fts search: %w", err)
		}
		// 纯 FTS：Score 为 BM25 绝对相关度，映射为绝对置信度 [0,1)（B8 待决项①）。
		results = withExactConfidence(results)

	case SearchModeSemantic:
		if s.vector == nil {
			return nil, 0, fmt.Errorf("vector search not available")
		}
		results, err = s.vector.Search(ctx, query, limit)
		if err != nil {
			return nil, 0, fmt.Errorf("vector search: %w", err)
		}
		// 语义模式：Score 即 chromem 余弦相似度（绝对 [0,1]），直接作为置信度。
		for i := range results {
			results[i].Confidence = results[i].Score
		}

	default: // SearchModeBoth
		ftsResults, err := s.fts.Search(ctx, query, FTSModeFuzzy, limit*2)
		if err != nil {
			return nil, 0, fmt.Errorf("fts search: %w", err)
		}

		var vectorResults []SearchResult
		if s.vector != nil {
			vecResults, verr := s.vector.Search(ctx, query, limit*2)
			if verr == nil {
				vectorResults = vecResults
			}
		}

		// 融合重排（Rerank 已为每条结果计算绝对 Confidence）
		results = s.reranker.Rerank(ftsResults, vectorResults, limit)
	}

	// B8：低置信度过滤（空结果优于误导结果）
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

// exactConfidenceTau 绝对置信度标定常数：纯 FTS 模式 Confidence = 1 - exp(-BM25/tau)，
// 与结果集大小无关（B8 待决项①落地）。BM25 已含 IDF + 文档长度归一，属绝对相关度量纲。
const exactConfidenceTau = 0.3

// withExactConfidence 将纯 FTS（BM25）结果映射为绝对置信度 [0,1)（B8 待决项①）。
//
// 与旧实现（按本结果集最大值归一化的相对尺度）不同，此处为绝对映射：
// 同一文档+查询始终得到同一置信度，使 MinScore 可作绝对阈值而不受结果集强弱影响。
func withExactConfidence(results []SearchResult) []SearchResult {
	for i := range results {
		results[i].Confidence = 1 - math.Exp(-results[i].Score/exactConfidenceTau)
	}
	return results
}
