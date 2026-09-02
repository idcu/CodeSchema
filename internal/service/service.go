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
	"sort"
	"strings"
	"sync/atomic"
	"time"

	"gitee.com/idcu-go/pathsafe"
	"gitee.com/idcu-go/trim"
	"gitee.com/idcu-go/ttlcache"

	"github.com/idcu/codeschema/internal/ai"
	"github.com/idcu/codeschema/internal/analyzer"
	cerrors "github.com/idcu/codeschema/internal/errors"
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

	// pathRoot 可选虚拟根（B9 路径虚拟化）。未注入或 path_style=absolute 时
	// 输出宿主真实绝对路径（默认，向后兼容）。
	pathRoot *pathsafe.Root

	// queryCache 查询级缓存（B4）：相同 symbol + 相同裁剪参数在 TTL 内直接命中，
	// 不再重复读盘与裁剪。nil 表示未启用（行为与启用前完全一致）。
	queryCache *ttlcache.Cache[string, *SymbolContext]
	// cacheEpoch 缓存世代号：失效时递增，旧 key 再也无法拼出，等价于 O(1) 全量失效。
	cacheEpoch atomic.Uint64
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

// 查询级缓存默认值（B4）。
const (
	// DefaultQueryCacheTTL 默认存活时间：一轮 Agent 会话内的重复查询足以命中，
	// 又不会让文件变更长时间反映不到结果里。
	DefaultQueryCacheTTL = 30 * time.Second
	// DefaultQueryCacheEntries 默认最多缓存的查询结果条目数。
	DefaultQueryCacheEntries = 512
)

// EnableQueryCache 启用查询级缓存（B4）。
//
// 命中条件：相同 symbol + 相同裁剪参数（mode / context_lines / 预算 / path_style）。
// 命中后不再读盘与裁剪，代价是索引或文件变更后必须主动 InvalidateQueryCache，
// 否则会短暂返回旧内容（由 TTL 兜底，默认 30 秒）。
//
// ttl<=0 用 DefaultQueryCacheTTL；maxEntries<=0 用 DefaultQueryCacheEntries。
func (s *Service) EnableQueryCache(ttl time.Duration, maxEntries int) *Service {
	if ttl <= 0 {
		ttl = DefaultQueryCacheTTL
	}
	if maxEntries <= 0 {
		maxEntries = DefaultQueryCacheEntries
	}
	s.queryCache = ttlcache.New[string, *SymbolContext](
		ttlcache.WithTTL(ttl),
		ttlcache.WithMaxEntries(maxEntries),
	)
	return s
}

// QueryCacheEnabled 报告查询级缓存是否已启用。
func (s *Service) QueryCacheEnabled() bool { return s.queryCache != nil }

// InvalidateQueryCache 全量失效查询缓存（索引重建 / 文件变更后调用）。
//
// 未启用缓存时是安全的空操作。实现上先递增世代号（让旧 key 永远拼不出来），
// 再清空现有条目回收内存——两步都做是为了「立即生效」与「立即回收」兼得。
func (s *Service) InvalidateQueryCache() {
	if s.queryCache == nil {
		return
	}
	s.cacheEpoch.Add(1)
	s.queryCache.Invalidate()
}

// QueryCacheStats 返回查询缓存的命中统计（未启用时返回零值）。
func (s *Service) QueryCacheStats() ttlcache.Stats {
	if s.queryCache == nil {
		return ttlcache.Stats{}
	}
	return s.queryCache.Stats()
}

// queryCacheKey 构造缓存 key：世代号 + 符号 + 全部影响输出的裁剪参数。
func (s *Service) queryCacheKey(symbol string, o ContextOptions) string {
	return fmt.Sprintf("e%d|%s|%s|%d|%d|%d|%d|%.4g|%s",
		s.cacheEpoch.Load(), symbol, o.Mode, o.ContextLines,
		o.MaxBytes, o.MaxTokens, o.MaxLineChars, o.charsPerToken(), o.PathStyle)
}

// WithPathRoot 注入虚拟根，启用 path_style=virtual 的路径虚拟化输出。
//
// 虚拟根把宿主真实目录映射为 /codebase：对外隐藏磁盘布局（少泄露信息），
// 同时缩短路径本身在响应里占的 token。未注入时 PathVirtual 退化为真实路径。
func (s *Service) WithPathRoot(root *pathsafe.Root) *Service {
	s.pathRoot = root
	return s
}

// renderPath 按 PathStyle 输出路径。
//
// virtual 且已注入虚拟根时映射为虚拟路径；根外路径（多仓/软链越界）原样返回——
// 宁可多给信息，也不把「映射失败」伪装成「映射成功」。
func (s *Service) renderPath(real string, style PathStyle) string {
	if style != PathVirtual || s.pathRoot == nil {
		return real
	}
	virtual, err := s.pathRoot.Virtualize(real)
	if err != nil {
		return real
	}
	return virtual
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
		StoreType: storeTypeName(s.store),
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

// storeTypeName 从 store 实例推断驱动名（不再硬编码 "file"）。
// 优先走 store.DriverNamer 可选接口（各驱动实现精确报告），
// 未实现时回退 FileStore 类型匹配，包装型返回 generic。
func storeTypeName(st store.Store) string {
	if st == nil {
		return "none"
	}
	if dn, ok := st.(store.DriverNamer); ok {
		return dn.DriverName()
	}
	switch st.(type) {
	case *store.FileStore:
		return "file"
	default:
		return "generic"
	}
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

	// 以下为「诊断元数据回传」字段（对齐 FastContext 的 [config]/[diagnostic] 行）：
	// 让调用方 Agent 拿到实际生效的参数与实际产出，能自行调参而不是盲目重试。
	Config        *TraceConfig `json:"config,omitempty"`         // 本次实际生效的配置
	ActualBytes   int          `json:"actual_bytes"`             // 实际输出字节数（含换行）
	ActualTokens  int          `json:"actual_tokens"`            // 按 charsPerToken 估算的实际 token 数
	ActualStart   int          `json:"actual_start,omitempty"`   // 实际输出首行（1-based）
	ActualEnd     int          `json:"actual_end,omitempty"`     // 实际输出末行（1-based）
	Degraded      bool         `json:"degraded"`                 // 是否因预算不足触发降级
	DegradeReason string       `json:"degrade_reason,omitempty"` // 降级原因（trim.Reason）
	LineTruncated bool         `json:"line_truncated"`           // 是否发生行级截断
	OmittedLines  int          `json:"omitted_lines"`            // 预算不足被省略的行数
	CacheHit      bool         `json:"cache_hit"`                // 是否命中查询级缓存
}

// TraceConfig 本次实际生效的上下文裁剪配置，随 _trace 回传。
type TraceConfig struct {
	Mode          string  `json:"mode"`            // full / minimal
	ContextLines  int     `json:"context_lines"`   // 外扩上下文行数（0 = 仅符号体）
	MaxBytes      int     `json:"max_bytes"`       // 字节预算（<=0 不限）
	MaxTokens     int     `json:"max_tokens"`      // token 预算（<=0 不限）
	MaxLineChars  int     `json:"max_line_chars"`  // 单行字符上限（<=0 不截断）
	CharsPerToken float64 `json:"chars_per_token"` // token 估算口径
	PathStyle     string  `json:"path_style"`      // absolute / virtual
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
	//   - N>0：注入符号体并前后各附带 N 行上下文（夹在文件边界内，且恒不切断符号体）。
	ContextLines int
	// Mode 注入模式：ModeFull（默认）或 ModeMinimal。
	Mode ContextMode
	// IncludeTrace 是否在响应中附加 _trace 追溯字段（默认 true）。
	IncludeTrace bool

	// MaxBytes 输出字节预算（<=0 不限）。与 MaxTokens 是「与」关系，任一超限即降级。
	MaxBytes int
	// MaxTokens 输出 token 预算（<=0 不限），按 CharsPerToken 口径折算。
	MaxTokens int
	// MaxLineChars 单行字符上限（<=0 不截断）。用于防止压缩产物/生成代码一行吃掉整份预算。
	MaxLineChars int
	// CharsPerToken token 估算口径（每 token 折合字节数）；<=0 用 trim.DefaultCharsPerToken（4）。
	CharsPerToken float64
	// PathStyle 路径输出形态：PathAbsolute（默认，宿主绝对路径）/ PathVirtual（虚拟根 /codebase）。
	PathStyle PathStyle
}

// PathStyle 路径输出形态。
type PathStyle string

const (
	// PathAbsolute 输出宿主真实绝对路径（默认，向后兼容）。
	PathAbsolute PathStyle = "absolute"
	// PathVirtual 输出虚拟路径（真实根映射为 /codebase），隐藏宿主布局并缩短输出。
	PathVirtual PathStyle = "virtual"
)

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
//
// GetContextMode 按指定选项获取符号上下文（查询级缓存入口，B4）。
//
// 缓存未启用时直接落到 getContextModeUncached；启用后按「符号 + 裁剪参数」命中缓存，
// 命中结果做浅拷贝返回（避免调用方改到缓存里的对象），并把 _trace.cache_hit 置为 true
// 回传——命中与否对调用方可见，便于判断省下了多少工作。
func (s *Service) GetContextMode(ctx context.Context, symbol string, opts ContextOptions) (*SymbolContext, error) {
	if s.queryCache == nil {
		return s.getContextModeUncached(ctx, symbol, opts)
	}
	key := s.queryCacheKey(symbol, opts)
	if hit, ok := s.queryCache.Get(key); ok && hit != nil {
		cached := *hit
		if hit.Trace != nil {
			traceCopy := *hit.Trace
			traceCopy.CacheHit = true
			cached.Trace = &traceCopy
		}
		return &cached, nil
	}
	got, err := s.getContextModeUncached(ctx, symbol, opts)
	if err != nil {
		return nil, err
	}
	s.queryCache.Set(key, got)
	return got, nil
}

// getContextModeUncached 是 GetContextMode 的无缓存实现（真正的裁剪逻辑）。
func (s *Service) getContextModeUncached(ctx context.Context, symbol string, opts ContextOptions) (*SymbolContext, error) {
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
			Config:        opts.traceConfig(),
			ActualBytes:   len(summary) + 1,
			ActualTokens:  trim.EstimateTokens(len(summary)+1, opts.charsPerToken()),
		}
		ctx2 := &SymbolContext{
			Symbol:    symbol,
			Source:    summary,
			FilePath:  s.renderPath(loc.FilePath, opts.PathStyle),
			StartLine: loc.StartLine,
			EndLine:   loc.EndLine,
			Doc:       loc.Doc,
		}
		if opts.IncludeTrace {
			ctx2.Trace = trace
		}
		return ctx2, nil
	}

	// full 模式：读取源码原文，按「语义块对齐 + 预算自适应降级」裁剪。
	lines, err := readFileLines(loc.FilePath)
	if err != nil {
		// 文件被移动/删除（扫描后磁盘变化）：回退为 minimal 形态，保留追溯留痕。
		trace := &TraceEntry{
			Source:        "store.GetContext",
			HitSymbols:    1,
			HitLines:      0,
			TrimReason:    "file_unreadable",
			TrimmedLines:  loc.LineCount,
			TokenEstimate: 0,
			Timestamp:     time.Now().UTC().Format(time.RFC3339),
			Config:        opts.traceConfig(),
		}
		ctx2 := &SymbolContext{
			Symbol:    symbol,
			Source:    loc.renderMinimal(),
			FilePath:  s.renderPath(loc.FilePath, opts.PathStyle),
			StartLine: loc.StartLine,
			EndLine:   loc.EndLine,
			Doc:       loc.Doc,
		}
		if opts.IncludeTrace {
			ctx2.Trace = trace
		}
		return ctx2, nil
	}

	// 符号体即语义块（Block）：裁剪窗口恒覆盖它，绝不给半个函数/类。
	block := trim.Block{
		Start: clamp(loc.StartLine, 1, len(lines)),
		End:   clamp(loc.EndLine, 1, len(lines)),
	}
	if block.End < block.Start {
		block.End = block.Start
	}
	start, end := trim.Window(len(lines), block, opts.ContextLines)

	// 一次调用完成「对齐 → 行级截断 → 预算降级」：超限按
	// 上下文 → 块 → 块内居中截断 → 仅首行 的链路自动收敛，并留痕原因。
	fitted, ferr := trim.Fit(lines, start, end, block,
		trim.Budget{MaxBytes: opts.MaxBytes, MaxTokens: opts.MaxTokens},
		trim.WithMaxLineChars(opts.MaxLineChars),
		trim.WithCharsPerToken(opts.CharsPerToken))
	if ferr != nil {
		// 索引与磁盘漂移导致块区间非法：退化为保守窗口，不因裁剪失败而断流。
		fitted = fallbackFit(lines, start, end)
	}

	// 关联单测（沿用五策略，低置信度过滤 60，静默失败）。
	var related []string
	if links, err := s.FindTestLinks(ctx, symbol, 60); err == nil {
		for _, l := range links {
			related = append(related, l.TestMethod)
		}
	}

	trace := &TraceEntry{
		Source:        "store.GetContext",
		HitSymbols:    1,
		HitLines:      len(fitted.Lines),
		TrimReason:    trimReasonOf(fitted),
		TrimmedLines:  len(lines) - len(fitted.Lines),
		TokenEstimate: len(fitted.Lines) * 4,
		Timestamp:     time.Now().UTC().Format(time.RFC3339),
		Config:        opts.traceConfig(),
		ActualBytes:   fitted.Bytes,
		ActualTokens:  fitted.Tokens,
		ActualStart:   fitted.Start,
		ActualEnd:     fitted.End,
		Degraded:      fitted.Degraded,
		LineTruncated: fitted.LineTruncated,
		OmittedLines:  fitted.OmittedLines,
	}
	if fitted.Degraded {
		trace.DegradeReason = string(fitted.Reason)
	}

	ctx2 := &SymbolContext{
		Symbol:       symbol,
		Source:       fitted.Text(),
		FilePath:     s.renderPath(loc.FilePath, opts.PathStyle),
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

// GetContextBatch 批量获取多个符号的上下文（B5：单轮返回 N 个符号，省工具调用轮次）。
//
// 语义：
//   - 单符号失败（未找到 / 参数非法）不中断整体，其位置返回 nil 并在 errs 中给出原因；
//   - 结果与 symbols 等长且同序（未失败的位置有值）；
//   - 空 symbols 返回空结果（不报错）。
func (s *Service) GetContextBatch(ctx context.Context, symbols []string, opts ContextOptions) ([]*SymbolContext, []error) {
	if len(symbols) == 0 {
		return nil, nil
	}
	out := make([]*SymbolContext, len(symbols))
	errs := make([]error, len(symbols))
	for i, sym := range symbols {
		if strings.TrimSpace(sym) == "" {
			errs[i] = &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "symbol is required"}
			continue
		}
		got, err := s.GetContextMode(ctx, sym, opts)
		if err != nil {
			errs[i] = err
			continue
		}
		out[i] = got
	}
	return out, errs
}

// BatchError 批量查询中单个符号的失败明细。
//
// 带 hint 是为了让 Agent 在批量场景里同样能一轮自愈：批量查询动辄几十个符号，
// 逐个人肉排查得不偿失。
type BatchError struct {
	Symbol  string `json:"symbol"`
	Code    string `json:"code"`
	Message string `json:"message"`
	Hint    string `json:"hint,omitempty"`
}

// newBatchError 把 error 归一为 BatchError（ServiceError 保留原 code，其余归 ERR_INTERNAL）。
func newBatchError(symbol string, err error) BatchError {
	be := BatchError{Symbol: symbol, Code: "ERR_INTERNAL", Message: err.Error()}
	if svcErr, ok := err.(*ServiceError); ok {
		be.Code = svcErr.Code
		be.Message = svcErr.Message
	}
	be.Hint = cerrors.Hint(be.Code)
	return be
}

// ContextBatchResult 批量上下文响应。
type ContextBatchResult struct {
	Results []*SymbolContext `json:"results"`
	Errors  []BatchError     `json:"errors,omitempty"`
}

// GetContextBatchDetailed 批量获取上下文，返回结构化结果（含逐项失败明细）。
//
// 单符号失败不中断整体：失败位置不出现在 results，原因落在 errors（带 code + hint）。
func (s *Service) GetContextBatchDetailed(ctx context.Context, symbols []string, opts ContextOptions) *ContextBatchResult {
	res := &ContextBatchResult{Results: make([]*SymbolContext, 0, len(symbols))}
	if len(symbols) == 0 {
		return res
	}
	got, errs := s.GetContextBatch(ctx, symbols, opts)
	for i := range symbols {
		if errs[i] != nil {
			res.Errors = append(res.Errors, newBatchError(symbols[i], errs[i]))
			continue
		}
		if got[i] != nil {
			res.Results = append(res.Results, got[i])
		}
	}
	return res
}

// ImpactBatchResult 批量影响面响应。
type ImpactBatchResult struct {
	Results []*ImpactResult `json:"results"`
	Errors  []BatchError    `json:"errors,omitempty"`
}

// GetImpactBatch 批量获取影响面（语义同 GetContextBatch：逐项失败隔离）。
func (s *Service) GetImpactBatch(ctx context.Context, methods []string, depth int) *ImpactBatchResult {
	res := &ImpactBatchResult{Results: make([]*ImpactResult, 0, len(methods))}
	for _, m := range methods {
		if strings.TrimSpace(m) == "" {
			res.Errors = append(res.Errors, newBatchError(m, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "method is required"}))
			continue
		}
		got, err := s.GetImpact(ctx, m, depth)
		if err != nil {
			res.Errors = append(res.Errors, newBatchError(m, err))
			continue
		}
		res.Results = append(res.Results, got)
	}
	return res
}

// TestBatchItem 批量测试关联结果中的单项。
type TestBatchItem struct {
	Method string       `json:"method"`
	Tests  []TestResult `json:"tests"`
}

// TestBatchResult 批量测试关联响应。
type TestBatchResult struct {
	Results []TestBatchItem `json:"results"`
	Errors  []BatchError    `json:"errors,omitempty"`
}

// GetTestsBatch 批量获取关联单测（语义同 GetContextBatch：逐项失败隔离）。
func (s *Service) GetTestsBatch(ctx context.Context, methods []string, minConfidence int) *TestBatchResult {
	res := &TestBatchResult{Results: make([]TestBatchItem, 0, len(methods))}
	for _, m := range methods {
		if strings.TrimSpace(m) == "" {
			res.Errors = append(res.Errors, newBatchError(m, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "method is required"}))
			continue
		}
		got, err := s.GetTests(ctx, m, minConfidence)
		if err != nil {
			res.Errors = append(res.Errors, newBatchError(m, err))
			continue
		}
		res.Results = append(res.Results, TestBatchItem{Method: m, Tests: got})
	}
	return res
}

// fallbackFit 裁剪失败时的保守窗口（保持旧行为：整窗口切片，不做预算降级）。
func fallbackFit(lines []string, start, end int) trim.Result {
	start = clamp(start, 1, len(lines))
	end = clamp(end, start, len(lines))
	seg := lines[start-1 : end]
	nbytes := 0
	for _, ln := range seg {
		nbytes += len(ln) + 1
	}
	return trim.Result{
		Lines:        seg,
		Start:        start,
		End:          end,
		Bytes:        nbytes,
		Tokens:       trim.EstimateTokens(nbytes, trim.DefaultCharsPerToken),
		Reason:       trim.ReasonBlockAligned,
		TrimmedLines: len(lines) - len(seg),
	}
}

// trimReasonOf 把 trim 的裁剪原因映射为 CodeSchema 的 trim_reason 口径。
// 预算降级类原因直接透出（budget_*），便于调用方按原因调参。
func trimReasonOf(r trim.Result) string {
	switch r.Reason {
	case trim.ReasonContextLines:
		return "context_lines"
	case trim.ReasonBlockAligned:
		return "semantic_block"
	case trim.ReasonShrinkContext, trim.ReasonTruncateBlock, trim.ReasonHeadOnly:
		return string(r.Reason)
	default:
		return "full"
	}
}

// charsPerToken 返回生效的 token 估算口径（未设置时用 trim 默认值）。
func (o ContextOptions) charsPerToken() float64 {
	if o.CharsPerToken > 0 {
		return o.CharsPerToken
	}
	return trim.DefaultCharsPerToken
}

// traceConfig 生成本次生效配置的快照（随 _trace 回传，供调用方自诊断）。
func (o ContextOptions) traceConfig() *TraceConfig {
	cpt := o.CharsPerToken
	if cpt <= 0 {
		cpt = trim.DefaultCharsPerToken
	}
	mode := string(o.Mode)
	if mode == "" {
		mode = string(ModeFull)
	}
	style := string(o.PathStyle)
	if style == "" {
		style = string(PathAbsolute)
	}
	return &TraceConfig{
		Mode:          mode,
		ContextLines:  o.ContextLines,
		MaxBytes:      o.MaxBytes,
		MaxTokens:     o.MaxTokens,
		MaxLineChars:  o.MaxLineChars,
		CharsPerToken: cpt,
		PathStyle:     style,
	}
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
//
// 快速路径：底层 store 实现 store.CacheReader（Redis L2 缓存）时，
// 类/方法符号均经缓存 O(1) 命中（GetClass/GetMethod + 路径反查），
// 避免全表扫描；未命中或未实现时回退全表遍历（数据一致性与向后兼容）。
func (s *Service) resolveSymbolLocation(ctx context.Context, symbol string) (*symbolLocation, bool) {
	if cr, ok := s.store.(store.CacheReader); ok {
		// 方法符号快速路径（方法 FQN = ClassFQN + "." + Name，最热查询形态）。
		if m, err := cr.GetMethod(ctx, symbol); err == nil && m != nil {
			if path, ok := cr.MethodFilePath(ctx, symbol); ok {
				loc := &symbolLocation{
					FilePath:  path,
					StartLine: m.StartLine,
					EndLine:   m.EndLine,
					Kind:      "method",
					Doc:       m.Doc,
				}
				if f, err := s.store.GetFileByPath(ctx, path); err == nil {
					loc.LineCount = f.LineCount
				}
				return loc, true
			}
		}
		// 类符号快速路径。
		if cls, err := cr.GetClass(ctx, symbol); err == nil && cls != nil {
			if path, ok := cr.ClassFilePath(ctx, symbol); ok {
				loc := &symbolLocation{
					FilePath:  path,
					StartLine: cls.StartLine,
					EndLine:   cls.EndLine,
					Kind:      "class",
					Doc:       cls.Doc,
				}
				// 补文件行数（读主存储一次；失败不阻断）。
				if f, err := s.store.GetFileByPath(ctx, path); err == nil {
					loc.LineCount = f.LineCount
				}
				return loc, true
			}
		}
	}
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
	Symbol        string  `json:"symbol"`
	QualifiedName string  `json:"fqn,omitempty"` // 全限定名（类 FQN 或 类FQN.方法名），供 context/impact/tests 链式调用（search→context 一致化 Fix）
	Kind          string  `json:"kind"`
	File          string  `json:"file"`
	Score         float64 `json:"score"`
	Snippet       string  `json:"snippet,omitempty"`
	Confidence    float64 `json:"confidence,omitempty"` // 绝对置信度 [0,1]（B8）
}

// SearchOutcome 检索响应信封（含 B8 低置信度过滤元信息）。
type SearchOutcome struct {
	Results    []SearchResult `json:"results"`
	TrimReason string         `json:"trim_reason,omitempty"` // below_threshold：有结果被低置信度阈值过滤
	Filtered   int            `json:"filtered,omitempty"`    // 被过滤掉的条数
}

// Search 搜索符号，支持双路检索（FTS 精确匹配 + 向量语义搜索）。
//
// 等价于 SearchWithOptions(ctx, query, mode, limit, 0)，不启用低置信度过滤，
// 向后兼容既有调用方（HTTP/MCP 旧路径）。
func (s *Service) Search(ctx context.Context, query string, mode string, limit int) ([]SearchResult, error) {
	out, err := s.SearchWithOptions(ctx, query, mode, limit, 0)
	if err != nil {
		return nil, err
	}
	return out.Results, nil
}

// SearchWithOptions 搜索符号，支持双路检索与 B8 低置信度过滤。
//
// minScore>0 时，过滤掉置信度低于阈值的检索结果（空结果优于误导结果）；
// 被过滤的条数通过 SearchOutcome.Filtered 返回，并置 TrimReason="below_threshold"。
// minScore<=0 表示不启用过滤（向后兼容）。
//
// mode 参数：
//   - "exact": 仅 FTS 精确搜索
//   - "semantic": 仅向量语义搜索
//   - "both"（默认）: FTS + 向量融合检索
//
// 当 searcher 未设置时，回退到 P0 占位行为（返回空结果）。
func (s *Service) SearchWithOptions(ctx context.Context, query, mode string, limit int, minScore float64) (*SearchOutcome, error) {
	if query == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "query is required"}
	}
	if limit <= 0 {
		limit = 20
	}

	outcome := &SearchOutcome{}

	// 如果有搜索器，使用双路检索
	if s.searcher != nil {
		searchMode := search.SearchModeBoth
		switch mode {
		case "exact":
			searchMode = search.SearchModeExact
		case "semantic":
			searchMode = search.SearchModeSemantic
		}

		results, filtered, err := s.searcher.SearchWithOptions(ctx, query, search.SearchOptions{
			Mode:     searchMode,
			Limit:    limit,
			MinScore: minScore,
		})
		if err != nil {
			return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("search: %v", err)}
		}

		if filtered > 0 {
			outcome.TrimReason = "below_threshold"
			outcome.Filtered = filtered
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
				Symbol:        r.Symbol,
				QualifiedName: r.QualifiedName,
				Kind:          r.Kind,
				File:          r.File,
				Score:         r.Score,
				Snippet:       r.Snippet,
				Confidence:    r.Confidence,
			})
		}
		outcome.Results = svcResults
		return outcome, nil
	}

	// 回退到 P0 占位行为
	outcome.Results = []SearchResult{}
	return outcome, nil
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
		if r.QualifiedName != "" {
			continue // 已经填充过（fqn 最后填充，作为已完成标记）
		}
		kind, file, fqn := s.resolveSymbol(ctx, r.Symbol)
		if kind != "" {
			results[i].Kind = kind
		}
		if file != "" {
			results[i].File = file
		}
		if fqn != "" {
			results[i].QualifiedName = fqn
		}
	}
}

// resolveSymbol 解析符号 ID 为 Kind、File 路径与全限定名（FQN）。
//
// FQN 格式与 context/impact/tests 的解析口径一致：类为 ClassFQN，方法为
// ClassFQN + "." + Name（见 resolveSymbolLocation / buildMethodIndexText）。
// 这样 search_symbols 返回的 fqn 可直接喂给 context 工具，打通 search→context 链路。
func (s *Service) resolveSymbol(ctx context.Context, symbol string) (kind, file, fqn string) {
	const (
		filePrefix   = "file:"
		classPrefix  = "class:"
		methodPrefix = "method:"
	)

	switch {
	case strings.HasPrefix(symbol, filePrefix):
		return "file", symbol[len(filePrefix):], ""

	case strings.HasPrefix(symbol, classPrefix):
		id := parseInt64(symbol[len(classPrefix):])
		if id <= 0 {
			return "", "", ""
		}
		// 遍历所有文件查找类
		files, err := s.store.GetAllFiles(ctx)
		if err != nil {
			return "", "", ""
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
					return kind, f.AbsolutePath, c.FullName
				}
			}
		}

	case strings.HasPrefix(symbol, methodPrefix):
		id := parseInt64(symbol[len(methodPrefix):])
		if id <= 0 {
			return "", "", ""
		}
		// 遍历所有文件查找方法
		files, err := s.store.GetAllFiles(ctx)
		if err != nil {
			return "", "", ""
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
						return "method", f.AbsolutePath, c.FullName + "." + m.Name
					}
				}
			}
		}
	}

	return "", "", ""
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

// GetCallGraph 获取指定符号的调用子图（基于真实调用图，T1 修复后可用）。
//
// 先按双向归一化定位命中节点（覆盖查询符号与图节点命名空间不一致），
// 再双向 BFS（callers + callees，受 depth 限制）收集节点与其间边。
// 未注入 analyzer 时返回空图（向后兼容）。
func (s *Service) GetCallGraph(ctx context.Context, symbol string, depth int) (map[string]any, error) {
	if symbol == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "symbol is required"}
	}
	if depth <= 0 {
		depth = 1
	}
	if s.analyzer == nil {
		return map[string]any{
			"symbol": symbol,
			"depth":  depth,
			"nodes":  []string{},
			"edges":  []string{},
		}, nil
	}

	cg, err := s.analyzer.BuildCallGraph(ctx)
	if err != nil {
		return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("build call graph: %v", err)}
	}

	// 双向归一化定位命中节点；未命中则仍以原始 symbol 作为子图根。
	resolved := symbol
	if r, ok := s.analyzer.ResolveImpactNode(ctx, symbol); ok {
		resolved = r
	}

	// 双向 BFS 收集节点集合
	nodeSet := map[string]bool{resolved: true}
	callers, callees, err := s.analyzer.FindImpactNodes(ctx, resolved, depth)
	if err != nil {
		return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("find impact: %v", err)}
	}
	for _, c := range callers {
		nodeSet[c] = true
	}
	for _, c := range callees {
		nodeSet[c] = true
	}

	// 收集节点集合内部的边（caller -> callee）
	edges := make([]string, 0)
	for n := range nodeSet {
		node, ok := cg.Nodes[n]
		if !ok {
			continue
		}
		for _, callee := range node.Callees {
			if nodeSet[callee] {
				edges = append(edges, n+" -> "+callee)
			}
		}
	}

	nodes := make([]string, 0, len(nodeSet))
	for n := range nodeSet {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	sort.Strings(edges)

	return map[string]any{
		"symbol": resolved,
		"depth":  depth,
		"nodes":  nodes,
		"edges":  edges,
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

// GetAffected 获取受指定符号变更影响的单测列表。
//
// 语义：给定符号（方法 FQN），返回「可能因该符号变更而需要重跑」的单测集合。
// 计算方式（与 impact/tests 共用同一套 FQN 口径，保证链路一致）：
//  1. 受影响符号集 = {symbol} ∪ 其传递调用者（callers，向上追溯谁调用了它）；
//     未注入 analyzer 时退化为仅 {symbol}（仍有命名/same_tag 关联单测可用）。
//  2. 对每个受影响符号调用 FindTestLinks（五策略）收集关联单测，全局去重返回。
//
// 与 GetImpact 的区别：GetImpact 返回调用图节点（影响面），GetAffected 直接收敛为
// 「需要重跑的测试」列表，供 CI 增量测试选择（T4-2 验收口径的自然延伸）。
func (s *Service) GetAffected(ctx context.Context, symbol string, recursive bool) ([]string, error) {
	if symbol == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "symbol is required"}
	}

	// 1. 受影响符号集：symbol 本身 + 其传递调用者。
	affected := map[string]bool{symbol: true}
	if s.analyzer != nil {
		depth := 1
		if recursive {
			depth = 10 // 传递追溯上限
		}
		callers, _, err := s.analyzer.FindImpactNodes(ctx, symbol, depth)
		if err == nil {
			for _, c := range callers {
				if c != "" {
					affected[c] = true
				}
			}
		}
	}

	// 2. 对每个受影响符号收集关联单测，去重。
	seen := make(map[string]bool)
	result := make([]string, 0)
	for sym := range affected {
		links, err := s.FindTestLinks(ctx, sym, 60)
		if err != nil {
			continue
		}
		for _, l := range links {
			if l.TestMethod == "" || seen[l.TestMethod] {
				continue
			}
			seen[l.TestMethod] = true
			result = append(result, l.TestMethod)
		}
	}
	return result, nil
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
