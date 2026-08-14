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
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

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

// ParseAll 批量解析，从 CodeGraph SQLite 数据库读取真实符号 / 调用数据。
//
// 数据源契约（P1 实现，待真实 CodeGraph schema 校准）：
//   - symbols 表：name TEXT, qualified_name TEXT, kind TEXT, file_path TEXT, language TEXT
//   - edges 表：   caller TEXT, callee TEXT, type TEXT
//
// 降级策略（不再静默返回空 IR）：
//   - 数据库文件不存在 / 非 SQLite / 缺少 symbols 或 edges 表 → 返回 ErrSourceUnavailable，
//     由编排层降级到 tree-sitter；
//   - symbols / edges 列与契约不符（真实 schema 漂移）→ 显式报错，绝不吐空 IR 文档。
func (a *CodeGraphAdapter) ParseAll(ctx context.Context, paths []string) (<-chan *parser.IRDocument, error) {
	// 文件缺失 → 显式降级
	if a.dbPath == "" || !adapter.FileExists(a.dbPath) {
		return nil, fmt.Errorf("CodeGraph database not found: %s: %w", a.dbPath, errors.ErrSourceUnavailable)
	}

	// 纯 Go（modernc.org/sqlite，免 CGO）打开，不破坏默认构建的免 gcc 约束。
	db, err := sql.Open("sqlite", a.dbPath)
	if err != nil {
		return nil, fmt.Errorf("open CodeGraph database %s: %w", a.dbPath, errors.ErrSourceUnavailable)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("CodeGraph database is not a valid SQLite file %s: %w", a.dbPath, errors.ErrSourceUnavailable)
	}
	defer db.Close()

	// 契约校验：缺 symbols / edges 表即视为不可用，显式降级（不静默空 IR）。
	if err := requireTables(ctx, db, "symbols", "edges"); err != nil {
		return nil, fmt.Errorf("CodeGraph schema contract unmet (%s): %w", a.dbPath, err)
	}

	// 按文件分组收集真实 IR，避免重复创建文档。
	docsByFile := make(map[string]*parser.IRDocument)
	order := make([]string, 0)

	rows, err := db.QueryContext(ctx, `SELECT name, qualified_name, kind, file_path, language FROM symbols`)
	if err != nil {
		return nil, fmt.Errorf("read symbols from CodeGraph db %s (schema drift? expected columns name,qualified_name,kind,file_path,language): %w", a.dbPath, err)
	}
	for rows.Next() {
		var name, qname, kind, filePath, language string
		if err := rows.Scan(&name, &qname, &kind, &filePath, &language); err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("scan symbol row from CodeGraph db %s (schema drift? expected columns name,qualified_name,kind,file_path,language): %w", a.dbPath, err)
		}
		doc, ok := docsByFile[filePath]
		if !ok {
			doc = &parser.IRDocument{Source: "codegraph", Language: language, FilePath: filePath}
			docsByFile[filePath] = doc
			order = append(order, filePath)
		}
		doc.Classes = append(doc.Classes, parser.ClassIR{Name: name, FullName: qname, Type: kind})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return nil, fmt.Errorf("iterate symbols from CodeGraph db %s: %w", a.dbPath, err)
	}
	_ = rows.Close()

	edgeRows, err := db.QueryContext(ctx, `SELECT caller, callee, type FROM edges`)
	if err != nil {
		return nil, fmt.Errorf("read edges from CodeGraph db %s (schema drift? expected columns caller,callee,type): %w", a.dbPath, err)
	}
	for edgeRows.Next() {
		var caller, callee, etype string
		if err := edgeRows.Scan(&caller, &callee, &etype); err != nil {
			_ = edgeRows.Close()
			return nil, fmt.Errorf("scan edge row from CodeGraph db %s (schema drift? expected columns caller,callee,type): %w", a.dbPath, err)
		}
		// 调用关系归属到其 caller 所在文件（按类名前缀匹配），匹配不到则跳过（不静默丢失，仅不入 IR）。
		filePath := fileOfCaller(caller, docsByFile, order)
		if filePath == "" {
			continue
		}
		docsByFile[filePath].Calls = append(docsByFile[filePath].Calls, parser.CallIR{
			CallerFQN: caller, CalleeFQN: callee, CallType: etype,
		})
	}
	if err := edgeRows.Err(); err != nil {
		_ = edgeRows.Close()
		return nil, fmt.Errorf("iterate edges from CodeGraph db %s: %w", a.dbPath, err)
	}
	_ = edgeRows.Close()

	ch := make(chan *parser.IRDocument)
	go func() {
		defer close(ch)
		for _, fp := range order {
			select {
			case <-ctx.Done():
				return
			case ch <- docsByFile[fp]:
			}
		}
	}()
	return ch, nil
}

// requireTables 校验 SQLite 数据库中存在给定名称的表 / 视图；任一缺失即返回错误。
func requireTables(ctx context.Context, db *sql.DB, tables ...string) error {
	for _, t := range tables {
		var n int
		const q = `SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table','view') AND name = ?`
		if err := db.QueryRowContext(ctx, q, t).Scan(&n); err != nil {
			return fmt.Errorf("check table %q: %w", t, err)
		}
		if n == 0 {
			return fmt.Errorf("missing required table %q: %w", t, errors.ErrSourceUnavailable)
		}
	}
	return nil
}

// fileOfCaller 将 caller FQN 归属到其所在文件：遍历已建文档，按类名前缀匹配。
func fileOfCaller(caller string, docsByFile map[string]*parser.IRDocument, order []string) string {
	for _, fp := range order {
		for _, c := range docsByFile[fp].Classes {
			if caller == c.FullName || strings.HasPrefix(caller, c.FullName+".") {
				return fp
			}
		}
	}
	return ""
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