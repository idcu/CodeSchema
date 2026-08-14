package treesitter

import (
	"context"
	"os"
	"path/filepath"
	"strings"
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
	supported := []string{"go", "java", "ts", "py", "rust", "cpp", "c", "kotlin", "swift", "php", "csharp", "ruby", "bash", "scala", "sql", "elixir", "ocaml", "lua", "groovy", "css", "toml", "yaml", "protobuf", "html", "hcl", "svelte"}
	unsupported := []string{"unknown"}

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

// TestTreeSitterAdapter_Parse_Swift 验证 Swift 类/方法/调用解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_Swift(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "UserService.swift")
	content := `import Foundation

class UserService {
    func getUser(id: Int) -> User {
        let s = "fakeCall(1)"
        return repository.findById(id)
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
	if doc.Language != "swift" {
		t.Errorf("expected swift, got %s", doc.Language)
	}
	if len(doc.Classes) != 1 {
		t.Errorf("expected 1 class (UserService), got %d", len(doc.Classes))
	}
	found := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "repository.findById" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected repository.findById detected, got calls: %+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_PHP 验证 PHP 类/方法/调用解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_PHP(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "OrderService.php")
	content := `<?php

class OrderService {
    public function createOrder($order) {
        $s = "fakeCall(1)";
        return $this->payment->pay($order);
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
	if doc.Language != "php" {
		t.Errorf("expected php, got %s", doc.Language)
	}
	if len(doc.Classes) != 1 {
		t.Errorf("expected 1 class (OrderService), got %d", len(doc.Classes))
	}
	found := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "pay" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected pay detected, got calls: %+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_CSharp 验证 C# 类/方法/调用解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_CSharp(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "OrderService.cs")
	content := `using System;

public class OrderService {
    public Order Create(OrderDto dto) {
        var s = "fakeCall(1)";
        return validator.Validate(dto);
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
	if doc.Language != "csharp" {
		t.Errorf("expected csharp, got %s", doc.Language)
	}
	if len(doc.Classes) != 1 {
		t.Errorf("expected 1 class (OrderService), got %d", len(doc.Classes))
	}
	found := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "validator.Validate" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected validator.Validate detected, got calls: %+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_Ruby 验证 Ruby 类/方法/调用解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_Ruby(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "order_service.rb")
	content := `class OrderService
  def create(order)
    s = "fakeCall(1)"
    validator.validate(order)
  end
end
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "ruby" {
		t.Errorf("expected ruby, got %s", doc.Language)
	}
	found := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "validate" || c.CalleeFQN == "validator.validate" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected validate detected, got calls: %+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_C 验证 C 语言 struct/函数/调用解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_C(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "engine.c")
	content := `#include <stdio.h>

struct Engine {
    int rpm;
};

int start(struct Engine *e) {
    char *s = "fakeCall(1)";
    fuelPump.pump();
    return ignition.fire(e);
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "c" {
		t.Errorf("expected c, got %s", doc.Language)
	}
	found := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "fuelPump.pump" || c.CalleeFQN == "ignition.fire" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected C calls detected, got calls: %+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_Bash 验证 Bash 函数/调用解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_Bash(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "deploy.sh")
	content := `#!/bin/bash
deploy() {
  build_app
  deploy_app
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "bash" {
		t.Errorf("expected bash, got %s", doc.Language)
	}
	found := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "build_app" || c.CalleeFQN == "deploy_app" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected bash calls detected, got calls: %+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_Scala 验证 Scala 类/方法/调用解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_Scala(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "OrderService.scala")
	content := `class OrderService {
  def create(order: Order): Order = {
    val s = "fakeCall(1)"
    validator.validate(order)
    mapper.map(order)
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
	if doc.Language != "scala" {
		t.Errorf("expected scala, got %s", doc.Language)
	}
	found := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "validator.validate" || c.CalleeFQN == "mapper.map" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected scala calls detected, got calls: %+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_SQL 验证 SQL 表/视图/过程/调用解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_SQL(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "orders.sql")
	content := `-- 订单表
CREATE TABLE orders (
    id INT PRIMARY KEY,
    amount DECIMAL(10,2)
);

CREATE VIEW paid_orders AS
SELECT * FROM orders WHERE amount > 0;

CREATE PROCEDURE process_orders()
BEGIN
    SELECT COUNT(*) FROM paid_orders;
    CALL archive_orders();
END;
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "sql" {
		t.Errorf("expected sql, got %s", doc.Language)
	}
	if len(doc.Classes) == 0 {
		t.Error("expected at least 1 SQL declaration (table/view/procedure)")
	}
	// CALL 语句 grammar 不支持（解析为 ERROR 节点，见 go-tree-sitter SQL grammar 局限）；
	// 验证 SELECT COUNT(*) 的 invocation 检出（AST 版）或任意调用检出（正则版）
	found := false
	for _, c := range doc.Calls {
		if strings.Contains(c.CalleeFQN, "COUNT") || strings.Contains(c.CalleeFQN, "paid_orders") || strings.Contains(c.CalleeFQN, "archive") {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected SQL call detected, got calls: %+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_Elixir 验证 Elixir 模块/方法/调用解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_Elixir(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "order_service.ex")
	content := `defmodule OrderService do
  def create(order) do
    s = "fakeCall(1)"
    validator.validate(order)
    mapper.map(order)
  end
end
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "elixir" {
		t.Errorf("expected elixir, got %s", doc.Language)
	}
	if len(doc.Classes) != 1 {
		t.Errorf("expected 1 module (OrderService), got %d", len(doc.Classes))
	}
	found := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "validator.validate" || c.CalleeFQN == "mapper.map" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected elixir calls detected, got calls: %+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_OCaml 验证 OCaml 模块/函数/调用解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_OCaml(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "order_service.ml")
	content := `module OrderService = struct
  let create order =
    let s = "fakeCall(1)" in
    validator.validate order;
    mapper.map order
end
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "ocaml" {
		t.Errorf("expected ocaml, got %s", doc.Language)
	}
	if len(doc.Classes) != 1 {
		t.Errorf("expected 1 module (OrderService), got %d", len(doc.Classes))
	}
	found := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "validator.validate" || c.CalleeFQN == "mapper.map" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected ocaml calls detected, got calls: %+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_Lua 验证 Lua 函数/调用解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_Lua(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "order_service.lua")
	content := `local OrderService = {}
function OrderService.create(order)
  local s = "fakeCall(1)"
  validator.validate(order)
  mapper.map(order)
end
return OrderService
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "lua" {
		t.Errorf("expected lua, got %s", doc.Language)
	}
	found := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "validator.validate" || c.CalleeFQN == "mapper.map" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected lua calls detected, got calls: %+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_Groovy 验证 Groovy 类/方法/调用解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_Groovy(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "OrderService.groovy")
	content := `class OrderService {
    def create(order) {
        def s = "fakeCall(1)"
        validator.validate(order)
        mapper.map(order)
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
	if doc.Language != "groovy" {
		t.Errorf("expected groovy, got %s", doc.Language)
	}
	if len(doc.Classes) != 1 {
		t.Errorf("expected 1 class (OrderService), got %d", len(doc.Classes))
	}
	found := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "validator.validate" || c.CalleeFQN == "mapper.map" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected groovy calls detected, got calls: %+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_CSS 验证 CSS 选择器/媒体查询解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_CSS(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.css")
	content := `.button {
  color: red;
}
@media (max-width: 600px) {
  .small { font-size: 10px; }
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "css" {
		t.Errorf("expected css, got %s", doc.Language)
	}
	if len(doc.Classes) == 0 {
		t.Error("expected at least 1 CSS declaration (selector/media)")
	}
}

// TestTreeSitterAdapter_Parse_TOML 验证 TOML 表段解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_TOML(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.toml")
	content := `[server]
host = "localhost"
port = 8080
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "toml" {
		t.Errorf("expected toml, got %s", doc.Language)
	}
}

// TestTreeSitterAdapter_Parse_YAML 验证 YAML 解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_YAML(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "app.yaml")
	content := `server:
  host: localhost
  port: 8080
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "yaml" {
		t.Errorf("expected yaml, got %s", doc.Language)
	}
}

// TestTreeSitterAdapter_Parse_Protobuf 验证 protobuf service/rpc 解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_Protobuf(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "order_service.proto")
	content := `syntax = "proto3";

service OrderService {
  rpc CreateOrder(CreateOrderReq) returns (CreateOrderResp);
}
message CreateOrderReq {
  string order_id = 1;
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "protobuf" {
		t.Errorf("expected protobuf, got %s", doc.Language)
	}
	if len(doc.Classes) < 2 {
		t.Errorf("expected service+message classes, got %d", len(doc.Classes))
	}
}

// TestTreeSitterAdapter_Parse_HTML 验证 HTML 元素解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_HTML(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "index.html")
	content := `<html>
<body>
  <div class="container"></div>
  <button onclick="submit()">Go</button>
</body>
</html>
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "html" {
		t.Errorf("expected html, got %s", doc.Language)
	}
	if len(doc.Classes) < 3 {
		t.Errorf("expected html/body/div/button elements, got %d", len(doc.Classes))
	}
}

// TestTreeSitterAdapter_Parse_HCL 验证 Terraform HCL 块解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_HCL(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.tf")
	content := `resource "aws_instance" "web" {
  ami = "ami-123"
}
variable "region" {
  default = "us-east-1"
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "hcl" {
		t.Errorf("expected hcl, got %s", doc.Language)
	}
	if len(doc.Classes) < 2 {
		t.Errorf("expected resource+variable blocks, got %d", len(doc.Classes))
	}
}

// TestTreeSitterAdapter_Parse_Svelte 验证 Svelte 组件解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_Svelte(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "Counter.svelte")
	content := `<script>
  let count = 0;
  function increment() {
    count += 1;
  }
</script>
<button on:click={increment}>{count}</button>
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "svelte" {
		t.Errorf("expected svelte, got %s", doc.Language)
	}
	if len(doc.Classes) == 0 {
		t.Error("expected at least 1 svelte component (script)")
	}
}
