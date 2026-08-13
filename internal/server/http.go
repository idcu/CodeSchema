// Package server 提供 HTTP API 和 MCP Server 接口层实现。
package server

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"strconv"
	"time"

	"codeschema/internal/service"
)

// HTTPServer 基于 net/http 标准库的 HTTP API 服务器。
type HTTPServer struct {
	service *service.Service
	addr    string
	server  *http.Server
}

// NewHTTPServer 创建 HTTP API 服务器实例。
func NewHTTPServer(svc *service.Service, addr string) *HTTPServer {
	return &HTTPServer{
		service: svc,
		addr:    addr,
	}
}

// Start 启动 HTTP 服务器，阻塞直到 Shutdown 被调用。
func (h *HTTPServer) Start(ctx context.Context) error {
	mux := http.NewServeMux()

	// 注册路由
	mux.HandleFunc("/health", h.handleHealth)
	mux.HandleFunc("/context", h.handleContext)
	mux.HandleFunc("/impact", h.handleImpact)
	mux.HandleFunc("/tests", h.handleTests)
	mux.HandleFunc("/search", h.handleSearch)

	// 标签相关端点
	mux.HandleFunc("/tags", h.handleGetTags)
	mux.HandleFunc("/tags/search", h.handleSearchByTag)
	mux.HandleFunc("/tags/all", h.handleGetAllTags)

	// 包装为带错误恢复的中间件
	handler := h.errorRecoveryMiddleware(h.corsMiddleware(mux))

	h.server = &http.Server{
		Addr:         h.addr,
		Handler:      handler,
		ReadTimeout:  10 * time.Second,
		WriteTimeout: 10 * time.Second,
		IdleTimeout:  30 * time.Second,
	}

	log.Printf("HTTP API server starting on %s", h.addr)

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

// ---- 路由处理函数 ----

func (h *HTTPServer) handleHealth(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		writeError(w, "ERR_INVALID_PARAMETER", "method not allowed", 405)
		return
	}
	status := h.service.Health(r.Context())
	writeJSON(w, http.StatusOK, status)
}

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

// errorRecoveryMiddleware 捕获 panic 并返回 500 错误。
func (h *HTTPServer) errorRecoveryMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		defer func() {
			if rec := recover(); rec != nil {
				log.Printf("panic recovered: %v", rec)
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