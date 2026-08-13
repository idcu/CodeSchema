package robust

import (
	"context"
	"fmt"
	"math"
	"math/rand"
	"time"
)

// RetryConfig 重试配置。
type RetryConfig struct {
	// MaxAttempts 最大重试次数（包括第一次尝试），默认 3。
	MaxAttempts int
	// BaseDelay 基础延迟，默认 100ms。
	BaseDelay time.Duration
	// MaxDelay 最大延迟，默认 5s。
	MaxDelay time.Duration
	// Jitter 抖动因子 [0.0 ~ 1.0]，默认 0.1。
	Jitter float64
	// Retryable 判断是否可重试的谓词，nil 表示所有错误都可重试。
	Retryable func(error) bool
}

// DefaultRetryConfig 默认重试配置。
var DefaultRetryConfig = RetryConfig{
	MaxAttempts: 3,
	BaseDelay:   100 * time.Millisecond,
	MaxDelay:    5 * time.Second,
	Jitter:      0.1,
	Retryable:   nil,
}

// Retry 执行带重试的操 maxAttempts。
//
// 使用指数退避 + 抖动，等效于 AWS SDK 的 StandardRetryMode。
// 可通过 context 控制超时，context 取消时立即返回。
func Retry(ctx context.Context, fn func(context.Context) error, opts ...RetryOption) error {
	cfg := DefaultRetryConfig
	for _, opt := range opts {
		opt(&cfg)
	}

	if cfg.MaxAttempts <= 0 {
		cfg.MaxAttempts = 1
	}
	if cfg.BaseDelay <= 0 {
		cfg.BaseDelay = 100 * time.Millisecond
	}
	if cfg.MaxDelay <= 0 {
		cfg.MaxDelay = 5 * time.Second
	}

	var lastErr error
	// 额外一次用于首次尝试
	for attempt := 0; attempt < cfg.MaxAttempts; attempt++ {
		// 检查 context 是否已取消
		select {
		case <-ctx.Done():
			return ctx.Err()
		default:
		}

		if attempt > 0 {
			// 计算延迟：指数退避 + 抖动
			delay := calcBackoff(attempt, cfg.BaseDelay, cfg.MaxDelay, cfg.Jitter)
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(delay):
			}
		}

		if err := fn(ctx); err != nil {
			lastErr = err
			// 检查是否可重试
			if cfg.Retryable != nil && !cfg.Retryable(err) {
				return err
			}
			continue
		}

		// 成功
		return nil
	}

	return fmt.Errorf("retry exhausted after %d attempts: %w", cfg.MaxAttempts, lastErr)
}

// calcBackoff 计算指数退避 + 抖动的延迟时间。
//
// 公式：min(base * 2^attempt, max) * (1 - jitter/2 + jitter * rand)
func calcBackoff(attempt int, base, max time.Duration, jitter float64) time.Duration {
	if attempt <= 0 {
		return 0
	}

	// 指数退避
	exp := math.Pow(2, float64(attempt-1))
	delay := float64(base) * exp

	// 上限
	if delay > float64(max) {
		delay = float64(max)
	}

	// 抖动
	if jitter > 0 {
		if jitter > 1.0 {
			jitter = 1.0
		}
		halfJitter := jitter / 2.0
		delay = delay * (1 - halfJitter + jitter*rand.Float64())
	}

	return time.Duration(delay)
}

// RetryOption 重试配置选项。
type RetryOption func(*RetryConfig)

// WithMaxAttempts 设置最大重试次数。
func WithMaxAttempts(n int) RetryOption {
	return func(c *RetryConfig) {
		if n > 0 {
			c.MaxAttempts = n
		}
	}
}

// WithBaseDelay 设置基础延迟。
func WithBaseDelay(d time.Duration) RetryOption {
	return func(c *RetryConfig) {
		if d > 0 {
			c.BaseDelay = d
		}
	}
}

// WithMaxDelay 设置最大延迟。
func WithMaxDelay(d time.Duration) RetryOption {
	return func(c *RetryConfig) {
		if d > 0 {
			c.MaxDelay = d
		}
	}
}

// WithJitter 设置抖动因子。
func WithJitter(j float64) RetryOption {
	return func(c *RetryConfig) {
		if j >= 0 && j <= 1.0 {
			c.Jitter = j
		}
	}
}

// WithRetryable 设置可重试谓词。
func WithRetryable(fn func(error) bool) RetryOption {
	return func(c *RetryConfig) {
		c.Retryable = fn
	}
}

// RetryableError 判断错误是否可重试。
//
// 目前将以下错误视为可重试：
//   - 临时性错误（如网络超时、连接重置）
//   - 不包含特定不可重试标记的错误
//
// 不可重试的错误类型：
//   - 参数错误（如 "not found"、"invalid"）
//   - 权限错误（如 "permission"、"denied"）
func RetryableError(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	// 不可重试的错误模式
	nonRetryablePatterns := []string{
		"not found", "not exist", "invalid", "permission",
		"denied", "unauthenticated", "unsupported",
	}
	for _, p := range nonRetryablePatterns {
		if containsSubstring(s, p) {
			return false
		}
	}
	return true
}

func containsSubstring(s, substr string) bool {
	return len(s) >= len(substr) && searchSubstring(s, substr)
}

func searchSubstring(s, substr string) bool {
	for i := 0; i <= len(s)-len(substr); i++ {
		match := true
		for j := 0; j < len(substr); j++ {
			// 不区分大小写
			c1 := s[i+j]
			c2 := substr[j]
			if c1 >= 'A' && c1 <= 'Z' {
				c1 += 32
			}
			if c2 >= 'A' && c2 <= 'Z' {
				c2 += 32
			}
			if c1 != c2 {
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