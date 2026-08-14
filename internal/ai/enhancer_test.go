package ai

import (
	"context"
	"errors"
	"fmt"
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

func TestEnhancer_PhaseSwitchesBudget(t *testing.T) {
	m := &mockLLMClient{completeTags: []string{"x"}}
	// 扫描期限 1、查询期限 1；先耗光扫描期，再切到查询期应仍可用。
	e := NewEnhancer(m, NewBudget(1, 1))
	e.SetPhase(PhaseScan)
	if _, err := e.EnhanceTag(context.Background(), fakeEntity{}); err != nil {
		t.Fatalf("scan call should succeed: %v", err)
	}
	if _, err := e.EnhanceTag(context.Background(), fakeEntity{}); !errors.Is(err, cerrors.ErrBudgetExceeded) {
		t.Fatalf("scan budget should be exhausted, got %v", err)
	}
	e.SetPhase(PhaseQuery)
	if _, err := e.EnhanceTag(context.Background(), fakeEntity{}); err != nil {
		t.Fatalf("query call should succeed after switching phase: %v", err)
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
