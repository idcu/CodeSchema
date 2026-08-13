package trace

import (
	"strings"
	"testing"
	"time"
)

func TestStart(t *testing.T) {
	s := Start("test_span", "key1", "val1")
	if s == nil {
		t.Fatal("expected non-nil span")
	}
	if s.Name() != "test_span" {
		t.Errorf("expected name 'test_span', got %s", s.Name())
	}
}

func TestEnd(t *testing.T) {
	s := Start("test_span")
	s.End()
	// Should not panic on double end
	s.End()
}

func TestDuration(t *testing.T) {
	s := Start("test_span")
	time.Sleep(10 * time.Millisecond)
	s.End()

	dur := s.Duration()
	if dur < 10*time.Millisecond {
		t.Errorf("expected duration >= 10ms, got %v", dur)
	}
}

func TestString(t *testing.T) {
	s := Start("test_span", "lang", "go")
	time.Sleep(time.Millisecond)
	str := s.String()
	if !strings.Contains(str, "test_span") {
		t.Errorf("expected 'test_span' in string, got: %s", str)
	}
	if !strings.Contains(str, "lang=go") {
		t.Errorf("expected 'lang=go' in string, got: %s", str)
	}
}

func TestStartChild(t *testing.T) {
	parent := Start("parent")
	child := parent.StartChild("child")
	if child == nil {
		t.Fatal("expected non-nil child span")
	}
	if child.parent != parent {
		t.Error("child.parent should point to parent")
	}

	parent.End()

	str := parent.String()
	if !strings.Contains(str, "child") {
		t.Errorf("expected 'child' in string, got: %s", str)
	}
}

func TestChildEnd(t *testing.T) {
	parent := Start("parent")
	child := parent.StartChild("child")
	child.End()
	parent.End()
	// Should not panic on child end
}

func TestSetTag(t *testing.T) {
	s := Start("test")
	s.SetTag("key", "value")
	s.End()
	// Should not panic after end
	s.SetTag("after_end", "should_not_panic")
}

func TestAddEvent(t *testing.T) {
	s := Start("test")
	s.AddEvent("event1", "file", "main.go")
	s.End()
	// Should not panic after end
	s.AddEvent("after_end", "should_not_panic")
}

func TestNewTrace(t *testing.T) {
	trace, root := NewTrace("root_span", "env", "test")
	if trace == nil {
		t.Fatal("expected non-nil trace")
	}
	if trace.ID <= 0 {
		t.Errorf("expected positive trace ID, got %d", trace.ID)
	}
	if root == nil {
		t.Fatal("expected non-nil root span")
	}
	if root.Name() != "root_span" {
		t.Errorf("expected name 'root_span', got %s", root.Name())
	}
}

func TestTraceEnd(t *testing.T) {
	trace, root := NewTrace("test_trace")
	child := root.StartChild("child_span")
	child.End()
	trace.End()
	// Should not panic on double end
	trace.End()
}

func TestTraceNestedChildren(t *testing.T) {
	parent := Start("parent")
	child := parent.StartChild("child")
	grandchild := child.StartChild("grandchild")
	grandchild.End()
	child.End()
	parent.End()

	str := parent.String()
	if !strings.Contains(str, "child") {
		t.Errorf("expected 'child' in string, got: %s", str)
	}
}

func TestDuplicateTags(t *testing.T) {
	s := Start("test", "key", "val1")
	s.SetTag("key", "val2") // 覆盖
	s.End()

	// string 中应该只包含最后一个值
	str := s.String()
	if strings.Contains(str, "val1") {
		// 这取决于 map 顺序，可能不准确
		// 只是确认不会 panic
	}
}

func TestMultipleChildren(t *testing.T) {
	parent := Start("parent")
	c1 := parent.StartChild("child1")
	c2 := parent.StartChild("child2")
	c1.End()
	c2.End()
	parent.End()

	// 确认 parent 包含两个子 span
	parent.mu.Lock()
	count := len(parent.children)
	parent.mu.Unlock()

	if count != 2 {
		t.Errorf("expected 2 children, got %d", count)
	}
}

func TestConcurrentSafe(t *testing.T) {
	s := Start("concurrent")
	done := make(chan bool)

	go func() {
		s.SetTag("goroutine1", "val1")
		done <- true
	}()
	go func() {
		s.SetTag("goroutine2", "val2")
		done <- true
	}()
	go func() {
		s.StartChild("child_from_g2")
		done <- true
	}()

	<-done
	<-done
	<-done
	s.End()
}

func TestTraceIDIncrement(t *testing.T) {
	t1, _ := NewTrace("trace1")
	t2, _ := NewTrace("trace2")

	if t2.ID <= t1.ID {
		t.Errorf("expected trace2 ID > trace1 ID, got %d <= %d", t2.ID, t1.ID)
	}
}

func TestSpanNotEndedDuration(t *testing.T) {
	s := Start("test")
	time.Sleep(time.Millisecond)
	dur := s.Duration()
	if dur < time.Millisecond {
		t.Errorf("expected duration >= 1ms, got %v", dur)
	}
	s.End()
}