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