package server

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// TestWriteStdioFrame 验证 Content-Length 帧格式。
func TestWriteStdioFrame(t *testing.T) {
	var buf bytes.Buffer
	resp := jsonRPCResponse{JSONRPC: "2.0", ID: 1, Result: map[string]string{"ok": "1"}}
	if err := writeStdioFrame(&buf, resp); err != nil {
		t.Fatalf("writeStdioFrame: %v", err)
	}
	out := buf.String()
	// 帧格式：Content-Length: N\r\n\r\n<json>
	if !strings.HasPrefix(out, "Content-Length: ") {
		t.Fatalf("missing Content-Length header: %q", out)
	}
	headerEnd := strings.Index(out, "\r\n\r\n")
	if headerEnd < 0 {
		t.Fatalf("missing header terminator: %q", out)
	}
	body := out[headerEnd+4:]
	var got jsonRPCResponse
	if err := json.Unmarshal([]byte(body), &got); err != nil {
		t.Fatalf("body not valid JSON: %v", err)
	}
	if got.ID != float64(1) {
		t.Fatalf("ID = %v, want 1", got.ID)
	}
}

// TestMCPServer_Stdio_ToolsList 验证 stdio 模式 tools/list 端到端。
func TestMCPServer_Stdio_ToolsList(t *testing.T) {
	// 构造请求帧
	req := jsonRPCRequest{JSONRPC: "2.0", ID: 1, Method: "tools/list"}
	reqData, _ := json.Marshal(req)
	input := "Content-Length: " + itoa(len(reqData)) + "\r\n\r\n" + string(reqData)

	srv := NewMCPServer(nil, "")
	var out bytes.Buffer
	// nil service 时 tools/call 会失败，但 tools/list 不需要 service
	if err := srv.serveStdio(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("serveStdio: %v", err)
	}

	// 解析响应帧
	outStr := out.String()
	headerEnd := strings.Index(outStr, "\r\n\r\n")
	if headerEnd < 0 {
		t.Fatalf("no response frame: %q", outStr)
	}
	body := outStr[headerEnd+4:]
	var resp jsonRPCResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
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

// TestMCPServer_Stdio_InvalidJSON 验证畸形 JSON 返回 parse error 帧。
func TestMCPServer_Stdio_InvalidJSON(t *testing.T) {
	badBody := `{not-json!!}`
	input := "Content-Length: " + itoa(len(badBody)) + "\r\n\r\n" + badBody
	srv := NewMCPServer(nil, "")
	var out bytes.Buffer
	if err := srv.serveStdio(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("serveStdio: %v", err)
	}
	outStr := out.String()
	headerEnd := strings.Index(outStr, "\r\n\r\n")
	if headerEnd < 0 {
		t.Fatalf("no response frame: %q", outStr)
	}
	body := outStr[headerEnd+4:]
	var resp jsonRPCResponse
	if err := json.Unmarshal([]byte(body), &resp); err != nil {
		t.Fatalf("response not valid JSON: %v", err)
	}
	if resp.Error == nil || resp.Error.Code != -32700 {
		t.Fatalf("expected parse error (-32700), got: %+v", resp.Error)
	}
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}
