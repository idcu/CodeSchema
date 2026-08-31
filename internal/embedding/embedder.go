// Package embedding 通用向量嵌入与索引能力（领域无关）。
//
// 本包只提供中性机制：Embedder 接口、向量存储 VectorStore、索引构建器
// Indexer、确定性 MockEmbedder、本地统计 Embedder。不引用任何代码模式 /
// 符号 / 租户等业务语义，可作为独立公共模块被多个领域复用。
package embedding

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
)

// Embedder 定义 embedding 模型接口。
//
// 支持多种实现：
//   - LocalEmbedder: 本地统计模型（词袋 + TF-IDF）
//   - MockEmbedder: 确定性哈希，用于测试和开发
//   - 各领域的远程 Embedder（OpenAI / ONNX 等）在调用方实现本接口
type Embedder interface {
	// Embed 对文本生成 embedding 向量。
	// 返回 float32 切片，长度由模型决定（通常 384 / 512 / 768 / 1024）。
	Embed(ctx context.Context, text string) ([]float32, error)

	// Dim 返回 embedding 向量维度。
	Dim() int
}

// Embeddable 可被 embedding 的实体接口。
//
// 任何实现该接口的实体都可以被加入向量索引。ID 为存储主键，Text 为
// 用于 embedding 的文本内容。具体文本如何拼接由调用方决定（与领域无关）。
type Embeddable interface {
	// ID 返回实体的唯一 ID（存储中的主键）。
	ID() string

	// Text 返回用于 embedding 的文本内容。
	Text() string
}

// MockEmbedder 固定维度 mock embedding 实现，用于测试和开发。
//
// 使用确定性哈希生成向量，相同文本生成相同向量。
// 维度：128（固定，节省内存）。
type MockEmbedder struct {
	dim int
}

// NewMockEmbedder 创建 mock embedding 模型。
//
// dim 为向量维度，传 0 使用默认值 128。
func NewMockEmbedder(dim int) *MockEmbedder {
	if dim <= 0 {
		dim = 128
	}
	return &MockEmbedder{dim: dim}
}

// Embed 对文本生成确定性浮点向量。
func (m *MockEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	h := sha256.Sum256([]byte(text))
	vec := make([]float32, m.dim)
	for i := range vec {
		// 用哈希的字节序列填充向量，确保确定性
		idx := (i * 4) % len(h)
		bits := binary.LittleEndian.Uint32(h[idx : idx+4])
		// 映射到 [-1, 1] 范围
		vec[i] = float32(float64(bits)/float64(0xFFFFFFFF))*2 - 1
	}
	return vec, nil
}

// Dim 返回向量维度。
func (m *MockEmbedder) Dim() int {
	return m.dim
}
