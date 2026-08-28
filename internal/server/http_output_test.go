package server

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"gitee.com/idcu-go/pathsafe"

	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
)

// TestErrorResponseCarriesHint 错误响应必须带可操作建议（B3），未知错误码则不带。
func TestErrorResponseCarriesHint(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/context", nil)
	w := httptest.NewRecorder()
	srv.handleContext(w, req)

	var errResp errorResponse
	if err := json.NewDecoder(w.Result().Body).Decode(&errResp); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if errResp.Error.Code != "ERR_INVALID_PARAMETER" {
		t.Fatalf("code=%s", errResp.Error.Code)
	}
	if errResp.Error.Hint == "" {
		t.Fatal("错误响应应带 hint，让 Agent 一轮内自愈")
	}
	if !strings.Contains(errResp.Error.Hint, "symbols[]") {
		t.Fatalf("hint 应提示批量入参用法, 实际 %q", errResp.Error.Hint)
	}
}

// TestContextEndpointBatch symbols 传多个时走批量路径（B5），返回 results + errors。
func TestContextEndpointBatch(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/context?symbols=com.example.MyClass,com.example.Missing", nil)
	w := httptest.NewRecorder()
	srv.handleContext(w, req)

	if w.Result().StatusCode != http.StatusOK {
		t.Fatalf("expected 200, got %d", w.Result().StatusCode)
	}
	var body struct {
		Results []map[string]any `json:"results"`
		Errors  []struct {
			Symbol string `json:"symbol"`
			Code   string `json:"code"`
			Hint   string `json:"hint"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(w.Result().Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(body.Results) != 1 {
		t.Fatalf("results=%d, want 1（仅存在的符号）", len(body.Results))
	}
	if len(body.Errors) != 1 || body.Errors[0].Code != "ERR_SYMBOL_NOT_FOUND" {
		t.Fatalf("errors=%+v, want 1 条 ERR_SYMBOL_NOT_FOUND", body.Errors)
	}
	if body.Errors[0].Hint == "" {
		t.Fatal("批量失败明细应带 hint")
	}
}

// TestContextEndpointSingleUnchanged 单个符号仍返回原对象形状（向后兼容，不被包成 results）。
func TestContextEndpointSingleUnchanged(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/context?symbol=com.example.MyClass", nil)
	w := httptest.NewRecorder()
	srv.handleContext(w, req)

	var body map[string]any
	if err := json.NewDecoder(w.Result().Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["symbol"] != "com.example.MyClass" {
		t.Fatalf("单符号应保持原有响应形状, 实际 %v", body["symbol"])
	}
	if _, ok := body["results"]; ok {
		t.Fatal("单符号不应被包成 results")
	}
}

// TestContextEndpointBudget max_bytes 生效：输出被压到预算内并留痕降级原因（B1）。
func TestContextEndpointBudget(t *testing.T) {
	srv := newTestHTTPServer(t)
	req := httptest.NewRequest(http.MethodGet, "/context?symbol=com.example.MyClass&max_bytes=20", nil)
	w := httptest.NewRecorder()
	srv.handleContext(w, req)

	var body struct {
		Trace struct {
			Degraded      bool   `json:"degraded"`
			DegradeReason string `json:"degrade_reason"`
			ActualBytes   int    `json:"actual_bytes"`
			Config        struct {
				MaxBytes int `json:"max_bytes"`
			} `json:"config"`
		} `json:"_trace"`
	}
	if err := json.NewDecoder(w.Result().Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !body.Trace.Degraded {
		t.Fatal("max_bytes=20 应触发降级")
	}
	if body.Trace.ActualBytes > 20 {
		t.Fatalf("actual_bytes=%d 超出预算 20", body.Trace.ActualBytes)
	}
	if body.Trace.Config.MaxBytes != 20 {
		t.Fatalf("生效配置未回传 max_bytes: %+v", body.Trace.Config)
	}
	if body.Trace.DegradeReason == "" {
		t.Fatal("降级原因不应为空")
	}
}

// TestContextDefaultsApplied 服务端默认值只在请求未传参时补位（不覆盖显式传参）。
func TestContextDefaultsApplied(t *testing.T) {
	srv := newTestHTTPServer(t)
	srv.SetContextDefaults(service.ContextOptions{MaxBytes: 4096, PathStyle: service.PathVirtual})

	// 未传 max_bytes → 取默认值 4096
	req := httptest.NewRequest(http.MethodGet, "/context?symbol=com.example.MyClass", nil)
	w := httptest.NewRecorder()
	srv.handleContext(w, req)
	var body struct {
		Trace struct {
			Config struct {
				MaxBytes  int    `json:"max_bytes"`
				PathStyle string `json:"path_style"`
			} `json:"config"`
		} `json:"_trace"`
	}
	if err := json.NewDecoder(w.Result().Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body.Trace.Config.MaxBytes != 4096 {
		t.Fatalf("默认 max_bytes 未生效: %+v", body.Trace.Config)
	}
	if body.Trace.Config.PathStyle != "virtual" {
		t.Fatalf("默认 path_style 未生效: %+v", body.Trace.Config)
	}

	// 显式传 max_bytes=1024 → 不被默认值顶掉
	req2 := httptest.NewRequest(http.MethodGet, "/context?symbol=com.example.MyClass&max_bytes=1024", nil)
	w2 := httptest.NewRecorder()
	srv.handleContext(w2, req2)
	var body2 struct {
		Trace struct {
			Config struct {
				MaxBytes int `json:"max_bytes"`
			} `json:"config"`
		} `json:"_trace"`
	}
	if err := json.NewDecoder(w2.Result().Body).Decode(&body2); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body2.Trace.Config.MaxBytes != 1024 {
		t.Fatalf("请求参数应优先于默认值, 实际 %d", body2.Trace.Config.MaxBytes)
	}
}

// TestPathVirtualEndpoint path_style=virtual 时，FilePath 输出虚拟根前缀（B9）。
func TestPathVirtualEndpoint(t *testing.T) {
	// 自建 server 以拿到种子文件所在目录（newTestHTTPServer 内部的临时目录对外不可见）。
	dir := t.TempDir()
	st := store.NewStore("file")
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })
	seedPath := seedSymbol(t, st)
	srv := NewHTTPServer(service.NewService(st), ":0")

	root, err := pathsafe.NewRoot(filepath.Dir(seedPath), "/codebase")
	if err != nil {
		t.Fatalf("NewRoot: %v", err)
	}
	srv.service.WithPathRoot(root)

	req := httptest.NewRequest(http.MethodGet, "/context?symbol=com.example.MyClass&path_style=virtual", nil)
	w := httptest.NewRecorder()
	srv.handleContext(w, req)

	var body struct {
		FilePath string `json:"file_path"`
	}
	if err := json.NewDecoder(w.Result().Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if !strings.HasPrefix(body.FilePath, "/codebase/") {
		t.Fatalf("虚拟路径应以 /codebase/ 开头, 实际 %q", body.FilePath)
	}
}
