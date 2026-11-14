package server

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"os"
)

// maxStdioLine stdio 单条消息上限（16MB），防止异常输入无限占用内存。
const maxStdioLine = 16 << 20

// supportedProtocolVersions 本服务真实支持的 MCP 协议版本（按优先级）。
//
// 当前仅实现 2024-11-05 SSE/stdio 语义；2025-03-26（Streamable HTTP）等新版本
// 的行为差异尚未实现，不能对客户端声称支持（P2-7）。
var supportedProtocolVersions = []string{"2024-11-05"}

// negotiateProtocolVersion 返回服务端支持的最高版本；客户端版本不支持时回退
// 到本服务默认版本，由客户端决定是否继续（MCP 握手约定）。
func negotiateProtocolVersion(requested string) string {
	for _, v := range supportedProtocolVersions {
		if requested == v {
			return v
		}
	}
	return supportedProtocolVersions[0]
}

// handleRequest 处理一条 JSON-RPC 消息（纯逻辑，HTTP 与 stdio 传输复用）。
// 通知（ID 为 nil）由传输层决定不回包；ctx 用于工具调用的取消/超时传递。
func (m *MCPServer) handleRequest(ctx context.Context, req jsonRPCRequest) jsonRPCResponse {
	switch req.Method {
	case "initialize":
		// MCP 初始化握手：标准 MCP 客户端（VS Code / JetBrains / SDK）连上来
		// 第一步必发 initialize 并期待 protocolVersion + capabilities 返回，否则直接断开。
		// 原实现缺此分支 → 所有真实客户端握手失败（端口虽通但协议不干活）。
		protoVer := supportedProtocolVersions[0]
		if p, ok := req.Params.(map[string]any); ok {
			if v, ok := p["protocolVersion"].(string); ok && v != "" {
				protoVer = negotiateProtocolVersion(v)
			}
		}
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string]any{
				"protocolVersion": protoVer,
				"capabilities":    map[string]any{"tools": map[string]any{}},
				"serverInfo":      map[string]any{"name": "codeschema", "version": "dev"},
			},
		}

	case "notifications/initialized":
		// 通知类消息：ID 为 nil 时传输层不写响应；带 id 的非法用法回空 result。
		return jsonRPCResponse{JSONRPC: "2.0", ID: req.ID, Result: map[string]any{}}

	case "tools/list":
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Result: map[string][]mcpTool{
				"tools": m.tools,
			},
		}

	case "tools/call":
		return m.handleToolCall(ctx, req.ID, req.Params)

	default:
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      req.ID,
			Error:   &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)},
		}
	}
}

// StartStdio 以 stdio 传输模式启动 MCP Server（T4-1）。
//
// MCP stdio 传输为「每行一条 JSON-RPC 消息」（UTF-8，消息内不得含裸换行），
// 不是 LSP 的 Content-Length 帧。供仅支持 stdio 的 MCP 客户端以子进程方式接入：
//
//	codeschema mcp --stdio
//
// 通知（无 id）不回包；解析失败回 id=null 的 parse error；EOF 时优雅退出。
func (m *MCPServer) StartStdio(ctx context.Context) error {
	log.Printf("MCP Server starting in stdio mode")
	return m.serveStdio(ctx, os.Stdin, os.Stdout)
}

// serveStdio 核心：逐行读消息、调用 handleRequest、逐行写响应。分离便于测试。
func (m *MCPServer) serveStdio(ctx context.Context, r io.Reader, w io.Writer) error {
	br := bufio.NewReaderSize(r, 64*1024)
	for {
		select {
		case <-ctx.Done():
			return nil
		default:
		}

		line, err := readStdioLine(br)
		if err != nil {
			if errors.Is(err, io.EOF) {
				return nil // 父进程关闭 stdin，优雅退出
			}
			return err
		}
		line = bytes.TrimSpace(line)
		if len(line) == 0 {
			continue
		}

		var req jsonRPCRequest
		if err := json.Unmarshal(line, &req); err != nil {
			resp := jsonRPCResponse{
				JSONRPC: "2.0", // id 保持 nil → 序列化为 null
				Error:   &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
			}
			if err := writeStdioLine(w, resp); err != nil {
				return err
			}
			continue
		}
		if req.ID == nil {
			// JSON-RPC 通知：处理（当前均为无副作用）但不允许回包。
			_ = m.handleRequest(ctx, req)
			continue
		}
		if err := writeStdioLine(w, m.handleRequest(ctx, req)); err != nil {
			return err
		}
	}
}

// readStdioLine 读取一行（含换行），EOF 时返回已读到的残余行。
func readStdioLine(br *bufio.Reader) ([]byte, error) {
	var buf []byte
	for {
		frag, err := br.ReadSlice('\n')
		buf = append(buf, frag...)
		if len(buf) > maxStdioLine {
			return nil, fmt.Errorf("stdio message exceeds %d bytes", maxStdioLine)
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		if err != nil {
			if errors.Is(err, io.EOF) && len(buf) > 0 {
				return buf, nil
			}
			return nil, err
		}
		return buf, nil
	}
}

// writeStdioLine 以单行 JSON 写入响应（MCP stdio 传输格式）。
func writeStdioLine(w io.Writer, resp jsonRPCResponse) error {
	data, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("stdio marshal response: %w", err)
	}
	data = append(data, '\n')
	_, err = w.Write(data)
	return err
}
