// Package treesitter 提供基于文本模式匹配的源码解析适配器。
//
// P0 阶段使用纯 Go 正则表达式实现代码解析，无需 CGO 或 tree-sitter 运行时。
// 支持 6 种语言（Go/Java/TypeScript/Python/Rust/C++）的类/方法/调用解析。
// P1 阶段可切换为 tree-sitter Go binding 以获得精确语法解析。
package treesitter

import (
	"bufio"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/parser/adapter"
)

// langPatterns 每种语言的解析模式。
type langPatterns struct {
	classPattern   *regexp.Regexp // 匹配类/结构体/接口定义
	classNameIndex int           // 类名在 classPattern 匹配结果中的索引
	methodPattern  *regexp.Regexp // 匹配方法/函数定义
	callPattern    *regexp.Regexp // 匹配函数调用
	commentTrim    string         // 注释前缀，用于提取文档
}

// TreeSitterAdapter 基于文本模式匹配的解析适配器。
type TreeSitterAdapter struct {
	patterns map[string]langPatterns
}

// NewTreeSitterAdapter 创建适配器实例并初始化所有语言模式。
func NewTreeSitterAdapter() *TreeSitterAdapter {
	return &TreeSitterAdapter{
		patterns: initPatterns(),
	}
}

// initPatterns 初始化各语言的正则模式。
func initPatterns() map[string]langPatterns {
	return map[string]langPatterns{
		"go": {
			classPattern:   regexp.MustCompile(`^(type\s+(\w+)\s+(struct|interface))\s*\{`),
			classNameIndex: 2,
			methodPattern:  regexp.MustCompile(`^func\s+(\([^)]*\)\s*)?(\w[\w.]*)\(`),
			callPattern:    regexp.MustCompile(`(\w[\w.]*)\([^)]*\)`),
			commentTrim:    "//",
		},
		"java": {
			classPattern:   regexp.MustCompile(`^(public\s+|private\s+|protected\s+)?(abstract\s+|final\s+)?(class|interface|enum|@interface|record)\s+(\w+)`),
			classNameIndex: 4,
			methodPattern:  regexp.MustCompile(`(public|private|protected|static|final|abstract|synchronized|\s)+\s+(\w[\w<>[\],\s]*)\s+(\w[\w]*)\s*\(`),
			callPattern:    regexp.MustCompile(`(\w[\w.]*)\s*\([^)]*\)`),
			commentTrim:    "//",
		},
		"ts": {
			classPattern:   regexp.MustCompile(`^(export\s+)?(abstract\s+)?(class|interface|enum|type)\s+(\w+)`),
			classNameIndex: 4,
			methodPattern:  regexp.MustCompile(`^\s*(public|private|protected|static|async|\s)*\s*(\w[\w]*)\s*\([^)]*\)\s*:`),
			callPattern:    regexp.MustCompile(`(\w[\w.]*)\s*\([^)]*\)`),
			commentTrim:    "//",
		},
		"py": {
			classPattern:   regexp.MustCompile(`^class\s+(\w+)\s*[\(:]`),
			classNameIndex: 1,
			methodPattern:  regexp.MustCompile(`^\s*def\s+(\w[\w]*)\s*\(`),
			callPattern:    regexp.MustCompile(`(\w[\w.]*)\s*\([^)]*\)`),
			commentTrim:    "#",
		},
		"rust": {
			classPattern:   regexp.MustCompile(`^(pub\s+)?(struct|enum|trait|impl)\s+(\w+)`),
			classNameIndex: 3,
			methodPattern:  regexp.MustCompile(`^(pub\s+|fn\s+)?(unsafe\s+)?fn\s+(\w[\w]*)\s*\(`),
			callPattern:    regexp.MustCompile(`(\w[\w!]*)\s*\([^)]*\)`),
			commentTrim:    "//",
		},
		"cpp": {
			classPattern:   regexp.MustCompile(`^(class|struct|enum)\s+(\w+)\s*[:\{]`),
			classNameIndex: 2,
			methodPattern:  regexp.MustCompile(`(\w[\w<>*&:\s]+)\s+(\w[\w]*)\s*\([^)]*\)\s*(const|override|final|\s)*\s*\{`),
			callPattern:    regexp.MustCompile(`(\w[\w.]*)\s*\([^)]*\)`),
			commentTrim:    "//",
		},
	}
}

// Name 返回适配器唯一标识。
func (a *TreeSitterAdapter) Name() string { return "treesitter" }

// Supports 判断是否支持指定语言。
func (a *TreeSitterAdapter) Supports(lang string) bool {
	_, ok := a.patterns[lang]
	return ok
}

// Init 初始化适配器（P0 无额外初始化）。
func (a *TreeSitterAdapter) Init(ctx context.Context, config map[string]any) error {
	return nil
}

// Close 清理适配器资源（P0 无资源需要清理）。
func (a *TreeSitterAdapter) Close() error {
	return nil
}

// Parse 解析单个源文件，返回归一化 IR。
// 支持 Go/Java/TypeScript/Python/Rust/C++ 六种语言。
// 无法识别的文件扩展名返回空 IR（非错误）。
func (a *TreeSitterAdapter) Parse(ctx context.Context, path string) (*parser.IRDocument, error) {
	ext := strings.ToLower(filepath.Ext(path))
	patterns, ok := a.patterns[adapter.ExtToLang(ext)]
	if !ok {
		// 不支持的扩展名返回空 IR
		return &parser.IRDocument{Source: "treesitter", FilePath: path}, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	lang := adapter.ExtToLang(ext)
	doc := &parser.IRDocument{
		Source:   "treesitter",
		Language: lang,
		FilePath: path,
	}

	scanner := bufio.NewScanner(file)
	lineNum := 0
	var currentClass *parser.ClassIR
	var docComment strings.Builder

	for scanner.Scan() {
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		line := scanner.Text()
		lineNum++

		// 收集文档注释
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, patterns.commentTrim) {
			if docComment.Len() > 0 {
				docComment.WriteString("\n")
			}
			docComment.WriteString(strings.TrimPrefix(trimmed, patterns.commentTrim))
			continue
		}
		if trimmed == "" && docComment.Len() > 0 {
			// 空行分隔，保留文档注释
			continue
		}

		// 解析类定义
		if matches := patterns.classPattern.FindStringSubmatch(line); len(matches) > patterns.classNameIndex {
			className := matches[patterns.classNameIndex]
			classType := detectClassType(matches, lang)
			class := parser.ClassIR{
				Name:      className,
				FullName:  className,
				Type:      classType,
				StartLine: lineNum,
				EndLine:   lineNum,
				Doc:       strings.TrimSpace(docComment.String()),
			}
			doc.Classes = append(doc.Classes, class)
			currentClass = &doc.Classes[len(doc.Classes)-1]
			docComment.Reset()
			continue
		}

		// 解析方法/函数定义
		if matches := patterns.methodPattern.FindStringSubmatch(line); len(matches) >= 2 {
			methodName := matches[len(matches)-1]
			method := parser.MethodIR{
				Name:      methodName,
				Signature: strings.TrimSpace(trimmed),
				StartLine: lineNum,
				EndLine:   lineNum,
				Doc:       strings.TrimSpace(docComment.String()),
			}
			// 设置 ClassFQN
			if currentClass != nil {
				method.ClassFQN = currentClass.FullName
			}
			doc.Methods = append(doc.Methods, method)
			docComment.Reset()
			continue
		}

		// 解析函数调用（仅当行包含调用）
		if doc.Language == "go" || doc.Language == "py" {
			// Go/Python 调用检测
			if strings.Contains(trimmed, "(") && !strings.HasPrefix(trimmed, "//") && !strings.HasPrefix(trimmed, "#") {
				detectCalls(trimmed, lineNum, &doc.Calls, patterns.callPattern)
			}
		}

		// 非注释行清空文档注释缓冲区
		if !strings.HasPrefix(trimmed, patterns.commentTrim) && trimmed != "" {
			docComment.Reset()
		}
	}

	if err := scanner.Err(); err != nil {
		return nil, fmt.Errorf("scan file: %w", err)
	}

	// 更新类结束行号
	if len(doc.Classes) > 0 {
		doc.Classes[len(doc.Classes)-1].EndLine = lineNum
	}

	return doc, nil
}

// detectClassType 根据语言和匹配结果推断类类型。
func detectClassType(matches []string, lang string) string {
	switch lang {
	case "go":
		// type X struct/interface
		for _, m := range matches {
			if m == "interface" {
				return "INTERFACE"
			}
		}
		return "CLASS"
	case "java":
		for _, m := range matches {
			switch m {
			case "interface":
				return "INTERFACE"
			case "enum":
				return "ENUM"
			case "@interface":
				return "INTERFACE"
			case "record":
				return "CLASS"
			}
		}
		return "CLASS"
	case "ts":
		for _, m := range matches {
			switch m {
			case "interface":
				return "INTERFACE"
			case "enum":
				return "ENUM"
			case "type":
				return "TYPE"
			}
		}
		return "CLASS"
	case "rust":
		for _, m := range matches {
			switch m {
			case "struct":
				return "CLASS"
			case "enum":
				return "ENUM"
			case "trait":
				return "INTERFACE"
			case "impl":
				return "CLASS"
			}
		}
		return "CLASS"
	case "cpp":
		for _, m := range matches {
			if m == "struct" {
				return "CLASS"
			}
			if m == "enum" {
				return "ENUM"
			}
		}
		return "CLASS"
	default:
		return "CLASS"
	}
}

// detectCalls 从行文本中提取函数调用。
func detectCalls(line string, lineNum int, calls *[]parser.CallIR, pattern *regexp.Regexp) {
	matches := pattern.FindAllStringSubmatch(line, -1)
	for _, m := range matches {
		if len(m) >= 2 {
			callee := m[1]
			// 跳过关键字和常见误匹配
			if isKeyword(callee) || len(callee) <= 1 {
				continue
			}
			*calls = append(*calls, parser.CallIR{
				CalleeFQN:  callee,
				CallType:   "direct",
				LineNumber: lineNum,
			})
		}
	}
}

// isKeyword 判断是否为语言关键字，避免误匹配。
func isKeyword(name string) bool {
	keywords := map[string]bool{
		"if": true, "for": true, "while": true, "switch": true,
		"return": true, "catch": true, "throw": true, "new": true,
		"delete": true, "typeof": true, "instanceof": true, "import": true,
		"print": true, "len": true, "cap": true, "make": true,
		"append": true, "copy": true, "close": true, "panic": true,
		"recover": true, "range": true, "defer": true, "go": true,
		"assert": true, "raise": true, "yield": true, "await": true,
		"async": true, "super": true, "this": true, "self": true,
	}
	return keywords[name]
}