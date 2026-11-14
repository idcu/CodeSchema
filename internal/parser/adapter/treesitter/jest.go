package treesitter

import (
	"path/filepath"
	"regexp"
	"strings"

	"github.com/idcu/codeschema/internal/parser"
)

// Jest `describe/it/test` 回调建模。
//
// 回调不是声明式语法节点：正则路径只看到一行调用，AST 路径只看到 call_expression。
// 两者都把回调合成 MethodIR（ClassFQN = 模块名，Name = describe 路径 + it/test 标题），
// 使回调体内的调用获得 CallerFQN，从而让「测试方法 → 生产方法」的调用边在 IR 中可见。
// 文件级关联方案不建模方法，无法复用 call_graph 策略与影响面管道，见 bench 文档的取舍说明。

// tmpJestDeclPattern 匹配单行内联的 Jest 声明：describe('...' / it('...' / test('...'。
// 只接受字符串字面量标题（模板字符串/变量标题无法静态命名，不建模）。
var tmpJestDeclPattern = regexp.MustCompile(`\b(describe|it|test)(?:\.(?:each|only|skip|concurrent|todo|failing))?\s*\(\s*["'` + "`" + `]([^"'` + "`" + `]*)["'` + "`" + `]`)

// tmpImportPattern 匹配具名导入 `import { a, b as c } from './x.ts'`。
var tmpImportPattern = regexp.MustCompile(`import\s*(?:type\s*)?\{([^}]*)\}\s*from\s*["']([^"']+)["']`)

// jestScope 是正则路径的 Jest 花括号作用域：openDepth 为进入该行前的净深度，
// 深度回落到 openDepth 即表示对应回调/块闭合。
type jestScope struct {
	kind      string // "describe" | "test"
	openDepth int
	methodIdx int // test 作用域对应的 doc.Methods 下标；describe 为 -1
}

// tsControlKeywords 是 TS/JS 中「形如调用但不是调用」的控制流/声明关键字。
//
// 全局 isKeyword 混合了多语言标准库名（repeat、map、get…），对 TS 会误伤
// `repeat(...)` 这类合法导出函数；TS 只按本语言关键字过滤。
var tsControlKeywords = map[string]bool{
	"if": true, "for": true, "while": true, "switch": true, "catch": true,
	"function": true, "return": true, "typeof": true, "instanceof": true,
	"new": true, "delete": true, "void": true, "await": true, "yield": true,
	"throw": true, "do": true, "else": true, "try": true, "finally": true,
	"import": true, "export": true, "super": true, "this": true,
	// 声明式构造器签名 `constructor();` 不是调用（JS 构造调用必须带 new）。
	"constructor": true,
	// 声明修饰符，不会以调用形式出现。
	"async": true,
}

// isNonCallName 判断 callee 裸名是否为当前语言的「非调用」关键字。
func isNonCallName(name, lang string) bool {
	if lang == "ts" || lang == "js" {
		return tsControlKeywords[name]
	}
	return isKeyword(name)
}

// tsModuleName 返回 TS/JS 文件的模块名（文件名去扩展名），与顶层函数 FQN 的限定口径一致。
func tsModuleName(path string) string {
	base := filepath.Base(path)
	return strings.TrimSuffix(base, filepath.Ext(base))
}

// isTSDeclarationFile 判断是否为 TypeScript 声明文件（.d.ts / .d.mts / .d.cts）。
// 声明文件只有类型契约（函数签名、接口成员），没有可执行体：
// 既不能登记为方法，也不能产生调用边。
func isTSDeclarationFile(path string) bool {
	lower := strings.ToLower(path)
	return strings.HasSuffix(lower, ".d.ts") || strings.HasSuffix(lower, ".d.mts") || strings.HasSuffix(lower, ".d.cts")
}

// tsTypeOnlyScope 判断类作用域是否只有类型契约（interface/type）。
// 其成员没有实现，不得登记为方法或调用边。
func tsTypeOnlyScope(c *parser.ClassIR) bool {
	return c != nil && (c.Type == "INTERFACE" || c.Type == "TYPE")
}

// matchJestDecl 判断一行是否为 Jest 块声明，返回 "describe"/"test" 与标题。
func matchJestDecl(line string) (kind, title string, ok bool) {
	m := tmpJestDeclPattern.FindStringSubmatch(line)
	if len(m) < 3 {
		return "", "", false
	}
	if m[1] == "describe" {
		return "describe", m[2], true
	}
	return "test", m[2], true
}

// jestDeclKind 判断 AST 调用节点的函数名是否 Jest 块声明（含 it.only / test.each 形式）。
func jestDeclKind(fnName string) (kind string, ok bool) {
	switch fnName {
	case "describe":
		return "describe", true
	case "it", "test":
		return "test", true
	}
	return "", false
}

// sanitizeJestName 把标题转为稳定的标识符片段：非字母数字折叠为单个下划线。
// 例：`greets a name with capitalization` → `greets_a_name_with_capitalization`。
func sanitizeJestName(s string) string {
	var b strings.Builder
	underscore := false
	for _, r := range s {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			b.WriteRune(r)
			underscore = false
		default:
			if b.Len() > 0 && !underscore {
				b.WriteByte('_')
				underscore = true
			}
		}
	}
	return strings.Trim(b.String(), "_")
}

// jestMethodName 合成测试方法名：describe 路径与 it/test 标题以下划线连接。
func jestMethodName(describes []string, title string) string {
	parts := make([]string, 0, len(describes)+1)
	for _, d := range describes {
		if s := sanitizeJestName(d); s != "" {
			parts = append(parts, s)
		}
	}
	if s := sanitizeJestName(title); s != "" {
		parts = append(parts, s)
	}
	return strings.Join(parts, "_")
}

// parseTSImports 解析具名导入，返回 本地名 → 模块名（导入路径 basename）。
//
// 测试文件通过相对路径导入生产模块（`../src/utils/math.ts`）；把裸 callee 限定为
// `math.add` 后，跨模块同名函数（多个 index.ts 都导出 add）才能准确关联。
func parseTSImports(src []byte) map[string]string {
	out := map[string]string{}
	for _, m := range tmpImportPattern.FindAllSubmatch(src, -1) {
		mod := tsImportModule(string(m[2]))
		if mod == "" {
			continue
		}
		for _, item := range strings.Split(string(m[1]), ",") {
			name := strings.TrimSpace(item)
			if name == "" {
				continue
			}
			if i := strings.Index(name, " as "); i > 0 {
				name = strings.TrimSpace(name[i+4:])
			}
			if isTSIdent(name) {
				out[name] = mod
			}
		}
	}
	return out
}

// tsImportModule 从导入路径取模块名：`../src/utils/math.ts` → `math`。
func tsImportModule(spec string) string {
	spec = strings.TrimSpace(spec)
	spec = strings.TrimSuffix(spec, "/")
	// 只解析项目内的相对/绝对路径导入；包名（react、@jest/globals）不产生本地模块。
	if spec == "" || (!strings.HasPrefix(spec, ".") && !strings.HasPrefix(spec, "/")) {
		return ""
	}
	base := filepath.Base(spec)
	if i := strings.IndexAny(base, "?"); i >= 0 {
		base = base[:i]
	}
	base = strings.TrimSuffix(base, filepath.Ext(base))
	if base == "." || base == ".." || base == "/" {
		return ""
	}
	return base
}

func isTSIdent(s string) bool {
	if s == "" {
		return false
	}
	for i, r := range s {
		if i == 0 && !(r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z')) {
			return false
		}
		if i > 0 && !(r == '_' || r == '$' || (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9')) {
			return false
		}
	}
	return true
}

// qualifyTSModuleCalls 把模块作用域内的裸名调用限定为 `module.func` 或 `导入模块.func`。
//
// 只处理 caller 属于本模块的调用（生产顶层函数 / Jest 回调），导入函数保持裸名以外
// 不做猜测：没有 import 映射的裸名不改写，避免把第三方 API 误挂到本地同名函数。
func qualifyTSModuleCalls(doc *parser.IRDocument, module string, imports map[string]string) {
	if module == "" {
		return
	}
	local := make(map[string]struct{})
	for _, m := range doc.Methods {
		if m.ClassFQN == module {
			local[m.Name] = struct{}{}
		}
	}
	prefix := module + "."
	for i := range doc.Calls {
		c := &doc.Calls[i]
		if !strings.HasPrefix(c.CallerFQN, prefix) || strings.Contains(c.CalleeFQN, ".") {
			continue
		}
		if _, ok := local[c.CalleeFQN]; ok {
			c.CalleeFQN = prefix + c.CalleeFQN
			continue
		}
		if mod := imports[c.CalleeFQN]; mod != "" {
			c.CalleeFQN = mod + "." + c.CalleeFQN
		}
	}
}
