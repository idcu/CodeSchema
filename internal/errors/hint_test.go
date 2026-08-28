package errors

import (
	"strings"
	"testing"
)

func TestHintKnownCodes(t *testing.T) {
	cases := []string{
		"ERR_SYMBOL_NOT_FOUND",
		"ERR_INVALID_PARAMETER",
		"ERR_RATE_LIMITED",
		"ERR_UNAUTHORIZED",
		"ERR_METHOD_NOT_ALLOWED",
		"ERR_INTERNAL",
	}
	for _, code := range cases {
		got := Hint(code)
		if got == "" {
			t.Errorf("Hint(%q) 不应为空", code)
			continue
		}
		if len([]rune(got)) > 200 {
			t.Errorf("Hint(%q) 过长（%d 字）：hint 是给 Agent 的一句话，不是文档", code, len([]rune(got)))
		}
	}
}

func TestHintUnknownCode(t *testing.T) {
	if got := Hint("ERR_NOT_A_REAL_CODE"); got != "" {
		t.Errorf("未知错误码应返回空串, got %q", got)
	}
}

func TestWithHint(t *testing.T) {
	// 已知错误码：hint 以 [hint] 起头拼在原文之后（MCP 协议只有一个 message 字段）。
	got := WithHint("ERR_SYMBOL_NOT_FOUND", "symbol not found: x")
	if !strings.HasPrefix(got, "symbol not found: x\n[hint] ") {
		t.Errorf("WithHint 格式不对: %q", got)
	}
	// 未知错误码：原样返回，不硬塞空信息。
	if plain := WithHint("ERR_UNKNOWN", "boom"); plain != "boom" {
		t.Errorf("未知码应原样返回, got %q", plain)
	}
}

func TestHintMentionsBatchForRateLimit(t *testing.T) {
	// 限流的修复建议里必须提示批量入参（这是降低调用次数最直接的手段）。
	if got := Hint("ERR_RATE_LIMITED"); !strings.Contains(got, "symbols[]") {
		t.Errorf("限流 hint 应提示批量入参, got %q", got)
	}
}
