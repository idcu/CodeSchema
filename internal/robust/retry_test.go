package robust

import (
	"context"
	"errors"
	"math"
	"testing"
	"time"
)

func TestRetry_Success(t *testing.T) {
	ctx := context.Background()
	var attempts int
	err := Retry(ctx, func(ctx context.Context) error {
		attempts++
		return nil
	})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt, got %d", attempts)
	}
}

func TestRetry_RetryThenSuccess(t *testing.T) {
	ctx := context.Background()
	var attempts int
	err := Retry(ctx, func(ctx context.Context) error {
		attempts++
		if attempts < 3 {
			return errors.New("temporary error")
		}
		return nil
	}, WithMaxAttempts(5))
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if attempts != 3 {
		t.Errorf("expected 3 attempts, got %d", attempts)
	}
}

func TestRetry_Exhausted(t *testing.T) {
	ctx := context.Background()
	err := Retry(ctx, func(ctx context.Context) error {
		return errors.New("persistent error")
	}, WithMaxAttempts(3))
	if err == nil {
		t.Fatal("expected error")
	}
	if !contains(err.Error(), "retry exhausted") {
		t.Errorf("expected 'retry exhausted' in error, got: %v", err)
	}
}

func TestRetry_ContextCanceled(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	err := Retry(ctx, func(ctx context.Context) error {
		return errors.New("should not be called")
	}, WithMaxAttempts(5))
	if err != context.Canceled {
		t.Errorf("expected context.Canceled, got: %v", err)
	}
}

func TestRetry_NonRetryable(t *testing.T) {
	ctx := context.Background()
	var attempts int
	err := Retry(ctx, func(ctx context.Context) error {
		attempts++
		return errors.New("not found: missing resource")
	}, WithMaxAttempts(5), WithRetryable(RetryableError))
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 1 {
		t.Errorf("expected 1 attempt (non-retryable), got %d", attempts)
	}
}

func TestRetryableError_NotRetryable(t *testing.T) {
	cases := []string{
		"not found",
		"file not exist",
		"invalid parameter",
		"permission denied",
		"unauthenticated access",
		"unsupported operation",
	}
	for _, c := range cases {
		if RetryableError(errors.New(c)) {
			t.Errorf("expected non-retryable for: %s", c)
		}
	}
}

func TestRetryableError_Retryable(t *testing.T) {
	cases := []string{
		"connection timeout",
		"i/o timeout",
		"resource temporarily unavailable",
		"broken pipe",
	}
	for _, c := range cases {
		if !RetryableError(errors.New(c)) {
			t.Errorf("expected retryable for: %s", c)
		}
	}
}

func TestCalcBackoff_Exponential(t *testing.T) {
	base := 100 * time.Millisecond
	max := 5 * time.Second

	// 不使用抖动
	backoff1 := calcBackoff(1, base, max, 0)
	expected1 := float64(base) * math.Pow(2, 0)
	if backoff1 != time.Duration(expected1) {
		t.Errorf("expected %v for attempt 1, got %v", time.Duration(expected1), backoff1)
	}

	backoff2 := calcBackoff(2, base, max, 0)
	expected2 := float64(base) * math.Pow(2, 1)
	if backoff2 != time.Duration(expected2) {
		t.Errorf("expected %v for attempt 2, got %v", time.Duration(expected2), backoff2)
	}

	backoff3 := calcBackoff(3, base, max, 0)
	expected3 := float64(base) * math.Pow(2, 2)
	if backoff3 != time.Duration(expected3) {
		t.Errorf("expected %v for attempt 3, got %v", time.Duration(expected3), backoff3)
	}
}

func TestCalcBackoff_MaxDelay(t *testing.T) {
	base := 1 * time.Second
	max := 2 * time.Second

	// 第 3 次尝试: 1 * 2^2 = 4s，但上限 2s
	backoff := calcBackoff(3, base, max, 0)
	if backoff > max {
		t.Errorf("expected backoff <= %v, got %v", max, backoff)
	}
}

func TestCalcBackoff_Jitter(t *testing.T) {
	base := 100 * time.Millisecond
	max := 5 * time.Second

	// 多次调用，确保有抖动变化
	values := make([]time.Duration, 10)
	for i := range values {
		values[i] = calcBackoff(3, base, max, 0.5)
	}

	// 应该至少有一次不同
	allSame := true
	for i := 1; i < len(values); i++ {
		if values[i] != values[0] {
			allSame = false
			break
		}
	}
	if allSame {
		t.Error("expected jitter to produce different values")
	}
}

func TestRetry_WithOptions(t *testing.T) {
	ctx := context.Background()
	var attempts int
	err := Retry(ctx, func(ctx context.Context) error {
		attempts++
		return errors.New("error")
	},
		WithMaxAttempts(2),
		WithBaseDelay(10*time.Millisecond),
		WithMaxDelay(100*time.Millisecond),
		WithJitter(0.05),
	)
	if err == nil {
		t.Fatal("expected error")
	}
	if attempts != 2 {
		t.Errorf("expected 2 attempts, got %d", attempts)
	}
}