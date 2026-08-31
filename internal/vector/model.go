// Package vector 向量索引与语义搜索（领域适配层）。
//
// 通用向量机制（Embedder / VectorStore / Indexer / MemoryStore /
// PersistentStore / LocalEmbedder / MockEmbedder）已下沉到 internal/embedding，
// 本包通过类型别名复用。vector 包仅保留领域相关部分：
//   - 代码实体文本拼接 DefaultText
//   - chromem / ONNX 等远程模型后端
//   - 模型注册与分发
package vector

// DefaultText 生成默认 embedding 文本。
//
// 对一个代码实体（类/方法），拼接：类全限定名 + 方法名 + 签名 + 文档注释。
// 属于领域辅助逻辑（代码实体语义），保留在 vector 包。
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
