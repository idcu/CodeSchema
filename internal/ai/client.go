package ai

import "context"

// LLMClient 是 AI 增强层的 LLM 调用抽象。
//
// 通过接口隔离具体 LLM 后端（OpenAI / 本地模型 / 离线规则），便于在测试中
// 注入 mock 实现，也便于后续按 config.ai 切换 provider。
type LLMClient interface {
	// Complete 向 LLM 发送提示词，返回补全结果。
	// 用于 EnhanceTag（每行一个标签的切片）与 EnhanceDoc（多行拼接为文档）。
	Complete(ctx context.Context, prompt string) ([]string, error)

	// Choose 向 LLM 发送选择型提示词，返回候选列表中最佳项的索引（从 0 开始）。
	// 用于 Disambiguate 同名方法消歧。
	Choose(ctx context.Context, prompt string) (int, error)
}
