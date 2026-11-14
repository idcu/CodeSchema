//go:build treesitter

package treesitter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestASTAdapter_Supports 验证 AST 版支持 7 语言。
func TestASTAdapter_Supports(t *testing.T) {
	a := NewTreeSitterAdapter()
	supported := []string{"go", "java", "ts", "py", "rust", "cpp", "c", "kotlin", "swift", "php", "csharp", "ruby", "bash", "scala", "sql", "elixir", "ocaml", "lua", "groovy", "css", "toml", "yaml", "protobuf", "html", "hcl", "svelte", "markdown", "dockerfile", "elm", "cue"}
	for _, lang := range supported {
		if !a.Supports(lang) {
			t.Errorf("expected %s to be supported", lang)
		}
	}
	if a.Supports("unknown") {
		t.Error("expected unknown to be unsupported")
	}
}

// TestASTAdapter_GoScopeAndPackageContainer 是 P0-1 遗留项的负控：AST 路径
// 必须像默认正则路径一样
//  1. 把无 receiver 的顶层函数挂到合成 PACKAGE 容器（ClassFQN=包名）；
//  2. 方法归属由 receiver 类型决定，不依赖「最近遍历到的 type 声明」；
//  3. caller/callee 使用包限定 FQN（pkg.Func / pkg.Type.Method）。
func TestASTAdapter_GoScopeAndPackageContainer(t *testing.T) {
	a := NewTreeSitterAdapter()
	path := filepath.Join(t.TempDir(), "sample.go")
	content := `package sample

type User struct{ Name string }

func (u *User) Greet() string {
	return helper()
}

func helper() string { return "x" }

func main() {
	u := User{}
	u.Greet()
	helper()
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	wantClass := map[string]string{"Greet": "User", "helper": "sample", "main": "sample"}
	for name, want := range wantClass {
		var got string
		for _, m := range doc.Methods {
			if m.Name == name {
				got = m.ClassFQN
			}
		}
		if got != want {
			t.Errorf("method %s ClassFQN = %q, want %q", name, got, want)
		}
	}
	var pkgContainer int
	for _, c := range doc.Classes {
		if c.Type == "PACKAGE" {
			pkgContainer++
			if c.FullName != "sample" {
				t.Errorf("package container FullName = %q, want sample", c.FullName)
			}
		}
	}
	if pkgContainer != 1 {
		t.Errorf("expected exactly 1 PACKAGE container, got %d (classes=%+v)", pkgContainer, doc.Classes)
	}

	wantCalls := map[string]string{
		"sample.main":       "sample.helper",
		"sample.User.Greet": "sample.helper",
	}
	gotCalls := map[string]string{}
	for _, c := range doc.Calls {
		gotCalls[c.CallerFQN] = c.CalleeFQN
	}
	for caller, callee := range wantCalls {
		if gotCalls[caller] != callee {
			t.Errorf("call %s -> %s not found; calls=%+v", caller, callee, doc.Calls)
		}
	}
}

// TestASTAdapter_NestedScopeRestore 是作用域跟踪负控：离开嵌套类/顶层类后，
// curClass 必须恢复上一层，否则嵌套类之后的兄弟方法会被错挂到内层类。
func TestASTAdapter_NestedScopeRestore(t *testing.T) {
	a := NewTreeSitterAdapter()
	path := filepath.Join(t.TempDir(), "Outer.java")
	content := `package com.example;

public class Outer {
    public void first(Service svc) {
        svc.before();
    }

    class Inner {
        public void innerCall(Service svc) {
            svc.inside();
        }
    }

    public void last(Service svc) {
        svc.after();
    }
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	found := false
	for _, c := range doc.Calls {
		if c.CallerFQN == "com.example.Outer.last" && c.CalleeFQN == "Service.after" {
			found = true
		}
	}
	if !found {
		t.Errorf("method after nested class should belong to Outer, calls=%+v", doc.Calls)
	}
}

// TestASTAdapter_Parse_Go 验证 Go 语法级解析（类/方法/调用）。
func TestASTAdapter_Parse_Go(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	content := `package main

// User 表示用户实体。
type User struct {
	Name string
}

// Greet 返回问候语。
func (u *User) Greet(name string) string {
	return fmt.Sprintf("Hello, %s", name)
}

func main() {
	u := &User{Name: "Alice"}
	msg := "fakeCall(1)"
	result := u.Greet(u.Name)
	realCall(msg)
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Classes) != 2 {
		t.Errorf("expected 2 classes (User + PACKAGE main), got %d: %+v", len(doc.Classes), doc.Classes)
	}
	if len(doc.Methods) < 2 {
		t.Errorf("expected >=2 methods (Greet + main), got %d", len(doc.Methods))
	}
	// 字符串内伪调用 fakeCall 不应进 Calls
	for _, c := range doc.Calls {
		if c.CalleeFQN == "fakeCall" {
			t.Errorf("expected string pseudo-call filtered, got %+v", doc.Calls)
		}
	}
	// 真实调用 u.Greet 与 realCall 应被检出：裸 callee 包限定为 main.realCall；
	// 接收者变量 u 由 `u := &User{}` 静态确定为 User，callee 限定为 main.User.Greet
	// （与默认正则路径同口径）。
	foundGreet, foundReal := false, false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "main.User.Greet" {
			foundGreet = true
		}
		if c.CalleeFQN == "main.realCall" {
			foundReal = true
		}
	}
	if !foundGreet || !foundReal {
		t.Errorf("expected main.User.Greet & main.realCall detected, got calls: %+v", doc.Calls)
	}
}

// TestASTAdapter_GoTypeConversionNotCall 负控：`(*T)(nil)` 是类型转换而非函数调用。
// 旧实现把括号包裹的指针类型当作 function 节点，产出 callee=`pkg.(*T)` 的假调用边。
func TestASTAdapter_GoTypeConversionNotCall(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "conv.go")
	content := `package demo

type Embedder interface{ Embed() }

type impl struct{}

func (i *impl) Embed() {}

var _ Embedder = (*impl)(nil)
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, c := range doc.Calls {
		if strings.HasPrefix(c.CalleeFQN, "demo.(*") || strings.HasPrefix(c.CalleeFQN, "(*") {
			t.Errorf("类型转换被登记为调用: %+v", c)
		}
	}
}

// TestASTAdapter_TSNamedArrowFunctionCaller 负控：`const f = () => {...}` 是具名函数，
// 体内的调用应有 caller=f。旧实现只识别 function_declaration / method_definition，
// 箭头函数体调用全部落入空 caller。
func TestASTAdapter_TSNamedArrowFunctionCaller(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "util.ts")
	content := `export const add = (a: number, b: number): number => {
  return helper(a, b);
};

export const mul = function (a: number, b: number): number {
  return helper(a, b);
};

function helper(a: number, b: number): number {
  return a + b;
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	callers := map[string]bool{}
	for _, m := range doc.Methods {
		callers[m.Name] = true
	}
	for _, want := range []string{"add", "mul"} {
		if !callers[want] {
			t.Errorf("具名箭头/函数表达式 %s 未登记为方法；methods=%+v", want, doc.Methods)
		}
	}
	got := map[string]bool{}
	for _, c := range doc.Calls {
		if c.CalleeFQN == "util.helper" {
			got[c.CallerFQN] = true
		}
	}
	for _, want := range []string{"util.add", "util.mul"} {
		if !got[want] {
			t.Errorf("helper 调用缺少 caller=%s；current=%v", want, got)
		}
	}
}

// TestASTAdapter_TSXGrammar .tsx 必须用 TSX grammar 解析：JSX 语法在纯 TypeScript
// grammar 下会产生 ERROR 节点，内部方法/调用作用域全部丢失（caller 为空）。
func TestASTAdapter_TSXGrammar(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.tsx")
	content := `function Clock(): JSX.Element {
  const [now, setNow] = React.useState<string>(new Date().toLocaleString());
  React.useEffect(() => {
    const id = window.setInterval(() => {
      setNow(new Date().toLocaleString());
    }, 1000);
    return () => window.clearInterval(id);
  }, []);
  return (
    <section className="clock">
      <h2>Live Clock</h2>
      <div className="clock-time">{now}</div>
    </section>
  );
}

function Counter(): JSX.Element {
  const [count, setCount] = React.useState<number>(0);
  return (
    <section className="counter">
      <button onClick={() => setCount((c) => c - 1)}>-1</button>
    </section>
  );
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "ts" {
		t.Errorf("Language = %q, want ts（IR 口径不变，仅 grammar 切换）", doc.Language)
	}
	names := map[string]bool{}
	for _, m := range doc.Methods {
		names[m.Name] = true
	}
	for _, want := range []string{"Clock", "Counter"} {
		if !names[want] {
			t.Fatalf("tsx 中的 %s 未登记为方法；methods=%+v", want, doc.Methods)
		}
	}
	callers := map[string]bool{}
	for _, c := range doc.Calls {
		if c.CalleeFQN == "React.useState" || c.CalleeFQN == "useState" {
			callers[c.CallerFQN] = true
		}
	}
	for _, want := range []string{"app.Clock", "app.Counter"} {
		if !callers[want] {
			t.Errorf("useState 调用缺少 caller=%s；current=%v", want, callers)
		}
	}
}

// TestASTAdapter_Parse_Java 验证 Java 语法级解析（类/方法/调用，含字符串陷阱）。
func TestASTAdapter_Parse_Java(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "OrderService.java")
	content := `package com.example;

public class OrderService {
    public void createOrder(Order order) {
        paymentService.pay(order);
        String s = "fakeCall(1)";
        if (order.isValid()) {
            notifyService.send(order);
        }
    }
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Classes) != 1 {
		t.Errorf("expected 1 class (OrderService), got %d", len(doc.Classes))
	}
	if len(doc.Methods) != 1 {
		t.Errorf("expected 1 method (createOrder), got %d", len(doc.Methods))
	}
	foundPay, foundSend := false, false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "paymentService.pay" {
			foundPay = true
		}
		if c.CalleeFQN == "notifyService.send" {
			foundSend = true
		}
	}
	if !foundPay || !foundSend {
		t.Errorf("expected paymentService.pay & notifyService.send, got calls: %+v", doc.Calls)
	}
}

// TestASTAdapter_GoLocalVarCallQualification 负控：AST 路径此前不跟踪方法体内的
// 局部变量类型，`fs := &FileStore{}` 后 `fs.GetFileByPath()` 的 callee 恒为
// `fs.GetFileByPath`。短名在 FileStore/PGStore/SQLiteStore 间多义时，测试关联
// 无法归位（treesitter 人工真值 Go recall 0.6287 的主因）。现按 `x := &T{}` /
// `x := T{}` / `var x *T` 限定为 `pkg.T.Method`，与正则路径一致。
func TestASTAdapter_GoLocalVarCallQualification(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "store.go")
	content := `package demo

type FileStore struct{}

func (f *FileStore) GetFileByPath(p string) (string, error) { return "", nil }

func Run() {
	fs := &FileStore{}
	var other FileStore
	_, _ = fs.GetFileByPath("x")
	_, _ = other.GetFileByPath("y")
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := map[string]bool{
		"demo.FileStore.GetFileByPath": false,
	}
	for _, c := range doc.Calls {
		if _, ok := want[c.CalleeFQN]; ok {
			want[c.CalleeFQN] = true
		}
	}
	for callee, found := range want {
		if !found {
			t.Fatalf("callee %q not qualified: %+v", callee, doc.Calls)
		}
	}
}

// TestASTAdapter_JavaNestedClassFlatten 负控：Java 嵌套测试类在 AST 路径下曾保留
// 内层类名（ProcessCreationFormHasErrors.initUpdateForm），而正则路径归到外层类
// （PetControllerTests.initUpdateForm）。同一方法两变体 FQN 不一致，人工真值与
// 影响面查询在 treesitter 构建下失效；现统一归到最外层类，且不单独登记嵌套类。
func TestASTAdapter_JavaNestedClassFlatten(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "PetControllerTests.java")
	content := `package demo;

public class PetControllerTests {
    @Test
    void outer() {}

    static class ProcessCreationFormHasErrors {
        @Test
        void initUpdateForm() {}
    }
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Classes) != 1 || doc.Classes[0].FullName != "PetControllerTests" {
		t.Fatalf("nested class should not be registered separately: %+v", doc.Classes)
	}
	found := false
	for _, m := range doc.Methods {
		if m.Name != "initUpdateForm" {
			continue
		}
		found = true
		if m.ClassFQN != "PetControllerTests" {
			t.Fatalf("initUpdateForm ClassFQN = %q, want PetControllerTests", m.ClassFQN)
		}
	}
	if !found {
		t.Fatalf("initUpdateForm not parsed: %+v", doc.Methods)
	}
}

// TestASTAdapter_JavaChainedNewCall 负控：`new Calculator().add(1, 2)` 的被调方
// 是构造表达式的链式方法调用。旧实现取 method_invocation 全文 `(` 前文本，得到
// callee=`new Calculator`（非法 FQN），真值抽取与测试关联全部失配——
// 这是 treesitter 下 releasebench Java 夹具真值 pair=0 的根因。
// 现识别 object 为 object_creation_expression：callee 取 `类型.方法`（Calculator.add）。
func TestASTAdapter_JavaChainedNewCall(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "CalculatorTest.java")
	content := `package demo;

public class CalculatorTest {
    public void testAdd() {
        new Calculator().add(1, 2);
    }
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	found := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "Calculator.add" {
			found = true
		}
		if strings.Contains(c.CalleeFQN, "new Calculator") {
			t.Fatalf("构造表达式被当作 callee: %+v", c)
		}
	}
	if !found {
		t.Fatalf("expected Calculator.add, got calls: %+v", doc.Calls)
	}
}

// TestASTAdapter_Parse_Kotlin 验证 Kotlin 语法级解析。
func TestASTAdapter_Parse_Kotlin(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "UserService.kt")
	content := `package com.example

data class User(val name: String)

class UserService {
    fun getUser(id: Long): User {
        val user = repository.findById(id)
        return user
    }
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}

	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "kotlin" {
		t.Errorf("expected kotlin, got %s", doc.Language)
	}
	foundRepo := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "repository.findById" {
			foundRepo = true
		}
	}
	if !foundRepo {
		t.Errorf("expected repository.findById detected, got calls: %+v", doc.Calls)
	}
}

// TestASTAdapter_Parse_NonGoCallerPkgQualified 验证 AST 路径下 Java/Kotlin/C# 的
// caller FQN 包限定（P20），与默认正则路径（P19）产出一致，使非 Go 调用图命名空间对齐。
func TestASTAdapter_Parse_NonGoCallerPkgQualified(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()

	cases := []struct {
		name, filename, content, wantCaller string
	}{
		{
			name:     "java",
			filename: "OrderService.java",
			content: `package com.example;

public class OrderService {
    public void createOrder(Order order) {
        paymentService.pay(order);
    }
}
`,
			wantCaller: "com.example.OrderService.createOrder",
		},
		{
			name:     "kotlin",
			filename: "UserService.kt",
			content: `package com.example

class UserService {
    fun getUser(id: Long) {
        val user = repository.findById(id)
    }
}
`,
			wantCaller: "com.example.UserService.getUser",
		},
		{
			name:     "csharp",
			filename: "OrderService.cs",
			content: `namespace Acme.Orders;

public class OrderService {
    public void Create(Order order) {
        repository.Save(order);
    }
}
`,
			wantCaller: "Acme.Orders.OrderService.Create",
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(dir, tc.filename)
			if err := os.WriteFile(path, []byte(tc.content), 0644); err != nil {
				t.Fatal(err)
			}
			doc, err := a.Parse(context.Background(), path)
			if err != nil {
				t.Fatalf("Parse: %v", err)
			}
			if len(doc.Calls) == 0 {
				t.Fatalf("no calls parsed for %s", tc.name)
			}
			found := false
			for _, c := range doc.Calls {
				if c.CallType == "direct" && c.CallerFQN == tc.wantCaller {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("callerFQN=%q not found; calls=%+v", tc.wantCaller, doc.Calls)
			}
		})
	}
}

// TestASTAdapter_JestCallbacksAsMethods 约束 AST 路径与正则路径同口径：
// describe/it/test 回调合成为模块内方法，回调体调用带 CallerFQN，导入函数模块限定。
func TestASTAdapter_JestCallbacksAsMethods(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "index.test.ts")
	content := `import { describe, it, expect } from '@jest/globals';
import { greet } from '../src/index';
import { add } from '../src/utils/math';

describe('greet', () => {
  it('greets a name with capitalization', () => {
    expect(greet('alice')).toBe('Hello, Alice!');
  });

  it('falls back to default', () => {
    expect(greet('')).toBe('Hello, World!');
  });
});

describe('add', () => {
  test('sums two numbers', () => {
    expect(add(1, 2)).toBe(3);
  });
});
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	want := map[string]string{
		"greet_greets_a_name_with_capitalization": "index.greet",
		"greet_falls_back_to_default":             "index.greet",
		"add_sums_two_numbers":                    "math.add",
	}
	got := map[string]bool{}
	for _, m := range doc.Methods {
		if m.ClassFQN != "index.test" {
			continue
		}
		got[m.Name] = true
		if _, ok := want[m.Name]; !ok {
			t.Errorf("unexpected jest method %q", m.Name)
		}
	}
	for name, callee := range want {
		if !got[name] {
			t.Errorf("jest method %q not found; methods=%+v", name, doc.Methods)
			continue
		}
		found := false
		for _, c := range doc.Calls {
			if c.CallerFQN == "index.test."+name && c.CalleeFQN == callee {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("call %q -> %q not found; calls=%+v", name, callee, doc.Calls)
		}
	}
}
