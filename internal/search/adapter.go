package search

import (
	"context"

	"codeschema/internal/vector"
)

// VectorAdapter 适配 vector.Indexer 实现 search.VectorSearcher 接口。
//
// 将 vector 包的 SearchResult 转换为 search 包的 SearchResult，
// 避免两个包之间的循环依赖，同时保持接口一致性。
type VectorAdapter struct {
	indexer *vector.Indexer
}

// NewVectorAdapter 创建向量搜索适配器。
func NewVectorAdapter(idx *vector.Indexer) *VectorAdapter {
	return &VectorAdapter{indexer: idx}
}

// Search 对查询文本执行语义搜索，返回 search.SearchResult 切片。
func (a *VectorAdapter) Search(ctx context.Context, query string, k int) ([]SearchResult, error) {
	results, err := a.indexer.Search(ctx, query, k)
	if err != nil {
		return nil, err
	}

	converted := make([]SearchResult, 0, len(results))
	for _, r := range results {
		converted = append(converted, SearchResult{
			Symbol: r.ID,
			Score:  r.Score,
			Source: "vector",
		})
	}
	return converted, nil
}