package ai

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"

	cerrors "github.com/idcu/codeschema/internal/errors"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// mockLLMClient 可配置的 LLM 桩：按方法返回预设结果或错误。
type mockLLMClient struct {
	completeTags   []string
	completeText   []string
	chooseIdx      int
	completeErr    error
	chooseErr      error
	completeCalls  int
	chooseCalls    int
}

func (m *mockLLMClient) Complete(_ context.Context, _ string) ([]string, error) {
	m.completeCalls++
	if m.completeErr != nil {
		return nil, m.completeErr
	}
	if m.completeText != nil {
		return m.completeText, nil
	}
	return m.completeTags, nil
}

func (m *mockLLMClient) Choose(_ context.Context, _ string) (int, error) {
	m.chooseCalls++
	if m.chooseErr != nil {
		return 0, m.chooseErr
	}
	return m.chooseIdx, nil
}

// fakeEntity 测试用 IRable 桩。
type fakeEntity struct {
	name  string
	fqn   string
	doc   string
	kind  string
}

func (f fakeEntity) Name() string          { return f.name }
func (f fakeEntity) QualifiedName() string { return f.fqn }
func (f fakeEntity) DocComment() string    { return f.doc }
func (f fakeEntity) Kind() string          { return f.kind }

func TestEnhancer_EnhanceTag_Success(t *testing.T) {
	m := &mockLLMClient{completeTags: []string{"biz:order", "tech:rpc"}}
	e := NewEnhancer(m, NewBudget(5, 5))
	tags, err := e.EnhanceTag(context.Background(), fakeEntity{name: "OrderService"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(tags) != 2 || tags[0] != "biz:order" {
		t.Errorf("unexpected tags: %v", tags)
	}
	if m.completeCalls != 1 {
		t.Errorf("expected 1 Complete call, got %d", m.completeCalls)
	}
}

func TestEnhancer_EnhanceTag_BudgetExceeded(t *testing.T) {
	m := &mockLLMClient{completeTags: []string{"x"}}
	e := NewEnhancer(m, NewBudget(1, 1))
	if _, err := e.EnhanceTag(context.Background(), fakeEntity{}); err != nil {
		t.Fatalf("first call should succeed: %v", err)
	}
	_, err := e.EnhanceTag(context.Background(), fakeEntity{})
	if !errors.Is(err, cerrors.ErrBudgetExceeded) {
		t.Fatalf("expected ErrBudgetExceeded, got %v", err)
	}
	if m.completeCalls != 1 {
		t.Errorf("LLM must not be called after budget exceeded, got %d", m.completeCalls)
	}
}

func TestEnhancer_EnhanceDoc_JoinsLines(t *testing.T) {
	m := &mockLLMClient{completeText: []string{"line1", "line2", "line3"}}
	e := NewEnhancer(m, NewBudget(-1, -1))
	doc, err := e.EnhanceDoc(context.Background(), fakeEntity{name: "PayService"})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if doc != "line1\nline2\nline3" {
		t.Errorf("unexpected joined doc: %q", doc)
	}
}

func TestEnhancer_Disambiguate_ReturnsIndex(t *testing.T) {
	m := &mockLLMClient{chooseIdx: 2}
	e := NewEnhancer(m, NewBudget(3, 3))
	cands := []parser.MethodIR{
		{Name: "save", ClassFQN: "a.A"},
		{Name: "save", ClassFQN: "b.B"},
		{Name: "save", ClassFQN: "c.C"},
	}
	idx, err := e.Disambiguate(context.Background(), cands, "persist to DB")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if idx != 2 {
		t.Errorf("expected index 2, got %d", idx)
	}
}

func TestEnhancer_LLMFailure_ReturnsEnhanceFailed(t *testing.T) {
	boom := fmt.Errorf("llm down")
	m := &mockLLMClient{completeErr: boom}
	e := NewEnhancer(m, NewBudget(5, 5))
	_, err := e.EnhanceTag(context.Background(), fakeEntity{})
	if !errors.Is(err, cerrors.ErrEnhanceFailed) {
		t.Fatalf("expected wrapped ErrEnhanceFailed, got %v", err)
	}
}

// staticLLM 无状态 LLM 桩（用于并发用例，避免桩自身计数引发竞争）。
type staticLLM struct{}

func (staticLLM) Complete(context.Context, string) ([]string, error) { return []string{"x"}, nil }
func (staticLLM) Choose(context.Context, string) (int, error)        { return 0, nil }

// TestEnhancer_ConcurrentPhases_NoRace 并发查询 + 扫描共享同一 Enhancer，
// 修复后 phase 不可变、查询预算按请求隔离，-race 下无竞争。
func TestEnhancer_ConcurrentPhases_NoRace(t *testing.T) {
	e := NewEnhancer(staticLLM{}, NewBudget(-1, -1))
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(2)
		go func() {
			defer wg.Done()
			q := e.ForQuery()
			_, _ = q.Disambiguate(context.Background(), []parser.MethodIR{{Name: "x"}}, "h")
		}()
		go func() {
			defer wg.Done()
			_, _ = e.EnhanceTag(context.Background(), fakeEntity{})
		}()
	}
	wg.Wait()
}

// TestEnhancer_QueryBudgetPerRequest 负控（R6-2）：查询预算必须按请求独立，
// 不能第一笔查询耗尽后让后续请求在进程生命周期内永久失效。
func TestEnhancer_QueryBudgetPerRequest(t *testing.T) {
	e := NewEnhancer(staticLLM{}, NewBudget(-1, 1))
	for i := 0; i < 3; i++ {
		if _, err := e.ForQuery().EnhanceTag(context.Background(), fakeEntity{}); err != nil {
			t.Fatalf("第 %d 次请求应获得独立预算，got %v", i+1, err)
		}
	}
}

func TestEnhancer_PhaseSwitchesBudget(t *testing.T) {
	m := &mockLLMClient{completeTags: []string{"x"}}
	// 扫描期限 1、查询期限 1；先耗光扫描期，再走查询作用域应仍可用。
	e := NewEnhancer(m, NewBudget(1, 1))
	if _, err := e.EnhanceTag(context.Background(), fakeEntity{}); err != nil {
		t.Fatalf("scan call should succeed: %v", err)
	}
	if _, err := e.EnhanceTag(context.Background(), fakeEntity{}); !errors.Is(err, cerrors.ErrBudgetExceeded) {
		t.Fatalf("scan budget should be exhausted, got %v", err)
	}
	if _, err := e.ForQuery().EnhanceTag(context.Background(), fakeEntity{}); err != nil {
		t.Fatalf("query call should succeed with its own budget: %v", err)
	}
	if m.completeCalls != 2 {
		t.Errorf("expected 2 Complete calls, got %d", m.completeCalls)
	}
}

func TestEnhancer_AdaptersFromStore(t *testing.T) {
	cls := store.ClassRecord{Name: "OrderSvc", FullName: "com.x.OrderSvc", Type: "class", Doc: "订单服务"}
	ent := NewClassEntity(cls)
	if ent.Name() != "OrderSvc" || ent.QualifiedName() != "com.x.OrderSvc" || ent.Kind() != "class" || ent.DocComment() != "订单服务" {
		t.Errorf("class adapter mismatch: %+v", ent)
	}
	mtd := store.MethodRecord{Name: "create", FullName: "com.x.OrderSvc.create", Doc: "创建订单"}
	me := NewMethodEntity(mtd)
	if me.Kind() != "method" || me.QualifiedName() != "com.x.OrderSvc.create" {
		t.Errorf("method adapter mismatch: %+v", me)
	}
}
