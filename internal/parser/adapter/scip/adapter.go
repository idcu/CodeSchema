// Package scip 提供 SCIP index 文件直读适配器。
//
// SCIP（Source Code Index Format）是 Sourcegraph 定义的代码索引格式，
// 支持精确的符号定义和引用关系。本适配器读取由外部工具生成的 SCIP
// index 文件（如 scip-java / scip-go / scip-clang / scip-typescript），
// 按文件路径分发 IRDocument。
//
// 降级策略：当 SCIP index 目录不存在或格式错误时，
// 返回 ErrSourceUnavailable 触发编排层降级到 tree-sitter。
package scip

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/idcu/codeschema/internal/errors"
	"github.com/idcu/codeschema/internal/parser"
)

// SCIP 文档/符号/引用结构见下方 SCIPDocument / SCIPSymbol / SCIPOccurrence。
// （.scip 为 protobuf 二进制，由 scipwire.go 内极简 wire 解码器解析。）

// SCIPDocument 表示 SCIP index 中的一个文档（由 scipwire.go wire 解码填充）。
type SCIPDocument struct {
	RelativePath string            `json:"relative_path"`
	Language     string            `json:"language"`
	Symbols      []*SCIPSymbol     `json:"symbols"`
	Occurrences  []*SCIPOccurrence `json:"occurrences"`
}

// SCIPSymbol 表示 SCIP 中的符号定义（SymbolInformation）。
// 实测 scip-typescript 0.4.0 仅填 ID（完整 SCIP symbol 串）；
// Kind/EnclosingSymbol 保留字段仅供早期产物兼容，真实产物为空。
type SCIPSymbol struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Kind            int    `json:"kind"` // 早期 enum（0.4.0 不填）
	PackageName     string `json:"package_name"`
	PackageVersion  string `json:"package_version"`
	EnclosingSymbol string `json:"enclosing_symbol"`
}

// SCIPOccurrence 表示 SCIP 中的符号引用位置（定义或使用）。
// SymbolRole 为位掩码（SCIP：Definition=1/Import=2/Write=4/Read=8/...）；
// 普通标识符「引用」的 SymbolRole 为 0。
// Range 实测 3 元素 [line, startChar, endChar]（同行的列区间）；
// EnclosingRange（field 7）4 元素 [sLine, sCol, eLine, eCol]，
// 仅定义 occurrence 携带，覆盖整个符号体（如方法体）——调用归属的关键。
type SCIPOccurrence struct {
	Symbol         string `json:"symbol"`
	SymbolRole     int    `json:"symbol_role"`
	Range          []int  `json:"range"`
	EnclosingRange []int  `json:"enclosing_range"`
}

// SCIPAdapter 读取 SCIP index 文件的适配器。
type SCIPAdapter struct {
	indexDir  string
	documents []*SCIPDocument
	loaded    bool
	// maxDocs 背压上限：加载的文档总数超过该值时停止继续解析（0 表示不限）。
	// 避免超大 index 文件一次性全量载入内存导致 OOM。
	maxDocs int
	// truncated 记录是否因 maxDocs 触发截断（可观测，避免静默丢信息）。
	truncated bool
}

// NewSCIPAdapter 创建 SCIP 适配器实例。
// indexDir 指向包含 SCIP index 文件（.scip）的目录。
// 可通过 SetMaxDocs 设置流式加载的背压上限。
func NewSCIPAdapter(indexDir string) *SCIPAdapter {
	return &SCIPAdapter{
		indexDir: indexDir,
	}
}

// SetMaxDocs 设置流式加载的文档背压上限（0 表示不限）。
func (a *SCIPAdapter) SetMaxDocs(n int) {
	if n > 0 {
		a.maxDocs = n
	}
}

// Truncated 返回上次加载是否因背压上限被截断。
func (a *SCIPAdapter) Truncated() bool { return a.truncated }

// Name 返回适配器唯一标识。
func (a *SCIPAdapter) Name() string { return "scip" }

// Supports 判断是否支持指定语言。
func (a *SCIPAdapter) Supports(lang string) bool {
	supported := map[string]bool{
		"go": true, "java": true, "ts": true, "js": true,
		"py": true, "rust": true, "cpp": true, "c": true,
	}
	return supported[lang]
}

// Init 初始化适配器，加载 SCIP index 文件。
func (a *SCIPAdapter) Init(ctx context.Context, config map[string]any) error {
	if config != nil {
		if path, ok := config["index_dir"].(string); ok {
			a.indexDir = path
		}
		if n, ok := config["max_docs"].(float64); ok {
			a.SetMaxDocs(int(n))
		}
	}
	if a.indexDir == "" {
		return fmt.Errorf("scip index directory not configured: %w", errors.ErrSourceUnavailable)
	}
	return nil
}

// Close 清理适配器资源。
func (a *SCIPAdapter) Close() error {
	a.documents = nil
	a.loaded = false
	return nil
}

// Parse 解析单个文件，从已加载的 SCIP index 中查找。
func (a *SCIPAdapter) Parse(ctx context.Context, path string) (*parser.IRDocument, error) {
	if a.indexDir == "" {
		return nil, fmt.Errorf("scip index directory not configured: %w", errors.ErrSourceUnavailable)
	}

	if !a.loaded {
		if err := a.loadIndex(); err != nil {
			return nil, err
		}
	}

	relPath, err := filepath.Rel(a.indexDir, path)
	if err != nil {
		relPath = path
	}

	// 标准化路径分隔符
	relPath = strings.ReplaceAll(relPath, "\\", "/")

	for _, doc := range a.documents {
		if doc.RelativePath == relPath || strings.HasSuffix(path, doc.RelativePath) {
			return a.convertDocument(doc), nil
		}
	}

	// 文件不在 SCIP index 中，返回空 IR（非错误，由编排层跳过）
	return &parser.IRDocument{
		Source:   "scip",
		FilePath: path,
	}, nil
}

// ParseAll 批量解析，加载 SCIP index 并按文件分发 IR。
func (a *SCIPAdapter) ParseAll(ctx context.Context, paths []string) (<-chan *parser.IRDocument, error) {
	if a.indexDir == "" || !scipDirExists(a.indexDir) {
		return nil, fmt.Errorf("SCIP index directory not found: %s: %w", a.indexDir, errors.ErrSourceUnavailable)
	}

	if !a.loaded {
		if err := a.loadIndex(); err != nil {
			return nil, err
		}
	}

	ch := make(chan *parser.IRDocument)
	go func() {
		defer close(ch)

		// 按路径建立查找映射
		docMap := make(map[string]*SCIPDocument)
		for _, doc := range a.documents {
			docMap[doc.RelativePath] = doc
		}

		for _, path := range paths {
			select {
			case <-ctx.Done():
				return
			default:
			}

			relPath, err := filepath.Rel(a.indexDir, path)
			if err != nil {
				relPath = path
			}
			relPath = strings.ReplaceAll(relPath, "\\", "/")

			if doc, ok := docMap[relPath]; ok {
				select {
				case <-ctx.Done():
					return
				case ch <- a.convertDocument(doc):
				}
			} else {
				// 不在 index 中的文件返回空 IR
				select {
				case <-ctx.Done():
					return
				case ch <- &parser.IRDocument{
					Source:   "scip",
					FilePath: path,
				}:
				}
			}
		}
	}()
	return ch, nil
}

// scipDirExists 判断路径是否为已存在的目录（indexDir 应是目录，非文件）。
func scipDirExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return info.IsDir()
}

// loadIndex 扫描 indexDir 目录下的 .scip 文件并加载。
//
// .scip 为 protobuf 二进制（非 JSON）；用极简 protobuf wire 解码器
// （见 scipwire.go）解析，避免引入外部 protobuf 依赖。
// 受 a.maxDocs 背压上限约束，达到上限即停止解析并置 truncated=true。
func (a *SCIPAdapter) loadIndex() error {
	entries, err := os.ReadDir(a.indexDir)
	if err != nil {
		return fmt.Errorf("cannot read SCIP index directory %s: %w", a.indexDir, errors.ErrSourceUnavailable)
	}

	// 若已加载过则先清空，保证重复 Init 幂等。
	a.documents = nil
	a.truncated = false

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".scip") {
			continue
		}
		filePath := filepath.Join(a.indexDir, entry.Name())

		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("cannot open SCIP index file %s: %w", filePath, err)
		}

		for _, doc := range decodeIndex(data) {
			if a.maxDocs > 0 && len(a.documents) >= a.maxDocs {
				a.truncated = true
				goto done
			}
			if doc.RelativePath != "" {
				doc.RelativePath = filepath.ToSlash(doc.RelativePath)
			}
			a.documents = append(a.documents, doc)
		}
	}
done:
	a.loaded = true
	return nil
}

// convertDocument 将 SCIP 文档转换为 IRDocument。
//
// 类/方法身份从 SymbolInformation 符号表按 SCIP symbol descriptor 语法解析
// （classifySymbol；真实产物 kind 字段为空，不能依赖）。
// 调用关系从 Occurrence 按方法级语义抽取：
//   - SymbolRole 位掩码 Definition=1；其余（0 普通引用/2 Import/4 Write/8 Read
//     等）均为「使用」，Definition 之外的引用视为潜在调用点；
//   - 方法定义 occurrence 的 EnclosingRange（方法体区间）承载 caller 归属：
//     引用行落在哪个方法体内，caller 即该方法；
//   - 仅方法级符号（descriptor 含方法签名）的引用计入调用，类/字段/参数
//     引用、类型注解、import 不计（避免把引用误当调用）。
// caller/callee 用完整 SCIP symbol（跨包唯一可溯源）；方法→类归属可由上层
// 按 symbol 前缀与 ClassIR.FullName 关联（MethodIR.ClassFQN 不重复冗余存储）。
func (a *SCIPAdapter) convertDocument(doc *SCIPDocument) *parser.IRDocument {
	ir := &parser.IRDocument{
		Source:   "scip",
		Language: scipLangToCodeLang(docLang(doc)),
		FilePath: doc.RelativePath,
	}

	// 类/方法从符号表抽取
	for _, sym := range doc.Symbols {
		isClass, isMethod, name := classifySymbol(sym.ID)
		switch {
		case isClass:
			ir.Classes = append(ir.Classes, parser.ClassIR{
				Name:      name,
				FullName:  sym.ID,
				Type:      "CLASS",
			})
		case isMethod:
			ir.Methods = append(ir.Methods, parser.MethodIR{
				Name:     name,
				ClassFQN: sym.EnclosingSymbol, // 真实产物为空；类归属按 symbol 前缀关联
			})
		}
	}

	// 方法定义 occurrence（携带方法体 enclosing_range）→ caller 归属池
	type methodDef struct {
		symbol string
		span   []int // EnclosingRange [sLine,sCol,eLine,eCol]
	}
	var defs []methodDef
	for _, occ := range doc.Occurrences {
		if occ.SymbolRole&scipRoleDefinition == 0 {
			continue
		}
		if _, isM, _ := classifySymbol(occ.Symbol); isM && len(occ.EnclosingRange) >= 4 {
			defs = append(defs, methodDef{symbol: occ.Symbol, span: occ.EnclosingRange})
		}
	}

	// 引用 occurrence → 方法级调用
	for _, occ := range doc.Occurrences {
		if occ.SymbolRole&scipRoleDefinition != 0 {
			continue // 定义本身不是调用
		}
		_, calleeIsMethod, _ := classifySymbol(occ.Symbol)
		if !calleeIsMethod {
			continue // 类/模块/字段/参数引用、import、类型注解等不算调用
		}
		line := 0
		if len(occ.Range) >= 1 {
			line = occ.Range[0]
		}
		caller := ""
		for _, d := range defs {
			if len(d.span) >= 4 && line >= d.span[0] && line <= d.span[2] {
				caller = d.symbol
				break
			}
		}
		if caller == "" {
			continue // 引用不在任何方法体内（顶层/字段初始化等）——不伪造调用边
		}
		ir.Calls = append(ir.Calls, parser.CallIR{
			CallerFQN:  caller,
			CalleeFQN:  occ.Symbol,
			CallType:   "direct",
			LineNumber: line,
		})
	}

	return ir
}

// scipLangToCodeLang 将 SCIP 语言标识映射到内部语言标识。
func scipLangToCodeLang(scipLang string) string {
	switch scipLang {
	case "Go":
		return "go"
	case "Java":
		return "java"
	case "TypeScript", "JavaScript":
		return "ts"
	case "Python":
		return "py"
	case "Rust":
		return "rust"
	case "C++", "C":
		return "cpp"
	default:
		return strings.ToLower(scipLang)
	}
}
