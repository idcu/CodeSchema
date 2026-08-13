package vector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"sync"
)

// PersistentStore 磁盘持久化的向量存储。
//
// 在 MemoryStore 的基础上增加了 Save/Load 机制，使用 JSON 序列化。
// 每次 Add/BatchAdd/Delete 操作后自动保存到磁盘文件。
// 适用于开发和小规模生产场景，无需外部数据库依赖。
type PersistentStore struct {
	mu       sync.RWMutex
	vecs     map[string][]float32
	filePath string
	dirties  int // 自上次保存以来的变更计数
}

// persistentData 序列化结构。
type persistentData struct {
	Vectors map[string][]float32 `json:"vectors"`
}

// NewPersistentStore 创建持久化向量存储。
//
// filePath 为存储文件路径（如 "./data/vector.json"）。
// 如果文件已存在，自动加载历史数据。
func NewPersistentStore(filePath string) (*PersistentStore, error) {
	ps := &PersistentStore{
		vecs:     make(map[string][]float32),
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
	if err := os.MkdirAll(filepath.Dir(ps.filePath), 0755); err != nil {
		return fmt.Errorf("persistent: mkdir: %w", err)
	}
	data := persistentData{Vectors: ps.vecs}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("persistent: marshal: %w", err)
	}
	if err := os.WriteFile(ps.filePath, raw, 0644); err != nil {
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