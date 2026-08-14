package treesitter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

func TestTreeSitterAdapter_Name(t *testing.T) {
	a := NewTreeSitterAdapter()
	if a.Name() != "treesitter" {
		t.Errorf("expected treesitter, got %s", a.Name())
	}
}

func TestTreeSitterAdapter_Supports(t *testing.T) {
	a := NewTreeSitterAdapter()
	supported := []string{"go", "java", "ts", "py", "rust", "cpp", "kotlin"}
	unsupported := []string{"ruby", "php", "swift", "unknown"}

	for _, lang := range supported {
		if !a.Supports(lang) {
			t.Errorf("expected %s to be supported", lang)
		}
	}
	for _, lang := range unsupported {
		if a.Supports(lang) {
			t.Errorf("expected %s to be unsupported", lang)
		}
	}
}

func TestTreeSitterAdapter_Parse_Go(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	content := `package main

// User 表示用户实体。
type User struct {
    Name string
    Age  int
}

// Greet 返回问候语。
func (u *User) Greet(name string) string {
    return fmt.Sprintf("Hello, %s", name)
}

func main() {
    u := &User{Name: "Alice"}
    result := u.Greet(u.Name)
    fmt.Println(result)
}
`
	os.WriteFile(path, []byte(content), 0644)

	ctx := context.Background()
	doc, err := a.Parse(ctx, path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if doc.Source != "treesitter" {
		t.Errorf("expected source treesitter, got %s", doc.Source)
	}
	if doc.Language != "go" {
		t.Errorf("expected language go, got %s", doc.Language)
	}

	// 检查类解析
	if len(doc.Classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(doc.Classes))
	}
	if doc.Classes[0].Name != "User" {
		t.Errorf("expected class User, got %s", doc.Classes[0].Name)
	}
	if doc.Classes[0].Type != "CLASS" {
		t.Errorf("expected CLASS, got %s", doc.Classes[0].Type)
	}

	// 检查方法解析
	if len(doc.Methods) < 2 {
		t.Fatalf("expected at least 2 methods, got %d", len(doc.Methods))
	}
	if doc.Methods[0].Name != "Greet" {
		t.Errorf("expected method Greet, got %s", doc.Methods[0].Name)
	}
	if doc.Methods[1].Name != "main" {
		t.Errorf("expected method main, got %s", doc.Methods[1].Name)
	}
}

func TestTreeSitterAdapter_Parse_Java(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "OrderService.java")
	content := `package com.example;

/**
 * 订单服务接口。
 */
public interface OrderService {
    /**
     * 创建订单。
     */
    Order createOrder(String userId, List<Item> items);
    
    Order findById(String id);
}
`
	os.WriteFile(path, []byte(content), 0644)

	ctx := context.Background()
	doc, err := a.Parse(ctx, path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if doc.Language != "java" {
		t.Errorf("expected language java, got %s", doc.Language)
	}

	if len(doc.Classes) != 1 {
		t.Fatalf("expected 1 interface, got %d", len(doc.Classes))
	}
	if doc.Classes[0].Name != "OrderService" {
		t.Errorf("expected OrderService, got %s", doc.Classes[0].Name)
	}
	if doc.Classes[0].Type != "INTERFACE" {
		t.Errorf("expected INTERFACE, got %s", doc.Classes[0].Type)
	}
}

func TestTreeSitterAdapter_Parse_TypeScript(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "user.ts")
	content := `export interface User {
    id: number;
    name: string;
}

export class UserService {
    getUser(id: number): User {
        return fetchUser(id);
    }
}
`
	os.WriteFile(path, []byte(content), 0644)

	ctx := context.Background()
	doc, err := a.Parse(ctx, path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if doc.Language != "ts" {
		t.Errorf("expected language ts, got %s", doc.Language)
	}

	if len(doc.Classes) != 2 {
		t.Fatalf("expected 2 classes, got %d", len(doc.Classes))
	}
	if doc.Classes[0].Name != "User" {
		t.Errorf("expected User, got %s", doc.Classes[0].Name)
	}
	if doc.Classes[0].Type != "INTERFACE" {
		t.Errorf("expected INTERFACE, got %s", doc.Classes[0].Type)
	}
	if doc.Classes[1].Name != "UserService" {
		t.Errorf("expected UserService, got %s", doc.Classes[1].Name)
	}
	if doc.Classes[1].Type != "CLASS" {
		t.Errorf("expected CLASS, got %s", doc.Classes[1].Type)
	}
}

func TestTreeSitterAdapter_Parse_Python(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "calculator.py")
	content := `class Calculator:
    def add(self, a, b):
        return a + b
    
    def multiply(self, a, b):
        return a * b

def main():
    calc = Calculator()
    result = calc.add(1, 2)
    print(result)
`
	os.WriteFile(path, []byte(content), 0644)

	ctx := context.Background()
	doc, err := a.Parse(ctx, path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if doc.Language != "py" {
		t.Errorf("expected language py, got %s", doc.Language)
	}

	if len(doc.Classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(doc.Classes))
	}
	if doc.Classes[0].Name != "Calculator" {
		t.Errorf("expected Calculator, got %s", doc.Classes[0].Name)
	}

	if len(doc.Methods) < 3 {
		t.Fatalf("expected at least 3 methods, got %d", len(doc.Methods))
	}
	if doc.Methods[0].Name != "add" {
		t.Errorf("expected method add, got %s", doc.Methods[0].Name)
	}
}

func TestTreeSitterAdapter_Parse_Rust(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.rs")
	content := `struct Config {
    name: String,
}

impl Config {
    fn new(name: &str) -> Self {
        Config { name: name.to_string() }
    }
}

fn main() {
    let cfg = Config::new("test");
    println!("{}", cfg.name);
}
`
	os.WriteFile(path, []byte(content), 0644)

	ctx := context.Background()
	doc, err := a.Parse(ctx, path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	if doc.Language != "rust" {
		t.Errorf("expected language rust, got %s", doc.Language)
	}

	if len(doc.Classes) < 1 {
		t.Fatalf("expected at least 1 class, got %d", len(doc.Classes))
	}
}

func TestTreeSitterAdapter_Parse_EmptyFile(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "empty.go")
	os.WriteFile(path, []byte{}, 0644)

	ctx := context.Background()
	doc, err := a.Parse(ctx, path)
	if err != nil {
		t.Fatalf("Parse empty file: %v", err)
	}
	if doc == nil {
		t.Fatal("expected non-nil document for empty file")
	}
}

func TestTreeSitterAdapter_Parse_UnsupportedExt(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "readme.md")
	os.WriteFile(path, []byte("# Hello"), 0644)

	ctx := context.Background()
	doc, err := a.Parse(ctx, path)
	if err != nil {
		t.Fatalf("Parse unsupported: %v", err)
	}
	if doc == nil {
		t.Fatal("expected non-nil document")
	}
	if len(doc.Classes) != 0 {
		t.Errorf("expected 0 classes for unsupported file, got %d", len(doc.Classes))
	}
}

func TestTreeSitterAdapter_InitClose(t *testing.T) {
	a := NewTreeSitterAdapter()
	ctx := context.Background()
	if err := a.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

// TestTreeSitterAdapter_Parse_JavaCalls 验证 Java 调用检测（T2-1：调用检测扩展到全语言）。
func TestTreeSitterAdapter_Parse_JavaCalls(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "OrderService.java")
	content := `package com.example;

public class OrderService {
    public void createOrder(Order order) {
        paymentService.pay(order);
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
		t.Fatalf("expected 1 class, got %d", len(doc.Classes))
	}
	if len(doc.Methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(doc.Methods))
	}
	if len(doc.Calls) == 0 {
		t.Fatal("expected calls detected in Java (T2-1), got 0")
	}
	// 应检测到 paymentService.pay 与 notifyService.send；if/order.isValid 为关键字/短名应被过滤或保留
	found := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "paymentService.pay" || c.CalleeFQN == "notifyService.send" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected paymentService.pay / notifyService.send in calls, got %+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_Kotlin 验证 Kotlin 类/方法解析（T6-2：多语言扩展）。
func TestTreeSitterAdapter_Parse_Kotlin(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "UserService.kt")
	content := `package com.example

data class User(val name: String, val age: Int)

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
		t.Errorf("expected language kotlin, got %s", doc.Language)
	}
	if len(doc.Classes) != 2 {
		t.Fatalf("expected 2 classes (User + UserService), got %d: %+v", len(doc.Classes), doc.Classes)
	}
	if len(doc.Methods) != 1 {
		t.Fatalf("expected 1 method (getUser), got %d", len(doc.Methods))
	}
	if doc.Methods[0].Name != "getUser" {
		t.Errorf("expected method getUser, got %s", doc.Methods[0].Name)
	}
}

// TestTreeSitterAdapter_Parse_CppCalls 验证 C++ 调用检测。
func TestTreeSitterAdapter_Parse_CppCalls(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "engine.cpp")
	content := `class Engine {
public:
    void start() {
        fuelPump.pump();
        if (ready) {
            ignition.fire();
        }
    }
};
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
		if c.CalleeFQN == "fuelPump.pump" || c.CalleeFQN == "ignition.fire" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected C++ calls detected, got %+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_StringCallFiltered 验证字符串内伪调用不进 Calls。
func TestTreeSitterAdapter_Parse_StringCallFiltered(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "svc.go")
	content := `package main

type Svc struct{}

func (s *Svc) Run() {
	msg := "fakeCall(1)"
	realCall(msg)
	// commentCall(2)
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	foundFake, foundReal := false, false
	for _, c := range doc.Calls {
		switch c.CalleeFQN {
		case "fakeCall", "commentCall":
			foundFake = true
		case "realCall":
			foundReal = true
		}
	}
	if foundFake {
		t.Errorf("expected string/comment pseudo-calls filtered, got calls: %+v", doc.Calls)
	}
	if !foundReal {
		t.Errorf("expected realCall detected, got calls: %+v", doc.Calls)
	}
}
