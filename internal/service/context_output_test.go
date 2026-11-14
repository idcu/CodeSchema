package service

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	metrics "gitee.com/idcu-go/metrics"
	"gitee.com/idcu-go/pathsafe"
)

// TestContextBudgetShrink 预算容不下外扩上下文时，应丢弃上下文只留语义块。
func TestContextBudgetShrink(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)
	ctx := context.Background()

	// 无预算基线：符号 5-7 行 + 前后各 2 行 = 3..9 共 7 行。
	base, err := svc.GetContextMode(ctx, "com.example.OrderService.GetUser", ContextOptions{
		Mode: ModeFull, ContextLines: 2, IncludeTrace: true,
	})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if base.Trace.Degraded {
		t.Fatalf("无预算时不应降级, reason=%s", base.Trace.DegradeReason)
	}
	if base.Trace.TrimReason != "context_lines" {
		t.Fatalf("TrimReason=%s, want context_lines", base.Trace.TrimReason)
	}

	// 预算小于基线字节数但容得下符号体：应降级为「只留块」。
	got, err := svc.GetContextMode(ctx, "com.example.OrderService.GetUser", ContextOptions{
		Mode: ModeFull, ContextLines: 2, IncludeTrace: true,
		MaxBytes: base.Trace.ActualBytes - 1,
	})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if !got.Trace.Degraded {
		t.Fatal("预算不足时应降级")
	}
	if got.Trace.DegradeReason != "budget_shrink_context" {
		t.Fatalf("DegradeReason=%s, want budget_shrink_context", got.Trace.DegradeReason)
	}
	if got.Trace.ActualBytes > base.Trace.ActualBytes-1 {
		t.Fatalf("降级后仍超预算: %d > %d", got.Trace.ActualBytes, base.Trace.ActualBytes-1)
	}
	// 语义块必须完整：首行与末行都要在。
	if !strings.Contains(got.Source, "func (s *OrderService) GetUser") || !strings.Contains(got.Source, "}") {
		t.Fatalf("降级后符号体不完整: %q", got.Source)
	}
}

// TestContextBudgetTruncate 预算连符号体都容不下时，应块内截断（保头 + 省略 + 保尾）。
func TestContextBudgetTruncate(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)
	ctx := context.Background()

	got, err := svc.GetContextMode(ctx, "com.example.OrderService.GetUser", ContextOptions{
		Mode: ModeFull, IncludeTrace: true, MaxBytes: 30,
	})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if got.Trace.DegradeReason != "budget_truncate_block" {
		t.Fatalf("DegradeReason=%s, want budget_truncate_block", got.Trace.DegradeReason)
	}
	if got.Trace.ActualBytes > 30 {
		t.Fatalf("超出预算: %d", got.Trace.ActualBytes)
	}
	if got.Trace.OmittedLines <= 0 {
		t.Fatal("应记录被省略的行数")
	}
	if !strings.Contains(got.Source, "...") {
		t.Fatalf("截断应带省略标记: %q", got.Source)
	}
}

// TestContextTokenBudget 只设 token 预算时同样生效（双轨预算）。
func TestContextTokenBudget(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)

	got, err := svc.GetContextMode(context.Background(), "com.example.OrderService.GetUser", ContextOptions{
		Mode: ModeFull, IncludeTrace: true, MaxTokens: 2,
	})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if !got.Trace.Degraded {
		t.Fatal("仅 token 轨超限时也应降级")
	}
	if got.Trace.ActualTokens > 2 {
		t.Fatalf("tokens=%d 超过预算 2", got.Trace.ActualTokens)
	}
	// 生效配置应如实回传，供调用方自诊断。
	if got.Trace.Config == nil || got.Trace.Config.MaxTokens != 2 {
		t.Fatalf("Config 未回传 token 预算: %+v", got.Trace.Config)
	}
}

// TestContextMaxLineChars 超长行应被截断，而不是吃掉整份预算。
func TestContextMaxLineChars(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)

	got, err := svc.GetContextMode(context.Background(), "com.example.OrderService.GetUser", ContextOptions{
		Mode: ModeFull, IncludeTrace: true, MaxLineChars: 8,
	})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if !got.Trace.LineTruncated {
		t.Fatal("应标记 LineTruncated")
	}
	for _, ln := range strings.Split(got.Source, "\n") {
		if len(ln) > 8 {
			t.Fatalf("行未被截断到 8 字节: len=%d %q", len(ln), ln)
		}
	}
}

// TestContextBlockNeverCut 语义块不得被切断：窗口恒覆盖符号体（B7）。
func TestContextBlockNeverCut(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)
	ctx := context.Background()

	// 调用方给的窗口小于符号体时应被向外扩展到块边界。
	got, err := svc.GetContextMode(ctx, "com.example.OrderService.GetUser", ContextOptions{
		Mode: ModeFull, IncludeTrace: true,
	})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	lines := strings.Split(got.Source, "\n")
	if len(lines) != 3 {
		t.Fatalf("符号体应为 3 行, 实际 %d 行: %q", len(lines), got.Source)
	}
	if got.Trace.ActualStart != 5 || got.Trace.ActualEnd != 7 {
		t.Fatalf("实际输出区间 [%d,%d], want [5,7]", got.Trace.ActualStart, got.Trace.ActualEnd)
	}
}

// TestContextBatch 批量查询：单符号失败不中断整体，失败项带 code + hint（B5）。
func TestContextBatch(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)

	res := svc.GetContextBatchDetailed(context.Background(), []string{
		"com.example.OrderService.GetUser",
		"com.example.OrderService.NotExist",
		"   ", // 空符号：参数非法
	}, ContextOptions{Mode: ModeFull, IncludeTrace: true})

	if len(res.Results) != 1 {
		t.Fatalf("成功结果应为 1 条, 实际 %d", len(res.Results))
	}
	if len(res.Errors) != 2 {
		t.Fatalf("失败明细应为 2 条, 实际 %d", len(res.Errors))
	}
	codes := map[string]string{}
	for _, e := range res.Errors {
		codes[e.Code] = e.Hint
	}
	if _, ok := codes["ERR_SYMBOL_NOT_FOUND"]; !ok {
		t.Fatalf("应包含 ERR_SYMBOL_NOT_FOUND, 实际 %v", codes)
	}
	if _, ok := codes["ERR_INVALID_PARAMETER"]; !ok {
		t.Fatalf("应包含 ERR_INVALID_PARAMETER, 实际 %v", codes)
	}
	for code, hint := range codes {
		if hint == "" {
			t.Fatalf("错误码 %s 应带修复建议 hint", code)
		}
	}
}

// TestImpactAndTestsBatch 批量影响面 / 关联单测：逐项失败隔离。
func TestImpactAndTestsBatch(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)
	ctx := context.Background()

	// 不存在的符号在 impact/tests 里是「空结果」而非失败（与单查语义一致），
	// 因此批量应返回 2 条结果、0 条失败。
	imp := svc.GetImpactBatch(ctx, []string{"com.example.OrderService.GetUser", "not.exist.Method"}, 1)
	if len(imp.Results) != 2 || len(imp.Errors) != 0 {
		t.Fatalf("impact 批量: results=%d errors=%d, want 2/0", len(imp.Results), len(imp.Errors))
	}

	tests := svc.GetTestsBatch(ctx, []string{"com.example.OrderService.GetUser", "not.exist.Method"}, 60)
	if len(tests.Results) != 2 || len(tests.Errors) != 0 {
		t.Fatalf("tests 批量: results=%d errors=%d, want 2/0", len(tests.Results), len(tests.Errors))
	}
	if tests.Results[0].Method != "com.example.OrderService.GetUser" {
		t.Fatalf("批量结果应保留 method 字段, 实际 %q", tests.Results[0].Method)
	}

	// 空符号是参数非法，应落到 errors 而不是被静默跳过。
	bad := svc.GetImpactBatch(ctx, []string{"com.example.OrderService.GetUser", "  "}, 1)
	if len(bad.Results) != 1 || len(bad.Errors) != 1 {
		t.Fatalf("空符号: results=%d errors=%d, want 1/1", len(bad.Results), len(bad.Errors))
	}
	if bad.Errors[0].Code != "ERR_INVALID_PARAMETER" || bad.Errors[0].Hint == "" {
		t.Fatalf("空符号失败明细应带 code 与 hint, 实际 %+v", bad.Errors[0])
	}
}

// TestQueryCache 查询级缓存：命中回传 cache_hit，主动失效后再查不命中（B4）。
func TestQueryCache(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)
	svc.EnableQueryCache(time.Minute, 16)
	ctx := context.Background()
	opts := ContextOptions{Mode: ModeFull, IncludeTrace: true}
	symbol := "com.example.OrderService.GetUser"

	first, err := svc.GetContextMode(ctx, symbol, opts)
	if err != nil {
		t.Fatalf("first GetContextMode: %v", err)
	}
	if first.Trace.CacheHit {
		t.Fatal("首次查询不应命中缓存")
	}
	second, err := svc.GetContextMode(ctx, symbol, opts)
	if err != nil {
		t.Fatalf("second GetContextMode: %v", err)
	}
	if !second.Trace.CacheHit {
		t.Fatal("第二次相同查询应命中缓存")
	}
	if second.Source != first.Source {
		t.Fatal("缓存命中应返回相同内容")
	}
	if st := svc.QueryCacheStats(); st.Hits != 1 {
		t.Fatalf("缓存命中计数=%d, want 1", st.Hits)
	}

	// 参数不同 → 不同 key，不命中。
	other, err := svc.GetContextMode(ctx, symbol, ContextOptions{Mode: ModeMinimal, IncludeTrace: true})
	if err != nil {
		t.Fatalf("minimal GetContextMode: %v", err)
	}
	if other.Trace.CacheHit {
		t.Fatal("裁剪参数不同不应命中")
	}

	// 主动失效（索引重建/文件变更）后应重新回源。
	svc.InvalidateQueryCache()
	third, err := svc.GetContextMode(ctx, symbol, opts)
	if err != nil {
		t.Fatalf("third GetContextMode: %v", err)
	}
	if third.Trace.CacheHit {
		t.Fatal("失效后不应命中缓存")
	}
}

// TestQueryCache_MissResultIsolated 负控（P2-6）：
// 未命中返回的对象不得与缓存共享底层引用，调用方修改不能污染后续命中。
func TestQueryCache_MissResultIsolated(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)
	svc.EnableQueryCache(time.Minute, 16)
	ctx := context.Background()
	opts := ContextOptions{Mode: ModeFull, IncludeTrace: true}
	symbol := "com.example.OrderService.GetUser"

	first, err := svc.GetContextMode(ctx, symbol, opts)
	if err != nil {
		t.Fatalf("first GetContextMode: %v", err)
	}
	// 模拟调用方修改未命中返回值（SymbolContext 是导出类型，可能被就地改写）。
	first.Symbol = "POLLUTED"
	first.Source = "POLLUTED"

	second, err := svc.GetContextMode(ctx, symbol, opts)
	if err != nil {
		t.Fatalf("second GetContextMode: %v", err)
	}
	if second.Symbol == "POLLUTED" || second.Source == "POLLUTED" {
		t.Fatalf("未命中返回值与缓存共享引用：second=%+v", second)
	}
	if !second.Trace.CacheHit {
		t.Fatal("第二次查询应命中缓存")
	}
}

// TestQueryCacheKeyIncludesIncludeTrace P2-2 负控：include_trace 不同取值必须各自成 key。
// 修复前 key 不含该参数，先 false 再 true 会命中无 _trace 的缓存条目。
func TestQueryCacheKeyIncludesIncludeTrace(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)
	svc.EnableQueryCache(time.Minute, 16)
	ctx := context.Background()
	symbol := "com.example.OrderService.GetUser"

	plain, err := svc.GetContextMode(ctx, symbol, ContextOptions{Mode: ModeFull, IncludeTrace: false})
	if err != nil {
		t.Fatalf("include_trace=false: %v", err)
	}
	if plain.Trace != nil {
		t.Fatal("include_trace=false 不应返回 _trace")
	}

	traced, err := svc.GetContextMode(ctx, symbol, ContextOptions{Mode: ModeFull, IncludeTrace: true})
	if err != nil {
		t.Fatalf("include_trace=true: %v", err)
	}
	if traced.Trace == nil {
		t.Fatal("include_trace=true 命中了 include_trace=false 的缓存条目（_trace 静默缺失）")
	}
	if traced.Trace.CacheHit {
		t.Fatal("include_trace 取值不同不应命中同一缓存条目")
	}
}

// TestQueryCacheReturnsDeepCopy P2-2 负控：命中返回对象必须与缓存隔离。
// 修复前是浅拷贝，调用方改 RelatedTests / Trace 会污染缓存，后续读取拿到脏值。
func TestQueryCacheReturnsDeepCopy(t *testing.T) {
	svc := newTestService(t)
	svc.EnableQueryCache(time.Minute, 16)
	ctx := context.Background()
	symbol := "com.example.OrderService.GetUser"
	opts := ContextOptions{Mode: ModeFull, IncludeTrace: true}

	// 直接预置缓存条目，避免依赖测试关联策略的可用性。
	svc.queryCache.Set(svc.queryCacheKey(symbol, opts), &SymbolContext{
		Symbol:       symbol,
		Source:       "cached",
		RelatedTests: []string{"pkg.TestA"},
		Trace: &TraceEntry{
			Source: "store.GetContext",
			Config: &TraceConfig{Mode: string(ModeFull)},
		},
	})

	first, err := svc.GetContextMode(ctx, symbol, opts)
	if err != nil {
		t.Fatalf("first: %v", err)
	}
	if len(first.RelatedTests) != 1 || first.RelatedTests[0] != "pkg.TestA" {
		t.Fatalf("预置缓存未命中: %+v", first)
	}
	first.RelatedTests[0] = "被污染"
	first.Trace.Source = "被污染"
	first.Trace.Config.Mode = "被污染"

	second, err := svc.GetContextMode(ctx, symbol, opts)
	if err != nil {
		t.Fatalf("second: %v", err)
	}
	if second.RelatedTests[0] != "pkg.TestA" || second.Trace.Source != "store.GetContext" ||
		second.Trace.Config.Mode != string(ModeFull) {
		t.Fatalf("调用方修改命中结果污染了缓存: %+v", second)
	}
}

// TestQueryCacheDisabled 未启用缓存时行为与启用前完全一致（向后兼容）。
func TestQueryCacheDisabled(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)
	if svc.QueryCacheEnabled() {
		t.Fatal("默认不应启用查询缓存")
	}
	if st := svc.QueryCacheStats(); st.Hits != 0 || st.Misses != 0 {
		t.Fatalf("未启用时统计应为零值, 实际 %+v", st)
	}
	svc.InvalidateQueryCache() // 未启用时必须是安全的空操作

	got, err := svc.GetContextMode(context.Background(), "com.example.OrderService.GetUser",
		ContextOptions{Mode: ModeFull, IncludeTrace: true})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if got.Trace.CacheHit {
		t.Fatal("未启用缓存时 cache_hit 应为 false")
	}
}

// TestPathVirtual 路径虚拟化：PathStyle=virtual 且已注入虚拟根时输出虚拟路径（B9）。
func TestPathVirtual(t *testing.T) {
	svc := newTestService(t)
	real := seedContextFile(t, svc)

	root, err := pathsafe.NewRoot(real[:len(real)-len("order.go")], "/codebase")
	if err != nil {
		t.Fatalf("NewRoot: %v", err)
	}
	svc.WithPathRoot(root)
	ctx := context.Background()

	abs, err := svc.GetContextMode(ctx, "com.example.OrderService.GetUser", ContextOptions{
		Mode: ModeFull, IncludeTrace: true, PathStyle: PathAbsolute,
	})
	if err != nil {
		t.Fatalf("absolute GetContextMode: %v", err)
	}
	if abs.FilePath != real {
		t.Fatalf("absolute 应输出真实路径 %q, 实际 %q", real, abs.FilePath)
	}

	virt, err := svc.GetContextMode(ctx, "com.example.OrderService.GetUser", ContextOptions{
		Mode: ModeFull, IncludeTrace: true, PathStyle: PathVirtual,
	})
	if err != nil {
		t.Fatalf("virtual GetContextMode: %v", err)
	}
	if virt.FilePath != "/codebase/order.go" {
		t.Fatalf("virtual 应输出 /codebase/order.go, 实际 %q", virt.FilePath)
	}
	if virt.Trace.Config == nil || virt.Trace.Config.PathStyle != "virtual" {
		t.Fatalf("生效配置应回传 path_style=virtual, 实际 %+v", virt.Trace.Config)
	}
}

// TestPathVirtualWithoutRoot 未注入虚拟根时，PathVirtual 应退化为真实路径而非报错。
func TestPathVirtualWithoutRoot(t *testing.T) {
	svc := newTestService(t)
	real := seedContextFile(t, svc)

	got, err := svc.GetContextMode(context.Background(), "com.example.OrderService.GetUser", ContextOptions{
		Mode: ModeFull, IncludeTrace: true, PathStyle: PathVirtual,
	})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if got.FilePath != real {
		t.Fatalf("未注入虚拟根时应退化输出真实路径 %q, 实际 %q", real, got.FilePath)
	}
}

// TestServiceMetrics 验证 service 层可观测化指标（P26）：预算降级计数、
// 查询级缓存命中计数、上下文查询计数均打到 metrics 输出。
func TestServiceMetrics(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)
	opts := ContextOptions{Mode: ModeFull, IncludeTrace: true}

	// 1. 预算触发降级 → context_queries_total + context_degraded_total
	if _, err := svc.GetContextMode(context.Background(), "com.example.OrderService.GetUser", ContextOptions{
		Mode: ModeFull, MaxBytes: 30, IncludeTrace: true,
	}); err != nil {
		t.Fatalf("GetContextMode(degrade): %v", err)
	}
	render := metrics.Render()
	if !strings.Contains(render, "context_queries_total") {
		t.Fatalf("缺少 context_queries_total 指标:\n%s", render)
	}
	if !strings.Contains(render, "context_degraded_total") {
		t.Fatal("缺少 context_degraded_total 指标（预算降级应计数）")
	}

	// 2. 启用查询缓存：首查 miss、二查 hit → querycache_misses/hits_total
	svc.EnableQueryCache(time.Minute, 16)
	if _, err := svc.GetContextMode(context.Background(), "com.example.OrderService.GetUser", opts); err != nil {
		t.Fatalf("first GetContextMode: %v", err)
	}
	if _, err := svc.GetContextMode(context.Background(), "com.example.OrderService.GetUser", opts); err != nil {
		t.Fatalf("second GetContextMode: %v", err)
	}
	render = metrics.Render()
	if !strings.Contains(render, `querycache_misses_total`) || !strings.Contains(render, `querycache_hits_total`) {
		t.Fatalf("查询缓存指标缺失:\n%s", render)
	}
}

// contextJSON 序列化上下文并清空时间戳，供「同输入同输出」比对。
func contextJSON(t *testing.T, sc *SymbolContext) string {
	t.Helper()
	cp := *sc
	if cp.Trace != nil {
		tr := *cp.Trace
		tr.Timestamp = ""
		cp.Trace = &tr
	}
	b, err := json.Marshal(cp)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return string(b)
}

// TestContextBudgetTokensAlias 负控（R6-1）：budget_tokens 必须与 max_tokens 等价，
// 且预算确实生效（输出小于无预算基线）。
func TestContextBudgetTokensAlias(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)
	ctx := context.Background()
	const sym = "com.example.OrderService.GetUser"

	base, err := svc.GetContextMode(ctx, sym, ContextOptions{Mode: ModeFull})
	if err != nil {
		t.Fatalf("baseline: %v", err)
	}
	asMax, err := svc.GetContextMode(ctx, sym, ContextOptions{Mode: ModeFull, MaxTokens: 10})
	if err != nil {
		t.Fatalf("max_tokens: %v", err)
	}
	asBudget, err := svc.GetContextMode(ctx, sym, ContextOptions{Mode: ModeFull, BudgetTokens: 10})
	if err != nil {
		t.Fatalf("budget_tokens: %v", err)
	}

	if got, want := contextJSON(t, asBudget), contextJSON(t, asMax); got != want {
		t.Fatalf("budget_tokens 与 max_tokens 输出不一致:\n got=%s\nwant=%s", got, want)
	}
	if len(asBudget.Source) >= len(base.Source) {
		t.Fatalf("预算未生效: budget=%d base=%d", len(asBudget.Source), len(base.Source))
	}
}

// TestContextBudgetTruncatedFlag 负控（R6-1）：极小预算必须显式 truncated=true
// 且 trim_reason 非空，调用方无需解析 _trace 即可判断被裁剪。
func TestContextBudgetTruncatedFlag(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)
	ctx := context.Background()
	const sym = "com.example.OrderService.GetUser"

	got, err := svc.GetContextMode(ctx, sym, ContextOptions{Mode: ModeFull, BudgetTokens: 5})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if !got.Truncated {
		t.Fatalf("极小预算应 truncated=true, trace=%+v", got.Trace)
	}
	if got.TrimReason == "" {
		t.Fatal("truncated 时 trim_reason 不能为空")
	}
	if len(got.IncludedSymbols) != 1 || got.IncludedSymbols[0] != sym {
		t.Fatalf("included_symbols=%v, want [%s]", got.IncludedSymbols, sym)
	}
}

// TestContextResponseFieldsMinimal 负控（R6-1）：minimal 模式也要有响应级状态字段。
func TestContextResponseFieldsMinimal(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)
	ctx := context.Background()
	const sym = "com.example.OrderService.GetUser"

	got, err := svc.GetContextMode(ctx, sym, ContextOptions{Mode: ModeMinimal})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if got.TrimReason != "mode_minimal" {
		t.Fatalf("trim_reason=%q, want mode_minimal", got.TrimReason)
	}
	if got.Truncated || got.Filtered != 0 {
		t.Fatalf("minimal 不应 truncated/filtered: %+v", got)
	}
	if len(got.IncludedSymbols) != 1 || got.IncludedSymbols[0] != sym {
		t.Fatalf("included_symbols=%v", got.IncludedSymbols)
	}
}

// TestContextSameInputSameOutput 负控（R6-1）：同输入连跑 20 次，除时间戳外逐字节一致。
func TestContextSameInputSameOutput(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)
	ctx := context.Background()
	const sym = "com.example.OrderService.GetUser"
	opts := ContextOptions{Mode: ModeFull, ContextLines: 2, BudgetTokens: 120}

	first := contextJSON(t, mustContext(t, svc, ctx, sym, opts))
	for i := 0; i < 20; i++ {
		if got := contextJSON(t, mustContext(t, svc, ctx, sym, opts)); got != first {
			t.Fatalf("第 %d 次输出漂移:\n got=%s\nwant=%s", i+1, got, first)
		}
	}
}

func mustContext(t *testing.T, svc *Service, ctx context.Context, sym string, opts ContextOptions) *SymbolContext {
	t.Helper()
	got, err := svc.GetContextMode(ctx, sym, opts)
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	return got
}
