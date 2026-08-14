package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/idcu/codeschema/internal/log"
	"github.com/idcu/codeschema/internal/metrics"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/trace"
)

// init 注册扫描器模块的监控指标。
func init() {
	metrics.RegisterCounter("scanner_processed_total", "Total files processed by scanner", "status")
	metrics.RegisterGauge("scanner_files_total", "Total files discovered during scan")
	metrics.RegisterCounter("scanner_errors_total", "Total scanner errors", "operation")
	metrics.RegisterGauge("scanner_active_workers", "Active scanner worker count")
}

// Scanner 是编排层的核心模块，负责协调全量扫描与增量更新。
//
// 全量扫描使用 worker pool 并发处理文件。
// 增量更新通过哈希闸门跳过未变更文件。
type Scanner struct {
	store     store.Store
	registry  *parser.Registry
	workers   int
	semaphore chan struct{}
	logger    *log.Logger
	onIndex   func(ctx context.Context, filePath string) error // P8.3 索引回调
	onDelete  func(ctx context.Context, filePath string) error // P8.3 删除回调
}

// NewScanner 创建 Scanner 实例。
// workers 指定并发 worker 数，默认为 4。
func NewScanner(st store.Store, reg *parser.Registry, workers int) *Scanner {
	if workers <= 0 {
		workers = 4
	}
	return &Scanner{
		store:     st,
		registry:  reg,
		workers:   workers,
		semaphore: make(chan struct{}, workers),
		logger:    log.WithModule("scanner"),
	}
}

// SetOnIndex 设置索引回调，在文件入库成功后自动更新搜索索引。
func (s *Scanner) SetOnIndex(fn func(ctx context.Context, filePath string) error) {
	s.onIndex = fn
}

// SetOnDelete 设置删除回调，在文件被删除时自动清理索引。
func (s *Scanner) SetOnDelete(fn func(ctx context.Context, filePath string) error) {
	s.onDelete = fn
}

// ProcessFile 处理单个文件：哈希闸门 → 适配器选择 → 解析 → 入库。
//
// 流程：
//  1. 计算文件 SHA-256 哈希。
//  2. 如果文件不存在，触发删除回调并返回。
//  3. 查询 store 中是否已有相同哈希的文件 → 跳过。
//  4. 按语言选择适配器，解析文件得到 IR。
//  5. 填充元信息（哈希、字节数、行数）。
//  6. 调用 store.UpsertIR 完成增量入库。
//  7. 增量索引回调（P8.3）。
func (s *Scanner) ProcessFile(ctx context.Context, path string) error {
	span := trace.Start("process_file", "path", path)
	defer span.End()

	// 1. 计算哈希，如果文件不存在则触发删除处理
	h, err := sha256sum(path)
	if err != nil {
		if os.IsNotExist(err) {
			// 文件已被删除，触发删除回调
			if s.onDelete != nil {
				if err := s.onDelete(ctx, path); err != nil {
					s.logger.Warn("delete callback failed", "path", path, "error", err)
				}
			}
			metrics.IncCounter("scanner_processed_total", "deleted")
			return nil
		}
		metrics.IncCounter("scanner_errors_total", "sha256")
		return fmt.Errorf("sha256 %s: %w", path, err)
	}

	// 2. 哈希闸门：相同哈希跳过
	existing, _ := s.store.GetFileByPath(ctx, path)
	if existing != nil && existing.ContentHash == h {
		metrics.IncCounter("scanner_processed_total", "skipped")
		s.logger.Debug("file skipped (hash match)", "path", path)
		return nil
	}

	// 3. 检测语言并选择适配器
	lang := detectLang(path)
	plugin, err := s.registry.Select(lang)
	if err != nil {
		// 无适配器时标记为跳过，不返回错误
		metrics.IncCounter("scanner_processed_total", "no_adapter")
		_, storeErr := s.store.UpsertFile(ctx, path, h, 0, 0)
		return storeErr
	}

	// 4. 解析
	ir, err := plugin.Parse(ctx, path)
	if err != nil {
		metrics.IncCounter("scanner_errors_total", "parse")
		metrics.IncCounter("scanner_processed_total", "error")
		return fmt.Errorf("parse %s: %w", path, err)
	}
	if ir == nil {
		metrics.IncCounter("scanner_processed_total", "skipped")
		return nil // 跳过
	}

	// 5. 填充元信息
	ir.FileHash = h
	ir.FilePath = path
	if ir.LineCount == 0 {
		ir.LineCount = countLines(path)
	}
	if ir.ByteSize == 0 {
		ir.ByteSize = statSize(path)
	}

	// 6. 入库
	if err := s.store.UpsertIR(ctx, ir); err != nil {
		metrics.IncCounter("scanner_errors_total", "upsert_ir")
		metrics.IncCounter("scanner_processed_total", "error")
		return fmt.Errorf("upsert IR %s: %w", path, err)
	}

	// 7. 增量索引回调（P8.3）
	if s.onIndex != nil {
		if err := s.onIndex(ctx, path); err != nil {
			s.logger.Warn("index callback failed", "path", path, "error", err)
		}
	}

	metrics.IncCounter("scanner_processed_total", "ok")
	s.logger.Debug("file processed", "path", path, "lang", lang, "lines", ir.LineCount)
	return nil
}

// ScanAll 全量扫描仓库目录，使用 worker pool 并发处理。
//
// 每个文件使用独立 goroutine，通过 semaphore 控制并发数。
// 文件列表按目录递归遍历，忽略 .git/ node_modules/ 等目录。
func (s *Scanner) ScanAll(ctx context.Context, root string) error {
	span := trace.Start("scan_all", "root", root)
	defer span.End()

	s.logger.Info("starting full scan", "root", root)

	files, err := listFiles(root)
	if err != nil {
		metrics.IncCounter("scanner_errors_total", "list_files")
		return fmt.Errorf("list files: %w", err)
	}

	metrics.SetGauge("scanner_files_total", float64(len(files)))
	metrics.SetGauge("scanner_active_workers", float64(s.workers))
	s.logger.Info("files discovered", "count", len(files), "workers", s.workers)

	errCh := make(chan error, len(files))
	done := make(chan struct{}, len(files))

	for _, f := range files {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case s.semaphore <- struct{}{}:
		}

		go func(path string) {
			defer func() {
				<-s.semaphore
				done <- struct{}{}
			}()
			if err := s.ProcessFile(ctx, path); err != nil {
				errCh <- err
			}
		}(f)
	}

	// 等待所有 worker 完成
	for i := 0; i < len(files); i++ {
		<-done
	}

	close(errCh)
	close(done)

	// 收集错误
	var errs []error
	for e := range errCh {
		errs = append(errs, e)
	}
	if len(errs) > 0 {
		metrics.IncCounter("scanner_errors_total", "scan_errors")
		s.logger.Error("scan completed with errors", "total", len(files), "errors", len(errs))
		return fmt.Errorf("scan errors (%d): %v", len(errs), errs[0])
	}

	s.logger.Info("scan completed", "files", len(files))
	return nil
}

// detectLang 根据文件扩展名检测语言。
func detectLang(path string) string {
	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".go":
		return "go"
	case ".java":
		return "java"
	case ".py":
		return "py"
	case ".ts", ".tsx":
		return "ts"
	case ".js", ".jsx":
		return "js"
	case ".rs":
		return "rust"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	case ".c":
		return "c"
	case ".h", ".hpp":
		return "cpp"
	case ".kt", ".kts":
		return "kotlin"
	case ".swift":
		return "swift"
	case ".php":
		return "php"
	case ".cs":
		return "csharp"
	case ".rb":
		return "ruby"
	case ".sh", ".bash":
		return "bash"
	case ".scala", ".sc":
		return "scala"
	default:
		return "unknown"
	}
}

// countLines 统计文件行数。
// 末尾换行符不增加行数。
func countLines(path string) int {
	data, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	if len(data) == 0 {
		return 0
	}
	count := 1
	for _, b := range data {
		if b == '\n' {
			count++
		}
	}
	// 末尾换行符不增加行数
	if data[len(data)-1] == '\n' {
		count--
	}
	return count
}

// statSize 获取文件字节大小。
func statSize(path string) int64 {
	info, err := os.Stat(path)
	if err != nil {
		return 0
	}
	return info.Size()
}

// listFiles 递归列出目录下所有文件，忽略常见非源码目录。
func listFiles(root string) ([]string, error) {
	var files []string
	ignoreDirs := map[string]bool{
		".git":         true,
		"node_modules": true,
		"target":       true,
		"build":        true,
		"vendor":       true,
		".idea":        true,
		".vscode":      true,
		"__pycache__":  true,
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过无法访问的目录
		}
		if info.IsDir() && ignoreDirs[info.Name()] {
			return filepath.SkipDir
		}
		if !info.IsDir() {
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
