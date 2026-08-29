// Package search 提供双路检索：FTS5 全文搜索 + 向量语义搜索 + 融合重排。
//
// 当前为 P0 骨架实现，使用内存 mock 替代真实引擎。
// P2 阶段将接入 chromem-go 和 SQLite FTS5。
package search

import (
	"context"
	"math"
	"sort"
	"strings"
	"sync"
	"unicode"
)

// SearchResult 检索结果。
type SearchResult struct {
	Symbol     string  `json:"symbol"`
	Kind       string  `json:"kind"`
	File       string  `json:"file"`
	Score      float64 `json:"score"`
	Snippet    string  `json:"snippet,omitempty"`
	Source     string  `json:"source"`     // "fts" 或 "vector" 或 "fused"
	Confidence float64 `json:"confidence,omitempty"` // 绝对置信度 [0,1]：语义=余弦相似度，FTS=BM25 映射（B8）
}

// FTSMode 搜索模式。
type FTSMode string

const (
	FTSModeExact   FTSMode = "exact"   // 精确匹配
	FTSModeFuzzy   FTSMode = "fuzzy"   // 模糊匹配（前缀 + 子串）
	FTSModeBoolean FTSMode = "boolean" // 布尔查询
)

// BM25 参数（B8 待决项①：纯 FTS 模式改用 BM25 绝对相关度，替代原 TF-IDF 相对归一）。
const (
	bm25K1 = 1.5
	bm25B  = 0.75
)

// FTSEngine 全文搜索引擎接口。
//
// 支持多种实现：
//   - MemoryFTS: 纯内存实现（P0 测试用）
//   - SQLiteFTS5: SQLite FTS5 实现（P2 生产路径）
type FTSEngine interface {
	// Index 索引一个文档。
	Index(ctx context.Context, id, content string) error

	// BatchIndex 批量索引文档。
	BatchIndex(ctx context.Context, ids, contents []string) error

	// Search 执行全文搜索，返回匹配结果（Score 为 BM25 绝对相关度）。
	Search(ctx context.Context, query string, mode FTSMode, limit int) ([]SearchResult, error)

	// Remove 删除指定 ID 的文档索引。
	Remove(ctx context.Context, id string) error

	// Size 返回索引文档数。
	Size() int
}

// DocEntry 内存 FTS 文档条目。
type DocEntry struct {
	ID      string
	Content string
	Tokens  []string
}

// MemoryFTS 纯内存全文搜索，用于测试和开发。
//
// 实现：
//   - 精确模式：词干匹配查询词
//   - 前缀模式：前缀匹配（`term*`）
//   - 模糊模式：子串匹配
//   - 布尔模式：AND/OR/NOT 简单运算
//   - 得分：BM25（IDF + 文档长度归一，绝对量纲，B8 待决项①）
type MemoryFTS struct {
	mu      sync.RWMutex
	docs    map[string]*DocEntry
}

// NewMemoryFTS 创建内存全文搜索引擎。
func NewMemoryFTS() *MemoryFTS {
	return &MemoryFTS{
		docs: make(map[string]*DocEntry),
	}
}

// Index 索引文档。
func (m *MemoryFTS) Index(_ context.Context, id, content string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.docs[id] = &DocEntry{
		ID:      id,
		Content: content,
		Tokens:  tokenize(content),
	}
	return nil
}

// BatchIndex 批量索引文档。
func (m *MemoryFTS) BatchIndex(_ context.Context, ids, contents []string) error {
	if len(ids) != len(contents) {
		return ErrMismatchedLength
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	for i := range ids {
		m.docs[ids[i]] = &DocEntry{
			ID:      ids[i],
			Content: contents[i],
			Tokens:  tokenize(contents[i]),
		}
	}
	return nil
}

// Search 执行全文搜索，返回 BM25 相关度得分（绝对量纲，B8 待决项①）。
func (m *MemoryFTS) Search(_ context.Context, query string, mode FTSMode, limit int) ([]SearchResult, error) {
	m.mu.RLock()
	defer m.mu.RUnlock()

	if len(m.docs) == 0 || query == "" {
		return nil, nil
	}

	query = strings.TrimSpace(query)
	isPrefix := strings.HasSuffix(query, "*")
	if isPrefix {
		query = strings.TrimSuffix(query, "*")
	}

	queryTokens := tokenize(query)
	if len(queryTokens) == 0 {
		return nil, nil
	}

	// 语料统计：文档频率 df 与平均文档长度 avgdl（BM25 需要）。
	n := float64(len(m.docs))
	var totalTokens int
	querySet := make(map[string]struct{}, len(queryTokens))
	for _, qt := range queryTokens {
		querySet[strings.ToLower(qt)] = struct{}{}
	}
	df := make(map[string]int, len(queryTokens))
	for _, doc := range m.docs {
		totalTokens += len(doc.Tokens)
		seen := make(map[string]struct{})
		for _, t := range doc.Tokens {
			lt := strings.ToLower(t)
			if _, ok := querySet[lt]; ok {
				if _, done := seen[lt]; !done {
					df[lt]++
					seen[lt] = struct{}{}
				}
			}
		}
	}
	avgdl := 1.0
	if n > 0 {
		avgdl = float64(totalTokens) / n
	}

	results := make([]SearchResult, 0)
	for _, doc := range m.docs {
		score := m.scoreDoc(doc, queryTokens, mode, isPrefix, df, n, avgdl)
		if score > 0 {
			results = append(results, SearchResult{
				Symbol:  doc.ID,
				Score:   score,
				Snippet: truncate(doc.Content, 120),
				Source:  "fts",
			})
		}
	}

	// 按得分降序排列
	sort.Slice(results, func(i, j int) bool {
		return results[i].Score > results[j].Score
	})

	if limit > 0 && limit < len(results) {
		results = results[:limit]
	}

	return results, nil
}

// scoreDoc 计算单文档 BM25 相关度（B8 待决项①：绝对量纲，含 IDF 与文档长度归一）。
func (m *MemoryFTS) scoreDoc(doc *DocEntry, queryTokens []string, mode FTSMode, isPrefix bool, df map[string]int, n, avgdl float64) float64 {
	var score float64
	lowerContent := strings.ToLower(doc.Content)

	for _, qt := range queryTokens {
		qt = strings.ToLower(qt)
		found := false

		switch mode {
		case FTSModeExact:
			// 精确匹配：词必须在文档的 token 列表中
			for _, t := range doc.Tokens {
				if strings.EqualFold(t, qt) {
					found = true
					break
				}
			}
		case FTSModeFuzzy:
			// 模糊匹配：子串匹配
			if isPrefix {
				// 前缀匹配：文档内容以查询词开头
				for _, t := range doc.Tokens {
					if strings.HasPrefix(strings.ToLower(t), qt) {
						found = true
						break
					}
				}
			} else {
				found = strings.Contains(lowerContent, qt)
			}
		default:
			// 默认：子串匹配
			found = strings.Contains(lowerContent, qt)
		}

		if !found {
			continue
		}

		// BM25：idf * 长度归一化 tf。
		d := float64(df[qt])
		idf := math.Log(1 + (n-d+0.5)/(d+0.5))
		tf := float64(strings.Count(lowerContent, qt))
		dl := float64(len(doc.Tokens))
		tfNorm := (tf * (bm25K1 + 1)) / (tf + bm25K1*(1-bm25B+bm25B*dl/avgdl))
		score += idf * tfNorm
	}

	return score
}

// Remove 删除文档索引。
func (m *MemoryFTS) Remove(_ context.Context, id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.docs, id)
	return nil
}

// Size 返回索引文档数。
func (m *MemoryFTS) Size() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return len(m.docs)
}

// tokenize 简单分词器：按非字母数字字符分割，转为小写。
func tokenize(s string) []string {
	var tokens []string
	var current strings.Builder
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) || r == '_' || r == '.' {
			current.WriteRune(r)
		} else {
			if current.Len() > 0 {
				tokens = append(tokens, strings.ToLower(current.String()))
				current.Reset()
			}
		}
	}
	if current.Len() > 0 {
		tokens = append(tokens, strings.ToLower(current.String()))
	}
	return tokens
}

func truncate(s string, maxLen int) string {
	runes := []rune(s)
	if len(runes) <= maxLen {
		return s
	}
	return string(runes[:maxLen]) + "..."
}

// ErrMismatchedLength IDs 和 Contents 长度不匹配。
var ErrMismatchedLength = &FTSError{"ids and contents length mismatch"}

// FTSError FTS 错误类型。
type FTSError struct {
	Message string
}

func (e *FTSError) Error() string { return e.Message }
