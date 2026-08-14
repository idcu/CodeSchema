package vector

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// buildTarGz 构造 tar.gz 压缩包（含 onnx/model.onnx 与 tokenizer.json，模拟模型包布局）。
func buildTarGz(t *testing.T, files map[string]string) ([]byte, string) {
	t.Helper()
	var buf strings.Builder
	_ = buf // 占位
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		gz := gzip.NewWriter(pw)
		tw := tar.NewWriter(gz)
		for name, content := range files {
			hdr := &tar.Header{Name: name, Mode: 0o644, Size: int64(len(content))}
			if err := tw.WriteHeader(hdr); err != nil {
				done <- err
				return
			}
			if _, err := tw.Write([]byte(content)); err != nil {
				done <- err
				return
			}
		}
		if err := tw.Close(); err != nil {
			done <- err
			return
		}
		if err := gz.Close(); err != nil {
			done <- err
			return
		}
		done <- pw.Close()
	}()

	data, err := io.ReadAll(pr)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(data)
	return data, hex.EncodeToString(sum[:])
}

func TestModelDownloader_Ensure_DownloadsAndExtracts(t *testing.T) {
	archive, sha := buildTarGz(t, map[string]string{
		"onnx/model.onnx": "fake-model",
		"tokenizer.json":  `{"model": {"vocab": {}}}`,
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	dest := t.TempDir()
	dl := NewModelDownloader(dest, srv.URL+"/models/{model}.tar.gz", sha)
	ok, err := dl.Ensure(context.Background(), "bge-small-zh")
	if err != nil {
		t.Fatalf("Ensure: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true after download")
	}
	// 解包后模型文件应存在（默认构建下 IsONNXModelAvailable 走 stub 恒 false，故校验文件系统）
	if _, err := os.Stat(filepath.Join(dest, "onnx", "model.onnx")); err != nil {
		t.Fatalf("onnx/model.onnx not extracted: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "tokenizer.json")); err != nil {
		t.Fatalf("tokenizer.json not extracted: %v", err)
	}
	// 幂等：再次 Ensure 不下载（本地已存在）
	srv.Close() // 关闭服务，若触发下载会失败
	ok2, err := dl.Ensure(context.Background(), "bge-small-zh")
	if err != nil || !ok2 {
		t.Fatalf("idempotent Ensure failed: ok=%v err=%v", ok2, err)
	}
}

func TestModelDownloader_Ensure_NoRemoteConfig(t *testing.T) {
	dest := t.TempDir()
	// 未知模型：不在注册表且无显式 URL → 报错（不触发下载）
	dl := NewModelDownloader(dest, "", "")
	ok, err := dl.Ensure(context.Background(), "unknown-model-not-in-registry")
	if err == nil {
		t.Fatal("expected error when no remote url and model missing")
	}
	if ok {
		t.Fatal("expected ok=false when model unavailable")
	}
	if !strings.Contains(err.Error(), "not in model registry") {
		t.Errorf("expected hint about model registry, got %v", err)
	}
}

func TestModelDownloader_Ensure_SHA256Mismatch(t *testing.T) {
	archive, _ := buildTarGz(t, map[string]string{
		"onnx/model.onnx": "fake-model",
		"tokenizer.json":  "{}",
	})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	dest := t.TempDir()
	wrongSHA := strings.Repeat("0", 64)
	dl := NewModelDownloader(dest, srv.URL+"/{model}.tar.gz", wrongSHA)
	ok, err := dl.Ensure(context.Background(), "bge-small-zh")
	if err == nil {
		t.Fatal("expected sha256 mismatch error")
	}
	if ok {
		t.Fatal("expected ok=false on mismatch")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("expected sha256 mismatch in error, got %v", err)
	}
}

func TestModelDownloader_Ensure_PathTraversalRejected(t *testing.T) {
	// 构造含 ../ 路径的恶意压缩包
	var buf strings.Builder
	_ = buf
	pr, pw := io.Pipe()
	done := make(chan error, 1)
	go func() {
		gz := gzip.NewWriter(pw)
		tw := tar.NewWriter(gz)
		hdr := &tar.Header{Name: "../evil.txt", Mode: 0o644, Size: 5}
		if err := tw.WriteHeader(hdr); err != nil {
			done <- err
			return
		}
		if _, err := tw.Write([]byte("evil")); err != nil {
			done <- err
			return
		}
		tw.Close()
		gz.Close()
		done <- pw.Close()
	}()
	data, _ := io.ReadAll(pr)
	_ = <-done

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	dest := t.TempDir()
	dl := NewModelDownloader(dest, srv.URL+"/m.tar.gz", "")
	_, err := dl.Ensure(context.Background(), "m")
	if err == nil {
		t.Fatal("expected path traversal rejection")
	}
	if !strings.Contains(err.Error(), "unsafe path") {
		t.Errorf("expected unsafe path error, got %v", err)
	}
	// evil.txt 不应出现在 temp 目录外
	if _, err := os.Stat(filepath.Join(t.TempDir(), "evil.txt")); err == nil {
		t.Error("path traversal wrote file outside dest")
	}
}

// TestModelDownloader_Ensure_LocalSource 验证本地分发源（file:// 与本地路径）。
func TestModelDownloader_Ensure_LocalSource(t *testing.T) {
	archive, sha := buildTarGz(t, map[string]string{
		"onnx/model.onnx": "fake-model",
		"tokenizer.json":  "{}",
	})
	// 写入本地归档文件
	archivePath := filepath.Join(t.TempDir(), "models-bge-small-zh.tar.gz")
	if err := os.WriteFile(archivePath, archive, 0644); err != nil {
		t.Fatal(err)
	}

	// file:// 形式
	dest := t.TempDir()
	dl := NewModelDownloader(dest, "file://"+archivePath, sha)
	ok, err := dl.Ensure(context.Background(), "bge-small-zh")
	if err != nil || !ok {
		t.Fatalf("file:// ensure failed: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "onnx", "model.onnx")); err != nil {
		t.Fatalf("file:// extract failed: %v", err)
	}

	// 本地路径形式
	dest2 := t.TempDir()
	dl2 := NewModelDownloader(dest2, archivePath, "")
	ok2, err := dl2.Ensure(context.Background(), "bge-small-zh")
	if err != nil || !ok2 {
		t.Fatalf("local path ensure failed: ok=%v err=%v", ok2, err)
	}
	if _, err := os.Stat(filepath.Join(dest2, "tokenizer.json")); err != nil {
		t.Fatalf("local path extract failed: %v", err)
	}
}

// TestLocalSourcePath 验证本地源 URL 解析。
func TestLocalSourcePath(t *testing.T) {
	cases := []struct {
		url  string
		want string
	}{
		{"https://x/m.tar.gz", ""},
		{"http://x/m.tar.gz", ""},
		{"file:///abs/m.tar.gz", "/abs/m.tar.gz"},
		{"file://rel/m.tar.gz", "rel/m.tar.gz"},
		{"rel/m.tar.gz", "rel/m.tar.gz"},
		{"/abs/m.tar.gz", "/abs/m.tar.gz"},
	}
	for _, c := range cases {
		got := localSourcePath(c.url)
		if got != c.want {
			t.Errorf("localSourcePath(%q) = %q, want %q", c.url, got, c.want)
		}
	}
}

// TestModelDownloader_Ensure_LocalPresent 验证本地优先路径：模型已就位（onnx/*.onnx + tokenizer.json）
// → Ensure 返回 ok=true 且不触发下载（即使 URL 为空、注册表无此模型也不报错）。
func TestModelDownloader_Ensure_LocalPresent(t *testing.T) {
	// 构造与真实模型同构的目录（down/models/<name>/onnx/*.onnx + tokenizer.json）
	dest := t.TempDir()
	if err := os.MkdirAll(filepath.Join(dest, "onnx"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "onnx", "model_fp16.onnx"), []byte("model"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dest, "tokenizer.json"), []byte(`{}`), 0644); err != nil {
		t.Fatal(err)
	}

	// URL 为空 + 未知模型名：本地已存在 → 仍应 ok=true 且无错误（零下载）
	dl := NewModelDownloader(dest, "", "")
	ok, err := dl.Ensure(context.Background(), "unknown-local-only-model")
	if err != nil {
		t.Fatalf("Ensure with local model present: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true when model already present locally")
	}

	// 本地已存在时 URL 是坏的也不应触发下载
	dl2 := NewModelDownloader(dest, "http://127.0.0.1:1/unreachable.tar.gz", "")
	ok2, err := dl2.Ensure(context.Background(), "bge-small-zh")
	if err != nil || !ok2 {
		t.Fatalf("Ensure with local model + bad url: ok=%v err=%v", ok2, err)
	}
}

// TestModelDownloader_Ensure_LocalArtifact 验证本地产物优先：make models-pack 产物
// （build/models-<name>.tar.gz）存在时零配置分发——URL 为空也走 file:// 本地源。
func TestModelDownloader_Ensure_LocalArtifact(t *testing.T) {
	// 真实 models-pack 布局：带顶层目录 bge-small-zh-v1.5/
	archive, _ := buildTarGz(t, map[string]string{
		"bge-small-zh-v1.5/onnx/model.onnx": "fake-model",
		"bge-small-zh-v1.5/tokenizer.json":  "{}",
	})
	artifactDir := t.TempDir()
	artifactPath := filepath.Join(artifactDir, "models-bge-small-zh-v1.5.tar.gz")
	if err := os.WriteFile(artifactPath, archive, 0644); err != nil {
		t.Fatal(err)
	}

	dest := t.TempDir()
	dl := NewModelDownloader(dest, "", "") // URL 为空
	dl.LocalArtifactDirs = []string{artifactDir}
	ok, err := dl.Ensure(context.Background(), "bge-small-zh-v1.5")
	if err != nil || !ok {
		t.Fatalf("Ensure via local artifact: ok=%v err=%v", ok, err)
	}
	// 顶层目录被剥离：dest/onnx/model.onnx + dest/tokenizer.json
	if _, err := os.Stat(filepath.Join(dest, "onnx", "model.onnx")); err != nil {
		t.Fatalf("onnx/model.onnx not extracted (top dir strip failed): %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "tokenizer.json")); err != nil {
		t.Fatalf("tokenizer.json not extracted: %v", err)
	}
}

// TestExtractTarGz_TopDirStrip 验证 extractTarGz 顶层目录剥离：
// 扁平布局（onnx/ + tokenizer.json 首段不同）不剥离；带顶层目录布局剥离。
func TestExtractTarGz_TopDirStrip(t *testing.T) {
	// 扁平布局
	flat, _ := buildTarGz(t, map[string]string{
		"onnx/model.onnx": "m1",
		"tokenizer.json":  "{}",
	})
	flatPath := filepath.Join(t.TempDir(), "flat.tar.gz")
	os.WriteFile(flatPath, flat, 0644)
	d1 := t.TempDir()
	if err := extractTarGz(flatPath, d1); err != nil {
		t.Fatalf("flat extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d1, "onnx", "model.onnx")); err != nil {
		t.Fatalf("flat layout should NOT strip top dir: %v", err)
	}

	// 带顶层目录布局（make models-pack 产物）
	wrapped, _ := buildTarGz(t, map[string]string{
		"bge-small-zh-v1.5/onnx/model.onnx": "m2",
		"bge-small-zh-v1.5/tokenizer.json":  "{}",
	})
	wPath := filepath.Join(t.TempDir(), "wrapped.tar.gz")
	os.WriteFile(wPath, wrapped, 0644)
	d2 := t.TempDir()
	if err := extractTarGz(wPath, d2); err != nil {
		t.Fatalf("wrapped extract: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d2, "onnx", "model.onnx")); err != nil {
		t.Fatalf("wrapped layout should strip top dir: %v", err)
	}
	if _, err := os.Stat(filepath.Join(d2, "bge-small-zh-v1.5")); !os.IsNotExist(err) {
		t.Fatalf("top dir should be stripped, found: %v", err)
	}
}
