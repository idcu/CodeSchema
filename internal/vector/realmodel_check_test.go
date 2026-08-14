package vector

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestRealModelDirPresent 用真实 down/models 目录验证本地优先零下载路径。
// 模型缺失时跳过（不阻塞无模型环境的 CI）。
func TestRealModelDirPresent(t *testing.T) {
	modelDir := filepath.Join("..", "..", "down", "models", "bge-small-zh-v1.5")
	if _, err := os.Stat(filepath.Join(modelDir, "tokenizer.json")); err != nil {
		t.Skipf("real model dir not present: %v", err)
	}
	dl := NewModelDownloader(modelDir, "", "")
	ok, err := dl.Ensure(context.Background(), "bge-small-zh-v1.5")
	if err != nil {
		t.Fatalf("Ensure on real model dir: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true for real model dir (zero-download)")
	}
}
