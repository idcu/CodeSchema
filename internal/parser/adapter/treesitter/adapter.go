//go:build !treesitter

// Package treesitter 提供基于文本模式匹配的源码解析适配器。
//
// 默认实现（本文件）：纯 Go 正则表达式实现代码解析，无需 CGO 或 tree-sitter 运行时。
// 支持 7 种语言（Go/Java/TypeScript/Python/Rust/C++/Kotlin）的类/方法/调用解析。
//
// 可选实现（真语法树）：以 `go build -tags treesitter` 编译时启用
// `adapter_ast.go`（基于 CGO tree-sitter binding，语法级精确解析），本文件被 build tag 排除。
// 二者共享同一 TreeSitterAdapter 类型与 IR 契约：默认路径免 CGO（与 T0-2 决策一致），
// 需要精度时一键切换（方案 C：analysis/2026-08-14-t2-1-parser-precision-eval.md）。
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
	classNameIndex int            // 类名在 classPattern 匹配结果中的索引
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
			// 捕获组含 . 以支持 obj.method() 与 mod::fn() 的调用形式
			callPattern: regexp.MustCompile(`(\w[\w.:]*)\s*\([^)]*\)`),
			commentTrim: "//",
		},
		"cpp": {
			classPattern:   regexp.MustCompile(`^(class|struct|enum)\s+(\w+)\s*[:\{]`),
			classNameIndex: 2,
			methodPattern:  regexp.MustCompile(`(\w[\w<>*&:\s]+)\s+(\w[\w]*)\s*\([^)]*\)\s*(const|override|final|\s)*\s*\{`),
			callPattern:    regexp.MustCompile(`(\w[\w.]*)\s*\([^)]*\)`),
			commentTrim:    "//",
		},
		"c": {
			classPattern:   regexp.MustCompile(`^(typedef\s+(struct|enum|union)\s+)?(struct|enum|union)\s+(\w+)\s*\{`),
			classNameIndex: 4,
			methodPattern:  regexp.MustCompile(`^([\w\s\*]+)\s+(\w[\w]*)\s*\([^)]*\)\s*\{`),
			callPattern:    regexp.MustCompile(`(\w[\w.]*)\s*\([^)]*\)`),
			commentTrim:    "//",
		},
		"kotlin": {
			classPattern:   regexp.MustCompile(`^(public\s+|private\s+|internal\s+|protected\s+)?(data\s+|sealed\s+|abstract\s+)?(class|interface|enum class|object|data class)\s+(\w+)`),
			classNameIndex: 4,
			methodPattern:  regexp.MustCompile(`^\s*(public|private|internal|protected|override|suspend|inline|async|\s)*\s*fun\s+(\w[\w]*)\s*\(`),
			callPattern:    regexp.MustCompile(`(\w[\w.]*)\s*\([^)]*\)`),
			commentTrim:    "//",
		},
		"swift": {
			classPattern:   regexp.MustCompile(`^(public\s+|private\s+|internal\s+|fileprivate\s+)?(final\s+|open\s+)?(class|struct|enum|protocol|extension)\s+(\w+)`),
			classNameIndex: 4,
			methodPattern:  regexp.MustCompile(`^\s*(public|private|internal|fileprivate|static|final|override|mutating|\s)*\s*func\s+(\w[\w]*)\s*\(`),
			callPattern:    regexp.MustCompile(`(\w[\w.]*)\s*\([^)]*\)`),
			commentTrim:    "//",
		},
		"php": {
			classPattern:   regexp.MustCompile(`^(final\s+|abstract\s+)?(class|interface|trait|enum)\s+(\w+)`),
			classNameIndex: 3,
			methodPattern:  regexp.MustCompile(`^\s*(public|private|protected|static|final|abstract|function|\s)+\s*(function\s+)?(\w[\w]*)\s*\(`),
			// 支持 $obj->method(...) / $obj::method(...) / func(...) 形式；非捕获组消费前缀，m[1] 为方法名
			callPattern: regexp.MustCompile(`(?:\$?[\w]*\s*(?:->|::)\s*)?(\w[\w:]*)\s*\([^)]*\)`),
			commentTrim: "//",
		},
		"csharp": {
			classPattern:   regexp.MustCompile(`^(public\s+|private\s+|internal\s+|protected\s+)?(abstract\s+|sealed\s+|static\s+|partial\s+)?(class|interface|struct|enum|record)\s+(\w+)`),
			classNameIndex: 4,
			methodPattern:  regexp.MustCompile(`^\s*(public|private|internal|protected|static|virtual|override|async|partial|\s)+\s*[\w<>\[\],\s]*\s+(\w[\w]*)\s*\([^)]*\)\s*\{`),
			callPattern:    regexp.MustCompile(`(\w[\w.]*)\s*\([^)]*\)`),
			commentTrim:    "//",
		},
		"ruby": {
			classPattern:   regexp.MustCompile(`^\s*(class|module)\s+(\w[\w:]*)\s*(<|\s*$)`),
			classNameIndex: 2,
			methodPattern:  regexp.MustCompile(`^\s*(def\s+|def\s+self\.)\s*(\w[\w!?=]*)\s*\(`),
			callPattern:    regexp.MustCompile(`(\w[\w.!?]*)\s*\([^)]*\)`),
			commentTrim:    "#",
		},
		"bash": {
			classPattern:   regexp.MustCompile(`^(function\s+)?(\w+)\s*\(\)\s*\{`),
			classNameIndex: 2,
			methodPattern:  regexp.MustCompile(`^(function\s+)?(\w+)\s*\(\)\s*\{`),
			// 行首命令名（无参数命令 build_app / 带参数命令 git commit）
			callPattern: regexp.MustCompile(`^\s*(\w[\w-]*)\s*($|[^=])`),
			commentTrim: "#",
		},
		"scala": {
			classPattern:   regexp.MustCompile(`^(case\s+|abstract\s+|final\s+|sealed\s+)?(class|trait|object|enum)\s+(\w+)`),
			classNameIndex: 3,
			methodPattern:  regexp.MustCompile(`^\s*(private|protected|final|override|implicit|def|\s)*\s*def\s+(\w[\w]*)\s*\(`),
			callPattern:    regexp.MustCompile(`(\w[\w.]*)\s*\([^)]*\)`),
			commentTrim:    "//",
		},
		"sql": {
			// SQL 无类；CREATE TABLE/VIEW/PROCEDURE 作为「声明」登记
			classPattern:   regexp.MustCompile(`(?i)^\s*CREATE\s+(OR\s+REPLACE\s+)?(TABLE|VIEW|FUNCTION|PROCEDURE)\s+([\w.]+)`),
			classNameIndex: 3,
			methodPattern:  regexp.MustCompile(`(?i)^\s*CREATE\s+(OR\s+REPLACE\s+)?(FUNCTION|PROCEDURE)\s+([\w.]+)\s*\(`),
			// CALL proc() / func(...) / schema.func(...) 形式；非捕获组消费 CALL，m[1] 为函数名
			callPattern: regexp.MustCompile(`(?i)\b(?:CALL\s+)?([\w.]+)\s*\([^)]*\)`),
			commentTrim: "--",
		},
		"elixir": {
			// defmodule 作为模块（MODULE），def/defp 为方法；elixir 无 class 概念
			classPattern:   regexp.MustCompile(`^\s*defmodule\s+([\w.]+)`),
			classNameIndex: 1,
			methodPattern:  regexp.MustCompile(`^\s*def(p)?\s+([\w!?]+)\s*(\(|do)`),
			callPattern:    regexp.MustCompile(`([\w.]+)\s*\([^)]*\)`),
			commentTrim:    "#",
		},
		"ocaml": {
			// module / class 声明；let ... = 为方法（OCaml 无 class 概念时 module 作模块）
			classPattern:   regexp.MustCompile(`^\s*(module|class)\s+([\w]+)`),
			classNameIndex: 2,
			methodPattern:  regexp.MustCompile(`^\s*(let|and)\s+(rec\s+)?([\w]+)\s+[\w\s]*=`),
			// OCaml 调用多为无括号应用（`validator.validate order`）；同时兼容括号形式。
			// 无括号分支要求函数名与参数间至少一个空格（避免 `end`/`in` 等单词自拆分）
			callPattern: regexp.MustCompile(`([\w.]+)\s*(?:\([^)]*\)|\s+[a-z_][\w']*(?:\s*;|\s*$|\s*\n))`),
			commentTrim: "(*",
		},
		"lua": {
			// Lua 无 class 概念；local function / function 定义为方法
			classPattern:   regexp.MustCompile(`^\s*local\s+[\w.]+\s*=\s*\{`),
			classNameIndex: 1,
			methodPattern:  regexp.MustCompile(`^\s*(local\s+)?function\s+([\w.:]+)\s*\(`),
			callPattern:    regexp.MustCompile(`([\w.:]+)\s*\([^)]*\)`),
			commentTrim:    "--",
		},
		"groovy": {
			// class / interface / trait 声明 + def 方法
			classPattern:   regexp.MustCompile(`^\s*(public|private|protected|final|abstract|@\w+\([^)]*\)\s*)*\s*(class|interface|trait|enum)\s+(\w+)`),
			classNameIndex: 3,
			methodPattern:  regexp.MustCompile(`^\s*(public|private|protected|static|final|synchronized|def|void|[\w.<>\[\]]+)\s+(def\s+)?(\w+)\s*\(`),
			callPattern:    regexp.MustCompile(`([\w.$]+)\s*\([^)]*\)`),
			commentTrim:    "//",
		},
		"css": {
			// CSS 无调用；选择器规则块作为「声明」登记（@media/.class/#id/元素选择器）
			classPattern:   regexp.MustCompile(`^\s*([.@#]?[\w-]+(?:\s*[>+~]\s*[\w.-]+|\s+[\w.-]+)*)\s*\{`),
			classNameIndex: 1,
			methodPattern:  regexp.MustCompile(`^\s*(@media|@supports|@keyframes|@font-face)\b`),
			callPattern:    regexp.MustCompile(`\b\B`), // CSS 无函数调用(永不匹配)
			commentTrim:    "/*",
		},
		"toml": {
			// TOML 无调用；table 段作为「声明」登记
			classPattern:   regexp.MustCompile(`^\s*\[\[?([\w.-]+)\]\]?\s*$`),
			classNameIndex: 1,
			methodPattern:  regexp.MustCompile(`^\s*[\w.-]+\s*=`),
			callPattern:    regexp.MustCompile(`\b\B`), // TOML 无函数调用(永不匹配)
			commentTrim:    "#",
		},
		"yaml": {
			// YAML 无调用；顶层 map 键作为「声明」登记
			classPattern:   regexp.MustCompile(`^([\w.-]+):\s*(?:\||>)?\s*$`),
			classNameIndex: 1,
			methodPattern:  regexp.MustCompile(`^\s{2,}[\w.-]+:`),
			callPattern:    regexp.MustCompile(`\b\B`), // YAML 无函数调用(永不匹配)
			commentTrim:    "#",
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
	var sanitizer codeSanitizer // 跨行状态（块注释 / 三引号字符串）

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

		// 解析函数调用（全部语言启用；Java/TS/Rust/C++/Kotlin 的调用检测
		// 依赖 callPattern 与 isKeyword 过滤，精度见 docs/dev 02 的启发式说明）
		// 先剔除字符串/注释内的伪调用（跨行状态机），再匹配
		// bash/ocaml 调用可无括号（命令名 / 无括号应用），其余语言调用需含 "("
		code := sanitizer.clean(trimmed, lang)
		if lang == "bash" || lang == "ocaml" || strings.Contains(code, "(") {
			detectCalls(code, lineNum, &doc.Calls, patterns.callPattern)
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
	case "c":
		for _, m := range matches {
			if m == "enum" {
				return "ENUM"
			}
			if m == "union" {
				return "CLASS"
			}
		}
		return "CLASS"
	case "kotlin":
		for _, m := range matches {
			switch m {
			case "interface":
				return "INTERFACE"
			case "enum", "enum class":
				return "ENUM"
			case "object":
				return "OBJECT"
			}
		}
		return "CLASS"
	case "swift":
		for _, m := range matches {
			switch m {
			case "protocol":
				return "INTERFACE"
			case "enum":
				return "ENUM"
			case "extension":
				return "CLASS"
			}
		}
		return "CLASS"
	case "php":
		for _, m := range matches {
			switch m {
			case "interface":
				return "INTERFACE"
			case "trait":
				return "CLASS"
			case "enum":
				return "ENUM"
			}
		}
		return "CLASS"
	case "csharp":
		for _, m := range matches {
			switch m {
			case "interface":
				return "INTERFACE"
			case "enum":
				return "ENUM"
			case "struct":
				return "CLASS"
			}
		}
		return "CLASS"
	case "ruby":
		for _, m := range matches {
			if m == "module" {
				return "MODULE"
			}
		}
		return "CLASS"
	case "bash":
		return "FUNCTION"
	case "scala":
		for _, m := range matches {
			switch m {
			case "trait":
				return "INTERFACE"
			case "enum":
				return "ENUM"
			case "object":
				return "OBJECT"
			}
		}
		return "CLASS"
	case "sql":
		for _, m := range matches {
			switch m {
			case "VIEW":
				return "VIEW"
			case "FUNCTION":
				return "FUNCTION"
			case "PROCEDURE":
				return "PROCEDURE"
			}
		}
		return "TABLE"
	case "elixir":
		return "MODULE"
	case "ocaml":
		for _, m := range matches {
			if m == "class" {
				return "CLASS"
			}
		}
		return "MODULE"
	case "lua":
		return "CLASS"
	case "groovy":
		for _, m := range matches {
			switch m {
			case "interface":
				return "INTERFACE"
			case "trait":
				return "INTERFACE"
			case "enum":
				return "ENUM"
			}
		}
		return "CLASS"
	default:
		return "CLASS"
	}
}

// codeSanitizer 剔除代码行中字符串/注释内的伪调用（跨行状态机）。
//
// 目标：`msg := "foo(bar)"` / `// foo(bar)` 等行内的括号不应被误认为函数调用。
// 状态机维护跨行状态：
//   - inBlockComment：C 风格 /* ... */ 注释（可跨多行）
//   - inTripleQuote：Python 三引号字符串 ”'...”' / """..."""（可跨多行）
//
// 剔除策略：字符串与注释内容替换为空格（保持字符位置，避免影响正则的行内列匹配），
// 行注释（// 与 #）之后的全部内容替换为空格。
type codeSanitizer struct {
	inBlockComment bool
	inTripleQuote  string // 三引号定界符（"""/'''），空表示不在三引号内
}

// clean 返回剔除字符串/注释后的代码行（仅保留真实代码），并更新跨行状态。
func (c *codeSanitizer) clean(line, lang string) string {
	if line == "" {
		return line
	}
	out := []byte(line)
	quote := byte(0) // 当前行内字符串定界符（0 = 不在字符串内）
	escaped := false // 上一个字符是转义符（\\）
	i := 0
	for i < len(line) {
		ch := line[i]

		// 块注释状态（跨行）
		if c.inBlockComment {
			if ch == '*' && i+1 < len(line) && line[i+1] == '/' {
				out[i], out[i+1] = ' ', ' '
				c.inBlockComment = false
				i += 2
				continue
			}
			out[i] = ' '
			i++
			continue
		}

		// 三引号字符串状态（跨行，Python）
		if c.inTripleQuote != "" {
			if strings.HasPrefix(line[i:], c.inTripleQuote) {
				delimLen := len(c.inTripleQuote)
				for j := 0; j < delimLen; j++ {
					out[i+j] = ' '
				}
				c.inTripleQuote = ""
				i += delimLen
				continue
			}
			out[i] = ' '
			i++
			continue
		}

		// 行内字符串状态
		if quote != 0 {
			if escaped {
				escaped = false
			} else if ch == '\\' {
				escaped = true
			} else if ch == quote {
				quote = 0
			}
			out[i] = ' '
			i++
			continue
		}

		// 行注释：// 与 Python/Ruby 的 #
		if ch == '/' && i+1 < len(line) && line[i+1] == '/' {
			for j := i; j < len(line); j++ {
				out[j] = ' '
			}
			break
		}
		if (lang == "py" || lang == "ruby") && ch == '#' {
			for j := i; j < len(line); j++ {
				out[j] = ' '
			}
			break
		}

		// 进入块注释
		if ch == '/' && i+1 < len(line) && line[i+1] == '*' {
			c.inBlockComment = true
			out[i], out[i+1] = ' ', ' '
			i += 2
			continue
		}

		// 进入三引号（Python）
		if lang == "py" && (ch == '"' || ch == '\'') &&
			i+2 < len(line) && line[i+1] == ch && line[i+2] == ch {
			c.inTripleQuote = string(ch) + string(ch) + string(ch)
			out[i], out[i+1], out[i+2] = ' ', ' ', ' '
			i += 3
			continue
		}

		// 进入行内字符串
		if ch == '"' || ch == '\'' {
			quote = ch
			out[i] = ' '
			i++
			continue
		}

		i++
	}
	return string(out)
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
		// Kotlin 关键字
		"fun": true, "val": true, "var": true, "when": true, "is": true,
		"in": true, "suspend": true, "require": true,
		"check": true, "error": true, "TODO": true, "println": true,
		// Rust
		"fn": true, "let": true, "mut": true, "match": true, "impl": true,
		"trait": true, "struct": true, "enum": true, "use": true, "mod": true,
		// C/C++
		"sizeof": true, "static_cast": true, "dynamic_cast": true,
		"reinterpret_cast": true, "const_cast": true, "printf": true,
		// Swift
		"func": true, "guard": true, "repeat": true, "where": true,
		"associatedtype": true, "fatalError": true,
		// PHP
		"echo": true, "print_r": true, "var_dump": true, "isset": true,
		"empty": true, "unset": true, "die": true, "exit": true,
		// Ruby
		"puts": true, "require_relative": true,
		"attr_accessor": true, "attr_reader": true, "attr_writer": true, "loop": true,
		// Bash
		"cd": true, "source": true, "local": true, "export": true, "eval": true,
		// Scala（println/require/assert 已在通用段）
		// Elixir（IO.puts/IO.inspect/Enum 等标准库函数调用为伪调用）
		"IO.puts": true, "IO.inspect": true, "IO.gets": true,
		"Enum.map": true, "Enum.filter": true, "Enum.reduce": true, "Enum.each": true,
		"Map.get": true, "Map.put": true, "String.length": true,
		// OCaml（print_endline/List.map/Printf 等标准库 + 语法关键字）
		"print_endline": true, "print_string": true, "print_int": true,
		"List.map": true, "List.filter": true, "List.fold_left": true, "List.iter": true,
		"Printf.printf": true, "failwith": true, "rec": true, "done": true,
		// Lua（标准库；print/require/assert/error/type/pairs/ipairs 已在通用段）
		"pcall":    true,
		"tostring": true, "tonumber": true, "rawget": true, "rawset": true,
		"string.format": true, "string.len": true, "table.insert": true,
		"table.remove": true, "math.floor": true, "math.max": true,
		// Groovy（println/assert/printf 已在通用段；groovy 特有）
		"sprintf": true, "size": true, "each": true, "collect": true,
		"findAll": true, "inject": true,
	}
	return keywords[name]
}
