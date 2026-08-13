package robust

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestGracefulManager_RegisterAndShutdown(t *testing.T) {
	m := NewGracefulManager(5 * time.Second)
	var called int32

	m.RegisterFunc("test_hook", func(ctx context.Context) error {
		atomic.AddInt32(&called, 1)
		return nil
	})

	err := m.Shutdown(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("expected hook to be called once, got %d", called)
	}
}

func TestGracefulManager_ShutdownOrder(t *testing.T) {
	m := NewGracefulManager(5 * time.Second)
	var order []int

	m.RegisterFunc("first", func(ctx context.Context) error {
		order = append(order, 1)
		return nil
	})

	m.RegisterFunc("second", func(ctx context.Context) error {
		order = append(order, 2)
		return nil
	})

	err := m.Shutdown(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 逆序执行：second 先于 first
	if len(order) != 2 {
		t.Fatalf("expected 2 calls, got %d", len(order))
	}
	if order[0] != 2 {
		t.Errorf("expected second hook (2) first, got %d", order[0])
	}
	if order[1] != 1 {
		t.Errorf("expected first hook (1) second, got %d", order[1])
	}
}

func TestGracefulManager_MultipleShutdown(t *testing.T) {
	m := NewGracefulManager(5 * time.Second)
	var called int32

	m.RegisterFunc("test", func(ctx context.Context) error {
		atomic.AddInt32(&called, 1)
		return nil
	})

	// 第一次关闭
	err := m.Shutdown(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// 第二次关闭应返回错误
	err = m.Shutdown(context.Background())
	if err == nil {
		t.Fatal("expected error for second shutdown")
	}

	if atomic.LoadInt32(&called) != 1 {
		t.Errorf("expected hook to be called once, got %d", called)
	}
}

func TestGracefulManager_ShutdownTimeout(t *testing.T) {
	m := NewGracefulManager(100 * time.Millisecond)

	m.RegisterFunc("slow_hook", func(ctx context.Context) error {
		// 超时等待
		<-ctx.Done()
		return ctx.Err()
	})

	err := m.Shutdown(context.Background())
	if err != nil {
		t.Logf("expected timeout error: %v", err)
	}
}

func TestGracefulManager_ShutdownRequested(t *testing.T) {
	m := NewGracefulManager(5 * time.Second)

	ch := m.ShutdownRequested()
	select {
	case <-ch:
		t.Fatal("channel should not be closed before shutdown")
	default:
		// 正确：未关闭
	}

	go func() {
		_ = m.Shutdown(context.Background())
	}()

	select {
	case <-ch:
		// 正确：已关闭
	case <-time.After(100 * time.Millisecond):
		t.Fatal("channel should be closed after shutdown")
	}
}

func TestGracefulManager_HookError(t *testing.T) {
	m := NewGracefulManager(5 * time.Second)

	m.RegisterFunc("error_hook", func(ctx context.Context) error {
		return context.DeadlineExceeded
	})

	err := m.Shutdown(context.Background())
	if err == nil {
		t.Fatal("expected error from hook")
	}
}

func TestGracefulManager_NilHooks(t *testing.T) {
	m := NewGracefulManager(5 * time.Second)

	err := m.Shutdown(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestWaitGraceful(t *testing.T) {
	m := NewGracefulManager(5 * time.Second)

	// 启动关闭
	go func() {
		_ = m.Shutdown(context.Background())
	}()

	// 等待 Done 或超时
	select {
	case <-m.Done():
		// 关闭完成
	case <-time.After(5 * time.Second):
		t.Fatal("graceful shutdown timed out")
	}
}

func TestWaitGraceful_Timeout(t *testing.T) {
	m := NewGracefulManager(5 * time.Second)

	err := WaitGraceful(m, 10*time.Millisecond)
	if err == nil {
		t.Fatal("expected timeout error")
	}
}

func TestShutdownHookFunc(t *testing.T) {
	var called bool
	hook := ShutdownHookFunc(func(ctx context.Context) error {
		called = true
		return nil
	})

	err := hook.Shutdown(context.Background())
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if !called {
		t.Error("expected hook to be called")
	}
}

func TestNewGracefulManager_DefaultTimeout(t *testing.T) {
	m := NewGracefulManager(0)
	if m.timeout != 30*time.Second {
		t.Errorf("expected default timeout 30s, got %v", m.timeout)
	}
}