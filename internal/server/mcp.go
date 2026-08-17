package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	"github.com/idcu/codeschema/internal/robust"
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/tenant"
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
	service       *service.Service
	addr          string
	server        *http.Server
	tools         []mcpTool
	authToken     atomic.Value // string：认证令牌，支持热更新（SetAuthToken 即时生效）
	manager       *tenant.Manager
	resolver      TenantResolver
	defaultTenant string
	projects      []tenant.Info
}

// TenantResolver 按租户 ID 解析出对应的运行期 Service。
// 单项目模式下忽略 id，始终返回唯一实例。
type TenantResolver func(ctx context.Context, id string) (*service.Service, error)

// NewMCPServer 创建 MCP Server 实例（单项目模式）。
func NewMCPServer(svc *service.Service, addr string) *MCPServer {
	m := &MCPServer{
		service:       svc,
		addr:          addr,
		tools:         defineTools(),
		resolver:      func(_ context.Context, _ string) (*service.Service, error) { return svc, nil },
		defaultTenant: "default",
		projects:      []tenant.Info{{ID: "default"}},
	}
	m.authToken.Store("")
	return m
}

// SetTenantManager 注入多租户管理器，使本服务器以单实例服务多个隔离仓库。
// 调用后所有工具按请求中的 project 参数（缺省用默认租户）路由到对应实例。
func (m *MCPServer) SetTenantManager(mgr *tenant.Manager) {
	m.manager = mgr
	m.resolver = mgr.Service
	m.defaultTenant = mgr.DefaultID()
	m.projects = mgr.List()
}

// resolveTenant 从工具参数中解析租户 ID 并返回对应 Service。
func (m *MCPServer) resolveTenant(ctx context.Context, args map[string]any) (*service.Service, error) {
	id, _ := args["project"].(string)
	if id == "" {
		id = m.defaultTenant
	}
	return m.resolver(ctx, id)
}

// SetAuthToken 设置 Bearer token 认证。
// 可热更新（配合 config.ConfigWatcher，无需重启进程）：新令牌即时生效，
// 传空串关闭认证。通过 atomic.Value 存储，读写无锁、无数据竞争。
func (m *MCPServer) SetAuthToken(token string) {
	m.authToken.Store(token)
}

// defineTools 定义 MCP 工具列表。
//
// 所有面向仓库的工具均接受可选的 project 参数（多租户模式下用于选择目标仓库，
// 缺省使用默认租户）。list_projects 用于枚举当前实例服务的所有仓库。
func defineTools() []mcpTool {
	projectProp := toolProperty{Type: "string", Description: "租户/项目 ID（多租户模式下选择目标仓库，缺省用默认租户）"}
	return []mcpTool{
		{
			Name:        "context",
			Description: "获取指定符号的精准裁剪上下文（方法体 + 字段 + 接口 + 单测）",
			InputSchema: toolParams{
				Type: "object",
				Properties: map[string]toolProperty{
					"symbol":        {Type: "string", Description: "类/方法全限定名"},
					"context_lines": {Type: "number", Description: "上下文行数", Default: 5},
					"project":       projectProp,
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
					"method":  {Type: "string", Description: "方法全限定名"},
					"depth":   {Type: "number", Description: "分析深度", Default: 1},
					"project": projectProp,
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
					"project":        projectProp,
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
					"project":   projectProp,
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
					"symbol":  {Type: "string", Description: "符号全限定名"},
					"depth":   {Type: "number", Description: "调用深度", Default: 1},
					"project": projectProp,
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
					"project": projectProp,
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
					"symbol":  {Type: "string", Description: "符号全限定名"},
					"project": projectProp,
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
					"q":       {Type: "string", Description: "搜索关键词"},
					"mode":    {Type: "string", Description: "搜索模式: exact/semantic/both", Default: "both"},
					"limit":   {Type: "number", Description: "返回结果数量限制", Default: 20},
					"project": projectProp,
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
					"symbol":  {Type: "string", Description: "类/方法全限定名"},
					"project": projectProp,
				},
				Required: []string{"symbol"},
			},
		},
		{
			Name:        "search_by_tag",
			Description: "按标签搜索类和方法的 ID 和名称（支持多标签 AND 交集，逗号分隔，如 controller,cache）",
			InputSchema: toolParams{
				Type: "object",
				Properties: map[string]toolProperty{
					"tag":     {Type: "string", Description: "标签名，可逗号分隔多个（AND 交集）。如 controller/service/cache/go"},
					"project": projectProp,
				},
				Required: []string{"tag"},
			},
		},
		{
			Name:        "get_all_tags",
			Description: "获取所有已知标签及其分类统计",
			InputSchema: toolParams{
				Type: "object",
				Properties: map[string]toolProperty{
					"project": projectProp,
				},
			},
		},
		{
			Name:        "list_projects",
			Description: "枚举当前实例服务的所有仓库（多租户模式下列出全部租户，单项目模式下仅 default）",
			InputSchema: toolParams{
				Type:       "object",
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

	// 复用纯逻辑处理器（stdio 传输同路径）
	writeJSON(w, http.StatusOK, m.handleRequest(req))
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

	// 多租户路由：解析目标租户的 Service（project 参数缺省用默认租户）。
	svc, rerr := m.resolveTenant(ctx, args)
	if rerr != nil {
		return mcpError(id, rerr)
	}

	switch name {
	case "context":
		symbol, _ := args["symbol"].(string)
		contextLines, _ := toInt(args["context_lines"])
		mode, _ := args["mode"].(string) // 注入模式：full（默认）/ minimal（极简，供评测基线）
		result, err := svc.GetContextMode(ctx, symbol, service.ContextOptions{
			ContextLines: contextLines,
			Mode:         service.ContextMode(mode),
			IncludeTrace: true,
		})
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "impact":
		method, _ := args["method"].(string)
		depth, _ := toInt(args["depth"])
		result, err := svc.GetImpact(ctx, method, depth)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "tests":
		method, _ := args["method"].(string)
		minConfidence, _ := toInt(args["min_confidence"])
		result, err := svc.GetTests(ctx, method, minConfidence)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "affected":
		symbol, _ := args["symbol"].(string)
		recursive, _ := args["recursive"].(bool)
		result, err := svc.GetAffected(ctx, symbol, recursive)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "get_call_graph":
		symbol, _ := args["symbol"].(string)
		depth, _ := toInt(args["depth"])
		result, err := svc.GetCallGraph(ctx, symbol, depth)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "search_config":
		pattern, _ := args["pattern"].(string)
		result, err := svc.SearchConfig(ctx, pattern)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "find_dependencies":
		symbol, _ := args["symbol"].(string)
		result, err := svc.FindDependencies(ctx, symbol)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "search_symbols":
		query, _ := args["q"].(string)
		mode, _ := args["mode"].(string)
		limit, _ := toInt(args["limit"])
		result, err := svc.Search(ctx, query, mode, limit)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "get_tags":
		symbol, _ := args["symbol"].(string)
		result, err := svc.GetTags(ctx, symbol)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "search_by_tag":
		tagStr, _ := args["tag"].(string)
		var tags []string
		if tagStr != "" {
			for _, t := range strings.Split(tagStr, ",") {
				if t = strings.TrimSpace(t); t != "" {
					tags = append(tags, t)
				}
			}
		}
		result, err := svc.SearchByTags(ctx, tags)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "get_all_tags":
		result, err := svc.GetAllTags(ctx)
		if err != nil {
			return mcpError(id, err)
		}
		return mcpResult(id, result)

	case "list_projects":
		return mcpResult(id, m.projects)

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
// 每次请求读取运行时快照中的认证令牌：支持热更新（SetAuthToken），即时生效。
func (m *MCPServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token, _ := m.authToken.Load().(string); token != "" {
			auth := r.Header.Get("Authorization")
			if !strings.HasPrefix(auth, "Bearer ") || strings.TrimPrefix(auth, "Bearer ") != token {
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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization, X-Tenant")
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

