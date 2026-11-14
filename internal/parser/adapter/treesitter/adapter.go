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
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/parser/adapter"
)

// maxParseLineBytes 单行内容解析上限（4MiB）。超限行跳过解析但继续文件，
// 避免生成代码/压缩产物的超长单行中断整次扫描（P1-15）。
const maxParseLineBytes = 4 << 20

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

// BuildVariant 返回当前构建的解析实现标识（供基准报告区分正则/真语法树）。
func BuildVariant() string { return "regex" }

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
			// 支持泛型类型参数：type Watcher[T any] struct { ... }（T1-2 真实缺陷：泛型类未识别）
			classPattern:   regexp.MustCompile(`^(type\s+(\w+)(?:\[[^\]]*\])?\s+(struct|interface))\s*\{`),
			classNameIndex: 2,
			methodPattern:  regexp.MustCompile(`^func\s+(\([^)]*\)\s*)?(\w[\w.]*)(?:\[[^\]]*\])?\(`),
			callPattern:    regexp.MustCompile(`(\w[\w.]*)\([^)]*\)`),
			commentTrim:    "//",
		},
		"java": {
			classPattern:   regexp.MustCompile(`^(public\s+|private\s+|protected\s+)?(abstract\s+|final\s+)?(class|interface|enum|@interface|record)\s+(\w+)`),
			classNameIndex: 4,
			// 行首锚定 + 修饰符可省略：JUnit 5 测试方法常为包级可见（`void foo()`），
			// 且方法体常以单个 tab 缩进——旧模式要求「修饰符/≥2 个空白 + 空白 + 类型」，
			// 单 tab 行会因修饰符组吃掉唯一空白而漏解析。类型组允许泛型中的空格。
			methodPattern: regexp.MustCompile(`^\s*(?:(?:public|private|protected|static|final|abstract|synchronized|native|default|strictfp)\s+)*([\w<>\[\],.?][\w<>\[\],.?\s]*?)\s+(\w[\w$]*)\s*\(`),
			callPattern:   regexp.MustCompile(`(\w[\w.]*)\s*\([^)]*\)`),
			commentTrim:   "//",
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
		"protobuf": {
			// service 作为「类」，rpc 作为「方法」
			classPattern:   regexp.MustCompile(`^\s*(message|service|enum)\s+(\w+)`),
			classNameIndex: 2,
			methodPattern:  regexp.MustCompile(`^\s*rpc\s+(\w+)\s*\(`),
			callPattern:    regexp.MustCompile(`\b\B`), // proto 无函数调用(永不匹配)
			commentTrim:    "//",
		},
		"html": {
			// 元素标签作为「类」登记
			classPattern:   regexp.MustCompile(`^\s*<([a-zA-Z][\w-]*)(?:\s[^>]*)?>`),
			classNameIndex: 1,
			methodPattern:  regexp.MustCompile(`^\s*<script\b`),
			callPattern:    regexp.MustCompile(`\b\B`), // HTML 标签非调用(永不匹配)
			commentTrim:    "<!--",
		},
		"hcl": {
			// Terraform 资源/变量/模块块作为「类」登记
			classPattern:   regexp.MustCompile(`^\s*(resource|data|variable|module|output|provider|locals|terraform)\s+([\w."-]+)`),
			classNameIndex: 2,
			methodPattern:  regexp.MustCompile(`^\s*[\w-]+\s*=\s*\{`),
			callPattern:    regexp.MustCompile(`\b\B`), // HCL 配置无函数调用(永不匹配)
			commentTrim:    "#",
		},
		"svelte": {
			// Svelte 组件：script 块作为「类」登记
			classPattern:   regexp.MustCompile(`^\s*<script(\s+[^>]*)?>`),
			classNameIndex: 1,
			methodPattern:  regexp.MustCompile(`^\s*(export\s+|async\s+)?function\s+(\w+)`),
			callPattern:    regexp.MustCompile(`([\w.]+)\s*\([^)]*\)`),
			commentTrim:    "<!--",
		},
		"markdown": {
			// 标题/代码块作为「声明」登记（文档语言，无类/调用语义）
			classPattern:   regexp.MustCompile(`^#{1,6}\s+(.+)`),
			classNameIndex: 1,
			methodPattern:  regexp.MustCompile(`\b\B`), // Markdown 无方法(永不匹配),
			callPattern:    regexp.MustCompile(`\b\B`), // Markdown 无函数调用(永不匹配)
			commentTrim:    "<!--",
		},
		"dockerfile": {
			// 构建指令作为「声明」登记（FROM/RUN/COPY 等）
			classPattern:   regexp.MustCompile(`^(FROM|STAGE)\s+([\w./:-]+)`),
			classNameIndex: 2,
			methodPattern:  regexp.MustCompile(`^(RUN|COPY|ADD|CMD|ENTRYPOINT|ENV|LABEL|WORKDIR|EXPOSE)\b`),
			callPattern:    regexp.MustCompile(`\b\B`), // Dockerfile 指令非调用(永不匹配)
			commentTrim:    "#",
		},
		"elm": {
			// module 声明 + 顶层函数
			classPattern:   regexp.MustCompile(`^\s*module\s+([\w.]+)`),
			classNameIndex: 1,
			methodPattern:  regexp.MustCompile(`^([\w.]+)\s*:\s*[\w\s->]+$`),
			callPattern:    regexp.MustCompile(`([\w.]+)\s+[\w]+`),
			commentTrim:    "--",
		},
		"cue": {
			// package/import + 结构定义作为「声明」登记
			classPattern:   regexp.MustCompile(`^(package|import)\s+([\w./]+)`),
			classNameIndex: 2,
			methodPattern:  regexp.MustCompile(`^[\w#]+\s*:\s*[\w{}"#]+`),
			callPattern:    regexp.MustCompile(`\b\B`), // CUE 配置无函数调用(永不匹配)
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
// parseLang 识别文件语言：扩展名优先，Dockerfile 按文件名识别。
func (a *TreeSitterAdapter) parseLang(path string) string {
	lang := adapter.ExtToLang(strings.ToLower(filepath.Ext(path)))
	if lang == "unknown" {
		if base := filepath.Base(path); base == "Dockerfile" || strings.HasPrefix(base, "Dockerfile.") {
			return "dockerfile"
		}
	}
	return lang
}

func (a *TreeSitterAdapter) Parse(ctx context.Context, path string) (*parser.IRDocument, error) {
	lang := a.parseLang(path)
	patterns, ok := a.patterns[lang]
	if !ok {
		// 不支持的扩展名返回空 IR
		return &parser.IRDocument{Source: "treesitter", FilePath: path}, nil
	}

	file, err := os.Open(path)
	if err != nil {
		return nil, fmt.Errorf("open file: %w", err)
	}
	defer file.Close()

	doc := &parser.IRDocument{
		Source:   "treesitter",
		Language: lang,
		FilePath: path,
	}

	scanner := bufio.NewReaderSize(file, 64*1024)
	lineNum := 0
	var currentClass *parser.ClassIR
	var currentMethod *parser.MethodIR // 通用路径：当前方法作用域（用于回填 CallerFQN）
	var docComment strings.Builder
	var sanitizer codeSanitizer // 跨行状态（块注释 / 三引号字符串）

	// Go 专属结构跟踪：花括号净深度 + 当前方法作用域。
	// 用途（T1-2 真实缺陷修复）：调用归属 CallerFQN、receiver 决定 ClassFQN、
	// 顶层函数不再被误挂到最近 class。仅 Go 启用——其余语言走原启发式路径。
	goDepth := 0            // 已见 `{`/`}` 净深度（字符串/注释已剔除）
	curMethodIdx := -1      // doc.Methods 中当前所在方法索引（-1 = 不在任何方法内）
	methodOpenDepth := 0    // 方法体起始深度（方法定义行之前的 goDepth）
	methodBodyOpen := false // 方法体 `{` 是否已出现（多行签名续行识别用）
	goPkg := ""             // 当前 Go 文件 package 声明（调用图 FQN 包限定用）
	curRecvVar := ""        // 当前方法 receiver 变量名（自调用 callee 限定用）
	curRecvType := ""       // 当前方法 receiver 类型名
	nonGoPkg := ""          // 非 Go 语言包/命名空间声明（Java/Kotlin/C#），调用图 caller FQN 包限定用
	// 成员变量/常量提取状态（2026-09-03 新增）：struct 体跟踪 + const 块状态
	curStructIdx := -1       // 当前所在 struct 在 doc.Classes 的索引（-1 = 不在 struct 体内）
	curStructOpenDepth := 0  // struct 体起始深度（type X struct { 的 `{` 计入前）
	curStructName := ""      // 当前 struct 名（field 的 ClassFQN 归属）
	constBlock := false      // 是否处于 const ( ... ) 块内
	hasTopLevelFunc := false // 是否存在包级函数（决定是否合成包级容器）

	for {
		raw, readErr := scanner.ReadString('\n')
		if raw == "" && readErr != nil {
			if readErr == io.EOF {
				break
			}
			return nil, fmt.Errorf("scan file: %w", readErr)
		}

		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		default:
		}

		line := strings.TrimRight(raw, "\r\n")
		lineNum++

		// 超长单行（生成代码/压缩产物）：跳过本行内容解析但保留行号推进，
		// 不得因单行超限中断整个文件（P1-15，旧实现返回 ErrTooLong）。
		if len(line) > maxParseLineBytes {
			docComment.Reset()
			continue
		}

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

		// Go 走精确路径（brace 作用域 + receiver 归属）；其余语言走启发式。
		// Go 语法结构可靠（func/type 仅顶层、括号必然配平），可精确判定
		// 方法体边界与调用归属；其余语言（无括号结构/缩进敏感）保持原启发式。
		if lang == "go" {
			code := sanitizer.clean(trimmed, lang) // 剔字符串/注释，供 brace 计数与调用检测

			// 捕获 package 声明（调用图 FQN 包限定用）。
			if strings.HasPrefix(trimmed, "package ") {
				goPkg = strings.TrimSpace(strings.TrimPrefix(trimmed, "package "))
			}

			// 解析类定义（支持泛型 type X[T any] struct/interface）
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
				// struct 体跟踪（成员变量提取用）：classType==CLASS 即 `type X struct {`
				if classType == "CLASS" {
					curStructIdx = len(doc.Classes) - 1
					curStructName = className
					curStructOpenDepth = goDepth // 该行 `{` 计入前的深度
				}
				goDepth += braceNet(code)
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
				// receiver 存在 → 方法归属 receiver 类型；否则为顶层函数，
				// 归入合成包级容器（ClassFQN = package 名）。旧行为留空 ClassFQN，
				// 三后端按类匹配时会把整条方法静默丢弃（P0-1），
				// 导致 /context、检索、标签、测试关联对包级函数全部失效。
				if recvType := parseGoReceiver(matches[1]); recvType != "" {
					method.ClassFQN = recvType
					// 记录当前方法 receiver 变量名/类型，供 callee 自调用限定
					curRecvType = recvType
					curRecvVar = goRecvVar(matches[1])
				} else {
					curRecvVar = ""
					curRecvType = ""
					if goPkg != "" {
						method.ClassFQN = goPkg
						hasTopLevelFunc = true
					}
				}
				doc.Methods = append(doc.Methods, method)
				curMethodIdx = len(doc.Methods) - 1
				// 合法 Go 中 func 不可能出现在 const 块内；若出现说明上游闭合行丢失，
				// 在此复位，避免 constBlock 继续吞掉方法体（P3-9）。
				constBlock = false
				methodOpenDepth = goDepth // 定义行 `{` 尚未计入
				goDepth += braceNet(code)
				// 方法体是否在本行开启：多行签名（`func F(` 后续参数行）尚未出现 `{`，
				// 需要把后续续行视为签名而非方法体，否则首个续行就会被误判为方法闭合（P1-14）。
				methodBodyOpen = strings.Contains(code, "{")
				// 单行方法体（`func F() { ... }`）：本行即闭合，EndLine 回填到本行（P1-1）。
				if methodBodyOpen && goDepth <= methodOpenDepth {
					doc.Methods[curMethodIdx].EndLine = lineNum
					curMethodIdx = -1
					methodBodyOpen = false
					curRecvVar = ""
					curRecvType = ""
				}
				docComment.Reset()
				continue
			}

			// 多行签名续行：`{` 未出现前，本行属于参数/返回值声明，累积进 Signature，
			// 不计调用、不触发方法闭合；出现 `{` 的当行开启方法体。
			if curMethodIdx >= 0 && !methodBodyOpen {
				if t := strings.TrimSpace(line); t != "" {
					doc.Methods[curMethodIdx].Signature += " " + t
				}
				goDepth += braceNet(code)
				if strings.Contains(code, "{") {
					methodBodyOpen = true
					// 签名与完整方法体同处一行：本行即闭合。
					if goDepth <= methodOpenDepth {
						doc.Methods[curMethodIdx].EndLine = lineNum
						curMethodIdx = -1
						methodBodyOpen = false
						curRecvVar = ""
						curRecvType = ""
					}
				}
				docComment.Reset()
				continue
			}

			// 常量（包级）：const X [Type] = v；const ( ... ) 块内行 X [Type] = v。
			// 仅方法外（curMethodIdx<0）产出为包级常量；方法内 const 忽略（局部常量不入库）。
			// 注意：条件须在 constBlock 激活时整体命中，否则闭合行 `)` 永远进不来，
			// constBlock 无法复位，会吞掉其后所有行（含 struct 字段行）。
			if strings.HasPrefix(trimmed, "const") || constBlock {
				switch {
				case trimmed == "const (" || trimmed == "const(":
					constBlock = true
				case strings.HasPrefix(trimmed, ")"):
					// 闭合行可能带行尾注释（`) // note`），按前缀识别，
					// 否则 constBlock 泄漏会吞掉其后所有方法体调用（P3-9）。
					constBlock = false
				case curMethodIdx < 0:
					if name, typ, val := parseGoConstLine(trimmed); name != "" {
						doc.Constants = append(doc.Constants, parser.ConstantIR{
							Name: name, Type: typ, Value: val,
							FilePath: path,
						})
					}
				}
				docComment.Reset()
				continue
			}

			// 成员变量：struct 体（type X struct { ... }）内的字段行。
			// 仅非方法体内（curMethodIdx<0）且深度处于 struct 体内时提取。
			if curStructIdx >= 0 && curMethodIdx < 0 && goDepth > curStructOpenDepth {
				if fname, ftype, ok := parseGoStructField(code); ok {
					doc.Fields = append(doc.Fields, parser.FieldIR{
						Name: fname, Type: ftype, ClassFQN: curStructName,
						StartLine: lineNum,
					})
				}
			}

			// 普通行：先判定是否仍在方法体内（基于进入本行前的深度），
			// 再更新深度；深度回落至 methodOpenDepth 即方法体闭合。
			caller := ""
			if curMethodIdx >= 0 && goDepth > methodOpenDepth {
				m := &doc.Methods[curMethodIdx]
				switch {
				case m.ClassFQN == "":
					caller = goPkg + "." + m.Name
				case m.ClassFQN == goPkg:
					// 包级容器下的顶层函数：FQN 为 pkg.Func，不得重复包前缀。
					caller = goPkg + "." + m.Name
				default:
					caller = goPkg + "." + m.ClassFQN + "." + m.Name
				}
			}
			if strings.Contains(code, "(") {
				detectCalls(code, lineNum, &doc.Calls, patterns.callPattern, caller, goPkg, curRecvVar, curRecvType, "", "")
			}
			goDepth += braceNet(code)
			if curMethodIdx >= 0 && goDepth <= methodOpenDepth {
				// 方法体闭合：EndLine 回填到闭合行（P1-1），支撑 /context 语义块裁剪。
				doc.Methods[curMethodIdx].EndLine = lineNum
				curMethodIdx = -1
				methodBodyOpen = false
				curRecvVar = "" // 重置 receiver 跟踪
				curRecvType = ""
			}
			// struct 体闭合检测（深度回落至起始深度）
			if curStructIdx >= 0 && goDepth <= curStructOpenDepth {
				curStructIdx = -1
				curStructName = ""
			}
			docComment.Reset()
			continue
		}

		// 捕获包/命名空间声明（Java/Kotlin/C#），供 caller FQN 包限定
		if p := parsePkgDecl(lang, trimmed); p != "" {
			nonGoPkg = p
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
			currentMethod = nil
			docComment.Reset()
			continue
		}

		// 解析方法/函数定义。
		// 行首语句关键字（return/new/if/...）开头的行不是方法声明：
		// Java/Groovy 的模式未加行首锚点且类型组过宽，会把 `return helper(a);`
		// 读成「返回类型 return + 方法名 helper」，既虚增方法数又让整行被 continue
		// 吞掉、丢失方法体内的调用边（S-2 基准真值为 0 的根因）。
		if matches := patterns.methodPattern.FindStringSubmatch(line); len(matches) >= 2 && !isStatementLine(trimmed) {
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
			currentMethod = &doc.Methods[len(doc.Methods)-1]
			docComment.Reset()
			continue
		}

		// 解析函数调用（全部语言启用；Java/TS/Rust/C++/Kotlin 的调用检测
		// 依赖 callPattern 与 isKeyword 过滤，精度见 docs/dev 02 的启发式说明）
		// 先剔除字符串/注释内的伪调用（跨行状态机），再匹配
		// bash/ocaml 调用可无括号（命令名 / 无括号应用），其余语言调用需含 "("
		code := sanitizer.clean(trimmed, lang)
		if lang == "bash" || lang == "ocaml" || strings.Contains(code, "(") {
			caller := ""
			if currentMethod != nil {
				if currentMethod.ClassFQN != "" {
					caller = currentMethod.ClassFQN + "." + currentMethod.Name
				} else {
					caller = currentMethod.Name
				}
				// 包/命名空间限定（Java/Kotlin/C# 等显式包声明）：pkg.Class.Method / pkg.func
				if nonGoPkg != "" {
					caller = nonGoPkg + "." + caller
				}
			}
			// 通用路径：传 nonGoPkg 与当前类名（Java/Kotlin/C# 显式包声明语言），
			// 使裸 callee（无点前缀）也包限定为 pkg.Class.Method，与 caller 命名空间对齐；
			// 无包信息语言（Python/TS/Rust/C++ 等）nonGoPkg 为空 → callee 保持裸名启发式。
			curClass := ""
			if currentClass != nil {
				curClass = currentClass.Name
			}
			detectCalls(code, lineNum, &doc.Calls, patterns.callPattern, caller, "", "", "", nonGoPkg, curClass)
		}

		// 非注释行清空文档注释缓冲区
		if !strings.HasPrefix(trimmed, patterns.commentTrim) && trimmed != "" {
			docComment.Reset()
		}
	}

	// 更新类结束行号
	if len(doc.Classes) > 0 {
		doc.Classes[len(doc.Classes)-1].EndLine = lineNum
	}

	// 合成 Go 包级容器：顶层函数必须有非空 ClassFQN 才能被三后端入库（P0-1）。
	// 容器以 package 名作为 FullName，覆盖整个文件；同文件已有同名类型时不重复创建
	// （同名极端场景下避免与真实类型的类映射冲突）。
	if lang == "go" && hasTopLevelFunc && goPkg != "" && !hasClassFullName(doc.Classes, goPkg) {
		doc.Classes = append(doc.Classes, parser.ClassIR{
			Name:      goPkg,
			FullName:  goPkg,
			Type:      "PACKAGE",
			StartLine: 1,
			EndLine:   lineNum,
			Doc:       "package " + goPkg,
		})
	}

	return doc, nil
}

// hasClassFullName 报告类列表中是否已存在指定 FullName 的类。
func hasClassFullName(classes []parser.ClassIR, fullName string) bool {
	for _, c := range classes {
		if c.FullName == fullName {
			return true
		}
	}
	return false
}

// —— Go 变量/常量提取辅助（2026-09-03 新增，仅 Go 精确路径） ——

// pkgDeclPatterns 语言→包/命名空间声明正则（提取包前缀，用于调用图 caller FQN 包限定）。
//
// 仅收录「单文件内显式、可靠的包声明」语言（Java/Kotlin/C#）。其余语言的包/模块语义
// 依赖文件路径或作用域嵌套（Python 模块以文件路径推导、TS 用 import/export 而非包声明、
// Rust 用嵌套 mod 树、C++ 命名空间可多段嵌套），留待语种类别逐步收敛。
var pkgDeclPatterns = map[string]*regexp.Regexp{
	"java":   regexp.MustCompile(`^\s*package\s+([\w.]+)\s*;?\s*$`),
	"kotlin": regexp.MustCompile(`^\s*package\s+([\w.]+)\s*$`),
	"csharp": regexp.MustCompile(`^\s*namespace\s+([\w.]+)\s*[{;]`),
}

// parsePkgDecl 若该行属于语言的包/命名空间声明，返回包 FQN；否则空串。
func parsePkgDecl(lang, line string) string {
	if re, ok := pkgDeclPatterns[lang]; ok {
		if m := re.FindStringSubmatch(line); m != nil {
			return m[1]
		}
	}
	return ""
}

var (
	// goConstPattern：const X [Type] = v；const ( ... ) 块内行 X [Type] = v
	goConstPattern = regexp.MustCompile(`^\s*(?:const\s+)?(\w+)\s+(?:([\w\[\]*.<>]+)\s+)?=\s*(.+)$`)
	// goFieldPattern：struct 字段行 `Name Type [tag]`
	goFieldPattern = regexp.MustCompile(`^\s*(\w+)\s+([\w\[\]*.<>]+)\s*(?:\x60[^\x60]*\x60)?\s*$`)
)

// parseGoConstLine 解析 Go 常量声明行（含 const ( ... ) 块内行，正则已兼容无 const 前缀）。
// 返回 name/type/value；无法识别（const ( 等）返回空 name。
func parseGoConstLine(line string) (name, typ, val string) {
	m := goConstPattern.FindStringSubmatch(line)
	if m == nil {
		return "", "", ""
	}
	return m[1], m[2], m[3]
}

// parseGoStructField 解析 struct 字段行 `Name Type [tag]`。
// 排除 struct/interface/map/func/chan/type 等关键字类型（嵌套类型定义等伪字段），
// 正则本身排除含 `{`/`}`/`(`/`=` 的行（嵌套/方法/初始化）。无法识别返回 ok=false。
func parseGoStructField(line string) (name, typ string, ok bool) {
	m := goFieldPattern.FindStringSubmatch(line)
	if m == nil {
		return "", "", false
	}
	switch m[2] {
	case "struct", "interface", "map", "func", "chan", "type":
		return "", "", false
	}
	return m[1], m[2], true
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
	case "protobuf":
		for _, m := range matches {
			if m == "service" {
				return "INTERFACE"
			}
			if m == "enum" {
				return "ENUM"
			}
		}
		return "CLASS"
	case "html":
		return "ELEMENT"
	case "hcl":
		return "BLOCK"
	case "svelte":
		return "COMPONENT"
	case "markdown", "dockerfile", "cue":
		return "BLOCK"
	case "elm":
		return "MODULE"
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
	inRawString    bool   // Go 反引号原始字符串（可跨行，P3-9）
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

		// Go 反引号原始字符串状态（跨行）：内部内容全部屏蔽，
		// 避免串内伪调用与 `{`/`}` 干扰调用检测和深度跟踪（P3-9）。
		if c.inRawString {
			out[i] = ' '
			if ch == '`' {
				c.inRawString = false
			}
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

		// 进入 Go 原始字符串（反引号，可跨行）
		if lang == "go" && ch == '`' {
			c.inRawString = true
			out[i] = ' '
			i++
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
// caller 为调用所在方法 FQN（Go 精确路径已包限定）；
// pkg/recvVar/recvType 仅 Go 精确路径提供，用于把 callee 包限定为
// pkg.Func（包级函数）或 pkg.RecvType.Method（自接收者调用）；其余语言传空，保持原行为。
// detectCalls 从一行源码中提取函数调用关系。
//
// caller 为当前方法的 FQN；pkg/recvVar/recvType 供 Go 精确路径的 callee 限定；
// nonGoPkg/currentClass 供 Java/Kotlin/C# 显式包声明语言的裸 callee 限定
// （把当前类内无点前缀的方法调用限定为 nonGoPkg.Class.Method）。
func detectCalls(line string, lineNum int, calls *[]parser.CallIR, pattern *regexp.Regexp, caller, pkg, recvVar, recvType, nonGoPkg, currentClass string) {
	matches := pattern.FindAllStringSubmatch(line, -1)
	for _, m := range matches {
		if len(m) >= 2 {
			callee := qualifyCallee(m[1], pkg, recvVar, recvType, nonGoPkg, currentClass)
			// 跳过关键字和常见误匹配
			if isKeyword(callee) || len(callee) <= 1 {
				continue
			}
			*calls = append(*calls, parser.CallIR{
				CallerFQN:  caller,
				CalleeFQN:  callee,
				CallType:   "direct",
				LineNumber: lineNum,
				Source:     parser.CallSourceRegex,
				Confidence: parser.ConfidenceRegex,
			})
		}
	}
}

// qualifyCallee 把裸名/ receiver.Method 形式的 callee 限定为包级 FQN。
//
// 规则（优先级从高到低）：
//   - 已带 pkg 前缀（如 pkg.Func）→ 保持原样；
//   - 自接收者调用（recv.Method 且 recv == 当前方法 receiver 变量）→ pkg.RecvType.Method（Go 精确路径）；
//   - 无点前缀的裸名（当前作用域方法调用）→ 若有 nonGoPkg+currentClass（Java/Kotlin/C#）限定为
//     nonGoPkg.CurrentClass.callee；否则若 pkg 非空（Go 包级函数）限定为 pkg.callee；
//   - 其余 receiver.Method（无法消歧 receiver 类型）→ 保持原样。
func qualifyCallee(callee, pkg, recvVar, recvType, nonGoPkg, currentClass string) string {
	if strings.Contains(callee, ".") {
		// 自接收者调用：recv == 当前方法 receiver 变量 → pkg.RecvType.Method（Go 精确路径）
		if recvVar != "" && recvType != "" && strings.HasPrefix(callee, recvVar+".") {
			return pkg + "." + recvType + strings.TrimPrefix(callee, recvVar)
		}
		// 已带 pkg 前缀或无法消歧的 receiver.Method → 保持
		return callee
	}
	// 无点前缀的裸名：当前作用域内的方法调用
	if nonGoPkg != "" && currentClass != "" {
		// Java/Kotlin/C# 显式包声明：限定为 pkg.Class.callee
		return nonGoPkg + "." + currentClass + "." + callee
	}
	if pkg != "" {
		// Go 包级函数调用 → 包限定 pkg.callee
		return pkg + "." + callee
	}
	// 无包信息（Python/TS/Rust/C++ 等）→ 原样返回，保持原启发式行为
	return callee
}

// goRecvVar 从方法定义的 receiver 段解析归属变量名。
// 输入为 methodPattern 捕获组 1（如 "(w *Watcher[T]) "，顶层函数为空串）：
//
//	"(w *Watcher[T]) " → "w"
//	"(w Watcher) "     → "w"
//
// 返回空串表示无 receiver。仅 Go 使用。
func goRecvVar(recv string) string {
	r := strings.TrimSpace(recv)
	if r == "" {
		return ""
	}
	r = strings.TrimPrefix(r, "(")
	r = strings.TrimSuffix(r, ")")
	r = strings.TrimSpace(r)
	fields := strings.Fields(r)
	if len(fields) == 0 {
		return ""
	}
	return strings.TrimPrefix(fields[0], "*") // 剥指针（*Watcher → Watcher 取变量名）
}

// parseGoReceiver 从方法定义的 receiver 段解析归属类型名。
// 输入为 methodPattern 捕获组 1（如 "(w *Watcher[T]) "，顶层函数为空串）：
//
//	"(w *Watcher[T]) " → "Watcher"（剥括号/指针/泛型实参）
//	"(u User) "        → "User"
//	""                 → ""（顶层函数，无 receiver）
//
// 返回空串表示无 receiver。仅 Go 使用；错误/非常规形态保守返回空串。
func parseGoReceiver(recv string) string {
	r := strings.TrimSpace(recv)
	if r == "" {
		return ""
	}
	r = strings.TrimPrefix(r, "(")
	r = strings.TrimSuffix(r, ")")
	r = strings.TrimSpace(r)
	fields := strings.Fields(r)
	if len(fields) < 2 {
		return "" // 无 receiver 名与类型之分，视为非常规形态
	}
	typ := fields[len(fields)-1]
	typ = strings.TrimPrefix(typ, "*")
	typ = strings.TrimPrefix(typ, "[]")
	if i := strings.IndexByte(typ, '['); i >= 0 { // 剥泛型实参 [T] / [T any]
		typ = typ[:i]
	}
	return typ
}

// braceNet 返回一行代码中 `{`/`}` 的净深度增量（调用方需传已剔除字符串/注释的行）。
func braceNet(s string) int {
	n := 0
	for i := 0; i < len(s); i++ {
		switch s[i] {
		case '{':
			n++
		case '}':
			n--
		}
	}
	return n
}

// stmtLineKeywords 行首语句关键字集合：以这些词开头的行不可能是方法声明。
//
// 仅用于非 Go 通用路径的方法解析抑制。Java/Groovy 的方法模式没有行首锚点，
// 且「类型」捕获组允许空白，会把 `return helper(a);` 解释为
// 「返回类型 return + 方法名 helper」，导致方法数虚增、方法体调用边被吞。
var stmtLineKeywords = map[string]bool{
	"return": true, "throw": true, "new": true, "else": true, "case": true,
	"assert": true, "yield": true, "await": true, "if": true, "for": true,
	"while": true, "switch": true, "catch": true, "finally": true, "do": true,
	"try": true, "continue": true, "break": true, "super": true, "this": true,
}

// isStatementLine 报告一行（已去首尾空白）是否以语句关键字开头。
func isStatementLine(trimmed string) bool {
	if trimmed == "" {
		return false
	}
	end := strings.IndexAny(trimmed, " \t(;{")
	if end < 0 {
		end = len(trimmed)
	}
	return stmtLineKeywords[trimmed[:end]]
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
