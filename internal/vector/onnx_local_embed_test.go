//go:build onnx

package vector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestLocalONNXEmbedE2E 端到端：真实模型 + 本机 ONNX Runtime 库 → 真实嵌入推理。
// 仅 -tags onnx 构建时运行；模型或库缺失则 skip（不阻塞无本地模型环境）。
func TestLocalONNXEmbedE2E(t *testing.T) {
	root, _ := os.Getwd() // internal/vector
	modelDir := filepath.Join(root, "..", "..", "down", "models", "bge-small-zh-v1.5")
	libDir := filepath.Join(root, "..", "..", "down", "onnxruntime")
	if _, err := os.Stat(filepath.Join(modelDir, "onnx")); err != nil {
		t.Skipf("real model absent: %v", err)
	}
	em := NewONNXEmbedderOrFallback(modelDir, 512, libDir)
	if em == nil || em.Dim() == 0 {
		t.Skipf("ONNX embedder unavailable (runtime lib or model issue)")
	}
	if em.Dim() != 512 {
		t.Fatalf("expected dim 512, got %d", em.Dim())
	}
	emb, err := em.Embed(context.Background(), "这是一个本地语义检索测试")
	if err != nil {
		t.Fatalf("embed: %v", err)
	}
	if len(emb) != 512 {
		t.Fatalf("bad emb shape: got %d want 512", len(emb))
	}
	t.Logf("local ONNX embed E2E OK: dim=%d", len(emb))
}
