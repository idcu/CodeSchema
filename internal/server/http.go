// Package server 提供 HTTP API 和 MCP Server 接口层实现。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/idcu/codeschema/internal/log"
	"github.com/idcu/codeschema/internal/metrics"
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/trace"
)

// init 注册 HTTP 服务器模块的监控指标。
func init() {
	metrics.RegisterCounter("http_requests_total", "Total HTTP requests", "method", "path", "status")
	metrics.RegisterGauge("http_active_requests", "Active HTTP requests")
}

// HTTPServer 基于 net/http 标准库的 HTTP API 服务器。
type HTTPServer struct {
	service   *service.Service
	addr      string
	server    *http.Server
	authToken string
	viz       *VizHandler // 可选的可视化工具处理器
	logger    *log.Logger
}

// NewHTTPServer 创建 HTTP API 服务器实例。
func NewHTTPServer(svc *service.Service, addr string) *HTTPServer {
	return &HTTPServer{
		service: svc,
		addr:    addr,
		logger:  log.WithModule("http"),
	}
}

// SetAuthToken 设置 Bearer token 认证。
func (h *HTTPServer) SetAuthToken(token string) {
	h.authToken = token
}

// SetVizHandler 设置向量索引可视化工具处理器。
func (h *HTTPServer) SetVizHandler(viz *VizHandler) {
	h.viz = viz
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

	// 中间件链：请求追踪 → 认证 → 路径遍历防护 → 错误恢复 → CORS
	handler := h.requestMetricsMiddleware(
		h.authMiddleware(
			h.pathTraversalMiddleware(
				h.errorRecoveryMiddleware(
					h.corsMiddleware(mux),
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
	status := h.service.Health(r.Context())
	writeJSON(w, http.StatusOK, status)
}

func (h *HTTPServer) handleHealthDB(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}

	start := time.Now()
	err := h.service.StoreHealthCheck(r.Context())
	latency := time.Since(start).Milliseconds()

	status := "ok"
	if err != nil {
		status = "degraded"
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"status":    status,
		"latency_ms": latency,
		"type":      "file",
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

	result, err := h.service.GetContext(r.Context(), symbol, contextLines)
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

	result, err := h.service.GetImpact(r.Context(), method, depth)
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

	result, err := h.service.GetTests(r.Context(), method, minConfidence)
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

	result, err := h.service.Search(r.Context(), query, mode, limit)
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
	result, err := h.service.GetTags(r.Context(), symbol)
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

	tag := r.URL.Query().Get("tag")
	result, err := h.service.SearchByTag(r.Context(), tag)
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
}

func (h *HTTPServer) handleGetAllTags(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}

	result, err := h.service.GetAllTags(r.Context())
	if err != nil {
		writeServiceError(w, err)
		return
	}
	writeJSON(w, http.StatusOK, result)
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
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")
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