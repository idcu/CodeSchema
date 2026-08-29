package search

import (
	"math"
	"sort"
)

// Reranker 融合重排器。
//
// 将 FTS5 精确匹配结果与向量语义检索结果做融合重排。
// 融合策略：归一化得分 → 加权融合 → 去重 → 降序排列。
type Reranker struct {
	FTSWeight    float64 // FTS 得分权重（默认 0.3）
	VectorWeight float64 // 向量得分权重（默认 0.7）
}

// DefaultReranker 返回默认权重的重排器。
func DefaultReranker() *Reranker {
	return &Reranker{
		FTSWeight:    0.3,
		VectorWeight: 0.7,
	}
}

// NewReranker 创建自定义权重重排器。
func NewReranker(ftsWeight, vectorWeight float64) *Reranker {
	return &Reranker{
		FTSWeight:    ftsWeight,
		VectorWeight: vectorWeight,
	}
}

// Rerank 融合 FTS 和向量检索结果，按融合得分降序排列。
//
// ftsResults: 全文搜索结果
// vectorResults: 向量语义搜索结果
// limit: 返回结果数上限（0 表示不限制）
func (r *Reranker) Rerank(ftsResults, vectorResults []SearchResult, limit int) []SearchResult {
	// 1. 归一化 FTS 得分
	maxFTS := maxScore(ftsResults)
	normalizedFTS := normalize(ftsResults, maxFTS)

	// 2. 归一化向量得分
	maxVec := maxScore(vectorResults)
	normalizedVec := normalize(vectorResults, maxVec)

	// 2.1 记录向量原始余弦相似度（绝对量纲 [0,1]），用于 B8 绝对置信度阈值
	vectorRaw := make(map[string]float64, len(vectorResults))
	for _, r := range vectorResults {
		vectorRaw[r.Symbol] = r.Score
	}

	// 3. 建立 ID → 融合得分的映射
	merged := make(map[string]*mergedResult)

	for _, r := range normalizedFTS {
		merged[r.Symbol] = &mergedResult{
			Symbol:   r.Symbol,
			Kind:     r.Kind,
			File:     r.File,
			FTSScore: r.Score,
			Snippet:  r.Snippet,
		}
	}

	for _, r := range normalizedVec {
		if mr, ok := merged[r.Symbol]; ok {
			mr.VectorScore = r.Score
			if r.Kind != "" {
				mr.Kind = r.Kind
			}
			if r.File != "" {
				mr.File = r.File
			}
		} else {
			merged[r.Symbol] = &mergedResult{
				Symbol:      r.Symbol,
				Kind:        r.Kind,
				File:        r.File,
				VectorScore: r.Score,
				Snippet:     r.Snippet,
			}
		}
	}

	// 4. 计算融合得分与绝对置信度
	results := make([]SearchResult, 0, len(merged))
	for _, mr := range merged {
		fused := r.FTSWeight*mr.FTSScore + r.VectorWeight*mr.VectorScore
		// 置信度口径（B8）：优先使用向量原始余弦相似度（绝对 [0,1]）；
		// 仅 FTS 命中的结果回退到归一化 FTS 得分（相对 [0,1]）。
		conf := mr.FTSScore
		if v, ok := vectorRaw[mr.Symbol]; ok {
			conf = v
		}
		results = append(results, SearchResult{
			Symbol:     mr.Symbol,
			Kind:       mr.Kind,
			File:       mr.File,
			Score:      fused,
			Snippet:    mr.Snippet,
			Source:     "fused",
			Confidence: conf,
		})
	}

	// 5. 按融合得分降序排列
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	// 6. 截取上限
	if limit > 0 && limit < len(results) {
		results = results[:limit]
	}

	return results
}

type mergedResult struct {
	Symbol      string
	Kind        string
	File        string
	FTSScore    float64
	VectorScore float64
	Snippet     string
}

func maxScore(results []SearchResult) float64 {
	max := 0.0
	for _, r := range results {
		if r.Score > max {
			max = r.Score
		}
	}
	return max
}

func normalize(results []SearchResult, max float64) []SearchResult {
	if max == 0 {
		return results
	}
	normalized := make([]SearchResult, len(results))
	for i, r := range results {
		normalized[i] = r
		normalized[i].Score = r.Score / max
		// 避免除零和负值
		if math.IsNaN(normalized[i].Score) || normalized[i].Score < 0 {
			normalized[i].Score = 0
		}
	}
	return normalized
}