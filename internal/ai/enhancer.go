package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/idcu/codeschema/internal/errors"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// Phase 表示 AI 增强所处的调用作用域，决定消耗哪类预算。
type Phase int

const (
	// PhaseScan 全量扫描期（对应 budget_per_scan）。
	PhaseScan Phase = iota
	// PhaseQuery 单次查询期（对应 budget_per_query）。
	PhaseQuery
)

// IRable 是 AI 增强层可增强的实体抽象，解耦于具体存储记录类型。
//
// 生产侧通过 NewClassEntity / NewMethodEntity 将 store 记录适配为 IRable；
// 测试侧可用自定义桩实现。
type IRable interface {
	Name() string
	QualifiedName() string
	DocComment() string
	Kind() string
}

// classEntity 将 store.ClassRecord 适配为 IRable。
type classEntity struct {
	rec store.ClassRecord
}

func (e classEntity) Name() string          { return e.rec.Name }
func (e classEntity) QualifiedName() string { return e.rec.FullName }
func (e classEntity) DocComment() string    { return e.rec.Doc }
func (e classEntity) Kind() string          { return e.rec.Type }

// NewClassEntity 将类记录适配为 AI 可增强实体。
func NewClassEntity(rec store.ClassRecord) IRable { return classEntity{rec: rec} }

// methodEntity 将 store.MethodRecord 适配为 IRable。
type methodEntity struct {
	rec store.MethodRecord
}

func (e methodEntity) Name() string          { return e.rec.Name }
func (e methodEntity) QualifiedName() string { return e.rec.FullName }
func (e methodEntity) DocComment() string    { return e.rec.Doc }
func (e methodEntity) Kind() string          { return "method" }

// NewMethodEntity 将方法记录适配为 AI 可增强实体。
func NewMethodEntity(rec store.MethodRecord) IRable { return methodEntity{rec: rec} }

// Enhancer 通过 LLMClient 对实体做标签补全 / 文档补全 / 同名方法消歧，
// 并受 Budget 硬限约束。所有失败均返回错误且不影响主流程（调用方应记录日志后继续）。
type Enhancer struct {
	client LLMClient
	budget *Budget
	phase  Phase
}

// NewEnhancer 创建 AI 增强器，默认处于扫描期（PhaseScan）。
func NewEnhancer(client LLMClient, budget *Budget) *Enhancer {
	return &Enhancer{client: client, budget: budget, phase: PhaseScan}
}

// SetPhase 切换作用域（扫描 / 查询），决定消耗哪类预算。
func (e *Enhancer) SetPhase(p Phase) { e.phase = p }

// EnhanceTag 对实体推导并补全标签。预算超限返回 errors.ErrBudgetExceeded。
func (e *Enhancer) EnhanceTag(ctx context.Context, ent IRable) ([]string, error) {
	if !e.consume() {
		return nil, errors.ErrBudgetExceeded
	}
	tags, err := e.client.Complete(ctx, buildTagPrompt(ent))
	if err != nil {
		return nil, fmt.Errorf("%w: %v", errors.ErrEnhanceFailed, err)
	}
	return tags, nil
}

// EnhanceDoc 为残缺的 doc 生成补充描述（多行拼接为单段文本）。
// 预算超限返回 errors.ErrBudgetExceeded。
func (e *Enhancer) EnhanceDoc(ctx context.Context, ent IRable) (string, error) {
	if !e.consume() {
		return "", errors.ErrBudgetExceeded
	}
	lines, err := e.client.Complete(ctx, buildDocPrompt(ent))
	if err != nil {
		return "", fmt.Errorf("%w: %v", errors.ErrEnhanceFailed, err)
	}
	return strings.Join(lines, "\n"), nil
}

// Disambiguate 对同名方法进行消歧，返回候选列表中最佳项的索引。
// 预算超限返回 errors.ErrBudgetExceeded。
func (e *Enhancer) Disambiguate(ctx context.Context, candidates []parser.MethodIR, hint string) (int, error) {
	if !e.consume() {
		return 0, errors.ErrBudgetExceeded
	}
	idx, err := e.client.Choose(ctx, buildDisambiguationPrompt(candidates, hint))
	if err != nil {
		return 0, fmt.Errorf("%w: %v", errors.ErrEnhanceFailed, err)
	}
	return idx, nil
}

// consume 按当前作用域消费一次预算。
func (e *Enhancer) consume() bool {
	if e.phase == PhaseQuery {
		return e.budget.tryConsumeQuery()
	}
	return e.budget.tryConsumeScan()
}

// buildTagPrompt 构造标签推导提示词。
func buildTagPrompt(ent IRable) string {
	return fmt.Sprintf(
		"为以下代码实体推导业务(biz)/技术(tech)/风险(risk)/测试(test)标签，"+
			"每行一个标签，不要编号：\n名称: %s\n全限定名: %s\n类型: %s\n文档: %s",
		ent.Name(), ent.QualifiedName(), ent.Kind(), ent.DocComment(),
	)
}

// buildDocPrompt 构造文档补全提示词。
func buildDocPrompt(ent IRable) string {
	return fmt.Sprintf(
		"为以下代码实体补全一段简洁的中文功能说明（3-5 句），直接输出说明文本：\n"+
			"名称: %s\n全限定名: %s\n类型: %s\n现有文档: %s",
		ent.Name(), ent.QualifiedName(), ent.Kind(), ent.DocComment(),
	)
}

// buildDisambiguationPrompt 构造同名方法消歧提示词。
func buildDisambiguationPrompt(candidates []parser.MethodIR, hint string) string {
	var b strings.Builder
	b.WriteString("以下同名方法需要消歧，请选择与提示最匹配的一个（只输出索引，从 0 开始）：\n")
	b.WriteString("提示: ")
	b.WriteString(hint)
	b.WriteString("\n")
	for i, m := range candidates {
		fmt.Fprintf(&b, "[%d] %s%s 签名:%s 文档:%s\n",
			i, m.ClassFQN, m.Name, m.Signature, m.Doc)
	}
	return b.String()
}
