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
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/idcu/codeschema/internal/log"
)

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

// Ensure 确保模型可用：已存在直接返回 true；缺失则尝试下载。
// 返回 (ok, err)：ok=false 表示模型不可用（调用方应降级），err 为可观测的失败原因。
func (d *ModelDownloader) Ensure(ctx context.Context, modelName string) (bool, error) {
	// 1. 本地已存在（校验文件系统：onnx/ 下任一 .onnx + tokenizer.json，
	//    不依赖 IsONNXModelAvailable——该函数在默认构建（非 -tags onnx）下走 stub 恒 false）
	if d.modelFilesPresent() {
		d.logger.Debug("ONNX model already present", "dir", d.ModelDir)
		return true, nil
	}

	// 2. 未配置远程源 → 降级
	if d.URL == "" {
		return false, fmt.Errorf("ONNX model not found under %s and no model_download_url configured", d.ModelDir)
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

// downloadAndExtract 下载 tar.gz 压缩包、可选校验、解包到 ModelDir。
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

	// 边下载边算 SHA-256（如需校验）
	var body io.Reader = resp.Body
	hasher := sha256.New()
	if d.SHA256 != "" {
		body = io.TeeReader(resp.Body, hasher)
	}
	if _, err := io.Copy(tmpFile, body); err != nil {
		return fmt.Errorf("download body: %w", err)
	}
	if err := tmpFile.Sync(); err != nil {
		return fmt.Errorf("sync temp: %w", err)
	}

	// 校验和
	if d.SHA256 != "" {
		got := hex.EncodeToString(hasher.Sum(nil))
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
