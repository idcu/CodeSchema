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
