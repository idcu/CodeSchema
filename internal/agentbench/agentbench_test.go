// Package agentbench 单元测试：评测逻辑（覆盖判定）+ 报告生成 + 汇总。
package agentbench

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idcu/codeschema/internal/service"
)

// TestEvalMode_None 无上下文档位：不注入任何内容，任务不可完成。
func TestEvalMode_None(t *testing.T) {
	svc := newTestService(t)
	task := AgentTask{
		ID:               "t1",
		TargetSymbol:     "Sample.Add",
		RequiredKeywords: []string{"Add"},
	}
	res := evalMode(context.Background(), svc, task, ModeNone, Options{})
	if res.Pass {
		t.Fatalf("none mode should not pass with required keywords")
	}
	if res.InjectedChars != 0 {
		t.Fatalf("none mode injected chars = %d, want 0", res.InjectedChars)
	}
	// 无必需要求时 none 视为「无任务信息需求」，通过（语义占位）。
	task2 := AgentTask{ID: "t-empty"}
	res2 := evalMode(context.Background(), svc, task2, ModeNone, Options{})
	if !res2.Pass {
		t.Fatalf("none mode with no requirements should pass by definition")
	}
}

// TestEvalMode_Full_Minimal 全量与极简档位：full 命中源码关键词，minimal 看定位线索。
func TestEvalMode_Full_Minimal(t *testing.T) {
	svc := newTestService(t)
	task := AgentTask{
		ID:               "t1",
		TargetSymbol:     "Sample.Add",
		RequiredKeywords: []string{"s.A + b"},
	}
	full := evalMode(context.Background(), svc, task, ModeFull, Options{ContextLines: 20})
	if !full.Pass {
		t.Fatalf("full mode should cover source keywords: %+v", full)
	}
	if full.TokenEstimate <= 0 {
		t.Fatalf("full mode token estimate should be > 0, got %d", full.TokenEstimate)
	}

	minimal := evalMode(context.Background(), svc, task, ModeMinimal, Options{})
	if !minimal.Pass {
		t.Fatalf("minimal mode should pass when symbol located: %+v", minimal)
	}
	// minimal 注入 token 应显著小于 full。
	if minimal.TokenEstimate >= full.TokenEstimate {
		t.Fatalf("minimal token (%d) should be < full token (%d)", minimal.TokenEstimate, full.TokenEstimate)
	}
}

// TestEvalMode_MissingSymbol 目标符号不存在：full/minimal 均失败（不 panic）。
func TestEvalMode_MissingSymbol(t *testing.T) {
	svc := newTestService(t)
	task := AgentTask{
		ID:               "t-missing",
		TargetSymbol:     "NoSuchSymbol.DoesNotExist",
		RequiredKeywords: []string{"x"},
	}
	for _, mode := range []Mode{ModeFull, ModeMinimal} {
		res := evalMode(context.Background(), svc, task, mode, Options{})
		if res.Pass {
			t.Fatalf("%s mode should not pass for missing symbol: %+v", mode, res)
		}
	}
}

// TestSummarize 汇总计算：通过率 / token 节省比例。
func TestSummarize(t *testing.T) {
	tasks := []TaskResult{
		{
			TaskID: "a",
			Modes: map[Mode]ModeResult{
				ModeNone:    {Pass: false},
				ModeFull:    {Pass: true, TokenEstimate: 400},
				ModeMinimal: {Pass: true, TokenEstimate: 40},
			},
		},
		{
			TaskID: "b",
			Modes: map[Mode]ModeResult{
				ModeNone:    {Pass: false},
				ModeFull:    {Pass: true, TokenEstimate: 800},
				ModeMinimal: {Pass: false, TokenEstimate: 80},
			},
		},
	}
	s := summarize(tasks)
	if s.TaskTotal != 2 {
		t.Fatalf("task total = %d, want 2", s.TaskTotal)
	}
	if s.PassRateFull != 1.0 {
		t.Fatalf("full pass rate = %v, want 1.0", s.PassRateFull)
	}
	if s.PassRateMinimal != 0.5 {
		t.Fatalf("minimal pass rate = %v, want 0.5", s.PassRateMinimal)
	}
	if s.TokenAvgFull != 600 {
		t.Fatalf("full avg token = %v, want 600", s.TokenAvgFull)
	}
	// 仅统计通过任务的 minimal token：40 / 2 任务 → 平均 20
	if s.TokenAvgMinimal != 20 {
		t.Fatalf("minimal avg token = %v, want 20", s.TokenAvgMinimal)
	}
	if s.TokenSavingMinimalVsFull <= 0 || s.TokenSavingMinimalVsFull > 0.97 {
		t.Fatalf("token saving out of range: %v", s.TokenSavingMinimalVsFull)
	}
}

// TestGenerateMarkdown 报告生成：包含关键字段。
func TestGenerateMarkdown(t *testing.T) {
	r := &Report{
		Timestamp: "2026-08-17T00:00:00Z",
		RepoPath:  "/tmp/repo",
		FileCount: 42,
		Tasks: []TaskResult{
			{
				TaskID: "t1",
				Type:   "bugfix",
				Modes: map[Mode]ModeResult{
					ModeNone:    {Pass: false},
					ModeFull:    {Pass: true, TokenEstimate: 400},
					ModeMinimal: {Pass: true, TokenEstimate: 40},
				},
			},
		},
	}
	r.Summary = summarize(r.Tasks)
	md := GenerateMarkdown(r)
	for _, want := range []string{"Agent 任务端到端评测报告", "通过率", "token 节省", "t1", "bugfix"} {
		if !strings.Contains(md, want) {
			t.Fatalf("markdown missing %q", want)
		}
	}
}

// TestGenerateJSON 报告 JSON 序列化。
func TestGenerateJSON(t *testing.T) {
	r := &Report{Timestamp: "2026-08-17T00:00:00Z", RepoPath: "/r", Tasks: []TaskResult{}}
	r.Summary = summarize(r.Tasks)
	data, err := GenerateJSON(r)
	if err != nil {
		t.Fatalf("GenerateJSON: %v", err)
	}
	for _, want := range []string{`"timestamp"`, `"pass_rate_full"`, `"summary"`} {
		if !strings.Contains(string(data), want) {
			t.Fatalf("json missing %q: %s", want, data)
		}
	}
}

// TestWriteOutput 输出文件写入（md + json）。
func TestWriteOutput(t *testing.T) {
	dir := t.TempDir()
	r := &Report{Timestamp: "x", RepoPath: "/r"}
	r.Summary = summarize(nil)
	paths, err := WriteOutput(r, dir)
	if err != nil {
		t.Fatalf("WriteOutput: %v", err)
	}
	if len(paths) != 2 {
		t.Fatalf("expected 2 output files, got %d", len(paths))
	}
	for _, p := range paths {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("output missing: %s", p)
		}
	}
}

// TestSortTasks 任务排序可复现。
func TestSortTasks(t *testing.T) {
	tasks := []AgentTask{{ID: "b"}, {ID: "a"}, {ID: "c"}}
	SortTasks(tasks)
	if tasks[0].ID != "a" || tasks[1].ID != "b" || tasks[2].ID != "c" {
		t.Fatalf("sort failed: %+v", tasks)
	}
}

// newTestService 用临时仓库构建 Service（真实 Scanner+Store+分析器）。
func newTestService(t *testing.T) *service.Service {
	t.Helper()
	dir := t.TempDir()
	// 构造一个最小可解析的 Go 文件（含类与方法，供符号解析）。
	src := `package sample

// Sample 一个测试类。
type Sample struct{ A, B int }

// Add 加法。
func (s *Sample) Add() int {
	b := s.B
	return s.A + b
}
`
	if err := os.WriteFile(filepath.Join(dir, "sample.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	comps, err := newComponents(ctx, 2)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(comps.close)
	if err := comps.scanner.ScanAll(ctx, dir); err != nil {
		t.Fatal(err)
	}
	if _, err := comps.builder.BuildFromStore(ctx, comps.store); err != nil {
		t.Fatal(err)
	}
	return service.NewService(comps.store).WithSearcher(comps.searcher)
}

// TestRun_RealRepo 以本仓库为评测对象跑通 Run 全流程（真实扫描+索引+评测）。
// 报告快照受 testutil.BenchOutPath 防护：默认写临时目录，CI 不触碰 build/。
func TestRun_RealRepo(t *testing.T) {
	root := repoRoot()
	if root == "" {
		t.Skip("未定位到仓库根，跳过真实仓库评测")
	}
	report, err := Run(context.Background(), root, DefaultTasks(), Options{Workers: 2, ContextLines: 10})
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if report == nil || len(report.Tasks) != len(DefaultTasks()) {
		t.Fatalf("expected %d tasks, got %d", len(DefaultTasks()), len(report.Tasks))
	}
	// 汇总口径：full 通过率 >= minimal >= none（上下文供给有信息量梯度）。
	if report.Summary.PassRateNone > report.Summary.PassRateFull {
		t.Fatalf("none pass rate (%v) should not exceed full (%v)", report.Summary.PassRateNone, report.Summary.PassRateFull)
	}
	// full token 应显著大于 minimal（token 节省成立）。
	if report.Summary.TokenAvgMinimal >= report.Summary.TokenAvgFull {
		t.Fatalf("minimal token (%v) should be < full token (%v)", report.Summary.TokenAvgMinimal, report.Summary.TokenAvgFull)
	}
	// JSON 可序列化（对外发布形态）。
	data, err := GenerateJSON(report)
	if err != nil {
		t.Fatalf("GenerateJSON: %v", err)
	}
	if len(data) == 0 {
		t.Fatal("empty JSON report")
	}
}

// repoRoot 向上查找 go.mod 所在目录（仓库根）。
func repoRoot() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return ""
		}
		dir = parent
	}
}

// TestRunMulti_RepoHintFilter 多仓库评测：按 RepoHint 过滤任务，Skipped 不计入分母。
func TestRunMulti_RepoHintFilter(t *testing.T) {
	root := repoRoot()
	if root == "" {
		t.Skip("未定位到仓库根，跳过")
	}
	// 用本仓 + 一个临时小仓（不含 code-schema 专属符号）。
	other := t.TempDir()
	src := "package other\n\ntype Config struct{ X int }\n\nfunc (c *Config) Load() int { return c.X }\n"
	if err := os.WriteFile(filepath.Join(other, "config.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	reports, err := RunMulti(context.Background(), []string{root, other}, DefaultTasks(), Options{Workers: 2, ContextLines: 10})
	if err != nil {
		t.Fatalf("RunMulti: %v", err)
	}
	if len(reports) != 2 {
		t.Fatalf("expected 2 reports, got %d", len(reports))
	}
	// 本仓：5 任务 active（4 code-schema + generic-feat-001 Config 符号存在；
	// 3 个 OrderService 通用任务经符号预检 Skipped——本仓无该符号）。
	if reports[0].ActiveTasks != 5 {
		t.Fatalf("repo1 active tasks = %d, want 5", reports[0].ActiveTasks)
	}
	// 临时仓：仅 generic-feat-001 active（Config 符号存在），其余 7 个 Skipped。
	if reports[1].ActiveTasks != 1 {
		t.Fatalf("repo2 active tasks = %d, want 1", reports[1].ActiveTasks)
	}
	var skipped int
	for _, tr := range reports[1].Tasks {
		if tr.Skipped {
			skipped++
		}
	}
	if skipped != 7 {
		t.Fatalf("repo2 skipped tasks = %d, want 7", skipped)
	}
	// 跨仓对比 Markdown 可生成。
	md := GenerateMultiMarkdown(reports)
	for _, want := range []string{"多仓库对比", "token 节省"} {
		if !strings.Contains(md, want) {
			t.Fatalf("multi markdown missing %q", want)
		}
	}
}

// TestSummarize_SkippedExcluded Skipped 任务不计入通过率分母。
func TestSummarize_SkippedExcluded(t *testing.T) {
	tasks := []TaskResult{
		{TaskID: "a", Modes: map[Mode]ModeResult{
			ModeNone: {Pass: false}, ModeFull: {Pass: true, TokenEstimate: 100}, ModeMinimal: {Pass: true, TokenEstimate: 10},
		}},
		{TaskID: "b", Skipped: true, Modes: map[Mode]ModeResult{}},
	}
	s := summarize(tasks)
	if s.TaskTotal != 1 {
		t.Fatalf("task total = %d, want 1 (skipped excluded)", s.TaskTotal)
	}
	if s.PassRateFull != 1.0 {
		t.Fatalf("full pass rate = %v, want 1.0", s.PassRateFull)
	}
}

// TestEvalTask_SymbolPresencePrecheck 符号预检：目标符号不在仓库时任务标记
// Skipped（不拉低通过率），与 RepoHint 过滤语义一致。
func TestEvalTask_SymbolPresencePrecheck(t *testing.T) {
	svc := newTestService(t)
	// 不存在符号的任务 → Skipped。
	task := AgentTask{ID: "t-missing", TargetSymbol: "NoSuch.Class", RequiredKeywords: []string{"x"}}
	tr := evalTask(context.Background(), svc, task, Options{})
	if !tr.Skipped {
		t.Fatalf("expected skipped for missing symbol, got %+v", tr)
	}
	// 存在符号的任务 → active（不 Skipped）。
	task2 := AgentTask{ID: "t-ok", TargetSymbol: "Sample.Add", RequiredKeywords: []string{"Add"}}
	tr2 := evalTask(context.Background(), svc, task2, Options{})
	if tr2.Skipped {
		t.Fatalf("expected active for existing symbol, got %+v", tr2)
	}
}

// TestDefaultTasks_MultiLanguage 内置任务集包含多语言通用任务（符号约定：
// 类 FQN = 简单类名，方法 FQN = "ClassName.MethodName"，与 tree-sitter 一致）。
func TestDefaultTasks_MultiLanguage(t *testing.T) {
	tasks := DefaultTasks()
	byID := map[string]AgentTask{}
	for _, t := range tasks {
		byID[t.ID] = t
	}
	for _, id := range []string{"generic-bug-001", "generic-bug-002", "generic-refactor-001"} {
		tk, ok := byID[id]
		if !ok {
			t.Fatalf("missing multi-language task %s", id)
		}
		if tk.RepoHint != "" {
			t.Fatalf("generic task %s should have empty RepoHint, got %q", id, tk.RepoHint)
		}
	}
	// 任务总数 = 4 code-schema + 4 generic。
	if len(tasks) != 8 {
		t.Fatalf("expected 8 tasks, got %d", len(tasks))
	}
}
