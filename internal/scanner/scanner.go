package scanner

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	log "gitee.com/idcu-go/log"
	"gitee.com/idcu-go/metrics"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/parser/adapter"
	"github.com/idcu/codeschema/internal/store"
	trace "gitee.com/idcu-go/trace"
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
	// 旁路限额（<=0 表示对应维度不限制）：超限文件跳过解析并标记 parse_skipped
	maxFileSize  int64
	maxLineCount int
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

// SetLimits 设置文件旁路限额：超过 maxFileSize 字节或 maxLineCount 行的文件跳过解析并
// 标记 parse_skipped（DoS 防护/索引净化）。参数 <=0 表示对应维度不限制。
func (s *Scanner) SetLimits(maxFileSize int64, maxLineCount int) {
	s.maxFileSize = maxFileSize
	s.maxLineCount = maxLineCount
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

	// 0. 限额旁路（DoS 防护）：先用廉价 stat 短路超大文件，避免整文件读取后再解析；
	//    也防止后续 sha256 全量读取占用 I/O 与内存。
	if s.maxFileSize > 0 {
		if info, serr := os.Stat(path); serr == nil && info.Size() > s.maxFileSize {
			s.logger.Debug("file skipped (size limit)", "path", path, "size", info.Size(), "limit", s.maxFileSize)
			return s.markSkipped(ctx, path, info.Size(), 0)
		}
	}

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

	// 2.5 行数旁路：超行数文件跳过解析并标记 parse_skipped
	if s.maxLineCount > 0 {
		if lc := countLines(path); lc > s.maxLineCount {
			s.logger.Debug("file skipped (line limit)", "path", path, "lines", lc, "limit", s.maxLineCount)
			return s.markSkipped(ctx, path, statSize(path), lc)
		}
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

// markSkipped 记录一个被旁路的文件（超限未解析），返回 false 表示无需继续。
// 优先经 store.SkippedWriter 标记 parse_skipped 留痕；未实现该接口的 store 回退 UpsertFile。
func (s *Scanner) markSkipped(ctx context.Context, path string, byteSize int64, lineCount int) error {
	if sw, ok := s.store.(store.SkippedWriter); ok {
		if _, err := sw.MarkParseSkipped(ctx, path, byteSize, lineCount); err != nil {
			metrics.IncCounter("scanner_errors_total", "mark_skipped")
			s.logger.Warn("mark parse_skipped failed", "path", path, "error", err)
			return nil
		}
	} else if _, err := s.store.UpsertFile(ctx, path, "", lineCount, byteSize); err != nil {
		metrics.IncCounter("scanner_errors_total", "mark_skipped")
		s.logger.Warn("record skipped file failed", "path", path, "error", err)
		return nil
	}
	metrics.IncCounter("scanner_processed_total", "parse_skipped")
	return nil
}

// detectLang 根据文件路径检测语言。
// Dockerfile 前缀特判（"Dockerfile.dev" 等）；其余委托 adapter.ExtToLang
// 的单一扩展名映射表（与解析适配层共用，避免双表漂移）。
func detectLang(path string) string {
	if base := filepath.Base(path); base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") {
		return "dockerfile"
	}
	return adapter.ExtToLang(filepath.Ext(path))
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
		// 测试临时目录：adapterbench 为规避 Windows 8.3 短路径会把样例写到仓库根
		// cs_ab_tmp/，与「扫描整仓」的测试（integration/agentbench）并行时若中途
		// 被清理会读到消失的文件导致 ScanAll 失败。忽略后互不干扰。
		"cs_ab_tmp": true,
	}
	// 数据文件：当 store 目录位于仓库根（默认 ./data）时，避免将 store.json /
	// store.lock 等持久化文件当作源码收录（无语言识别，会污染文件清单与集成测试断言）。
	ignoreFiles := map[string]bool{
		"store.json": true,
		"store.lock": true,
		"store.db":   true,
	}

	err := filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil // 跳过无法访问的目录
		}
		if info.IsDir() && ignoreDirs[info.Name()] {
			return filepath.SkipDir
		}
		if !info.IsDir() && !ignoreFiles[info.Name()] {
			// 符号链接：Walk 用 Lstat 不把链接视为目录，指向目录的链接会被误收集，
			// 后续 os.ReadFile 跟随链接读目录会报 "is a directory"。解析真实类型跳过目录链接。
			if info.Mode()&os.ModeSymlink != 0 {
				if st, err := os.Stat(path); err == nil && st.IsDir() {
					return nil
				}
			}
			files = append(files, path)
		}
		return nil
	})
	return files, err
}
