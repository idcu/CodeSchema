package vector

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
)

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