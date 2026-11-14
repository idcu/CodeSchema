package service

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// cacheErrStore 模拟 Redis 不可用：所有缓存读接口返回错误。
// 上层必须把读错误当作未命中回退主存储，而不是返回空结果或报错。
type cacheErrStore struct {
	*store.FileStore
}

var errCacheDown = errors.New("redis: connection refused")

func (c *cacheErrStore) GetClass(context.Context, string) (*parser.ClassIR, error) {
	return nil, errCacheDown
}

func (c *cacheErrStore) ClassFilePath(context.Context, string) (string, bool) {
	return "", false
}

func (c *cacheErrStore) GetMethod(context.Context, string) (*parser.MethodIR, error) {
	return nil, errCacheDown
}

func (c *cacheErrStore) MethodFilePath(context.Context, string) (string, bool) {
	return "", false
}

func (c *cacheErrStore) CallersOf(context.Context, string) ([]string, error) {
	return nil, errCacheDown
}

func (c *cacheErrStore) CalleesOf(context.Context, string) ([]string, error) {
	return nil, errCacheDown
}

func (c *cacheErrStore) ClassesOfFile(context.Context, string) ([]string, error) {
	return nil, errCacheDown
}

// TestGetContext_CacheReadErrorFallsBackToStore 对抗性用例（场景 4）：
// Redis 读路径报错时必须降级到主存储全表扫描，返回与缓存健康时逐条一致的上下文，
// 不能把"缓存不可用"表现为符号不存在。
func TestGetContext_CacheReadErrorFallsBackToStore(t *testing.T) {
	svc := newTestService(t)
	path := seedContextFile(t, svc)

	// 缓存健康时的基线。
	want, err := svc.GetContextMode(context.Background(), "com.example.OrderService.GetUser", ContextOptions{Mode: ModeFull})
	if err != nil {
		t.Fatalf("baseline GetContextMode: %v", err)
	}

	base, ok := svc.store.(*store.FileStore)
	if !ok {
		t.Fatalf("expected *store.FileStore, got %T", svc.store)
	}
	degraded := NewService(&cacheErrStore{FileStore: base})

	got, err := degraded.GetContextMode(context.Background(), "com.example.OrderService.GetUser", ContextOptions{Mode: ModeFull})
	if err != nil {
		t.Fatalf("缓存不可用时不应报错，应回退主存储: %v", err)
	}
	if got.FilePath != path || got.FilePath != want.FilePath {
		t.Fatalf("回退结果路径不一致: got=%s want=%s", got.FilePath, want.FilePath)
	}
	if got.Source != want.Source {
		t.Fatalf("回退结果源码不一致（长度 got=%d want=%d）", len(got.Source), len(want.Source))
	}
	if !strings.Contains(got.Source, "return \"user\"") {
		t.Fatalf("回退结果缺少方法体: %q", got.Source)
	}
	if got.StartLine != want.StartLine || got.EndLine != want.EndLine {
		t.Fatalf("回退结果行区间不一致: got=%d-%d want=%d-%d",
			got.StartLine, got.EndLine, want.StartLine, want.EndLine)
	}
}
