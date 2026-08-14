//go:build !onnx

package vector

import "context"

// 该文件在「未启用 onnx build tag」时编译，提供与 embedder_onnx.go 同名的
// 公开 API 桩实现，使默认构建无需 CGO/gcc 即可通过。
//
// 行为：所有 ONNX 相关函数均返回零值 / nil，调用方据此自动降级到 LocalEmbedder。
// 启用真正的 ONNX 语义嵌入需以 `go build -tags onnx` 编译（此时本文件被排除，
// 由 embedder_onnx.go 提供真实实现，且需要 onnxruntime 动态库与 gcc）。

// ONNXEmbedderConfig 与真实实现的配置结构保持一致（字段名/类型）。
type ONNXEmbedderConfig struct {
	ModelPath     string
	TokenizerPath string
	MaxLen        int
	LibraryDir    string
	OutputLayer   string
	InputNames    []string
	Dim           int
	Precision     string
}

// ONNXEmbedder 是禁用 onnx tag 时的占位类型，仅用于满足类型与接口要求。
type ONNXEmbedder struct{}

// 确保禁用 onnx 时 ONNXEmbedder 仍满足 Embedder 接口（供调用方赋值使用）。
var _ Embedder = (*ONNXEmbedder)(nil)

// Embed 占位实现，返回零长度向量。
func (e *ONNXEmbedder) Embed(_ context.Context, _ string) ([]float32, error) {
	return nil, nil
}

// Dim 占位实现，返回 0。
func (e *ONNXEmbedder) Dim() int { return 0 }

// Close 占位实现。
func (e *ONNXEmbedder) Close() error { return nil }

// NewONNXEmbedder 占位：未启用 onnx 时恒返回 nil。
func NewONNXEmbedder(_ ONNXEmbedderConfig) (*ONNXEmbedder, error) {
	return nil, nil
}

// ONNXModelAvailable 占位：未启用 onnx 时模型恒不可用。
func ONNXModelAvailable(_ string) (modelPath, tokenizerPath string) {
	return "", ""
}

// ONNXModelAvailableWithPrecision 占位。
func ONNXModelAvailableWithPrecision(_ string, _ string) (modelPath, tokenizerPath string) {
	return "", ""
}

// IsONNXModelAvailable 占位。
func IsONNXModelAvailable(_ string) bool { return false }

// NewONNXEmbedderOrFallback 占位：未启用 onnx 时恒返回 nil，由调用方降级。
func NewONNXEmbedderOrFallback(_ string, _ int, _ string) *ONNXEmbedder {
	return nil
}

// NewONNXEmbedderOrFallbackWithConfig 占位。
func NewONNXEmbedderOrFallbackWithConfig(_ string, _ int, _ string, _ ONNXEmbedderConfig) *ONNXEmbedder {
	return nil
}

// GetONNXEmbedderGlobal 占位。
func GetONNXEmbedderGlobal(_ string, _ int, _ string) *ONNXEmbedder {
	return nil
}

// GetONNXEmbedderGlobalWithConfig 占位。
func GetONNXEmbedderGlobalWithConfig(_ string, _ int, _ string, _ ONNXEmbedderConfig) *ONNXEmbedder {
	return nil
}

// LastGlobalONNXInitError 占位。
func LastGlobalONNXInitError() error { return nil }

// CloseGlobalONNXEmbedder 占位。
func CloseGlobalONNXEmbedder() error { return nil }
