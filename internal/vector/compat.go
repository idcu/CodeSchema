package vector

import (
	"github.com/idcu/codeschema/internal/embedding"
)

// 本文件为兼容层：将通用向量机制下沉到 internal/embedding，
// 通过类型别名与薄封装函数保持既有调用方零改动。
// 领域相关后端（chromem / ONNX / 模型分发）仍保留在 vector 包。

// Embedder 向量模型接口（别名到 embedding.Embedder）。
type Embedder = embedding.Embedder

// TextEmbeddable 可被 embedding 的实体接口（别名到 embedding.Embeddable）。
//
// 领域侧用 DefaultText 拼接代码实体文本，见 model.go。
type TextEmbeddable = embedding.Embeddable

// SearchResult 向量检索结果（别名到 embedding.SearchResult）。
type SearchResult = embedding.SearchResult

// VectorStore 向量库存储接口（别名到 embedding.VectorStore）。
type VectorStore = embedding.VectorStore

// DocContentStore 可选原文存储接口（别名到 embedding.DocContentStore）。
type DocContentStore = embedding.DocContentStore

// MemoryStore 纯内存向量存储（别名到 embedding.MemoryStore）。
type MemoryStore = embedding.MemoryStore

// PersistentStore 持久化向量存储（别名到 embedding.PersistentStore）。
type PersistentStore = embedding.PersistentStore

// Indexer 向量索引构建器（别名到 embedding.Indexer）。
type Indexer = embedding.Indexer

// LocalEmbedder 本地统计 Embedder（别名到 embedding.LocalEmbedder）。
type LocalEmbedder = embedding.LocalEmbedder

// MockEmbedder 确定性 mock Embedder（别名到 embedding.MockEmbedder）。
type MockEmbedder = embedding.MockEmbedder

// StoreError 存储错误类型（别名到 embedding.StoreError）。
type StoreError = embedding.StoreError

// ErrMismatchedLength 批量长度不匹配（重新指向 embedding.ErrMismatchedLength）。
var ErrMismatchedLength = embedding.ErrMismatchedLength

// NewMockEmbedder 创建 mock embedding 模型。
func NewMockEmbedder(dim int) *MockEmbedder { return embedding.NewMockEmbedder(dim) }

// NewMemoryStore 创建内存向量存储。
func NewMemoryStore() *MemoryStore { return embedding.NewMemoryStore() }

// NewPersistentStore 创建持久化向量存储。
func NewPersistentStore(filePath string) (*PersistentStore, error) {
	return embedding.NewPersistentStore(filePath)
}

// NewIndexer 创建向量索引器。
func NewIndexer(store VectorStore, model Embedder, workers int) *Indexer {
	return embedding.NewIndexer(store, model, workers)
}

// NewLocalEmbedder 创建本地统计 Embedder。
func NewLocalEmbedder(dim int) *LocalEmbedder { return embedding.NewLocalEmbedder(dim) }
