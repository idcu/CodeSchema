package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
)

func newTestMCPServer(t *testing.T) *MCPServer {
	t.Helper()
	dir := t.TempDir()
	st := store.NewStore("file")
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	seedSymbol(t, st)
	svc := service.NewService(st)
	return NewMCPServer(svc, ":0")
}

func TestMCP_ToolsList(t *testing.T) {
	srv := newTestMCPServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`

	req := httptest.NewRequest(http.MethodPost, "/message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleMessage(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Result().StatusCode)
	}

	var resp jsonRPCResponse
	json.NewDecoder(w.Result().Body).Decode(&resp)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("expected map result, got %T", resp.Result)
	}

	tools, ok := result["tools"].([]any)
	if !ok {
		t.Fatalf("expected tools array, got %T", result["tools"])
	}

	if len(tools) != 12 {
		t.Errorf("expected 12 tools, got %d", len(tools))
	}
}

func TestMCP_ToolCall_Context(t *testing.T) {
	srv := newTestMCPServer(t)
	body := `{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"context","arguments":{"symbol":"com.example.MyClass"}}}`

	req := httptest.NewRequest(http.MethodPost, "/message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleMessage(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Result().StatusCode)
	}

	var resp jsonRPCResponse
	json.NewDecoder(w.Result().Body).Decode(&resp)

	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestMCP_ToolCall_MissingSymbol(t *testing.T) {
	srv := newTestMCPServer(t)
	body := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"context","arguments":{"symbol":""}}}`

	req := httptest.NewRequest(http.MethodPost, "/message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleMessage(w, req)

	var resp jsonRPCResponse
	json.NewDecoder(w.Result().Body).Decode(&resp)

	if resp.Error == nil {
		t.Fatal("expected error for empty symbol")
	}
}

func TestMCP_ToolCall_InvalidMethod(t *testing.T) {
	srv := newTestMCPServer(t)
	body := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nonexistent","arguments":{}}}`

	req := httptest.NewRequest(http.MethodPost, "/message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleMessage(w, req)

	var resp jsonRPCResponse
	json.NewDecoder(w.Result().Body).Decode(&resp)

	if resp.Error == nil {
		t.Fatal("expected error for nonexistent tool")
	}
}

func TestMCP_UnknownMethod(t *testing.T) {
	srv := newTestMCPServer(t)
	body := `{"jsonrpc":"2.0","id":5,"method":"unknown"}`

	req := httptest.NewRequest(http.MethodPost, "/message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleMessage(w, req)

	var resp jsonRPCResponse
	json.NewDecoder(w.Result().Body).Decode(&resp)

	if resp.Error == nil {
		t.Fatal("expected error for unknown method")
	}
	if resp.Error.Code != -32601 {
		t.Errorf("expected code -32601, got %d", resp.Error.Code)
	}
}

func TestMCP_InvalidJSON(t *testing.T) {
	srv := newTestMCPServer(t)
	body := `invalid json`

	req := httptest.NewRequest(http.MethodPost, "/message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()

	srv.handleMessage(w, req)

	var resp jsonRPCResponse
	json.NewDecoder(w.Result().Body).Decode(&resp)

	if resp.Error == nil {
		t.Fatal("expected error for invalid JSON")
	}
	if resp.Error.Code != -32700 {
		t.Errorf("expected code -32700, got %d", resp.Error.Code)
	}
}

// TestMCP_ToolCall_ContextBatch symbols 数组传多个时走批量路径（B5）。
// 单符号失败不中断整体：results 只含存在的符号，errors 带 code + hint。
func TestMCP_ToolCall_ContextBatch(t *testing.T) {
	srv := newTestMCPServer(t)
	body := `{"jsonrpc":"2.0","id":9,"method":"tools/call","params":{"name":"context","arguments":{"symbols":["com.example.MyClass","com.example.Missing"]}}}`
	req := httptest.NewRequest(http.MethodPost, "/message", strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleMessage(w, req)

	var resp jsonRPCResponse
	if err := json.NewDecoder(w.Result().Body).Decode(&resp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}

	// mcpResult 把结果序列化进 content[0].text，batch 结构在其中。
	content, ok := resp.Result.(map[string]any)["content"].([]any)
	if !ok || len(content) == 0 {
		t.Fatal("缺少 content")
	}
	text, ok := content[0].(map[string]any)["text"].(string)
	if !ok {
		t.Fatal("缺少 text")
	}
	var batch struct {
		Results []map[string]any `json:"results"`
		Errors  []struct {
			Symbol string `json:"symbol"`
			Code   string `json:"code"`
			Hint   string `json:"hint"`
		} `json:"errors"`
	}
	if err := json.Unmarshal([]byte(text), &batch); err != nil {
		t.Fatalf("parse batch result: %v", err)
	}
	if len(batch.Results) != 1 {
		t.Fatalf("results=%d, want 1（仅存在的符号）", len(batch.Results))
	}
	if batch.Results[0]["symbol"] != "com.example.MyClass" {
		t.Fatalf("results[0].symbol=%v", batch.Results[0]["symbol"])
	}
	if len(batch.Errors) != 1 || batch.Errors[0].Code != "ERR_SYMBOL_NOT_FOUND" {
		t.Fatalf("errors=%+v, want 1 条 ERR_SYMBOL_NOT_FOUND", batch.Errors)
	}
	if batch.Errors[0].Hint == "" {
		t.Fatal("批量失败明细应带 hint")
	}
}

func TestMCP_ToolCall_AllTools(t *testing.T) {
	srv := newTestMCPServer(t)

	tools := []struct {
		name string
		args string
	}{
		{"context", `{"symbol":"com.example.MyClass"}`},
		{"impact", `{"method":"com.example.MyClass.myMethod"}`},
		{"tests", `{"method":"com.example.MyClass.myMethod"}`},
		{"affected", `{"symbol":"com.example.MyClass"}`},
		{"get_call_graph", `{"symbol":"com.example.MyClass"}`},
		{"search_config", `{"pattern":"spring.*"}`},
		{"find_dependencies", `{"symbol":"com.example.MyClass"}`},
		{"search_symbols", `{"q":"MyClass"}`},
	}

	for _, tool := range tools {
		t.Run(tool.name, func(t *testing.T) {
			body := `{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"` + tool.name + `","arguments":` + tool.args + `}}`
			req := httptest.NewRequest(http.MethodPost, "/message", strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			srv.handleMessage(w, req)

			var resp jsonRPCResponse
			json.NewDecoder(w.Result().Body).Decode(&resp)

			if resp.Error != nil {
				t.Errorf("tool %s returned error: %+v", tool.name, resp.Error)
			}
		})
	}
}

func TestMCPSSE_Endpoint(t *testing.T) {
	srv := newTestMCPServer(t)
	// 使用可取消的上下文，避免 handleSSE 无限阻塞
	ctx, cancel := context.WithCancel(context.Background())
	req := httptest.NewRequest(http.MethodGet, "/sse", nil).WithContext(ctx)
	w := httptest.NewRecorder()

	done := make(chan struct{})
	go func() {
		srv.handleSSE(w, req)
		close(done)
	}()

	// 取消上下文，触发 handler 退出
	cancel()
	<-done

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Result().StatusCode)
	}

	contentType := w.Header().Get("Content-Type")
	if contentType != "text/event-stream" {
		t.Errorf("expected text/event-stream, got %s", contentType)
	}
}

func TestMCP_MessageMethodNotAllowed(t *testing.T) {
	srv := newTestMCPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/message", nil)
	w := httptest.NewRecorder()
	srv.handleMessage(w, req)

	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Result().StatusCode)
	}
}

// TestMCP_UpdateAuthToken_HotUpdate 验证认证令牌热更新（无需重启进程）。
func TestMCP_UpdateAuthToken_HotUpdate(t *testing.T) {
	srv := newTestMCPServer(t) // 初始无认证

	do := func(token string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodPost, "/message", strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"tools/list"}`))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)
		return w.Result().StatusCode
	}

	// 初始：无认证放行。
	if code := do(""); code != http.StatusOK {
		t.Fatalf("initial: expected 200, got %d", code)
	}
	// 热更新启用认证：无 token 401，正确 token 200。
	srv.SetAuthToken("mcp-secret")
	if code := do(""); code != http.StatusUnauthorized {
		t.Fatalf("no token after enable: expected 401, got %d", code)
	}
	if code := do("mcp-secret"); code != http.StatusOK {
		t.Fatalf("valid token: expected 200, got %d", code)
	}
	// 热更新轮换令牌：旧令牌失效，新令牌生效。
	srv.SetAuthToken("mcp-secret-2")
	if code := do("mcp-secret"); code != http.StatusUnauthorized {
		t.Fatalf("old token after rotate: expected 401, got %d", code)
	}
	if code := do("mcp-secret-2"); code != http.StatusOK {
		t.Fatalf("new token: expected 200, got %d", code)
	}
	// 热更新关闭认证（空串）：无 token 放行。
	srv.SetAuthToken("")
	if code := do(""); code != http.StatusOK {
		t.Fatalf("no token after disable: expected 200, got %d", code)
	}
}