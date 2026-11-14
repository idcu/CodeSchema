package embedding

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"sort"
	"sync"

	"github.com/idcu/codeschema/internal/atomicfile"
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
	modelID  string // 期望/记录的 embedding 模型标识（空表示不做校验）
	dim      int    // 期望/记录的向量维度（0 表示不做校验）
	dirties  int    // 自上次保存以来的变更计数
}

// persistentData 序列化结构（向后兼容：旧文件仅含 vectors，contents 缺失时初始化为空 map）。
type persistentData struct {
	Vectors  map[string][]float32 `json:"vectors"`
	Contents map[string]string    `json:"contents,omitempty"`
	ModelID  string               `json:"model_id,omitempty"`
	Dim      int                  `json:"dim,omitempty"`
}

// NewPersistentStore 创建持久化向量存储。
//
// filePath 为存储文件路径（如 "./data/vector.json"）。
// 如果文件已存在，自动加载历史数据。
func NewPersistentStore(filePath string) (*PersistentStore, error) {
	return NewPersistentStoreWithModel(filePath, "", 0)
}

// NewPersistentStoreWithModel 创建带模型标识与维度校验的持久化向量存储。
//
// 数据文件记录写入时的 modelID / dim；加载时若与传入值不一致则返回错误，
// 避免切换 embedding 模型（或维度）后沿用旧向量导致余弦恒 0 / 结果静默错配。
// 旧版文件（无 model_id/dim 字段）以实际向量长度推断维度并补写元数据。
// modelID 或 dim 传空/0 表示该项不校验。
func NewPersistentStoreWithModel(filePath, modelID string, dim int) (*PersistentStore, error) {
	ps := &PersistentStore{
		vecs:     make(map[string][]float32),
		contents: make(map[string]string),
		filePath: filePath,
		modelID:  modelID,
		dim:      dim,
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

// BatchSetContents 批量保存文档原文，只触发一次落盘。
func (ps *PersistentStore) BatchSetContents(_ context.Context, ids, contents []string) error {
	if len(ids) != len(contents) {
		return ErrMismatchedLength
	}
	ps.mu.Lock()
	for i := range ids {
		ps.contents[ids[i]] = contents[i]
	}
	ps.dirties += len(ids)
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
//
// 流式写出 + 原子替换：按 ID 排序逐条序列化，避免整索引 MarshalIndent
// 产生与索引等大的瞬态缓冲；写出中途失败不会留下半份索引（见 atomicfile）。
func (ps *PersistentStore) save() error {
	if ps.filePath == "" {
		return nil
	}
	if err := atomicfile.Write(ps.filePath, ps.writeJSON); err != nil {
		return fmt.Errorf("persistent: save: %w", err)
	}
	ps.dirties = 0
	return nil
}

// writeJSON 以稳定键序流式写出 persistentData 等价 JSON。
//
// 字段顺序与 omitempty 语义与旧 persistentData 保持一致，旧文件仍可加载；
// 逐条 json.Marshal 使峰值只与单条向量相关，而不是整索引。
func (ps *PersistentStore) writeJSON(w io.Writer) error {
	bw := bufio.NewWriterSize(w, 64*1024)
	if _, err := bw.WriteString(`{"vectors":{`); err != nil {
		return err
	}
	for i, id := range sortedKeys(ps.vecs) {
		if i > 0 {
			if err := bw.WriteByte(','); err != nil {
				return err
			}
		}
		if err := writeJSONString(bw, id); err != nil {
			return err
		}
		if err := bw.WriteByte(':'); err != nil {
			return err
		}
		raw, err := json.Marshal(ps.vecs[id])
		if err != nil {
			return err
		}
		if _, err := bw.Write(raw); err != nil {
			return err
		}
	}
	if _, err := bw.WriteString("}"); err != nil {
		return err
	}

	if len(ps.contents) > 0 {
		if _, err := bw.WriteString(`,"contents":{`); err != nil {
			return err
		}
		for i, id := range sortedKeys(ps.contents) {
			if i > 0 {
				if err := bw.WriteByte(','); err != nil {
					return err
				}
			}
			if err := writeJSONString(bw, id); err != nil {
				return err
			}
			if err := bw.WriteByte(':'); err != nil {
				return err
			}
			raw, err := json.Marshal(ps.contents[id])
			if err != nil {
				return err
			}
			if _, err := bw.Write(raw); err != nil {
				return err
			}
		}
		if _, err := bw.WriteString("}"); err != nil {
			return err
		}
	}
	if ps.modelID != "" {
		raw, err := json.Marshal(ps.modelID)
		if err != nil {
			return err
		}
		if _, err := bw.WriteString(`,"model_id":`); err != nil {
			return err
		}
		if _, err := bw.Write(raw); err != nil {
			return err
		}
	}
	if ps.dim != 0 {
		if _, err := fmt.Fprintf(bw, `,"dim":%d`, ps.dim); err != nil {
			return err
		}
	}
	if _, err := bw.WriteString("}\n"); err != nil {
		return err
	}
	return bw.Flush()
}

// writeJSONString 写出一个 JSON 字符串（键/值共用）。
func writeJSONString(bw *bufio.Writer, s string) error {
	raw, err := json.Marshal(s)
	if err != nil {
		return err
	}
	_, err = bw.Write(raw)
	return err
}

// sortedKeys 返回 map 的升序键，保证落盘字节稳定、可比对。
func sortedKeys[V any](m map[string]V) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
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

	// 模型/维度校验（R6-3）：切换 embedding 模型或维度后，旧向量与查询向量不在
	// 同一空间（维度不同直接算不出余弦，同维不同模型则结果静默错配），必须拒绝加载。
	fileDim := data.Dim
	if fileDim == 0 {
		fileDim = inferDim(ps.vecs)
	}
	if ps.dim > 0 && fileDim > 0 && fileDim != ps.dim {
		return fmt.Errorf("dimension mismatch: file=%d, expected=%d (model=%q)",
			fileDim, ps.dim, ps.modelID)
	}
	if ps.dim > 0 {
		for id, vec := range ps.vecs {
			if len(vec) != ps.dim {
				return fmt.Errorf("vector %q has dim %d, expected %d", id, len(vec), ps.dim)
			}
		}
	}
	if data.ModelID != "" && ps.modelID != "" && data.ModelID != ps.modelID {
		return fmt.Errorf("model mismatch: file=%q, expected=%q", data.ModelID, ps.modelID)
	}
	// 旧版文件无元数据时，把推断结果/调用方期望固化下来，后续保存即完成格式升级。
	if data.Dim > 0 {
		ps.dim = data.Dim
	}
	if data.ModelID != "" {
		ps.modelID = data.ModelID
	}
	return nil
}

// inferDim 从已有向量推断维度（取第一条非空向量的长度），无数据时返回 0。
func inferDim(vecs map[string][]float32) int {
	for _, vec := range vecs {
		if len(vec) > 0 {
			return len(vec)
		}
	}
	return 0
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
