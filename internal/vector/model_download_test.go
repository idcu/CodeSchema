package vector

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
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

// TestModelDownloader_Ensure_RealArtifactOverHTTP 端到端验证公网分发链路（T2-1）：
// 用 make models-pack 的**真实制品**（build/models-bge-small-zh-v1.5.tar.gz）经
// HTTP 服务分发到「无本地模型」的干净目录，验证 自动下载 → SHA-256 校验 → 解包 →
// 模型就位 全链路。制品缺失时跳过（CI 无制品时优雅跳过）。
func TestModelDownloader_Ensure_RealArtifactOverHTTP(t *testing.T) {
	const artifactPath = "../.." // 相对 internal/vector 的仓库根路径（实际在下方用 os.Getwd 解析）

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// internal/vector → 仓库根
	root := filepath.Dir(filepath.Dir(wd))
	realArtifact := filepath.Join(root, "build", "models-bge-small-zh-v1.5.tar.gz")
	realSHA, err := os.ReadFile(filepath.Join(root, "build", "models-bge-small-zh-v1.5.sha256"))
	if err != nil {
		t.Skipf("real model artifact sha256 not present (run make models-pack first): %v", err)
	}
	archive, err := os.ReadFile(realArtifact)
	if err != nil {
		t.Skipf("real model artifact not present (run make models-pack first): %v", err)
	}
	// 从 sha256 文件提取哈希（格式：`<hash>  build/models-*.tar.gz`）
	shaParts := strings.Fields(string(realSHA))
	if len(shaParts) == 0 {
		t.Fatal("sha256 file format unexpected")
	}
	sha := shaParts[0]

	// 用 HTTP 服务模拟公网制品源
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/gzip")
		_, _ = w.Write(archive)
	}))
	defer srv.Close()

	// 干净目录（无本地模型）→ 从 HTTP 源拉取
	dest := t.TempDir()
	dl := NewModelDownloader(dest, srv.URL+"/models-{model}.tar.gz", sha)
	ok, err := dl.Ensure(context.Background(), "bge-small-zh-v1.5")
	if err != nil {
		t.Fatalf("Ensure from HTTP source: %v", err)
	}
	if !ok {
		t.Fatal("expected ok=true after HTTP download")
	}
	// 真实制品布局：onnx/*.onnx + tokenizer.json 已解包到 dest 根（顶层目录剥离）
	if _, err := os.Stat(filepath.Join(dest, "onnx")); err != nil {
		t.Fatalf("onnx dir not extracted from real artifact: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dest, "tokenizer.json")); err != nil {
		t.Fatalf("tokenizer.json not extracted from real artifact: %v", err)
	}
	// 幂等：关闭服务后再次 Ensure 不触发下载（本地已就位）
	srv.Close()
	ok2, err := dl.Ensure(context.Background(), "bge-small-zh-v1.5")
	if err != nil || !ok2 {
		t.Fatalf("idempotent Ensure after HTTP download: ok=%v err=%v", ok2, err)
	}
}

// parseRangeStart 解析 "bytes=N-" → N；非法返回 0。
func parseRangeStart(header string) int64 {
	if !strings.HasPrefix(header, "bytes=") {
		return 0
	}
	v := strings.TrimPrefix(header, "bytes=")
	if i := strings.Index(v, "-"); i >= 0 {
		n, _ := strconv.ParseInt(v[:i], 10, 64)
		return n
	}
	return 0
}

// TestModelDownloader_ResumeDownload 验证 HTTP Range 断点续传：
// 首次下载中途返回不完整内容（校验失败、.part 保留）→ 二次 Ensure 带 Range 续传剩余 → 成功。
func TestModelDownloader_ResumeDownload(t *testing.T) {
	archive, sha := buildTarGz(t, map[string]string{
		"onnx/model.onnx": "fake-model",
		"tokenizer.json":  "{}",
	})
	half := len(archive) / 2
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if rg := r.Header.Get("Range"); rg != "" {
			start := parseRangeStart(rg)
			w.Header().Set("Content-Range", fmt.Sprintf("bytes %d-%d/%d", start, len(archive)-1, len(archive)))
			w.WriteHeader(http.StatusPartialContent)
			_, _ = w.Write(archive[start:])
			return
		}
		// 首次完整请求：模拟中断——只返回前一半（连接正常结束，内容不完整）
		_, _ = w.Write(archive[:half])
	}))
	defer srv.Close()

	dest := t.TempDir()
	dl := NewModelDownloader(dest, srv.URL+"/models-{model}.tar.gz", sha)

	// 第一次：内容不完整 → SHA-256 校验失败，.part 保留（断点）
	ok, err := dl.Ensure(context.Background(), "bge-small-zh")
	if err == nil {
		t.Fatal("expected first attempt to fail on sha mismatch")
	}
	if ok {
		t.Fatal("expected ok=false on first attempt")
	}
	if !strings.Contains(err.Error(), "sha256 mismatch") {
		t.Errorf("expected sha256 mismatch, got %v", err)
	}
	part := filepath.Join(dest, ".download.part")
	if st, serr := os.Stat(part); serr != nil || st.Size() != int64(half) {
		t.Fatalf(".part should retain %d bytes for resume, got size=%v err=%v", half, st, serr)
	}

	// 第二次：带 Range 续传剩余部分 → 完整 → 校验通过 → 解包
	ok, err = dl.Ensure(context.Background(), "bge-small-zh")
	if err != nil || !ok {
		t.Fatalf("resume Ensure failed: ok=%v err=%v", ok, err)
	}
	if calls != 2 {
		t.Fatalf("expected 2 HTTP requests (full + range), got %d", calls)
	}
	if _, err := os.Stat(filepath.Join(dest, "onnx", "model.onnx")); err != nil {
		t.Fatalf("model not extracted after resume: %v", err)
	}
	if _, err := os.Stat(part); !os.IsNotExist(err) {
		t.Fatal(".part should be removed after successful extract")
	}
}

// TestModelDownloader_ReuseCompletePart 验证完整 .part 复用：
// 上次下载已完成但未解包（.part 校验通过）→ Ensure 直接解包，不再发起下载请求。
func TestModelDownloader_ReuseCompletePart(t *testing.T) {
	archive, sha := buildTarGz(t, map[string]string{
		"onnx/model.onnx": "fake-model",
		"tokenizer.json":  "{}",
	})
	dest := t.TempDir()
	part := filepath.Join(dest, ".download.part")
	if err := os.MkdirAll(dest, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(part, archive, 0o644); err != nil {
		t.Fatal(err)
	}
	// 服务器不应被调用
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("server should not be called when .part is already complete")
	}))
	defer srv.Close()

	dl := NewModelDownloader(dest, srv.URL+"/models-{model}.tar.gz", sha)
	ok, err := dl.Ensure(context.Background(), "bge-small-zh")
	if err != nil || !ok {
		t.Fatalf("Ensure with complete .part: ok=%v err=%v", ok, err)
	}
	if _, err := os.Stat(filepath.Join(dest, "onnx", "model.onnx")); err != nil {
		t.Fatalf("model not extracted from .part: %v", err)
	}
}

// TestResolveLocalArtifact_VersionSuffix 回归已知问题 #6：
// 默认 EmbeddingModel 为 bge-small-zh（旧短名）时，resolveLocalArtifact 必须能
// 命中 make models-pack 生成的真实本地制品 models-bge-small-zh-v1.5.tar.gz
// （精确名 bge-small-zh-v1.5 亦应命中；完全未知名返回 false）。
func TestResolveLocalArtifact_VersionSuffix(t *testing.T) {
	artifactDir := t.TempDir()
	v15 := filepath.Join(artifactDir, "models-bge-small-zh-v1.5.tar.gz")
	if err := os.WriteFile(v15, []byte("fake-artifact"), 0o644); err != nil {
		t.Fatal(err)
	}
	dl := &ModelDownloader{LocalArtifactDirs: []string{artifactDir}}

	// 旧短名 → 应回退命中 -v1.5 制品
	if p, ok := dl.resolveLocalArtifact("bge-small-zh"); !ok || p != v15 {
		t.Fatalf("resolveLocalArtifact(bge-small-zh) = (%q,%v), want (%q,true)", p, ok, v15)
	}
	// 精确名 → 直接命中
	if p, ok := dl.resolveLocalArtifact("bge-small-zh-v1.5"); !ok || p != v15 {
		t.Fatalf("resolveLocalArtifact(bge-small-zh-v1.5) = (%q,%v), want (%q,true)", p, ok, v15)
	}
	// 完全未知名 → 不命中
	if _, ok := dl.resolveLocalArtifact("unknown-model"); ok {
		t.Fatalf("resolveLocalArtifact(unknown-model) unexpectedly hit")
	}
}
