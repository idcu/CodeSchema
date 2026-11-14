package server

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// batchItems 构造 n 个互不相同的符号名。
func batchItems(prefix string, n int) []string {
	out := make([]string, n)
	for i := range out {
		out[i] = fmt.Sprintf("%s%d", prefix, i)
	}
	return out
}

// TestHTTPServer_BatchLimit 负控（P2-3）：批量接口超过条目上限必须返回 400，
// 而不是照单逐项串行查询存储。
func TestHTTPServer_BatchLimit(t *testing.T) {
	srv := newTestHTTPServer(t)
	const over = 257
	cases := []struct {
		name    string
		target  string
		handler http.HandlerFunc
	}{
		{"context", "/context", srv.handleContext},
		{"impact", "/impact", srv.handleImpact},
		{"tests", "/tests", srv.handleTests},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			param := "symbols"
			if tc.name != "context" {
				param = "methods"
			}
			req := httptest.NewRequest(http.MethodGet,
				tc.target+"?"+param+"="+strings.Join(batchItems("sym", over), ","), nil)
			w := httptest.NewRecorder()
			tc.handler(w, req)

			if w.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400（batch size %d）", w.Code, over)
			}
			var body struct {
				Error struct {
					Code string `json:"code"`
				} `json:"error"`
			}
			if err := json.NewDecoder(w.Body).Decode(&body); err != nil {
				t.Fatalf("decode body: %v", err)
			}
			if body.Error.Code != "ERR_INVALID_PARAMETER" {
				t.Fatalf("error code = %q, want ERR_INVALID_PARAMETER", body.Error.Code)
			}
		})
	}
}

// TestMCP_BatchLimit 负控（P2-3）：MCP 批量参数同样受条目上限约束。
func TestMCP_BatchLimit(t *testing.T) {
	srv := newTestMCPServer(t)
	const over = 257
	symbols := make([]any, over)
	for i := range symbols {
		symbols[i] = fmt.Sprintf("sym%d", i)
	}
	params, _ := json.Marshal(map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "context",
			"arguments": map[string]any{"symbols": symbols},
		},
	})
	_, resp := mcpExchange(t, srv, string(params))
	if resp.Error == nil {
		t.Fatalf("超限请求未被拒绝：%+v", resp.Result)
	}
	if resp.Error.Code != -32602 {
		t.Fatalf("error code = %d, want -32602（invalid params）", resp.Error.Code)
	}
}
