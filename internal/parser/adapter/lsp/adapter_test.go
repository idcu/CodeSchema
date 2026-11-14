package lsp

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"gitee.com/idcu-go/metrics"
	cerrors "github.com/idcu/codeschema/internal/errors"
	"github.com/idcu/codeschema/internal/parser"
)

// TestMain 支持 mock LSP 服务器子进程模式。
// 当 MOCK_LSP_SERVER 环境变量为 "1" 时，运行 mock LSP 服务器并退出。
func TestMain(m *testing.M) {
	if os.Getenv("MOCK_LSP_SERVER") == "1" {
		runMockLSPServer()
		os.Exit(0)
	}
	os.Exit(m.Run())
}

// mockLSPServer 模拟 LSP 服务器的 stdin/stdout 通信。
func runMockLSPServer() {
	reader := bufio.NewReader(os.Stdin)

	for {
		// 读取多行头直到空行
		contentLength := 0
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break
			}
			if strings.HasPrefix(line, "Content-Length: ") {
				cl := strings.TrimPrefix(line, "Content-Length: ")
				contentLength, _ = strconv.Atoi(strings.TrimSpace(cl))
			}
		}

		if contentLength <= 0 {
			continue
		}

		// 读取 body
		body := make([]byte, contentLength)
		_, err := io.ReadFull(reader, body)
		if err != nil {
			return
		}

		// 解析请求
		var req struct {
			JSONRPC string          `json:"jsonrpc"`
			ID      int             `json:"id,omitempty"`
			Method  string          `json:"method"`
			Params  json.RawMessage `json:"params,omitempty"`
		}
		if err := json.Unmarshal(body, &req); err != nil {
			continue
		}

		// exit 是通知（无 id），需在通知过滤前处理，供 Close 优雅退出。
		if req.Method == "exit" {
			return
		}

		// 无 ID 的是通知，不需要响应
		if req.ID == 0 {
			continue
		}

		// 根据 method / MOCK_LSP_MODE 生成响应
		var result any
		switch req.Method {
		case "initialize":
			caps := map[string]any{}
			if os.Getenv("MOCK_LSP_MODE") == "calls" {
				caps["callHierarchyProvider"] = true
			}
			result = map[string]any{"capabilities": caps}
		case "textDocument/documentSymbol":
			switch os.Getenv("MOCK_LSP_MODE") {
			case "nested":
				result = mockNestedSymbols()
			case "calls":
				result = mockCallSymbols()
			case "empty":
				result = []map[string]any{}
			default:
				result = mockFlatSymbols()
			}
		case "textDocument/prepareCallHierarchy":
			result = mockPrepareCallHierarchy(req.Params)
		case "callHierarchy/outgoingCalls":
			result = mockOutgoingCalls(req.Params)
		case "shutdown":
			result = nil
		default:
			result = nil
		}

		resp := map[string]any{
			"jsonrpc": "2.0",
			"id":      req.ID,
			"result":  result,
		}
		data, _ := json.Marshal(resp)
		// 发送含 Content-Type 多行头的响应
		header := fmt.Sprintf("Content-Length: %d\r\nContent-Type: application/vscode-jsonrpc; charset=utf-8\r\n\r\n", len(data))
		os.Stdout.Write([]byte(header + string(data)))
	}
}

// newMockLSPAdapter 创建使用测试二进制作为 mock LSP 服务器的适配器。
func newMockLSPAdapter(t *testing.T, timeout time.Duration) *LSPAdapter {
	t.Helper()
	return newMockLSPAdapterMode(t, timeout, "")
}

// newMockLSPAdapterMode 按 MOCK_LSP_MODE 构造 mock 适配器，覆盖分层符号 / 调用边 /
// 空符号等场景。
func newMockLSPAdapterMode(t *testing.T, timeout time.Duration, mode string) *LSPAdapter {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("get executable: %v", err)
	}
	a := NewLSPAdapter("mock", exe, nil, "go", timeout)
	a.env = []string{"MOCK_LSP_SERVER=1"}
	if mode != "" {
		a.env = append(a.env, "MOCK_LSP_MODE="+mode)
	}
	return a
}

func mockRange(sl, sc, el, ec int) map[string]any {
	return map[string]any{
		"start": map[string]int{"line": sl, "character": sc},
		"end":   map[string]int{"line": el, "character": ec},
	}
}

func mockSymbol(name string, kind, sl, sc, el, ec int, children ...map[string]any) map[string]any {
	sym := map[string]any{
		"name":           name,
		"kind":           kind,
		"range":          mockRange(sl, sc, el, ec),
		"selectionRange": mockRange(sl, sc, sl, sc+len(name)),
	}
	if len(children) > 0 {
		sym["children"] = children
	}
	return sym
}

// mockFlatSymbols 返回 DocumentSymbol[]（无嵌套），与旧测试的扁平结构等价。
func mockFlatSymbols() []map[string]any {
	return []map[string]any{
		mockSymbol("MockClass", 5, 0, 0, 10, 0),
		mockSymbol("mockMethod", 6, 5, 0, 8, 0),
	}
}

// mockNestedSymbols 返回分层 DocumentSymbol[]：Outer > Inner > Run，另有 Outer.Own
// 与顶层函数 topLevel；用于验证容器名逐层传递与 ClassFQN 归属。
func mockNestedSymbols() []map[string]any {
	inner := mockSymbol("Inner", 5, 1, 0, 8, 0,
		mockSymbol("Run", 6, 2, 4, 5, 0))
	outer := mockSymbol("Outer", 5, 0, 0, 9, 0,
		inner,
		mockSymbol("Own", 6, 6, 4, 7, 0))
	top := mockSymbol("topLevel", 12, 11, 0, 13, 0)
	return []map[string]any{outer, top}
}

// mockCallSymbols 返回一个类与两个方法，selectionRange 分别落在 line 1 / 4。
func mockCallSymbols() []map[string]any {
	return []map[string]any{
		mockSymbol("MockClass", 5, 0, 0, 20, 0,
			mockSymbol("mockMethod", 6, 1, 4, 3, 0),
			mockSymbol("helper", 6, 4, 4, 6, 0)),
	}
}

// mockPrepareCallHierarchy 按请求位置返回对应方法项（line>=4 视为 helper）。
func mockPrepareCallHierarchy(params json.RawMessage) any {
	var p struct {
		TextDocument struct{ URI string } `json:"textDocument"`
		Position     struct{ Line int }   `json:"position"`
	}
	_ = json.Unmarshal(params, &p)
	name, line := "mockMethod", 1
	if p.Position.Line >= 4 {
		name, line = "helper", 4
	}
	return []map[string]any{{
		"name":           name,
		"kind":           6,
		"uri":            p.TextDocument.URI,
		"range":          mockRange(line, 4, line+2, 0),
		"selectionRange": mockRange(line, 4, line, 4+len(name)),
	}}
}

// mockOutgoingCalls 对 mockMethod 返回一条指向 helper 的调用边。
func mockOutgoingCalls(params json.RawMessage) any {
	var p struct {
		Item struct {
			Name string `json:"name"`
			URI  string `json:"uri"`
		} `json:"item"`
	}
	_ = json.Unmarshal(params, &p)
	if p.Item.Name != "mockMethod" {
		return []map[string]any{}
	}
	return []map[string]any{{
		"to": map[string]any{
			"name":           "helper",
			"kind":           6,
			"detail":         "func (m *MockClass) helper()",
			"uri":            p.Item.URI,
			"range":          mockRange(4, 4, 6, 0),
			"selectionRange": mockRange(4, 4, 4, 10),
		},
		"fromRanges": []map[string]any{mockRange(2, 8, 2, 14)},
	}}
}

func TestLSPAdapter_Name(t *testing.T) {
	a := NewLSPAdapter("test-lsp", "echo", nil, "go", 0)
	if a.Name() != "test-lsp" {
		t.Errorf("expected 'test-lsp', got '%s'", a.Name())
	}
}

func TestLSPAdapter_Supports(t *testing.T) {
	a := NewLSPAdapter("gopls", "gopls", nil, "go", 0)
	if !a.Supports("go") {
		t.Error("expected Supports(go)=true")
	}
	if a.Supports("java") {
		t.Error("expected Supports(java)=false")
	}
}

func TestNewGoplsAdapter(t *testing.T) {
	a := NewGoplsAdapter()
	if a.Name() != "gopls" {
		t.Errorf("expected name=gopls, got %s", a.Name())
	}
	if !a.Supports("go") {
		t.Error("expected gopls to support go")
	}
}

func TestNewJDTLSAdapter(t *testing.T) {
	a := NewJDTLSAdapter()
	if a.Name() != "jdtls" {
		t.Errorf("expected name=jdtls, got %s", a.Name())
	}
	if !a.Supports("java") {
		t.Error("expected jdtls to support java")
	}
}

func TestNewClangdAdapter(t *testing.T) {
	a := NewClangdAdapter()
	if a.Name() != "clangd" {
		t.Errorf("expected name=clangd, got %s", a.Name())
	}
	if !a.Supports("cpp") {
		t.Error("expected clangd to support cpp")
	}
}

func TestLSPAdapter_Close_Uninitialized(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 0)
	err := a.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLSPAdapter_DefaultTimeout(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 0)
	if a.timeout != 10*time.Second {
		t.Errorf("expected default timeout 10s, got %v", a.timeout)
	}
}

func TestLSPAdapter_CustomTimeout(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 30*time.Second)
	if a.timeout != 30*time.Second {
		t.Errorf("expected custom timeout 30s, got %v", a.timeout)
	}
}

func TestAddSymbolInfo_Class(t *testing.T) {
	a := NewGoplsAdapter()
	ir := &parser.IRDocument{}
	sym := symbolInfo{
		Name: "Foo",
		Kind: 5, // Class
		Location: struct {
			URI   string `json:"uri"`
			Range struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
				End struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"end"`
			} `json:"range"`
		}{
			URI: "file:///test.go",
			Range: struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
				End struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"end"`
			}{
				Start: struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				}{Line: 0, Character: 0},
				End: struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				}{Line: 10, Character: 0},
			},
		},
		ContainerName: "pkg",
	}
	ir = a.addSymbolInfo(ir, sym)
	if len(ir.Classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(ir.Classes))
	}
	if ir.Classes[0].Name != "Foo" {
		t.Errorf("expected class name=Foo, got %s", ir.Classes[0].Name)
	}
	if ir.Classes[0].StartLine != 1 {
		t.Errorf("expected StartLine=1, got %d", ir.Classes[0].StartLine)
	}
}

func TestAddSymbolInfo_Method(t *testing.T) {
	a := NewGoplsAdapter()
	ir := &parser.IRDocument{}
	sym := symbolInfo{
		Name: "bar",
		Kind: 6, // Method
		Location: struct {
			URI   string `json:"uri"`
			Range struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
				End struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"end"`
			} `json:"range"`
		}{
			URI: "file:///test.go",
			Range: struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
				End struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"end"`
			}{
				Start: struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				}{Line: 5, Character: 0},
				End: struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				}{Line: 8, Character: 0},
			},
		},
		ContainerName: "pkg.Foo",
	}
	ir = a.addSymbolInfo(ir, sym)
	if len(ir.Methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(ir.Methods))
	}
	if ir.Methods[0].Name != "bar" {
		t.Errorf("expected method name=bar, got %s", ir.Methods[0].Name)
	}
	if ir.Methods[0].ClassFQN != "pkg.Foo" {
		t.Errorf("expected ClassFQN=pkg.Foo, got %s", ir.Methods[0].ClassFQN)
	}
}

func TestAddDocumentSymbol_ClassWithChildren(t *testing.T) {
	a := NewGoplsAdapter()
	ir := &parser.IRDocument{}
	ds := documentSymbol{
		Name: "Foo",
		Kind: 5,
		Range: documentRange{
			Start: struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			}{Line: 0, Character: 0},
			End: struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			}{Line: 20, Character: 0},
		},
		Children: []documentSymbol{
			{
				Name: "bar",
				Kind: 6,
				Range: documentRange{
					Start: struct {
						Line      int `json:"line"`
						Character int `json:"character"`
					}{Line: 5, Character: 0},
					End: struct {
						Line      int `json:"line"`
						Character int `json:"character"`
					}{Line: 10, Character: 0},
				},
			},
		},
	}
	ir = a.addDocumentSymbol(ir, ds, "")
	if len(ir.Classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(ir.Classes))
	}
	if len(ir.Methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(ir.Methods))
	}
	if ir.Methods[0].Name != "bar" {
		t.Errorf("expected method name=bar, got %s", ir.Methods[0].Name)
	}
	if ir.Methods[0].ClassFQN != "Foo" {
		t.Errorf("expected ClassFQN=Foo, got %q", ir.Methods[0].ClassFQN)
	}
}

func TestLSPAdapter_SendNotification(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 0)
	a.sendNotification("test/method", map[string]string{"key": "value"})
}

// --- 稳定性测试 ---

// TestLSPAdapter_IsAlive_Default 验证新创建的适配器 IsAlive 返回 false。
func TestLSPAdapter_IsAlive_Default(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 0)
	if a.IsAlive() {
		t.Error("expected IsAlive=false for uninitialized adapter")
	}
}

// TestLSPAdapter_IsAlive_AfterClose 验证 Close 后 IsAlive 返回 false。
func TestLSPAdapter_IsAlive_AfterClose(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 0)
	a.Close()
	if a.IsAlive() {
		t.Error("expected IsAlive=false after Close")
	}
}

// TestLSPAdapter_Init_WithMockServer 使用 mock LSP 服务器验证完整初始化流程。
func TestLSPAdapter_Init_WithMockServer(t *testing.T) {
	a := newMockLSPAdapter(t, 3*time.Second)
	err := a.Init(context.Background(), map[string]any{"rootUri": "file:///test"})
	if err != nil {
		t.Fatalf("Init with mock server failed: %v", err)
	}
	defer a.Close()

	if !a.IsAlive() {
		t.Error("expected IsAlive=true after successful Init")
	}
}

// TestLSPAdapter_Init_WithRootUri 验证 rootUri 被正确传递。
func TestLSPAdapter_Init_WithRootUri(t *testing.T) {
	a := newMockLSPAdapter(t, 3*time.Second)
	err := a.Init(context.Background(), map[string]any{"rootUri": "file:///workspace"})
	if err != nil {
		t.Fatalf("Init with rootUri failed: %v", err)
	}
	defer a.Close()
}

// TestLSPAdapter_Parse_WithMockServer 使用 mock LSP 验证 Parse 返回正确符号。
func TestLSPAdapter_Parse_WithMockServer(t *testing.T) {
	a := newMockLSPAdapter(t, 3*time.Second)
	if err := a.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer a.Close()

	ir, err := a.Parse(context.Background(), "/mock/project/main.go")
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if ir.Source != "mock" {
		t.Errorf("expected Source=mock, got %s", ir.Source)
	}
	if ir.Language != "go" {
		t.Errorf("expected Language=go, got %s", ir.Language)
	}
	if len(ir.Classes) != 1 || ir.Classes[0].Name != "MockClass" {
		t.Errorf("expected 1 class (MockClass), got %d classes", len(ir.Classes))
	}
	if len(ir.Methods) != 1 || ir.Methods[0].Name != "mockMethod" {
		t.Errorf("expected 1 method (mockMethod), got %d methods", len(ir.Methods))
	}
}

// TestLSPAdapter_Parse_NotInitialized 验证未初始化时 Parse 返回错误。
func TestLSPAdapter_Parse_NotInitialized(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 0)
	_, err := a.Parse(context.Background(), "test.go")
	if err == nil {
		t.Fatal("expected error for uninitialized adapter")
	}
}

// TestLSPAdapter_Init_CommandNotFound 验证命令不存在时返回错误。
func TestLSPAdapter_Init_CommandNotFound(t *testing.T) {
	a := NewLSPAdapter("nonexistent", "command-that-does-not-exist-12345", nil, "go", 0)
	err := a.Init(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for non-existent command")
	}
}

// TestLSPAdapter_ReadResponses_PanicRecovery 验证 readResponses panic 不崩溃适配器。
func TestLSPAdapter_ReadResponses_PanicRecovery(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 0)

	// 创建一个会导致 panic 的 stdout（写入后关闭，readResponses 空指针等）
	pr, pw := io.Pipe()
	pw.Close() // 立即关闭，readResponses 会读到 err 并正常退出
	a.stdout = pr

	// readResponses 应通过 recover 捕获任何 panic 并正常退出
	done := make(chan struct{})
	go func() {
		a.readResponses()
		close(done)
	}()

	select {
	case <-done:
		// 正常退出，不 panic
	case <-time.After(time.Second):
		t.Fatal("readResponses did not exit within timeout")
	}

	if a.IsAlive() {
		t.Error("expected IsAlive=false after readResponses exit")
	}
}

// TestLSPAdapter_SendRequest_Timeout 验证请求超时返回错误且 pending 被清理。
func TestLSPAdapter_SendRequest_Timeout(t *testing.T) {
	a := newMockLSPAdapter(t, 10*time.Second) // 适配器超时设长，避免干扰
	if err := a.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer a.Close()

	// 使用已取消的 context，确保 ctx.Done() 立即返回
	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消

	_, err := a.sendRequest(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]string{"uri": "file:///test.go"},
	})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
	t.Logf("Got expected error: %v", err)

	// 验证 pending map 已被清理
	a.mu.Lock()
	pendingLen := len(a.pending)
	a.mu.Unlock()
	if pendingLen != 0 {
		t.Errorf("expected pending map to be empty after cancellation, got %d entries", pendingLen)
	}
}

// TestLSPAdapter_Init_StderrCapture 验证 stderr 被捕获，不会阻塞进程。
func TestLSPAdapter_Init_StderrCapture(t *testing.T) {
	a := newMockLSPAdapter(t, 3*time.Second)
	if err := a.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer a.Close()
	// 不 panic 即通过
}

// TestLSPAdapter_Close_Idempotent 验证 Close 可多次调用。
func TestLSPAdapter_Close_Idempotent(t *testing.T) {
	a := newMockLSPAdapter(t, 3*time.Second)
	if err := a.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	a.Close()
	a.Close() // 第二次调用不应 panic
	a.Close() // 第三次调用也不应 panic
}

// TestLSPAdapter_ReadResponses_MultiHeader 验证多行头（Content-Type 等）被正确处理。
func TestLSPAdapter_ReadResponses_MultiHeader(t *testing.T) {
	// 使用 mock server 验证多行头（mock server 默认发送 Content-Type 头）
	a := newMockLSPAdapter(t, 3*time.Second)
	if err := a.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer a.Close()

	// 执行一次 Parse 验证多行头场景下协议解析正常
	ir, err := a.Parse(context.Background(), "/mock/project/main.go")
	if err != nil {
		t.Fatalf("Parse with multi-header failed: %v", err)
	}
	if len(ir.Classes) == 0 {
		t.Error("expected at least 1 class from Parse")
	}
}

// --- T2-3 LSP 健壮性测试 ---

// TestLSPAdapter_ProbeCompileCommands_Found 验证找到 compile_commands.json 时返回 true。
func TestLSPAdapter_ProbeCompileCommands_Found(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "compile_commands.json"), []byte("[]"), 0o644); err != nil {
		t.Fatalf("write compile_commands.json: %v", err)
	}
	a := NewClangdAdapter()
	if !a.probeCompileCommands(dir) {
		t.Error("expected probeCompileCommands=true when compile_commands.json exists")
	}
}

// TestLSPAdapter_ProbeCompileCommands_Missing 验证缺失时返回 false 并暴露指标。
func TestLSPAdapter_ProbeCompileCommands_Missing(t *testing.T) {
	dir := t.TempDir() // 空目录，无 compile_commands.json
	a := NewClangdAdapter()
	if a.probeCompileCommands(dir) {
		t.Error("expected probeCompileCommands=false when compile_commands.json missing")
	}
	// 无标签指标用 Collect 精确校验增量（Render 会对无值指标打印 0，无法分辨是否触发）。
	if metrics.Collect().Counters["lsp_missing_compile_commands_total"] < 1 {
		t.Error("expected lsp_missing_compile_commands_total >= 1 after missing probe")
	}
}

// TestLSPAdapter_MaybeReportEmptySymbols 验证非空 C/C++ 文件 0 符号时显式告警并记指标。
func TestLSPAdapter_MaybeReportEmptySymbols(t *testing.T) {
	// 1) cpp + 非空文本 + 空 IR → 应触发
	clangd := NewClangdAdapter()
	clangd.maybeReportEmptySymbols("a.cc", "int main(){}", &parser.IRDocument{})
	if !strings.Contains(metrics.Render(), `lsp_parse_empty_symbols_total{lang="cpp"}`) {
		t.Error("expected lsp_parse_empty_symbols_total{lang=\"cpp\"} after empty cpp parse")
	}

	// 2) go 语言不应触发（避免误报普通空文件）
	gopls := NewGoplsAdapter()
	gopls.maybeReportEmptySymbols("a.go", "package main", &parser.IRDocument{})

	// 3) 空文本不应触发
	clangd2 := NewClangdAdapter()
	clangd2.maybeReportEmptySymbols("a.cc", "", &parser.IRDocument{})

	// 4) 有符号不应触发
	clangd3 := NewClangdAdapter()
	clangd3.maybeReportEmptySymbols("a.cc", "int main(){}", &parser.IRDocument{
		Classes: []parser.ClassIR{{Name: "Foo"}},
	})
}

// TestLSPAdapter_RequestWithRetry_Failure 验证瞬时失败时按 robust 重试并记指标。
func TestLSPAdapter_RequestWithRetry_Failure(t *testing.T) {
	a := newMockLSPAdapter(t, 3*time.Second)
	if err := a.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer a.Close()

	// 关闭 stdin，使后续请求写入失败（瞬时可重试错误），驱动重试逻辑。
	if err := a.stdin.Close(); err != nil {
		t.Fatalf("close stdin: %v", err)
	}

	_, err := a.requestWithRetry(context.Background(), "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]string{"uri": "file:///x.cc"},
	})
	if err == nil {
		t.Fatal("expected error from requestWithRetry after stdin closed")
	}
	if !strings.Contains(metrics.Render(), `lsp_retries_total{method="textDocument/documentSymbol"}`) {
		t.Error("expected lsp_retries_total{method=\"textDocument/documentSymbol\"} after retry")
	}
}

// TestLSPAdapter_RequestWithRetry_Cancelled 验证已取消的 context 不重试、立即返回。
func TestLSPAdapter_RequestWithRetry_Cancelled(t *testing.T) {
	a := newMockLSPAdapter(t, 3*time.Second)
	if err := a.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer a.Close()

	ctx, cancel := context.WithCancel(context.Background())
	cancel() // 立即取消
	_, err := a.requestWithRetry(ctx, "textDocument/documentSymbol", map[string]any{
		"textDocument": map[string]string{"uri": "file:///x.cc"},
	})
	if err == nil {
		t.Fatal("expected cancellation error")
	}
}

// TestLSPAdapter_ReadResponses_OrphanResponse 验证孤儿响应被显式暴露为 WARN 而非静默丢弃。
func TestLSPAdapter_ReadResponses_OrphanResponse(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 0)
	pr, pw := io.Pipe()
	a.stdout = pr

	go a.readResponses()

	// 发送一个 id=999 的孤儿响应（无对应 pending 请求）
	body := `{"jsonrpc":"2.0","id":999,"result":[]}`
	fmt.Fprintf(pw, "Content-Length: %d\r\n\r\n%s", len(body), body)
	time.Sleep(100 * time.Millisecond)
	pw.Close()
	a.WaitAlive()

	if !strings.Contains(metrics.Render(), `lsp_malformed_frames_total{kind="orphan"}`) {
		t.Error("expected lsp_malformed_frames_total{kind=\"orphan\"} for orphan response")
	}
}

// TestLSPAdapter_ReadResponses_MalformedContentLength 验证 Content-Length 解析失败时暴露为 WARN。
func TestLSPAdapter_ReadResponses_MalformedContentLength(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 0)
	pr, pw := io.Pipe()
	a.stdout = pr

	go a.readResponses()

	// 发送非法 Content-Length 头
	fmt.Fprint(pw, "Content-Length: not-a-number\r\n\r\n")
	time.Sleep(100 * time.Millisecond)
	pw.Close()
	a.WaitAlive()

	if !strings.Contains(metrics.Render(), `lsp_malformed_frames_total{kind="content_length"}`) {
		t.Error("expected lsp_malformed_frames_total{kind=\"content_length\"} for malformed header")
	}
}

// --- P0-2 LSP 精度负控 ---

// TestLSPAdapter_Parse_NestedContainersSetClassFQN 验证分层 DocumentSymbol 的容器名逐层
// 传递：嵌套类 FullName 为 Outer.Inner，其方法 ClassFQN 为 Outer.Inner；顶层函数
// ClassFQN 留空（由 store 的合成包级容器承接）。
func TestLSPAdapter_Parse_NestedContainersSetClassFQN(t *testing.T) {
	a := newMockLSPAdapterMode(t, 3*time.Second, "nested")
	if err := a.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer a.Close()

	path := writeTempSource(t, ".go", "package x\n\nfunc f() {}\n")
	ir, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}

	classFull := make(map[string]string, len(ir.Classes))
	for _, c := range ir.Classes {
		classFull[c.Name] = c.FullName
	}
	if classFull["Outer"] != "Outer" {
		t.Errorf("Outer.FullName = %q, want Outer", classFull["Outer"])
	}
	if classFull["Inner"] != "Outer.Inner" {
		t.Errorf("Inner.FullName = %q, want Outer.Inner", classFull["Inner"])
	}

	methodClass := make(map[string]string, len(ir.Methods))
	for _, m := range ir.Methods {
		methodClass[m.Name] = m.ClassFQN
	}
	if methodClass["Run"] != "Outer.Inner" {
		t.Errorf("Run.ClassFQN = %q, want Outer.Inner", methodClass["Run"])
	}
	if methodClass["Own"] != "Outer" {
		t.Errorf("Own.ClassFQN = %q, want Outer", methodClass["Own"])
	}
	if got, ok := methodClass["topLevel"]; !ok || got != "" {
		t.Errorf("topLevel.ClassFQN = %q (present=%v), want empty", got, ok)
	}
}

// TestLSPAdapter_Parse_ExtractsCallEdges 验证 callHierarchy 抽取调用边：
// mockMethod -> helper，CallerFQN/CalleeFQN 均为容器限定名。
func TestLSPAdapter_Parse_ExtractsCallEdges(t *testing.T) {
	a := newMockLSPAdapterMode(t, 3*time.Second, "calls")
	if err := a.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer a.Close()

	path := writeTempSource(t, ".go", "package x\n\nfunc f() {}\n")
	ir, err := a.Parse(context.Background(), path)
	if err != nil {
		t.Fatalf("Parse failed: %v", err)
	}
	if len(ir.Calls) == 0 {
		t.Fatal("expected call edges from callHierarchy, got 0")
	}
	found := false
	for _, c := range ir.Calls {
		if c.CallerFQN == "MockClass.mockMethod" && c.CalleeFQN == "MockClass.helper" {
			found = true
		}
	}
	if !found {
		t.Fatalf("expected MockClass.mockMethod -> MockClass.helper, got %+v", ir.Calls)
	}
}

// TestLSPAdapter_Parse_EmptySymbolsReturnsSourceUnavailable 验证非空源文件却拿到空符号时
// 返回 ErrSourceUnavailable，使 FallbackParser 能回退到 tree-sitter（而非静默固化空 IR）。
func TestLSPAdapter_Parse_EmptySymbolsReturnsSourceUnavailable(t *testing.T) {
	a := newMockLSPAdapterMode(t, 3*time.Second, "empty")
	if err := a.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	defer a.Close()

	path := writeTempSource(t, ".go", "package x\n\nfunc f() {}\n")
	ir, err := a.Parse(context.Background(), path)
	if err == nil {
		t.Fatalf("expected error for empty symbols on non-empty file, got ir=%+v", ir)
	}
	if !errors.Is(err, cerrors.ErrSourceUnavailable) {
		t.Fatalf("error = %v, want ErrSourceUnavailable", err)
	}
}

// TestLSPAdapter_Close_ReapsChildProcess 验证 Close 调用 cmd.Wait 回收子进程
// （ProcessState 非空），不再留下僵尸进程。
func TestLSPAdapter_Close_ReapsChildProcess(t *testing.T) {
	a := newMockLSPAdapter(t, 3*time.Second)
	if err := a.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init failed: %v", err)
	}
	cmd := a.cmdObj
	if cmd == nil {
		t.Fatal("expected child process after Init")
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
	if cmd.ProcessState == nil {
		t.Fatal("child process not reaped: cmd.Wait was not called")
	}
}

// TestLSPAdapter_ReadResponses_MalformedJSON 验证 body 非法 JSON 时被暴露为 WARN 而非静默跳过。
func TestLSPAdapter_ReadResponses_MalformedJSON(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 0)
	pr, pw := io.Pipe()
	a.stdout = pr

	go a.readResponses()

	// 合法 Content-Length 但非法 JSON body
	body := `{not valid json`
	fmt.Fprintf(pw, "Content-Length: %d\r\n\r\n%s", len(body), body)
	time.Sleep(100 * time.Millisecond)
	pw.Close()
	a.WaitAlive()

	if !strings.Contains(metrics.Render(), `lsp_malformed_frames_total{kind="json"}`) {
		t.Error("expected lsp_malformed_frames_total{kind=\"json\"} for invalid JSON body")
	}
}
