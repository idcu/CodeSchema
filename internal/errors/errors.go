// Package errors 定义 CodeSchema 系统的错误类型体系。
//
// 错误分类：
// - 适配器错误：解析失败、源不可用、超时
// - AI 增强错误：预算超限、LLM 不可用、增强失败
// - 存储错误：事务失败、KV 写入失败、向量索引构建失败
// - 通用错误：文件未找到、配置无效
package errors

import "errors"

// 适配器错误
var (
	ErrNoAdapter         = errors.New("no adapter found for language")
	ErrParseFailed       = errors.New("parse failed")
	ErrSourceUnavailable = errors.New("source unavailable")
	ErrParseTimeout      = errors.New("parse timeout")
)

// AI 增强错误
var (
	ErrBudgetExceeded   = errors.New("AI budget exceeded")
	ErrLLMUnavailable   = errors.New("LLM unavailable")
	ErrEnhanceFailed    = errors.New("enhance failed")
)

// 存储错误
var (
	ErrTxFailed          = errors.New("transaction failed")
	ErrKVWriteFailed     = errors.New("KV write failed")
	ErrVectorBuildFailed = errors.New("vector index build failed")
)

// 通用错误
var (
	ErrFileNotFound  = errors.New("file not found")
	ErrInvalidConfig = errors.New("invalid configuration")
)