package service

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/idcu/codeschema/internal/analyzer"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// seedContextFile 在临时目录创建真实源文件并入库，返回文件绝对路径。
//
// 文件内容（行号与 ClassIR/MethodIR 的行区间对齐）：
//
//	1  package demo
//	2
//	3  type OrderService struct{}
//	4
//	5  func (s *OrderService) GetUser(id int) string {
//	6      return "user"
//	7  }
func seedContextFile(t testing.TB, svc *Service) string {
	t.Helper()
	content := "package demo\n\ntype OrderService struct{}\n\nfunc (s *OrderService) GetUser(id int) string {\n\treturn \"user\"\n}\n"
	path := filepath.Join(t.TempDir(), "order.go")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write file: %v", err)
	}

	ir := &parser.IRDocument{
		Source:    "test",
		Language:  "go",
		FilePath:  path,
		FileHash:  "h-order",
		LineCount: 7,
		ByteSize:  int64(len(content)),
		Classes: []parser.ClassIR{{
			Name:      "OrderService",
			FullName:  "com.example.OrderService",
			Type:      "CLASS",
			StartLine: 3,
			EndLine:   3,
		}},
		Methods: []parser.MethodIR{{
			Name:      "GetUser",
			ClassFQN:  "com.example.OrderService",
			Signature: "GetUser(id int) string",
			StartLine: 5,
			EndLine:   7,
			Doc:       "按 ID 查询用户",
		}},
	}
	if err := svc.store.UpsertIR(context.Background(), ir); err != nil {
		t.Fatalf("upsert ir: %v", err)
	}
	return path
}

func TestGetContextMode_Full_Trace(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)

	ctx, err := svc.GetContextMode(context.Background(), "com.example.OrderService.GetUser", ContextOptions{
		Mode:         ModeFull,
		IncludeTrace: true,
	})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if ctx.Trace == nil {
		t.Fatal("expected non-nil trace in full mode")
	}
	if ctx.Trace.Source != "store.GetContext" {
		t.Errorf("expected source store.GetContext, got %s", ctx.Trace.Source)
	}
	// 方法行区间 5-7 = 3 行，注入完整内容（无裁剪）
	if ctx.Trace.HitLines != 3 {
		t.Errorf("expected HitLines 3, got %d", ctx.Trace.HitLines)
	}
	if ctx.Trace.TrimReason != "full" {
		t.Errorf("expected TrimReason full, got %s", ctx.Trace.TrimReason)
	}
	if ctx.Trace.TrimmedLines != 0 {
		t.Errorf("expected TrimmedLines 0, got %d", ctx.Trace.TrimmedLines)
	}
	if ctx.Trace.TokenEstimate != 3*4 {
		t.Errorf("expected TokenEstimate %d, got %d", 3*4, ctx.Trace.TokenEstimate)
	}
	if ctx.Trace.Timestamp == "" {
		t.Error("expected non-empty timestamp")
	}
	// 注入的是源码原文
	if !strings.Contains(ctx.Source, "GetUser") || !strings.Contains(ctx.Source, "return \"user\"") {
		t.Errorf("expected source code body in full mode, got %q", ctx.Source)
	}
}

func TestGetContextMode_Full_ContextLines_Trace(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)

	ctx, err := svc.GetContextMode(context.Background(), "com.example.OrderService.GetUser", ContextOptions{
		ContextLines: 2,
		Mode:         ModeFull,
		IncludeTrace: true,
	})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if ctx.Trace == nil {
		t.Fatal("expected non-nil trace")
	}
	if ctx.Trace.TrimReason != "context_lines" {
		t.Errorf("expected TrimReason context_lines, got %s", ctx.Trace.TrimReason)
	}
	// 方法 5-7 + 前后各 2 行 → 5 行注入（含第 3 行 struct），裁剪 7-5=2 行
	if ctx.Trace.HitLines != 5 {
		t.Errorf("expected HitLines 5, got %d", ctx.Trace.HitLines)
	}
	if ctx.Trace.TrimmedLines != 2 {
		t.Errorf("expected TrimmedLines 2, got %d", ctx.Trace.TrimmedLines)
	}
	if ctx.Trace.TokenEstimate != 5*4 {
		t.Errorf("expected TokenEstimate %d, got %d", 5*4, ctx.Trace.TokenEstimate)
	}
	// 上下文应包含符号体前的 struct 定义（第 3 行）
	if !strings.Contains(ctx.Source, "type OrderService struct{}") {
		t.Errorf("expected surrounding context, got %q", ctx.Source)
	}
}

func TestGetContextMode_Minimal_Trace(t *testing.T) {
	svc := newTestService(t)
	path := seedContextFile(t, svc)

	ctx, err := svc.GetContextMode(context.Background(), "com.example.OrderService.GetUser", ContextOptions{
		Mode:         ModeMinimal,
		IncludeTrace: true,
	})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if ctx.Trace == nil {
		t.Fatal("expected non-nil trace")
	}
	if ctx.Trace.TrimReason != "mode_minimal" {
		t.Errorf("expected TrimReason mode_minimal, got %s", ctx.Trace.TrimReason)
	}
	// 极简模式：仅 1 行定位摘要，token 估算 4
	if ctx.Trace.HitLines != 1 {
		t.Errorf("expected HitLines 1, got %d", ctx.Trace.HitLines)
	}
	if ctx.Trace.TokenEstimate != 4 {
		t.Errorf("expected TokenEstimate 4, got %d", ctx.Trace.TokenEstimate)
	}
	// 不喂源码原文：source 是定位摘要而非方法体
	if strings.Contains(ctx.Source, "return \"user\"") {
		t.Errorf("expected no source body in minimal mode, got %q", ctx.Source)
	}
	if !strings.Contains(ctx.Source, "lines 5-7") {
		t.Errorf("expected line range in minimal summary, got %q", ctx.Source)
	}
	if ctx.FilePath != path {
		t.Errorf("expected FilePath %s, got %s", path, ctx.FilePath)
	}
}

func TestGetContextMode_FileUnreadable_FallbackTrace(t *testing.T) {
	svc := newTestService(t)
	// 入库但磁盘上不存在的文件 → full 模式读文件失败 → 回退 minimal 形态并留痕
	ir := &parser.IRDocument{
		Source:    "test",
		Language:  "go",
		FilePath:  filepath.Join(t.TempDir(), "missing.go"),
		FileHash:  "h-missing",
		LineCount: 10,
		ByteSize:  100,
		Classes: []parser.ClassIR{{
			Name:      "Gone",
			FullName:  "com.example.Gone",
			Type:      "CLASS",
			StartLine: 1,
			EndLine:   10,
		}},
	}
	if err := svc.store.UpsertIR(context.Background(), ir); err != nil {
		t.Fatalf("upsert ir: %v", err)
	}

	ctx, err := svc.GetContextMode(context.Background(), "com.example.Gone", ContextOptions{
		Mode:         ModeFull,
		IncludeTrace: true,
	})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if ctx.Trace == nil {
		t.Fatal("expected non-nil trace on fallback")
	}
	if ctx.Trace.TrimReason != "file_unreadable" {
		t.Errorf("expected TrimReason file_unreadable, got %s", ctx.Trace.TrimReason)
	}
	if ctx.Trace.HitLines != 0 {
		t.Errorf("expected HitLines 0 on unreadable, got %d", ctx.Trace.HitLines)
	}
}

func TestGetContextMode_IncludeTraceFalse(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)

	ctx, err := svc.GetContextMode(context.Background(), "com.example.OrderService.GetUser", ContextOptions{
		Mode:         ModeFull,
		IncludeTrace: false,
	})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if ctx.Trace != nil {
		t.Error("expected nil trace when IncludeTrace=false")
	}
}

func TestGetContextMode_EmptySymbol(t *testing.T) {
	svc := newTestService(t)
	_, err := svc.GetContextMode(context.Background(), "", ContextOptions{Mode: ModeFull})
	if err == nil {
		t.Fatal("expected error for empty symbol")
	}
	if svcErr, ok := err.(*ServiceError); !ok || svcErr.Code != "ERR_INVALID_PARAMETER" {
		t.Errorf("expected ERR_INVALID_PARAMETER, got %v", err)
	}
}

func TestGetContextMode_DefaultModeIsFull(t *testing.T) {
	svc := newTestService(t)
	path := seedContextFile(t, svc)

	// 未显式指定 Mode → 默认 full：注入源码原文
	ctx, err := svc.GetContextMode(context.Background(), "com.example.OrderService.GetUser", ContextOptions{
		IncludeTrace: true,
	})
	if err != nil {
		t.Fatalf("GetContextMode: %v", err)
	}
	if !strings.Contains(ctx.Source, "return \"user\"") {
		t.Errorf("expected source body by default full mode, got %q", ctx.Source)
	}
	if ctx.FilePath != path {
		t.Errorf("expected FilePath %s, got %s", path, ctx.FilePath)
	}
}

func TestGetImpact_Trace_WithAnalyzer(t *testing.T) {
	dir := t.TempDir()
	st := store.NewStore("file")
	if err := st.Open(context.Background(), dir); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { st.Close() })

	svc := NewService(st)
	an := analyzer.NewAnalyzer(st)
	svc.WithImpactAnalyzer(an)

	// handler.Handle → service.GetUser
	ir1 := &parser.IRDocument{
		Source: "test", Language: "go", FilePath: "/proj/handler.go", FileHash: "h1", LineCount: 30, ByteSize: 1024,
		Classes: []parser.ClassIR{{Name: "Handler", FullName: "com.example.Handler", Type: "CLASS"}},
		Methods: []parser.MethodIR{{Name: "Handle", ClassFQN: "com.example.Handler"}},
	}
	ir2 := &parser.IRDocument{
		Source: "test", Language: "go", FilePath: "/proj/service.go", FileHash: "h2", LineCount: 50, ByteSize: 2048,
		Classes: []parser.ClassIR{{Name: "Service", FullName: "com.example.Service", Type: "CLASS"}},
		Methods: []parser.MethodIR{{Name: "GetUser", ClassFQN: "com.example.Service"}},
		Calls: []parser.CallIR{
			{CallerFQN: "com.example.Handler.Handle", CalleeFQN: "com.example.Service.GetUser", CallType: "direct", LineNumber: 10},
		},
	}
	if err := st.UpsertIR(context.Background(), ir1); err != nil {
		t.Fatalf("upsert handler: %v", err)
	}
	if err := st.UpsertIR(context.Background(), ir2); err != nil {
		t.Fatalf("upsert service: %v", err)
	}

	result, err := svc.GetImpact(context.Background(), "com.example.Service.GetUser", 1)
	if err != nil {
		t.Fatalf("GetImpact: %v", err)
	}
	if result.Trace == nil {
		t.Fatal("expected non-nil trace on impact with analyzer")
	}
	if result.Trace.Source != "store.GetImpact" {
		t.Errorf("expected source store.GetImpact, got %s", result.Trace.Source)
	}
	// 1 个 caller，0 个 callee → HitSymbols = 1
	if result.Trace.HitSymbols != 1 {
		t.Errorf("expected HitSymbols 1, got %d", result.Trace.HitSymbols)
	}
	if result.Trace.TrimReason != "depth_limit" {
		t.Errorf("expected TrimReason depth_limit, got %s", result.Trace.TrimReason)
	}
	if result.Trace.TokenEstimate != 1*4 {
		t.Errorf("expected TokenEstimate %d, got %d", 1*4, result.Trace.TokenEstimate)
	}
}

func TestGetImpact_Trace_WithoutAnalyzer(t *testing.T) {
	svc := newTestService(t)
	result, err := svc.GetImpact(context.Background(), "com.example.MyClass.myMethod", 2)
	if err != nil {
		t.Fatalf("GetImpact: %v", err)
	}
	if result.Trace == nil {
		t.Fatal("expected non-nil trace even without analyzer")
	}
	if result.Trace.TrimReason != "analyzer_unavailable" {
		t.Errorf("expected TrimReason analyzer_unavailable, got %s", result.Trace.TrimReason)
	}
	if result.Trace.HitSymbols != 0 {
		t.Errorf("expected HitSymbols 0, got %d", result.Trace.HitSymbols)
	}
}

// cacheReaderStore 包装真实 FileStore 并实现 store.CacheReader（模拟 Redis L2 缓存层），
// 用于验证 resolveSymbolLocation 的缓存快速路径（类符号命中）。
type cacheReaderStore struct {
	*store.FileStore
	classes map[string]*parser.ClassIR
	paths   map[string]string
}

func (c *cacheReaderStore) GetClass(ctx context.Context, fqn string) (*parser.ClassIR, error) {
	if cls, ok := c.classes[fqn]; ok {
		return cls, nil
	}
	return nil, nil
}

func (c *cacheReaderStore) ClassFilePath(ctx context.Context, fqn string) (string, bool) {
	p, ok := c.paths[fqn]
	return p, ok
}

func (c *cacheReaderStore) CallersOf(ctx context.Context, fqn string) ([]string, error) {
	return nil, nil
}
func (c *cacheReaderStore) CalleesOf(ctx context.Context, fqn string) ([]string, error) {
	return nil, nil
}
func (c *cacheReaderStore) ClassesOfFile(ctx context.Context, path string) ([]string, error) {
	return nil, nil
}

func TestGetContextMode_ClassViaCacheFastPath(t *testing.T) {
	// 构建真实 FileStore 并播种（方法解析需要类记录，缓存只加速类命中）。
	svc := newTestService(t)
	path := seedContextFile(t, svc)

	// 包装为缓存 store：类 FQN → ClassIR + 路径索引。
	cached := &cacheReaderStore{
		FileStore: svc.store.(*store.FileStore),
		classes: map[string]*parser.ClassIR{
			"com.example.OrderService": {
				Name: "OrderService", FullName: "com.example.OrderService",
				Type: "CLASS", StartLine: 3, EndLine: 3,
			},
		},
		paths: map[string]string{
			"com.example.OrderService": path,
		},
	}
	svc2 := NewService(cached)

	// 类符号：应经缓存快速路径命中（即便 FileStore 全表也不丢，此路径证明缓存被使用）。
	ctx, err := svc2.GetContextMode(context.Background(), "com.example.OrderService", ContextOptions{
		Mode:         ModeFull,
		IncludeTrace: true,
	})
	if err != nil {
		t.Fatalf("GetContextMode class via cache: %v", err)
	}
	if ctx == nil || ctx.Symbol != "com.example.OrderService" {
		t.Fatalf("unexpected context: %+v", ctx)
	}
	if ctx.StartLine != 3 || ctx.EndLine != 3 {
		t.Errorf("class lines via cache: %d-%d, want 3-3", ctx.StartLine, ctx.EndLine)
	}
	if ctx.Trace == nil || ctx.Trace.Source != "store.GetContext" {
		t.Errorf("expected trace from cache fast path, got %+v", ctx.Trace)
	}
}

func TestGetContextMode_ClassCacheMissFallsBack(t *testing.T) {
	svc := newTestService(t)
	seedContextFile(t, svc)

	// 缓存不包含该类 → 必须回退 FileStore 全表解析成功。
	cached := &cacheReaderStore{
		FileStore: svc.store.(*store.FileStore),
		classes:   map[string]*parser.ClassIR{},
		paths:     map[string]string{},
	}
	svc2 := NewService(cached)

	ctx, err := svc2.GetContextMode(context.Background(), "com.example.OrderService", ContextOptions{
		Mode: ModeFull,
	})
	if err != nil {
		t.Fatalf("GetContextMode cache miss fallback: %v", err)
	}
	if ctx == nil || !strings.Contains(ctx.FilePath, "order.go") {
		t.Fatalf("expected fallback to FileStore, got %+v", ctx)
	}
}
