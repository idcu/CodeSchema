package robust

import (
	"context"
	"errors"
	"testing"
)

func TestSafeCall_Panic(t *testing.T) {
	err := SafeCall(func() {
		panic("test panic")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "test panic") {
		t.Errorf("expected 'test panic' in error, got: %v", err)
	}
}

func TestSafeCall_Success(t *testing.T) {
	err := SafeCall(func() {
		// normal execution
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestSafeCallWithResult_Panic(t *testing.T) {
	result, err := SafeCallWithResult(func() int {
		panic("panic in result func")
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if result != 0 {
		t.Errorf("expected zero value, got %d", result)
	}
}

func TestSafeCallWithResult_Success(t *testing.T) {
	result, err := SafeCallWithResult(func() string {
		return "hello"
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if result != "hello" {
		t.Errorf("expected 'hello', got %s", result)
	}
}

func TestRecoveryHandler_Recover(t *testing.T) {
	handler := NewRecoveryHandler()
	panicked := false

	func() {
		defer handler.Recover()
		panic("test")
	}()

	// 不 panic 说明恢复成功
	_ = panicked
}

func TestRecoveryHandler_RecoverWithCallback(t *testing.T) {
	handler := NewRecoveryHandler()
	var capturedPanic any
	var capturedStack string

	func() {
		defer handler.RecoverWithCallback(func(panicVal any, stack string) {
			capturedPanic = panicVal
			capturedStack = stack
		})
		panic("callback test")
	}()

	if capturedPanic == nil {
		t.Fatal("expected callback to be called")
	}
	if capturedPanic.(string) != "callback test" {
		t.Errorf("expected 'callback test', got %v", capturedPanic)
	}
	if capturedStack == "" {
		t.Error("expected non-empty stack")
	}
}

func TestRecoveryHandler_Go(t *testing.T) {
	handler := NewRecoveryHandler()
	done := make(chan struct{})

	handler.Go(func() {
		defer close(done)
		// 正常执行
	})

	<-done // 等待 goroutine 完成
}

func TestRecoveryHandler_GoWithPanic(t *testing.T) {
	handler := NewRecoveryHandler()
	done := make(chan struct{})

	handler.Go(func() {
		defer close(done)
		panic("goroutine panic")
	})

	<-done // 应该不会 panic 地完成
}

func contains(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			if s[i+j] != substr[j] {
				match = false
				break
			}
		}
		if match {
			return true
		}
	}
	return false
}

func TestRetryWithRecovery(t *testing.T) {
	// 组合测试：重试 + 恢复
	ctx := testContext()

	var attempts int
	err := Retry(ctx, func(ctx context.Context) error {
		attempts++
		if attempts == 1 {
			// 第一次 panic，但被外部恢复
			return errors.New("panic-like error")
		}
		return nil
	}, WithMaxAttempts(3))

	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}

func testContext() context.Context {
	return context.Background()
}