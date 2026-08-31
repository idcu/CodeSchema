// Package scheduler 提供中性的事件防抖与排队调度原语。
//
// 设计目标：
//   - 防抖窗口内合并相同 key 的重复事件；
//   - 队列达到阈值时发出降级信号（DegradeSignal），由调用方决定降级策略；
//   - 单 goroutine 顺序处理，无需调用方做并发安全。
//
// 本包不绑定任何业务语义（key 为泛型），可作为通用原语被任意项目复用
// （档二本地公共化）。领域层（如 code-schema）以 string 路径作为 key 使用。
package scheduler

import (
	"context"
	"sync"
	"time"

	"gitee.com/idcu-go/recovery"
)

// Scheduler 是事件调度器，负责防抖合并与排队。
// K 为事件 key 类型（如文件路径），需可比较。
type Scheduler[K comparable] struct {
	mu         sync.Mutex
	queue      map[K]time.Time // key -> last event time
	order      []K             // 保持入队顺序
	debounceMs int
	threshold  int

	// DegradeSignal 当队列超阈值时发送信号，通知调用方进行降级。
	// 接收方应消费此信号（非阻塞）。
	DegradeSignal chan struct{}
}

// NewScheduler 创建 Scheduler 实例。
// debounceMs: 防抖窗口毫秒数（默认 300）。
// threshold: 队列阈值（默认 1000），超限触发降级信号。
func NewScheduler[K comparable](debounceMs, threshold int) *Scheduler[K] {
	if debounceMs <= 0 {
		debounceMs = 300
	}
	if threshold <= 0 {
		threshold = 1000
	}
	return &Scheduler[K]{
		queue:         make(map[K]time.Time),
		debounceMs:    debounceMs,
		threshold:     threshold,
		DegradeSignal: make(chan struct{}, 1),
	}
}

// Enqueue 将事件入队。
// 同一 key 在防抖窗口内多次调用仅刷新时间戳，不重复入队。
func (s *Scheduler[K]) Enqueue(key K) {
	s.mu.Lock()
	defer s.mu.Unlock()

	if _, exists := s.queue[key]; exists {
		// 已存在，仅更新时间戳
		s.queue[key] = time.Now()
		return
	}

	s.queue[key] = time.Now()
	s.order = append(s.order, key)

	// 检查阈值
	if len(s.order) >= s.threshold {
		select {
		case s.DegradeSignal <- struct{}{}:
		default:
		}
	}
}

// Ready 返回防抖窗口已到期的 key 列表。
// 未到期的事件仍留在队列中。
func (s *Scheduler[K]) Ready() []K {
	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.order) == 0 {
		return nil
	}

	now := time.Now()
	var ready []K
	var remaining []K

	for _, key := range s.order {
		lastTime, ok := s.queue[key]
		if !ok {
			continue
		}
		if now.Sub(lastTime) >= time.Duration(s.debounceMs)*time.Millisecond {
			ready = append(ready, key)
			delete(s.queue, key)
		} else {
			remaining = append(remaining, key)
		}
	}
	s.order = remaining
	return ready
}

// Start 启动调度循环，从队列中取出到期事件并调用 processFn。
// 每 100ms 轮询一次队列，直到 context 取消。
func (s *Scheduler[K]) Start(ctx context.Context, processFn func(context.Context, K) error) {
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			keys := s.Ready()
			for _, key := range keys {
				select {
				case <-ctx.Done():
					return
				default:
					recovery.SafeCall(func() {
						_ = processFn(ctx, key) // 错误已由 processFn 内部处理
					})
				}
			}
		}
	}
}

// Len 返回当前队列长度。
func (s *Scheduler[K]) Len() int {
	s.mu.Lock()
	defer s.mu.Unlock()
	return len(s.order)
}

// Clear 清空队列。
func (s *Scheduler[K]) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.queue = make(map[K]time.Time)
	s.order = nil
}
