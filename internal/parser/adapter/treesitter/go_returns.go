package treesitter

import (
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
)

// goFuncSigPattern 匹配包级函数声明的单一返回类型（多返回值/命名返回值不计）。
var goFuncSigPattern = regexp.MustCompile(`(?m)^func\s+(\w+)\s*\([^)]*\)\s*(\([^)]*\)|[\w.*\[\]]+)\s*\{`)

// goBuiltinTypes 非「类/结构体」的内建返回类型，不参与 receiver 消歧。
var goBuiltinTypes = map[string]bool{
	"string": true, "error": true, "bool": true, "byte": true, "rune": true,
	"int": true, "int8": true, "int16": true, "int32": true, "int64": true,
	"uint": true, "uint8": true, "uint16": true, "uint32": true, "uint64": true,
	"uintptr": true, "float32": true, "float64": true, "complex64": true, "complex128": true,
	"any": true, "interface": true, "func": true, "map": true, "chan": true,
}

// scanGoFuncReturns 预扫描 Go 源文件，收集包级函数的单一具名返回类型。
//
// 只接受 `*T` / `T` 形态且 T 不是内建类型；多返回值、命名返回值、
// 泛型/带包路径/切片等形态一律跳过（不做不可靠推断）。
// 正则与 AST 两条路径共用（`x := helper()` 的接收者类型推导）。
func scanGoFuncReturns(data []byte) map[string]string {
	out := map[string]string{}
	for _, m := range goFuncSigPattern.FindAllStringSubmatch(string(data), -1) {
		typ := strings.TrimSpace(m[2])
		if typ == "" || strings.HasPrefix(typ, "(") {
			continue
		}
		if strings.ContainsAny(typ, ".[] ") {
			continue
		}
		typ = strings.TrimPrefix(typ, "*")
		if goBuiltinTypes[typ] {
			continue
		}
		out[m[1]] = typ
	}
	return out
}

// goReturnsCache 缓存「目录 → 同包函数返回类型」。键含目录 mtime 与 `.go` 文件数，
// 目录静态时同一目录只扫描一次，避免每个文件都重读同包全部文件（O(N²)）。
var goReturnsCache sync.Map

// scanGoPkgReturns 收集同包（同目录全部 `.go`）的包级函数返回类型。
//
// 跨文件引用（`svc := NewService(st)`，NewService 定义在 service.go）无法从
// 当前文件推导，需要同包签名。返回的 map 只读，调用方合并前须自行复制。
func scanGoPkgReturns(path string) map[string]string {
	dir := filepath.Dir(path)
	entries, err := os.ReadDir(dir)
	if err != nil {
		return map[string]string{}
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(e.Name(), ".go") {
			n++
		}
	}
	key := dir + "|" + dirStatKey(dir) + "|" + strconv.Itoa(n)
	if v, ok := goReturnsCache.Load(key); ok {
		return v.(map[string]string)
	}
	out := map[string]string{}
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(e.Name(), ".go") {
			continue
		}
		data, rerr := os.ReadFile(filepath.Join(dir, e.Name()))
		if rerr != nil {
			continue
		}
		for k, v := range scanGoFuncReturns(data) {
			out[k] = v
		}
	}
	goReturnsCache.Store(key, out)
	return out
}

// dirStatKey 返回目录 mtime 的稳定字符串（取不到时回退空串，等价于不缓存失效）。
func dirStatKey(dir string) string {
	info, err := os.Stat(dir)
	if err != nil {
		return ""
	}
	return info.ModTime().String()
}

// mergeGoReturns 合并同包与当前文件签名，当前文件同名项优先。
func mergeGoReturns(pkg, local map[string]string) map[string]string {
	if len(local) == 0 {
		return pkg
	}
	out := make(map[string]string, len(pkg)+len(local))
	for k, v := range pkg {
		out[k] = v
	}
	for k, v := range local {
		out[k] = v
	}
	return out
}
