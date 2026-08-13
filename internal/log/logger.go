// Package log 提供结构化日志功能，基于 Go 标准库 log/slog。
//
// 支持 JSON 格式输出、日志级别控制、模块化 Logger。
// 使用方式：
//
//	 log.Init(log.LevelInfo, true)    // JSON 输出到 stderr
//	 log.Init(log.LevelDebug, false)  // 文本格式输出到 stderr
//	 logger := log.WithModule("scanner")
//	 logger.Info("scan started", "root", "/repo")
package log

import (
	"context"
	"io"
	"log/slog"
	"os"
	"runtime"
	"sync"
	"time"
)

// Level 日志级别，与 slog.Level 对齐。
type Level int

const (
	LevelDebug Level = Level(slog.LevelDebug)
	LevelInfo  Level = Level(slog.LevelInfo)
	LevelWarn  Level = Level(slog.LevelWarn)
	LevelError Level = Level(slog.LevelError)
)

// Logger 封装的结构化日志实例。
type Logger struct {
	inner  *slog.Logger
	module string
}

var (
	defaultLogger *Logger
	mu            sync.Mutex
)

// newHandler 创建 slog.Handler，带自定义时间格式和 ReplaceAttr。
func newHandler(w io.Writer, level Level, jsonOutput bool) slog.Handler {
	opts := &slog.HandlerOptions{
		Level: slog.Level(level),
		ReplaceAttr: func(groups []string, a slog.Attr) slog.Attr {
			if a.Key == "time" {
				if t, ok := a.Value.Any().(time.Time); ok {
					return slog.String("timestamp", t.Format("2006-01-02T15:04:05.000Z07:00"))
				}
			}
			return a
		},
	}
	if jsonOutput {
		return slog.NewJSONHandler(w, opts)
	}
	return slog.NewTextHandler(w, opts)
}

// Init 初始化全局日志。
//
//	level: 日志级别（LevelDebug/LevelInfo/LevelWarn/LevelError）
//	jsonOutput: true 输出 JSON 格式，false 输出文本格式
func Init(level Level, jsonOutput bool) {
	mu.Lock()
	defer mu.Unlock()
	defaultLogger = &Logger{
		inner: slog.New(newHandler(os.Stderr, level, jsonOutput)),
	}
}

// InitWriter 使用自定义 writer 初始化日志（用于测试）。
func InitWriter(w io.Writer, level Level, jsonOutput bool) {
	mu.Lock()
	defer mu.Unlock()
	defaultLogger = &Logger{
		inner: slog.New(newHandler(w, level, jsonOutput)),
	}
}

// L 返回全局默认 Logger（线程安全）。
func L() *Logger {
	mu.Lock()
	defer mu.Unlock()
	if defaultLogger == nil {
		defaultLogger = &Logger{
			inner: slog.New(newHandler(os.Stderr, LevelInfo, true)),
		}
	}
	return defaultLogger
}

// WithModule 创建一个带模块名的 Logger。
func WithModule(module string) *Logger {
	return L().WithModule(module)
}

// WithModule 返回一个带模块名的 Logger 副本。
func (l *Logger) WithModule(module string) *Logger {
	return &Logger{
		inner:  l.inner.With("module", module),
		module: module,
	}
}

// With 返回一个带额外字段的 Logger 副本。
func (l *Logger) With(args ...any) *Logger {
	return &Logger{
		inner:  l.inner.With(args...),
		module: l.module,
	}
}

// Debug 记录 Debug 级别日志。
func (l *Logger) Debug(msg string, args ...any) {
	l.log(slog.LevelDebug, msg, args...)
}

// Info 记录 Info 级别日志。
func (l *Logger) Info(msg string, args ...any) {
	l.log(slog.LevelInfo, msg, args...)
}

// Warn 记录 Warn 级别日志。
func (l *Logger) Warn(msg string, args ...any) {
	l.log(slog.LevelWarn, msg, args...)
}

// Error 记录 Error 级别日志。
func (l *Logger) Error(msg string, args ...any) {
	l.log(slog.LevelError, msg, args...)
}

// Debugf 记录 Debug 级别格式化日志。
func (l *Logger) Debugf(msg string, args ...any) {
	l.log(slog.LevelDebug, formatMsg(msg, args))
}

// Infof 记录 Info 级别格式化日志。
func (l *Logger) Infof(msg string, args ...any) {
	l.log(slog.LevelInfo, formatMsg(msg, args))
}

// Warnf 记录 Warn 级别格式化日志。
func (l *Logger) Warnf(msg string, args ...any) {
	l.log(slog.LevelWarn, formatMsg(msg, args))
}

// Errorf 记录 Error 级别格式化日志。
func (l *Logger) Errorf(msg string, args ...any) {
	l.log(slog.LevelError, formatMsg(msg, args))
}

// log 内部日志记录方法，自动添加 caller 信息。
func (l *Logger) log(level slog.Level, msg string, args ...any) {
	if l.inner == nil {
		return
	}

	// 获取调用者信息（跳过 2 层：log + Debug/Info 等）
	_, file, line, ok := runtime.Caller(2)
	if ok {
		// 只保留文件名
		short := file
		for i := len(file) - 1; i > 0; i-- {
			if file[i] == '/' {
				short = file[i+1:]
				break
			}
		}
		args = append(args, "caller", short+":"+itoa(line))
	}

	l.inner.LogAttrs(context.Background(), level, msg, argsToAttrs(args)...)
}

// argsToAttrs 将 key-value 对转换为 slog.Attr 切片。
func argsToAttrs(args []any) []slog.Attr {
	if len(args) == 0 {
		return nil
	}

	attrs := make([]slog.Attr, 0, len(args)/2)
	for i := 0; i < len(args); i += 2 {
		if i+1 >= len(args) {
			break
		}
		key, ok := args[i].(string)
		if !ok {
			continue
		}
		attrs = append(attrs, slog.Any(key, args[i+1]))
	}
	return attrs
}

// formatMsg 格式化消息（用于 printf 风格的方法）。
func formatMsg(msg string, args []any) string {
	if len(args) == 0 {
		return msg
	}
	// 简单实现，直接拼接
	result := msg
	for _, a := range args {
		result += " " + fmtSprint(a)
	}
	return result
}

// fmtSprint 简单格式化值。
func fmtSprint(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// itoa 快速整数转字符串。
func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	return string(buf[i:])
}

// 全局便捷函数

// Debug 使用全局 Logger 记录 Debug 级别日志。
func Debug(msg string, args ...any) {
	L().Debug(msg, args...)
}

// Info 使用全局 Logger 记录 Info 级别日志。
func Info(msg string, args ...any) {
	L().Info(msg, args...)
}

// Warn 使用全局 Logger 记录 Warn 级别日志。
func Warn(msg string, args ...any) {
	L().Warn(msg, args...)
}

// Error 使用全局 Logger 记录 Error 级别日志。
func Error(msg string, args ...any) {
	L().Error(msg, args...)
}