package scheduler

import (
	"context"
	"sync/atomic"
	"testing"
	"time"
)

func TestEnqueue_Duplicate(t *testing.T) {
	s := NewScheduler(50, 100)

	s.Enqueue("/path/a.go")
	s.Enqueue("/path/a.go") // 重复

	if s.Len() != 1 {
		t.Errorf("expected queue length 1, got %d", s.Len())
	}
}

func TestEnqueue_DifferentPaths(t *testing.T) {
	s := NewScheduler(50, 100)

	s.Enqueue("/path/a.go")
	s.Enqueue("/path/b.go")
	s.Enqueue("/path/c.go")

	if s.Len() != 3 {
		t.Errorf("expected queue length 3, got %d", s.Len())
	}
}

func TestReady_Debounce(t *testing.T) {
	s := NewScheduler(100, 100)

	s.Enqueue("/path/a.go")

	// 100ms 内，不应 ready
	ready := s.Ready()
	if len(ready) != 0 {
		t.Errorf("expected 0 ready before debounce, got %d", len(ready))
	}

	// 等待防抖窗口
	time.Sleep(150 * time.Millisecond)

	ready = s.Ready()
	if len(ready) != 1 {
		t.Errorf("expected 1 ready after debounce, got %d", len(ready))
	}
	if ready[0] != "/path/a.go" {
		t.Errorf("expected /path/a.go, got %s", ready[0])
	}
}

func TestReady_DebounceRefresh(t *testing.T) {
	s := NewScheduler(200, 100)

	s.Enqueue("/path/a.go")
	time.Sleep(100 * time.Millisecond)
	s.Enqueue("/path/a.go") // 刷新时间戳

	// 从第一次入队已过 100ms，但刷新后只过了 0ms
	ready := s.Ready()
	if len(ready) != 0 {
		t.Errorf("expected 0 ready after refresh, got %d", len(ready))
	}

	// 等待防抖窗口
	time.Sleep(250 * time.Millisecond)
	ready = s.Ready()
	if len(ready) != 1 {
		t.Errorf("expected 1 ready after full debounce, got %d", len(ready))
	}
}

func TestDegradeSignal(t *testing.T) {
	s := NewScheduler(10, 3) // 阈值 3

	s.Enqueue("/path/a.go")
	s.Enqueue("/path/b.go")
	s.Enqueue("/path/c.go")

	select {
	case <-s.DegradeSignal:
		// 正确：应触发降级信号
	default:
		t.Error("expected degrade signal when queue >= threshold")
	}
}

func TestStart_ProcessesEvents(t *testing.T) {
	s := NewScheduler(50, 100)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var processed atomic.Int32
	go s.Start(ctx, func(ctx context.Context, path string) error {
		processed.Add(1)
		return nil
	})

	s.Enqueue("/path/a.go")
	s.Enqueue("/path/b.go")

	time.Sleep(200 * time.Millisecond)
	cancel()

	if n := processed.Load(); n != 2 {
		t.Errorf("expected 2 processed, got %d", n)
	}
}

func TestClear(t *testing.T) {
	s := NewScheduler(50, 100)

	s.Enqueue("/path/a.go")
	s.Enqueue("/path/b.go")
	s.Clear()

	if s.Len() != 0 {
		t.Errorf("expected 0 after clear, got %d", s.Len())
	}
}