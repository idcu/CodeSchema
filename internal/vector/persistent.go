package vector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"

	"gitee.com/idcu-go/pathsafe"
)

// PersistentStore 磁盘持久化的向量存储。
//
// 在 MemoryStore 的基础上增加了 Save/Load 机制，使用 JSON 序列化。
// 每次 Add/BatchAdd/Delete 操作后自动保存到磁盘文件。
// 适用于开发和小规模生产场景，无需外部数据库依赖。
type PersistentStore struct {
	mu       sync.RWMutex
	vecs     map[string][]float32
	contents map[string]string // id → 原文（DocContentStore 可选能力，持久化）
	filePath string
	dirties  int // 自上次保存以来的变更计数
}

// persistentData 序列化结构（向后兼容：旧文件仅含 vectors，contents 缺失时初始化为空 map）。
type persistentData struct {
	Vectors  map[string][]float32 `json:"vectors"`
	Contents map[string]string    `json:"contents,omitempty"`
}

// NewPersistentStore 创建持久化向量存储。
//
// filePath 为存储文件路径（如 "./data/vector.json"）。
// 如果文件已存在，自动加载历史数据。
func NewPersistentStore(filePath string) (*PersistentStore, error) {
	ps := &PersistentStore{
		vecs:     make(map[string][]float32),
		contents: make(map[string]string),
		filePath: filePath,
	}

	// 尝试加载已有数据
	if err := ps.load(); err != nil {
		return nil, fmt.Errorf("persistent: load %s: %w", filePath, err)
	}

	return ps, nil
}

// Add 添加向量。
func (ps *PersistentStore) Add(ctx context.Context, id string, vector []float32) error {
	ps.mu.Lock()
	ps.vecs[id] = vector
	ps.dirties++
	ps.mu.Unlock()
	return ps.maybeSave()
}

// BatchAdd 批量添加向量。
func (ps *PersistentStore) BatchAdd(ctx context.Context, ids []string, vectors [][]float32) error {
	if len(ids) != len(vectors) {
		return ErrMismatchedLength
	}
	ps.mu.Lock()
	for i := range ids {
		ps.vecs[ids[i]] = vectors[i]
	}
	ps.dirties += len(ids)
	ps.mu.Unlock()
	return ps.maybeSave()
}

// Search 余弦相似度搜索 Top-K。
func (ps *PersistentStore) Search(_ context.Context, query []float32, k int) ([]SearchResult, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()

	if len(ps.vecs) == 0 {
		return nil, nil
	}

	results := make([]SearchResult, 0, len(ps.vecs))
	for id, vec := range ps.vecs {
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
func (ps *PersistentStore) Delete(ctx context.Context, id string) error {
	ps.mu.Lock()
	delete(ps.vecs, id)
	ps.dirties++
	ps.mu.Unlock()
	return ps.maybeSave()
}

// Size 返回向量数量。
func (ps *PersistentStore) Size() int {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	return len(ps.vecs)
}

// Close 关闭存储，确保数据落盘。
func (ps *PersistentStore) Close() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.save()
}

// ListIDs 返回当前索引中所有向量的 ID 列表。
func (ps *PersistentStore) ListIDs(_ context.Context) ([]string, error) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	ids := make([]string, 0, len(ps.vecs))
	for id := range ps.vecs {
		ids = append(ids, id)
	}
	return ids, nil
}

// SetContent 保存文档原文（DocContentStore 实现）。
func (ps *PersistentStore) SetContent(_ context.Context, id, content string) error {
	ps.mu.Lock()
	ps.contents[id] = content
	ps.dirties++
	ps.mu.Unlock()
	return ps.maybeSave()
}

// Content 读取文档原文（DocContentStore 实现）。
func (ps *PersistentStore) Content(_ context.Context, id string) (string, bool) {
	ps.mu.RLock()
	defer ps.mu.RUnlock()
	c, ok := ps.contents[id]
	return c, ok
}

// Save 强制保存数据到磁盘。
func (ps *PersistentStore) Save() error {
	ps.mu.Lock()
	defer ps.mu.Unlock()
	return ps.save()
}

// save 执行实际序列化写入（调用方需持有锁）。
func (ps *PersistentStore) save() error {
	if ps.filePath == "" {
		return nil
	}
	if err := pathsafe.MkdirAll(filepath.Dir(ps.filePath)); err != nil {
		return fmt.Errorf("persistent: mkdir: %w", err)
	}
	data := persistentData{Vectors: ps.vecs, Contents: ps.contents}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("persistent: marshal: %w", err)
	}
	if err := pathsafe.WriteFile(ps.filePath, raw); err != nil {
		return fmt.Errorf("persistent: write: %w", err)
	}
	ps.dirties = 0
	return nil
}

// load 从磁盘加载数据（初始化时调用）。
func (ps *PersistentStore) load() error {
	if ps.filePath == "" {
		return nil
	}
	raw, err := os.ReadFile(ps.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("persistent: read: %w", err)
	}
	var data persistentData
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("persistent: unmarshal: %w", err)
	}
	if data.Vectors != nil {
		ps.vecs = data.Vectors
	}
	if data.Contents != nil {
		ps.contents = data.Contents
	}
	return nil
}

// maybeSave 当变更累积到阈值时自动保存。
func (ps *PersistentStore) maybeSave() error {
	ps.mu.RLock()
	dirties := ps.dirties
	ps.mu.RUnlock()
	if dirties >= 10 { // 每 10 次变更自动保存
		return ps.Save()
	}
	return nil
}