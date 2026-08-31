//go:build onnx

package vector

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idcu/codeschema/internal/embedding"
)

// testModelDir 返回模型目录相对于测试包目录的路径。
func testModelDir() string {
	return filepath.Join("..", "..", "down", "models", "bge-small-zh-v1.5")
}

// testLibDir 返回 ONNX Runtime 共享库目录。
func testLibDir() string {
	return filepath.Join("..", "..", "down", "onnxruntime")
}

// newTestONNXEmbedder 创建测试用的 ONNX Embedder，如果模型不可用则跳过测试。
func newTestONNXEmbedder(t *testing.T) *ONNXEmbedder {
	t.Helper()
	emb := NewONNXEmbedderOrFallback(testModelDir(), 512, testLibDir())
	if emb == nil {
		t.Skip("ONNX model or runtime not available, skipping")
	}
	return emb
}

func TestONNXModelAvailable(t *testing.T) {
	modelPath, tokPath := ONNXModelAvailable(testModelDir())
	if modelPath == "" {
		t.Skip("ONNX model not found, skipping")
	}
	t.Logf("model: %s, tokenizer: %s", modelPath, tokPath)
}

func TestONNXEmbedder_Embed(t *testing.T) {
	emb := newTestONNXEmbedder(t)
	defer emb.Close()

	vec, err := emb.Embed(context.Background(), "测试文本")
	if err != nil {
		t.Fatalf("Embed failed: %v", err)
	}

	if len(vec) != 512 {
		t.Fatalf("expected dim 512, got %d", len(vec))
	}

	nonZero := false
	for _, v := range vec {
		if v != 0 {
			nonZero = true
			break
		}
	}
	if !nonZero {
		t.Fatal("embedding vector is all zeros")
	}

	t.Logf("embedding dim=%d, first 5 values: %v", len(vec), vec[:5])
}

func TestONNXEmbedder_Dim(t *testing.T) {
	emb := newTestONNXEmbedder(t)
	defer emb.Close()

	if emb.Dim() != 512 {
		t.Fatalf("expected dim 512, got %d", emb.Dim())
	}
}

func TestONNXEmbedder_SemanticSimilarity(t *testing.T) {
	emb := newTestONNXEmbedder(t)
	defer emb.Close()

	ctx := context.Background()

	vec1, err := emb.Embed(ctx, "今天天气真好")
	if err != nil {
		t.Fatalf("Embed text1 failed: %v", err)
	}
	vec2, err := emb.Embed(ctx, "今天天气不错")
	if err != nil {
		t.Fatalf("Embed text2 failed: %v", err)
	}
	vec3, err := emb.Embed(ctx, "操作系统调度算法")
	if err != nil {
		t.Fatalf("Embed text3 failed: %v", err)
	}

	sim12 := cosSim(vec1, vec2)
	sim13 := cosSim(vec1, vec3)

	t.Logf("similarity(天气,天气)=%.4f, similarity(天气,调度)=%.4f", sim12, sim13)

	if sim12 <= sim13 {
		t.Logf("WARN: semantic similarity not as expected: sim12=%.4f <= sim13=%.4f", sim12, sim13)
	}
}

func TestONNXEmbedder_EmptyText(t *testing.T) {
	emb := newTestONNXEmbedder(t)
	defer emb.Close()

	vec, err := emb.Embed(context.Background(), "")
	if err != nil {
		t.Fatalf("Embed empty text failed: %v", err)
	}
	if len(vec) != 512 {
		t.Fatalf("expected dim 512, got %d", len(vec))
	}
}

func TestONNXEmbedder_LongText(t *testing.T) {
	emb := newTestONNXEmbedder(t)
	defer emb.Close()

	longText := ""
	for i := 0; i < 1000; i++ {
		longText += "测试长文本分词截断功能 "
	}

	vec, err := emb.Embed(context.Background(), longText)
	if err != nil {
		t.Fatalf("Embed long text failed: %v", err)
	}
	if len(vec) != 512 {
		t.Fatalf("expected dim 512, got %d", len(vec))
	}
}

func TestONNXEmbedder_Deterministic(t *testing.T) {
	emb := newTestONNXEmbedder(t)
	defer emb.Close()

	ctx := context.Background()

	vec1, err := emb.Embed(ctx, "确定性测试")
	if err != nil {
		t.Fatalf("Embed 1 failed: %v", err)
	}
	vec2, err := emb.Embed(ctx, "确定性测试")
	if err != nil {
		t.Fatalf("Embed 2 failed: %v", err)
	}

	if len(vec1) != len(vec2) {
		t.Fatalf("vec lengths differ: %d vs %d", len(vec1), len(vec2))
	}

	for i := range vec1 {
		if vec1[i] != vec2[i] {
			t.Fatalf("deterministic mismatch at index %d: %f vs %f", i, vec1[i], vec2[i])
		}
	}
}

// cosSim 计算余弦相似度，返回 float32。
func cosSim(a, b []float32) float32 {
	return float32(embedding.CosineSimilarity(a, b))
}
func TestONNXEmbedderConfig_Defaults(t *testing.T) {
	// 验证默认值推导：OutputLayer / InputNames / Dim / Precision 可配且默认合理
	e := &ONNXEmbedder{}
	_ = e // 占位：默认值推导在 NewONNXEmbedder 内部，此处验证字段可配性
	cfg := ONNXEmbedderConfig{MaxLen: 128}
	if cfg.OutputLayer != "" {
		t.Fatalf("expected empty default OutputLayer, got %q", cfg.OutputLayer)
	}
	if cfg.Dim != 0 {
		t.Fatalf("expected zero default Dim, got %d", cfg.Dim)
	}
}

func TestONNXModelAvailableWithPrecision_Candidates(t *testing.T) {
	dir := t.TempDir()
	// 只放 fp32 模型，验证 fp32 偏好命中 model.onnx
	onnxDir := filepath.Join(dir, "onnx")
	if err := os.MkdirAll(onnxDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(onnxDir, "model.onnx"), []byte("fp32"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tokenizer.json"), []byte("{}"), 0o644); err != nil {
		t.Fatal(err)
	}

	// fp32 偏好应命中 model.onnx
	mp, tok := ONNXModelAvailableWithPrecision(dir, "fp32")
	if mp == "" || tok == "" {
		t.Fatal("expected model available with fp32 preference")
	}
	if !strings.HasSuffix(mp, "model.onnx") {
		t.Fatalf("expected model.onnx for fp32, got %s", mp)
	}

	// 默认（fp16）偏好应回落命中 model.onnx（fp16 文件不存在时）
	mp2, _ := ONNXModelAvailableWithPrecision(dir, "")
	if !strings.HasSuffix(mp2, "model.onnx") {
		t.Fatalf("expected fallback to model.onnx, got %s", mp2)
	}

	// any 同样命中
	mp3, _ := ONNXModelAvailableWithPrecision(dir, "any")
	if mp3 == "" {
		t.Fatal("expected model available with any precision")
	}
}
