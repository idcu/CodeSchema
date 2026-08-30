package server

import (
	"fmt"
	"net/http"

	log "gitee.com/idcu-go/log"
)

// 共享中间件：CORS 与 panic 恢复。
// HTTP API 与 MCP Server（SSE/stdio）此前各有一份实现（方法集/日志策略有差异），
// 收敛为参数化公共函数，消除重复。

// corsMiddlewareFor 生成 CORS 中间件。
// allowMethods 为该入口允许的 HTTP 方法（HTTP API 为 "GET, OPTIONS"；
// MCP SSE 需要 POST，为 "GET, POST, OPTIONS"）；allowHeaders 为允许的请求头。
func corsMiddlewareFor(allowMethods, allowHeaders string) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Access-Control-Allow-Origin", "*")
			w.Header().Set("Access-Control-Allow-Methods", allowMethods)
			w.Header().Set("Access-Control-Allow-Headers", allowHeaders)
			if r.Method == http.MethodOptions {
				w.WriteHeader(http.StatusNoContent)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// recoveryMiddlewareFor 生成 panic 恢复中间件。
// withError 为 true 时写 500 错误体（HTTP API 行为）；false 时仅恢复不中断
// （MCP 行为，SSE 连接由传输层处理）。两者都记录结构化日志。
func recoveryMiddlewareFor(withError bool, logger *log.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			defer func() {
				if rec := recover(); rec != nil {
					if logger != nil {
						logger.Error("panic recovered",
							"path", r.URL.Path,
							"error", fmt.Sprintf("%v", rec),
						)
					}
					if withError {
						writeError(w, "ERR_INTERNAL", fmt.Sprintf("internal server error: %v", rec), 500)
					}
				}
			}()
			next.ServeHTTP(w, r)
		})
	}
}
