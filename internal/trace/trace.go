// Package trace 提供简单的链路追踪功能。
//
// 基于 span 模型，支持嵌套、耗时记录、标签附加。
// 纯 Go 实现，无外部依赖。输出通过 log 包记录。
//
// 使用方式：
//
//	 span := trace.Start("scan", "root", "/repo")
//	 defer span.End()
//	 // ... do work ...
//	 span.AddEvent("file parsed", "file", "main.go")
package trace

import (
	"fmt"
	"strings"
	"sync"
	"time"

	"codeschema/internal/log"
)

// Span 表示一个追踪跨度。
type Span struct {
	mu       sync.Mutex
	name     string
	tags     map[string]string
	start    time.Time
	children []*Span
	parent   *Span
	ended    bool
}

var (
	// globalTraceID 全局追踪 ID 计数器。
	globalTraceID int64
	mu            sync.Mutex
)

// nextTraceID 生成下一个追踪 ID。
func nextTraceID() int64 {
	mu.Lock()
	defer mu.Unlock()
	globalTraceID++
	return globalTraceID
}

// Start 创建一个新的追踪 span。
// name 为 span 名称，tags 为可选的 key-value 标签对。
func Start(name string, tags ...string) *Span {
	s := &Span{
		name:  name,
		tags:  make(map[string]string),
		start: time.Now(),
	}

	for i := 0; i < len(tags)-1; i += 2 {
		s.tags[tags[i]] = tags[i+1]
	}

	return s
}

// StartChild 创建一个子 span。
func (s *Span) StartChild(name string, tags ...string) *Span {
	child := Start(name, tags...)
	child.parent = s

	s.mu.Lock()
	s.children = append(s.children, child)
	s.mu.Unlock()

	return child
}

// End 结束 span，记录耗时日志。
func (s *Span) End() {
	s.mu.Lock()
	if s.ended {
		s.mu.Unlock()
		return
	}
	s.ended = true
	duration := time.Since(s.start)
	s.mu.Unlock()

	// 构建日志参数
	args := []any{
		"duration_ms", duration.Milliseconds(),
		"span", s.name,
	}

	for k, v := range s.tags {
		args = append(args, k, v)
	}

	if s.parent != nil {
		args = append(args, "parent_span", s.parent.name)
	}

	if len(s.children) > 0 {
		childNames := make([]string, 0, len(s.children))
		for _, c := range s.children {
			childNames = append(childNames, c.name)
		}
		args = append(args, "children", strings.Join(childNames, ","))
	}

	log.L().With("module", "trace").Debug("span completed", args...)
}

// AddEvent 向 span 添加一个事件/标签。
func (s *Span) AddEvent(event string, tags ...string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.ended {
		return
	}

	s.tags["event"] = event
	for i := 0; i < len(tags)-1; i += 2 {
		if i+1 < len(tags) {
			s.tags[tags[i]] = tags[i+1]
		}
	}
}

// SetTag 设置 span 标签。
func (s *Span) SetTag(key, value string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ended {
		s.tags[key] = value
	}
}

// Duration 返回 span 的持续时间（仅在 End 后调用）。
func (s *Span) Duration() time.Duration {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.ended {
		return time.Since(s.start)
	}
	return time.Since(s.start)
}

// Name 返回 span 名称。
func (s *Span) Name() string {
	return s.name
}

// String 返回 span 的文本表示。
func (s *Span) String() string {
	s.mu.Lock()
	defer s.mu.Unlock()

	duration := time.Since(s.start)
	var b strings.Builder
	fmt.Fprintf(&b, "%s [%v]", s.name, duration.Round(time.Microsecond))

	if len(s.tags) > 0 {
		b.WriteString(" {")
		first := true
		for k, v := range s.tags {
			if !first {
				b.WriteString(", ")
			}
			fmt.Fprintf(&b, "%s=%s", k, v)
			first = false
		}
		b.WriteString("}")
	}

	if len(s.children) > 0 {
		b.WriteString(" [")
		for i, child := range s.children {
			if i > 0 {
				b.WriteString(", ")
			}
			b.WriteString(child.name)
		}
		b.WriteString("]")
	}

	return b.String()
}

// Trace 表示一个完整的追踪链路。
type Trace struct {
	ID      int64
	Root    *Span
	start   time.Time
	ended   bool
}

// NewTrace 创建一个新的追踪链路。
// 返回 Trace 和根 span。
func NewTrace(name string, tags ...string) (*Trace, *Span) {
	root := Start(name, tags...)
	return &Trace{
		ID:    nextTraceID(),
		Root:  root,
		start: time.Now(),
	}, root
}

// End 结束追踪链路，记录汇总日志。
func (t *Trace) End() {
	if t.ended {
		return
	}
	t.ended = true
	t.Root.End()

	duration := time.Since(t.start)
	log.L().With("module", "trace").Info("trace completed",
		"trace_id", t.ID,
		"root_span", t.Root.name,
		"total_duration_ms", duration.Milliseconds(),
	)
}