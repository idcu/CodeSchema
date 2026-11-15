package runtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

const (
	serviceImportPath = "github.com/idcu/codeschema/internal/service"
	searchImportPath  = "github.com/idcu/codeschema/internal/search"
)

// constructorRules 声明运行时组件的唯一构造点（key = 真实 import path + 函数名）。
//
// 这不是风格检查：Searcher / IndexBuilder / Service 都持有构造时载入的内存索引状态，
// 一旦同类型出现第二个实例，增量会写在没人读的副本上（/search、/viz 永远陈旧）。
//
// 按 import path 而非标识符匹配：`import svc ".../service"` 这类别名导入
// 同样会被识别；包级 var 初始化与函数字面量里的调用也会被扫描。
var constructorRules = map[string][]string{
	serviceImportPath + ".NewService":     {"BuildRuntime", "BuildReadOnlyService"},
	searchImportPath + ".NewSearcher":     {"newSearcherWithStore"},
	searchImportPath + ".NewIndexBuilder": {"newSearcherWithStore"},
}

type constructorCall struct {
	file string // 文件名
	decl string // 所在顶层声明：函数名或 "var:<name>"
	expr string // 解析后的 "import/path.Func"
}

// importAliases 返回文件内「本地标识符 → 真实 import path」的映射。
func importAliases(file *ast.File) map[string]string {
	aliases := make(map[string]string)
	for _, imp := range file.Imports {
		if imp.Path == nil {
			continue
		}
		p, err := strconv.Unquote(imp.Path.Value)
		if err != nil {
			continue
		}
		name := path.Base(p)
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if name == "_" || name == "." {
			continue
		}
		aliases[name] = p
	}
	return aliases
}

// scanFileConstructors 扫描单个文件内的构造函数调用（含包级 var 初始化）。
//
// 返回的 expr 是解析后的真实包路径，调用方据此比对 constructorRules。
func scanFileConstructors(fileName string, file *ast.File) []constructorCall {
	aliases := importAliases(file)
	var out []constructorCall

	// declName 返回节点所属顶层声明名；包级 var 统一标记为 "var:<name>"。
	declName := func(n ast.Node) string {
		switch d := n.(type) {
		case *ast.FuncDecl:
			return d.Name.Name
		case *ast.GenDecl:
			if d.Tok == token.VAR {
				for _, spec := range d.Specs {
					if vs, ok := spec.(*ast.ValueSpec); ok && len(vs.Names) > 0 {
						return "var:" + vs.Names[0].Name
					}
				}
				return "var"
			}
		}
		return "<file>"
	}

	scan := func(node ast.Node, owner string) {
		ast.Inspect(node, func(n ast.Node) bool {
			call, ok := n.(*ast.CallExpr)
			if !ok {
				return true
			}
			sel, ok := call.Fun.(*ast.SelectorExpr)
			if !ok {
				return true
			}
			id, ok := sel.X.(*ast.Ident)
			if !ok {
				return true
			}
			pkg, ok := aliases[id.Name]
			if !ok {
				return true
			}
			out = append(out, constructorCall{
				file: filepath.Base(fileName),
				decl: owner,
				expr: pkg + "." + sel.Sel.Name,
			})
			return true
		})
	}

	for _, decl := range file.Decls {
		switch d := decl.(type) {
		case *ast.FuncDecl:
			if d.Body != nil {
				scan(d.Body, declName(d))
			}
		case *ast.GenDecl:
			if d.Tok == token.VAR {
				scan(d, declName(d))
			}
		}
	}
	return out
}

// collectConstructorCalls 解析目录下全部非测试 .go 文件，收集构造调用。
func collectConstructorCalls(t *testing.T, dir string) []constructorCall {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse dir %s: %v", dir, err)
	}

	var out []constructorCall
	for _, pkg := range pkgs {
		for fileName, file := range pkg.Files {
			out = append(out, scanFileConstructors(fileName, file)...)
		}
	}
	return out
}

// declaredFuncs 返回目录下非测试 .go 文件里的顶层函数名集合。
func declaredFuncs(t *testing.T, dir string) map[string]bool {
	t.Helper()
	fset := token.NewFileSet()
	pkgs, err := parser.ParseDir(fset, dir, func(fi os.FileInfo) bool {
		return !strings.HasSuffix(fi.Name(), "_test.go")
	}, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse dir %s: %v", dir, err)
	}
	names := make(map[string]bool)
	for _, pkg := range pkgs {
		for _, file := range pkg.Files {
			for _, decl := range file.Decls {
				if fd, ok := decl.(*ast.FuncDecl); ok {
					names[fd.Name.Name] = true
				}
			}
		}
	}
	return names
}

// TestConstructorGuardCatchesAliasAndPackageVar 守卫自证：
// 别名导入 + 包级 var 初始化里的构造调用必须被识别（旧守卫只扫 FuncDecl.Body、
// 按标识符匹配，这两种形态都会漏掉）。
func TestConstructorGuardCatchesAliasAndPackageVar(t *testing.T) {
	src := `package probe

import (
	svc "github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/search"
)

var _ = svc.NewService(nil)

func assemble() *search.Searcher {
	return search.NewSearcher(nil, nil, nil)
}
`
	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "probe.go", src, parser.SkipObjectResolution)
	if err != nil {
		t.Fatalf("parse probe: %v", err)
	}
	calls := scanFileConstructors("probe.go", file)
	seen := make(map[string]string)
	for _, c := range calls {
		seen[c.expr] = c.decl
	}
	if decl, ok := seen[serviceImportPath+".NewService"]; !ok || decl != "var:_" {
		t.Errorf("未识别包级 var 中的别名导入构造：%v", calls)
	}
	if _, ok := seen[searchImportPath+".NewSearcher"]; !ok {
		t.Errorf("未识别函数体中的构造调用：%v", calls)
	}
}

// TestRuntimeSingleConstructionPoint 守卫「副本实例」错误形态：
// 运行时组件只允许在指定函数里构造，且不得再暴露第二个导出构造入口。
func TestRuntimeSingleConstructionPoint(t *testing.T) {
	calls := collectConstructorCalls(t, ".")
	counts := make(map[string]int)
	for _, c := range calls {
		allowed, guarded := constructorRules[c.expr]
		if !guarded {
			continue
		}
		counts[c.expr]++
		ok := false
		for _, name := range allowed {
			if c.decl == name {
				ok = true
				break
			}
		}
		if !ok {
			t.Errorf("%s:%s 在 %s 中构造 %s；运行时组件只允许在 %v 中构造",
				c.file, c.decl, c.decl, c.expr, allowed)
		}
	}
	for expr, allowed := range constructorRules {
		if counts[expr] != len(allowed) {
			t.Errorf("%s 在 runtime 包内构造点数量 = %d，期望 %d（唯一构造点：%v）；"+
				"出现第二个实例会让增量写进没人读的副本", expr, counts[expr], len(allowed), allowed)
		}
	}

	// 导出构造函数会让仓内其他包（cmd / tenant / 新代码）绕过 BuildRuntime 再建副本。
	funcs := declaredFuncs(t, ".")
	for _, name := range []string{"NewSearcher", "NewSearcherWithStore"} {
		if funcs[name] {
			t.Errorf("runtime 包仍声明导出构造函数 %s；必须改为非导出并仅由 BuildRuntime 装配", name)
		}
	}
}

// TestNoRuntimeComponentsOutsideAssemblers 仓内其他包不得自建运行时组件，
// 必须经 BuildRuntime（单项目）、BuildReadOnlyService（离线只读工具）或
// tenant.Manager（多租户）取同一批实例。
func TestNoRuntimeComponentsOutsideAssemblers(t *testing.T) {
	for _, dir := range []string{
		filepath.Join("..", "..", "cmd", "codeschema"),
		filepath.Join("..", "tenant"),
		filepath.Join("..", "releasebench"),
	} {
		for _, c := range collectConstructorCalls(t, dir) {
			if _, guarded := constructorRules[c.expr]; guarded {
				t.Errorf("%s:%s(%s) 直接构造 %s；只能通过 runtime 装配入口取组件",
					dir, c.file, c.decl, c.expr)
			}
		}
	}
}
