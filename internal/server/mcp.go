package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"time"

	"codeschema/internal/robust"
	"codeschema/internal/service"
)

// MCP 工具定义
type mcpTool struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	InputSchema any    `json:"inputSchema"`
}

// MCP 工具参数 Schema
type toolParams struct {
	Type       string                  `json:"type"`
	Properties map[string]toolProperty `json:"properties"`
	Required   []string                `json:"required,omitempty"`
}

type toolProperty struct {
	Type        string `json:"type"`
	Description string `json:"description"`
	Default     any    `json:"default,omitempty"`
}

// JSON-RPC 消息
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type jsonRPCResponse struct {
	JSONRPC string `json:"jsonrpc"`
	ID      any    `json:"id"`
	Result  any    `json:"result,omitempty"`
	Error   *rpcError `json:"error,omitempty"`
}

type rpcError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// MCPServer MCP 协议服务器，使用 SSE（Server-Sent Events）传输。
type MCPServer struct {
	service   *service.Service
	addr      string
	server    *http.Server
	tools     []mcpTool
	authToken string
}

// NewMCPServer 创建 MCP Server 实例。
func NewMCPServer(svc *service.Service, addr string) *MCPServer {
	return &MCPServer{
		service: svc,
		addr:    addr,
		tools:   defineTools(),
	}
}

// SetAuthToken 设置 Bearer token 认证。
func (m *MCPServer) SetAuthToken(token string) {
	m.authToken = token
}

// defineTools 定义 MCP 工具列表。
func defineTools() []mcpTool {
	return []mcpTool{
		{
			Name:        "context",
			Description: "获取指定符号的精准裁剪上下文（方法体 + 字段 + 接口 + 单测）",
			InputSchema: toolParams{
				Type: "object",
				Properties: map[string]toolProperty{
					"symbol":        {Type: "string", Description: "类/方法全限定名"},
					"context_lines": {Type: "number", Description: "上下文行数", Default: 5},
				},
				Required: []string{"symbol"},
			},
		},
		{
			Name:        "impact",
			Description: "分析指定方法的调用影响面（上游调用方 + 下游被调）",
			InputSchema: toolParams{
				Type: "object",
				Properties: map[string]toolProperty{
					"method": {Type: "string", Description: "方法全限定名"},
					"depth":  {Type: "number", Description: "分析深度", Default: 1},
				},
				Required: []string{"method"},
			},
		},
		{
			Name:        "tests",
			Description: "查询指定方法的关联单测",
			InputSchema: toolParams{
				Type: "object",
				Properties: map[string]toolProperty{
					"method":         {Type: "string", Description: "方法全限定名"},
					"min_confidence": {Type: "number", Description: "最小置信度", Default: 60},
				},
				Required: []string{"method"},
			},
		},
		{
			Name:        "affected",
			Description: "递归查找受指定符号影响的测试",
			InputSchema: toolParams{
				Type: "object",
				Properties: map[string]toolProperty{
					"symbol":    {Type: "string", Description: "符号全限定名"},
					"recursive": {Type: "boolean", Description: "是否递归", Default: false},
				},
				Required: []string{"symbol"},
			},
		},
		{
			Name:        "get_call_graph",
			Description: "获取指定符号的调用图（双向），返回节点和边列表",
			InputSchema: toolParams{
				Type: "object",
				Properties: map[string]toolProperty{
					"symbol": {Type: "string", Description: "符号全限定名"},
					"depth":  {Type: "number", Description: "调用深度", Default: 1},
				},
				Required: []string{"symbol"},
			},
		},
		{
			Name:        "search_config",
			Description: "搜索配置项和注解",
			InputSchema: toolParams{
				Type: "object",
				Properties: map[string]toolProperty{
					"pattern": {Type: "string", Description: "搜索模式"},
				},
				Required: []string{"pattern"},
			},
		},
		{
			Name:        "find_dependencies",
			Description: "查找指定符号的依赖关系列表",
			InputSchema: toolParams{
				Type: "object",
				Properties: map[string]toolProperty{
					"symbol": {Type: "string", Description: "符号全限定名"},
				},
				Required: []string{"symbol"},
			},
		},
		{
			Name:        "search_symbols",
			Description: "搜索符号（精确匹配 + 语义检索）",
			InputSchema: toolParams{
				Type: "object",
				Properties: map[string]toolProperty{
					"q":     {Type: "string", Description: "搜索关键词"},
					"mode":  {Type: "string", Description: "搜索模式: exact/semantic/both", Default: "both"},
					"limit": {Type: "number", Description: "返回结果数量限制", Default: 20},
				},
				Required: []string{"q"},
			},
		},
		{
			Name:        "get_tags",
			Description: "获取指定符号（类/方法）的标签列表",
			InputSchema: toolParams{
				Type: "object",
				Properties: map[string]toolProperty{
					"symbol": {Type: "string", Description: "类/方法全限定名"},
				},
				Required: []string{"symbol"},
			},
		},
		{
			Name:        "search_by_tag",
			Description: "按标签搜索类和方法的 ID 和名称",
			InputSchema: toolParams{
				Type: "object",
				Properties: map[string]toolProperty{
					"tag": {Type: "string", Description: "标签名，如 controller/service/cache/go"},
				},
				Required: []string{"tag"},
			},
		},
		{
			Name:        "get_all_tags",
			Description: "获取所有已知标签及其分类统计",
			InputSchema: toolParams{
				Type: "object",
				Properties: map[string]toolProperty{},
			},
		},
	}
}

// Start 启动 MCP Server，使用 SSE 传输协议。
func (m *MCPServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()
	mux.HandleFunc("/sse", m.handleSSE)
	mux.HandleFunc("/message", m.handleMessage)

	// 中间件链：认证 → CORS → Panic 恢复
	handler := m.authMiddleware(
		recoveryMiddleware(
			corsMiddleware(mux),
		),
	)

	m.server = &http.Server{
		Addr:         m.addr,
		Handler:      handler,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 0, // SSE 需要长连接
		IdleTimeout:  30 * time.Second,
	}

	log.Printf("MCP Server starting on %s", m.addr)

	errCh := make(chan error, 1)
	go func() {
		if err := m.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		return m.Stop()
	case err := <-errCh:
		return err
	}
}

// Stop 优雅关闭 MCP Server。
func (m *MCPServer) Stop() error {
	if m.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return m.server.Shutdown(ctx)
}

// handleSSE SSE 端点，客户端通过此端点建立连接并接收事件。
func (m *MCPServer) handleSSE(w http.ResponseWriter, r *http.Request) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("Access-Control-Allow-Origin", "*")

	// 发送 endpoint 事件告知客户端消息端点
	fmt.Fprintf(w, "event: endpoint\ndata: /message\n\n")
	flusher.Flush()

	// 保持连接直到客户端断开或 context 取消
	<-r.Context().Done()
}

// handleMessage 处理 JSON-RPC 消息。
func (m *MCPServer) handleMessage(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeJSON(w, http.StatusMethodNotAllowed, jsonRPCResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32600, Message: "method not allowed"},
		})
		return
	}

	var req jsonRPCRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		writeJSON(w, http.StatusBadRequest, jsonRPCResponse{
			JSONRPC: "2.0",
			Error:   &rpcError{Code: -32700, Message: "parse error: " + err.Error()},
		})
		return
	}

	// 处理请求 ID
	id := req.ID
	if id == nil {
		id = 0
	}

	var resp jsonRPCResponse
	switch req.Method {
	case "tools/list":
		resp = jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Result: map[string][]mcpTool{
				"tools": m.tools,
			},
		}

	case "tools/call":
		resp = m.handleToolCall(r.Context(), id, req.Params)

	default:
		resp = jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32601, Message: fmt.Sprintf("method not found: %s", req.Method)},
		}
	}

	writeJSON(w, http.StatusOK, resp)
}

// handleToolCall 处理工具调用请求。
func (m *MCPServer) handleToolCall(ctx context.Context, id any, params any) jsonRPCResponse {
	paramsMap, ok := params.(map[string]any)
	if !ok {
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32602, Message: "invalid params"},
		}
	}

	name, _ := paramsMap["name"].(string)
	args, _ := paramsMap["arguments"].(map[string]any)

	switch name {
	case "context":
		symbol, _ := args["symbol"].(string)
		contextLines, _ := toInt(args["context_lines"])
		result, err := m.service.GetContext(ctx, symbol, contextLines)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "impact":
		method, _ := args["method"].(string)
		depth, _ := toInt(args["depth"])
		result, err := m.service.GetImpact(ctx, method, depth)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "tests":
		method, _ := args["method"].(string)
		minConfidence, _ := toInt(args["min_confidence"])
		result, err := m.service.GetTests(ctx, method, minConfidence)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "affected":
		symbol, _ := args["symbol"].(string)
		recursive, _ := args["recursive"].(bool)
		result, err := m.service.GetAffected(ctx, symbol, recursive)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "get_call_graph":
		symbol, _ := args["symbol"].(string)
		depth, _ := toInt(args["depth"])
		result, err := m.service.GetCallGraph(ctx, symbol, depth)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "search_config":
		pattern, _ := args["pattern"].(string)
		result, err := m.service.SearchConfig(ctx, pattern)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "find_dependencies":
		symbol, _ := args["symbol"].(string)
		result, err := m.service.FindDependencies(ctx, symbol)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "search_symbols":
		query, _ := args["q"].(string)
		mode, _ := args["mode"].(string)
		limit, _ := toInt(args["limit"])
		result, err := m.service.Search(ctx, query, mode, limit)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "get_tags":
		symbol, _ := args["symbol"].(string)
		result, err := m.service.GetTags(ctx, symbol)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "search_by_tag":
		tag, _ := args["tag"].(string)
		result, err := m.service.SearchByTag(ctx, tag)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "get_all_tags":
		result, err := m.service.GetAllTags(ctx)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	default:
		return jsonRPCResponse{
			JSONRPC: "2.0",
			ID:      id,
			Error:   &rpcError{Code: -32601, Message: fmt.Sprintf("tool not found: %s", name)},
		}
	}
}

// ---- 辅助函数 ----

func mcpResult(id any, result any) jsonRPCResponse {
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Result: map[string]any{
			"content": []map[string]any{
				{"type": "text", "text": toJSONString(result)},
			},
		},
	}
}

func mcpError(id any, err error) jsonRPCResponse {
	code := -32603 // Internal error
	msg := err.Error()
	if svcErr, ok := err.(*service.ServiceError); ok {
		switch svcErr.Code {
		case "ERR_INVALID_PARAMETER":
			code = -32602
		case "ERR_SYMBOL_NOT_FOUND":
			code = -32602
		}
		msg = svcErr.Message
	}
	return jsonRPCResponse{
		JSONRPC: "2.0",
		ID:      id,
		Error:   &rpcError{Code: code, Message: msg},
	}
}

func toJSONString(v any) string {
	b, _ := json.MarshalIndent(v, "", "  ")
	return string(b)
}

func toInt(v any) (int, bool) {
	switch val := v.(type) {
	case float64:
		return int(val), true
	case int:
		return val, true
	case json.Number:
		n, err := val.Int64()
		return int(n), err == nil
	default:
		// Try string parsing
		s, ok := v.(string)
		if !ok {
			return 0, false
		}
		var n int
		_, err := fmt.Sscanf(s, "%d", &n)
		return n, err == nil
	}
}

// ---- MCP 中间件 ----

// authMiddleware Bearer token 认证中间件。
func (m *MCPServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.authToken != "" {
			token := r.Header.Get("Authorization")
			if !strings.HasPrefix(token, "Bearer ") || strings.TrimPrefix(token, "Bearer ") != m.authToken {
				http.Error(w, `{"error":"unauthorized"}`, http.StatusUnauthorized)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware 添加 CORS 头，用于 MCP SSE 连接。
func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// recoveryMiddleware 捕获 panic 并返回 500 错误。
func recoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer robust.Recover()
		next.ServeHTTP(w, r)
	})
}

