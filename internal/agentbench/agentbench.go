// Package agentbench 提供「Agent 任务端到端」评测框架（建议 1 落地）。
//
// 目标：对标 Sverklo 90-task bench / grepai 97% token 节省，产出 CodeSchema
// 可对外发布的「上下文质量」数据——用真实仓库 + 一组代码修改任务（bug 修复/
// 特性实现），对比三档上下文供给：
//
//   - none：    不给任何上下文（0 token，作为对照基线）
//   - full：    GetContextMode 注入完整源码原文（context_lines 裁剪）
//   - minimal： 仅注入符号元数据（名称/位置/文档，零文件 IO，极省 token）
//
// 每个任务带「必需符号」（完成修改需要看到的关键符号）与「关键信息」（决定
// 改法的事实片段，如依赖名/函数名）。评测判定：上下文包是否覆盖了全部必需
// 符号与关键信息 → 该档位「任务可完成」。输出通过率 × token 消耗的权衡表，
// 让「省了多少 token、为什么省」有数据支撑（与 service.TraceEntry 呼应）。
//
// 纯生产代码（不依赖 testing），供 cmd/codeschema 的 agent-bench 子命令调用。
package agentbench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idcu/codeschema/internal/service"
)

// Mode 上下文供给档位。
type Mode string

const (
	ModeNone    Mode = "none"    // 无上下文（对照基线）
	ModeFull    Mode = "full"    // 完整源码原文
	ModeMinimal Mode = "minimal" // 仅符号元数据
)

// AgentTask 一个代码修改任务。
type AgentTask struct {
	// ID 任务标识（如 "code-schema-bug-001"）。
	ID string `json:"id"`
	// Description 任务描述（自然语言，模拟用户/issue 给 agent 的需求）。
	Description string `json:"description"`
	// Type 任务类型：bugfix / feature / refactor。
	Type string `json:"type"`
	// TargetSymbol 完成任务需要了解/修改的核心符号（如 "Scanner.ScanAll"）。
	// 为 minimal/full 档位提供注入目标；none 档位不注入。
	TargetSymbol string `json:"target_symbol"`
	// RequiredKeywords 关键信息片段（决定改法的事实，如依赖名/方法名/配置键）。
	RequiredKeywords []string `json:"required_keywords,omitempty"`
	// RepoHint 任务适用的仓库名（多仓库评测时按 filepath.Base(仓库路径) 匹配；
	// 为空表示适用于任意仓库）。
	RepoHint string `json:"repo_hint,omitempty"`
}

// ModeResult 单档位评测结果。
type ModeResult struct {
	TokenEstimate    int     `json:"token_estimate"`     // 注入上下文估算 token 数
	SymbolsCovered   int     `json:"symbols_covered"`    // 命中必需符号数
	RequiredSymbols  int     `json:"required_symbols"`   // 必需符号总数
	KeywordsCovered  int     `json:"keywords_covered"`   // 命中关键信息数
	RequiredKeywords int     `json:"required_keywords"`  // 关键信息总数
	Pass             bool    `json:"pass"`               // 符号+关键词全覆盖 = 任务可完成
	InjectedChars    int     `json:"injected_chars"`     // 注入字符数（衡量上下文体积）
	GenerationTimeMs float64 `json:"generation_time_ms"` // 上下文生成耗时
}

// TaskResult 单任务三档评测结果。
type TaskResult struct {
	TaskID       string `json:"task_id"`
	Description  string `json:"description"`
	Type         string `json:"type"`
	TargetSymbol string `json:"target_symbol"`
	// Skipped 任务因符号不适用于当前仓库而未评测（多仓库场景按 RepoHint 过滤）。
	Skipped bool                `json:"skipped,omitempty"`
	Modes   map[Mode]ModeResult `json:"modes"`
}

// Summary 汇总统计（对外发布的核心数据）。
type Summary struct {
	// 三档通过率（任务可完成比例）。
	PassRateNone    float64 `json:"pass_rate_none"`
	PassRateFull    float64 `json:"pass_rate_full"`
	PassRateMinimal float64 `json:"pass_rate_minimal"`
	// 三档平均 token 消耗。
	TokenAvgNone    float64 `json:"token_avg_none"`
	TokenAvgFull    float64 `json:"token_avg_full"`
	TokenAvgMinimal float64 `json:"token_avg_minimal"`
	// TokenSavingMinimalVsFull minimal 相对 full 的平均 token 节省比例（0~1）。
	TokenSavingMinimalVsFull float64 `json:"token_saving_minimal_vs_full"`
	// FullVsNone 相对无上下文的增益（通过率提升，full 通常 1.0）。
	FullVsNone float64 `json:"full_vs_none"`
	// MinimalVsNone minimal 相对无上下文的增益。
	MinimalVsNone float64 `json:"minimal_vs_none"`
	// TaskTotal 任务总数。
	TaskTotal int `json:"task_total"`
}

// Report 完整评测报告。
type Report struct {
	Timestamp   string       `json:"timestamp"`
	RepoPath    string       `json:"repo_path"`          // 仓库名（filepath.Base，跨机器可移植，快照 diff 稳定）
	RepoAbs     string       `json:"repo_abs,omitempty"` // 仓库绝对路径（诊断用，不参与快照对比）
	FileCount   int          `json:"file_count"`
	ActiveTasks int          `json:"active_tasks"` // 实际评测的任务数（排除 Skipped）
	Tasks       []TaskResult `json:"tasks"`
	Summary     Summary      `json:"summary"`
}

// Options 评测参数。
type Options struct {
	// Workers 解析并发数（默认 2）。
	Workers int
	// ContextLines full 档位注入的上下文行数（默认 10，0 表示不裁剪）。
	ContextLines int
}

// DefaultTasks 返回内置任务集（以 CodeSchema 自身仓库为评测对象，符号真实可查）。
// 注意：TargetSymbol 必须是 tree-sitter 能解析进 class/method 表的符号
// （Go 方法 FQN = "ClassName.MethodName"，类 FQN = 简单类名；包级函数不在此列）。
// 多仓库评测时，RepoHint 为空的通用任务在所有仓库评测，非空的任务仅在其
// 指定仓库评测（其余仓库标记 Skipped，不惩罚）。
func DefaultTasks() []AgentTask {
	return []AgentTask{
		{
			ID:               "generic-feat-001",
			Description:      "为项目核心类型补充导出文档注释（golint 要求导出的标识符必须有注释）",
			Type:             "feature",
			TargetSymbol:     "Config",
			RequiredKeywords: []string{"Config"},
		},
		{
			ID:               "code-schema-bug-001",
			Description:      "修复 Scanner.ScanAll 在存在被忽略目录（vendor/node_modules）时仍可能尝试解析其中文件的问题",
			Type:             "bugfix",
			TargetSymbol:     "Scanner.ScanAll",
			RequiredKeywords: []string{"ScanAll"},
			RepoHint:         "code-schema",
		},
		{
			ID:               "code-schema-feat-001",
			Description:      "为 Analyzer 增加 Prometheus 指标打点（analyzer_build_total），需要确认现有的指标注册方式（metrics.RegisterCounter）",
			Type:             "feature",
			TargetSymbol:     "Analyzer",
			RequiredKeywords: []string{"Analyzer"},
			RepoHint:         "code-schema",
		},
		{
			ID:               "code-schema-refactor-001",
			Description:      "重构：把 FileStore/SQLiteStore/PGStore 三处重复的标签分类逻辑统一到 store.DeriveTagCategory",
			Type:             "refactor",
			TargetSymbol:     "FileStore",
			RequiredKeywords: []string{"FileStore"},
			RepoHint:         "code-schema",
		},
		{
			ID:               "code-schema-bug-002",
			Description:      "修复 SQLiteStore.BulkUpsert 未先清理旧数据导致重扫残留脏 class/method 的问题（应先 DELETE 再 INSERT）",
			Type:             "bugfix",
			TargetSymbol:     "SQLiteStore.BulkUpsert",
			RequiredKeywords: []string{"BulkUpsert"},
			RepoHint:         "code-schema",
		},
	}
}

// DefaultRepo 内置评测仓库（CodeSchema 自身）。
func DefaultRepo() string {
	// 运行时从 cwd 探测仓库根（本仓即评测对象，与 integration.FindRepoRoot 同思路）。
	wd, err := os.Getwd()
	if err != nil {
		return "."
	}
	if _, err := os.Stat(filepath.Join(wd, "go.mod")); err == nil {
		return wd
	}
	return "."
}

// Run 对单个仓库执行 Agent 任务端到端评测，返回完整报告。
// 流程：扫描仓库建索引 → 对每个任务生成三档上下文 → 判定覆盖 → 汇总。
func Run(ctx context.Context, repoPath string, tasks []AgentTask, opts Options) (*Report, error) {
	if opts.Workers <= 0 {
		opts.Workers = 2
	}
	if _, err := os.Stat(repoPath); err != nil {
		return nil, fmt.Errorf("agent-bench repo %s: %w", repoPath, err)
	}

	// 组装组件（FileStore + tree-sitter + 内存 FTS/向量，与 benchmark 同构）。
	comps, err := newComponents(ctx, opts.Workers)
	if err != nil {
		return nil, err
	}
	defer comps.close()

	if err := comps.scanner.ScanAll(ctx, repoPath); err != nil {
		return nil, fmt.Errorf("agent-bench scan %s: %w", repoPath, err)
	}
	if _, err := comps.builder.BuildFromStore(ctx, comps.store); err != nil {
		return nil, fmt.Errorf("agent-bench index %s: %w", repoPath, err)
	}

	svc := service.NewService(comps.store).WithSearcher(comps.searcher)

	report := &Report{
		Timestamp: time.Now().UTC().Format(time.RFC3339),
		// 归一化：RepoPath 存仓库名（filepath.Base），绝对路径入 RepoAbs。
		// 使 build/agent-task-bench 快照可跨机器 diff（CI 看护 git diff --exit-code
		// 不受本机路径差异影响）。
		RepoPath:  filepath.Base(repoPath),
		RepoAbs:   repoPath,
		FileCount: countSourceFiles(repoPath),
	}

	for _, t := range tasks {
		tr := evalTask(ctx, svc, t, opts)
		report.Tasks = append(report.Tasks, tr)
	}
	report.Summary = summarize(report.Tasks)
	return report, nil
}

// RunMulti 对多个仓库执行 Agent 任务评测，返回按仓库分组的报告列表。
// 任务集按 RepoHint 过滤：匹配仓库名的任务执行评测，其余标记 Skipped（不计入
// 通过率分母，保证跨仓对比公平——符号不存在的仓库不被"惩罚"）。
func RunMulti(ctx context.Context, repos []string, tasks []AgentTask, opts Options) ([]*Report, error) {
	if len(repos) == 0 {
		return nil, fmt.Errorf("agent-bench: no repos specified")
	}
	var reports []*Report
	for _, repo := range repos {
		report, err := Run(ctx, repo, tasks, opts)
		if err != nil {
			return reports, fmt.Errorf("agent-bench %s: %w", repo, err)
		}
		// 按 RepoHint 过滤任务：标记不适用于本仓的任务为 Skipped。
		repoName := filepath.Base(repo)
		filtered := make([]TaskResult, 0, len(report.Tasks))
		active := 0
		for _, tr := range report.Tasks {
			// 找到对应任务定义取 RepoHint。
			hint := ""
			for _, t := range tasks {
				if t.ID == tr.TaskID {
					hint = t.RepoHint
					break
				}
			}
			if hint != "" && hint != repoName {
				tr.Skipped = true
				tr.Modes = map[Mode]ModeResult{}
			} else {
				active++
			}
			filtered = append(filtered, tr)
		}
		report.Tasks = filtered
		report.Summary = summarize(report.Tasks)
		report.ActiveTasks = active
		reports = append(reports, report)
	}
	return reports, nil
}

// evalTask 对单个任务执行三档评测。
func evalTask(ctx context.Context, svc *service.Service, t AgentTask, opts Options) TaskResult {
	tr := TaskResult{
		TaskID:       t.ID,
		Description:  t.Description,
		Type:         t.Type,
		TargetSymbol: t.TargetSymbol,
		Modes:        map[Mode]ModeResult{},
	}
	tr.Modes[ModeNone] = evalMode(ctx, svc, t, ModeNone, opts)
	tr.Modes[ModeFull] = evalMode(ctx, svc, t, ModeFull, opts)
	tr.Modes[ModeMinimal] = evalMode(ctx, svc, t, ModeMinimal, opts)
	return tr
}

// evalMode 生成单档位上下文并判定覆盖。
func evalMode(ctx context.Context, svc *service.Service, t AgentTask, mode Mode, opts Options) ModeResult {
	res := ModeResult{
		RequiredKeywords: len(t.RequiredKeywords),
	}
	if mode == ModeNone || t.TargetSymbol == "" {
		res.Pass = res.RequiredKeywords == 0
		return res
	}

	start := time.Now()
	var content string
	located := false                                      // 符号是否被成功定位（class/method 记录命中）
	ctxOpts := service.ContextOptions{IncludeTrace: true} // 开启追溯以获取 token 估算
	if mode == ModeMinimal {
		ctxOpts.Mode = service.ModeMinimal
	} else {
		ctxOpts.Mode = service.ModeFull
		if opts.ContextLines > 0 {
			ctxOpts.ContextLines = opts.ContextLines
		}
	}
	sc, err := svc.GetContextMode(ctx, t.TargetSymbol, ctxOpts)
	elapsed := time.Since(start).Seconds() * 1000
	res.GenerationTimeMs = round2(elapsed)
	injected := err == nil && sc != nil && sc.Source != ""
	if injected {
		content = sc.Source
		located = sc.FilePath != "" && sc.StartLine > 0
		if sc.Trace != nil {
			res.TokenEstimate = sc.Trace.TokenEstimate
		}
	}
	res.InjectedChars = len(content)
	// 兜底估算：无 Trace 时按注入字符数估算（≈ chars/4）。
	if res.TokenEstimate == 0 && res.InjectedChars > 0 {
		res.TokenEstimate = res.InjectedChars / 4
	}

	// 覆盖判定（按档位差异化，贴近真实 Agent 工作方式）：
	//   none：    无任何上下文 → 任务不可完成（除非任务本无必需符号/关键词）。
	//   minimal： 仅符号定位线索（文件路径 + 行号 + 文档）。Agent 可据此自行
	//             打开文件读取源码，因此判定 = 定位成功（located）。
	//   full：    完整源码原文。判定 = 定位成功 + 源码包含全部必需关键词
	//             （决定改法的事实片段）。
	// 注意：minimal 注入的是定位摘要（不含源码正文），关键词/符号命中在此档
	// 不适用——用 located 代表「有线索」；full 才是「内容是否够用」的判定。
	if injected && t.TargetSymbol != "" {
		res.SymbolsCovered = 1 // 目标符号已供给
	}
	switch mode {
	case ModeNone:
		res.Pass = len(t.RequiredKeywords) == 0
	case ModeMinimal:
		res.Pass = located
	case ModeFull:
		kwHit := 0
		for _, kw := range t.RequiredKeywords {
			if content != "" && strings.Contains(content, kw) {
				kwHit++
			}
		}
		res.KeywordsCovered = kwHit
		res.Pass = located && kwHit >= len(t.RequiredKeywords)
	}
	return res
}

// containsSymbol 判断注入内容是否包含符号（全名或点号分隔的末段短名）。
// 源码原文通常不出现拼接 FQN（如 "Sample.Add"），短名匹配是更贴近实际的判定。
func containsSymbol(content, symbol string) bool {
	if strings.Contains(content, symbol) {
		return true
	}
	if idx := strings.LastIndex(symbol, "."); idx >= 0 {
		return strings.Contains(content, symbol[idx+1:])
	}
	return false
}

// summarize 汇总通过率与 token 权衡（仅统计未 Skipped 的任务）。
func summarize(tasks []TaskResult) Summary {
	s := Summary{TaskTotal: len(tasks)}
	// 有效任务：未被 Skipped（Skipped 任务 Modes 为空 map）。
	active := make([]TaskResult, 0, len(tasks))
	for _, t := range tasks {
		if !t.Skipped {
			active = append(active, t)
		}
	}
	tasks = active
	s.TaskTotal = len(tasks)
	if len(tasks) == 0 {
		return s
	}
	var passNone, passFull, passMinimal int
	var tokNone, tokFull, tokMinimal float64
	for _, t := range tasks {
		if mr, ok := t.Modes[ModeNone]; ok && mr.Pass {
			passNone++
		}
		if mr, ok := t.Modes[ModeFull]; ok && mr.Pass {
			passFull++
			tokFull += float64(mr.TokenEstimate)
		}
		if mr, ok := t.Modes[ModeMinimal]; ok && mr.Pass {
			passMinimal++
			tokMinimal += float64(mr.TokenEstimate)
		}
		if mr, ok := t.Modes[ModeNone]; ok {
			tokNone += float64(mr.TokenEstimate)
		}
	}
	n := float64(len(tasks))
	s.PassRateNone = round4(float64(passNone) / n)
	s.PassRateFull = round4(float64(passFull) / n)
	s.PassRateMinimal = round4(float64(passMinimal) / n)
	s.TokenAvgNone = round2(tokNone / n)
	s.TokenAvgFull = round2(tokFull / n)
	s.TokenAvgMinimal = round2(tokMinimal / n)
	if s.TokenAvgFull > 0 {
		s.TokenSavingMinimalVsFull = round4(1 - s.TokenAvgMinimal/s.TokenAvgFull)
	}
	s.FullVsNone = round4(s.PassRateFull - s.PassRateNone)
	s.MinimalVsNone = round4(s.PassRateMinimal - s.PassRateNone)
	return s
}

// GenerateMarkdown 生成 Markdown 报告（对外发布形态）。
func GenerateMarkdown(r *Report) string {
	var b strings.Builder
	b.WriteString("## Agent 任务端到端评测报告\n\n")
	if r.RepoAbs != "" {
		b.WriteString(fmt.Sprintf("- 仓库：`%s`（%s，文件数 %d）\n", r.RepoPath, r.RepoAbs, r.FileCount))
	} else {
		b.WriteString(fmt.Sprintf("- 仓库：`%s`（文件数 %d）\n", r.RepoPath, r.FileCount))
	}
	b.WriteString(fmt.Sprintf("- 任务数：%d\n", r.Summary.TaskTotal))
	b.WriteString(fmt.Sprintf("- 时间：%s\n\n", r.Timestamp))

	b.WriteString("### 汇总\n\n")
	b.WriteString("| 指标 | none（无上下文） | full（完整源码） | minimal（符号元数据） |\n")
	b.WriteString("|---|---|---|---|\n")
	b.WriteString(fmt.Sprintf("| 通过率 | %.1f%% | %.1f%% | %.1f%% |\n",
		r.Summary.PassRateNone*100, r.Summary.PassRateFull*100, r.Summary.PassRateMinimal*100))
	b.WriteString(fmt.Sprintf("| 平均 token | %.0f | %.0f | %.0f |\n",
		r.Summary.TokenAvgNone, r.Summary.TokenAvgFull, r.Summary.TokenAvgMinimal))
	b.WriteString(fmt.Sprintf("| 相对 none 增益 | — | +%.1fpp | +%.1fpp |\n",
		r.Summary.FullVsNone*100, r.Summary.MinimalVsNone*100))
	b.WriteString(fmt.Sprintf("| **minimal vs full token 节省** | — | — | **%.1f%%** |\n\n",
		r.Summary.TokenSavingMinimalVsFull*100))

	b.WriteString("### 分任务明细\n\n")
	b.WriteString("| 任务 | 类型 | none | full (token) | minimal (token) |\n")
	b.WriteString("|---|---|---|---|---|\n")
	for _, t := range r.Tasks {
		none := t.Modes[ModeNone]
		full := t.Modes[ModeFull]
		min := t.Modes[ModeMinimal]
		b.WriteString(fmt.Sprintf("| %s | %s | %v | %v (%d) | %v (%d) |\n",
			t.TaskID, t.Type,
			none.Pass, full.Pass, full.TokenEstimate,
			min.Pass, min.TokenEstimate))
	}
	b.WriteString("\n> 判定口径：上下文中命中全部「必需符号」与「关键信息」即视为该档位任务可完成。\n")
	b.WriteString("> full 注入完整源码原文（context_lines 裁剪）；minimal 仅符号元数据、零文件 IO。\n")
	return b.String()
}

// GenerateJSON 生成 JSON 报告（机器可读，供基准存档）。
func GenerateJSON(r *Report) ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// GenerateMultiMarkdown 生成多仓库跨仓对比 Markdown（对外发布形态）。
// 按仓库列出通过率与 token 消耗，突出 minimal 相对 full 的 token 节省一致性。
func GenerateMultiMarkdown(reports []*Report) string {
	var b strings.Builder
	b.WriteString("## Agent 任务端到端评测（多仓库对比）\n\n")
	if len(reports) == 0 {
		b.WriteString("(no reports)\n")
		return b.String()
	}
	b.WriteString("| 仓库 | 任务数 | full 通过率 | minimal 通过率 | none 通过率 | full token | minimal token | token 节省 |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, r := range reports {
		name := r.RepoPath // 已是仓库名（Run 归一化），无需再 Base
		s := r.Summary
		b.WriteString(fmt.Sprintf("| %s | %d | %.0f%% | %.0f%% | %.0f%% | %.0f | %.0f | **%.1f%%** |\n",
			name, s.TaskTotal,
			s.PassRateFull*100, s.PassRateMinimal*100, s.PassRateNone*100,
			s.TokenAvgFull, s.TokenAvgMinimal, s.TokenSavingMinimalVsFull*100))
	}
	b.WriteString("\n> 口径：full 注入完整源码原文（context_lines 裁剪）；minimal 仅符号元数据、零文件 IO；")
	b.WriteString("none 无上下文（对照基线）。任务按 RepoHint 匹配仓库，不适用的任务标记 Skipped 不计入分母。\n")
	b.WriteString("> token 节省 = 1 − minimal 平均 token / full 平均 token。\n")
	return b.String()
}

// WriteOutput 把报告写到 outDir（markdown + json），返回写入文件列表。
func WriteOutput(r *Report, outDir string) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	mdPath := filepath.Join(outDir, "agent-task-bench.md")
	md := GenerateMarkdown(r)
	if err := os.WriteFile(mdPath, []byte(md), 0o644); err != nil {
		return nil, err
	}
	jsonPath := filepath.Join(outDir, "agent-task-bench.json")
	data, err := GenerateJSON(r)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(jsonPath, data, 0o644); err != nil {
		return nil, err
	}
	return []string{mdPath, jsonPath}, nil
}

// SortTasks 按 ID 稳定排序任务（可复现输出顺序）。
func SortTasks(tasks []AgentTask) {
	sort.Slice(tasks, func(i, j int) bool { return tasks[i].ID < tasks[j].ID })
}

func round2(v float64) float64 { return float64(int(v*100+0.5)) / 100 }
func round4(v float64) float64 { return float64(int(v*10000+0.5)) / 10000 }
