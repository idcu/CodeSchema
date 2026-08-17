// Package service 提供 CodeSchema 系统的业务逻辑层。
//
// 封装 Store 操作，为 HTTP API 和 MCP Server 提供统一查询接口。
// P0 阶段实现基础查询骨架，P1 阶段接入真实数据。
package service

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"time"

	"github.com/idcu/codeschema/internal/ai"
	"github.com/idcu/codeschema/internal/analyzer"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/search"
	"github.com/idcu/codeschema/internal/store"
)

// Service 是业务逻辑层，封装所有查询操作。
type Service struct {
	store        store.Store
	startTime    time.Time
	searcher     *search.Searcher
	indexBuilder *search.IndexBuilder // P8.3 自动索引构建

	// analyzer 供影响面分析（GetImpact）使用；未注入时 GetImpact 返回空（向后兼容）。
	analyzer *analyzer.Analyzer

	// enhancer 供查询期同名方法消歧（Disambiguate）使用；
	// 未注入或 LLM 不可用时搜索不消歧（结果原样返回，向后兼容）。
	enhancer *ai.Enhancer

	// coverage 保存「测试方法 FQN → 其覆盖的生产方法 FQN 列表」的映射，
	// 由 SetCoverage / LoadCoverageJSON 注入，供 coverage 测试关联策略反查。
	coverage map[string][]string
}

// NewService 创建 Service 实例。
func NewService(st store.Store) *Service {
	return &Service{
		store:     st,
		startTime: time.Now(),
	}
}

// WithSearcher 设置双路检索器，启用语义搜索能力。
//
// 当 searcher 为 nil 时，Search 方法回退到 P0 占位行为（返回空结果）。
func (s *Service) WithSearcher(searcher *search.Searcher) *Service {
	s.searcher = searcher
	return s
}

// WithIndexBuilder 设置自动索引构建器，在扫描后自动更新搜索索引。
func (s *Service) WithIndexBuilder(b *search.IndexBuilder) *Service {
	s.indexBuilder = b
	return s
}

// WithImpactAnalyzer 注入代码图分析器，启用真实调用图影响面分析（含关联单测）。
func (s *Service) WithImpactAnalyzer(a *analyzer.Analyzer) *Service {
	s.analyzer = a
	return s
}

// WithAIEnhancer 注入 AI 增强层，启用查询期同名方法消歧（Disambiguate）。
//
// 未注入或 LLM 不可用时，搜索不消歧、结果原样返回（向后兼容）；
// 消歧失败（预算超限/LLM 错误）同样回退到原始结果，不影响主流程。
func (s *Service) WithAIEnhancer(e *ai.Enhancer) *Service {
	s.enhancer = e
	return s
}

// SetCoverage 注入覆盖率报告，供 coverage 测试关联策略反查。
//
// 入参映射：测试方法 FQN → 其覆盖的生产方法 FQN 列表。
// 例如：{"com.x.OrderServiceTest.testGetOrder": ["com.x.OrderService.getOrder"]}。
func (s *Service) SetCoverage(report map[string][]string) {
	s.coverage = report
}

// LoadCoverageJSON 从 JSON 读取器解析覆盖率报告并注入。
//
// JSON 格式：对象，键为测试方法 FQN，值为被覆盖的生产方法 FQN 数组。
// 解析失败返回错误（不污染已注入的 coverage）。
func (s *Service) LoadCoverageJSON(r io.Reader) error {
	var report map[string][]string
	if err := json.NewDecoder(r).Decode(&report); err != nil {
		return fmt.Errorf("decode coverage json: %w", err)
	}
	s.coverage = report
	return nil
}

// BuildIndex 从 Store 全量构建搜索索引。
//
// 返回构建统计信息，包括文档数、索引数、耗时等。
func (s *Service) BuildIndex(ctx context.Context) (*search.BuildResult, error) {
	if s.indexBuilder == nil {
		return &search.BuildResult{}, nil
	}
	return s.indexBuilder.BuildFromStore(ctx, s.store)
}

// HealthStatus 健康检查响应。
type HealthStatus struct {
	Status    string `json:"status"`
	Uptime    string `json:"uptime"`
	StoreOK   bool   `json:"store_ok"`
	StoreType string `json:"store_type"`
}

// Health 返回系统健康状态。
func (s *Service) Health(ctx context.Context) *HealthStatus {
	status := &HealthStatus{
		Uptime:    time.Since(s.startTime).Round(time.Second).String(),
		StoreType: "file",
	}
	if err := s.store.HealthCheck(ctx); err != nil {
		status.Status = "degraded"
		status.StoreOK = false
	} else {
		status.Status = "ok"
		status.StoreOK = true
	}
	return status
}

// StoreHealthCheck 执行存储层健康检查。
func (s *Service) StoreHealthCheck(ctx context.Context) error {
	return s.store.HealthCheck(ctx)
}

// TraceEntry 上下文注入追溯记录，记录每次注入的来源、裁剪依据与时间戳。
// 对齐 DeepSeek Harness 的 append-only 轨迹思想：让每次上下文注入"有迹可循"，
// 可量化省了多少 token、为什么省（与 agentic grep 对比的基线数据）。
type TraceEntry struct {
	Source        string `json:"source"`         // 来源：如 "store.GetContext" / "store.GetImpact"
	HitSymbols    int    `json:"hit_symbols"`    // 命中符号数
	HitLines      int    `json:"hit_lines"`      // 命中行数（实际注入行数）
	TrimReason    string `json:"trim_reason"`    // 裁剪原因：如 "context_lines" / "mode_minimal" / "full" / "file_unreadable"
	TrimmedLines  int    `json:"trimmed_lines"`  // 裁剪行数（文件总行 - 注入行）
	TokenEstimate int    `json:"token_estimate"` // 估算 token 数（≈ 注入行数 × 4）
	Timestamp     string `json:"timestamp"`      // ISO 8601 时间戳
}

// ContextMode 上下文注入模式：控制"喂给 Agent 的内容形态"。
//   - ModeFull（默认）：注入真实源码原文（方法体/类体），可配 context_lines 裁剪；
//   - ModeMinimal：仅注入符号元数据（签名/文档/行列区间），不喂源码原文，
//     作为「极简上下文模式」评测基线，用于与全文档对照产出 token 节省数据。
type ContextMode string

const (
	ModeFull    ContextMode = "full"    // 注入源码原文（默认）
	ModeMinimal ContextMode = "minimal" // 仅符号 + 行号，不喂源码原文
)

// ContextOptions 上下文注入选项。
type ContextOptions struct {
	// ContextLines 上下文裁剪行数（mode=full 生效）：
	//   - 0：注入符号完整内容（方法/类全量，不裁剪）；
	//   - N>0：注入符号体并前后各附带 N 行上下文（夹在文件边界内）。
	ContextLines int
	// Mode 注入模式：ModeFull（默认）或 ModeMinimal。
	Mode ContextMode
	// IncludeTrace 是否在响应中附加 _trace 追溯字段（默认 true）。
	IncludeTrace bool
}

// SymbolContext 符号上下文响应。
type SymbolContext struct {
	Symbol       string      `json:"symbol"`
	Source       string      `json:"source"`
	FilePath     string      `json:"file_path"`
	StartLine    int         `json:"start_line"`
	EndLine      int         `json:"end_line"`
	Doc          string      `json:"doc,omitempty"`
	RelatedTests []string    `json:"related_tests,omitempty"`
	Trace        *TraceEntry `json:"_trace,omitempty"` // 追溯日志（仅供调试/审计，不公开）
}

// GetContext 获取指定符号的上下文（contextLines=0 表示完整内容，>0 前后裁剪）。
//
// 向后兼容的默认行为：contextLines<=0 视为 0（注入符号完整内容，不再按旧占位
// 默认 5 行）。真实实现从 Store 解析符号位置并从磁盘读取源码注入，附带 _trace
// 追溯字段（建议 2：上下文注入追溯）。
func (s *Service) GetContext(ctx context.Context, symbol string, contextLines int) (*SymbolContext, error) {
	return s.GetContextMode(ctx, symbol, ContextOptions{
		ContextLines: contextLines,
		Mode:         ModeFull,
		IncludeTrace: true,
	})
}

// GetContextMode 按指定选项获取符号上下文，支持 full/minimal 两种注入模式。
//
// 定位逻辑：
//   - 先按类 FullName 精确匹配，再按方法 FullName 精确匹配（与 GetTags 一致）；
//   - 命中后读取磁盘源码，按 context_lines 语义裁剪；
//   - minimal 模式只返回签名/文档/行列区间，不读源码原文（零文件 IO 的快路径）。
func (s *Service) GetContextMode(ctx context.Context, symbol string, opts ContextOptions) (*SymbolContext, error) {
	if symbol == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "symbol is required"}
	}
	if opts.Mode == "" {
		opts.Mode = ModeFull
	}

	loc, ok := s.resolveSymbolLocation(ctx, symbol)
	if !ok {
		return nil, &ServiceError{Code: "ERR_SYMBOL_NOT_FOUND", Message: fmt.Sprintf("symbol not found: %s", symbol)}
	}

	// minimal 模式：不读源码原文，只回元数据（快路径）。
	if opts.Mode == ModeMinimal {
		summary := loc.renderMinimal()
		trace := &TraceEntry{
			Source:        "store.GetContext",
			HitSymbols:    1,
			HitLines:      1, // 极简模式仅注入一行定位信息
			TrimReason:    "mode_minimal",
			TrimmedLines:  loc.LineCount - 1,
			TokenEstimate: 4,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
		}
		ctx2 := &SymbolContext{
			Symbol:    symbol,
			Source:    summary,
			FilePath:  loc.FilePath,
			StartLine: loc.StartLine,
			EndLine:   loc.EndLine,
			Doc:       loc.Doc,
		}
		if opts.IncludeTrace {
			ctx2.Trace = trace
		}
		return ctx2, nil
	}

	// full 模式：读取源码原文并按 context_lines 语义裁剪。
	lines, err := readFileLines(loc.FilePath)
	if err != nil {
		// 文件被移动/删除（扫描后磁盘变化）：回退为 minimal 形态，保留追溯留痕。
		trimReason := "file_unreadable"
		trace := &TraceEntry{
			Source:        "store.GetContext",
			HitSymbols:    1,
			HitLines:      0,
			TrimReason:    trimReason,
			TrimmedLines:  loc.LineCount,
			TokenEstimate: 0,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
		}
		ctx2 := &SymbolContext{
			Symbol:    symbol,
			Source:    loc.renderMinimal(),
			FilePath:  loc.FilePath,
			StartLine: loc.StartLine,
			EndLine:   loc.EndLine,
			Doc:       loc.Doc,
		}
		if opts.IncludeTrace {
			ctx2.Trace = trace
		}
		return ctx2, nil
	}

	// 计算注入窗口：context_lines==0 → 符号完整内容；>0 → 前后各 N 行上下文。
	start := clamp(loc.StartLine-1, 0, len(lines)) // 转 0 基
	end := clamp(loc.EndLine, 0, len(lines))       // 半开区间
	trimmed := 0
	if opts.ContextLines > 0 {
		start = clamp(start-opts.ContextLines, 0, len(lines))
		end = clamp(end+opts.ContextLines, 0, len(lines))
		trimmed = len(lines) - (end - start)
	}
	hit := lines[start:end]
	if len(hit) == 0 {
		hit = lines // 兜底：窗口为空时退化为整文件（防御，正常不会发生）
	}

	// 关联单测（沿用五策略，低置信度过滤 60，静默失败）。
	var related []string
	if links, err := s.FindTestLinks(ctx, symbol, 60); err == nil {
		for _, l := range links {
			related = append(related, l.TestMethod)
		}
	}

	trimReason := "full"
	if opts.ContextLines > 0 {
		trimReason = "context_lines"
	}
	trace := &TraceEntry{
		Source:        "store.GetContext",
		HitSymbols:    1,
		HitLines:      len(hit),
		TrimReason:    trimReason,
		TrimmedLines:  trimmed,
		TokenEstimate: len(hit) * 4,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
	}

	ctx2 := &SymbolContext{
		Symbol:       symbol,
		Source:       strings.Join(hit, "\n"),
		FilePath:     loc.FilePath,
		StartLine:    loc.StartLine,
		EndLine:      loc.EndLine,
		Doc:          loc.Doc,
		RelatedTests: related,
	}
	if opts.IncludeTrace {
		ctx2.Trace = trace
	}
	return ctx2, nil
}

// symbolLocation 符号的物理定位信息（从 Store 解析）。
type symbolLocation struct {
	FilePath  string
	StartLine int
	EndLine   int
	Kind      string // "class" | "method"
	Doc       string
	LineCount int // 文件总行数（从 FileRecord 读取）
}

// renderMinimal 生成极简上下文：仅一行定位摘要（供 minimal 模式与文件不可读兜底）。
func (l *symbolLocation) renderMinimal() string {
	return fmt.Sprintf("%s (%s lines %d-%d)%s", l.Kind, l.FilePath, l.StartLine, l.EndLine, suffixIf(l.Doc != "", " /* "+singleLine(l.Doc)+" */"))
}

// resolveSymbolLocation 在 Store 中查找符号（类/方法）的物理位置。
// 先按类 FullName 精确匹配，再按方法 FullName 精确匹配。
func (s *Service) resolveSymbolLocation(ctx context.Context, symbol string) (*symbolLocation, bool) {
	files, err := s.store.GetAllFiles(ctx)
	if err != nil {
		return nil, false
	}
	lineCount := make(map[int64]int, len(files))
	for _, f := range files {
		lineCount[f.ID] = f.LineCount
	}
	for _, f := range files {
		classes, err := s.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			continue
		}
		for _, cls := range classes {
			if cls.FullName == symbol {
				return &symbolLocation{
					FilePath:  f.AbsolutePath,
					StartLine: cls.StartLine,
					EndLine:   cls.EndLine,
					Kind:      "class",
					Doc:       cls.Doc,
					LineCount: lineCount[f.ID],
				}, true
			}
		}
	}
	// 方法匹配（需要先找类再找方法）
	for _, f := range files {
		classes, err := s.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			continue
		}
		for _, cls := range classes {
			methods, err := s.store.GetMethodsByClassID(ctx, cls.ID)
			if err != nil {
				continue
			}
			for _, m := range methods {
				if m.FullName == symbol {
					return &symbolLocation{
						FilePath:  f.AbsolutePath,
						StartLine: m.StartLine,
						EndLine:   m.EndLine,
						Kind:      "method",
						Doc:       m.Doc,
						LineCount: lineCount[f.ID],
					}, true
				}
			}
		}
	}
	return nil, false
}

// readFileLines 读取文件并按行拆分（保留行尾换行前的原始内容，去除末尾空行）。
func readFileLines(path string) ([]string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	lines := strings.Split(string(data), "\n")
	for len(lines) > 0 && lines[len(lines)-1] == "" {
		lines = lines[:len(lines)-1]
	}
	return lines, nil
}

// clamp 将 v 限制在 [lo, hi] 区间内。
func clamp(v, lo, hi int) int {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}

// suffixIf 条件拼接后缀。
func suffixIf(cond bool, suffix string) string {
	if cond {
		return suffix
	}
	return ""
}

// singleLine 将多行文档压成单行（minimal 摘要用）。
func singleLine(doc string) string {
	doc = strings.ReplaceAll(doc, "\n", " ")
	doc = strings.ReplaceAll(doc, "\r", "")
	if len(doc) > 60 {
		doc = doc[:60] + "..."
	}
	return doc
}

// ImpactNode 影响面分析节点。
type ImpactNode struct {
	Method       string   `json:"method"`
	Depth        int      `json:"depth"`
	RelatedTests []string `json:"related_tests,omitempty"`
}

// ImpactResult 影响面分析响应。
type ImpactResult struct {
	Method  string       `json:"method"`
	Callers []ImpactNode `json:"callers"`
	Callees []ImpactNode `json:"callees"`
	Trace   *TraceEntry  `json:"_trace,omitempty"` // 追溯日志（建议 2：上下文注入追溯）
}

// GetImpact 获取指定方法的影响面（基于真实调用图 + 关联单测）。
//
// 依赖注入的 analyzer（见 WithImpactAnalyzer）：
//   - 有 analyzer 时：按调用图递归查找 callers/callees（带深度），
//     并利用 FindTestLinks 五策略为每个受影响节点关联对应单测
//     （改动一处可列出受影响的单测，即 T4-2 验收口径）；
//   - 无 analyzer 时：返回空影响面（不报错），保持向后兼容。
func (s *Service) GetImpact(ctx context.Context, method string, depth int) (*ImpactResult, error) {
	if method == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "method is required"}
	}
	if depth <= 0 {
		depth = 1
	}

	result := &ImpactResult{
		Method:  method,
		Callers: []ImpactNode{},
		Callees: []ImpactNode{},
	}

	if s.analyzer == nil {
		// 未注入 analyzer：空影响面，追溯记录来源（供复盘判断"为什么空"）。
		result.Trace = &TraceEntry{
			Source:        "store.GetImpact",
			HitSymbols:    0,
			HitLines:      0,
			TrimReason:    "analyzer_unavailable",
			TrimmedLines:  0,
			TokenEstimate: 0,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
		}
		return result, nil
	}

	callers, callees, err := s.analyzer.FindImpactNodesWithDepth(ctx, method, depth)
	if err != nil {
		return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("find impact nodes: %v", err)}
	}

	result.Callers = s.enrichImpactNodes(ctx, callers)
	result.Callees = s.enrichImpactNodes(ctx, callees)

	// 追溯记录：命中符号数 = callers + callees 总数，裁剪依据 = 深度限制。
	hitSymbols := len(result.Callers) + len(result.Callees)
	result.Trace = &TraceEntry{
		Source:        "store.GetImpact",
		HitSymbols:    hitSymbols,
		HitLines:      hitSymbols, // 影响面以节点粒度注入（每节点一行摘要 + 关联单测），按节点数计
		TrimReason:    "depth_limit",
		TrimmedLines:  0,
		TokenEstimate: hitSymbols * 4,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
	}
	return result, nil
}

// enrichImpactNodes 为影响面节点关联对应单测（复用 FindTestLinks 五策略，minConfidence=60）。
func (s *Service) enrichImpactNodes(ctx context.Context, nodes []analyzer.ImpactNode) []ImpactNode {
	out := make([]ImpactNode, 0, len(nodes))
	for _, n := range nodes {
		node := ImpactNode{Method: n.Method, Depth: n.Depth}
		if links, err := s.FindTestLinks(ctx, n.Method, 60); err == nil {
			for _, l := range links {
				node.RelatedTests = append(node.RelatedTests, l.TestMethod)
			}
		}
		out = append(out, node)
	}
	return out
}

// TestResult 关联单测结果。
type TestResult struct {
	TestMethod string `json:"test_method"`
	Strategy   string `json:"strategy"`
	Confidence int    `json:"confidence"`
}

// GetTests 获取关联单测，使用多种策略（naming/same_tag/dependency）。
func (s *Service) GetTests(ctx context.Context, method string, minConfidence int) ([]TestResult, error) {
	if method == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "method is required"}
	}
	if minConfidence <= 0 {
		minConfidence = 60
	}

	links, err := s.FindTestLinks(ctx, method, minConfidence)
	if err != nil {
		return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("find test links: %v", err)}
	}

	results := make([]TestResult, 0, len(links))
	for _, link := range links {
		results = append(results, TestResult{
			TestMethod: link.TestMethod,
			Strategy:   link.Strategy,
			Confidence: link.Confidence,
		})
	}
	return results, nil
}

// SearchResult 搜索结果项。
type SearchResult struct {
	Symbol  string  `json:"symbol"`
	Kind    string  `json:"kind"`
	File    string  `json:"file"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
}

// Search 搜索符号，支持双路检索（FTS 精确匹配 + 向量语义搜索）。
//
// mode 参数：
//   - "exact": 仅 FTS 精确搜索
//   - "semantic": 仅向量语义搜索
//   - "both"（默认）: FTS + 向量融合检索
//
// 当 searcher 未设置时，回退到 P0 占位行为（返回空结果）。
func (s *Service) Search(ctx context.Context, query string, mode string, limit int) ([]SearchResult, error) {
	if query == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "query is required"}
	}
	if limit <= 0 {
		limit = 20
	}

	// 如果有搜索器，使用双路检索
	if s.searcher != nil {
		searchMode := search.SearchModeBoth
		switch mode {
		case "exact":
			searchMode = search.SearchModeExact
		case "semantic":
			searchMode = search.SearchModeSemantic
		}

		results, err := s.searcher.Search(ctx, query, searchMode, limit)
		if err != nil {
			return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("search: %v", err)}
		}

		// 富化搜索结果：从 Store 查询 Kind 和 File 信息
		s.enrichResults(ctx, results)

		// 查询期同名方法消歧（可选）：多个同名方法候选时用 AI 选最佳，
		// 预算超限/LLM 失败时结果原样返回（向后兼容）。
		results = s.disambiguateMethodResults(ctx, query, results)

		// 映射为 service.SearchResult
		svcResults := make([]SearchResult, 0, len(results))
		for _, r := range results {
			svcResults = append(svcResults, SearchResult{
				Symbol:  r.Symbol,
				Kind:    r.Kind,
				File:    r.File,
				Score:   r.Score,
				Snippet: r.Snippet,
			})
		}
		return svcResults, nil
	}

	// 回退到 P0 占位行为
	return []SearchResult{}, nil
}

// disambiguateMethodResults 对搜索结果中的同名方法候选做 AI 消歧（可选）。
//
// 逻辑：
//  1. 仅当注入 enhancer 且查询非空时执行；
//  2. 收集 Kind=="method" 的结果，按「方法简单名」分组（同名方法 = 多个类中同名方法）；
//  3. 对每组（≥2 候选）构建 parser.MethodIR 候选列表，调用 Enhancer.Disambiguate
//     选择最佳项；最佳项保留、其余同组候选取消（降噪）；
//  4. 任何失败（预算超限/LLM 错误/解析异常）都静默回退：结果原样返回，不影响主流程。
func (s *Service) disambiguateMethodResults(ctx context.Context, query string, results []search.SearchResult) []search.SearchResult {
	if s.enhancer == nil || query == "" || len(results) < 2 {
		return results
	}

	// 收集 method 类型结果（符号为 method:<id>）
	type methodCandidate struct {
		idx      int
		methodIR parser.MethodIR
	}
	methodByID := make(map[int64]parser.MethodIR)
	var methodIdxs []int
	for i, r := range results {
		if !strings.HasPrefix(r.Symbol, "method:") {
			continue
		}
		id := parseInt64(strings.TrimPrefix(r.Symbol, "method:"))
		if id <= 0 {
			continue
		}
		ir, ok := s.loadMethodIR(ctx, id)
		if !ok {
			continue
		}
		methodByID[id] = ir
		methodIdxs = append(methodIdxs, i)
	}
	if len(methodIdxs) < 2 {
		return results
	}

	// 按方法简单名分组
	groups := make(map[string][]methodCandidate)
	for _, i := range methodIdxs {
		id := parseInt64(strings.TrimPrefix(results[i].Symbol, "method:"))
		ir := methodByID[id]
		groups[ir.Name] = append(groups[ir.Name], methodCandidate{idx: i, methodIR: ir})
	}

	// 对每组 ≥2 的候选做消歧：保留最佳，取消其余
	drop := make(map[int]bool)
	s.enhancer.SetPhase(ai.PhaseQuery)
	for _, group := range groups {
		if len(group) < 2 {
			continue
		}
		candidates := make([]parser.MethodIR, 0, len(group))
		for _, c := range group {
			candidates = append(candidates, c.methodIR)
		}
		best, err := s.enhancer.Disambiguate(ctx, candidates, query)
		if err != nil || best < 0 || best >= len(group) {
			continue // 失败回退：该组不消歧
		}
		for gi, c := range group {
			if gi != best {
				drop[c.idx] = true
			}
		}
	}

	if len(drop) == 0 {
		return results
	}
	filtered := make([]search.SearchResult, 0, len(results))
	for i, r := range results {
		if drop[i] {
			continue
		}
		filtered = append(filtered, r)
	}
	return filtered
}

// loadMethodIR 按方法 ID 加载 MethodIR（供消歧候选构建）。
func (s *Service) loadMethodIR(ctx context.Context, id int64) (parser.MethodIR, bool) {
	files, err := s.store.GetAllFiles(ctx)
	if err != nil {
		return parser.MethodIR{}, false
	}
	for _, f := range files {
		classes, err := s.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			continue
		}
		for _, cls := range classes {
			methods, err := s.store.GetMethodsByClassID(ctx, cls.ID)
			if err != nil {
				continue
			}
			for _, m := range methods {
				if m.ID == id {
					return parser.MethodIR{
						Name:      m.Name,
						ClassFQN:  cls.FullName,
						Signature: m.Signature,
						Doc:       m.Doc,
					}, true
				}
			}
		}
	}
	return parser.MethodIR{}, false
}

// enrichResults 从 Store 查询搜索结果中每个符号的 Kind 和 File 信息。
//
// 符号 ID 格式：
//   - "file:/path/to/file.go" → 直接提取文件路径
//   - "class:123" → 从 Store 查询类记录
//   - "method:456" → 从 Store 查询方法记录
//
// 对于找不到的符号，留空（不阻断搜索流程）。
func (s *Service) enrichResults(ctx context.Context, results []search.SearchResult) {
	for i, r := range results {
		if r.Kind != "" && r.File != "" {
			continue // 已经填充过
		}
		kind, file := s.resolveSymbol(ctx, r.Symbol)
		if kind != "" {
			results[i].Kind = kind
		}
		if file != "" {
			results[i].File = file
		}
	}
}

// resolveSymbol 解析符号 ID 为 Kind 和 File 路径。
func (s *Service) resolveSymbol(ctx context.Context, symbol string) (kind, file string) {
	const (
		filePrefix   = "file:"
		classPrefix  = "class:"
		methodPrefix = "method:"
	)

	switch {
	case strings.HasPrefix(symbol, filePrefix):
		return "file", symbol[len(filePrefix):]

	case strings.HasPrefix(symbol, classPrefix):
		id := parseInt64(symbol[len(classPrefix):])
		if id <= 0 {
			return "", ""
		}
		// 遍历所有文件查找类
		files, err := s.store.GetAllFiles(ctx)
		if err != nil {
			return "", ""
		}
		for _, f := range files {
			classes, err := s.store.GetClassesByFileID(ctx, f.ID)
			if err != nil {
				continue
			}
			for _, c := range classes {
				if c.ID == id {
					kind := "class"
					if c.Type != "" {
						kind = strings.ToLower(c.Type)
					}
					return kind, f.AbsolutePath
				}
			}
		}

	case strings.HasPrefix(symbol, methodPrefix):
		id := parseInt64(symbol[len(methodPrefix):])
		if id <= 0 {
			return "", ""
		}
		// 遍历所有文件查找方法
		files, err := s.store.GetAllFiles(ctx)
		if err != nil {
			return "", ""
		}
		for _, f := range files {
			classes, err := s.store.GetClassesByFileID(ctx, f.ID)
			if err != nil {
				continue
			}
			for _, c := range classes {
				methods, err := s.store.GetMethodsByClassID(ctx, c.ID)
				if err != nil {
					continue
				}
				for _, m := range methods {
					if m.ID == id {
						return "method", f.AbsolutePath
					}
				}
			}
		}
	}

	return "", ""
}

// parseInt64 简单解析 int64，解析失败返回 0。
func parseInt64(s string) int64 {
	if len(s) == 0 {
		return 0
	}
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

// FindDependencies 查找依赖关系（P0 骨架）。
func (s *Service) FindDependencies(ctx context.Context, symbol string) ([]string, error) {
	if symbol == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "symbol is required"}
	}
	return []string{}, nil
}

// GetCallGraph 获取调用图（P0 骨架）。
func (s *Service) GetCallGraph(ctx context.Context, symbol string, depth int) (map[string]any, error) {
	if symbol == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "symbol is required"}
	}
	if depth <= 0 {
		depth = 1
	}
	return map[string]any{
		"symbol": symbol,
		"depth":  depth,
		"nodes":  []string{},
		"edges":  []string{},
	}, nil
}

// TagResult 标签查询结果。
type TagResult struct {
	Symbol     string            `json:"symbol"`
	Kind       string            `json:"kind"` // "class" or "method"
	Tags       []string          `json:"tags"`
	Categories map[string]string `json:"categories,omitempty"` // tag -> category
}

// GetTags 获取指定符号的标签列表。
//
// symbol 格式：类的 FullName 或方法的 FullName。
// 先尝试按类名查询，再按方法名查询。
func (s *Service) GetTags(ctx context.Context, symbol string) (*TagResult, error) {
	if symbol == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "symbol is required"}
	}

	// 先尝试按类名查询（通过所有文件）
	files, err := s.store.GetAllFiles(ctx)
	if err != nil {
		return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("get all files: %v", err)}
	}

	for _, f := range files {
		classes, err := s.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			continue
		}
		for _, cls := range classes {
			if cls.FullName == symbol {
				tags, err := s.store.GetTagsByClassID(ctx, cls.ID)
				if err != nil {
					return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("get tags: %v", err)}
				}
				cats, _ := s.store.GetAllTagsWithCategories(ctx)
				result := &TagResult{
					Symbol: symbol,
					Kind:   "class",
					Tags:   tags,
				}
				if len(cats) > 0 {
					filtered := make(map[string]string)
					for _, t := range tags {
						if c, ok := cats[t]; ok {
							filtered[t] = c
						}
					}
					result.Categories = filtered
				}
				return result, nil
			}

			// 在方法中查询
			methods, err := s.store.GetMethodsByClassID(ctx, cls.ID)
			if err != nil {
				continue
			}
			for _, m := range methods {
				if m.FullName == symbol {
					tags, err := s.store.GetTagsByMethodID(ctx, m.ID)
					if err != nil {
						return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("get tags: %v", err)}
					}
					cats, _ := s.store.GetAllTagsWithCategories(ctx)
					result := &TagResult{
						Symbol: symbol,
						Kind:   "method",
						Tags:   tags,
					}
					if len(cats) > 0 {
						filtered := make(map[string]string)
						for _, t := range tags {
							if c, ok := cats[t]; ok {
								filtered[t] = c
							}
						}
						result.Categories = filtered
					}
					return result, nil
				}
			}
		}
	}

	return nil, &ServiceError{Code: "ERR_SYMBOL_NOT_FOUND", Message: fmt.Sprintf("symbol not found: %s", symbol)}
}

// TagSearchResult 标签搜索结果。
type TagSearchResult struct {
	Tag       string   `json:"tag"`
	ClassIDs  []int64  `json:"class_ids,omitempty"`
	MethodIDs []int64  `json:"method_ids,omitempty"`
	Classes   []string `json:"classes,omitempty"` // 类名列表
	Methods   []string `json:"methods,omitempty"` // 方法名列表
}

// SearchByTag 按单个标签搜索类和方法的 ID 和名称（兼容入口，委托 SearchByTags）。
func (s *Service) SearchByTag(ctx context.Context, tag string) (*TagSearchResult, error) {
	return s.SearchByTags(ctx, []string{tag})
}

// SearchByTags 按多个标签（AND 交集）搜索类和方法的 ID 和名称。
// 返回同时拥有所有指定标签的类和方法。
func (s *Service) SearchByTags(ctx context.Context, tags []string) (*TagSearchResult, error) {
	if len(tags) == 0 {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "tags is required"}
	}
	for _, t := range tags {
		if t == "" {
			return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "tag must not be empty"}
		}
	}

	classIDs, methodIDs, err := s.store.SearchByTags(ctx, tags)
	if err != nil {
		return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("search by tags: %v", err)}
	}

	result := &TagSearchResult{
		Tag:       strings.Join(tags, ","),
		ClassIDs:  classIDs,
		MethodIDs: methodIDs,
	}

	// 解析类名与方法名（沿用共享辅助，避免复制粘贴）。
	s.resolveTagSearchResult(ctx, result, classIDs, methodIDs)
	return result, nil
}

// resolveTagSearchResult 将 class/method ID 列表解析为全限定名列表，填入 result。
// 供 SearchByTag / SearchByTags 共用。
func (s *Service) resolveTagSearchResult(ctx context.Context, result *TagSearchResult, classIDs, methodIDs []int64) {
	// 解析类名
	files, _ := s.store.GetAllFiles(ctx)
	for _, cid := range classIDs {
		for _, f := range files {
			classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
			for _, cls := range classes {
				if cls.ID == cid {
					result.Classes = append(result.Classes, cls.FullName)
				}
			}
		}
	}

	// 解析方法名
	for _, mid := range methodIDs {
		for _, f := range files {
			classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
			for _, cls := range classes {
				methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
				for _, m := range methods {
					if m.ID == mid {
						result.Methods = append(result.Methods, m.FullName)
					}
				}
			}
		}
	}
}

// AllTagsResult 所有标签及其分类。
type AllTagsResult struct {
	Tags       map[string]string `json:"tags"`       // tag -> category
	Categories map[string]int    `json:"categories"` // category -> count
}

// GetAllTags 返回所有已知标签及其分类统计。
func (s *Service) GetAllTags(ctx context.Context) (*AllTagsResult, error) {
	tags, err := s.store.GetAllTagsWithCategories(ctx)
	if err != nil {
		return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("get all tags: %v", err)}
	}

	cats := make(map[string]int)
	for _, cat := range tags {
		cats[cat]++
	}

	return &AllTagsResult{
		Tags:       tags,
		Categories: cats,
	}, nil
}

// SearchConfig 搜索配置项（P0 骨架）。
func (s *Service) SearchConfig(ctx context.Context, pattern string) ([]string, error) {
	if pattern == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "pattern is required"}
	}
	return []string{}, nil
}

// GetAffected 获取受影响内容（P0 骨架）。
func (s *Service) GetAffected(ctx context.Context, symbol string, recursive bool) ([]string, error) {
	if symbol == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "symbol is required"}
	}
	return []string{}, nil
}

// ServiceError 业务错误，包含错误码和消息。
type ServiceError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ServiceError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// HTTPStatus 返回对应的 HTTP 状态码。
func (e *ServiceError) HTTPStatus() int {
	switch e.Code {
	case "ERR_SYMBOL_NOT_FOUND":
		return 404
	case "ERR_INVALID_PARAMETER":
		return 400
	case "ERR_RATE_LIMITED":
		return 429
	case "ERR_UNAUTHORIZED":
		return 401
	default:
		return 500
	}
}
