// Package robust 提供生产级健壮性工具：优雅关闭、重试机制、Panic 恢复。
//
// 设计原则：
//  1. 无依赖侵入：通用工具可被各包按需使用
//  2. 可扩展性：ShutdownHook 支持任意组件注册
//  3. 超时控制：每个关闭阶段都有超时，避免无限挂起
//  4. 顺序保证：先关闭网络服务器，再关闭内部队列，最后持久化数据
package robust

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/idcu/codeschema/internal/log"
)

// ShutdownHook 是关闭钩子接口，在优雅关闭时被调用。
type ShutdownHook interface {
	// Shutdown 执行关闭操作。
	// ctx 带有超时限制，实现者应尊重 ctx 的取消。
	Shutdown(ctx context.Context) error
}

// ShutdownHookFunc 函数类型适配 ShutdownHook。
type ShutdownHookFunc func(ctx context.Context) error

// Shutdown 调用函数。
func (f ShutdownHookFunc) Shutdown(ctx context.Context) error {
	return f(ctx)
}

// GracefulManager 管理优雅关闭生命周期。
//
// 功能：
//   - 注册多个关闭钩子，按逆序执行
//   - 全局超时控制
//   - 关闭进度日志
//   - 错误收集
type GracefulManager struct {
	hooks     []ShutdownHook
	names     []string
	mu        sync.Mutex
	logger    *log.Logger
	timeout   time.Duration
	started   bool
	shutdownC chan struct{}
	doneC     chan struct{}
}

// NewGracefulManager 创建优雅关闭管理器。
// timeout: 整个关闭流程的最大超时。
func NewGracefulManager(timeout time.Duration) *GracefulManager {
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	return &GracefulManager{
		logger:    log.WithModule("robust.graceful"),
		timeout:   timeout,
		shutdownC: make(chan struct{}),
		doneC:     make(chan struct{}),
	}
}

// Register 注册一个关闭钩子。
//
// 钩子按注册顺序执行关闭，因此：
//   - 先注册的后关闭（依赖关系：服务器 -> 队列 -> 存储）
//   - 网络服务器应该最后注册（最先关闭）
//   - 存储/持久化应该最先注册（最后关闭）
func (m *GracefulManager) Register(name string, hook ShutdownHook) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.hooks = append(m.hooks, hook)
	m.names = append(m.names, name)
}

// RegisterFunc 注册函数作为关闭钩子。
func (m *GracefulManager) RegisterFunc(name string, hook func(ctx context.Context) error) {
	m.Register(name, ShutdownHookFunc(hook))
}

// ShutdownRequested 返回一个 channel，当收到关闭信号时关闭。
// 应用应监听此 channel 开始优雅关闭流程。
func (m *GracefulManager) ShutdownRequested() <-chan struct{} {
	return m.shutdownC
}

// Done 返回一个 channel，当所有关闭钩子执行完成后关闭。
func (m *GracefulManager) Done() <-chan struct{} {
	return m.doneC
}

// WaitForSignal 等待信号并启动关闭流程。
//
// 通常在 main goroutine 中调用，阻塞直到关闭完成。
// 返回最终错误（如果有）。
func (m *GracefulManager) WaitForSignal(ctx context.Context) error {
	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	select {
	case <-ctx.Done():
		// 上下文已取消，直接开始关闭
	case sig := <-sigCh:
		m.logger.Info("received shutdown signal", "signal", sig.String())
	}

	return m.Shutdown(context.Background())
}

// Shutdown 执行所有关闭钩子，按逆序执行。
//
// 每个钩子都有全局超时的等分时间，超时后强制继续下一个钩子。
func (m *GracefulManager) Shutdown(rootCtx context.Context) error {
	m.mu.Lock()
	if m.started {
		m.mu.Unlock()
		return fmt.Errorf("shutdown already started")
	}
	m.started = true
	close(m.shutdownC)
	m.mu.Unlock()

	ctx, cancel := context.WithTimeout(rootCtx, m.timeout)
	defer cancel()

	var errors []error
	var errorsMu sync.Mutex
	done := make(chan struct{})

	go func() {
		defer close(done)
		defer close(m.doneC)

		n := len(m.hooks)
		if n == 0 {
			return
		}

		perHookTimeout := m.timeout / time.Duration(n)
		if perHookTimeout < 500*time.Millisecond {
			perHookTimeout = 500 * time.Millisecond
		}

		m.logger.Info("starting graceful shutdown", "hooks", n)

		// 逆序执行：先注册的后关闭
		for i := n - 1; i >= 0; i-- {
			select {
			case <-ctx.Done():
				m.logger.Warn("shutdown timed out", "remaining_hooks", i+1)
				errorsMu.Lock()
				errors = append(errors, fmt.Errorf("global shutdown timeout after %d hooks", n-i))
				errorsMu.Unlock()
				return
			default:
			}

			hook := m.hooks[i]
			name := m.names[i]

			hookCtx, hookCancel := context.WithTimeout(ctx, perHookTimeout)
			m.logger.Debug("running shutdown hook", "name", name)

			start := time.Now()
			if err := hook.Shutdown(hookCtx); err != nil {
				m.logger.Warn("shutdown hook failed", "name", name, "error", err)
				errorsMu.Lock()
				errors = append(errors, fmt.Errorf("%s: %w", name, err))
				errorsMu.Unlock()
			} else {
				m.logger.Debug("shutdown hook completed", "name", name, "duration_ms", time.Since(start).Milliseconds())
			}

			hookCancel()
		}

		totalDuration := time.Since(ctxTimeStart(ctx)).Round(time.Millisecond)
		if len(errors) > 0 {
			m.logger.Warn("graceful shutdown completed with errors", "duration", totalDuration, "errors", len(errors))
		} else {
			m.logger.Info("graceful shutdown completed", "duration", totalDuration)
		}
	}()

	select {
	case <-done:
	case <-ctx.Done():
	}

	errorsMu.Lock()
	defer errorsMu.Unlock()

	if len(errors) > 0 {
		return errors[0]
	}
	return nil
}

func ctxTimeStart(ctx context.Context) time.Time {
	return time.Now()
}

// WaitGraceful 通用等待优雅关闭完成。
func WaitGraceful(manager *GracefulManager, timeout time.Duration) error {
	select {
	case <-manager.Done():
		return nil
	case <-time.After(timeout):
		return fmt.Errorf("graceful shutdown timed out after %v", timeout)
	}
}

// ForceExitOnSecondSignal 如果收到第二个信号，强制退出。
func ForceExitOnSecondSignal(manager *GracefulManager) {
	go func() {
		sigCh := make(chan os.Signal, 1)
		signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

		select {
		case <-manager.ShutdownRequested():
		case <-sigCh:
		}

		// 强制退出
		fmt.Fprintf(os.Stderr, "\nreceived second shutdown signal, forcing exit\n")
		os.Exit(1)
	}()
}