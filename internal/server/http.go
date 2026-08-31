// Package server 提供 HTTP API 和 MCP Server 接口层实现。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	cerrors "github.com/idcu/codeschema/internal/errors"
	auth "gitee.com/idcu-go/auth"
	log "gitee.com/idcu-go/log"
	"gitee.com/idcu-go/metrics"
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/tenant"
	trace "gitee.com/idcu-go/trace"
)

// init 注册 HTTP 服务器模块的监控指标。
func init() {
	metrics.RegisterCounter("http_requests_total", "Total HTTP requests", "method", "path", "status")
	metrics.RegisterGauge("http_active_requests", "Active HTTP requests")
}

// TenantResolverHTTP 按租户 ID 解析出对应的运行期 Service。
type TenantResolverHTTP func(ctx context.Context, id string) (*service.Service, error)

// httpRuntimeConfig 每次请求读取的运行时快照，支持热更新（认证令牌 + 限流）。
// 通过 atomic.Value 整体替换实现无锁读，配置变更即时生效，无需重启进程。
type httpRuntimeConfig struct {
	authToken   string
	rateLimiter *tokenBucket // nil 表示不限流
}

// HTTPServer 基于 net/http 标准库的 HTTP API 服务器。
type HTTPServer struct {
	service       *service.Service
	addr          string
	server        *http.Server
	runtime       atomic.Value // httpRuntimeConfig：authToken / rateLimiter 热更新快照
	viz           *VizHandler  // 可选的可视化工具处理器
	logger        *log.Logger
	manager       *tenant.Manager
	resolver      TenantResolverHTTP
	defaultTenant string
	projects      []tenant.Info
	ln            net.Listener // 当前监听器（UpdateRuntime 地址重绑时替换）
	lnMu          sync.Mutex
	errCh         chan error
	// kvHealth/vectorHealth 可选健康检查函数（由 cmd 层注入真实状态；
	// 未注入时健康端点返回 not_configured/默认描述，行为向后兼容）。
	kvHealth     func(ctx context.Context) error
	vectorHealth func(ctx context.Context) error
	// ctxDefaults 上下文裁剪的服务端默认值（来自 config.context）；
	// 请求显式传参时以请求为准，未传时取这里的值。
	ctxDefaults service.ContextOptions
}

// NewHTTPServer 创建 HTTP API 服务器实例（单项目模式）。
// SetContextDefaults 设置上下文裁剪的服务端默认值（context.* 配置）。
//
// 优先级：请求参数 > 服务端默认值 > 类型零值（不限）。
// 只覆盖调用方未显式指定的字段，因此请求里传了 max_bytes 就不会被默认值顶掉。
func (h *HTTPServer) SetContextDefaults(def service.ContextOptions) {
	h.ctxDefaults = def
}

// mergeContextOptions 用服务端默认值补齐请求未指定的裁剪选项。
func (h *HTTPServer) mergeContextOptions(o service.ContextOptions) service.ContextOptions {
	d := h.ctxDefaults
	if o.ContextLines == 0 {
		o.ContextLines = d.ContextLines
	}
	if o.Mode == "" {
		o.Mode = d.Mode
	}
	if o.MaxBytes == 0 {
		o.MaxBytes = d.MaxBytes
	}
	if o.MaxTokens == 0 {
		o.MaxTokens = d.MaxTokens
	}
	if o.MaxLineChars == 0 {
		o.MaxLineChars = d.MaxLineChars
	}
	if o.CharsPerToken == 0 {
		o.CharsPerToken = d.CharsPerToken
	}
	if o.PathStyle == "" {
		o.PathStyle = d.PathStyle
	}
	return o
}

func NewHTTPServer(svc *service.Service, addr string) *HTTPServer {
	h := &HTTPServer{
		service:       svc,
		addr:          addr,
		logger:        log.WithModule("http"),
		resolver:      func(_ context.Context, _ string) (*service.Service, error) { return svc, nil },
		defaultTenant: "default",
		projects:      []tenant.Info{{ID: "default"}},
	}
	h.runtime.Store(httpRuntimeConfig{})
	return h
}

// SetAuthToken 设置 Bearer token 认证（可热更新，即时生效）。
func (h *HTTPServer) SetAuthToken(token string) {
	h.UpdateRuntime(h.addr, token, h.currentRateLimit())
}

// SetRateLimit 设置全局限流：每分钟最多放行 rpm 个请求（令牌桶，突发=上限）。
// rpm <= 0 表示不限流（默认），调用方可据此保持默认行为不变。可热更新，即时生效。
func (h *HTTPServer) SetRateLimit(rpm int) {
	h.UpdateRuntime(h.addr, h.currentAuthToken(), rpm)
}

// currentAuthToken / currentRateLimit 读取当前运行时快照（供 SetAuthToken 等
// 单字段 setter 在 UpdateRuntime 时保留另一个字段的值）。
func (h *HTTPServer) currentAuthToken() string {
	return h.runtime.Load().(httpRuntimeConfig).authToken
}

func (h *HTTPServer) currentRateLimit() int {
	rl := h.runtime.Load().(httpRuntimeConfig).rateLimiter
	if rl == nil {
		return 0
	}
	return int(rl.capacity)
}

// rateLimiterFromRPM 按每分钟上限构造令牌桶；rpm <= 0 返回 nil（不限流）。
func rateLimiterFromRPM(rpm int) *tokenBucket {
	if rpm <= 0 {
		return nil
	}
	// 令牌桶：容量=每分钟上限（允许突发一整桶），补充速率=上限/60 个/秒。
	return newTokenBucket(float64(rpm), float64(rpm)/60.0)
}

// UpdateRuntime 热更新运行期配置（配合 config.ConfigWatcher，无需重启进程）：
//   - authToken：认证令牌即时替换，新请求立即使用新令牌；
//   - rpm：限流上限即时生效（0 表示不限流）；
//   - addr：非空且与当前不同时，无中断重绑监听地址（旧监听关闭、在途请求
//     由原 server 继续处理完毕），返回非 nil 错误表示重绑失败（保留旧监听）。
func (h *HTTPServer) UpdateRuntime(addr, authToken string, rpm int) error {
	h.runtime.Store(httpRuntimeConfig{
		authToken:   authToken,
		rateLimiter: rateLimiterFromRPM(rpm),
	})
	if addr != "" && addr != h.addr {
		if h.server == nil {
			// 尚未 Start：仅记录新地址，Start 时按最新地址监听（避免提前开监听）。
			h.addr = addr
			return nil
		}
		if err := h.rebind(addr); err != nil {
			return fmt.Errorf("rebind %s: %w", addr, err)
		}
		h.addr = addr
		h.logger.Info("HTTP API server rebound", "addr", addr)
	}
	return nil
}

// rebind 在指定地址开启新监听并切换：新监听器立即接管，旧监听器关闭后其
// Serve goroutine 自然退出（已建立的连接继续由原 server 服务完毕）。
func (h *HTTPServer) rebind(addr string) error {
	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return err
	}
	h.lnMu.Lock()
	old := h.ln
	h.ln = ln
	h.lnMu.Unlock()
	go h.serve(ln)
	if old != nil {
		_ = old.Close()
	}
	return nil
}

// serve 在指定监听器上服务连接；仅当前监听器的异常退出会反馈到 Start 的错误通道。
func (h *HTTPServer) serve(ln net.Listener) {
	err := h.server.Serve(ln)
	if err == http.ErrServerClosed {
		return
	}
	h.lnMu.Lock()
	current := h.ln == ln
	h.lnMu.Unlock()
	if current && err != nil {
		select {
		case h.errCh <- err:
		default:
		}
	}
}

// SetTenantManager 注入多租户管理器，使本服务器以单实例服务多个隔离仓库。
// 调用后所有查询按 X-Tenant 头或 ?tenant= 参数（缺省用默认租户）路由到对应实例。
func (h *HTTPServer) SetTenantManager(mgr *tenant.Manager) {
	h.manager = mgr
	h.resolver = mgr.Service
	h.defaultTenant = mgr.DefaultID()
	h.projects = mgr.List()
}

// SetVizHandler 设置向量索引可视化工具处理器。
func (h *HTTPServer) SetVizHandler(viz *VizHandler) {
	h.viz = viz
}

// SetKVHealthCheck 注入 KV 缓存健康检查函数（Redis 可用时报告真实状态）。
func (h *HTTPServer) SetKVHealthCheck(fn func(ctx context.Context) error) {
	h.kvHealth = fn
}

// SetVectorHealthCheck 注入向量索引健康检查函数（可选后端时报告真实状态）。
func (h *HTTPServer) SetVectorHealthCheck(fn func(ctx context.Context) error) {
	h.vectorHealth = fn
}

// serviceForRequest 从请求中解析租户 ID 并返回对应 Service。
// 单项目模式下忽略租户标识，始终返回唯一实例。
func (h *HTTPServer) serviceForRequest(r *http.Request) *service.Service {
	id := r.Header.Get("X-Tenant")
	if id == "" {
		id = r.URL.Query().Get("tenant")
	}
	if id == "" {
		id = h.defaultTenant
	}
	if h.resolver != nil {
		if svc, err := h.resolver(r.Context(), id); err == nil {
			return svc
		}
	}
	return h.service
}

// Start 启动 HTTP 服务器，阻塞直到 Shutdown 被调用。
func (h *HTTPServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// 健康检查端点
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/health/db", h.handleHealthDB)
	mux.HandleFunc("/health/kv", h.handleHealthKV)
	mux.HandleFunc("/health/vector", h.handleHealthVector)

	// 核心查询端点
	mux.HandleFunc("/context", h.handleContext)
	mux.HandleFunc("/impact", h.handleImpact)
	mux.HandleFunc("/tests", h.handleTests)
	mux.HandleFunc("/search", h.handleSearch)

	// 标签相关端点
	mux.HandleFunc("/tags", h.handleGetTags)
	mux.HandleFunc("/tags/search", h.handleSearchByTag)
	mux.HandleFunc("/tags/all", h.handleGetAllTags)

	// 多租户：枚举当前实例服务的仓库
	mux.HandleFunc("/projects", h.handleProjects)

	// 可观测性端点
	mux.HandleFunc("/metrics", h.handleMetrics)

	// OpenAPI 3.0 规范与文档页（T4-2）
	mux.HandleFunc("/openapi.json", handleOpenAPI)
	mux.HandleFunc("/docs", handleAPIDocs)
	mux.HandleFunc("/docs/", handleAPIDocs)

	// 向量索引可视化工具（可选）
	if h.viz != nil {
		RegisterVizRoutes(mux, h.viz)
	}

	// 中间件链：请求追踪 → 限流 → 认证 → 路径遍历防护 → 错误恢复 → CORS
	handler := h.requestMetricsMiddleware(
		h.rateLimitMiddleware(
			h.authMiddleware(
				h.pathTraversalMiddleware(
					recoveryMiddlewareFor(true, h.logger)(
						corsMiddlewareFor("GET, OPTIONS", "Content-Type, X-Tenant")(mux),
					),
				),
			),
		),
	)

	h.server = &http.Server{
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}
	h.errCh = make(chan error, 1)

	h.logger.Info("HTTP API server starting", "addr", h.addr)

	// 显式监听：监听器可被 UpdateRuntime 在地址变更时热替换。
	if err := h.rebind(h.addr); err != nil {
		return err
	}

	select {
	case <-ctx.Done():
		return h.Stop()
	case err := <-h.errCh:
		return err
	}
}

// Stop 优雅关闭 HTTP 服务器。
func (h *HTTPServer) Stop() error {
	if h.server == nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	return h.server.Shutdown(ctx)
}

// ---- 健康检查路由处理函数 ----

func (h *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}
	status := h.serviceForRequest(r).Health(r.Context())
	writeJSON(w, http.StatusOK, status)
}

func (h *HTTPServer) handleHealthDB(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}

	start := time.Now()
	err := h.serviceForRequest(r).StoreHealthCheck(r.Context())
	latency := time.Since(start).Milliseconds()

	status := "ok"
	if err != nil {
		status = "degraded"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":     status,
		"latency_ms": latency,
		"type":       "file",
	})
}

func (h *HTTPServer) handleHealthKV(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}
	// KV 缓存层（Redis）为可选后端：已实现但不强制启用。
	// 状态由 cmd 层注入（SetKVHealthCheck），未注入时返回 not_configured。
	if h.kvHealth != nil {
		status := "ok"
		if err := h.kvHealth(r.Context()); err != nil {
			status = "degraded"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": status,
			"type":   "redis",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"type":   "none",
		"note":   "KV cache (redis) not configured",
	})
}

func (h *HTTPServer) handleHealthVector(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}
	// 向量索引已实现（Persistent/Memory/Chromem），状态由 cmd 层注入。
	if h.vectorHealth != nil {
		status := "ok"
		if err := h.vectorHealth(r.Context()); err != nil {
			status = "degraded"
		}
		writeJSON(w, http.StatusOK, map[string]any{
			"status": status,
			"type":   "vector",
		})
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"type":   "memory",
		"note":   "Vector index active (Persistent/Memory)",
	})
}

// ---- 核心查询路由处理函数 ----

func (h *HTTPServer) handleContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}

	q := r.URL.Query()
	symbols := querySymbols(q, "symbols", "symbol")
	opts := h.mergeContextOptions(contextOptionsFromQuery(q))
	if len(symbols) > 1 {
		// 批量：单轮返回 N 个符号，把 N 次请求压成 1 次（B5）。
		writeJSON(w, http.StatusOK, h.serviceForRequest(r).GetContextBatchDetailed(r.Context(), symbols, opts))
		return
	}
	symbol := q.Get("symbol")
	if len(symbols) == 1 {
		symbol = symbols[0]
	}
	result, err := h.serviceForRequest(r).GetContextMode(r.Context(), symbol, opts)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *HTTPServer) handleImpact(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}

	q := r.URL.Query()
	methods := querySymbols(q, "methods", "method")
	depth, _ := strconv.Atoi(q.Get("depth"))
	if len(methods) > 1 {
		writeJSON(w, http.StatusOK, h.serviceForRequest(r).GetImpactBatch(r.Context(), methods, depth))
		return
	}
	method := q.Get("method")
	if len(methods) == 1 {
		method = methods[0]
	}
	result, err := h.serviceForRequest(r).GetImpact(r.Context(), method, depth)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *HTTPServer) handleTests(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}

	q := r.URL.Query()
	methods := querySymbols(q, "methods", "method")
	minConfidence, _ := strconv.Atoi(q.Get("min_confidence"))
	if len(methods) > 1 {
		writeJSON(w, http.StatusOK, h.serviceForRequest(r).GetTestsBatch(r.Context(), methods, minConfidence))
		return
	}
	method := q.Get("method")
	if len(methods) == 1 {
		method = methods[0]
	}
	result, err := h.serviceForRequest(r).GetTests(r.Context(), method, minConfidence)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *HTTPServer) handleSearch(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}

	query := r.URL.Query().Get("q")
	mode := r.URL.Query().Get("mode")
	limit, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	minScore, _ := strconv.ParseFloat(r.URL.Query().Get("min_score"), 64)

	outcome, err := h.serviceForRequest(r).SearchWithOptions(r.Context(), query, mode, limit, minScore)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, outcome)
}

// ---- 标签路由处理函数 ----

func (h *HTTPServer) handleGetTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}

	symbol := r.URL.Query().Get("symbol")
	result, err := h.serviceForRequest(r).GetTags(r.Context(), symbol)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *HTTPServer) handleSearchByTag(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}

	tags := parseQueryTags(r.URL.Query())
	result, err := h.serviceForRequest(r).SearchByTags(r.Context(), tags)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// parseQueryTags 解析 /tags/search 的 tag 参数：支持逗号分隔与重复参数
// （?tag=a,b&tag=c → [a b c]）。空参数返回 [""] 触发 service 层校验。
func parseQueryTags(q url.Values) []string {
	var tags []string
	for _, v := range q["tag"] {
		for _, p := range strings.Split(v, ",") {
			if p = strings.TrimSpace(p); p != "" {
				tags = append(tags, p)
			}
		}
	}
	if len(tags) == 0 {
		tags = []string{""}
	}
	return tags
}

func (h *HTTPServer) handleGetAllTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}

	result, err := h.serviceForRequest(r).GetAllTags(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

// handleProjects 返回当前实例服务的所有仓库元信息（多租户模式下列出全部租户）。
func (h *HTTPServer) handleProjects(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"default":  h.defaultTenant,
		"projects": h.projects,
	})
}

// ---- 可观测性路由 ----

// handleMetrics 返回 Prometheus 文本格式的指标数据。
func (h *HTTPServer) handleMetrics(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}
	w.Header().Set("Content-Type", "text/plain; version=0.0.4")
	w.WriteHeader(http.StatusOK)
	fmt.Fprint(w, metrics.Render())
}

// ---- 工具函数 ----

// errorResponse 错误响应结构。
//
// Hint 字段（B3）：随错误回传「这类错误的常见修法」，让调用方 Agent 一轮内自愈，
// 而不是靠猜——与 FastContext 的错误 hint 同源。未知错误码不输出该字段。
type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
		Hint    string `json:"hint,omitempty"`
	} `json:"error"`
}

func writeJSON(w http.ResponseWriter, status int, data any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(data)
}

func writeError(w http.ResponseWriter, code string, message string, status int) {
	resp := errorResponse{}
	resp.Error.Code = code
	resp.Error.Message = message
	resp.Error.Hint = cerrors.Hint(code)
	writeJSON(w, status, resp)
}

func writeServiceError(w http.ResponseWriter, err error) {
	if svcErr, ok := err.(*service.ServiceError); ok {
		writeError(w, svcErr.Code, svcErr.Message, svcErr.HTTPStatus())
		return
	}
	writeError(w, "ERR_INTERNAL", err.Error(), 500)
}

// querySymbols 从 query 提取符号列表：支持重复参数与逗号分隔两种写法，并兼容单值参数。
//
// 批量入参（B5）在 HTTP 侧没有数组类型，统一按「重复参数 + 逗号分隔」收敛，
// 与 MCP 侧的 symbols: [...] 语义保持一致。
func querySymbols(q url.Values, plural, single string) []string {
	out := make([]string, 0, 4)
	seen := make(map[string]bool)
	add := func(s string) {
		s = strings.TrimSpace(s)
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, raw := range q[plural] {
		for _, part := range strings.Split(raw, ",") {
			add(part)
		}
	}
	add(q.Get(single))
	return out
}

// contextOptionsFromQuery 从 query 解析上下文选项（含输出预算与路径形态）。
func contextOptionsFromQuery(q url.Values) service.ContextOptions {
	contextLines, _ := strconv.Atoi(q.Get("context_lines"))
	return service.ContextOptions{
		ContextLines:  contextLines,
		Mode:          service.ContextMode(q.Get("mode")), // full（默认）/ minimal（极简，供评测基线）
		IncludeTrace:  true,
		MaxBytes:      atoiOr(q.Get("max_bytes"), 0),
		MaxTokens:     atoiOr(q.Get("max_tokens"), 0),
		MaxLineChars:  atoiOr(q.Get("max_line_chars"), 0),
		CharsPerToken: atofOr(q.Get("chars_per_token"), 0),
		PathStyle:     service.PathStyle(q.Get("path_style")),
	}
}

// atoiOr 解析整数参数，为空或非法时返回默认值。
func atoiOr(s string, def int) int {
	if s == "" {
		return def
	}
	n, err := strconv.Atoi(s)
	if err != nil {
		return def
	}
	return n
}

// atofOr 解析浮点参数，为空或非法时返回默认值。
func atofOr(s string, def float64) float64 {
	if s == "" {
		return def
	}
	f, err := strconv.ParseFloat(s, 64)
	if err != nil {
		return def
	}
	return f
}

// ---- 中间件 ----
//
// CORS 与 panic 恢复收敛为包级公共函数（middleware.go）：
//   recoveryMiddlewareFor(true, h.logger) / corsMiddlewareFor("GET, OPTIONS", ...)。
// 本文件仅保留依赖实例状态（认证/限流/路径遍历/请求指标）的中间件。

// authMiddleware Bearer token 认证中间件。
// 每次请求读取运行时快照中的认证令牌：支持热更新（UpdateRuntime），即时生效。
func (h *HTTPServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if token := h.runtime.Load().(httpRuntimeConfig).authToken; token != "" && !auth.Check(r, token) {
			writeError(w, "ERR_UNAUTHORIZED", "invalid or missing auth token", 401)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitMiddleware 全局限流中间件：未启用限流（rateLimiter == nil）时直接放行，
// 否则按令牌桶判断，超限返回 429 Too Many Requests。
// 每次请求读取运行时快照中的限流器：支持热更新（UpdateRuntime），即时生效。
func (h *HTTPServer) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if rl := h.runtime.Load().(httpRuntimeConfig).rateLimiter; rl == nil || rl.allow() {
			next.ServeHTTP(w, r)
			return
		}
		w.Header().Set("Retry-After", "1")
		writeError(w, "ERR_RATE_LIMITED", "too many requests", http.StatusTooManyRequests)
	})
}

// tokenBucket 是一个简单的并发安全令牌桶限流器。
type tokenBucket struct {
	mu       sync.Mutex
	capacity float64 // 桶容量（允许突发）
	tokens   float64 // 当前令牌数
	rate     float64 // 每秒补充速率
	last     time.Time
}

// newTokenBucket 创建容量为 capacity 的令牌桶，按 rate（个/秒）持续补充令牌。
func newTokenBucket(capacity, rate float64) *tokenBucket {
	return &tokenBucket{capacity: capacity, tokens: capacity, rate: rate, last: time.Now()}
}

// allow 原子地判断是否放行一个请求（消耗一枚令牌）。
func (b *tokenBucket) allow() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := time.Now()
	// 先按经过的时间补充令牌，再消费。
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return true
	}
	return false
}

// pathTraversalMiddleware 防止路径遍历攻击。
func (h *HTTPServer) pathTraversalMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// 检查所有查询参数中的路径值
		for _, values := range r.URL.Query() {
			for _, v := range values {
				if strings.Contains(v, "..") {
					writeError(w, "ERR_INVALID_PARAMETER", "invalid path: '..' detected", 400)
					return
				}
			}
		}
		next.ServeHTTP(w, r)
	})
}

// requestMetricsMiddleware 记录请求指标和追踪。
func (h *HTTPServer) requestMetricsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		span := trace.Start("http_request", "method", r.Method, "path", r.URL.Path)
		defer span.End()

		metrics.IncGauge("http_active_requests")
		defer metrics.DecGauge("http_active_requests")

		// 包装 ResponseWriter 以捕获状态码
		lrw := &loggingResponseWriter{ResponseWriter: w, statusCode: http.StatusOK}
		start := time.Now()

		next.ServeHTTP(lrw, r)

		duration := time.Since(start).Milliseconds()
		status := strconv.Itoa(lrw.statusCode)

		metrics.IncCounter("http_requests_total", r.Method, r.URL.Path, status)

		h.logger.Debug("request completed",
			"method", r.Method,
			"path", r.URL.Path,
			"status", lrw.statusCode,
			"duration_ms", duration,
		)
	})
}

// loggingResponseWriter 包装 http.ResponseWriter 以捕获状态码。
type loggingResponseWriter struct {
	http.ResponseWriter
	statusCode int
}

func (lrw *loggingResponseWriter) WriteHeader(code int) {
	lrw.statusCode = code
	lrw.ResponseWriter.WriteHeader(code)
}
