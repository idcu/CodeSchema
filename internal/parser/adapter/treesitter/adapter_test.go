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
	supported := []string{"go", "java", "ts", "py", "rust", "cpp", "c", "kotlin", "swift", "php", "csharp", "ruby", "bash", "scala", "sql", "elixir", "ocaml", "lua", "groovy", "css", "toml", "yaml", "protobuf", "html", "hcl", "svelte", "markdown", "dockerfile", "elm", "cue"}
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

	// 检查类解析：User 为真实 class；顶层函数 main 触发合成包级容器（P0-1）。
	if len(doc.Classes) != 2 {
		t.Fatalf("expected 2 classes (User + PACKAGE main), got %d", len(doc.Classes))
	}
	if doc.Classes[0].Name != "User" {
		t.Errorf("expected class User, got %s", doc.Classes[0].Name)
	}
	if doc.Classes[0].Type != "CLASS" {
		t.Errorf("expected CLASS, got %s", doc.Classes[0].Type)
	}
	if doc.Classes[1].Name != "main" || doc.Classes[1].Type != "PACKAGE" {
		t.Errorf("expected PACKAGE container main, got name=%s type=%s", doc.Classes[1].Name, doc.Classes[1].Type)
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

// TestTreeSitterAdapter_Parse_JavaInlineAnnotationMethod 负控：行内注解
// （`public @ResponseBody Vets showResourcesVetList()`）中的方法曾被 methodPattern
// 漏解析——修饰符组不含 `@`，注解被当作返回类型的一部分而无法匹配。
// 后果：该方法不进索引，人工真值自检直接报「production not found」。
func TestTreeSitterAdapter_Parse_JavaInlineAnnotationMethod(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "VetController.java")
	content := `package demo;

public class VetController {
    @GetMapping("/vets")
    public @ResponseBody Vets showResourcesVetList() {
        return new Vets();
    }
}
`
	os.WriteFile(path, []byte(content), 0644)

	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	found := false
	for _, m := range doc.Methods {
		if m.Name == "showResourcesVetList" {
			found = true
		}
	}
	if !found {
		t.Fatalf("inline annotation method not parsed: methods=%+v", doc.Methods)
	}
}

// TestTreeSitterAdapter_Parse_JavaBodyStatementsAreNotMethods 负控：
// Java 方法体内的 `return helper(a);` / `new Foo().bar();` 曾被正则方法模式
// 误判为「返回类型 + 方法名」声明，导致方法数虚增且调用边被整行 continue 吞掉。
func TestTreeSitterAdapter_Parse_JavaBodyStatementsAreNotMethods(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "Calculator.java")
	content := `package demo;

public class Calculator {
    public int add(int a, int b) {
        return helper(a, b);
    }

    private int helper(int a, int b) {
        Calculator c = new Calculator();
        return c.mul(a, b);
    }

    private int mul(int a, int b) { return a * b; }
}
`
	os.WriteFile(path, []byte(content), 0644)

	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	// 方法体语句不得被当作方法声明：add/helper/mul 共 3 个。
	if len(doc.Methods) != 3 {
		names := make([]string, 0, len(doc.Methods))
		for _, m := range doc.Methods {
			names = append(names, m.Name)
		}
		t.Fatalf("methods = %v (len=%d), want [add helper mul]", names, len(doc.Methods))
	}
	for _, m := range doc.Methods {
		if m.ClassFQN != "Calculator" {
			t.Errorf("method %s ClassFQN = %q, want Calculator", m.Name, m.ClassFQN)
		}
	}

	// 方法体内的调用必须被抽取（调用边依赖这些行不再被 continue 吞掉）。
	var sawHelper, sawMul bool
	for _, c := range doc.Calls {
		if c.CallerFQN == "" {
			t.Errorf("call %+v has empty CallerFQN", c)
		}
		if strings.HasSuffix(c.CalleeFQN, "helper") {
			sawHelper = true
		}
		if strings.HasSuffix(c.CalleeFQN, "mul") {
			sawMul = true
		}
	}
	if !sawHelper || !sawMul {
		t.Fatalf("calls = %+v, want edges to helper and mul", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_JavaPackagePrivateMethods 负控：JUnit 5 允许
// 包级可见性测试方法（`void initCreationForm() {`，无 public/private），
// 旧 Java 方法模式强制要求修饰符，导致 Spring 仓测试方法大量漏解析。
func TestTreeSitterAdapter_Parse_JavaPackagePrivateMethods(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "OwnerControllerTests.java")
	content := `package demo;

class OwnerControllerTests {
	void initCreationForm() throws Exception {
		controller.initCreationForm();
	}

	private void helper() {}
}
`
	os.WriteFile(path, []byte(content), 0644)

	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	names := make([]string, 0, len(doc.Methods))
	for _, m := range doc.Methods {
		names = append(names, m.Name)
	}
	if len(doc.Methods) != 2 {
		t.Fatalf("methods = %v, want [initCreationForm helper]", names)
	}
	for _, m := range doc.Methods {
		if m.ClassFQN != "OwnerControllerTests" {
			t.Errorf("method %s ClassFQN = %q", m.Name, m.ClassFQN)
		}
	}
	if len(doc.Calls) == 0 {
		t.Fatalf("expected call edge in package-private method body")
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

// TestTreeSitterAdapter_Parse_JavaAnnotationsNotCalls 负控：类/字段注解
// （@Table(name="x")）不是调用边，旧实现记为 caller 为空的 pkg.Class.Table。
func TestTreeSitterAdapter_Parse_JavaAnnotationsNotCalls(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "Owner.java")
	content := `package demo;

@Entity
@Table(name = "owners")
public class Owner {
    @Column(name = "first_name")
    private String firstName;

    public void save() {
        repo.save();
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
	for _, c := range doc.Calls {
		base := c.CalleeFQN
		if i := strings.LastIndex(base, "."); i >= 0 {
			base = base[i+1:]
		}
		switch base {
		case "Table", "Column", "Entity":
			t.Errorf("annotation recorded as call: %+v", c)
		}
	}
}

// TestTreeSitterAdapter_Parse_TSConstructorSignatureNotCall 负控：.d.ts 等声明式
// 构造器 `constructor();` 不是调用边；旧实现记为 callee=constructor、caller 为空。
func TestTreeSitterAdapter_Parse_TSConstructorSignatureNotCall(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "types.d.ts")
	content := `export declare class Server {
    constructor(port: number);
    start(): void;
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	for _, c := range doc.Calls {
		if c.CalleeFQN == "constructor" || strings.HasSuffix(c.CalleeFQN, ".constructor") {
			t.Errorf("constructor signature recorded as call: %+v", c)
		}
	}
}

// TestTreeSitterAdapter_Parse_TSDeclarationFileMethodsNotRegistered 负控：.d.ts 是类型
// 契约，函数签名与接口成员都没有实现，不得登记为可执行方法或调用边。
func TestTreeSitterAdapter_Parse_TSDeclarationFileMethodsNotRegistered(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "types.d.ts")
	content := `export interface Options {
    mode?: string;
}

export function outline(src: string): string[];

export declare class Server {
    constructor(port: number);
    start(): void;
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Methods) != 0 {
		t.Errorf("declaration file registered %d methods: %+v", len(doc.Methods), doc.Methods)
	}
	if len(doc.Calls) != 0 {
		t.Errorf("declaration file registered %d calls: %+v", len(doc.Calls), doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_TSInterfaceMemberNotMethod 负控：接口成员是类型契约，
// 不登记为方法也不产生调用；同文件的类方法与顶层函数仍须正常入库。
func TestTreeSitterAdapter_Parse_TSInterfaceMemberNotMethod(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "types.ts")
	content := `export interface Repository<T> {
  getAll(): T[];
  add(item: T): void;
}

class Card {
  render(): string {
    return helper();
  }
}

export function top(): void {}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	got := map[string]string{}
	for _, m := range doc.Methods {
		got[m.ClassFQN+"."+m.Name] = m.Name
	}
	for _, bad := range []string{"Repository.getAll", "Repository.add"} {
		if _, ok := got[bad]; ok {
			t.Errorf("interface member registered as method: %s; methods=%+v", bad, doc.Methods)
		}
	}
	for _, want := range []string{"Card.render", "types.top"} {
		if _, ok := got[want]; !ok {
			t.Errorf("missing method %q; methods=%+v", want, doc.Methods)
		}
	}
	found := false
	for _, c := range doc.Calls {
		if c.CallerFQN == "Card.render" && c.CalleeFQN == "helper" {
			found = true
		}
	}
	if !found {
		t.Errorf("call Card.render -> helper not found; calls=%+v", doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_JavaControlFlowNotCalls 负控：控制流关键字（if/for/...）
// 经包+类限定后（demo.Foo.if）曾绕过 isKeyword 过滤，被记为调用边。
func TestTreeSitterAdapter_Parse_JavaControlFlowNotCalls(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "Foo.java")
	content := `package demo;

public class Foo {
    public void run(int x) {
        if (x > 0) { helper(x); }
        for (int i = 0; i < x; i++) { helper(i); }
        while (x > 0) { x--; }
        switch (x) { case 1: break; default: break; }
        synchronized (this) { helper(x); }
        try { helper(x); } catch (Exception e) { throw e; }
    }

    private void helper(int i) { }
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	blocked := map[string]bool{
		"if": true, "for": true, "while": true, "switch": true,
		"catch": true, "synchronized": true, "try": true, "case": true,
	}
	var helperCalls int
	for _, c := range doc.Calls {
		name := c.CalleeFQN
		if i := strings.LastIndex(name, "."); i >= 0 {
			name = name[i+1:]
		}
		if blocked[name] {
			t.Errorf("control-flow keyword recorded as call: %s (caller=%s)", c.CalleeFQN, c.CallerFQN)
		}
		if name == "helper" {
			helperCalls++
		}
	}
	if helperCalls == 0 {
		t.Errorf("expected helper() calls to remain, got %d calls: %+v", len(doc.Calls), doc.Calls)
	}
}

// TestTreeSitterAdapter_Parse_TypeScriptTopLevelFunctions 负控：顶层 function 与
// 无返回类型注解的方法都必须入索引（旧 TS 模式强制 `):`，这两类全部漏解析）。
func TestTreeSitterAdapter_Parse_TypeScriptTopLevelFunctions(t *testing.T) {
	if BuildVariant() != "regex" {
		t.Skip("断言默认正则路径的 TS 模块限定 FQN；AST 变体由 adapter_ast_test.go 覆盖")
	}
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "handlers.ts")
	content := `export function buildIndex(items: string[]): number {
    return items.length;
}

async function loadConfig() {
    return readConfig();
}

function plain() {
    return buildIndex([]);
}

export class Server {
    start(port: number) {
        return listen(port);
    }

    stop(): void {
        closeAll();
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

	got := make(map[string]string, len(doc.Methods))
	for _, m := range doc.Methods {
		got[m.Name] = m.ClassFQN
	}
	for name, class := range map[string]string{
		"buildIndex": "handlers",
		"loadConfig": "handlers",
		"plain":      "handlers",
	} {
		fqn, ok := got[name]
		if !ok {
			t.Errorf("top-level function %q not parsed; methods=%v", name, got)
			continue
		}
		if fqn != class {
			t.Errorf("%q ClassFQN = %q, want %q", name, fqn, class)
		}
	}
	if fqn, ok := got["start"]; !ok || fqn != "Server" {
		t.Errorf("method without return annotation: got (%q, %v), want (\"Server\", true)", fqn, ok)
	}
	if fqn, ok := got["stop"]; !ok || fqn != "Server" {
		t.Errorf("method with return annotation: got (%q, %v), want (\"Server\", true)", fqn, ok)
	}

	// 同模块裸名调用应限定为 module.func；导入函数保持裸名。
	var localCall, importedCall bool
	for _, c := range doc.Calls {
		if c.CallerFQN == "handlers.plain" && c.CalleeFQN == "handlers.buildIndex" {
			localCall = true
		}
		if c.CallerFQN == "handlers.loadConfig" && c.CalleeFQN == "readConfig" {
			importedCall = true
		}
	}
	if !localCall {
		t.Errorf("同模块调用未限定为 handlers.buildIndex; calls=%+v", doc.Calls)
	}
	if !importedCall {
		t.Errorf("导入函数裸名调用被错误限定; calls=%+v", doc.Calls)
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
	path := filepath.Join(dir, "readme.unknown_ext")
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

// TestTreeSitterAdapter_Parse_JavaNestedCalls 验证实参位置的内层调用不丢边。
//
// 正则 callPattern 为非重叠匹配：`assertThat(owner.getLastName())` 会被外层
// assertThat 整段吞掉。基准（Java 人工真值扩容）中该形态覆盖 getLastName/getId/
// getPets/getSpecialties/supports 等，全被计为漏召；AST 路径因按 invocation 节点
// 遍历天然可见。此用例约束两条路径口径一致。
//
// 接收者按方法参数的可见类型限定：`owner`/`petValidator` 的声明类型分别是
// Owner/PetValidator，故期望 `Owner.getLastName` 等而非裸 `owner.getLastName`。
func TestTreeSitterAdapter_Parse_JavaNestedCalls(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "OwnerControllerTests.java")
	content := `package com.example;

class OwnerControllerTests {
    void check(Owner owner, PetValidator petValidator) {
        assertThat(owner.getLastName()).startsWith("Franklin");
        assertTrue(petValidator.supports(Pet.class));
        assertThat(owner.getId()).isNotZero();
        assertEquals(1, owner.getPets().size());
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
	want := []string{"Owner.getLastName", "PetValidator.supports", "Owner.getId", "Owner.getPets"}
	got := make(map[string]bool, len(doc.Calls))
	for _, c := range doc.Calls {
		got[c.CalleeFQN] = true
	}
	for _, w := range want {
		if !got[w] {
			t.Errorf("expected nested callee %q in calls, got %+v", w, doc.Calls)
		}
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
		// Go 默认路径产出包限定 FQN（T1 修复：main.realCall 而非裸 realCall）
		case "main.realCall":
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

// TestTreeSitterAdapter_Parse_Markdown 验证 Markdown 解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_Markdown(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "readme.md")
	content := `# Title
Some text.
## Section
- item
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "markdown" {
		t.Errorf("expected markdown, got %s", doc.Language)
	}
}

// TestTreeSitterAdapter_Parse_Dockerfile 验证 Dockerfile 解析（T6-2 扩展，无扩展名按文件名识别）。
func TestTreeSitterAdapter_Parse_Dockerfile(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "Dockerfile")
	content := `FROM golang:1.22
RUN go build -o app .
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "dockerfile" {
		t.Errorf("expected dockerfile, got %s", doc.Language)
	}
}

// TestTreeSitterAdapter_Parse_Elm 验证 Elm 模块解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_Elm(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "Main.elm")
	content := `module Main exposing (main)

main =
    text "Hello"
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "elm" {
		t.Errorf("expected elm, got %s", doc.Language)
	}
}

// TestTreeSitterAdapter_Parse_CUE 验证 CUE 配置解析（T6-2 扩展）。
func TestTreeSitterAdapter_Parse_CUE(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "config.cue")
	content := `package config

server: {
  host: "localhost"
  port: 8080
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if doc.Language != "cue" {
		t.Errorf("expected cue, got %s", doc.Language)
	}
}

// TestTreeSitterAdapter_Parse_NonGoCallerPkgQualified 验证非 Go 语言调用图 caller FQN 包限定
// （P19：Java/Kotlin/C# 等显式包声明语言，caller 产出 pkg.Class.Method，与 Go 精确路径命名空间对齐）。
func TestTreeSitterAdapter_Parse_NonGoCallerPkgQualified(t *testing.T) {
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
			found := false
			for _, c := range doc.Calls {
				if strings.Contains(c.CalleeFQN, "pay") || strings.Contains(c.CalleeFQN, "findById") || strings.Contains(c.CalleeFQN, "Save") {
					if c.CallerFQN == tc.wantCaller {
						found = true
						break
					}
				}
			}
			if !found {
				t.Errorf("callerFQN=%q not found on callee call; calls=%+v", tc.wantCaller, doc.Calls)
			}
		})
	}
}

// TestTreeSitterAdapter_Parse_NonGoBareCalleePkgQualified 验证非 Go 语言裸 callee
// （当前类内无点前缀的方法调用）的包限定（P21 遗留收敛）：
// Java/Kotlin/C# 显式包声明语言，裸 callee 产出 pkg.Class.Method，与 caller 命名空间对齐。
func TestTreeSitterAdapter_Parse_NonGoBareCalleePkgQualified(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()

	cases := []struct {
		name, filename, content, wantCallee string
	}{
		{
			name:     "java",
			filename: "OrderService.java",
			content: `package com.example;

public class OrderService {
    public void createOrder(Order order) {
        helper();
    }
    private void helper() {}
}
`,
			wantCallee: "com.example.OrderService.helper",
		},
		{
			name:     "kotlin",
			filename: "UserService.kt",
			content: `package com.example

class UserService {
    fun getUser(id: Long) {
        normalize(id)
    }
    private fun normalize(id: Long): Long = id
}
`,
			wantCallee: "com.example.UserService.normalize",
		},
		{
			name:     "csharp",
			filename: "OrderService.cs",
			content: `namespace Acme.Orders;

public class OrderService {
    public void Create(Order order) {
        Normalize(order);
    }
    private void Normalize(Order o) {}
}
`,
			wantCallee: "Acme.Orders.OrderService.Normalize",
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
			found := false
			for _, c := range doc.Calls {
				if c.CalleeFQN == tc.wantCallee {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("bare callee FQN=%q not found; calls=%+v", tc.wantCallee, doc.Calls)
			}
		})
	}
}

// TestTreeSitterAdapter_CallProvenance 负控（R5-3）：解析产出的每条调用边都必须
// 带来源与置信度，不得留空让上层无法判断精度。
// regex 变体应为 regex/70；AST 变体为 treesitter/90（同一断言取共同下界）。
func TestTreeSitterAdapter_CallProvenance(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "main.go")
	content := `package main

import "fmt"

func Run() {
	fmt.Println("hi")
}
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if len(doc.Calls) == 0 {
		t.Fatal("expected calls parsed")
	}
	for _, c := range doc.Calls {
		if c.Source == "" {
			t.Errorf("call %s -> %s missing source", c.CallerFQN, c.CalleeFQN)
		}
		if c.Confidence < 70 {
			t.Errorf("call %s -> %s confidence=%d, want >=70", c.CallerFQN, c.CalleeFQN, c.Confidence)
		}
	}
}

// TestTreeSitterAdapter_NonGoScopeAttribution 负控（R5-5）：嵌套类结束后
// 外层方法应回到外层类，顶层函数不得粘到上一个类。
func TestTreeSitterAdapter_NonGoScopeAttribution(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "scope.rb")
	content := `module Outer
  class Inner
    def inner_method()
      helper()
    end
  end

  def outer_method()
    helper()
  end
end

def top_level()
  helper()
end
`
	if err := os.WriteFile(path, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
	doc, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}

	want := map[string]string{
		"inner_method": "Inner",
		"outer_method": "Outer",
		"top_level":    "",
	}
	got := make(map[string]string, len(doc.Methods))
	for _, m := range doc.Methods {
		got[m.Name] = m.ClassFQN
	}
	for name, wantFQN := range want {
		if got[name] != wantFQN {
			t.Errorf("method %s ClassFQN = %q, want %q (all=%v)", name, got[name], wantFQN, got)
		}
	}
}

// TestTreeSitterAdapter_ElixirMethodNames 负控（R5-5）：Elixir 方法名在
// 正则第 2 捕获组，取末组会把 `(`/`do` 当成方法名。
func TestTreeSitterAdapter_ElixirMethodNames(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "demo.ex")
	content := `defmodule Demo do
  def run(x) do
    helper(x)
  end

  defp priv do
    :ok
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

	names := make(map[string]bool, len(doc.Methods))
	for _, m := range doc.Methods {
		names[m.Name] = true
	}
	for _, want := range []string{"run", "priv"} {
		if !names[want] {
			t.Errorf("missing method %q, got %v", want, names)
		}
	}
	if names["("] || names["do"] {
		t.Errorf("method name captured punctuation/keyword: %v", names)
	}
}

// TestTreeSitterAdapter_CppMethodName 负控（R5-5）：C++ 方法名在第 2 捕获组，
// 取末组会把 `const`/`override` 当成方法名。
func TestTreeSitterAdapter_CppMethodName(t *testing.T) {
	a := NewTreeSitterAdapter()
	dir := t.TempDir()
	path := filepath.Join(dir, "engine.cpp")
	content := `class Engine {
public:
    void start() const {
        fuelPump.pump();
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
	for _, m := range doc.Methods {
		if m.Name == "start" {
			found = true
			if m.ClassFQN != "Engine" {
				t.Errorf("start ClassFQN = %q, want Engine", m.ClassFQN)
			}
		}
		if m.Name == "const" || m.Name == "override" {
			t.Errorf("method name captured qualifier: %q", m.Name)
		}
	}
	if !found {
		t.Errorf("method start not found, got %+v", doc.Methods)
	}
}

// TestTreeSitterAdapter_JavaReceiverType 是 Java 同名方法歧义的负控：
// `owner6.addVisit(...)` 的接收者是可见类型 Owner，应限定为 `Owner.addVisit`；
// `pet.addVisit(...)` 应限定为 `Pet.addVisit`。同时方法参数要进 IR（Params），
// 供按实参个数消歧使用。
func TestTreeSitterAdapter_JavaReceiverType(t *testing.T) {
	a := NewTreeSitterAdapter()
	path := filepath.Join(t.TempDir(), "OwnerControllerTests.java")
	content := `package org.demo;

class Owner {
    public void addVisit(Integer petId, Visit visit) {}
}

class Pet {
    public void addVisit(Visit visit) {}
}

class Visit {}

class OwnerControllerTests {
    void shouldAddNewVisitForPet() {
        Owner owner6 = new Owner();
        Pet pet7 = new Pet();
        Visit visit = new Visit();
        owner6.addVisit(pet7.getId(), visit);
    }

    void shouldAddVisitForPet() {
        Pet pet = new Pet();
        pet.addVisit(null);
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

	want := map[string]string{
		"Owner.addVisit": "shouldAddNewVisitForPet",
		"Pet.addVisit":   "shouldAddVisitForPet",
	}
	got := map[string]string{}
	for _, c := range doc.Calls {
		for callee, caller := range want {
			if strings.HasSuffix(c.CalleeFQN, callee) && strings.HasSuffix(c.CallerFQN, caller) {
				got[callee] = c.CalleeFQN
			}
		}
	}
	for callee := range want {
		if got[callee] == "" {
			var pairs []string
			for _, c := range doc.Calls {
				pairs = append(pairs, c.CallerFQN+" -> "+c.CalleeFQN)
			}
			t.Errorf("missing qualified callee %s, calls=%v", callee, pairs)
		}
	}

	// 参数信息进 IR：Owner.addVisit(Integer, Visit) 两个参数。
	for _, m := range doc.Methods {
		if m.Name != "addVisit" || !strings.HasSuffix(m.ClassFQN, "Owner") {
			continue
		}
		if len(m.Params) != 2 {
			t.Fatalf("Owner.addVisit params = %+v, want 2", m.Params)
		}
		if m.Params[0].Type != "Integer" || m.Params[1].Type != "Visit" {
			t.Errorf("Owner.addVisit param types = %q,%q, want Integer,Visit", m.Params[0].Type, m.Params[1].Type)
		}
	}
}

// TestTreeSitterAdapter_Parse_JestCallbacksAsMethods 约束 Jest describe/it/test
// 回调合成为 IR 方法：回调体内的调用必须带 CallerFQN，导入函数限定为模块 FQN。
func TestTreeSitterAdapter_Parse_JestCallbacksAsMethods(t *testing.T) {
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
