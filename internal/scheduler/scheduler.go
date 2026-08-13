// Package scheduler 提供事件防抖与排队调度。
//
// 设计目标：
// - 300ms 防抖窗口内合并相同文件路径的事件。
// - 队列阈值 1000 触发全量扫描降级信号。
// - 单 goroutine 顺序处理，无需并发安全。
package scheduler

import (
	"context"
	"sync"
	"time"
)

// Scheduler 是事件调度器，负责防抖合并与排队。
type Scheduler struct {
	mu         sync.Mutex
	queue      map[string]time.Time // path -> last event time
	order      []string             // 保持入队顺序
	debounceMs int
	threshold  int

	// DegradeSignal 当队列超阈值时发送信号，通知调用方进行全量降级。
	// 接收方应消费此信号（非阻塞）。
	DegradeSignal chan struct{}
}

// NewScheduler 创建 Scheduler 实例。
// debounceMs: 防抖窗口毫秒数（默认 300）。
// threshold: 队列阈值（默认 1000），超限触发降级信号。
func NewScheduler(debounceMs, threshold int) *Scheduler {
	if debounceMs <= 0 {
		debounceMs = 300
	}
	if threshold <= 0 {
		threshold = 1000
	}
	return &Scheduler{
		queue:         make(map[string]time.Time),
		debounceMs:    debounceMs,
		threshold:     threshold,
		DegradeSignal: make(chan struct{}, 1),
	}
}

// Enqueue 将文件变更事件入队。
// 同一文件在防抖窗口内多次调用仅刷新时间戳，不重复入队。
func (s *Scheduler) Enqueue(path string) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.queue[path]; exists {
		// 已存在，仅更新时间戳
		s.queue[path] = time.Now()
		return
	}

	s.queue[path] = time.Now()
	s.order = append(s.order, path)

	// 检查阈值
	if len(s.order) >= s.threshold {
		select {
		case s.DegradeSignal <- struct{}{}:
		default:
		}
	}
}

// Ready 返回防抖窗口已到期的文件路径列表。
// 未到期的事件仍留在队列中。
func (s *Scheduler) Ready() []string {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.order) == 0 {
		return nil
	}

	now := time.Now()
	var ready []string
	var remaining []string

	for _, path := range s.order {
		lastTime, ok := s.queue[path]
		if !ok {
			continue
		}
		if now.Sub(lastTime) >= time.Duration(s.debounceMs)*time.Millisecond {
			ready = append(ready, path)
			delete(s.queue, path)
		} else {
			remaining = append(remaining, path)
		}
	}
	s.order = remaining
	return ready
}

// Start 启动调度循环，从队列中取出到期事件并调用 processFn。
// 每 100ms 轮询一次队列，直到 context 取消。
func (s *Scheduler) Start(ctx context.Context, processFn func(context.Context, string) error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			paths := s.Ready()
			for _, path := range paths {
				select {
				case <-ctx.Done():
					return
				default:
					_ = processFn(ctx, path) // 错误已由 processFn 内部处理
				}
			}
		}
	}
}

// Len 返回当前队列长度。
func (s *Scheduler) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.order)
}

// Clear 清空队列。
func (s *Scheduler) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = make(map[string]time.Time)
	s.order = nil
}