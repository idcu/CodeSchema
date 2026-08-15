package server

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"os"
)

// handleRequest 处理一条 JSON-RPC 消息（纯逻辑，HTTP 与 stdio 传输复用）。
func (m *MCPServer) handleRequest(req jsonRPCRequest) jsonRPCResponse {
	id := req.ID
	if id == nil {
		id = 0
	}

	switch req.Method {
	case "tools/list":
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result: map[string][]mcpTool{
				"tools": m.tools,
			},
		}

	case "tools/call":
		return m.handleToolCall(context.Background(), id, req.Params)

	default:
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)},
		}
	}
}

// StartStdio 以 stdio 传输模式启动 MCP Server（T4-1）。
//
// 读取 stdin 的 JSON-RPC 消息（LSP 风格 Content-Length 帧），响应写 stdout，
// 供仅支持 stdio 的 MCP 客户端直接以子进程方式连接：
//   codeschema mcp --stdio
//
// 帧格式：
//   Content-Length: <N>\r\n
//   \r\n
//   <N 字节 JSON>
//
// EOF 时优雅退出（父进程关闭 stdin）。
func (m *MCPServer) StartStdio(ctx context.Context) error {
	log.Printf("MCP Server starting in stdio mode")
	return m.serveStdio(ctx, os.Stdin, os.Stdout)
}

// serveStdio 核心：从 r 读帧、调用 handleRequest、向 w 写帧。分离便于测试。
func (m *MCPServer) serveStdio(ctx context.Context, r io.Reader, w io.Writer) error {
	br := bufio.NewReader(r)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		// 读 Content-Length 头
		var contentLength int
		headerDone := false
		for {
			line, err := br.ReadString('\n')
			if err != nil {
				if err == io.EOF {
					return nil // 父进程关闭 stdin，优雅退出
				}
				return fmt.Errorf("stdio read header: %w", err)
			}
			line = trimCRLF(line)
			if line == "" {
				headerDone = true
				break
			}
			var n int
			if _, err := fmt.Sscanf(line, "Content-Length: %d", &n); err == nil {
				contentLength = n
			}
		}
		if !headerDone {
			continue
		}
		if contentLength <= 0 || contentLength > 16*1024*1024 {
			return fmt.Errorf("stdio invalid Content-Length: %d", contentLength)
		}

		// 读 JSON 体
		body := make([]byte, contentLength)
		if _, err := io.ReadFull(br, body); err != nil {
			return fmt.Errorf("stdio read body: %w", err)
		}

		// 解析并处理
		var req jsonRPCRequest
		if err := json.Unmarshal(body, &req); err != nil {
			resp := jsonRPCResponse{
				JSONRPC: "2.0",
				Error:   &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
			}
			if err := writeStdioFrame(w, resp); err != nil {
				return err
			}
			continue
		}
		resp := m.handleRequest(req)
		if err := writeStdioFrame(w, resp); err != nil {
			return err
		}
	}
}

// writeStdioFrame 以 Content-Length 帧格式写入响应。
func writeStdioFrame(w io.Writer, resp jsonRPCResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("stdio marshal response: %w", err)
	}
	if _, err := fmt.Fprintf(w, "Content-Length: %d\r\n\r\n", len(data)); err != nil {
		return err
	}
	_, err = w.Write(data)
	return err
}

// trimCRLF 去除行尾 \r\n 或 \n。
func trimCRLF(s string) string {
	for len(s) > 0 && (s[len(s)-1] == '\n' || s[len(s)-1] == '\r') {
		s = s[:len(s)-1]
	}
	return s
}
