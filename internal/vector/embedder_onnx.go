//go:build onnx

package vector

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"unicode"

	ort "github.com/yalue/onnxruntime_go"
)

// ONNXEmbedder 基于 ONNX Runtime 的 BGE 系列 Embedding 模型。
//
// 默认加载 bge-small-zh-v1.5 模型（512 维输出）。
// 需要：
//   - onnxruntime 动态库（onnxruntime.dll / libonnxruntime.so / libonnxruntime.dylib）
//   - 模型文件：model.onnx + model.onnx_data（或 model_fp16.onnx + model_fp16.onnx_data）
//   - 分词器文件：tokenizer.json
type ONNXEmbedder struct {
	dim         int
	outputLayer string
	inputNames  []string
	model       *ort.DynamicAdvancedSession
	vocab       map[string]int32 // token -> id
	unkID       int32
	clsID       int32
	sepID       int32
	padID       int32
	maxLen      int
	mu          sync.Mutex
	initErr     error
}

// ONNXEmbedderConfig 配置 ONNXEmbedder。
type ONNXEmbedderConfig struct {
	// ModelPath ONNX 模型文件路径（必需）。
	ModelPath string
	// TokenizerPath tokenizer.json 路径（必需）。
	TokenizerPath string
	// MaxLen 最大序列长度，默认 512。
	MaxLen int
	// LibraryDir ONNX Runtime 共享库所在目录（可选）。
	// 如果为空，使用系统默认搜索路径（PATH / LD_LIBRARY_PATH）。
	LibraryDir string
	// OutputLayer 输出张量层名，默认 "sentence_embedding"。
	// 更换模型（如 bge-m3 输出层名不同）时无需改代码，仅配置即可。
	OutputLayer string
	// InputNames 输入张量层名，默认 ["input_ids","attention_mask","token_type_ids"]。
	// 空则使用默认；更换输入约定不同的模型时按需覆盖。
	InputNames []string
	// Dim 输出向量维度，默认 512。与模型实际输出维度不一致时推理会报错，
	// 按模型实际维度配置即可（如 bge-m3 为 1024）。
	Dim int
	// Precision 模型精度偏好：""/fp16（默认，优先 fp16 量化）、fp32（优先 FP32 原始精度）、
	// any（不偏好）。影响 ONNXModelAvailableWithPrecision 的模型文件选择顺序。
	Precision string
}

// NewONNXEmbedder 创建 ONNX Embedder，自动初始化 ONNX Runtime 环境。
//
// 如果模型文件不存在，返回 nil, nil（不视为错误，由调用方决定降级策略）。
// 如果 ONNX Runtime 初始化失败或模型加载失败，记录错误并可降级到 LocalEmbedder。
func NewONNXEmbedder(cfg ONNXEmbedderConfig) (*ONNXEmbedder, error) {
	// 检查模型文件是否存在
	if _, err := os.Stat(cfg.ModelPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("ONNX model not found: %s", cfg.ModelPath)
	}
	if _, err := os.Stat(cfg.TokenizerPath); os.IsNotExist(err) {
		return nil, fmt.Errorf("tokenizer file not found: %s", cfg.TokenizerPath)
	}

	maxLen := cfg.MaxLen
	if maxLen <= 0 {
		maxLen = 512
	}
	dim := cfg.Dim
	if dim <= 0 {
		dim = 512
	}
	outputLayer := cfg.OutputLayer
	if outputLayer == "" {
		outputLayer = "sentence_embedding"
	}
	inputNames := cfg.InputNames
	if len(inputNames) == 0 {
		inputNames = []string{"input_ids", "attention_mask", "token_type_ids"}
	}

	e := &ONNXEmbedder{
		dim:         dim,
		outputLayer: outputLayer,
		inputNames:  inputNames,
		maxLen:      maxLen,
		unkID:       100,
		clsID:       101,
		sepID:       102,
		padID:       0,
	}

	// 加载分词器
	if err := e.loadTokenizer(cfg.TokenizerPath); err != nil {
		return nil, fmt.Errorf("load tokenizer: %w", err)
	}

	// 初始化 ONNX Runtime
	if err := e.initRuntime(cfg.ModelPath, cfg.LibraryDir); err != nil {
		return nil, fmt.Errorf("init onnx runtime: %w", err)
	}

	return e, nil
}

// Embed 生成文本的 embedding 向量。
func (e *ONNXEmbedder) Embed(_ context.Context, text string) ([]float32, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	// 分词并编码
	inputIDs, attnMask, typeIDs := e.encode(text)
	if len(inputIDs) == 0 {
		return make([]float32, e.dim), nil
	}

	// 整理形状
	batchSize := int64(1)
	seqLen := int64(len(inputIDs))
	shape := ort.Shape{batchSize, seqLen}

	// 创建输入 tensor
	inputTensor, err := ort.NewTensor(shape, inputIDs)
	if err != nil {
		return nil, fmt.Errorf("create input_ids tensor: %w", err)
	}
	defer inputTensor.Destroy()

	maskTensor, err := ort.NewTensor(shape, attnMask)
	if err != nil {
		return nil, fmt.Errorf("create attention_mask tensor: %w", err)
	}
	defer maskTensor.Destroy()

	typeTensor, err := ort.NewTensor(shape, typeIDs)
	if err != nil {
		return nil, fmt.Errorf("create token_type_ids tensor: %w", err)
	}
	defer typeTensor.Destroy()

	// 创建输出 tensor（batch=1, dim=512）
	outShape := ort.Shape{batchSize, int64(e.dim)}
	outputTensor, err := ort.NewEmptyTensor[float32](outShape)
	if err != nil {
		return nil, fmt.Errorf("create output tensor: %w", err)
	}
	defer outputTensor.Destroy()

	// 运行推理
	inputs := []ort.Value{inputTensor, maskTensor, typeTensor}
	outputs := []ort.Value{outputTensor}
	if err := e.model.Run(inputs, outputs); err != nil {
		return nil, fmt.Errorf("onnx run: %w", err)
	}

	// 复制输出
	data := outputTensor.GetData()
	result := make([]float32, e.dim)
	copy(result, data)

	return result, nil
}

// Dim 返回向量维度（bge-small-zh-v1.5 为 512）。
func (e *ONNXEmbedder) Dim() int { return e.dim }

// initRuntime 初始化 ONNX Runtime 并加载模型。
func (e *ONNXEmbedder) initRuntime(modelPath, libDir string) error {
	if !ort.IsInitialized() {
		// 如果指定了共享库目录，优先使用该路径
		if libDir != "" {
			dllPath := filepath.Join(libDir, "onnxruntime.dll")
			if _, err := os.Stat(dllPath); err == nil {
				ort.SetSharedLibraryPath(dllPath)
			} else {
				// 也尝试 .so / .dylib
				soPath := filepath.Join(libDir, "libonnxruntime.so")
				dylibPath := filepath.Join(libDir, "libonnxruntime.dylib")
				if _, err := os.Stat(soPath); err == nil {
					ort.SetSharedLibraryPath(soPath)
				} else if _, err := os.Stat(dylibPath); err == nil {
					ort.SetSharedLibraryPath(dylibPath)
				}
			}
		}
		if err := ort.InitializeEnvironment(); err != nil {
			return fmt.Errorf("initialize onnxruntime environment: %w", err)
		}
	}

	session, err := ort.NewDynamicAdvancedSession(
		modelPath, e.inputNames, []string{e.outputLayer}, nil,
	)
	if err != nil {
		// 尝试销毁环境（部分初始化失败时清理）
		_ = ort.DestroyEnvironment()
		return fmt.Errorf("create onnx session: %w", err)
	}

	e.model = session
	return nil
}

// loadTokenizer 从 tokenizer.json 加载 WordPiece 词汇表。
func (e *ONNXEmbedder) loadTokenizer(path string) error {
	data, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read tokenizer file: %w", err)
	}

	var tok tokenizerJSON
	if err := json.Unmarshal(data, &tok); err != nil {
		return fmt.Errorf("parse tokenizer JSON: %w", err)
	}

	if tok.Model.Vocab == nil {
		return fmt.Errorf("tokenizer has no vocab")
	}

	e.vocab = make(map[string]int32, len(tok.Model.Vocab))
	for token, id := range tok.Model.Vocab {
		e.vocab[token] = id
	}

	return nil
}

// encode 对文本进行分词并编码为模型输入 ID 序列。
func (e *ONNXEmbedder) encode(text string) (inputIDs []int64, attnMask []int64, typeIDs []int64) {
	// 1. BERT 归一化
	normalized := e.bertNormalize(text)

	// 2. BERT 预分词
	words := e.bertPreTokenize(normalized)

	// 3. WordPiece 分词
	tokens := make([]string, 0, 128)
	tokens = append(tokens, "[CLS]")
	for _, word := range words {
		subTokens := e.wordPiece(word)
		tokens = append(tokens, subTokens...)
		// 截断
		if len(tokens) > e.maxLen-1 {
			tokens = tokens[:e.maxLen-1]
			break
		}
	}
	tokens = append(tokens, "[SEP]")

	// 4. 截断到 maxLen
	if len(tokens) > e.maxLen {
		tokens = tokens[:e.maxLen]
	}

	// 5. 转换为 ID
	seqLen := len(tokens)
	inputIDs = make([]int64, seqLen)
	attnMask = make([]int64, seqLen)
	typeIDs = make([]int64, seqLen)

	for i, token := range tokens {
		if id, ok := e.vocab[token]; ok {
			inputIDs[i] = int64(id)
		} else {
			inputIDs[i] = int64(e.unkID)
		}
		attnMask[i] = 1
		typeIDs[i] = 0
	}

	return
}

// bertNormalize 执行 BERT 文本归一化。
//
// 实现：
//   - 全部转为小写（英文）
//   - 去除非 ASCII 重音/变音符号
//   - 中文标点标准化
func (e *ONNXEmbedder) bertNormalize(text string) string {
	var b strings.Builder
	b.Grow(len(text))

	for _, r := range text {
		// 英文字母降为小写
		if r >= 'A' && r <= 'Z' {
			b.WriteRune(r + 32)
			continue
		}
		// 处理带重音的字母（简化：直接保留原字符）
		// 中文、数字、标点直接保留
		b.WriteRune(r)
	}

	return b.String()
}

// bertPreTokenize 执行 BERT 预分词（BertPreTokenizer）。
//
// 规则：
//   - 按空白字符和标点分割
//   - 中文汉字单独分割（每个汉字独立成词）
func (e *ONNXEmbedder) bertPreTokenize(text string) []string {
	var words []string
	var current strings.Builder

	flush := func() {
		if current.Len() > 0 {
			words = append(words, current.String())
			current.Reset()
		}
	}

	for _, r := range text {
		if unicode.IsSpace(r) {
			flush()
			continue
		}
		// 中文汉字、全角标点单独分割
		if isChineseChar(r) || isFullwidthPunct(r) {
			flush()
			words = append(words, string(r))
			continue
		}
		// 英文标点单独分割
		if unicode.IsPunct(r) || unicode.IsSymbol(r) {
			flush()
			words = append(words, string(r))
			continue
		}
		current.WriteRune(r)
	}
	flush()

	return words
}

// wordPiece 对单个词执行 WordPiece 分词。
//
// 贪心最长匹配优先算法：
//  1. 尝试匹配整个词
//  2. 如果不在词汇表中，从尾部逐字缩减去匹配，匹配到的部分加 ## 前缀
//  3. 如果到第一个字符仍未匹配，返回 [UNK]
func (e *ONNXEmbedder) wordPiece(word string) []string {
	if word == "" {
		return nil
	}

	// 尝试直接匹配整个词
	if _, ok := e.vocab[word]; ok {
		return []string{word}
	}

	// 贪心最长匹配
	runes := []rune(word)
	var tokens []string
	start := 0

	for start < len(runes) {
		// 从最长开始匹配
		matched := false
		for end := len(runes); end > start; end-- {
			substr := string(runes[start:end])
			if start > 0 {
				substr = "##" + substr
			}

			if _, ok := e.vocab[substr]; ok {
				tokens = append(tokens, substr)
				start = end
				matched = true
				break
			}
		}

		if !matched {
			// 单字符匹配：尝试 ##X
			single := string(runes[start])
			cont := "##" + single
			if _, ok := e.vocab[cont]; ok && start > 0 {
				tokens = append(tokens, cont)
			} else if _, ok := e.vocab[single]; ok {
				// 单字符在词汇表中
				if start > 0 {
					// 不应该发生（前面应该匹配到），但作为容错处理
					tokens = append(tokens, single)
				} else {
					tokens = append(tokens, single)
				}
			} else {
				tokens = append(tokens, "[UNK]")
			}
			start++
		}
	}

	return tokens
}

// isChineseChar 判断是否为中文字符（CJK 统一表意文字）。
func isChineseChar(r rune) bool {
	return (r >= 0x4E00 && r <= 0x9FFF) ||
		(r >= 0x3400 && r <= 0x4DBF) ||
		(r >= 0xF900 && r <= 0xFAFF) ||
		(r >= 0x2F800 && r <= 0x2FA1F)
}

// isFullwidthPunct 判断是否为全角标点。
func isFullwidthPunct(r rune) bool {
	return (r >= 0xFF00 && r <= 0xFFEF) && unicode.IsPunct(r)
}

// tokenizerJSON 用于解析 HuggingFace tokenizer.json 的顶层结构。
type tokenizerJSON struct {
	Model tokenizerModel `json:"model"`
}

type tokenizerModel struct {
	Vocab map[string]int32 `json:"vocab"`
}

// Close 释放 ONNX Runtime 资源。
func (e *ONNXEmbedder) Close() error {
	e.mu.Lock()
	defer e.mu.Unlock()

	if e.model != nil {
		if err := e.model.Destroy(); err != nil {
			return fmt.Errorf("destroy onnx session: %w", err)
		}
		e.model = nil
	}
	return nil
}

// 确保 ONNXEmbedder 实现 Embedder 接口。
var _ Embedder = (*ONNXEmbedder)(nil)

// ONNXModelAvailable 检查 ONNX 模型是否可用（默认精度偏好 fp16）。
//
// 如果模型文件存在，返回模型路径和 tokenizer 路径。
// 如果不存在，返回空字符串（可用于降级到 LocalEmbedder）。
func ONNXModelAvailable(modelDir string) (modelPath, tokenizerPath string) {
	return ONNXModelAvailableWithPrecision(modelDir, "")
}

// ONNXModelAvailableWithPrecision 检查 ONNX 模型是否可用，支持精度偏好。
//
// precision 取值：
//   - "fp16" 或 ""（默认）：优先 fp16 量化模型（体积小、速度快的生产默认）；
//   - "fp32"：优先 FP32 原始精度模型（召回精度更高，体积大）；
//   - "any"：不偏好，按候选顺序取第一个可用的。
//
// 模型文件候选（按目录 onnx/ 下）：model_fp16.onnx / model.onnx /
// model_quantized.onnx / model_q4.onnx。返回最先命中的路径与 tokenizer.json。
func ONNXModelAvailableWithPrecision(modelDir, precision string) (modelPath, tokenizerPath string) {
	// 按精度偏好重排候选：fp32 优先 FP32 文件；fp16/默认 优先 FP16 文件；any 保持原顺序
	var candidates []string
	switch strings.ToLower(precision) {
	case "fp32":
		candidates = []string{
			"model.onnx",
			"model_fp32.onnx",
			"model_fp16.onnx",
			"model_quantized.onnx",
		}
	case "any":
		candidates = []string{
			"model.onnx",
			"model_fp16.onnx",
			"model_quantized.onnx",
			"model_q4.onnx",
		}
	default: // fp16 / 空
		candidates = []string{
			"model_fp16.onnx",
			"model.onnx",
			"model_quantized.onnx",
			"model_q4.onnx",
		}
	}
	modelPath = ""
	for _, name := range candidates {
		p := filepath.Join(modelDir, "onnx", name)
		if _, err := os.Stat(p); err == nil {
			modelPath = p
			break
		}
	}
	if modelPath == "" {
		return "", ""
	}

	tokenizerPath = filepath.Join(modelDir, "tokenizer.json")
	if _, err := os.Stat(tokenizerPath); os.IsNotExist(err) {
		return "", ""
	}

	return modelPath, tokenizerPath
}

// IsONNXModelAvailable 快速检查 ONNX 模型是否可用。
func IsONNXModelAvailable(modelDir string) bool {
	mp, _ := ONNXModelAvailable(modelDir)
	return mp != ""
}

// init 包初始化时尝试注册 ONNX Runtime 共享库路径。
func init() {
	// 在 Windows 上，onnxruntime.dll 的路由由 Makefile 或手动设置处理。
	// 这里不自动设置，以避免干扰用户自定义路径。
	_ = os.Setenv
}

// NewONNXEmbedderOrFallback 尝试创建 ONNX Embedder，失败时返回 nil。
//
// 调用方可在返回 nil 时回退到 LocalEmbedder。
// modelDir 是包含模型文件（onnx/ 子目录和 tokenizer.json）的目录。
// maxLen 是最大序列长度。libDir 是 ONNX Runtime 共享库所在目录（可选，为空时使用系统默认搜索路径）。
func NewONNXEmbedderOrFallback(modelDir string, maxLen int, libDir string) *ONNXEmbedder {
	modelPath, tokenizerPath := ONNXModelAvailable(modelDir)
	if modelPath == "" {
		return nil
	}

	embedder, err := NewONNXEmbedder(ONNXEmbedderConfig{
		ModelPath:     modelPath,
		TokenizerPath: tokenizerPath,
		MaxLen:        maxLen,
		LibraryDir:    libDir,
	})
	if err != nil {
		// 初始化失败时静默返回 nil，由调用方降级
		return nil
	}

	return embedder
}

// NewONNXEmbedderOrFallbackWithConfig 按完整配置创建 ONNX Embedder，失败时返回 nil。
//
// 相比 NewONNXEmbedderOrFallback，支持精度偏好（Precision）、输出层名（OutputLayer）、
// 输入层名（InputNames）与输出维度（Dim）的可配，便于更换不同模型的部署而无需改代码。
// modelDir 是包含模型文件（onnx/ 子目录和 tokenizer.json）的目录；
// maxLen/libDir 与 OrFallback 同名参数一致；cfg 中未显式设置的字段使用模型目录推导默认值。
func NewONNXEmbedderOrFallbackWithConfig(modelDir string, maxLen int, libDir string, cfg ONNXEmbedderConfig) *ONNXEmbedder {
	modelPath, tokenizerPath := ONNXModelAvailableWithPrecision(modelDir, cfg.Precision)
	if modelPath == "" {
		return nil
	}
	cfg.ModelPath = modelPath
	cfg.TokenizerPath = tokenizerPath
	cfg.MaxLen = maxLen
	cfg.LibraryDir = libDir

	embedder, err := NewONNXEmbedder(cfg)
	if err != nil {
		return nil
	}
	return embedder
}

// ONNXEmbedderPool 池化 ONNX Embedder（全局单例）。
//
// 由于 ONNX Runtime 环境是全局的，通常只有一个 Embedder 实例；
// 使用互斥锁保护 get-or-create，保证并发初始化安全且 Close 后可按需重建
// （旧版 sync.Once 在 Close 后重置存在竞态窗口）。
type onnxGlobalState struct {
	mu       sync.Mutex
	embedder *ONNXEmbedder
	initErr  error
}

var onnxGlobal onnxGlobalState

// GetONNXEmbedderGlobal 获取全局 ONNX Embedder 单例（并发安全，可重建）。
//
// 如果模型可用则返回（首次调用时初始化），否则返回 nil。
// 初始化失败时返回 nil，可用 LastGlobalONNXInitError 查询失败原因。
// 调用方不再需要时调用 CloseGlobalONNXEmbedder 释放资源；之后再次调用
// GetONNXEmbedderGlobal 会重新初始化（而非旧版 Once 的一次性语义）。
func GetONNXEmbedderGlobal(modelDir string, maxLen int, libDir string) *ONNXEmbedder {
	return GetONNXEmbedderGlobalWithConfig(modelDir, maxLen, libDir, ONNXEmbedderConfig{})
}

// GetONNXEmbedderGlobalWithConfig 获取全局 ONNX Embedder 单例，支持完整配置。
func GetONNXEmbedderGlobalWithConfig(modelDir string, maxLen int, libDir string, cfg ONNXEmbedderConfig) *ONNXEmbedder {
	onnxGlobal.mu.Lock()
	defer onnxGlobal.mu.Unlock()

	if onnxGlobal.embedder != nil {
		return onnxGlobal.embedder
	}
	onnxGlobal.embedder = NewONNXEmbedderOrFallbackWithConfig(modelDir, maxLen, libDir, cfg)
	if onnxGlobal.embedder == nil {
		modelPath, _ := ONNXModelAvailableWithPrecision(modelDir, cfg.Precision)
		if modelPath == "" {
			onnxGlobal.initErr = fmt.Errorf("ONNX model not available under %s", modelDir)
		} else {
			onnxGlobal.initErr = fmt.Errorf("ONNX embedder init failed (model %s)", modelPath)
		}
	}
	return onnxGlobal.embedder
}

// LastGlobalONNXInitError 返回全局 ONNX Embedder 最近一次初始化的失败原因。
// 若从未失败或已成功初始化，返回 nil。
func LastGlobalONNXInitError() error {
	onnxGlobal.mu.Lock()
	defer onnxGlobal.mu.Unlock()
	return onnxGlobal.initErr
}

// CloseGlobalONNXEmbedder 释放全局 ONNX Embedder 资源（并发安全）。
func CloseGlobalONNXEmbedder() error {
	onnxGlobal.mu.Lock()
	defer onnxGlobal.mu.Unlock()

	if onnxGlobal.embedder != nil {
		err := onnxGlobal.embedder.Close()
		onnxGlobal.embedder = nil
		onnxGlobal.initErr = nil
		_ = ort.DestroyEnvironment()
		return err
	}
	return nil
}
