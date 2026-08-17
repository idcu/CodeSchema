package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
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

// TestParseQueryTags 验证多标签 query 参数解析。
func TestParseQueryTags(t *testing.T) {
	tests := []struct {
		name string
		raw  string
		want []string
	}{
		{"single tag", "tag=controller", []string{"controller"}},
		{"comma separated", "tag=controller,service", []string{"controller", "service"}},
		{"three tags", "tag=cache,read,write", []string{"cache", "read", "write"}},
		{"repeated params", "tag=controller&tag=service", []string{"controller", "service"}},
		{"mixed", "tag=controller,service&tag=cache", []string{"controller", "service", "cache"}},
		{"empty", "", []string{""}},
		{"whitespace trimmed", "tag= controller , service ", []string{"controller", "service"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			q, err := url.ParseQuery(tt.raw)
			if err != nil {
				t.Fatalf("parse query: %v", err)
			}
			got := parseQueryTags(q)
			if len(got) != len(tt.want) {
				t.Fatalf("len: got %v, want %v", got, tt.want)
			}
			for i := range got {
				if got[i] != tt.want[i] {
					t.Fatalf("[%d]: got %q, want %q", i, got[i], tt.want[i])
				}
			}
		})
	}
}

// TestTagsSearchEndpoint_MultiTag 验证 HTTP 端点多标签 AND 查询。
func TestTagsSearchEndpoint_MultiTag(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore("file")
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	seedSymbol(t, st)
	srv := NewHTTPServer(service.NewService(st), ":0")

	// 为 seed 类打标签
	ctx := context.Background()
	files, _ := st.GetAllFiles(ctx)
	if len(files) == 0 {
		t.Fatal("no seed file")
	}
	classes, _ := st.GetClassesByFileID(ctx, files[0].ID)
	if len(classes) == 0 {
		t.Fatal("no seed class")
	}
	if err := st.UpsertTags(ctx, classes[0].ID, []string{"controller", "service"}); err != nil {
		t.Fatal(err)
	}

	// 单标签 GET /tags/search?tag=controller
	req := httptest.NewRequest(http.MethodGet, "/tags/search?tag=controller", nil)
	w := httptest.NewRecorder()
	srv.handleSearchByTag(w, req)
	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}
	var res service.TagSearchResult
	if err := json.NewDecoder(w.Result().Body).Decode(&res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Classes) != 1 {
		t.Fatalf("single tag: expected 1 class, got %v", res.Classes)
	}

	// 多标签 GET /tags/search?tag=controller,service
	req2 := httptest.NewRequest(http.MethodGet, "/tags/search?tag=controller,service", nil)
	w2 := httptest.NewRecorder()
	srv.handleSearchByTag(w2, req2)
	if w2.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", w2.Result().StatusCode)
	}
	var res2 service.TagSearchResult
	if err := json.NewDecoder(w2.Result().Body).Decode(&res2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res2.Classes) != 1 {
		t.Fatalf("multi tag: expected 1 class, got %v", res2.Classes)
	}

	// 不存在的组合 → 空
	req3 := httptest.NewRequest(http.MethodGet, "/tags/search?tag=controller,mq", nil)
	w3 := httptest.NewRecorder()
	srv.handleSearchByTag(w3, req3)
	var res3 service.TagSearchResult
	json.NewDecoder(w3.Result().Body).Decode(&res3)
	if len(res3.Classes) != 0 {
		t.Fatalf("no match: expected 0 classes, got %v", res3.Classes)
	}
}

// ---- 全局能力热重载测试（UpdateRuntime，无需重启进程） ----

func TestHTTPServer_UpdateRuntime_AuthToken(t *testing.T) {
	srv := newTestHTTPServer(t) // 初始无认证

	do := func(token string) int {
		w := httptest.NewRecorder()
		req := httptest.NewRequest(http.MethodGet, "/health", nil)
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		srv.authMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, req)
		return w.Result().StatusCode
	}

	// 初始：无认证放行。
	if code := do(""); code != http.StatusOK {
		t.Fatalf("initial: expected 200, got %d", code)
	}
	// 热更新启用认证：无 token 401，正确 token 200。
	if err := srv.UpdateRuntime("", "hot-token", 0); err != nil {
		t.Fatalf("enable auth: %v", err)
	}
	if code := do(""); code != http.StatusUnauthorized {
		t.Fatalf("no token after enable: expected 401, got %d", code)
	}
	if code := do("hot-token"); code != http.StatusOK {
		t.Fatalf("valid token: expected 200, got %d", code)
	}
	// 热更新轮换令牌：旧令牌失效，新令牌生效。
	if err := srv.UpdateRuntime("", "new-token", 0); err != nil {
		t.Fatalf("rotate token: %v", err)
	}
	if code := do("hot-token"); code != http.StatusUnauthorized {
		t.Fatalf("old token after rotate: expected 401, got %d", code)
	}
	if code := do("new-token"); code != http.StatusOK {
		t.Fatalf("new token: expected 200, got %d", code)
	}
	// 热更新关闭认证（空串）：无 token 放行。
	if err := srv.UpdateRuntime("", "", 0); err != nil {
		t.Fatalf("disable auth: %v", err)
	}
	if code := do(""); code != http.StatusOK {
		t.Fatalf("no token after disable: expected 200, got %d", code)
	}
}

func TestHTTPServer_UpdateRuntime_RateLimit(t *testing.T) {
	srv := newTestHTTPServer(t)

	do := func() int {
		w := httptest.NewRecorder()
		srv.rateLimitMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.WriteHeader(http.StatusOK)
		})).ServeHTTP(w, httptest.NewRequest(http.MethodGet, "/health", nil))
		return w.Result().StatusCode
	}

	// 初始：不限流。
	if code := do(); code != http.StatusOK {
		t.Fatalf("initial: expected 200, got %d", code)
	}
	// 热更新启用 rpm=1：首个放行（突发=上限），随后 429。
	if err := srv.UpdateRuntime("", "", 1); err != nil {
		t.Fatalf("enable rate limit: %v", err)
	}
	if code := do(); code != http.StatusOK {
		t.Fatalf("first after enable: expected 200, got %d", code)
	}
	if code := do(); code != http.StatusTooManyRequests {
		t.Fatalf("second after enable: expected 429, got %d", code)
	}
	// 热更新关闭（rpm=0）：恢复不限流。
	if err := srv.UpdateRuntime("", "", 0); err != nil {
		t.Fatalf("disable rate limit: %v", err)
	}
	for i := 0; i < 3; i++ {
		if code := do(); code != http.StatusOK {
			t.Fatalf("request %d after disable: expected 200, got %d", i, code)
		}
	}
}

func TestHTTPServer_UpdateRuntime_PreserveFields(t *testing.T) {
	// 单字段 setter（SetAuthToken/SetRateLimit）必须保留另一字段的当前值，
	// 与配置热重载"整体替换快照"语义互补（preset 变更连带场景）。
	srv := newTestHTTPServer(t)
	srv.SetAuthToken("tok") // UpdateRuntime(addr, "tok", 0)
	srv.SetRateLimit(5)     // UpdateRuntime(addr, "tok", 5)
	if got := srv.currentAuthToken(); got != "tok" {
		t.Fatalf("authToken: got %q, want %q", got, "tok")
	}
	if got := srv.currentRateLimit(); got != 5 {
		t.Fatalf("rateLimit: got %d, want 5", got)
	}

	// 仅更新限流：认证令牌保持。
	srv.SetRateLimit(0)
	if got := srv.currentAuthToken(); got != "tok" {
		t.Fatalf("authToken after SetRateLimit: got %q, want %q", got, "tok")
	}
	if got := srv.currentRateLimit(); got != 0 {
		t.Fatalf("rateLimit after SetRateLimit(0): got %d, want 0", got)
	}

	// 仅更新认证：限流保持。
	srv.SetAuthToken("tok2")
	if got := srv.currentRateLimit(); got != 0 {
		t.Fatalf("rateLimit after SetAuthToken: got %d, want 0", got)
	}
	if got := srv.currentAuthToken(); got != "tok2" {
		t.Fatalf("authToken: got %q, want %q", got, "tok2")
	}
}

// waitForListener 等待 HTTP 服务器开始监听（Start 异步 rebind 完成）。
func waitForListener(t *testing.T, srv *HTTPServer) {
	t.Helper()
	for i := 0; i < 200; i++ {
		srv.lnMu.Lock()
		ln := srv.ln
		srv.lnMu.Unlock()
		if ln != nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("server listener did not become ready")
}

func TestHTTPServer_UpdateRuntime_AddrRebind(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore("file")
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	srv := NewHTTPServer(service.NewService(st), "127.0.0.1:0")

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { done <- srv.Start(ctx) }()

	waitForListener(t, srv)
	oldAddr := srv.ln.Addr().String()

	// 热重绑到新端口（无需重启进程）：地址字符串变化即触发重绑，
	// 旧监听仍在占用原端口，新监听必然分配到不同端口。
	if err := srv.UpdateRuntime("localhost:0", "", 0); err != nil {
		t.Fatalf("UpdateRuntime rebind: %v", err)
	}
	newAddr := srv.ln.Addr().String()
	if newAddr == oldAddr {
		t.Fatal("expected listener to rebind to a new address")
	}

	// 新地址可服务请求。
	client := &http.Client{Timeout: 2 * time.Second}
	resp, err := client.Get("http://" + newAddr + "/health")
	if err != nil {
		t.Fatalf("request to new addr: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected 200 from new addr, got %d", resp.StatusCode)
	}

	// 旧地址应拒绝连接。
	if _, err := client.Get("http://" + oldAddr + "/health"); err == nil {
		t.Fatal("expected old addr to refuse connections after rebind")
	}

	cancel()
	select {
	case err := <-done:
		if err != nil && err != http.ErrServerClosed {
			t.Fatalf("server stop: %v", err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("server did not stop")
	}
}