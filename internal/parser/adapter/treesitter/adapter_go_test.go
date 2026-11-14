package treesitter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
)

// TestGoCallAndTypeExtraction 实证 + 回归：验证 Go 适配器
//  1. 泛型类型 type X[T any] struct 应被识别为 class；
//  2. 调用边 CallerFQN 应带包前缀限定（FQN 命名空间对齐，使影响面分析可查）：
//     - NewWatcher（顶层函数）体内的 w.ReloadNow() → caller=config.NewWatcher；
//     （w 是局部变量而非 receiver，类型不可消歧，callee 保持 w.ReloadNow）
//     - ReloadNow（Watcher 方法，receiver w）体内的 loadConfig() →
//     caller=config.Watcher.ReloadNow、callee=config.loadConfig（包级函数包限定）；
//  3. 自接收者调用（recv == 当前方法 receiver 变量）会被限定为 pkg.RecvType.Method；
//     其余 receiver.Method（如局部变量 w.ReloadNow）保持原样（类型推导范畴）；
//  4. 顶层函数 NewWatcher 归入合成包级容器（ClassFQN = package 名），不再被误挂到
//     最近的真实 class（如 Watcher）——P0-1：非空 ClassFQN 才能被存储后端入库。
func TestGoCallAndTypeExtraction(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "watcher.go")
	src := `package config

type Watcher[T any] struct {
	path string
}

func NewWatcher(path string) *Watcher[T] {
	w := &Watcher[T]{path: path}
	w.ReloadNow()
	return w
}

func (w *Watcher[T]) ReloadNow() {
	loadConfig(w.path)
}
`
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewTreeSitterAdapter()
	doc, err := a.Parse(context.Background(), p)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	t.Logf("CLASSES:")
	for _, c := range doc.Classes {
		t.Logf("  name=%q full_name=%q type=%q", c.Name, c.FullName, c.Type)
	}
	t.Logf("METHODS:")
	for _, m := range doc.Methods {
		t.Logf("  name=%q class_fqn=%q sig=%q", m.Name, m.ClassFQN, m.Signature)
	}
	t.Logf("CALLS:")
	for _, c := range doc.Calls {
		t.Logf("  caller=%q callee=%q type=%q line=%d", c.CallerFQN, c.CalleeFQN, c.CallType, c.LineNumber)
	}

	// 1. 泛型类型被识别为 class
	hasWatcherClass := false
	for _, c := range doc.Classes {
		if c.Name == "Watcher" {
			hasWatcherClass = true
		}
	}
	if !hasWatcherClass {
		t.Errorf("泛型类型 Watcher[T] 未被识别为 class；当前 classes=%v", doc.Classes)
	}

	// 2. 调用归属精确断言（包限定 FQN）：
	//    config.NewWatcher -> config.Watcher.ReloadNow（w 为 &Watcher[T]{} 局部变量，类型可推导）；
	//    config.Watcher.ReloadNow -> config.loadConfig（包级函数包限定）。
	hasNewWatcherCall := false
	hasReloadNowCall := false
	for _, c := range doc.Calls {
		if c.CallerFQN == "config.NewWatcher" && c.CalleeFQN == "config.Watcher.ReloadNow" {
			hasNewWatcherCall = true
		}
		if c.CallerFQN == "config.Watcher.ReloadNow" && c.CalleeFQN == "config.loadConfig" {
			hasReloadNowCall = true
		}
	}
	if !hasNewWatcherCall {
		t.Errorf("期望调用 config.NewWatcher -> config.Watcher.ReloadNow；当前 calls=%v", doc.Calls)
	}
	if !hasReloadNowCall {
		t.Errorf("期望调用 config.Watcher.ReloadNow -> config.loadConfig；当前 calls=%v", doc.Calls)
	}

	// 3. 顶层函数归入合成包级容器（ClassFQN = 包名），不挂到真实 class（Watcher）
	var containerName string
	for _, c := range doc.Classes {
		if c.Type == "PACKAGE" {
			containerName = c.FullName
		}
	}
	if containerName != "config" {
		t.Fatalf("期望合成包级容器 FullName=config，当前 classes=%v", doc.Classes)
	}
	for _, m := range doc.Methods {
		if m.Name == "NewWatcher" && m.ClassFQN != containerName {
			t.Errorf("顶层函数 NewWatcher 应挂包级容器 %q，实际 ClassFQN=%q", containerName, m.ClassFQN)
		}
	}

	var _ *parser.IRDocument = doc
}

// TestGoRawStringDoesNotCreateMethods 负控：反引号原始字符串里的 Go 代码不是源码，
// 旧实现会把串内 `func Ghost() { ... }` 当方法、串内 `}` 当方法结束，导致后续真实
// 调用（helper）caller 变为空。
func TestGoRawStringDoesNotCreateMethods(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "embed.go")
	src := "package demo\n\nfunc write() {\n\tsrc := `package embedded\nfunc Ghost() { Fake() }\n`\n\thelper(src)\n}\n"
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := NewTreeSitterAdapter().Parse(context.Background(), p)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	for _, m := range doc.Methods {
		if m.Name == "Ghost" {
			t.Errorf("raw string content created method: %+v", m)
		}
	}
	for _, c := range doc.Calls {
		if c.CalleeFQN == "Fake" || c.CalleeFQN == "demo.Fake" {
			t.Errorf("raw string content created call: %+v", c)
		}
		if c.CalleeFQN == "helper" && c.CallerFQN != "demo.write" {
			t.Errorf("helper caller = %q, want demo.write", c.CallerFQN)
		}
	}
}

// TestGoLocalVarCallQualification 负控：`x := &T{}` / `var x *T` 声明的局部变量，
// 其 `x.Method()` 调用必须限定为 pkg.T.Method；旧实现只跟踪 receiver，保持裸 selector。
func TestGoLocalVarCallQualification(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "local.go")
	src := `package demo

type ConfigWatcher struct{}

func (c *ConfigWatcher) Start() {}

func Run() {
	cfg := &ConfigWatcher{}
	cfg.Start()
	var svc *ConfigWatcher
	svc.Start()
}
`
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	doc, err := NewTreeSitterAdapter().Parse(context.Background(), p)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var qualified int
	for _, c := range doc.Calls {
		if c.CallerFQN != "demo.Run" {
			continue
		}
		if c.CalleeFQN == "demo.ConfigWatcher.Start" {
			qualified++
		}
		if strings.HasPrefix(c.CalleeFQN, "cfg.") || strings.HasPrefix(c.CalleeFQN, "svc.") {
			t.Errorf("局部变量 selector 未消歧: %s", c.CalleeFQN)
		}
	}
	if qualified != 2 {
		t.Errorf("qualified Start calls = %d, want 2; calls=%+v", qualified, doc.Calls)
	}
}

// TestGoMultiLineSignature_CallAttribution 是 P1-14 负控：Go 多行函数签名
// （签名行无 `{`）此前会在首个参数续行处被误判为方法体闭合，导致方法体内
// 调用的 CallerFQN 为空、analyzer 不建边。修复后签名续行累积进 Signature，
// 方法体调用归属 sample.MultiLine。
func TestGoMultiLineSignature_CallAttribution(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "multi.go")
	src := `package sample

func helper(x int) int { return x }

func MultiLine(
	a int,
	b string,
) int {
	return helper(a) + len(b)
}
`
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewTreeSitterAdapter()
	doc, err := a.Parse(context.Background(), p)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	var multi *parser.MethodIR
	for i := range doc.Methods {
		if doc.Methods[i].Name == "MultiLine" {
			multi = &doc.Methods[i]
		}
	}
	if multi == nil {
		t.Fatalf("MultiLine 未识别；methods=%v", doc.Methods)
	}
	if !strings.Contains(multi.Signature, "b string") {
		t.Errorf("多行签名应累积续行，实际 Signature=%q", multi.Signature)
	}

	found := false
	for _, c := range doc.Calls {
		if c.CallerFQN == "sample.MultiLine" && c.CalleeFQN == "sample.helper" {
			found = true
		}
	}
	if !found {
		t.Errorf("期望调用 sample.MultiLine -> sample.helper，实际 calls=%v", doc.Calls)
	}
}

// TestGoLongLine_DoesNotFailFile 是 P1-15 负控：单行超过 bufio.Scanner 默认
// 64KB 上限时，旧实现返回 ErrTooLong，scanner 聚合后使整次扫描以错误退出。
// 修复后超长行不再中断文件解析：<4MiB 行照常解析，超限行跳过但继续后续行。
func TestGoLongLine_DoesNotFailFile(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "long.go")
	longComment := "// " + strings.Repeat("x", 70*1024) // 70KB，超过旧 64KB 限制
	hugeLine := strings.Repeat("y", 5*1024*1024)        // 5MiB，超过解析上限，应跳过
	src := "package sample\n\n" + longComment + "\n\nfunc AfterLong() {}\n\n// " + hugeLine + "\n\nfunc AfterHuge() {}\n"
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewTreeSitterAdapter()
	doc, err := a.Parse(context.Background(), p)
	if err != nil {
		t.Fatalf("超长行不应使 Parse 失败：%v", err)
	}

	names := map[string]bool{}
	for _, m := range doc.Methods {
		names[m.Name] = true
	}
	for _, want := range []string{"AfterLong", "AfterHuge"} {
		if !names[want] {
			t.Errorf("超长行之后的函数 %s 未被解析；methods=%v", want, doc.Methods)
		}
	}
}

// parseGoSource 把源码写入临时文件并用默认适配器解析，供 Go 解析硬化用例复用。
func parseGoSource(t *testing.T, src string) *parser.IRDocument {
	t.Helper()
	p := filepath.Join(t.TempDir(), "sample.go")
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatalf("write fixture: %v", err)
	}
	doc, err := NewTreeSitterAdapter().Parse(context.Background(), p)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	return doc
}

func findMethod(doc *parser.IRDocument, name string) *parser.MethodIR {
	for i := range doc.Methods {
		if doc.Methods[i].Name == name {
			return &doc.Methods[i]
		}
	}
	return nil
}

// TestGoParser_RawStringConstGeneric 是 P3-9 负控，覆盖 Go 解析三项硬化：
//  1. 反引号原始字符串未剔除 → 字符串内伪调用被当真实调用，且串内 `{` 破坏方法深度；
//  2. const 块闭合行带行尾注释时 `)` 不被识别 → constBlock 泄漏，吞掉后续方法体调用；
//  3. 泛型函数 `func F[T any](` 不匹配方法正则 → 整个函数丢失。
func TestGoParser_RawStringConstGeneric(t *testing.T) {
	t.Run("raw_string", func(t *testing.T) {
		src := "package sample\n\n" +
			"func UsesRaw() string {\n" +
			"\ts := `fake(1) {`\n" +
			"\treturn s\n" +
			"}\n\n" +
			"func Next() {}\n"
		doc := parseGoSource(t, src)

		for _, c := range doc.Calls {
			if strings.Contains(c.CalleeFQN, "fake") {
				t.Errorf("原始字符串内伪调用不应入库：%+v", c)
			}
		}
		m := findMethod(doc, "UsesRaw")
		if m == nil {
			t.Fatal("UsesRaw 未识别")
		}
		if m.EndLine != 6 {
			t.Errorf("原始字符串内 `{` 不应影响方法闭合：EndLine want 6, got %d", m.EndLine)
		}
	})

	t.Run("const_block_leak", func(t *testing.T) {
		src := "package sample\n\n" +
			"const (\n" +
			"\tA = 1\n" +
			") // trailing comment\n\n" +
			"func AfterConst() {\n" +
			"\thelper()\n" +
			"}\n"
		doc := parseGoSource(t, src)

		found := false
		for _, c := range doc.Calls {
			if c.CallerFQN == "sample.AfterConst" && c.CalleeFQN == "sample.helper" {
				found = true
			}
		}
		if !found {
			t.Errorf("const 块闭合后方法体调用被吞；calls=%v", doc.Calls)
		}
	})

	t.Run("generic_func", func(t *testing.T) {
		src := "package sample\n\n" +
			"func Generic[T any](x T) T {\n" +
			"\treturn x\n" +
			"}\n"
		doc := parseGoSource(t, src)

		m := findMethod(doc, "Generic")
		if m == nil {
			t.Fatalf("泛型函数未识别；methods=%v", doc.Methods)
		}
		if m.ClassFQN != "sample" {
			t.Errorf("泛型函数应挂包级容器，got ClassFQN=%q", m.ClassFQN)
		}
	})
}

// TestGoFieldAndConstantExtraction 验证 Go 路径的变量/常量 IR 产出（2026-09-03 新增）：
//  1. 包级常量：单行 const X [Type] = v 与 const ( ... ) 块内行 X = v 均产出为
//     ConstantIR{FilePath}（包/文件级常量）；
//  2. 成员变量：struct 体（type X struct { ... }）内的字段行（含 tag）产出为
//     FieldIR{ClassFQN}，方法内/顶层局部变量不产出；
//  3. 值/类型/行列信息完整。
func TestGoFieldAndConstantExtraction(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "svc.go")
	src := `package svc

const AppVersion = "1.0"
const MaxItems int = 100

const (
	KB = 1024
	MB = 1024 * KB
)

type Service struct {
	name string
	port int
	tags []string
	max  int ` + "`json:\"max\"`" + `
}

func (s *Service) Start() {
	local := 5
	_ = local
}
`
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewTreeSitterAdapter()
	doc, err := a.Parse(context.Background(), p)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	t.Logf("FIELDS:")
	for _, f := range doc.Fields {
		t.Logf("  name=%q type=%q class_fqn=%q", f.Name, f.Type, f.ClassFQN)
	}
	t.Logf("CONSTANTS:")
	for _, c := range doc.Constants {
		t.Logf("  name=%q type=%q value=%q file=%q", c.Name, c.Type, c.Value, c.FilePath)
	}

	// 1. 常量：单行 + 块内均产出为包级常量
	wantConsts := map[string]struct{ typ, val string }{
		"AppVersion": {"", "\"1.0\""},
		"MaxItems":   {"int", "100"},
		"KB":         {"", "1024"},
		"MB":         {"", "1024 * KB"},
	}
	gotConsts := map[string]struct{ typ, val string }{}
	for _, c := range doc.Constants {
		if c.FilePath != p {
			t.Errorf("常量 %s 应归属当前文件 %s，实际 file=%q", c.Name, p, c.FilePath)
		}
		gotConsts[c.Name] = struct{ typ, val string }{c.Type, c.Value}
	}
	if len(gotConsts) != len(wantConsts) {
		t.Fatalf("常量数量不匹配：want=%v got=%v", wantConsts, gotConsts)
	}
	for name, want := range wantConsts {
		got, ok := gotConsts[name]
		if !ok || got != want {
			t.Errorf("常量 %s：want=%v got=%v", name, want, got)
		}
	}

	// 2. 成员变量：struct 字段产出（含 tag 行），局部变量不产出
	wantFields := map[string]string{
		"name": "string",
		"port": "int",
		"tags": "[]string",
		"max":  "int",
	}
	gotFields := map[string]string{}
	for _, f := range doc.Fields {
		if f.ClassFQN != "Service" {
			t.Errorf("字段 %s 应归属类 Service，实际 class_fqn=%q", f.Name, f.ClassFQN)
		}
		gotFields[f.Name] = f.Type
	}
	if len(gotFields) != len(wantFields) {
		t.Fatalf("成员变量数量不匹配：want=%v got=%v", wantFields, gotFields)
	}
	for name, typ := range wantFields {
		if gotFields[name] != typ {
			t.Errorf("字段 %s：want=%q got=%q", name, typ, gotFields[name])
		}
	}
	// 局部变量 local 不应被产出为成员变量
	if _, ok := gotFields["local"]; ok {
		t.Errorf("局部变量 local 不应产出为成员变量：%v", gotFields)
	}
}
