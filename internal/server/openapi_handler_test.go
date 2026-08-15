package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// TestHandleOpenAPI 验证 /openapi.json 返回合法 OpenAPI 3.0 规范。
func TestHandleOpenAPI(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/openapi.json", nil)
	rec := httptest.NewRecorder()
	handleOpenAPI(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	ct := rec.Header().Get("Content-Type")
	if !strings.Contains(ct, "application/json") {
		t.Fatalf("content-type = %s, want application/json", ct)
	}
	var spec map[string]any
	if err := json.Unmarshal(rec.Body.Bytes(), &spec); err != nil {
		t.Fatalf("invalid JSON: %v", err)
	}
	if spec["openapi"] != "3.0.3" {
		t.Fatalf("openapi version = %v, want 3.0.3", spec["openapi"])
	}
	paths, ok := spec["paths"].(map[string]any)
	if !ok {
		t.Fatal("missing paths")
	}
	// 应覆盖全部核心端点
	for _, p := range []string{"/health", "/context", "/impact", "/tests", "/search", "/tags", "/tags/search", "/tags/all", "/metrics"} {
		if _, ok := paths[p]; !ok {
			t.Fatalf("missing path %s in openapi spec", p)
		}
	}
}

// TestHandleAPIDocs 验证 /docs 返回 swagger-ui HTML。
func TestHandleAPIDocs(t *testing.T) {
	req := httptest.NewRequest(http.MethodGet, "/docs", nil)
	rec := httptest.NewRecorder()
	handleAPIDocs(rec, req)

	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rec.Code)
	}
	body := rec.Body.String()
	if !strings.Contains(body, "swagger-ui") || !strings.Contains(body, "openapi.json") {
		t.Fatalf("docs page missing swagger-ui/openapi.json references")
	}
}
