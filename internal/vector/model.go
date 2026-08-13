// Package vector 向量索引与语义搜索
//
// 定义 Embedding 模型接口，支持不同模型实现（本地 / OpenAI / Anthropic 等）。
package vector

import "context"

// Embedder 定义 embedding 模型接口。
//
// 支持多种实现：
//   - LocalEmbedder: 本地运行模型（BGE / mxbai-embed 等）
//   - OpenAIEmbedder: 调用 OpenAI API 生成 embedding
//   - AnthropicEmbedder: 调用 Anthropic API 生成 embedding
type Embedder interface {
	// Embed 对文本生成 embedding 向量。
	// 返回 float32 切片，长度由模型决定（通常 384 / 512 / 768 / 1024）。
	Embed(ctx context.Context, text string) ([]float32, error)

	// Dim 返回 embedding 向量维度。
	Dim() int
}

// TextEmbeddable 可被 embedding 的实体接口。
//
// 任何实现该接口的实体都可以被加入向量索引。
type TextEmbeddable interface {
	// ID 返回实体的唯一 ID（存储中的主键）。
	ID() string

	// Text 返回用于 embedding 的文本内容。
	// 通常是 doc_comment + 方法签名 + 类名 + 包名拼接。
	Text() string
}

// DefaultText 生成默认 embedding 文本。
//
// 对一个代码实体（类/方法），拼接：类全限定名 + 方法名 + 签名 + 文档注释。
func DefaultText(fullName, signature, doc string) string {
	text := fullName
	if signature != "" {
		text += " " + signature
	}
	if doc != "" {
		text += "\n" + doc
	}
	return text
}