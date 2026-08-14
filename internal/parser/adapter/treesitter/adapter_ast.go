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
	"strings"

	ts "github.com/smacker/go-tree-sitter"
	"github.com/smacker/go-tree-sitter/cpp"
	"github.com/smacker/go-tree-sitter/golang"
	"github.com/smacker/go-tree-sitter/java"
	"github.com/smacker/go-tree-sitter/javascript"
	"github.com/smacker/go-tree-sitter/kotlin"
	"github.com/smacker/go-tree-sitter/python"
	"github.com/smacker/go-tree-sitter/rust"
	tslang "github.com/smacker/go-tree-sitter/typescript/typescript"

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
			"go":     golang.GetLanguage(),
			"java":   java.GetLanguage(),
			"ts":     tslang.GetLanguage(),
			"js":     javascript.GetLanguage(),
			"py":     python.GetLanguage(),
			"rust":   rust.GetLanguage(),
			"cpp":    cpp.GetLanguage(),
			"kotlin": kotlin.GetLanguage(),
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
	"go":     {"type_declaration": true},
	"java":   {"class_declaration": true, "interface_declaration": true, "enum_declaration": true, "record_declaration": true},
	"ts":     {"class_declaration": true, "interface_declaration": true, "enum_declaration": true},
	"js":     {"class_declaration": true},
	"py":     {"class_definition": true},
	"rust":   {"struct_item": true, "enum_item": true, "trait_item": true, "impl_item": true},
	"cpp":    {"class_specifier": true, "struct_specifier": true, "enum_specifier": true},
	"kotlin": {"class_declaration": true, "interface_declaration": true, "object_declaration": true},
}

// astMethodNodeTypes 各语言「方法/函数声明」的 AST 节点类型集合。
var astMethodNodeTypes = map[string]map[string]bool{
	"go":     {"method_declaration": true, "function_declaration": true},
	"java":   {"method_declaration": true, "constructor_declaration": true},
	"ts":     {"method_definition": true, "function_declaration": true},
	"js":     {"method_definition": true, "function_declaration": true},
	"py":     {"function_definition": true},
	"rust":   {"function_item": true},
	"cpp":    {"function_definition": true},
	"kotlin": {"function_declaration": true},
}

// astCallNodeTypes 各语言「调用表达式」的 AST 节点类型集合。
var astCallNodeTypes = map[string]map[string]bool{
	"go":     {"call_expression": true},
	"java":   {"method_invocation": true},
	"ts":     {"call_expression": true},
	"js":     {"call_expression": true},
	"py":     {"call": true},
	"rust":   {"call_expression": true},
	"cpp":    {"call_expression": true},
	"kotlin": {"call_expression": true},
}

// Parse 解析单个源文件，返回归一化 IR（基于 AST 语法级提取）。
func (a *TreeSitterAdapter) Parse(ctx context.Context, path string) (*parser.IRDocument, error) {
	ext := strings.ToLower(filepath.Ext(path))
	lang := adapter.ExtToLang(ext)
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

	classTypes := astClassNodeTypes[lang]
	methodTypes := astMethodNodeTypes[lang]
	callTypes := astCallNodeTypes[lang]

	var curClass *parser.ClassIR
	var walk func(n *ts.Node)
	walk = func(n *ts.Node) {
		if n == nil || n.IsNull() {
			return
		}
		typ := n.Type()
		start := n.StartPoint()

		switch {
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

		case methodTypes[typ]:
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

		case callTypes[typ]:
			callee := astCalleeName(n, data)
			if callee == "" || isKeyword(callee) {
				break
			}
			doc.Calls = append(doc.Calls, parser.CallIR{
				CalleeFQN:  callee,
				CallType:   "direct",
				LineNumber: int(start.Row) + 1,
			})
		}

		for i := 0; i < int(n.NamedChildCount()); i++ {
			walk(n.NamedChild(i))
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
//     但头部有 simple_identifier；注意避开返回类型/参数中的 identifier）。
//
// 兜底：子树中第一个 identifier/type_identifier 文本。
func astNodeName(n *ts.Node, lang string, src []byte) string {
	if name := n.ChildByFieldName("name"); name != nil && !name.IsNull() {
		return string(name.Content(src))
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
		if c.Type() == "identifier" || c.Type() == "simple_identifier" || c.Type() == "type_identifier" {
			return string(c.Content(src))
		}
		// 嵌套一层（如 modifiers + name 组合）
		if inner := firstIdentifierText(c, src); inner != "" {
			return inner
		}
	}
	return firstIdentifierText(n, src)
}

// astClassType 从 AST 节点类型映射 IR 类类型。
func astClassType(nodeType, lang string) string {
	switch nodeType {
	case "interface_declaration", "trait_item":
		return "INTERFACE"
	case "enum_declaration", "enum_item", "enum_specifier":
		return "ENUM"
	case "object_declaration":
		return "OBJECT"
	default:
		return "CLASS"
	}
}

// astCalleeName 提取调用表达式的被调方名。
//
// 通用策略：取调用表达式文本中第一个 '(' 之前的部分（如 `paymentService.pay(order)`
// → `paymentService.pay`；`repository.findById(id)` → `repository.findById`），
// 覆盖 Java method_invocation / Kotlin·Go call_expression / Python call 等所有语言的
// 对象方法调用与命名空间调用形式。
func astCalleeName(n *ts.Node, src []byte) string {
	text := string(n.Content(src))
	if idx := strings.Index(text, "("); idx >= 0 {
		return strings.TrimSpace(text[:idx])
	}
	return ""
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
	}
	return keywords[name]
}
