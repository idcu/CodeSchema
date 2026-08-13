package vector

import (
	"context"
	"math"
	"strings"
	"sync"
	"unicode"
)

// LocalEmbedder 基于统计特征的纯 Go Embedder。
//
// 使用词袋模型 + 哈希技巧 将文本映射为固定维度的向量，
// 无需外部 API 或模型文件，完全本地运行。
//
// 原理：
//   - 对文本分词后，每个 token 通过哈希函数映射到 [0, Dim) 的索引
//   - 使用 TF-IDF 风格的权重（词频 * IDF 近似）
//   - 输出向量归一化到单位长度
//
// 适用场景：
//   - 开发和测试环境
//   - 语义搜索需求不高的场景
//   - 网络不可达时的降级方案
type LocalEmbedder struct {
	dim    int
	mu     sync.RWMutex
	df     map[string]int // 文档频率（词出现在多少个文档中）
	docCnt int             // 总文档数
}

// NewLocalEmbedder 创建本地统计 Embedder。
//
// dim 为输出的向量维度，推荐 1024。
func NewLocalEmbedder(dim int) *LocalEmbedder {
	if dim <= 0 {
		dim = 1024
	}
	return &LocalEmbedder{
		dim: dim,
		df:  make(map[string]int),
	}
}

// Embed 对文本生成 embedding 向量。
func (l *LocalEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	tokens := localTokenize(text)
	if len(tokens) == 0 {
		vec := make([]float32, l.dim)
		return vec, nil
	}

	l.mu.RLock()
	defer l.mu.RUnlock()

	vec := make([]float64, l.dim)
	tf := make(map[string]int)

	// 统计词频
	for _, t := range tokens {
		tf[t]++
	}

	// 计算 TF-IDF 加权向量
	totalTokens := float64(len(tokens))
	for token, count := range tf {
		tfidf := float64(count) / totalTokens // TF
		if df, ok := l.df[token]; ok && df > 0 {
			tfidf *= math.Log2(float64(l.docCnt+1) / float64(df))
		}
		idx := hashToken(token) % uint64(l.dim)
		vec[idx] += tfidf
	}

	// L2 归一化
	var norm float64
	for _, v := range vec {
		norm += v * v
	}
	if norm > 0 {
		norm = math.Sqrt(norm)
		for i := range vec {
			vec[i] /= norm
		}
	}

	// 转换为 float32
	result := make([]float32, l.dim)
	for i, v := range vec {
		result[i] = float32(v)
	}
	return result, nil
}

// Dim 返回向量维度。
func (l *LocalEmbedder) Dim() int { return l.dim }

// Observe 观察一个文档，更新文档频率统计。
//
// 应在 Embed 之前调用，用于建立 IDF 词典。
func (l *LocalEmbedder) Observe(text string) {
	tokens := localTokenize(text)
	l.mu.Lock()
	defer l.mu.Unlock()
	l.docCnt++
	seen := make(map[string]bool)
	for _, t := range tokens {
		if !seen[t] {
			l.df[t]++
			seen[t] = true
		}
	}
}

// ObserveBatch 批量观察文档。
func (l *LocalEmbedder) ObserveBatch(texts []string) {
	for _, t := range texts {
		l.Observe(t)
	}
}

// Reset 重置统计信息（用于测试）。
func (l *LocalEmbedder) Reset() {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.df = make(map[string]int)
	l.docCnt = 0
}

// hashToken 使用 FNV-1a 哈希将 token 映射到 uint64。
func hashToken(token string) uint64 {
	var h uint64 = 14695981039346656037 // FNV offset basis
	for _, c := range token {
		h ^= uint64(c)
		h *= 1099511628211 // FNV prime
	}
	return h
}

// localTokenize 分词器，与 search 包 tokenize 保持一致。
func localTokenize(s string) []string {
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