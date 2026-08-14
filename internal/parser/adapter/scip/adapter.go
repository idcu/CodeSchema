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

	"github.com/idcu/codeschema/internal/errors"
	"github.com/idcu/codeschema/internal/parser"
)

// SCIPIndex 表示 SCIP index 文件的结构。
type SCIPIndex struct {
	Metadata  *SCIPMetadata   `json:"metadata"`
	Documents []*SCIPDocument `json:"documents"`
}

// SCIPMetadata 表示 SCIP index 元数据。
type SCIPMetadata struct {
	ToolInfo             *SCIPToolInfo `json:"tool_info"`
	ProjectRoot          string        `json:"project_root"`
	TextDocumentEncoding string        `json:"text_document_encoding"`
}

// SCIPToolInfo 表示 SCIP 工具信息。
type SCIPToolInfo struct {
	Name    string `json:"name"`
	Version string `json:"version"`
}

// SCIPDocument 表示 SCIP index 中的一个文档。
type SCIPDocument struct {
	RelativePath string            `json:"relative_path"`
	Language     string            `json:"language"`
	Symbols      []*SCIPSymbol     `json:"symbols"`
	Occurrences  []*SCIPOccurrence `json:"occurrences"`
}

// SCIPSymbol 表示 SCIP 中的符号定义。
type SCIPSymbol struct {
	ID              string `json:"id"`
	Name            string `json:"name"`
	Kind            int    `json:"kind"` // 0=class, 1=method, 2=field, ...
	PackageName     string `json:"package_name"`
	PackageVersion  string `json:"package_version"`
	EnclosingSymbol string `json:"enclosing_symbol"`
}

// SCIPOccurrence 表示 SCIP 中的符号引用（定义或引用位置）。
type SCIPOccurrence struct {
	Symbol     string `json:"symbol"`
	SymbolRole int    `json:"symbol_role"` // 0=definition, 1=reference, 2=forward_decl
	Range      []int  `json:"range"`       // [startLine, startChar, endLine, endChar]
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

// loadIndex 流式扫描 indexDir 目录下的 .scip 文件并加载。
//
// 采用 json.Decoder 流式解析（不整体 os.ReadFile 全量载入内存）：
//   - 逐 .scip 文件打开后按顶层对象流式读取 documents 数组，逐文档增量载入；
//   - 受 a.maxDocs 背压上限约束，达到上限即停止解析并置 truncated=true，
//     避免超大 index 一次性驻留内存（大项目内存可控）；
//   - 任一文件格式错误返回错误（不静默跳过），由编排层决定降级。
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

		file, err := os.Open(filePath)
		if err != nil {
			return fmt.Errorf("cannot open SCIP index file %s: %w", filePath, err)
		}

		dec := json.NewDecoder(file)
		// 读取顶层对象
		tok, err := dec.Token()
		if err != nil {
			file.Close()
			return fmt.Errorf("cannot parse SCIP index file %s: %w", filePath, err)
		}
		if d, ok := tok.(json.Delim); !ok || d != '{' {
			file.Close()
			return fmt.Errorf("cannot parse SCIP index file %s: expected top-level object", filePath)
		}

		// 逐顶层字段流式读取，命中 documents 数组时逐文档增量载入
		for dec.More() {
			keyTok, err := dec.Token()
			if err != nil {
				file.Close()
				return fmt.Errorf("cannot parse SCIP index file %s: %w", filePath, err)
			}
			key, _ := keyTok.(string)
			if key != "documents" {
				// 非 documents 字段（metadata 等）整体跳过（ReadValue 流式丢弃）
				var raw json.RawMessage
				if err := dec.Decode(&raw); err != nil {
					file.Close()
					return fmt.Errorf("cannot parse SCIP index file %s: field %s: %w", filePath, key, err)
				}
				continue
			}

			// documents 数组：逐元素流式 Decode，支持背压
			arrTok, err := dec.Token()
			if err != nil {
				file.Close()
				return fmt.Errorf("cannot parse SCIP index file %s: documents: %w", filePath, err)
			}
			if d, ok := arrTok.(json.Delim); !ok || d != '[' {
				file.Close()
				return fmt.Errorf("cannot parse SCIP index file %s: documents must be an array", filePath)
			}

			for dec.More() {
				if a.maxDocs > 0 && len(a.documents) >= a.maxDocs {
					a.truncated = true
					// 达到背压上限：停止读取该文件（文件由 defer 关闭）
					goto done
				}
				var doc SCIPDocument
				if err := dec.Decode(&doc); err != nil {
					file.Close()
					return fmt.Errorf("cannot parse SCIP index file %s: document: %w", filePath, err)
				}
				if doc.RelativePath != "" {
					doc.RelativePath = filepath.ToSlash(doc.RelativePath)
				}
				a.documents = append(a.documents, &doc)
			}
			// 数组闭合 token 消耗
			_, _ = dec.Token()
		}
	done:
		file.Close()
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
					Name:      sym.Name,
					FullName:  sym.ID,
					Type:      "CLASS",
					StartLine: startLine,
					StartCol:  startCol,
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
			CallerFQN:  doc.RelativePath,
			CalleeFQN:  symID,
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
