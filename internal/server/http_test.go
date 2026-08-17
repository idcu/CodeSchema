package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
)

func newTestHTTPServer(t *testing.T) *HTTPServer {
	t.Helper()
	dir := t.TempDir()
	st := store.NewStore("file")
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	seedSymbol(t, st)
	svc := service.NewService(st)
	return NewHTTPServer(svc, ":0")
}

func TestHealthEndpoint(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()

	srv.handleHealth(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %v", body["status"])
	}
}

func TestHealthEndpoint_MethodNotAllowed(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodPost, "/health", nil)
	w := httptest.NewRecorder()
	srv.handleHealth(w, req)

	if w.Result().StatusCode != http.StatusMethodNotAllowed {
		t.Errorf("expected 405, got %d", w.Result().StatusCode)
	}
}

func TestContextEndpoint_EmptySymbol(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/context", nil)
	w := httptest.NewRecorder()
	srv.handleContext(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Result().StatusCode)
	}

	var errResp errorResponse
	json.NewDecoder(w.Result().Body).Decode(&errResp)
	if errResp.Error.Code != "ERR_INVALID_PARAMETER" {
		t.Errorf("expected ERR_INVALID_PARAMETER, got %s", errResp.Error.Code)
	}
}

func TestContextEndpoint_Success(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/context?symbol=com.example.MyClass", nil)
	w := httptest.NewRecorder()
	srv.handleContext(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Result().StatusCode)
	}

	var body map[string]any
	json.NewDecoder(w.Result().Body).Decode(&body)
	if body["symbol"] != "com.example.MyClass" {
		t.Errorf("expected symbol, got %v", body["symbol"])
	}
}

func TestImpactEndpoint_EmptyMethod(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/impact", nil)
	w := httptest.NewRecorder()
	srv.handleImpact(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestImpactEndpoint_Success(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/impact?method=com.example.MyClass.myMethod", nil)
	w := httptest.NewRecorder()
	srv.handleImpact(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Result().StatusCode)
	}

	var body map[string]any
	json.NewDecoder(w.Result().Body).Decode(&body)
	if body["method"] != "com.example.MyClass.myMethod" {
		t.Errorf("expected method, got %v", body["method"])
	}
}

func TestTestsEndpoint_EmptyMethod(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/tests", nil)
	w := httptest.NewRecorder()
	srv.handleTests(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestTestsEndpoint_Success(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/tests?method=com.example.MyClass.myMethod", nil)
	w := httptest.NewRecorder()
	srv.handleTests(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Result().StatusCode)
	}
}

func TestSearchEndpoint_EmptyQuery(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/search", nil)
	w := httptest.NewRecorder()
	srv.handleSearch(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestSearchEndpoint_Success(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/search?q=MyClass", nil)
	w := httptest.NewRecorder()
	srv.handleSearch(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Result().StatusCode)
	}
}

func TestCORSMiddleware(t *testing.T) {
	srv := newTestHTTPServer(t)
	handler := srv.corsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	// Test preflight
	req := httptest.NewRequest(http.MethodOptions, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusNoContent {
		t.Errorf("expected 204 for OPTIONS, got %d", w.Result().StatusCode)
	}
	if w.Header().Get("Access-Control-Allow-Origin") != "*" {
		t.Error("expected CORS header")
	}
}

func TestErrorRecoveryMiddleware(t *testing.T) {
	srv := newTestHTTPServer(t)
	handler := srv.errorRecoveryMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		panic("test panic")
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusInternalServerError {
		t.Errorf("expected 500, got %d", w.Result().StatusCode)
	}
}

// ---- P6 可观测性端点测试 ----

func TestHealthDBEndpoint(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/health/db", nil)
	w := httptest.NewRecorder()
	srv.handleHealthDB(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}

	var body map[string]any
	json.NewDecoder(resp.Body).Decode(&body)
	if body["status"] != "ok" {
		t.Errorf("expected status ok, got %v", body["status"])
	}
	if _, ok := body["latency_ms"]; !ok {
		t.Error("expected latency_ms field")
	}
}

func TestHealthKVEndpoint(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/health/kv", nil)
	w := httptest.NewRecorder()
	srv.handleHealthKV(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestHealthVectorEndpoint(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/health/vector", nil)
	w := httptest.NewRecorder()
	srv.handleHealthVector(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
}

func TestMetricsEndpoint(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/metrics", nil)
	w := httptest.NewRecorder()
	srv.handleMetrics(w, req)

	resp := w.Result()
	if resp.StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", resp.StatusCode)
	}
	if ctype := resp.Header.Get("Content-Type"); ctype != "text/plain; version=0.0.4" {
		t.Errorf("expected Content-Type 'text/plain; version=0.0.4', got %s", ctype)
	}
}

// ---- 安全中间件测试 ----

func TestAuthMiddleware_NoToken(t *testing.T) {
	srv := newTestHTTPServer(t)
	srv.SetAuthToken("secret123")

	handler := srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Result().StatusCode)
	}
}

func TestAuthMiddleware_ValidToken(t *testing.T) {
	srv := newTestHTTPServer(t)
	srv.SetAuthToken("secret123")

	handler := srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Authorization", "Bearer secret123")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Result().StatusCode)
	}
}

func TestAuthMiddleware_EmptyToken(t *testing.T) {
	srv := newTestHTTPServer(t)
	// authToken 为空时，不验证

	handler := srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Result().StatusCode)
	}
}

func TestAuthMiddleware_WrongToken(t *testing.T) {
	srv := newTestHTTPServer(t)
	srv.SetAuthToken("secret123")

	handler := srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/health", nil)
	req.Header.Set("Authorization", "Bearer wrongtoken")
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusUnauthorized {
		t.Errorf("expected 401, got %d", w.Result().StatusCode)
	}
}

func TestPathTraversalMiddleware_Valid(t *testing.T) {
	srv := newTestHTTPServer(t)
	handler := srv.pathTraversalMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/context?symbol=com.example.MyClass", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Errorf("expected 200, got %d", w.Result().StatusCode)
	}
}

func TestPathTraversalMiddleware_Blocked(t *testing.T) {
	srv := newTestHTTPServer(t)
	handler := srv.pathTraversalMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/context?path=../../../etc/passwd", nil)
	w := httptest.NewRecorder()
	handler.ServeHTTP(w, req)

	if w.Result().StatusCode != http.StatusBadRequest {
		t.Errorf("expected 400, got %d", w.Result().StatusCode)
	}
}

func TestTokenBucketAllowsBurstThenLimits(t *testing.T) {
	// 容量=3，速率=3/秒。初始即可突发消费 3 次，随后需按速率补充。
	b := newTokenBucket(3, 3)
	// 桶满：连续 3 次应全部放行。
	for i := 0; i < 3; i++ {
		if !b.allow() {
			t.Fatalf("request %d: expected allowed within burst capacity", i)
		}
	}
	// 第 4 次（几乎同一时刻）应被拒绝。
	if b.allow() {
		t.Fatal("expected 4th request to be rejected after burst exhausted")
	}
}

func TestTokenBucketRefillsOverTime(t *testing.T) {
	b := newTokenBucket(1, 10) // 容量 1，速率 10/秒
	if !b.allow() {
		t.Fatal("first request should be allowed")
	}
	if b.allow() {
		t.Fatal("second request should be rejected immediately")
	}
	// 等待约 100ms，速率 10/秒 => 补充约 1 枚令牌。
	time.Sleep(120 * time.Millisecond)
	if !b.allow() {
		t.Fatal("request after refill should be allowed")
	}
}

func TestRateLimitMiddleware_Disabled(t *testing.T) {
	srv := newTestHTTPServer(t) // rateLimiter 默认 nil = 不限流
	hit := false
	handler := srv.rateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hit = true
		w.WriteHeader(http.StatusOK)
	}))
	for i := 0; i < 5; i++ {
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
		if w.Result().StatusCode != http.StatusOK {
			t.Fatalf("request %d: expected 200 (unlimited), got %d", i, w.Result().StatusCode)
		}
	}
	if !hit {
		t.Fatal("expected downstream handler to be reached")
	}
}

func TestRateLimitMiddleware_RejectsOverflow(t *testing.T) {
	srv := newTestHTTPServer(t)
	srv.SetRateLimit(2) // 每分钟 2 个请求
	ok := 0
	limited := 0
	for i := 0; i < 6; i++ {
		w := httptest.NewRecorder()
		srv.rateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
		switch w.Result().StatusCode {
		case http.StatusOK:
			ok++
		case http.StatusTooManyRequests:
			limited++
		default:
			t.Fatalf("request %d: unexpected status %d", i, w.Result().StatusCode)
		}
	}
	if ok != 2 {
		t.Errorf("expected 2 allowed, got %d", ok)
	}
	if limited == 0 {
		t.Errorf("expected some requests to be rate-limited")
	}
}

func TestTestMethodNotAllowedOnAllEndpoints(t *testing.T) {
	srv := newTestHTTPServer(t)
	endpoints := []struct {
		name    string
		handler func(http.ResponseWriter, *http.Request)
	}{
		{"health", srv.handleHealth},
		{"healthDB", srv.handleHealthDB},
		{"healthKV", srv.handleHealthKV},
		{"healthVector", srv.handleHealthVector},
		{"context", srv.handleContext},
		{"impact", srv.handleImpact},
		{"tests", srv.handleTests},
		{"search", srv.handleSearch},
		{"tags", srv.handleGetTags},
		{"tagsSearch", srv.handleSearchByTag},
		{"tagsAll", srv.handleGetAllTags},
		{"metrics", srv.handleMetrics},
	}

	for _, ep := range endpoints {
		req := httptest.NewRequest(http.MethodPost, "/"+ep.name, nil)
		w := httptest.NewRecorder()
		ep.handler(w, req)

		if w.Result().StatusCode != http.StatusMethodNotAllowed {
			t.Errorf("%s: expected 405 for POST, got %d", ep.name, w.Result().StatusCode)
		}
	}
}