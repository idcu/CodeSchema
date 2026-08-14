//go:build treesitter

package treesitter

import (
	"context"
	"os"
	"path/filepath"
	"testing"
)

// TestASTAdapter_Supports 验证 AST 版支持 7 语言。
func TestASTAdapter_Supports(t *testing.T) {
	a := NewTreeSitterAdapter()
	supported := []string{"go", "java", "ts", "py", "rust", "cpp", "c", "kotlin", "swift", "php", "csharp", "ruby"}
	for _, lang := range supported {
		if !a.Supports(lang) {
			t.Errorf("expected %s to be supported", lang)
		}
	}
	if a.Supports("unknown") {
		t.Error("expected unknown to be unsupported")
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
	if len(doc.Classes) != 1 {
		t.Errorf("expected 1 class (User), got %d: %+v", len(doc.Classes), doc.Classes)
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
	// 真实调用 u.Greet 与 realCall 应被检出
	foundGreet, foundReal := false, false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "u.Greet" {
			foundGreet = true
		}
		if c.CalleeFQN == "realCall" {
			foundReal = true
		}
	}
	if !foundGreet || !foundReal {
		t.Errorf("expected u.Greet & realCall detected, got calls: %+v", doc.Calls)
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
