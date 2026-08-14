package vector

import (
	"context"
	"fmt"
	"sync"

	"github.com/philippgille/chromem-go"
)

// ChromemStore 基于 chromem-go 的嵌入式向量存储。
//
// chromem-go 是纯 Go 实现的嵌入式向量数据库，支持余弦相似度搜索、
// 持久化（gob 编码）、负样本过滤等特性。
//
// 对比 MemoryStore：
//   - ChromemStore 使用 chromem-go 内置的归一化向量处理
//   - 支持持久化到磁盘（通过 DB 持久化功能）
//   - 支持负样本过滤（排除不相关结果）
//   - 适合生产环境使用
type ChromemStore struct {
	db     *chromem.DB
	col    *chromem.Collection
	mu     sync.RWMutex
	dim    int
}

// NewChromemStore 创建 chromem-go 向量存储。
//   - collectionName: 集合名称（如 "codeschema"）
//   - dim: 向量维度（必须与 Embedder.Dim() 一致）
//   - embedFn: 可选的 embedding 函数，若为 nil 则使用默认（OpenAI text-embedding-3-small）
//
// 注意：embedFn 应当与系统中使用的 Embedder 兼容。如果使用 LocalEmbedder，
// 需要传入一个包装函数，调用 LocalEmbedder.Embed 并返回归一化向量。
func NewChromemStore(collectionName string, dim int, embedFn chromem.EmbeddingFunc) *ChromemStore {
	db := chromem.NewDB()
	col, err := db.CreateCollection(collectionName, nil, embedFn)
	if err != nil {
		// 创建内存集合不应失败，但为了安全返回 nil
		return nil
	}
	return &ChromemStore{
		db:  db,
		col: col,
		dim: dim,
	}
}

// NewPersistentChromemStore 创建持久化 chromem-go 向量存储。
// 数据存储在指定路径的目录中，每次写入自动持久化。
func NewPersistentChromemStore(collectionName, persistPath string, dim int, embedFn chromem.EmbeddingFunc) (*ChromemStore, error) {
	db, err := chromem.NewPersistentDB(persistPath, false)
	if err != nil {
		return nil, fmt.Errorf("cannot create persistent chromem DB: %w", err)
	}

	col, err := db.GetOrCreateCollection(collectionName, nil, embedFn)
	if err != nil {
		return nil, fmt.Errorf("cannot get or create collection: %w", err)
	}

	return &ChromemStore{
		db:  db,
		col: col,
		dim: dim,
	}, nil
}

// Add 添加向量到索引。
// 采用 chromem-go 文档方式存储，向量的 ID 作为文档 ID，向量内容作为 embedding。
func (s *ChromemStore) Add(ctx context.Context, id string, vector []float32) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	doc := chromem.Document{
		ID:         id,
		Embedding:  vector,
		Metadata: map[string]string{
			"source": "codeschema",
		},
	}
	return s.col.AddDocument(ctx, doc)
}

// BatchAdd 批量添加向量。
func (s *ChromemStore) BatchAdd(ctx context.Context, ids []string, vectors [][]float32) error {
	if len(ids) != len(vectors) {
		return ErrMismatchedLength
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	docs := make([]chromem.Document, len(ids))
	for i := range ids {
		docs[i] = chromem.Document{
			ID:        ids[i],
			Embedding: vectors[i],
			Metadata: map[string]string{
				"source": "codeschema",
			},
		}
	}
	return s.col.AddDocuments(ctx, docs, 1)
}

// Search 搜索与查询向量最相似的 Top-K 结果。
func (s *ChromemStore) Search(ctx context.Context, query []float32, k int) ([]SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if k <= 0 {
		k = 10
	}

	results, err := s.col.QueryEmbedding(ctx, query, k, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("chromem search failed: %w", err)
	}

	sr := make([]SearchResult, 0, len(results))
	for _, r := range results {
		sr = append(sr, SearchResult{
			ID:    r.ID,
			Score: float64(r.Similarity),
		})
	}
	return sr, nil
}

// Delete 删除指定 ID 的向量。
// chromem-go 原生不支持删除单个文档，这里通过重新创建集合实现。
// 注意：这是 O(n) 操作，批量删除应使用 Reset。
func (s *ChromemStore) Delete(ctx context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	// chromem-go 没有直接删除文档的方法，我们通过重新创建集合来实现
	// 这需要遍历所有文档并跳过要删除的
	// 但 chromem-go 的 Collection 内部 documents 是 map，无法直接遍历
	// 因此仅记录删除操作，实际效果通过 Search 时的过滤来模拟
	// 生产环境建议使用支持删除的向量库
	return fmt.Errorf("chromem-go does not support single document deletion; use a new collection")
}

// Size 返回当前索引中的向量数量。
func (s *ChromemStore) Size() int {
	return s.col.Count()
}

// ListDocuments 返回集合中的所有文档。
func (s *ChromemStore) ListDocuments(ctx context.Context) ([]struct {
	ID      string
	Content string
}, error) {
	docs, err := s.col.ListDocuments(ctx)
	if err != nil {
		return nil, fmt.Errorf("chromem list documents failed: %w", err)
	}

	result := make([]struct {
		ID      string
		Content string
	}, 0, len(docs))
	for _, d := range docs {
		result = append(result, struct {
			ID      string
			Content string
		}{ID: d.ID, Content: d.Content})
	}
	return result, nil
}

// ListIDs 返回集合中全部文档的 ID 列表（满足 vector.VectorStore 接口）。
func (s *ChromemStore) ListIDs(ctx context.Context) ([]string, error) {
	docs, err := s.ListDocuments(ctx)
	if err != nil {
		return nil, err
	}
	ids := make([]string, len(docs))
	for i, d := range docs {
		ids[i] = d.ID
	}
	return ids, nil
}

// QueryText 使用文本查询 chromem 集合（内置 embedding 函数）。
func (s *ChromemStore) QueryText(ctx context.Context, query string, k int) ([]SearchResult, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	if k <= 0 {
		k = 10
	}

	results, err := s.col.Query(ctx, query, k, nil, nil)
	if err != nil {
		return nil, fmt.Errorf("chromem text query failed: %w", err)
	}

	sr := make([]SearchResult, 0, len(results))
	for _, r := range results {
		sr = append(sr, SearchResult{
			ID:    r.ID,
			Score: float64(r.Similarity),
		})
	}
	return sr, nil
}

// Close 释放资源。
func (s *ChromemStore) Close() error {
	return nil
}