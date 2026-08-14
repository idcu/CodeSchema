// Package codegraph 提供 CodeGraph SQLite 数据库直读适配器。
//
// 作为 BatchParser 实现，批量读取 CodeGraph 的符号表与关系表，
// 按文件路径分组组装 IRDocument。
//
// 数据源契约（2026-08-15 校准）：
//   真实 CodeGraph（主流实现，如 optave/codegraph 等）SQLite schema 为：
//     nodes: id INTEGER PK, name TEXT, kind TEXT, file TEXT, line INTEGER,
//            end_line INTEGER, role TEXT
//     edges: id INTEGER PK, source_id INTEGER, target_id INTEGER, kind TEXT,
//            confidence REAL
//   edges 通过 source_id/target_id 外键引用 nodes.id，kind 为
//   calls/imports/extends/implements/references 等。
//   历史假设契约（symbols: name/qualified_name/kind/file_path/language；
//   edges: caller/callee/type）仍作为兼容路径支持。
//
// 降级策略（绝不静默返回空 IR）：
//   - 数据库文件不存在 / 非 SQLite / nodes+edges 与 symbols+edges 均缺失
//     → 返回 ErrSourceUnavailable，由编排层降级到 tree-sitter；
//   - 列与契约不符（真实 schema 漂移）→ 显式报错。
package codegraph

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"strings"

	_ "modernc.org/sqlite"

	"github.com/idcu/codeschema/internal/errors"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/parser/adapter"
)

// schemaVariant 标识检测到的 CodeGraph 数据库 schema 形态。
type schemaVariant int

const (
	schemaUnknown schemaVariant = iota
	// schemaReal 真实 CodeGraph：nodes(id,name,kind,file,line) + edges(id,source_id,target_id,kind)
	schemaReal
	// schemaLegacy 历史假设契约：symbols(name,qualified_name,kind,file_path,language) + edges(caller,callee,type)
	schemaLegacy
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
// 自动检测 schema 形态（真实 nodes/edges 优先，历史 symbols/edges 兼容），
// 按文件分组输出 IRDocument；列漂移 / 缺表 / 非 SQLite 均显式报错，
// 绝不静默返回空 IR。
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

	// 自动检测 schema 形态
	variant, err := detectSchema(ctx, db)
	if err != nil {
		return nil, fmt.Errorf("CodeGraph schema detection failed (%s): %w", a.dbPath, err)
	}
	if variant == schemaUnknown {
		return nil, fmt.Errorf("CodeGraph schema contract unmet (%s): neither nodes/edges nor symbols/edges tables found: %w", a.dbPath, errors.ErrSourceUnavailable)
	}

	var (
		docsByFile = make(map[string]*parser.IRDocument)
		order      = make([]string, 0)
	)

	switch variant {
	case schemaReal:
		if err := readRealSchema(ctx, db, docsByFile, &order); err != nil {
			return nil, fmt.Errorf("read CodeGraph (real schema) db %s: %w", a.dbPath, err)
		}
	default: // schemaLegacy
		if err := readLegacySchema(ctx, db, docsByFile, &order); err != nil {
			return nil, fmt.Errorf("read CodeGraph (legacy schema) db %s: %w", a.dbPath, err)
		}
	}

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

// detectSchema 检测数据库采用哪种 schema 形态。
// 优先 nodes+edges（真实 CodeGraph），其次 symbols+edges（历史假设契约）。
func detectSchema(ctx context.Context, db *sql.DB) (schemaVariant, error) {
	has := func(names ...string) (bool, error) {
		for _, n := range names {
			var c int
			const q = `SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table','view') AND name = ?`
			if err := db.QueryRowContext(ctx, q, n).Scan(&c); err != nil {
				return false, err
			}
			if c > 0 {
				return true, nil
			}
		}
		return false, nil
	}

	hasNodes, err := has("nodes")
	if err != nil {
		return schemaUnknown, err
	}
	hasSymbols, err := has("symbols")
	if err != nil {
		return schemaUnknown, err
	}
	hasEdges, err := has("edges")
	if err != nil {
		return schemaUnknown, err
	}

	switch {
	case hasNodes && hasEdges:
		return schemaReal, nil
	case hasSymbols && hasEdges:
		return schemaLegacy, nil
	default:
		return schemaUnknown, nil
	}
}

// readRealSchema 读取真实 CodeGraph schema（nodes/edges）。
func readRealSchema(ctx context.Context, db *sql.DB, docsByFile map[string]*parser.IRDocument, order *[]string) error {
	// nodes: id, name, kind, file, line
	// 仅提取「符号」类节点（class/interface/struct/trait/enum/module/function/method），
	// 跳过 file/directory 等非符号节点。
	rows, err := db.QueryContext(ctx,
		`SELECT name, kind, file, COALESCE(line, 0) FROM nodes WHERE kind NOT IN ('file','directory','package')`)
	if err != nil {
		return fmt.Errorf("read nodes (schema drift? expected columns name,kind,file,line): %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var name, kind, file string
		var line int
		if err := rows.Scan(&name, &kind, &file, &line); err != nil {
			return fmt.Errorf("scan node row (schema drift? expected columns name,kind,file,line): %w", err)
		}
		if name == "" || file == "" {
			continue
		}
		doc, ok := docsByFile[file]
		if !ok {
			doc = &parser.IRDocument{Source: "codegraph", Language: langFromKind(kind), FilePath: file}
			docsByFile[file] = doc
			*order = append(*order, file)
		}
		switch {
		case isClassLike(kind):
			doc.Classes = append(doc.Classes, parser.ClassIR{
				Name: name, FullName: name, Type: classTypeFromKind(kind), StartLine: line,
			})
		case isMethodLike(kind):
			doc.Methods = append(doc.Methods, parser.MethodIR{
				Name: name, ClassFQN: "", StartLine: line,
			})
		}
	}
	if err := rows.Err(); err != nil {
		return err
	}

	// edges: source_id → target_id，JOIN nodes 取 caller/callee 名称与文件
	edgeRows, err := db.QueryContext(ctx, `
		SELECT e.kind, sn.name, sn.file, tn.name
		FROM edges e
		JOIN nodes sn ON sn.id = e.source_id
		JOIN nodes tn ON tn.id = e.target_id
		WHERE sn.file IS NOT NULL AND sn.file != ''`)
	if err != nil {
		return fmt.Errorf("read edges via nodes join (schema drift? expected edges.source_id,target_id,kind): %w", err)
	}
	defer edgeRows.Close()

	for edgeRows.Next() {
		var ekind, caller, callerFile, callee string
		if err := edgeRows.Scan(&ekind, &caller, &callerFile, &callee); err != nil {
			return fmt.Errorf("scan edge row (schema drift?): %w", err)
		}
		if doc, ok := docsByFile[callerFile]; ok {
			doc.Calls = append(doc.Calls, parser.CallIR{
				CallerFQN: caller, CalleeFQN: callee, CallType: callTypeFromKind(ekind),
			})
		}
	}
	return edgeRows.Err()
}

// readLegacySchema 读取历史假设契约 schema（symbols/edges）。
func readLegacySchema(ctx context.Context, db *sql.DB, docsByFile map[string]*parser.IRDocument, order *[]string) error {
	rows, err := db.QueryContext(ctx, `SELECT name, qualified_name, kind, file_path, language FROM symbols`)
	if err != nil {
		return fmt.Errorf("read symbols from CodeGraph db (schema drift? expected columns name,qualified_name,kind,file_path,language): %w", err)
	}
	for rows.Next() {
		var name, qname, kind, filePath, language string
		if err := rows.Scan(&name, &qname, &kind, &filePath, &language); err != nil {
			_ = rows.Close()
			return fmt.Errorf("scan symbol row from CodeGraph db (schema drift? expected columns name,qualified_name,kind,file_path,language): %w", err)
		}
		doc, ok := docsByFile[filePath]
		if !ok {
			doc = &parser.IRDocument{Source: "codegraph", Language: language, FilePath: filePath}
			docsByFile[filePath] = doc
			*order = append(*order, filePath)
		}
		doc.Classes = append(doc.Classes, parser.ClassIR{Name: name, FullName: qname, Type: kind})
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return err
	}
	_ = rows.Close()

	edgeRows, err := db.QueryContext(ctx, `SELECT caller, callee, type FROM edges`)
	if err != nil {
		return fmt.Errorf("read edges from CodeGraph db (schema drift? expected columns caller,callee,type): %w", err)
	}
	for edgeRows.Next() {
		var caller, callee, etype string
		if err := edgeRows.Scan(&caller, &callee, &etype); err != nil {
			_ = edgeRows.Close()
			return fmt.Errorf("scan edge row from CodeGraph db (schema drift? expected columns caller,callee,type): %w", err)
		}
		// 调用关系归属到其 caller 所在文件（按类名前缀匹配），匹配不到则跳过（不静默丢失，仅不入 IR）。
		filePath := fileOfCaller(caller, docsByFile, *order)
		if filePath == "" {
			continue
		}
		docsByFile[filePath].Calls = append(docsByFile[filePath].Calls, parser.CallIR{
			CallerFQN: caller, CalleeFQN: callee, CallType: etype,
		})
	}
	if err := edgeRows.Err(); err != nil {
		_ = edgeRows.Close()
		return err
	}
	_ = edgeRows.Close()
	return nil
}

// isClassLike 判断 kind 是否属于类级符号。
func isClassLike(kind string) bool {
	switch strings.ToLower(kind) {
	case "class", "interface", "struct", "trait", "enum", "module", "object", "record":
		return true
	}
	return false
}

// isMethodLike 判断 kind 是否属于方法/函数级符号。
func isMethodLike(kind string) bool {
	switch strings.ToLower(kind) {
	case "method", "function", "func", "constructor":
		return true
	}
	return false
}

// classTypeFromKind 将 CodeGraph kind 映射为 ClassIR.Type。
func classTypeFromKind(kind string) string {
	switch strings.ToLower(kind) {
	case "interface":
		return "INTERFACE"
	case "abstract", "trait":
		return "ABSTRACT"
	case "enum":
		return "ENUM"
	case "object", "module", "record":
		return "OBJECT"
	default:
		return "CLASS"
	}
}

// callTypeFromKind 将 CodeGraph edge kind 映射为 CallIR.CallType。
func callTypeFromKind(kind string) string {
	switch strings.ToLower(kind) {
	case "calls", "call":
		return "direct"
	case "extends", "inherits":
		return "interface"
	case "imports", "references", "implements":
		return "dynamic"
	default:
		return "unknown"
	}
}

// langFromKind 从节点 kind 推断语言（真实 schema 无 language 列，best-effort 兜底）。
func langFromKind(kind string) string {
	return strings.ToLower(kind)
}

// fileOfCaller 将 caller FQN 归属到其所在文件：遍历已建文档，按类名前缀匹配。
func fileOfCaller(caller string, docsByFile map[string]*parser.IRDocument, order []string) string {
	for _, fp := range order {
		for _, c := range docsByFile[fp].Classes {
			if caller == c.FullName || strings.HasPrefix(caller, c.FullName+".") {
				return fp
			}
		}
		for _, m := range docsByFile[fp].Methods {
			if caller == m.Name || strings.HasPrefix(caller, m.Name+".") {
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
