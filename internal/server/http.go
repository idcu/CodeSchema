// Package server 提供 HTTP API 和 MCP Server 接口层实现。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/idcu/codeschema/internal/log"
	"github.com/idcu/codeschema/internal/metrics"
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/tenant"
	"github.com/idcu/codeschema/internal/trace"
)

// init 注册 HTTP 服务器模块的监控指标。
func init() {
	metrics.RegisterCounter("http_requests_total", "Total HTTP requests", "method", "path", "status")
	metrics.RegisterGauge("http_active_requests", "Active HTTP requests")
}

// TenantResolverHTTP 按租户 ID 解析出对应的运行期 Service。
type TenantResolverHTTP func(ctx context.Context, id string) (*service.Service, error)

// HTTPServer 基于 net/http 标准库的 HTTP API 服务器。
type HTTPServer struct {
	service      *service.Service
	addr         string
	server       *http.Server
	authToken    string
	viz          *VizHandler // 可选的可视化工具处理器
	logger       *log.Logger
	manager      *tenant.Manager
	resolver     TenantResolverHTTP
	defaultTenant string
	projects     []tenant.Info
	rateLimiter  *tokenBucket // 全局令牌桶限流器；nil 表示不限流
}

// NewHTTPServer 创建 HTTP API 服务器实例（单项目模式）。
func NewHTTPServer(svc *service.Service, addr string) *HTTPServer {
	return &HTTPServer{
		service:      svc,
		addr:         addr,
		logger:       log.WithModule("http"),
		resolver:     func(_ context.Context, _ string) (*service.Service, error) { return svc, nil },
		defaultTenant: "default",
		projects:     []tenant.Info{{ID: "default"}},
	}
}

// SetAuthToken 设置 Bearer token 认证。
func (h *HTTPServer) SetAuthToken(token string) {
	h.authToken = token
}

// SetRateLimit 设置全局限流：每分钟最多放行 rpm 个请求（令牌桶，突发=上限）。
// rpm <= 0 表示不限流（默认），调用方可据此保持默认行为不变。
func (h *HTTPServer) SetRateLimit(rpm int) {
	if rpm <= 0 {
		h.rateLimiter = nil
		return
	}
	// 令牌桶：容量=每分钟上限（允许突发一整桶），补充速率=上限/60 个/秒。
	h.rateLimiter = newTokenBucket(float64(rpm), float64(rpm)/60.0)
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
					h.errorRecoveryMiddleware(
						h.corsMiddleware(mux),
					),
				),
			),
		),
	)

	h.server = &http.Server{
		Addr:         h.addr,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	h.logger.Info("HTTP API server starting", "addr", h.addr)

	errCh := make(chan error, 1)
	go func() {
		if err := h.server.ListenAndServe(); err != nil && err != http.ErrServerClosed {
			errCh <- err
		}
	}()

	select {
	case <-ctx.Done():
		return h.Stop()
	case err := <-errCh:
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
	// P0: KV 缓存尚未实现，返回占位状态
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"type":   "none",
		"note":   "KV cache not implemented (P0 placeholder)",
	})
}

func (h *HTTPServer) handleHealthVector(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}
	// P8.1: 内存向量索引已实现，支持语义搜索
	writeJSON(w, http.StatusOK, map[string]any{
		"status": "ok",
		"type":   "memory",
		"note":   "In-memory vector index active (P8.1, P2 for chromem-go)",
	})
}

// ---- 核心查询路由处理函数 ----

func (h *HTTPServer) handleContext(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}

	symbol := r.URL.Query().Get("symbol")
	contextLines, _ := strconv.Atoi(r.URL.Query().Get("context_lines"))
	mode := r.URL.Query().Get("mode") // full（默认）/ minimal（极简，供评测基线）

	result, err := h.serviceForRequest(r).GetContextMode(r.Context(), symbol, service.ContextOptions{
		ContextLines: contextLines,
		Mode:         service.ContextMode(mode),
		IncludeTrace: true,
	})
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

	method := r.URL.Query().Get("method")
	depth, _ := strconv.Atoi(r.URL.Query().Get("depth"))

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

	method := r.URL.Query().Get("method")
	minConfidence, _ := strconv.Atoi(r.URL.Query().Get("min_confidence"))

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

	result, err := h.serviceForRequest(r).Search(r.Context(), query, mode, limit)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
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
type errorResponse struct {
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
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
	writeJSON(w, status, resp)
}

func writeServiceError(w http.ResponseWriter, err error) {
	if svcErr, ok := err.(*service.ServiceError); ok {
		writeError(w, svcErr.Code, svcErr.Message, svcErr.HTTPStatus())
		return
	}
	writeError(w, "ERR_INTERNAL", err.Error(), 500)
}

// ---- 中间件 ----

// errorRecoveryMiddleware 捕获 panic 并返回 500 错误。
func (h *HTTPServer) errorRecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				h.logger.Error("panic recovered",
					"path", r.URL.Path,
					"error", fmt.Sprintf("%v", rec),
				)
				writeError(w, "ERR_INTERNAL", fmt.Sprintf("internal server error: %v", rec), 500)
			}
		}()
		next.ServeHTTP(w, r)
	})
}

// corsMiddleware 添加 CORS 头。
func (h *HTTPServer) corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, X-Tenant")
		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}
		next.ServeHTTP(w, r)
	})
}

// authMiddleware Bearer token 认证中间件。
func (h *HTTPServer) authMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.authToken != "" {
			token := r.Header.Get("Authorization")
			if !strings.HasPrefix(token, "Bearer ") || strings.TrimPrefix(token, "Bearer ") != h.authToken {
				writeError(w, "ERR_UNAUTHORIZED", "invalid or missing auth token", 401)
				return
			}
		}
		next.ServeHTTP(w, r)
	})
}

// rateLimitMiddleware 全局限流中间件：未启用限流（rateLimiter == nil）时直接放行，
// 否则按令牌桶判断，超限返回 429 Too Many Requests。
func (h *HTTPServer) rateLimitMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if h.rateLimiter == nil || h.rateLimiter.allow() {
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