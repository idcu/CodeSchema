//go:build treesitter

// Package treesitter 提供源码解析适配器。
//
// 本文件为「真语法树」实现（`-tags treesitter` 启用）：基于 CGO tree-sitter binding
// （github.com/smacker/go-tree-sitter）做语法级精确解析，替换默认的正则启发式。
//
// 与默认实现（adapter.go，`//go:build !treesitter`）共享：
//   - 同名 TreeSitterAdapter 类型 / NewTreeSitterAdapter 构造
//   - 同一 parser.IRDocument IR 契约（类/方法/调用）
//
// 构建方式：
//   - 默认（免 CGO）：go build ./...（走 adapter.go 正则实现）
//   - 真语法树：go build -tags treesitter ./...（本文件生效，需 gcc/CGO）
//
// 方案背景：T2-1 方案 C（混合）——默认免 CGO、需要精度时一键切换，
// 见 analysis/2026-08-14-t2-1-parser-precision-eval.md。
package treesitter

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	ts "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/bash"
	"github.com/smacker/go-tree-sitter/c"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/csharp"
	"github.com/smacker/go-tree-sitter/css"
	"github.com/smacker/go-tree-sitter/cue"
	"github.com/smacker/go-tree-sitter/dockerfile"
	"github.com/smacker/go-tree-sitter/elixir"
	"github.com/smacker/go-tree-sitter/elm"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/groovy"
	"github.com/smacker/go-tree-sitter/hcl"
	"github.com/smacker/go-tree-sitter/html"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/kotlin"
	"github.com/smacker/go-tree-sitter/lua"
	"github.com/smacker/go-tree-sitter/markdown/tree-sitter-markdown"
	"github.com/smacker/go-tree-sitter/ocaml"
	"github.com/smacker/go-tree-sitter/php"
	"github.com/smacker/go-tree-sitter/protobuf"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/ruby"
	"github.com/smacker/go-tree-sitter/rust"
	"github.com/smacker/go-tree-sitter/scala"
	"github.com/smacker/go-tree-sitter/sql"
	"github.com/smacker/go-tree-sitter/svelte"
	"github.com/smacker/go-tree-sitter/swift"
	"github.com/smacker/go-tree-sitter/toml"
	tslang "github.com/smacker/go-tree-sitter/typescript/typescript"
	"github.com/smacker/go-tree-sitter/yaml"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/parser/adapter"
)

// TreeSitterAdapter 基于 tree-sitter 真语法树的解析适配器（-tags treesitter）。
type TreeSitterAdapter struct {
	langs map[string]*ts.Language
}

// NewTreeSitterAdapter 创建适配器并注册全部支持语言的 grammar。
func NewTreeSitterAdapter() *TreeSitterAdapter {
	return &TreeSitterAdapter{
		langs: map[string]*ts.Language{
			"go":         golang.GetLanguage(),
			"java":       java.GetLanguage(),
			"ts":         tslang.GetLanguage(),
			"js":         javascript.GetLanguage(),
			"py":         python.GetLanguage(),
			"rust":       rust.GetLanguage(),
			"cpp":        cpp.GetLanguage(),
			"c":          c.GetLanguage(),
			"kotlin":     kotlin.GetLanguage(),
			"swift":      swift.GetLanguage(),
			"php":        php.GetLanguage(),
			"csharp":     csharp.GetLanguage(),
			"ruby":       ruby.GetLanguage(),
			"bash":       bash.GetLanguage(),
			"scala":      scala.GetLanguage(),
			"sql":        sql.GetLanguage(),
			"elixir":     elixir.GetLanguage(),
			"ocaml":      ocaml.GetLanguage(),
			"lua":        lua.GetLanguage(),
			"groovy":     groovy.GetLanguage(),
			"css":        css.GetLanguage(),
			"toml":       toml.GetLanguage(),
			"yaml":       yaml.GetLanguage(),
			"protobuf":   protobuf.GetLanguage(),
			"html":       html.GetLanguage(),
			"hcl":        hcl.GetLanguage(),
			"svelte":     svelte.GetLanguage(),
			"markdown":   tree_sitter_markdown.GetLanguage(),
			"dockerfile": dockerfile.GetLanguage(),
			"elm":        elm.GetLanguage(),
			"cue":        cue.GetLanguage(),
		},
	}
}

// Name 返回适配器唯一标识。
func (a *TreeSitterAdapter) Name() string { return "treesitter" }

// Supports 判断是否支持指定语言。
func (a *TreeSitterAdapter) Supports(lang string) bool {
	_, ok := a.langs[lang]
	return ok
}

// Init 初始化适配器（grammar 已在构造时注册）。
func (a *TreeSitterAdapter) Init(_ context.Context, _ map[string]any) error {
	return nil
}

// Close 清理适配器资源。
func (a *TreeSitterAdapter) Close() error {
	return nil
}

// astClassNodeTypes 各语言「类/接口/结构体/枚举声明」的 AST 节点类型集合。
var astClassNodeTypes = map[string]map[string]bool{
	"go":         {"type_declaration": true},
	"java":       {"class_declaration": true, "interface_declaration": true, "enum_declaration": true, "record_declaration": true},
	"ts":         {"class_declaration": true, "interface_declaration": true, "enum_declaration": true},
	"js":         {"class_declaration": true},
	"py":         {"class_definition": true},
	"rust":       {"struct_item": true, "enum_item": true, "trait_item": true, "impl_item": true},
	"cpp":        {"class_specifier": true, "struct_specifier": true, "enum_specifier": true},
	"c":          {"struct_specifier": true, "enum_specifier": true, "union_specifier": true},
	"kotlin":     {"class_declaration": true, "interface_declaration": true, "object_declaration": true},
	"swift":      {"class_declaration": true, "protocol_declaration": true, "enum_declaration": true, "struct_declaration": true, "extension_declaration": true},
	"php":        {"class_declaration": true, "interface_declaration": true, "trait_declaration": true, "enum_declaration": true},
	"csharp":     {"class_declaration": true, "interface_declaration": true, "struct_declaration": true, "enum_declaration": true, "record_declaration": true},
	"ruby":       {"class": true, "module": true},
	"bash":       {},
	"scala":      {"class_definition": true, "object_definition": true, "trait_definition": true, "enum_definition": true},
	"sql":        {"create_table": true, "create_view": true, "create_function": true, "create_procedure": true},
	"elixir":     {},
	"ocaml":      {"module_definition": true},
	"lua":        {},
	"groovy":     {"class_definition": true},
	"css":        {"rule_set": true, "media_statement": true, "keyframes_statement": true},
	"toml":       {"table": true},
	"yaml":       {},
	"protobuf":   {"message": true, "service": true, "enum": true},
	"html":       {"element": true},
	"hcl":        {"block": true},
	"svelte":     {"script_element": true},
	"markdown":   {"section": true},
	"dockerfile": {"instruction": true},
	"elm":        {"module_declaration": true, "value_declaration": true},
	"cue":        {"struct_lit": true},
}

// astMethodNodeTypes 各语言「方法/函数声明」的 AST 节点类型集合。
var astMethodNodeTypes = map[string]map[string]bool{
	"go":         {"method_declaration": true, "function_declaration": true},
	"java":       {"method_declaration": true, "constructor_declaration": true},
	"ts":         {"method_definition": true, "function_declaration": true},
	"js":         {"method_definition": true, "function_declaration": true},
	"py":         {"function_definition": true},
	"rust":       {"function_item": true},
	"cpp":        {"function_definition": true},
	"c":          {"function_definition": true},
	"kotlin":     {"function_declaration": true},
	"swift":      {"function_declaration": true},
	"php":        {"function_definition": true, "method_declaration": true},
	"csharp":     {"method_declaration": true, "constructor_declaration": true},
	"ruby":       {"method": true, "singleton_method": true},
	"bash":       {"function_definition": true},
	"scala":      {"function_definition": true},
	"sql":        {"create_function": true, "create_procedure": true},
	"elixir":     {},
	"ocaml":      {"value_definition": true},
	"lua":        {"function_statement": true},
	"groovy":     {"function_definition": true},
	"css":        {},
	"toml":       {},
	"yaml":       {},
	"protobuf":   {"rpc": true},
	"html":       {},
	"hcl":        {},
	"svelte":     {},
	"markdown":   {},
	"dockerfile": {},
	"elm":        {},
	"cue":        {},
}

// astCallNodeTypes 各语言「调用表达式」的 AST 节点类型集合。
var astCallNodeTypes = map[string]map[string]bool{
	"go":         {"call_expression": true},
	"java":       {"method_invocation": true},
	"ts":         {"call_expression": true},
	"js":         {"call_expression": true},
	"py":         {"call": true},
	"rust":       {"call_expression": true},
	"cpp":        {"call_expression": true},
	"c":          {"call_expression": true},
	"kotlin":     {"call_expression": true},
	"swift":      {"call_expression": true},
	"php":        {"function_call_expression": true, "member_call_expression": true},
	"csharp":     {"invocation_expression": true},
	"ruby":       {"call": true},
	"bash":       {"command": true, "command_call": true},
	"scala":      {"call_expression": true, "method_invocation": true},
	"sql":        {"invocation": true, "function_call": true, "call_statement": true},
	"elixir":     {"call": true},
	"ocaml":      {"application_expression": true},
	"lua":        {"function_call": true},
	"groovy":     {"function_call": true},
	"css":        {},
	"toml":       {},
	"yaml":       {},
	"markdown":   {},
	"dockerfile": {},
	"elm":        {},
	"cue":        {},
	"protobuf":   {},
	"html":       {},
	"hcl":        {},
	"svelte":     {},
}

// Parse 解析单个源文件，返回归一化 IR（基于 AST 语法级提取）。
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
	langPtr, ok := a.langs[lang]
	if !ok {
		return &parser.IRDocument{Source: "treesitter", FilePath: path}, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read file: %w", err)
	}

	p := ts.NewParser()
	defer p.Close()
	p.SetLanguage(langPtr)

	tree, err := p.ParseCtx(ctx, nil, data)
	if err != nil {
		return nil, fmt.Errorf("tree-sitter parse: %w", err)
	}
	defer tree.Close()

	doc := &parser.IRDocument{
		Source:   "treesitter",
		Language: lang,
		FilePath: path,
	}

	// P20：Java/Kotlin/C# 单文件显式包/命名空间声明 → caller FQN 包限定，
	// 与默认正则路径（adapter.go 的 nonGoPkg）对齐，使非 Go 调用图与 Go 同命名空间。
	astPkg := astPackageDecl(lang, data)

	classTypes := astClassNodeTypes[lang]
	methodTypes := astMethodNodeTypes[lang]
	callTypes := astCallNodeTypes[lang]

	var curClass *parser.ClassIR
	var curMethod *parser.MethodIR // 跟踪当前方法，用于回填调用关系的 CallerFQN
	// skipChild：Elixir def 的 arguments（函数签名）子节点，避免签名中的
	// `create(order)` 被误判为调用
	var skipChild *ts.Node
	var walk func(n *ts.Node)
	walk = func(n *ts.Node) {
		if n == nil || n.IsNull() {
			return
		}
		typ := n.Type()
		start := n.StartPoint()

		switch {
		case lang == "elixir" && typ == "call":
			// Elixir 的 defmodule / def / defp 与普通调用都是 call 节点，
			// 按首个子节点标识符区分：defmodule → 模块（类）、def/defp → 方法、其余 → 调用
			if head := elixirCallHead(n, data); head != "" {
				switch {
				case head == "defmodule":
					name := elixirModuleName(n, data)
					if name != "" {
						cls := parser.ClassIR{
							Name:      name,
							FullName:  name,
							Type:      "MODULE",
							StartLine: int(start.Row) + 1,
						}
						doc.Classes = append(doc.Classes, cls)
						curClass = &doc.Classes[len(doc.Classes)-1]
						curMethod = nil // 进入新的模块作用域，方法跟踪重置
					}
				case head == "def" || head == "defp" || head == "defmacro" || head == "defguard":
					name := elixirDefName(n, data)
					if name != "" {
						m := parser.MethodIR{
							Name:      name,
							Signature: string(n.Content(data)),
							StartLine: int(start.Row) + 1,
						}
						if curClass != nil {
							m.ClassFQN = curClass.FullName
						}
						doc.Methods = append(doc.Methods, m)
						curMethod = &doc.Methods[len(doc.Methods)-1]
						// 跳过 arguments（函数签名 `create(order)`）子树
						for i := 0; i < int(n.NamedChildCount()); i++ {
							if c := n.NamedChild(i); c.Type() == "arguments" {
								skipChild = c
								break
							}
						}
					}
				default:
					callee := astCalleeName(n, data)
					if callee != "" && !isKeyword(callee) {
						caller := ""
						if curMethod != nil {
							if curMethod.ClassFQN != "" {
								caller = curMethod.ClassFQN + "." + curMethod.Name
							} else {
								caller = curMethod.Name
							}
							// 包/命名空间限定（Java/Kotlin/C#）：pkg.Class.Method / pkg.func
							if astPkg != "" {
								caller = astPkg + "." + caller
							}
						}
						doc.Calls = append(doc.Calls, parser.CallIR{
							CalleeFQN:  callee,
							CallerFQN:  caller,
							CallType:   "direct",
							LineNumber: int(start.Row) + 1,
						})
					}
				}
				break
			}

		case classTypes[typ]:
			name := astNodeName(n, lang, data)
			if name == "" {
				break
			}
			cls := parser.ClassIR{
				Name:      name,
				FullName:  name,
				Type:      astClassType(typ, lang),
				StartLine: int(start.Row) + 1,
			}
			doc.Classes = append(doc.Classes, cls)
			curClass = &doc.Classes[len(doc.Classes)-1]
			curMethod = nil // 进入新的类作用域，方法跟踪重置

		case methodTypes[typ]:
			// OCaml：value_definition 可能是纯值绑定（`let s = "..." in`），
			// 仅当含 parameter（函数定义）时登记为方法
			if lang == "ocaml" && typ == "value_definition" && !ocamlHasParam(n) {
				break
			}
			name := astNodeName(n, lang, data)
			if name == "" {
				break
			}
			m := parser.MethodIR{
				Name:      name,
				Signature: string(n.Content(data)),
				StartLine: int(start.Row) + 1,
			}
			if curClass != nil {
				m.ClassFQN = curClass.FullName
			}
			doc.Methods = append(doc.Methods, m)
			curMethod = &doc.Methods[len(doc.Methods)-1]

		case callTypes[typ]:
			callee := astCalleeName(n, data)
			if callee == "" || isKeyword(callee) {
				break
			}
			caller := ""
			if curMethod != nil {
				if curMethod.ClassFQN != "" {
					caller = curMethod.ClassFQN + "." + curMethod.Name
				} else {
					caller = curMethod.Name
				}
				// 包/命名空间限定（Java/Kotlin/C#）：pkg.Class.Method / pkg.func
				if astPkg != "" {
					caller = astPkg + "." + caller
				}
			}
			doc.Calls = append(doc.Calls, parser.CallIR{
				CalleeFQN:  callee,
				CallerFQN:  caller,
				CallType:   "direct",
				LineNumber: int(start.Row) + 1,
			})
		}

		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if skipChild != nil && c == skipChild {
				skipChild = nil
				continue
			}
			walk(c)
		}
	}
	walk(tree.RootNode())

	return doc, nil
}

// astNodeName 提取声明节点的名称。
//
// 优先级：
//  1. name 字段（Go method_declaration / Java method_declaration / TS 等）；
//  2. 声明头部第一个 identifier 类子节点（Kotlin function_declaration 无 name 字段，
//     但头部有 simple_identifier；bash function_definition 头部是 word）。
//
// 兜底：子树中第一个 identifier/type_identifier 文本。
func astNodeName(n *ts.Node, lang string, src []byte) string {
	// OCaml: module_definition → module_binding.name（module_name）；value_definition → let_binding.value_name
	if lang == "ocaml" && (n.Type() == "module_definition" || n.Type() == "value_definition") {
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if name := c.ChildByFieldName("name"); name != nil && !name.IsNull() {
				if s := string(name.Content(src)); s != "" {
					return s
				}
			}
			for j := 0; j < int(c.NamedChildCount()); j++ {
				g := c.NamedChild(j)
				if g.Type() == "value_name" {
					return string(g.Content(src))
				}
			}
		}
	}
	// CSS rule_set：取 selectors 子节点文本（`.button` 等选择器）
	if n.Type() == "rule_set" {
		for i := 0; i < int(n.NamedChildCount()); i++ {
			if c := n.NamedChild(i); c.Type() == "selectors" {
				return string(c.Content(src))
			}
		}
	}
	// Markdown section：取首个 atx_heading 标题文本（`# Title` → `Title`）
	if n.Type() == "section" {
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if c.Type() == "atx_heading" || c.Type() == "setext_heading" {
				s := string(c.Content(src))
				// 剥离 # 前缀
				s = strings.TrimLeft(s, "# ")
				s = strings.TrimSpace(s)
				if s != "" {
					return s
				}
			}
		}
	}
	// Svelte script_element：固定名称「script」
	if n.Type() == "script_element" {
		return "script"
	}
	// HCL block：类型 + 标签拼接（`resource "aws_instance" "web"` → `resource.aws_instance.web`）
	if n.Type() == "block" {
		parts := []string{}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if c.Type() == "identifier" || c.Type() == "string_lit" {
				s := string(c.Content(src))
				s = strings.Trim(s, "\"")
				parts = append(parts, s)
			}
		}
		if len(parts) > 0 {
			return strings.Join(parts, ".")
		}
	}
	// HTML element → start_tag → tag_name（`<div ...>` → `div`）
	if n.Type() == "element" {
		for i := 0; i < int(n.NamedChildCount()); i++ {
			st := n.NamedChild(i)
			if st.Type() != "start_tag" {
				continue
			}
			for j := 0; j < int(st.NamedChildCount()); j++ {
				if c := st.NamedChild(j); c.Type() == "tag_name" {
					return string(c.Content(src))
				}
			}
		}
	}
	if name := n.ChildByFieldName("name"); name != nil && !name.IsNull() {
		if s := string(name.Content(src)); s != "" {
			return s
		}
	}
	// 部分语言（如 Go 的 type_declaration → type_spec 子节点）name 字段在更深层
	if child := n.ChildByFieldName("type"); child != nil && !child.IsNull() {
		if name := child.ChildByFieldName("name"); name != nil && !name.IsNull() {
			return string(name.Content(src))
		}
	}
	// Kotlin 等：取头部前几个 named 子节点中第一个 identifier/simple_identifier
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c.Type() == "identifier" || c.Type() == "simple_identifier" || c.Type() == "type_identifier" ||
			(lang == "bash" && c.Type() == "word") {
			return string(c.Content(src))
		}
		// 嵌套一层（如 modifiers + name 组合）
		if inner := firstIdentifierText(c, src); inner != "" {
			return inner
		}
	}
	return firstIdentifierText(n, src)
}

// astPackageDecl 提取 Java/Kotlin/C# 的单文件显式包/命名空间声明（P20）。
// 与默认正则路径 adapter.go 的 pkgDeclPatterns 口径一致；其余语言无显式包声明 → 空串。
func astPackageDecl(lang string, data []byte) string {
	var re *regexp.Regexp
	switch lang {
	case "java":
		re = regexp.MustCompile(`(?m)^\s*package\s+([\w.]+)\s*;?\s*$`)
	case "kotlin":
		re = regexp.MustCompile(`(?m)^\s*package\s+([\w.]+)\s*$`)
	case "csharp":
		re = regexp.MustCompile(`(?m)^\s*namespace\s+([\w.]+)\s*[{;]`)
	default:
		return ""
	}
	if m := re.FindSubmatch(data); m != nil {
		return string(m[1])
	}
	return ""
}

// astClassType 从 AST 节点类型映射 IR 类类型。
func astClassType(nodeType, lang string) string {
	switch nodeType {
	case "interface_declaration", "trait_item", "service":
		return "INTERFACE"
	case "enum_declaration", "enum_item", "enum_specifier", "enum":
		return "ENUM"
	case "object_declaration":
		return "OBJECT"
	case "element":
		return "ELEMENT"
	case "block":
		return "BLOCK"
	case "script_element":
		return "COMPONENT"
	default:
		return "CLASS"
	}
}

// cppCtorTypes C++ 常见标准库/内置类型名：`std::string(x)` 这类函数式转换是类型构造，
// 不是函数调用（区别于 `std::to_string(x)` 真调用）。
var cppCtorTypes = map[string]bool{
	"string": true, "wstring": true, "vector": true, "list": true, "map": true,
	"set": true, "pair": true, "tuple": true, "optional": true, "unique_ptr": true,
	"shared_ptr": true, "array": true, "deque": true, "queue": true, "stack": true,
	"string_view": true, "function": true, "unordered_map": true, "unordered_set": true,
}

// astCalleeName 提取调用表达式的被调方名。
//
// 处理三类 AST 形态：
//  1. 简单调用 `foo(x)` / `obj.method(x)` → function 字段文本（`foo` / `obj.method`）；
//  2. 链式调用 `a().b().c()` → 取 selector 链最后一段的标识符（`c`），
//     避免嵌套 call 都截到第一个 `(` 变成 `a`；
//  3. 泛型调用 `http.get<T[]>(x)` → 剥离 `<...>` 泛型参数（`http.get`）；
//  4. C++/TS 类型构造 `std::string(x)` / `Foo<T>(x)`：function 字段为类型节点时跳过
//     （类型转换/构造不是函数调用）；
//  5. PHP `member_call_expression`：取最后一个 name 子节点（`$payment->pay` → `pay`），
//     与正则路径口径一致（方法名）。
func astCalleeName(n *ts.Node, src []byte) string {
	fn := n.ChildByFieldName("function")
	if fn != nil && !fn.IsNull() {
		// 类型构造 / 类型转换调用（C++ std::string(x)、TS 类型断言）跳过
		if isTypeNode(fn.Type()) || isCppCtorCall(fn, src) {
			return ""
		}
		// 成员选择表达式：区分「普通 obj.method」与「链式 a().b().c()」
		if isMemberExprType(fn.Type()) {
			// 操作数是调用 → 链式调用，取最后一段标识符（c）
			if first := fn.NamedChild(0); first != nil && !first.IsNull() && isCallNodeType(first.Type()) {
				if last := fn.NamedChild(int(fn.NamedChildCount()) - 1); last != nil && !last.IsNull() {
					return stripTypeArgs(string(last.Content(src)))
				}
			}
			// 普通 obj.method / obj.field 调用 → 保留完整成员表达式文本
			return stripTypeArgs(string(fn.Content(src)))
		}
		return stripTypeArgs(string(fn.Content(src)))
	}
	// PHP member_call_expression：无 function 字段，取最后一个 name 子节点（方法名）
	if n.Type() == "member_call_expression" {
		for i := int(n.NamedChildCount()) - 1; i >= 0; i-- {
			if c := n.NamedChild(i); c != nil && !c.IsNull() && c.Type() == "name" {
				return string(c.Content(src))
			}
		}
	}
	// Ruby call：取调用表达式 `(` 前完整文本（`validator.validate(x)` → `validator.validate`），
	// 与正则路径口径一致（完整成员链，而非仅方法名）
	if n.Type() == "call" {
		text := string(n.Content(src))
		if idx := strings.Index(text, "("); idx >= 0 {
			return stripTypeArgs(strings.TrimSpace(text[:idx]))
		}
	}
	// SQL call_statement（CALL proc();）：取子 invocation 文本（`(` 前）
	if n.Type() == "call_statement" {
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if c.Type() == "invocation" || c.Type() == "function_call" {
				text := string(c.Content(src))
				if idx := strings.Index(text, "("); idx >= 0 {
					return stripTypeArgs(strings.TrimSpace(text[:idx]))
				}
				return text
			}
		}
	}
	// Bash command：取第一个 word 子节点（命令名）
	if n.Type() == "command" || n.Type() == "command_call" {
		for i := 0; i < int(n.NamedChildCount()); i++ {
			c := n.NamedChild(i)
			if c.Type() == "word" || c.Type() == "command_name" {
				return strings.TrimSpace(string(c.Content(src)))
			}
		}
	}
	// C# invocation_expression：取 name/member_access 的完整文本（`validator.Validate` → 完整）
	if n.Type() == "invocation_expression" {
		text := string(n.Content(src))
		if idx := strings.Index(text, "("); idx >= 0 {
			return stripTypeArgs(strings.TrimSpace(text[:idx]))
		}
	}
	// Java method_invocation：object 是 method_invocation → 链式，取 name 字段（`invoke`）；
	// object 是 identifier → 普通 obj.method，取 `(` 前完整文本
	if n.Type() == "method_invocation" {
		if obj := n.ChildByFieldName("object"); obj != nil && !obj.IsNull() && obj.Type() == "method_invocation" {
			if name := n.ChildByFieldName("name"); name != nil && !name.IsNull() {
				return string(name.Content(src))
			}
		}
		text := string(n.Content(src))
		if idx := strings.Index(text, "("); idx >= 0 {
			return stripTypeArgs(strings.TrimSpace(text[:idx]))
		}
	}
	// OCaml application_expression：无括号应用（`validator.validate x`），
	// 取首个子节点（field_get_expression / identifier）文本作为被调方
	if n.Type() == "application_expression" {
		if first := n.NamedChild(0); first != nil && !first.IsNull() {
			return string(first.Content(src))
		}
	}
	// Python call 等：直接取调用表达式文本（`(` 前）
	text := string(n.Content(src))
	if idx := strings.Index(text, "("); idx >= 0 {
		return stripTypeArgs(strings.TrimSpace(text[:idx]))
	}
	return ""
}

// ocamlHasParam 判断 OCaml value_definition 是否为函数定义（let_binding 含 parameter 子节点）。
func ocamlHasParam(n *ts.Node) bool {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		for j := 0; j < int(c.NamedChildCount()); j++ {
			if c.NamedChild(j).Type() == "parameter" {
				return true
			}
		}
	}
	return false
}

// elixirCallHead 返回 Elixir call 节点的首个子节点标识符文本（defmodule / def / defp / 被调函数名）。
func elixirCallHead(n *ts.Node, src []byte) string {
	if first := n.NamedChild(0); first != nil && !first.IsNull() {
		return string(first.Content(src))
	}
	return ""
}

// elixirModuleName 提取 Elixir defmodule 的模块名（`defmodule OrderService do` → `OrderService`）。
// defmodule 的首个参数是模块名（标识符或别名 `Foo.Bar`），取 arguments 子节点内的完整文本。
func elixirModuleName(n *ts.Node, src []byte) string {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c.Type() != "arguments" {
			continue
		}
		for j := 0; j < int(c.NamedChildCount()); j++ {
			g := c.NamedChild(j)
			if g.Type() == "identifier" || g.Type() == "aliases" || g.Type() == "call" {
				return string(g.Content(src))
			}
		}
		// 兜底：arguments 内文本（`OrderService` / `Foo.Bar`）
		if s := strings.TrimSpace(string(c.Content(src))); s != "" {
			if idx := strings.IndexAny(s, " \t\n"); idx >= 0 {
				s = s[:idx]
			}
			return s
		}
	}
	return ""
}

// elixirDefName 提取 Elixir def/defp 的方法名：arguments 子节点内的首个 identifier（`def create(order)` → `create`）。
func elixirDefName(n *ts.Node, src []byte) string {
	for i := 0; i < int(n.NamedChildCount()); i++ {
		c := n.NamedChild(i)
		if c.Type() != "arguments" {
			continue
		}
		for j := 0; j < int(c.NamedChildCount()); j++ {
			g := c.NamedChild(j)
			if g.Type() == "identifier" || g.Type() == "call" {
				if s := string(g.Content(src)); s != "" {
					// `create(order)` 取 `(` 前；纯 identifier 直接返回
					if idx := strings.Index(s, "("); idx >= 0 {
						return strings.TrimSpace(s[:idx])
					}
					return s
				}
			}
		}
	}
	return ""
}

// isCppCtorCall 判断 C++ 类型构造调用（如 `std::string(key)`）。
func isCppCtorCall(fn *ts.Node, src []byte) bool {
	if fn.Type() != "qualified_identifier" {
		return false
	}
	text := string(fn.Content(src))
	idx := strings.LastIndex(text, "::")
	if idx < 0 {
		return false
	}
	return cppCtorTypes[text[idx+2:]]
}

// isTypeNode 判断节点类型是否为「类型」节点（用于跳过类型构造/转换调用）。
func isTypeNode(t string) bool {
	switch t {
	case "type_identifier", "qualified_type", "primitive_type",
		"generic_type", "template_type", "type_arguments", "type_expression":
		return true
	}
	return false
}

// isMemberExprType 判断节点类型是否为「成员选择/字段访问」表达式。
func isMemberExprType(t string) bool {
	switch t {
	case "selector_expression", "field_expression", "member_expression", "select_expression", "nav_expression", "attribute":
		return true
	}
	return false
}

// isCallNodeType 判断节点类型是否为「调用表达式」（链式调用的操作数）。
func isCallNodeType(t string) bool {
	switch t {
	case "call_expression", "method_invocation", "call":
		return true
	}
	return false
}

// stripTypeArgs 剥离泛型/模板实参（`http.get<T[]>` → `http.get`；`foo<int>` → `foo`）。
// 同时剥离 PHP 变量前缀（`$payment->pay` → `payment->pay`）。
func stripTypeArgs(s string) string {
	s = strings.TrimSpace(s)
	s = strings.TrimPrefix(s, "$")
	if idx := strings.Index(s, "<"); idx >= 0 {
		return strings.TrimSpace(s[:idx])
	}
	return s
}

// firstIdentifierText 在子树中找第一个 identifier/type_identifier/name 节点文本（名称提取兜底）。
func firstIdentifierText(n *ts.Node, src []byte) string {
	var found string
	var walk func(n *ts.Node)
	walk = func(n *ts.Node) {
		if found != "" || n == nil || n.IsNull() {
			return
		}
		if n.Type() == "identifier" || n.Type() == "type_identifier" || n.Type() == "name" {
			found = string(n.Content(src))
			return
		}
		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
		}
	}
	walk(n)
	return found
}

// isKeyword 判断是否为语言关键字，避免把控制流表达式误当调用。
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
		"fun": true, "val": true, "var": true, "when": true, "is": true,
		"in": true, "suspend": true, "require": true,
		"check": true, "error": true, "TODO": true, "println": true,
		"fn": true, "let": true, "mut": true, "match": true, "impl": true,
		"trait": true, "struct": true, "enum": true, "use": true, "mod": true,
		"sizeof": true, "static_cast": true, "dynamic_cast": true,
		"reinterpret_cast": true, "const_cast": true, "printf": true,
		// Bash
		"echo": true, "cd": true, "source": true, "local": true,
		"export": true, "eval": true, "exit": true,
		// Ruby / Scala
		"puts": true, "require_relative": true,
		// Elixir 标准库
		"IO.puts": true, "IO.inspect": true, "IO.gets": true,
		"Enum.map": true, "Enum.filter": true, "Enum.reduce": true, "Enum.each": true,
		"Map.get": true, "Map.put": true, "String.length": true,
		// OCaml 标准库
		"print_endline": true, "print_string": true, "print_int": true,
		"List.map": true, "List.filter": true, "List.fold_left": true, "List.iter": true,
		"Printf.printf": true, "failwith": true,
	}
	return keywords[name]
}
