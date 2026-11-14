package server

import (
	"bufio"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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

// mcpExchange 模拟 MCP SSE 客户端：登记会话 → POST（期望 202）→ 从会话队列
// 取 SSE 推送的响应。解析失败等同步 400 场景直接读响应体。
func mcpExchange(t *testing.T, srv *MCPServer, body string) (int, *jsonRPCResponse) {
	t.Helper()
	sess := srv.newMCPSession()
	defer srv.removeSession(sess.id)
	req := httptest.NewRequest(http.MethodPost, "/message?sessionId="+sess.id, strings.NewReader(body))
	req.Header.Set("Content-Type", "application/json")
	w := httptest.NewRecorder()
	srv.handleMessage(w, req)
	if w.Body.Len() > 0 {
		var resp jsonRPCResponse
		if err := json.NewDecoder(w.Body).Decode(&resp); err != nil {
			t.Fatalf("decode inline response: %v", err)
		}
		return w.Result().StatusCode, &resp
	}
	select {
	case data := <-sess.ch:
		var resp jsonRPCResponse
		if err := json.Unmarshal(data, &resp); err != nil {
			t.Fatalf("decode SSE response: %v", err)
		}
		return w.Result().StatusCode, &resp
	case <-time.After(2 * time.Second):
		t.Fatal("timeout waiting for SSE response")
		return 0, nil
	}
}

func TestMCP_ToolsList(t *testing.T) {
	srv := newTestMCPServer(t)
	body := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}`
	code, resp := mcpExchange(t, srv, body)
	if code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", code)
	}
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
	code, resp := mcpExchange(t, srv, body)
	if code != http.StatusAccepted {
		t.Errorf("expected 202, got %d", code)
	}
	if resp.Error != nil {
		t.Fatalf("unexpected error: %+v", resp.Error)
	}
}

func TestMCP_ToolCall_MissingSymbol(t *testing.T) {
	srv := newTestMCPServer(t)
	body := `{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"context","arguments":{"symbol":""}}}`
	_, resp := mcpExchange(t, srv, body)
	if resp.Error == nil {
		t.Fatal("expected error for empty symbol")
	}
}

func TestMCP_ToolCall_InvalidMethod(t *testing.T) {
	srv := newTestMCPServer(t)
	body := `{"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"nonexistent","arguments":{}}}`
	_, resp := mcpExchange(t, srv, body)
	if resp.Error == nil {
		t.Fatal("expected error for nonexistent tool")
	}
}

func TestMCP_UnknownMethod(t *testing.T) {
	srv := newTestMCPServer(t)
	body := `{"jsonrpc":"2.0","id":5,"method":"unknown"}`
	_, resp := mcpExchange(t, srv, body)
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
	code, resp := mcpExchange(t, srv, body)
	if code != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", code)
	}
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
	_, resp := mcpExchange(t, srv, body)
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
			_, resp := mcpExchange(t, srv, body)
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
	if body := w.Body.String(); !strings.Contains(body, "event: endpoint") || !strings.Contains(body, "sessionId=") {
		t.Errorf("endpoint event must carry sessionId, got %q", body)
	}
}

// TestMCP_SSECompliance 负控（P2-20）：MCP 2024-11-05 SSE 传输要求
//  1. GET /sse 先下发 `event: endpoint`，data 为带 sessionId 的 /message 地址；
//  2. POST /message?sessionId=... 返回 202（不回包）；
//  3. JSON-RPC 响应经 SSE 的 `event: message` 推送。
func TestMCP_SSECompliance(t *testing.T) {
	srv := NewMCPServer(nil, "")
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", srv.handleSSE)
	mux.HandleFunc("/message", srv.handleMessage)
	ts := httptest.NewServer(mux)
	defer ts.Close()

	resp, err := http.Get(ts.URL + "/sse")
	if err != nil {
		t.Fatalf("GET /sse: %v", err)
	}
	defer resp.Body.Close()
	if ct := resp.Header.Get("Content-Type"); ct != "text/event-stream" {
		t.Fatalf("Content-Type = %q, want text/event-stream", ct)
	}
	reader := bufio.NewReader(resp.Body)
	endpoint, err := readSSEData(reader, "endpoint")
	if err != nil {
		t.Fatalf("read endpoint event: %v", err)
	}
	if !strings.Contains(endpoint, "sessionId=") {
		t.Fatalf("endpoint event must carry sessionId, got %q", endpoint)
	}

	body := `{"jsonrpc":"2.0","id":7,"method":"tools/list"}`
	postResp, err := http.Post(ts.URL+endpoint, "application/json", strings.NewReader(body))
	if err != nil {
		t.Fatalf("POST message: %v", err)
	}
	defer postResp.Body.Close()
	if postResp.StatusCode != http.StatusAccepted {
		t.Fatalf("POST /message status = %d, want 202", postResp.StatusCode)
	}

	data, err := readSSEData(reader, "message")
	if err != nil {
		t.Fatalf("read message event: %v", err)
	}
	var rpcResp jsonRPCResponse
	if err := json.Unmarshal([]byte(data), &rpcResp); err != nil {
		t.Fatalf("SSE data is not JSON-RPC: %v (%q)", err, data)
	}
	if rpcResp.Error != nil {
		t.Fatalf("tools/list error: %+v", rpcResp.Error)
	}
	if rpcResp.ID != float64(7) {
		t.Fatalf("response id = %v, want 7", rpcResp.ID)
	}
}

// readSSEData 读取首个指定事件的 data 行（忽略注释与其他事件）。
func readSSEData(r *bufio.Reader, wantEvent string) (string, error) {
	current := ""
	for {
		line, err := r.ReadString('\n')
		if err != nil {
			return "", err
		}
		line = strings.TrimRight(line, "\r\n")
		if strings.HasPrefix(line, "event:") {
			current = strings.TrimSpace(strings.TrimPrefix(line, "event:"))
			continue
		}
		if strings.HasPrefix(line, "data:") && current == wantEvent {
			return strings.TrimSpace(strings.TrimPrefix(line, "data:")), nil
		}
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
