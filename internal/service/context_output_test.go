package service

import (
	"context"
	"strings"
	"testing"
	"time"

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
