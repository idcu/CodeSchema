package ai

import (
	"context"
	"fmt"
	"strings"

	"gitee.com/idcu-go/metrics"
	"github.com/idcu/codeschema/internal/errors"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// init 注册 AI 增强层指标（差异点可观测性）。
func init() {
	metrics.RegisterCounter("ai_enhance_total", "Total AI enhancement calls", "kind", "phase")
	metrics.RegisterCounter("ai_budget_exceeded_total", "Total AI budget-exceeded skips", "phase")
}

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
//
// 并发约定（R6-2）：Enhancer 不可变，phase 与查询预算在构造时确定；
// 查询路径必须用 ForQuery 取得「本次请求独立预算」的实例，禁止共享可变 phase。
type Enhancer struct {
	client LLMClient
	budget *Budget
	phase  Phase
	query  *QueryBudget // 非 nil 表示查询期实例（每次查询独立）
}

// NewEnhancer 创建 AI 增强器，处于扫描期（PhaseScan）。
func NewEnhancer(client LLMClient, budget *Budget) *Enhancer {
	return &Enhancer{client: client, budget: budget, phase: PhaseScan}
}

// ForQuery 返回一次查询专用的增强器：独立查询预算，互不泄漏，phase 不可变。
func (e *Enhancer) ForQuery() *Enhancer {
	return &Enhancer{
		client: e.client,
		budget: e.budget,
		phase:  PhaseQuery,
		query:  e.budget.NewQueryBudget(),
	}
}

// ResetScanBudget 重置扫描期计数（每次全量扫描开始时调用）。
func (e *Enhancer) ResetScanBudget() { e.budget.ResetScan() }

// BudgetRemaining 返回当前作用域剩余可用预算（不限时返回 -1）。
// 供编排层在调用前做低开销预检，避免空调用 LLM。
func (e *Enhancer) BudgetRemaining() int {
	if e.query != nil {
		return e.query.Remaining()
	}
	return e.budget.ScanRemaining()
}

// phaseName 返回当前作用域名（打点标签）。
func (e *Enhancer) phaseName() string {
	if e.phase == PhaseQuery {
		return "query"
	}
	return "scan"
}

// EnhanceTag 对实体推导并补全标签。预算超限返回 errors.ErrBudgetExceeded。
func (e *Enhancer) EnhanceTag(ctx context.Context, ent IRable) ([]string, error) {
	if !e.consume() {
		metrics.IncCounter("ai_budget_exceeded_total", e.phaseName())
		return nil, errors.ErrBudgetExceeded
	}
	metrics.IncCounter("ai_enhance_total", "tag", e.phaseName())
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
		metrics.IncCounter("ai_budget_exceeded_total", e.phaseName())
		return "", errors.ErrBudgetExceeded
	}
	metrics.IncCounter("ai_enhance_total", "doc", e.phaseName())
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
		metrics.IncCounter("ai_budget_exceeded_total", e.phaseName())
		return 0, errors.ErrBudgetExceeded
	}
	metrics.IncCounter("ai_enhance_total", "disambiguate", e.phaseName())
	idx, err := e.client.Choose(ctx, buildDisambiguationPrompt(candidates, hint))
	if err != nil {
		return 0, fmt.Errorf("%w: %v", errors.ErrEnhanceFailed, err)
	}
	return idx, nil
}

// consume 按当前作用域消费一次预算。
func (e *Enhancer) consume() bool {
	if e.query != nil {
		return e.query.tryConsume()
	}
	return e.budget.tryConsumeScan()
}

// buildTagPrompt 构造标签推导提示词。
//
// 明确约束输出格式为「分类:标签」前缀：六类 biz/tech/risk/test（layer/lang
// 由规则引擎负责，不要求模型产出），每行一个，避免模型把分类名与标签值混淆。
// 分类：biz=业务域、tech=技术特征、risk=风险、test=测试类型。
func buildTagPrompt(ent IRable) string {
	return fmt.Sprintf(
		"为以下代码实体推导标签，每行一个，格式必须是「分类:标签」前缀，"+
			"分类仅限 biz(业务域)/tech(技术特征)/risk(风险)/test(测试类型) 四类，"+
			"不要输出 layer/lang 分类，不要编号或解释：\n名称: %s\n全限定名: %s\n类型: %s\n文档: %s\n示例：\nbiz:订单中心\ntech:缓存\ntest:单元测试",
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
