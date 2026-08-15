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
	// LocalArtifactDirs 本地产物搜索目录（make models-pack 产物所在目录）。
	// 解析分发源时优先匹配 `<dir>/models-<model>.tar.gz`，命中则用 file:// 本地源，
	// 实现「make models-pack 后零配置分发」。默认 ["build", "down"]。
	LocalArtifactDirs []string
	logger            *log.Logger
}

// NewModelDownloader 创建模型下载器。
func NewModelDownloader(modelDir, url, sha256 string) *ModelDownloader {
	return &ModelDownloader{
		ModelDir:          modelDir,
		URL:               url,
		SHA256:            sha256,
		Timeout:           5 * time.Minute,
		LocalArtifactDirs: []string{"build", "down"},
		logger:            log.WithModule("vector.model"),
	}
}

// resolveLocalArtifact 在本地产物目录中查找 models-<model>.tar.gz；
// 命中返回 (本地路径, true)，未命中返回 ("", false)。
//
// 精确名优先；再试常见版本后缀（如 bge-small-zh → bge-small-zh-v1.5 制品），
// 与内置注册表别名思路一致——使显式配置旧短名 embedding_model 时也能命中
// make models-pack 生成的本地打包产物（build/models-bge-small-zh-v1.5.tar.gz）。
func (d *ModelDownloader) resolveLocalArtifact(modelName string) (string, bool) {
	candidates := []string{modelName}
	if !strings.HasSuffix(modelName, "-v1.5") {
		candidates = append(candidates, modelName+"-v1.5")
	}
	for _, dir := range d.LocalArtifactDirs {
		for _, name := range candidates {
			p := filepath.Join(dir, "models-"+name+".tar.gz")
			if st, err := os.Stat(p); err == nil && !st.IsDir() {
				return p, true
			}
		}
	}
	return "", false
}

// ResolveFromRegistry 解析模型分发源（优先级）：
//  1. 显式配置 URL → 直接使用；
//  2. 本地产物（make models-pack 产物 models-<model>.tar.gz）→ file:// 本地源；
//  3. 内置注册表 → 回填 URL（含 SHA256）。
//
// 返回是否成功回填；全部未命中且无显式 URL 时返回 false（调用方降级）。
func (d *ModelDownloader) ResolveFromRegistry(modelName string) bool {
	if d.URL != "" {
		return true // 已有显式配置
	}
	// 本地产物优先：make models-pack 产物，零配置本地分发
	if p, ok := d.resolveLocalArtifact(modelName); ok {
		d.URL = "file://" + p
		d.logger.Debug("model download config resolved from local artifact", "model", modelName, "path", p)
		return true
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
// 断点续传：下载写入固定 `<ModelDir>/.download.part` 文件，而非临时文件——
// 下载中断/校验失败时保留 .part，下次重试从断点继续（HTTP Range），避免浪费已下载部分。
// 本地源（file:// / 本地路径）拷贝本身快，不续传。
//
// 分发源支持三类：
//   - http(s)://...：HTTP 下载（支持 Range 续传）；
//   - file:///abs/path：本地文件直读（无网络环境可分发 make models-pack 产物）；
//   - 相对/绝对本地路径：直接作为 tar.gz 路径。
func (d *ModelDownloader) downloadAndExtract(ctx context.Context, modelName string) error {
	url := strings.ReplaceAll(d.URL, "{model}", modelName)
	if err := os.MkdirAll(d.ModelDir, 0o755); err != nil {
		return fmt.Errorf("mkdir %s: %w", d.ModelDir, err)
	}
	partPath := filepath.Join(d.ModelDir, ".download.part")

	// 场景：上次下载已完整（校验通过）但未解包（如解包前中断）→ 直接复用
	if d.SHA256 != "" {
		if sum, err := sha256File(partPath); err == nil && strings.EqualFold(sum, d.SHA256) {
			d.logger.Debug("reusing complete .part archive, skipping download", "path", partPath)
			if err := extractTarGz(partPath, d.ModelDir); err != nil {
				return fmt.Errorf("extract archive: %w", err)
			}
			_ = os.Remove(partPath)
			return nil
		}
	}

	// 本地源（file:// 或本地路径）→ 直接拷贝（无需续传）
	if localPath := localSourcePath(url); localPath != "" {
		src, err := os.Open(localPath)
		if err != nil {
			return fmt.Errorf("open local model archive %s: %w", localPath, err)
		}
		defer src.Close()
		out, err := os.OpenFile(partPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o644)
		if err != nil {
			return fmt.Errorf("create part file: %w", err)
		}
		if _, err := io.Copy(out, src); err != nil {
			out.Close()
			return fmt.Errorf("copy local archive: %w", err)
		}
		if err := out.Sync(); err != nil {
			out.Close()
			return fmt.Errorf("sync part file: %w", err)
		}
		if err := out.Close(); err != nil {
			return fmt.Errorf("close part file: %w", err)
		}
	} else {
		// HTTP 源：支持 Range 断点续传
		offset := int64(0)
		if st, err := os.Stat(partPath); err == nil {
			offset = st.Size()
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
		if err != nil {
			return fmt.Errorf("new request: %w", err)
		}
		if offset > 0 {
			req.Header.Set("Range", fmt.Sprintf("bytes=%d-", offset))
		}
		client := &http.Client{Timeout: d.Timeout}
		resp, err := client.Do(req)
		if err != nil {
			return fmt.Errorf("http get %s: %w", url, err)
		}
		defer resp.Body.Close()

		flag := os.O_CREATE | os.O_WRONLY
		switch resp.StatusCode {
		case http.StatusPartialContent: // 206：服务器接受续传，追加
			flag |= os.O_APPEND
		case http.StatusOK: // 200：服务器忽略 Range（不支持续传），从头下载
			flag |= os.O_TRUNC
		default:
			return fmt.Errorf("http status %d for %s", resp.StatusCode, url)
		}
		out, err := os.OpenFile(partPath, flag, 0o644)
		if err != nil {
			return fmt.Errorf("open part file: %w", err)
		}
		if _, err := io.Copy(out, resp.Body); err != nil {
			out.Close()
			return fmt.Errorf("copy model archive: %w", err)
		}
		if err := out.Sync(); err != nil {
			out.Close()
			return fmt.Errorf("sync part file: %w", err)
		}
		if err := out.Close(); err != nil {
			return fmt.Errorf("close part file: %w", err)
		}
	}

	// 校验和：从 .part 重新读取计算（覆盖续传场景的完整内容）。
	// 校验失败保留 .part 供下次续传（可能是上一轮下载残留的不完整数据）。
	if d.SHA256 != "" {
		sum, err := sha256File(partPath)
		if err != nil {
			return fmt.Errorf("hash part file: %w", err)
		}
		if !strings.EqualFold(sum, d.SHA256) {
			return fmt.Errorf("sha256 mismatch: got %s want %s", sum, d.SHA256)
		}
		d.logger.Info("model sha256 verified", "sha256", sum)
	}

	// 解包到 ModelDir（成功后清理 .part）
	if err := extractTarGz(partPath, d.ModelDir); err != nil {
		return fmt.Errorf("extract archive: %w", err)
	}
	_ = os.Remove(partPath)
	return nil
}

// sha256File 计算文件的 SHA-256 十六进制摘要。
func sha256File(path string) (string, error) {
	f, err := os.Open(path)
	if err != nil {
		return "", err
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return "", err
	}
	return hex.EncodeToString(h.Sum(nil)), nil
}

// extractTarGz 解包 tar.gz 到目标目录（防路径穿越：拒绝绝对路径与 .. 段）。
//
// 兼容两种归档布局：
//   - 扁平：`onnx/model.onnx` / `tokenizer.json`（直接解到 destDir 根）；
//   - 带顶层目录：`bge-small-zh-v1.5/onnx/model.onnx`（make models-pack 产物，
//     剥离首段后解到 destDir 根，避免多出一层 `destDir/<model>/`）。
//
// 顶层目录探测：两遍扫描——先读全部条目名判断是否所有条目共享同一顶层段
// （目录条目本身不计入）；若共享则第二遍解包时剥离该段。
func extractTarGz(archivePath, destDir string) error {
	top := detectTarTopDir(archivePath)

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

		name := filepath.Clean(hdr.Name)
		if filepath.IsAbs(name) || strings.HasPrefix(name, "..") {
			return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
		}
		// 跳过 macOS AppleDouble 元数据条目（tar 生成的 ._* 文件，非模型内容）
		if strings.HasPrefix(filepath.Base(name), "._") {
			continue
		}
		// 剥离顶层目录段（若存在）
		if top != "" {
			name = strings.TrimPrefix(name, top+"/")
			if name == "" || strings.HasPrefix(name, "..") {
				return fmt.Errorf("unsafe path in archive: %s", hdr.Name)
			}
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

// detectTarTopDir 探测 tar.gz 的公共顶层目录：仅当**所有非目录条目**的首段一致时
// 返回该段（视为打包时的包裹目录）；扁平布局（onnx/ + tokenizer.json 首段不同）
// 或空归档返回 ""。
func detectTarTopDir(archivePath string) string {
	f, err := os.Open(archivePath)
	if err != nil {
		return ""
	}
	defer f.Close()
	gzr, err := gzip.NewReader(f)
	if err != nil {
		return ""
	}
	defer gzr.Close()

	tr := tar.NewReader(gzr)
	var top string
	seen := false
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return ""
		}
		if hdr.Typeflag == tar.TypeDir {
			continue // 目录条目不计入
		}
		name := filepath.Clean(hdr.Name)
		// 跳过 macOS AppleDouble 元数据条目（._* 文件）
		if strings.HasPrefix(filepath.Base(name), "._") {
			continue
		}
		seg := name
		if idx := strings.Index(seg, "/"); idx >= 0 {
			seg = seg[:idx]
		}
		if !seen {
			top = seg
			seen = true
			continue
		}
		if seg != top {
			return "" // 首段不一致 → 扁平布局
		}
	}
	return top
}
