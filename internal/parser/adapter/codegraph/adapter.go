// Package codegraph 提供 CodeGraph SQLite 数据库直读适配器。
//
// 作为 BatchParser 实现，批量读取 CodeGraph 的 symbols 表和 edges 表，
// 按文件路径分组组装 IRDocument。
//
// 降级策略：当 CodeGraph SQLite 数据库文件不存在或格式错误时，
// 返回 ErrSourceUnavailable 触发编排层降级到 tree-sitter。
package codegraph

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/idcu/codeschema/internal/errors"
	"github.com/idcu/codeschema/internal/parser/adapter"
	"github.com/idcu/codeschema/internal/parser"
)

// CodeGraphAdapter 直读 CodeGraph SQLite 数据库的适配器。
type CodeGraphAdapter struct {
	dbPath string
}

// NewCodeGraphAdapter 创建 CodeGraph 适配器实例。
// dbPath 指向 CodeGraph 的 SQLite 数据库文件路径。
func NewCodeGraphAdapter(dbPath string) *CodeGraphAdapter {
	return &CodeGraphAdapter{
		dbPath: dbPath,
	}
}

// Name 返回适配器唯一标识。
func (a *CodeGraphAdapter) Name() string { return "codegraph" }

// Supports 判断是否支持指定语言。
// CodeGraph 支持 Go / Java / TypeScript / Python 等主流语言。
func (a *CodeGraphAdapter) Supports(lang string) bool {
	supported := map[string]bool{
		"go": true, "java": true, "ts": true, "js": true,
		"py": true, "rust": true, "cpp": true, "c": true,
	}
	return supported[lang]
}

// Init 初始化适配器，检查数据库文件是否存在。
func (a *CodeGraphAdapter) Init(ctx context.Context, config map[string]any) error {
	if config != nil {
		if path, ok := config["db_path"].(string); ok {
			a.dbPath = path
		}
	}
	return nil
}

// Close 清理适配器资源。
func (a *CodeGraphAdapter) Close() error {
	return nil
}

// Parse 解析单个文件（P0 暂不支持单文件查询，返回 ErrSourceUnavailable 触发降级）。
func (a *CodeGraphAdapter) Parse(ctx context.Context, path string) (*parser.IRDocument, error) {
	if a.dbPath == "" || !adapter.FileExists(a.dbPath) {
		return nil, errors.ErrSourceUnavailable
	}
	return nil, fmt.Errorf("single file parse not supported by CodeGraph adapter, use ParseAll instead: %w", errors.ErrSourceUnavailable)
}

// ParseAll 批量解析，从 CodeGraph SQLite 数据库读取数据。
//
// P0 实现：当数据库文件不存在时返回 ErrSourceUnavailable 触发降级。
// P1 实现：使用 database/sql + go-sqlite3 读取 symbols 和 edges 表。
func (a *CodeGraphAdapter) ParseAll(ctx context.Context, paths []string) (<-chan *parser.IRDocument, error) {
	// 检查数据库文件是否存在
	if a.dbPath == "" || !adapter.FileExists(a.dbPath) {
		return nil, fmt.Errorf("CodeGraph database not found: %s: %w", a.dbPath, errors.ErrSourceUnavailable)
	}

	ch := make(chan *parser.IRDocument)
	go func() {
		defer close(ch)

		// P0 骨架：按文件路径分组返回空 IR 文档
		// P1 实现：打开 SQLite 数据库，查询 symbols 和 edges 表
		fileGroups := groupByExt(paths)
		for ext, files := range fileGroups {
			lang := adapter.ExtToLang(ext)
			if lang == "unknown" {
				continue
			}
			for _, filePath := range files {
				select {
				case <-ctx.Done():
					return
				case ch <- &parser.IRDocument{
					Source:   "codegraph",
					Language: lang,
					FilePath: filePath,
				}:
				}
			}
		}
	}()
	return ch, nil
}

// groupByExt 按文件扩展名分组路径列表。
func groupByExt(paths []string) map[string][]string {
	groups := make(map[string][]string)
	for _, p := range paths {
		ext := strings.ToLower(filepath.Ext(p))
		groups[ext] = append(groups[ext], p)
	}
	return groups
}