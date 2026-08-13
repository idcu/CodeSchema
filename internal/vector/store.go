package vector

import (
	"context"
	"math"
	"sort"
	"sync"
)

// SearchResult 向量检索结果。
type SearchResult struct {
	ID     string  `json:"id"`
	Score  float64 `json:"score"`
	Vector []float32 `json:"-"`
}

// VectorStore 向量库存储接口。
//
// 支持多种实现：
//   - MemoryStore: 纯内存实现（P0 测试用）
//   - ChromemStore: chromem-go 嵌入式实现（P2 生产路径）
//   - MilvusStore: Milvus 集群（P3 扩展）
type VectorStore interface {
	// Add 添加向量到索引。
	Add(ctx context.Context, id string, vector []float32) error

	// BatchAdd 批量添加向量。
	BatchAdd(ctx context.Context, ids []string, vectors [][]float32) error

	// Search 搜索与查询向量最相似的 Top-K 结果。
	Search(ctx context.Context, vector []float32, k int) ([]SearchResult, error)

	// Delete 删除指定 ID 的向量。
	Delete(ctx context.Context, id string) error

	// Size 返回当前索引中的向量数量。
	Size() int

	// Close 释放资源。
	Close() error
}

// MemoryStore 纯内存向量存储，用于测试和开发。
//
// 使用余弦相似度计算向量相似性。
// 不依赖任何外部库，通过 math 包手写余弦相似度。
type MemoryStore struct {
	mu   sync.RWMutex
	vecs map[string][]float32
}

// NewMemoryStore 创建内存向量存储。
func NewMemoryStore() *MemoryStore {
	return &MemoryStore{
		vecs: make(map[string][]float32),
	}
}

// Add 添加向量。
func (m *MemoryStore) Add(_ context.Context, id string, vector []float32) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.vecs[id] = vector
	return nil
}

// BatchAdd 批量添加向量。
func (m *MemoryStore) BatchAdd(_ context.Context, ids []string, vectors [][]float32) error {
	if len(ids) != len(vectors) {
		return ErrMismatchedLength
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range ids {
		m.vecs[ids[i]] = vectors[i]
	}
	return nil
}

// Search 余弦相似度搜索 Top-K。
func (m *MemoryStore) Search(_ context.Context, query []float32, k int) ([]SearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.vecs) == 0 {
		return nil, nil
	}

	results := make([]SearchResult, 0, len(m.vecs))
	for id, vec := range m.vecs {
		score := cosineSimilarity(query, vec)
		results = append(results, SearchResult{
			ID:    id,
			Score: score,
		})
	}

	// 按得分降序排列
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if k > 0 && k < len(results) {
		results = results[:k]
	}

	return results, nil
}

// Delete 删除向量。
func (m *MemoryStore) Delete(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.vecs, id)
	return nil
}

// Size 返回向量数量。
func (m *MemoryStore) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.vecs)
}

// Close 释放资源（MemoryStore 无操作）。
func (m *MemoryStore) Close() error {
	return nil
}

// cosineSimilarity 计算两个向量的余弦相似度。
// 返回 [-1, 1] 之间的值，1 表示完全相似。
func cosineSimilarity(a, b []float32) float64 {
	if len(a) != len(b) {
		return 0
	}

	var dotProduct, normA, normB float64
	for i := range a {
		fa := float64(a[i])
		fb := float64(b[i])
		dotProduct += fa * fb
		normA += fa * fa
		normB += fb * fb
	}

	if normA == 0 || normB == 0 {
		return 0
	}

	return dotProduct / (math.Sqrt(normA) * math.Sqrt(normB))
}

// ErrMismatchedLength 批量操作时 IDs 和 Vectors 长度不匹配。
var ErrMismatchedLength = &StoreError{"ids and vectors length mismatch"}

// StoreError 存储错误类型。
type StoreError struct {
	Message string
}

func (e *StoreError) Error() string { return e.Message }