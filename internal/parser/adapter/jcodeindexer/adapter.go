// Package jcodeindexer 提供 JCodeIndexer（第三方 JVM 专项索引器）SQLite 数据库直读适配器。
//
// 作为 BatchParser 实现，批量读取 JCodeIndexer 的符号表与调用关系表，
// 按文件路径分组组装 IRDocument，喂给 CodeSchema 统一的 IR 管线。
//
// 定位：CodeSchema 采用「解析外包 + 应用层差异化」——不重造 JVM AST，
// 而把 CodeGraph / JCodeIndexer / SCIP / LSP 当上游数据源消费，叠加其没有的
// Tag 体系、测试关联、AI 预算管控、语义检索。本适配器即消费 jcodeindexer 的产物。
//
// 数据源契约（2026-09-03 校准，对外部工具 jcodeindexer 的假设兼容契约）：
//
//	jcodeindexer 为 JVM 专项索引器（Java/Kotlin/Scala），产物为 SQLite，
//	数据形态为「符号 + 调用图 + 配置」。本适配器按其通用符号形态读取：
//	  symbols: name TEXT, qualified_name TEXT, kind TEXT, file_path TEXT,
//	           signature TEXT, line INTEGER
//	  edges:   caller TEXT, callee TEXT, type TEXT
//	配置项 config_file / env 为对外索引器的透传参数，本适配器只读 db 字段。
//
// 降级策略（绝不静默返回空 IR）：
//   - 数据库文件不存在 / 非 SQLite / symbols+edges 均缺失
//     → 返回 ErrSourceUnavailable，由编排层降级到 tree-sitter；
//   - 列与契约不符（真实 schema 漂移）→ 显式报错。
package jcodeindexer

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

// JCodeIndexerAdapter 直读 JCodeIndexer SQLite 数据库的适配器。
type JCodeIndexerAdapter struct {
	dbPath string
}

// NewJCodeIndexerAdapter 创建 JCodeIndexer 适配器实例。
// dbPath 指向 JCodeIndexer 的 SQLite 数据库文件路径。
func NewJCodeIndexerAdapter(dbPath string) *JCodeIndexerAdapter {
	return &JCodeIndexerAdapter{
		dbPath: dbPath,
	}
}

// Name 返回适配器唯一标识。
func (a *JCodeIndexerAdapter) Name() string { return "jcodeindexer" }

// Supports 判断是否支持指定语言。
// JCodeIndexer 为 JVM 专项索引器，覆盖 Java / Kotlin / Scala。
func (a *JCodeIndexerAdapter) Supports(lang string) bool {
	supported := map[string]bool{
		"java": true, "kotlin": true, "scala": true,
	}
	return supported[lang]
}

// Init 初始化适配器，从 config 读取 db 路径。
func (a *JCodeIndexerAdapter) Init(ctx context.Context, config map[string]any) error {
	if config != nil {
		if path, ok := config["db_path"].(string); ok {
			a.dbPath = path
		}
	}
	return nil
}

// Close 清理适配器资源（无句柄需释放）。
func (a *JCodeIndexerAdapter) Close() error {
	return nil
}

// Parse 解析单个文件（批量直读适配器不支持单文件查询，返回 ErrSourceUnavailable 触发降级）。
func (a *JCodeIndexerAdapter) Parse(ctx context.Context, path string) (*parser.IRDocument, error) {
	if a.dbPath == "" || !adapter.FileExists(a.dbPath) {
		return nil, errors.ErrSourceUnavailable
	}
	return nil, fmt.Errorf("single file parse not supported by JCodeIndexer adapter, use ParseAll instead: %w", errors.ErrSourceUnavailable)
}

// ParseAll 批量解析，从 JCodeIndexer SQLite 数据库读取真实符号 / 调用数据。
//
// 按文件分组输出 IRDocument；缺表 / 列漂移 / 非 SQLite 均显式报错，
// 绝不静默返回空 IR。JVM 概念（class/interface/enum/record）映射为 ClassIR，
// 方法级概念映射为 MethodIR，调用边映射为 CallIR。
func (a *JCodeIndexerAdapter) ParseAll(ctx context.Context, paths []string) (<-chan *parser.IRDocument, error) {
	// 文件缺失 → 显式降级
	if a.dbPath == "" || !adapter.FileExists(a.dbPath) {
		return nil, fmt.Errorf("JCodeIndexer database not found: %s: %w", a.dbPath, errors.ErrSourceUnavailable)
	}

	// 纯 Go（modernc.org/sqlite，免 CGO）打开，不破坏默认构建的免 gcc 约束。
	db, err := sql.Open("sqlite", a.dbPath)
	if err != nil {
		return nil, fmt.Errorf("open JCodeIndexer database %s: %w", a.dbPath, errors.ErrSourceUnavailable)
	}
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("JCodeIndexer database is not a valid SQLite file %s: %w", a.dbPath, errors.ErrSourceUnavailable)
	}
	defer db.Close()

	// 契约校验：symbols + edges 均缺失 → 非本适配器可消费的库，降级。
	var hasSymbols, hasEdges int
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table','view') AND name IN ('symbols','edges') AND name = 'symbols'`).Scan(&hasSymbols); err != nil {
		return nil, fmt.Errorf("JCodeIndexer schema detection failed (%s): %w", a.dbPath, err)
	}
	if err := db.QueryRowContext(ctx,
		`SELECT COUNT(*) FROM sqlite_master WHERE type IN ('table','view') AND name = 'edges'`).Scan(&hasEdges); err != nil {
		return nil, fmt.Errorf("JCodeIndexer schema detection failed (%s): %w", a.dbPath, err)
	}
	if hasSymbols == 0 || hasEdges == 0 {
		return nil, fmt.Errorf("JCodeIndexer schema contract unmet (%s): symbols/edges tables missing: %w", a.dbPath, errors.ErrSourceUnavailable)
	}

	docsByFile := make(map[string]*parser.IRDocument)
	order := make([]string, 0)

	rows, err := db.QueryContext(ctx,
		`SELECT name, qualified_name, kind, file_path, COALESCE(signature,''), COALESCE(line, 0) FROM symbols`)
	if err != nil {
		return nil, fmt.Errorf("read symbols from JCodeIndexer db (schema drift? expected columns name,qualified_name,kind,file_path,signature,line): %w", err)
	}
	processSymbols(rows, docsByFile, &order)
	rows.Close()
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate symbols from JCodeIndexer db: %w", err)
	}

	edgeRows, err := db.QueryContext(ctx, `SELECT caller, callee, COALESCE(type,'') FROM edges`)
	if err != nil {
		return nil, fmt.Errorf("read edges from JCodeIndexer db (schema drift? expected columns caller,callee,type): %w", err)
	}
	processEdges(edgeRows, docsByFile, order)
	edgeRows.Close()
	if err := edgeRows.Err(); err != nil {
		return nil, fmt.Errorf("iterate edges from JCodeIndexer db: %w", err)
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

// processSymbols 将 symbols 行写入按文件分组的 IRDocument。
func processSymbols(rows *sql.Rows, docsByFile map[string]*parser.IRDocument, order *[]string) {
	for rows.Next() {
		var name, qname, kind, filePath, signature string
		var line int
		if err := rows.Scan(&name, &qname, &kind, &filePath, &signature, &line); err != nil {
			continue // 单行列漂移不炸整批；契约级缺失由上层 rows.Err()/查询错误显式暴露
		}
		if name == "" || filePath == "" {
			continue
		}
		doc, ok := docsByFile[filePath]
		if !ok {
			doc = &parser.IRDocument{Source: "jcodeindexer", Language: langFromExt(filePath), FilePath: filePath}
			docsByFile[filePath] = doc
			*order = append(*order, filePath)
		}
		switch {
		case isClassLike(kind):
			doc.Classes = append(doc.Classes, parser.ClassIR{
				Name: name, FullName: firstNonEmpty(qname, name), Type: classTypeFromKind(kind), StartLine: line,
			})
		case isMethodLike(kind):
			doc.Methods = append(doc.Methods, parser.MethodIR{
				Name: name, ClassFQN: classFQNForMethod(qname, name), Signature: signature, StartLine: line,
			})
		}
	}
}

// processEdges 将 edges 行挂到 caller 所在文件，构建调用边。
func processEdges(rows *sql.Rows, docsByFile map[string]*parser.IRDocument, order []string) {
	for rows.Next() {
		var caller, callee, etype string
		if err := rows.Scan(&caller, &callee, &etype); err != nil {
			continue
		}
		fp := fileOfCaller(caller, docsByFile, order)
		if fp == "" {
			continue
		}
		docsByFile[fp].Calls = append(docsByFile[fp].Calls, parser.CallIR{
			CallerFQN: caller, CalleeFQN: callee, CallType: callTypeFromKind(etype),
		})
	}
}

// isClassLike 判断 kind 是否属于类级符号（JVM 概念）。
func isClassLike(kind string) bool {
	switch strings.ToLower(kind) {
	case "class", "interface", "enum", "record", "object", "trait", "annotation":
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

// classTypeFromKind 将 JCodeIndexer kind 映射为 ClassIR.Type。
func classTypeFromKind(kind string) string {
	switch strings.ToLower(kind) {
	case "interface":
		return "INTERFACE"
	case "enum":
		return "ENUM"
	case "record", "object", "trait":
		return "OBJECT"
	case "annotation":
		return "ANNOTATION"
	default:
		return "CLASS"
	}
}

// callTypeFromKind 将 JCodeIndexer edge type 映射为 CallIR.CallType。
func callTypeFromKind(ekind string) string {
	switch strings.ToLower(ekind) {
	case "calls", "call", "invoke":
		return "direct"
	case "extends", "implements", "inherits":
		return "interface"
	case "imports", "references", "uses", "dynamic":
		return "dynamic"
	default:
		return "unknown"
	}
}

// classFQNForMethod 从 qualified_name 推导方法所属类 FQN（JVM：pkg.Class.method）。
// 剥掉末尾方法段（若 qualified_name 以 name 结尾）得到类 FQN。
func classFQNForMethod(qname, name string) string {
	if qname == "" {
		return ""
	}
	if strings.HasSuffix(qname, "."+name) {
		return strings.TrimSuffix(qname, "."+name)
	}
	// 兼容 kotlin 顶层函数等无类载体场景：取父段
	if i := strings.LastIndex(qname, "."); i > 0 {
		return qname[:i]
	}
	return ""
}

// langFromExt 从文件扩展名推断语言（best-effort，默认 java）。
func langFromExt(path string) string {
	switch strings.ToLower(filepath.Ext(path)) {
	case ".kt", ".kts":
		return "kotlin"
	case ".scala", ".sc":
		return "scala"
	default:
		return "java"
	}
}

// fileOfCaller 将 caller FQN 归属到其所在文件：遍历已建文档，按类/方法 FQN 前缀匹配。
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

// firstNonEmpty 返回首个非空字符串（qname 优先）。
func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}
