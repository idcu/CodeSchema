package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestWriteStdioLine 验证 MCP stdio 的单行 JSON 帧格式（非 LSP Content-Length）。
func TestWriteStdioLine(t *testing.T) {
	var buf bytes.Buffer
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: 1, Result: map[string]string{"ok": "1"}}
	if err := writeStdioLine(&buf, resp); err != nil {
		t.Fatalf("writeStdioLine: %v", err)
	}
	out := buf.String()
	if strings.Contains(out, "Content-Length") {
		t.Fatalf("must not use LSP framing: %q", out)
	}
	if !strings.HasSuffix(out, "\n") || strings.Count(out, "\n") != 1 {
		t.Fatalf("response must be exactly one JSON line: %q", out)
	}
	var got jsonRPCResponse
	if err := json.Unmarshal([]byte(strings.TrimSpace(out)), &got); err != nil {
		t.Fatalf("line not valid JSON: %v", err)
	}
	if got.ID != float64(1) {
		t.Fatalf("ID = %v, want 1", got.ID)
	}
}

// TestMCPServer_Stdio_ToolsList 验证 stdio 模式 tools/list 端到端（逐行 JSON）。
func TestMCPServer_Stdio_ToolsList(t *testing.T) {
	req := jsonRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"}
	reqData, _ := json.Marshal(req)
	input := string(reqData) + "\n"

	srv := NewMCPServer(nil, "")
	var out bytes.Buffer
	// nil service 时 tools/call 会失败，但 tools/list 不需要 service
	if err := srv.serveStdio(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("serveStdio: %v", err)
	}

	outStr := strings.TrimSpace(out.String())
	var resp jsonRPCResponse
	if err := json.Unmarshal([]byte(outStr), &resp); err != nil {
		t.Fatalf("response not valid JSON: %v (%q)", err, outStr)
	}
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}
	result, ok := resp.Result.(map[string]any)
	if !ok {
		t.Fatalf("result type = %T", resp.Result)
	}
	tools, ok := result["tools"].([]any)
	if !ok || len(tools) < 10 {
		t.Fatalf("expected >=10 tools, got %d (%v)", len(tools), tools)
	}
}

// TestMCPServer_Stdio_EOF 验证 stdin 关闭时优雅退出（无错误）。
func TestMCPServer_Stdio_EOF(t *testing.T) {
	srv := NewMCPServer(nil, "")
	if err := srv.serveStdio(context.Background(), strings.NewReader(""), &bytes.Buffer{}); err != nil {
		t.Fatalf("EOF should exit cleanly, got: %v", err)
	}
}

// TestMCPServer_Stdio_InvalidJSON 验证畸形 JSON 返回 id=null 的 parse error。
func TestMCPServer_Stdio_InvalidJSON(t *testing.T) {
	srv := NewMCPServer(nil, "")
	var out bytes.Buffer
	if err := srv.serveStdio(context.Background(), strings.NewReader("{not-json!!}\n"), &out); err != nil {
		t.Fatalf("serveStdio: %v", err)
	}
	outStr := strings.TrimSpace(out.String())
	var resp jsonRPCResponse
	if err := json.Unmarshal([]byte(outStr), &resp); err != nil {
		t.Fatalf("response not valid JSON: %v (%q)", err, outStr)
	}
	if resp.Error == nil || resp.Error.Code != -32700 {
		t.Fatalf("expected parse error (-32700), got: %+v", resp.Error)
	}
	if resp.ID != nil {
		t.Fatalf("parse error id must be null, got %v", resp.ID)
	}
}

// TestMCP_Stdio_LineDelimitedJSON 负控（P2-20）：MCP stdio 传输是「每行一个
// JSON-RPC 消息」，不是 LSP 的 Content-Length 帧。请求后的 notification 不得回包。
func TestMCP_Stdio_LineDelimitedJSON(t *testing.T) {
	srv := NewMCPServer(nil, "")
	input := `{"jsonrpc":"2.0","id":1,"method":"tools/list"}` + "\n" +
		`{"jsonrpc":"2.0","method":"notifications/initialized"}` + "\n"
	var out bytes.Buffer
	if err := srv.serveStdio(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("serveStdio: %v", err)
	}
	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 1 {
		t.Fatalf("expected exactly 1 response line (notification must not reply), got %d: %q", len(lines), out.String())
	}
	if strings.Contains(lines[0], "Content-Length") {
		t.Fatalf("stdio must be line-delimited JSON, got LSP frame: %q", lines[0])
	}
	var resp jsonRPCResponse
	if err := json.Unmarshal([]byte(lines[0]), &resp); err != nil {
		t.Fatalf("response line is not JSON: %v (%q)", err, lines[0])
	}
	if resp.Error != nil {
		t.Fatalf("tools/list error: %+v", resp.Error)
	}
}

// TestMCP_Stdio_ParseErrorHasNullID 负控（P2-20）：解析失败时 id 必须是 null，
// 不能伪造成 0（0 是合法请求 ID）。
func TestMCP_Stdio_ParseErrorHasNullID(t *testing.T) {
	srv := NewMCPServer(nil, "")
	var out bytes.Buffer
	if err := srv.serveStdio(context.Background(), strings.NewReader("{bad}\n"), &out); err != nil {
		t.Fatalf("serveStdio: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(out.String())), &raw); err != nil {
		t.Fatalf("parse response: %v (%q)", err, out.String())
	}
	if v, ok := raw["id"]; !ok || v != nil {
		t.Fatalf("parse error id must be null, got %v", raw["id"])
	}
}
