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
		if c.CallerFQN == "com.example.Outer.last" && c.CalleeFQN == "svc.after" {
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
	// 真实调用 u.Greet 与 realCall 应被检出（Go 裸 callee 包限定为 main.realCall，
	// 与默认正则路径口径一致，P0-1 遗留对齐）
	foundGreet, foundReal := false, false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "u.Greet" {
			foundGreet = true
		}
		if c.CalleeFQN == "main.realCall" {
			foundReal = true
		}
	}
	if !foundGreet || !foundReal {
		t.Errorf("expected u.Greet & main.realCall detected, got calls: %+v", doc.Calls)
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
