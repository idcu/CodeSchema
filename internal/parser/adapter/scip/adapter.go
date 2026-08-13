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
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"codeschema/internal/errors"
	"codeschema/internal/parser"
	"codeschema/internal/parser/adapter"
)

// SCIPIndex 表示 SCIP index 文件的结构。
type SCIPIndex struct {
	Metadata  *SCIPMetadata  `json:"metadata"`
	Documents []*SCIPDocument `json:"documents"`
}

// SCIPMetadata 表示 SCIP index 元数据。
type SCIPMetadata struct {
	ToolInfo  *SCIPToolInfo  `json:"tool_info"`
	ProjectRoot string        `json:"project_root"`
	TextDocumentEncoding string `json:"text_document_encoding"`
}

// SCIPToolInfo 表示 SCIP 工具信息。
type SCIPToolInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// SCIPDocument 表示 SCIP index 中的一个文档。
type SCIPDocument struct {
	RelativePath  string      `json:"relative_path"`
	Language      string      `json:"language"`
	Symbols       []*SCIPSymbol `json:"symbols"`
	Occurrences   []*SCIPOccurrence `json:"occurrences"`
}

// SCIPSymbol 表示 SCIP 中的符号定义。
type SCIPSymbol struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Kind        int      `json:"kind"` // 0=class, 1=method, 2=field, ...
	PackageName string   `json:"package_name"`
	PackageVersion string `json:"package_version"`
	EnclosingSymbol string `json:"enclosing_symbol"`
}

// SCIPOccurrence 表示 SCIP 中的符号引用（定义或引用位置）。
type SCIPOccurrence struct {
	Symbol      string   `json:"symbol"`
	SymbolRole  int      `json:"symbol_role"` // 0=definition, 1=reference, 2=forward_decl
	Range       []int    `json:"range"`       // [startLine, startChar, endLine, endChar]
}

// SCIPAdapter 读取 SCIP index 文件的适配器。
type SCIPAdapter struct {
	indexDir string
	documents []*SCIPDocument
	loaded    bool
}

// NewSCIPAdapter 创建 SCIP 适配器实例。
// indexDir 指向包含 SCIP index 文件（.scip）的目录。
func NewSCIPAdapter(indexDir string) *SCIPAdapter {
	return &SCIPAdapter{
		indexDir: indexDir,
	}
}

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
	if a.indexDir == "" || !adapter.FileExists(a.indexDir) {
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

// loadIndex 扫描 indexDir 目录下的 .scip 文件并加载。
func (a *SCIPAdapter) loadIndex() error {
	entries, err := os.ReadDir(a.indexDir)
	if err != nil {
		return fmt.Errorf("cannot read SCIP index directory %s: %w", a.indexDir, errors.ErrSourceUnavailable)
	}

	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".scip") {
			continue
		}
		filePath := filepath.Join(a.indexDir, entry.Name())
		data, err := os.ReadFile(filePath)
		if err != nil {
			return fmt.Errorf("cannot read SCIP index file %s: %w", filePath, err)
		}

		var index SCIPIndex
		if err := json.Unmarshal(data, &index); err != nil {
			return fmt.Errorf("cannot parse SCIP index file %s: %w", filePath, err)
		}

		// 合并文档列表
		for _, doc := range index.Documents {
			if doc.RelativePath != "" {
				// 标准化路径
				doc.RelativePath = filepath.ToSlash(doc.RelativePath)
			}
		}
		a.documents = append(a.documents, index.Documents...)
	}

	a.loaded = true
	return nil
}

// convertDocument 将 SCIP 文档转换为 IRDocument。
func (a *SCIPAdapter) convertDocument(doc *SCIPDocument) *parser.IRDocument {
	ir := &parser.IRDocument{
		Source:   "scip",
		Language: scipLangToCodeLang(doc.Language),
		FilePath: doc.RelativePath,
	}

	// 构建符号映射（symbolID -> SCIPSymbol）
	symMap := make(map[string]*SCIPSymbol)
	for _, sym := range doc.Symbols {
		symMap[sym.ID] = sym
	}

	// 构建符号名到定义行的映射
	typeDefs := make(map[string]struct {
		SCIPSymbol
		startLine int
		startCol  int
	})

	for _, occ := range doc.Occurrences {
		if occ.SymbolRole == 0 { // definition
			sym, ok := symMap[occ.Symbol]
			if !ok {
				continue
			}

			startLine, startCol := 0, 0
			if len(occ.Range) >= 4 {
				startLine = occ.Range[0]
				startCol = occ.Range[1]
			}

			switch sym.Kind {
			case 0: // class/interface
				ir.Classes = append(ir.Classes, parser.ClassIR{
					Name:       sym.Name,
					FullName:   sym.ID,
					Type:       "CLASS",
					StartLine:  startLine,
					StartCol:   startCol,
				})
			case 1: // method/function
				ir.Methods = append(ir.Methods, parser.MethodIR{
					Name:      sym.Name,
					ClassFQN:  sym.EnclosingSymbol,
					StartLine: startLine,
					StartCol:  startCol,
				})
			}

			typeDefs[occ.Symbol] = struct {
				SCIPSymbol
				startLine int
				startCol  int
			}{SCIPSymbol: *sym, startLine: startLine, startCol: startCol}
		}
	}

	// 提取引用关系（仅当符号同时有定义和引用时）
	refMap := make(map[string]int) // symbolID -> 引用行号
	for _, occ := range doc.Occurrences {
		if occ.SymbolRole == 1 { // reference
			if _, hasDef := typeDefs[occ.Symbol]; hasDef {
				line := 0
				if len(occ.Range) >= 4 {
					line = occ.Range[0]
				}
				refMap[occ.Symbol] = line
			}
		}
	}

	for symID, line := range refMap {
		if _, ok := symMap[symID]; !ok {
			continue
		}
		ir.Calls = append(ir.Calls, parser.CallIR{
			CallerFQN: doc.RelativePath,
			CalleeFQN: symID,
			CallType:  "direct",
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