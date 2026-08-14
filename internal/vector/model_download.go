package vector

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idcu/codeschema/internal/log"
)

// localSourcePath 判断 URL 是否为本地分发源并返回本地路径。
//
//   - `file:///abs/path` → `/abs/path`
//   - `file://relative.tar.gz` → `relative.tar.gz`
//   - 不以 http(s):// 开头 → 视为本地路径原样返回
//   - http(s):// → 返回空（走 HTTP 下载）
func localSourcePath(url string) string {
	if strings.HasPrefix(url, "http://") || strings.HasPrefix(url, "https://") {
		return ""
	}
	if strings.HasPrefix(url, "file://") {
		p := strings.TrimPrefix(url, "file://")
		// file:///abs → 绝对路径（保留前导 /）；file://rel → 相对路径
		if strings.HasPrefix(p, "/") && strings.HasPrefix(url, "file:///") {
			return p // 已是 /abs
		}
		return p
	}
	return url // 本地相对/绝对路径
}

// ModelDownloader 负责 ONNX 语义模型的远程分发（幂等下载 + SHA-256 校验 + tar.gz 解包）。
//
// 分发策略：
//  1. 本地优先：ModelDir 已含模型文件（onnx/ + tokenizer.json）→ 直接使用，不下载；
//  2. 远程回填：模型缺失且配置 ModelDownloadURL → 下载压缩包、校验、解包到 ModelDir；
//  3. 优雅降级：下载失败 / 校验不匹配 / 未配置远程源 → 返回错误信息，调用方降级到 LocalEmbedder。
type ModelDownloader struct {
	ModelDir string // 目标目录（含 onnx/ + tokenizer.json）
	URL      string // 远程地址（支持 {model} 占位）
	SHA256   string // 可选校验和
	Timeout  time.Duration
	logger   *log.Logger
}

// NewModelDownloader 创建模型下载器。
func NewModelDownloader(modelDir, url, sha256 string) *ModelDownloader {
	return &ModelDownloader{
		ModelDir: modelDir,
		URL:      url,
		SHA256:   sha256,
		Timeout:  5 * time.Minute,
		logger:   log.WithModule("vector.model"),
	}
}

// ResolveFromRegistry 若未显式配置 URL，则按模型名查内置注册表回填（含 SHA256）。
// 返回是否成功回填；未命中注册表且无显式 URL 时返回 false（调用方降级）。
func (d *ModelDownloader) ResolveFromRegistry(modelName string) bool {
	if d.URL != "" {
		return true // 已有显式配置
	}
	url, sha, ok := ResolveDownloadConfig(modelName, "", "")
	if !ok {
		return false
	}
	d.URL = url
	if d.SHA256 == "" {
		d.SHA256 = sha
	}
	d.logger.Debug("model download config resolved from registry", "model", modelName, "url", url)
	return true
}

// Ensure 确保模型可用：已存在直接返回 true；缺失则尝试下载。
// 返回 (ok, err)：ok=false 表示模型不可用（调用方应降级），err 为可观测的失败原因。
func (d *ModelDownloader) Ensure(ctx context.Context, modelName string) (bool, error) {
	// 1. 本地已存在（校验文件系统：onnx/ 下任一 .onnx + tokenizer.json，
	//    不依赖 IsONNXModelAvailable——该函数在默认构建（非 -tags onnx）下走 stub 恒 false）
	if d.modelFilesPresent() {
		d.logger.Debug("ONNX model already present", "dir", d.ModelDir)
		return true, nil
	}

	// 2. 未配置远程源 → 尝试注册表回填；仍未命中 → 降级
	if d.URL == "" && !d.ResolveFromRegistry(modelName) {
		return false, fmt.Errorf("ONNX model not found under %s, no model_download_url configured, and %q not in model registry", d.ModelDir, modelName)
	}

	// 3. 下载 + 校验 + 解包
	d.logger.Info("downloading ONNX model", "model", modelName, "url", d.URL)
	if err := d.downloadAndExtract(ctx, modelName); err != nil {
		return false, fmt.Errorf("download ONNX model %s: %w", modelName, err)
	}

	// 4. 解包后确认模型文件已就位
	if !d.modelFilesPresent() {
		return false, fmt.Errorf("model downloaded but still unavailable under %s (archive layout mismatch?)", d.ModelDir)
	}
	d.logger.Info("ONNX model ready", "dir", d.ModelDir)
	return true, nil
}

// modelFilesPresent 校验模型目录是否含 onnx/ 下的模型文件与 tokenizer.json。
func (d *ModelDownloader) modelFilesPresent() bool {
	entries, err := os.ReadDir(filepath.Join(d.ModelDir, "onnx"))
	if err != nil {
		return false
	}
	hasModel := false
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".onnx") {
			hasModel = true
			break
		}
	}
	if !hasModel {
		return false
	}
	if _, err := os.Stat(filepath.Join(d.ModelDir, "tokenizer.json")); err != nil {
		return false
	}
	return true
}

// downloadAndExtract 获取 tar.gz 压缩包、可选校验、解包到 ModelDir。
//
// 分发源支持三类：
//   - http(s)://...：HTTP 下载；
//   - file:///abs/path：本地文件直读（无网络环境可分发 make models-pack 产物）；
//   - 相对/绝对本地路径：直接作为 tar.gz 路径。
func (d *ModelDownloader) downloadAndExtract(ctx context.Context, modelName string) error {
	url := strings.ReplaceAll(d.URL, "{model}", modelName)

	// 下载到临时文件
	tmpFile, err := os.CreateTemp("", "codeschema-model-*.tar.gz")
	if err != nil {
		return fmt.Errorf("create temp: %w", err)
	}
	tmpPath := tmpFile.Name()
	defer func() {
		tmpFile.Close()
		os.Remove(tmpPath)
	}()

	var hasher io.Writer = sha256.New()
	var body io.Reader
	var needHash bool = d.SHA256 != ""

	// 本地源（file:// 或本地路径）→ 直接拷贝文件
	if localPath := localSourcePath(url); localPath != "" {
		src, err := os.Open(localPath)
		if err != nil {
			return fmt.Errorf("open local model archive %s: %w", localPath, err)
		}
		defer src.Close()
		body = src
	} else {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("new request: %w", err)
		}
		client := &http.Client{Timeout: d.Timeout}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("http get %s: %w", url, err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return fmt.Errorf("http status %d for %s", resp.StatusCode, url)
		}
		body = resp.Body
	}

	// 边拷贝边算 SHA-256（如需校验）
	if needHash {
		body = io.TeeReader(body, hasher)
	}
	if _, err := io.Copy(tmpFile, body); err != nil {
		return fmt.Errorf("copy model archive: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("sync temp: %w", err)
	}

	// 校验和
	if needHash {
		got := hex.EncodeToString(hasher.(hash.Hash).Sum(nil))
		if !strings.EqualFold(got, d.SHA256) {
			return fmt.Errorf("sha256 mismatch: got %s want %s", got, d.SHA256)
		}
		d.logger.Info("model sha256 verified", "sha256", got)
	}

	// 解包到 ModelDir（先清空残留）
	if err := os.MkdirAll(d.ModelDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", d.ModelDir, err)
	}
	if err := extractTarGz(tmpPath, d.ModelDir); err != nil {
		return fmt.Errorf("extract archive: %w", err)
	}
	return nil
}

// extractTarGz 解包 tar.gz 到目标目录（防路径穿越：拒绝绝对路径与 .. 段）。
func extractTarGz(archivePath, destDir string) error {
	f, err := os.Open(archivePath)
	if err != nil {
		return err
	}
	defer f.Close()

	gzr, err := gzip.NewReader(f)
	if err != nil {
		return fmt.Errorf("gzip: %w", err)
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}

		// 安全路径
		name := filepath.Clean(hdr.Name)
		if filepath.IsAbs(name) || strings.HasPrefix(name, "..") {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		target := filepath.Join(destDir, name)
		if !strings.HasPrefix(target, filepath.Clean(destDir)+string(filepath.Separator)) && target != filepath.Clean(destDir) {
			return fmt.Errorf("unsafe target path: %s", target)
		}

		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, 0o755); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(hdr.Mode)&0o777)
			if err != nil {
				return err
			}
			if _, err := io.Copy(out, tr); err != nil {
				out.Close()
				return err
			}
			out.Close()
		}
	}
	return nil
}
