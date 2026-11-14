package server

import (
	"bufio"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// TestMCPServer_StopClosesActiveSSE 负控（P3-4）：
// Stop 必须先结束活跃 SSE 会话再 Shutdown，否则长连接会让关闭固定等到 5s 超时。
func TestMCPServer_StopClosesActiveSSE(t *testing.T) {
	m := NewMCPServer(nil, "")
	ts := httptest.NewServer(http.HandlerFunc(m.handleSSE))
	defer ts.Close()
	m.server = ts.Config

	resp, err := http.Get(ts.URL) //nolint:noctx // 测试内短连接
	if err != nil {
		t.Fatalf("GET /sse: %v", err)
	}
	defer resp.Body.Close()
	line, err := bufio.NewReader(resp.Body).ReadString('\n')
	if err != nil || !strings.Contains(line, "endpoint") {
		t.Fatalf("未收到 endpoint 事件: line=%q err=%v", line, err)
	}

	done := make(chan error, 1)
	go func() { done <- m.Stop() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("Stop: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("Stop 阻塞超过 2s：活跃 SSE 连接未被主动关闭（P3-4）")
	}
}
