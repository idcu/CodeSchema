package search

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/idcu/codeschema/internal/fsperm"
)

// PersistentFTS 磁盘持久化的全文搜索引擎。
//
// 在 MemoryFTS 的基础上增加 Save/Load 机制，使用 JSON 序列化。
// 每次 Index/BatchIndex/Remove 后自动保存到磁盘文件。
// 适用于开发和小规模生产场景，无需 SQLite 依赖。
type PersistentFTS struct {
	mu       sync.RWMutex
	docs     map[string]*DocEntry
	filePath string
	dirties  int
}

// persistentFTSData 序列化结构。
type persistentFTSData struct {
	Docs map[string]*docEntryJSON `json:"docs"`
}

type docEntryJSON struct {
	ID      string   `json:"id"`
	Content string   `json:"content"`
	Tokens  []string `json:"tokens"`
}

// NewPersistentFTS 创建持久化全文搜索引擎。
//
// filePath 为存储文件路径（如 "./data/fts.json"）。
// 如果文件已存在，自动加载历史数据。
func NewPersistentFTS(filePath string) (*PersistentFTS, error) {
	pf := &PersistentFTS{
		docs:     make(map[string]*DocEntry),
		filePath: filePath,
	}
	if err := pf.load(); err != nil {
		return nil, fmt.Errorf("persistent fts: load %s: %w", filePath, err)
	}
	return pf, nil
}

// Index 索引文档。
func (pf *PersistentFTS) Index(ctx context.Context, id, content string) error {
	pf.mu.Lock()
	pf.docs[id] = &DocEntry{
		ID:      id,
		Content: content,
		Tokens:  tokenize(content),
	}
	pf.dirties++
	pf.mu.Unlock()
	return pf.maybeSave()
}

// BatchIndex 批量索引文档。
func (pf *PersistentFTS) BatchIndex(ctx context.Context, ids, contents []string) error {
	if len(ids) != len(contents) {
		return ErrMismatchedLength
	}
	pf.mu.Lock()
	for i := range ids {
		pf.docs[ids[i]] = &DocEntry{
			ID:      ids[i],
			Content: contents[i],
			Tokens:  tokenize(contents[i]),
		}
	}
	pf.dirties += len(ids)
	pf.mu.Unlock()
	return pf.maybeSave()
}

// Search 执行全文搜索。
func (pf *PersistentFTS) Search(ctx context.Context, query string, mode FTSMode, limit int) ([]SearchResult, error) {
	pf.mu.RLock()
	// 临时复制到 MemoryFTS 复用搜索逻辑
	mem := &MemoryFTS{docs: pf.docs}
	pf.mu.RUnlock()
	return mem.Search(ctx, query, mode, limit)
}

// Remove 删除文档索引。
func (pf *PersistentFTS) Remove(ctx context.Context, id string) error {
	pf.mu.Lock()
	delete(pf.docs, id)
	pf.dirties++
	pf.mu.Unlock()
	return pf.maybeSave()
}

// Size 返回索引文档数。
func (pf *PersistentFTS) Size() int {
	pf.mu.RLock()
	defer pf.mu.RUnlock()
	return len(pf.docs)
}

// Save 强制保存到磁盘。
func (pf *PersistentFTS) Save() error {
	pf.mu.Lock()
	defer pf.mu.Unlock()
	return pf.save()
}

// save 序列化写入（调用方需持有写锁）。
func (pf *PersistentFTS) save() error {
	if pf.filePath == "" {
		return nil
	}
	if err := fsperm.MkdirAll(filepath.Dir(pf.filePath)); err != nil {
		return fmt.Errorf("persistent fts: mkdir: %w", err)
	}
	data := persistentFTSData{
		Docs: make(map[string]*docEntryJSON, len(pf.docs)),
	}
	for id, doc := range pf.docs {
		entry := &docEntryJSON{
			ID:      doc.ID,
			Content: doc.Content,
			Tokens:  doc.Tokens,
		}
		data.Docs[id] = entry
	}
	raw, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("persistent fts: marshal: %w", err)
	}
	if err := fsperm.WriteFile(pf.filePath, raw); err != nil {
		return fmt.Errorf("persistent fts: write: %w", err)
	}
	pf.dirties = 0
	return nil
}

// load 从磁盘加载（初始化时调用）。
func (pf *PersistentFTS) load() error {
	if pf.filePath == "" {
		return nil
	}
	raw, err := os.ReadFile(pf.filePath)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("persistent fts: read: %w", err)
	}
	var data persistentFTSData
	if err := json.Unmarshal(raw, &data); err != nil {
		return fmt.Errorf("persistent fts: unmarshal: %w", err)
	}
	if data.Docs != nil {
		for id, entry := range data.Docs {
			pf.docs[id] = &DocEntry{
				ID:      entry.ID,
				Content: entry.Content,
				Tokens:  entry.Tokens,
			}
		}
	}
	return nil
}

// maybeSave 当变更累积到阈值时自动保存。
func (pf *PersistentFTS) maybeSave() error {
	pf.mu.RLock()
	dirties := pf.dirties
	pf.mu.RUnlock()
	if dirties >= 10 {
		return pf.Save()
	}
	return nil
}