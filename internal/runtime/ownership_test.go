package runtime

import (
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// constructorRules 声明运行时组件的唯一构造点。
//
// 这不是风格检查：Searcher / IndexBuilder / Service 都持有构造时载入的内存索引状态，
// 一旦同类型出现第二个实例，增量会写在没人读的副本上（/search、/viz 永远陈旧）。
var constructorRules = map[string]string{
	"service.NewService":     "BuildRuntime",
	"search.NewSearcher":     "newSearcherWithStore",
	"search.NewIndexBuilder": "newSearcherWithStore",
}

type constructorCall struct {
	file string
	fn   string
	expr string
}

// collectConstructorCalls 解析目录下全部非测试 .go 文件，收集顶层函数内的构造调用。
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
			base := filepath.Base(fileName)
			for _, decl := range file.Decls {
				fd, ok := decl.(*ast.FuncDecl)
				if !ok || fd.Body == nil {
					continue
				}
				ast.Inspect(fd.Body, func(n ast.Node) bool {
					call, ok := n.(*ast.CallExpr)
					if !ok {
						return true
					}
					if name := callExprName(call); name != "" {
						out = append(out, constructorCall{file: base, fn: fd.Name.Name, expr: name})
					}
					return true
				})
			}
		}
	}
	return out
}

// callExprName 返回 pkg.Func 或 Func 形式的调用名（其他形态返回空串）。
func callExprName(call *ast.CallExpr) string {
	switch fun := call.Fun.(type) {
	case *ast.SelectorExpr:
		if id, ok := fun.X.(*ast.Ident); ok {
			return id.Name + "." + fun.Sel.Name
		}
	case *ast.Ident:
		return fun.Name
	}
	return ""
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

// TestRuntimeSingleConstructionPoint 守卫「副本实例」错误形态：
// 运行时组件只允许在指定函数里各构造一次，且不得再暴露第二个导出构造入口。
func TestRuntimeSingleConstructionPoint(t *testing.T) {
	calls := collectConstructorCalls(t, ".")
	counts := make(map[string]int)
	for _, c := range calls {
		wantFn, guarded := constructorRules[c.expr]
		if !guarded {
			continue
		}
		counts[c.expr]++
		if c.fn != wantFn {
			t.Errorf("%s:%s 在 %s 中构造 %s；运行时组件只允许在 %s 单点构造",
				c.file, c.fn, c.fn, c.expr, wantFn)
		}
	}
	for expr, wantFn := range constructorRules {
		if counts[expr] != 1 {
			t.Errorf("%s 在 runtime 包内构造点数量 = %d，期望 1（唯一构造点：%s）；"+
				"出现第二个实例会让增量写进没人读的副本", expr, counts[expr], wantFn)
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

// TestCmdDoesNotConstructRuntimeComponents cmd 层不得自建运行时组件，
// 必须经 BuildRuntime（单项目）或 tenant.Manager（多租户）取同一批实例。
func TestCmdDoesNotConstructRuntimeComponents(t *testing.T) {
	forbidden := map[string]bool{
		"service.NewService":     true,
		"rt.NewSearcher":         true,
		"search.NewSearcher":     true,
		"search.NewIndexBuilder": true,
	}
	for _, c := range collectConstructorCalls(t, filepath.Join("..", "..", "cmd", "codeschema")) {
		if forbidden[c.expr] {
			t.Errorf("%s:%s 直接构造 %s；cmd 只能通过 runtime.BuildRuntime / tenant.Manager 取组件",
				c.file, c.fn, c.expr)
		}
	}
}
