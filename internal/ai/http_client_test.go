package ai

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// chatCompletionsHandler 返回一个伪造 OpenAI Chat Completions 的 handler。
func chatCompletionsHandler(t *testing.T, content string) http.HandlerFunc {
	t.Helper()
	return func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Errorf("unexpected path %s", r.URL.Path)
		}
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Errorf("expected bearer token, got %q", r.Header.Get("Authorization"))
		}
		var req chatRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Errorf("decode request: %v", err)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(chatResponse{
			Choices: []struct {
				Message chatMessage `json:"message"`
			}{{Message: chatMessage{Role: "assistant", Content: content}}},
		})
	}
}

func TestNewOpenAICompatClient_Disabled(t *testing.T) {
	// 缺任一关键配置即返回 nil（AI 增强禁用，主流程零影响）
	if c := NewOpenAICompatClient(HTTPClientConfig{BaseURL: "http://x", APIKey: "k"}); c != nil {
		t.Fatal("expected nil client when model empty")
	}
	if c := NewOpenAICompatClient(HTTPClientConfig{BaseURL: "http://x", Model: "m"}); c != nil {
		t.Fatal("expected nil client when api key empty")
	}
	if c := NewOpenAICompatClient(HTTPClientConfig{APIKey: "k", Model: "m"}); c != nil {
		t.Fatal("expected nil client when base url empty")
	}
}

func TestOpenAICompatClient_Complete(t *testing.T) {
	srv := httptest.NewServer(chatCompletionsHandler(t, "biz:payment\ntech:cache\n- risk:todo"))
	defer srv.Close()

	c := NewOpenAICompatClient(HTTPClientConfig{BaseURL: srv.URL, APIKey: "test-key", Model: "gpt-4o-mini"})
	if c == nil {
		t.Fatal("expected non-nil client")
	}

	lines, err := c.Complete(context.Background(), "为实体推导标签")
	if err != nil {
		t.Fatalf("Complete: %v", err)
	}
	if len(lines) != 3 {
		t.Fatalf("expected 3 lines, got %v", lines)
	}
	if lines[0] != "biz:payment" {
		t.Errorf("expected first line biz:payment, got %q", lines[0])
	}
	// 行内 "- " 前缀应被去除
	if lines[2] != "risk:todo" {
		t.Errorf("expected risk:todo, got %q", lines[2])
	}
}

func TestOpenAICompatClient_Choose(t *testing.T) {
	srv := httptest.NewServer(chatCompletionsHandler(t, "[2]"))
	defer srv.Close()

	c := NewOpenAICompatClient(HTTPClientConfig{BaseURL: srv.URL, APIKey: "test-key", Model: "gpt-4o-mini"})
	idx, err := c.Choose(context.Background(), "选择最佳索引")
	if err != nil {
		t.Fatalf("Choose: %v", err)
	}
	if idx != 2 {
		t.Errorf("expected index 2, got %d", idx)
	}
}

func TestOpenAICompatClient_Choose_ParseVariants(t *testing.T) {
	// 容忍 "索引: 3" / "3。" 等格式
	for _, content := range []string{"索引: 3", "3。", "（1）"} {
		srv := httptest.NewServer(chatCompletionsHandler(t, content))
		c := NewOpenAICompatClient(HTTPClientConfig{BaseURL: srv.URL, APIKey: "test-key", Model: "m"})
		idx, err := c.Choose(context.Background(), "选择")
		srv.Close()
		if err != nil {
			t.Fatalf("Choose(%q): %v", content, err)
		}
		if idx != 3 && idx != 1 {
			t.Errorf("Choose(%q) = %d, want 3 or 1", content, idx)
		}
	}
}

func TestOpenAICompatClient_Non200(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error": {"message": "invalid api key"}}`))
	}))
	defer srv.Close()

	c := NewOpenAICompatClient(HTTPClientConfig{BaseURL: srv.URL, APIKey: "bad", Model: "m"})
	_, err := c.Complete(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error on non-200")
	}
	if !strings.Contains(err.Error(), "invalid api key") {
		t.Errorf("expected upstream error message in err, got %v", err)
	}
}

func TestOpenAICompatClient_NetworkError(t *testing.T) {
	c := NewOpenAICompatClient(HTTPClientConfig{BaseURL: "http://127.0.0.1:1", APIKey: "k", Model: "m"})
	_, err := c.Complete(context.Background(), "hi")
	if err == nil {
		t.Fatal("expected error on network failure")
	}
}
