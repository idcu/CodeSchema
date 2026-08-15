package errors

import (
	"errors"
	"fmt"
	"testing"
)

// allSentinelErrors 全部 sentinel 错误，供各测试复用。
func allSentinelErrors() []error {
	return []error{
		ErrNoAdapter, ErrParseFailed, ErrSourceUnavailable, ErrParseTimeout,
		ErrBudgetExceeded, ErrLLMUnavailable, ErrEnhanceFailed,
		ErrTxFailed, ErrKVWriteFailed, ErrVectorBuildFailed,
		ErrFileNotFound, ErrInvalidConfig,
	}
}

// TestSentinelErrors_NonNil 验证所有 sentinel error 非 nil 且 Error() 非空。
func TestSentinelErrors_NonNil(t *testing.T) {
	for _, e := range allSentinelErrors() {
		if e == nil {
			t.Error("sentinel error should not be nil")
			continue
		}
		if e.Error() == "" {
			t.Errorf("sentinel error has empty Error(): %v", e)
		}
	}
}

// TestSentinelErrors_Unique 验证 sentinel 消息两两不重复（避免误判）。
func TestSentinelErrors_Unique(t *testing.T) {
	seen := map[string]bool{}
	for _, e := range allSentinelErrors() {
		if seen[e.Error()] {
			t.Errorf("duplicate sentinel message: %q", e.Error())
		}
		seen[e.Error()] = true
	}
}

// TestErrorsIs_Wrapped 验证 fmt.Errorf %w 包装后 errors.Is 能匹配 sentinel。
func TestErrorsIs_Wrapped(t *testing.T) {
	got := fmt.Errorf("parse file x.go: %w", ErrParseFailed)
	if !errors.Is(got, ErrParseFailed) {
		t.Error("errors.Is should match wrapped sentinel")
	}
	if errors.Is(got, ErrSourceUnavailable) {
		t.Error("errors.Is should NOT match unrelated sentinel")
	}
}

// TestErrorsIs_Direct 验证 errors.Is 对裸 sentinel 直接匹配。
func TestErrorsIs_Direct(t *testing.T) {
	if !errors.Is(ErrTxFailed, ErrTxFailed) {
		t.Error("errors.Is should match the same sentinel")
	}
	if errors.Is(ErrTxFailed, ErrKVWriteFailed) {
		t.Error("errors.Is should NOT match a different sentinel")
	}
}
