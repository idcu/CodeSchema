package server

import (
	"encoding/json"
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

// TestOpenAPIParametersMatchHandlers 判据：OpenAPI 声明的 query 参数必须与
// handler 实际读取的参数一致（名称、类型双向一致）。
//
// 漂移历史：/context 的 budget_tokens handler 已读但规范漏声明；/tests 的
// min_confidence 实现按整数解析但规范写成 number。静态扫描 server 包源码，
// 沿着 handler → 本地 helper 的调用递归收集参数，再与 openapiJSON 比对。
//
// 跨切面参数 tenant 由 resolveService 从 query 读取（也支持 X-Tenant 头），
// 不属于单个端点契约，比对时排除。
func TestOpenAPIParametersMatchHandlers(t *testing.T) {
	funcs, routes := scanServerParams(t)

	var spec struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name   string `json:"name"`
				In     string `json:"in"`
				Schema struct {
					Type string `json:"type"`
				} `json:"schema"`
			} `json:"parameters"`
		} `json:"paths"`
	}
	if err := json.Unmarshal(openapiJSON, &spec); err != nil {
		t.Fatalf("openapiJSON 解析失败: %v", err)
	}

	for path, ops := range spec.Paths {
		op, ok := ops["get"]
		if !ok {
			continue
		}
		handler, ok := routes[path]
		if !ok {
			t.Errorf("%s 在 OpenAPI 中声明但未找到路由注册", path)
			continue
		}
		declared := make(map[string]string, len(op.Parameters))
		for _, p := range op.Parameters {
			if p.In == "query" {
				declared[p.Name] = p.Schema.Type
			}
		}

		read := resolveHandlerParams(funcs, handler)
		for name := range declared {
			if name == "tenant" {
				continue
			}
			if !read.keys[name] {
				t.Errorf("%s (%s): OpenAPI 声明了 query 参数 %q，但 handler 未读取", path, handler, name)
			}
		}
		for name := range read.keys {
			if name == "tenant" {
				continue
			}
			if _, ok := declared[name]; !ok {
				t.Errorf("%s (%s): handler 读取了 query 参数 %q，但 OpenAPI 未声明", path, handler, name)
			}
			if typ, ok := declared[name]; ok {
				want := ""
				switch {
				case read.intKeys[name]:
					want = "integer"
				case read.numberKeys[name]:
					want = "number"
				case read.boolKeys[name]:
					want = "boolean"
				}
				if want != "" && typ != want {
					t.Errorf("%s (%s): 参数 %q 声明类型 %q，handler 按 %s 解析", path, handler, name, typ, want)
				}
			}
		}
	}
}

// handlerParams 单个函数的直接参数读取与本地调用。
type handlerParams struct {
	keys       map[string]bool
	intKeys    map[string]bool
	numberKeys map[string]bool
	boolKeys   map[string]bool
	calls      map[string]bool
}

func newHandlerParams() *handlerParams {
	return &handlerParams{
		keys:       map[string]bool{},
		intKeys:    map[string]bool{},
		numberKeys: map[string]bool{},
		boolKeys:   map[string]bool{},
		calls:      map[string]bool{},
	}
}

// resolveHandlerParams 递归合并 handler 所有可达本地函数的参数读取。
func resolveHandlerParams(funcs map[string]*handlerParams, name string) *handlerParams {
	out := newHandlerParams()
	seen := map[string]bool{}
	var walk func(string)
	walk = func(fn string) {
		if seen[fn] {
			return
		}
		seen[fn] = true
		info, ok := funcs[fn]
		if !ok {
			return
		}
		for k := range info.keys {
			out.keys[k] = true
		}
		for k := range info.intKeys {
			out.intKeys[k] = true
		}
		for k := range info.numberKeys {
			out.numberKeys[k] = true
		}
		for k := range info.boolKeys {
			out.boolKeys[k] = true
		}
		for callee := range info.calls {
			walk(callee)
		}
	}
	walk(name)
	return out
}

// scanServerParams 静态扫描 server 包（不含测试），返回函数参数表与路由映射。
func scanServerParams(t *testing.T) (map[string]*handlerParams, map[string]string) {
	t.Helper()
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}
	fset := token.NewFileSet()
	var parsed []*ast.File
	funcs := map[string]*handlerParams{}
	for _, f := range files {
		if strings.HasSuffix(f, "_test.go") {
			continue
		}
		af, err := parser.ParseFile(fset, f, nil, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", f, err)
		}
		parsed = append(parsed, af)
		for _, decl := range af.Decls {
			if fd, ok := decl.(*ast.FuncDecl); ok {
				funcs[fd.Name.Name] = newHandlerParams()
			}
		}
	}

	routes := map[string]string{}
	for _, af := range parsed {
		for _, decl := range af.Decls {
			fd, ok := decl.(*ast.FuncDecl)
			if !ok || fd.Body == nil {
				continue
			}
			info := funcs[fd.Name.Name]
			collectParams(fd.Body, info, funcs)
			collectRoutes(fd.Body, routes)
		}
	}
	return funcs, routes
}

// collectParams 收集函数体内直接读取的 query 参数与本地调用。
func collectParams(body *ast.BlockStmt, info *handlerParams, funcs map[string]*handlerParams) {
	ast.Inspect(body, func(n ast.Node) bool {
		switch x := n.(type) {
		case *ast.CallExpr:
			name := calleeName(x.Fun)
			switch name {
			case "Get":
				if k, ok := stringArg(x.Args, 0); ok {
					info.keys[k] = true
				}
			case "querySymbols":
				// querySymbols(q, plural, single)：两个字面量都是参数名。
				if k, ok := stringArg(x.Args, 1); ok {
					info.keys[k] = true
				}
				if k, ok := stringArg(x.Args, 2); ok {
					info.keys[k] = true
				}
			case "parseQueryTags":
				info.keys["tag"] = true
			}
			switch name {
			case "Atoi", "ParseInt", "atoiOr":
				markGetKeys(x, info.intKeys)
			case "ParseFloat", "atofOr":
				markGetKeys(x, info.numberKeys)
			case "ParseBool":
				markGetKeys(x, info.boolKeys)
			}
			if id, ok := x.Fun.(*ast.Ident); ok {
				if _, local := funcs[id.Name]; local {
					info.calls[id.Name] = true
				}
			}
		case *ast.BinaryExpr:
			// q.Get("recursive") == "true" / == "1"
			if k, ok := getKeyInExpr(x.X); ok {
				if lit, ok := x.Y.(*ast.BasicLit); ok && lit.Kind == token.STRING {
					v, _ := strconv.Unquote(lit.Value)
					if v == "true" || v == "false" || v == "1" || v == "0" {
						info.boolKeys[k] = true
					}
				}
			}
		}
		return true
	})
}

func collectRoutes(body *ast.BlockStmt, routes map[string]string) {
	ast.Inspect(body, func(n ast.Node) bool {
		call, ok := n.(*ast.CallExpr)
		if !ok || calleeName(call.Fun) != "HandleFunc" || len(call.Args) < 2 {
			return true
		}
		path, ok := stringArg(call.Args, 0)
		if !ok {
			return true
		}
		switch h := call.Args[1].(type) {
		case *ast.SelectorExpr:
			routes[path] = h.Sel.Name
		case *ast.Ident:
			routes[path] = h.Name
		}
		return true
	})
}

func calleeName(fun ast.Expr) string {
	switch f := fun.(type) {
	case *ast.Ident:
		return f.Name
	case *ast.SelectorExpr:
		return f.Sel.Name
	}
	return ""
}

func stringArg(args []ast.Expr, i int) (string, bool) {
	if i >= len(args) {
		return "", false
	}
	lit, ok := args[i].(*ast.BasicLit)
	if !ok || lit.Kind != token.STRING {
		return "", false
	}
	s, err := strconv.Unquote(lit.Value)
	if err != nil {
		return "", false
	}
	return s, true
}

// markGetKeys 收集表达式内所有 `.Get("literal")` 的 key。
func markGetKeys(call *ast.CallExpr, dst map[string]bool) {
	for _, arg := range call.Args {
		ast.Inspect(arg, func(n ast.Node) bool {
			c, ok := n.(*ast.CallExpr)
			if !ok || calleeName(c.Fun) != "Get" {
				return true
			}
			if k, ok := stringArg(c.Args, 0); ok {
				dst[k] = true
			}
			return true
		})
	}
}

// getKeyInExpr 若表达式为 `x.Get("key")` 则返回 key。
func getKeyInExpr(e ast.Expr) (string, bool) {
	call, ok := e.(*ast.CallExpr)
	if !ok || calleeName(call.Fun) != "Get" {
		return "", false
	}
	return stringArg(call.Args, 0)
}
