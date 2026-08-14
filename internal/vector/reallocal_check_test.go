package vector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestRealLocalArtifact 用真实 make models-pack 产物验证零配置本地分发：
// build/models-bge-small-zh-v1.5.tar.gz 存在时，空 ModelDir + URL 为空 → 解包成功。
// 产物缺失时跳过（不阻塞无产物环境）。
func TestRealLocalArtifact(t *testing.T) {
	artifact := filepath.Join("..", "..", "build", "models-bge-small-zh-v1.5.tar.gz")
	if _, err := os.Stat(artifact); err != nil {
		t.Skipf("local artifact not present: %v", err)
	}
	dest := t.TempDir()
	dl := NewModelDownloader(dest, "", "")
	dl.LocalArtifactDirs = []string{filepath.Join("..", "..", "build")}
	ok, err := dl.Ensure(context.Background(), "bge-small-zh-v1.5")
	if err != nil || !ok {
		t.Fatalf("Ensure via real local artifact: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "onnx", "model_fp16.onnx")); err != nil {
		t.Fatalf("onnx/model_fp16.onnx not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "tokenizer.json")); err != nil {
		t.Fatalf("tokenizer.json not extracted: %v", err)
	}
}
