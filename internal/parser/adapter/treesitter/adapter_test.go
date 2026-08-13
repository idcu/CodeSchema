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
	supported := []string{"go", "java", "ts", "py", "rust", "cpp"}
	unsupported := []string{"ruby", "php", "swift", "kotlin", "unknown"}

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