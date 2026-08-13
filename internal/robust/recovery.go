package robust

import (
	"fmt"
	"runtime/debug"

	"github.com/idcu/codeschema/internal/log"
)

// RecoveryHandler 处理 panic 恢复。
type RecoveryHandler struct {
	logger *log.Logger
}

// NewRecoveryHandler 创建 panic 恢复处理器。
func NewRecoveryHandler() *RecoveryHandler {
	return &RecoveryHandler{
		logger: log.WithModule("robust.recovery"),
	}
}

// WithLogger 设置自定义日志器。
func (r *RecoveryHandler) WithLogger(l *log.Logger) *RecoveryHandler {
	r.logger = l
	return r
}

// Go 安全启动 goroutine，自动捕获 panic。
//
// 用法：
//
//	handler := robust.NewRecoveryHandler()
//	handler.Go(func() {
//	    // 可能 panic 的代码
//	})
func (r *RecoveryHandler) Go(fn func()) {
	go func() {
		defer r.Recover()
		fn()
	}()
}

// GoWithContext 安全启动带 context 的 goroutine。
//
// 用法：
//
//	handler.GoWithContext(ctx, func(ctx context.Context) {
//	    // 可能 panic 的代码
//	})
func (r *RecoveryHandler) GoWithContext(ctx interface{}, fn func(interface{})) {
	go func() {
		defer r.Recover()
		fn(ctx)
	}()
}

// Recover 捕获 panic 并记录日志。
//
// 通常在 defer 中调用：
//
//	defer handler.Recover()
func (r *RecoveryHandler) Recover() {
	if rec := recover(); rec != nil {
		r.logger.Error("panic recovered",
			"panic", fmt.Sprintf("%v", rec),
			"stack", string(debug.Stack()),
		)
	}
}

// RecoverWithCallback 捕获 panic 并执行回调。
//
// 回调函数接收 panic 值和堆栈信息。
// 通常在 defer 中调用：
//
//	defer handler.RecoverWithCallback(func(panicVal any, stack string) {
//	    // 自定义处理，如上报指标
//	})
func (r *RecoveryHandler) RecoverWithCallback(callback func(panicVal any, stack string)) {
	if rec := recover(); rec != nil {
		stack := string(debug.Stack())
		r.logger.Error("panic recovered",
			"panic", fmt.Sprintf("%v", rec),
			"stack", stack,
		)
		if callback != nil {
			callback(rec, stack)
		}
	}
}

// SafeCall 安全调用函数，捕获 panic 并返回错误。
func SafeCall(fn func()) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v\n%s", rec, debug.Stack())
		}
	}()
	fn()
	return nil
}

// SafeCallWithResult 安全调用带返回值的函数，捕获 panic 并返回错误。
func SafeCallWithResult[T any](fn func() T) (result T, err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("panic: %v\n%s", rec, debug.Stack())
		}
	}()
	result = fn()
	return result, nil
}

// GlobalRecoveryHandler 全局默认 panic 恢复处理器。
var GlobalRecoveryHandler = NewRecoveryHandler()

// Go 全局便捷函数，使用 GlobalRecoveryHandler 启动 goroutine。
func Go(fn func()) {
	GlobalRecoveryHandler.Go(fn)
}

// Recover 全局便捷函数，使用 GlobalRecoveryHandler 捕获 panic。
func Recover() {
	GlobalRecoveryHandler.Recover()
}