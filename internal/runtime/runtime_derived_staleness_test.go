package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/scanner"
	"github.com/idcu/codeschema/internal/store"
)

// writeCalleeOnlyFixture 写一个只含 Target + 当前被调方的源文件：
// 切换被调方后旧符号在主存储中彻底消失，便于断言检索文档没有残留。
func writeCalleeOnlyFixture(t *testing.T, path, callee, pad string) {
	t.Helper()
	src := `package fixture

func Target() int {
	return ` + callee + `()
}

func ` + callee + `() int { return 1 }

// ` + pad + `
`
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
}

// failClassesStore 模拟「主存储已写入新 IR、增量索引读取类失败」的形态。
//
// 复审实证：BuildAndIndex 失败直接 return，调用方不再执行任何失效；
// 若派生缓存不以存储版本为准，调用图与 /search 会永久停在上一代。
type failClassesStore struct {
	store.Store
}

func (f failClassesStore) GetClassesByFileID(ctx context.Context, fileID int64) ([]store.ClassRecord, error) {
	return nil, errors.New("injected class read failure")
}

// countClassesForFile 直接读主存储，返回该文件当前的类记录数（不经过缓存）。
func countClassesForFile(t *testing.T, ctx context.Context, st store.Store, path string) int {
	t.Helper()
	f, err := st.GetFileByPath(ctx, path)
	if err != nil || f == nil {
		return 0
	}
	classes, err := st.GetClassesByFileID(ctx, f.ID)
	if err != nil {
		return -1
	}
	return len(classes)
}

// TestDerivedCachesSurviveIncrementalIndexFailure 派生缓存正确性不得依赖失效调用。
//
// 场景：文件从 helperA 改为 helperB，ProcessFile 已把新 IR 写入主存储，
// 但增量索引回调失败（提前 return）。调用图与 /search 必须按存储版本自愈，
// 不能继续返回 helperA。
func TestDerivedCachesSurviveIncrementalIndexFailure(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repoDir := t.TempDir()
	srcPath := filepath.Join(repoDir, "target.go")
	writeCalleeOnlyFixture(t, srcPath, "helperA", "padding-v1")

	cfg := noNetConfig(t, t.TempDir())
	cfg.Project.Root = repoDir
	st := openFileStore(t, cfg.Storage.DSN)
	defer st.Close()
	if err := ScanRepository(ctx, st, cfg, repoDir); err != nil {
		t.Fatalf("ScanRepository: %v", err)
	}
	run, err := BuildRuntime(ctx, st, cfg)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}

	// 预热派生缓存：此时必须是 helperA。
	cg, err := run.Analyzer.BuildCallGraph(ctx)
	if err != nil {
		t.Fatalf("BuildCallGraph(before): %v", err)
	}
	if !callGraphHasSuffix(cg, "helperA") {
		t.Fatalf("初始调用图缺少 helperA，用例前提不成立")
	}
	alphaFQN := methodFQNByName(ctx, t, st, "helperA")
	if !searchSnippetMatches(ctx, run.Svc, alphaFQN, "helperA") {
		t.Fatalf("初始 /search 看不到 helperA，用例前提不成立")
	}

	// 改文件（长度不同，确保不是哈希闸门跳过）。
	writeCalleeOnlyFixture(t, srcPath, "helperB", "padding-v2-longer")

	sc := scanner.NewScanner(st, NewParserRegistry(ctx, cfg, repoDir), 1)
	sc.SetOnIndex(func(ctx context.Context, filePath string) error {
		// 注入失败：BuildAndIndex 在读类时提前返回，随后没有任何失效调用。
		return run.Builder.BuildAndIndex(ctx, failClassesStore{Store: st}, filePath)
	})
	if err := sc.ProcessFile(ctx, srcPath); err != nil {
		t.Fatalf("ProcessFile: %v", err)
	}

	// 主存储（正确性基准）已切换：新边存在、旧边消失。
	waitFor(t, 5*time.Second, "主存储未写入 helperB 调用边", func() bool {
		return storeHasCalleeSuffix(t, ctx, st, "helperB")
	})
	if storeHasCalleeSuffix(t, ctx, st, "helperA") {
		t.Fatalf("主存储仍含 helperA 调用边，用例前提不成立")
	}

	// 派生缓存必须自愈（不依赖任何 Invalidate 调用）。
	betaFQN := methodFQNByName(ctx, t, st, "helperB")
	waitFor(t, 10*time.Second, "/search 仍命中已删除的 helperA（检索文档陈旧）", func() bool {
		return !searchSnippetMatches(ctx, run.Svc, alphaFQN, "helperA")
	})
	waitFor(t, 10*time.Second, "/search 看不到新符号 helperB（检索文档陈旧）", func() bool {
		return searchSnippetMatches(ctx, run.Svc, betaFQN, "helperB")
	})
	waitFor(t, 10*time.Second, "调用图仍返回已删除的 helperA 边（增量失败后缓存陈旧）", func() bool {
		g, err := run.Analyzer.BuildCallGraph(ctx)
		return err == nil && callGraphHasSuffix(g, "helperB") && !callGraphHasSuffix(g, "helperA")
	})
}

// TestDerivedCachesDropSymbolsOnParseSkipped parse_skipped 清空 IR 后，
// 调用图与 /search 必须立刻看不到旧符号（复审实证：markSkipped 只清 Store）。
func TestDerivedCachesDropSymbolsOnParseSkipped(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repoDir := t.TempDir()
	srcPath := filepath.Join(repoDir, "target.go")
	writeWatchFixture(t, srcPath, "helperA", "padding-v1")

	cfg := noNetConfig(t, t.TempDir())
	cfg.Project.Root = repoDir
	st := openFileStore(t, cfg.Storage.DSN)
	defer st.Close()
	if err := ScanRepository(ctx, st, cfg, repoDir); err != nil {
		t.Fatalf("ScanRepository: %v", err)
	}
	run, err := BuildRuntime(ctx, st, cfg)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}

	// 预热：调用图与 /search 都看到 helperA。
	cg, err := run.Analyzer.BuildCallGraph(ctx)
	if err != nil {
		t.Fatalf("BuildCallGraph(before): %v", err)
	}
	if !callGraphHasSuffix(cg, "helperA") {
		t.Fatalf("初始调用图缺少 helperA，用例前提不成立")
	}
	alphaFQN := methodFQNByName(ctx, t, st, "helperA")
	if !searchSnippetMatches(ctx, run.Svc, alphaFQN, "helperA") {
		t.Fatalf("初始 /search 看不到 helperA，用例前提不成立")
	}

	// 超限旁路：size limit 1 字节 → markSkipped 清空该类文件的 IR。
	sc := scanner.NewScanner(st, NewParserRegistry(ctx, cfg, repoDir), 1)
	sc.SetLimits(1, 0)
	if err := sc.ProcessFile(ctx, srcPath); err != nil {
		t.Fatalf("ProcessFile(skip): %v", err)
	}
	if n := countClassesForFile(t, ctx, st, srcPath); n != 0 {
		t.Fatalf("主存储类记录 = %d，期望 0（parse_skipped 应清空 IR）", n)
	}

	// 派生缓存必须自愈：/search 不再命中旧符号，调用图无旧边。
	waitFor(t, 10*time.Second, "/search 仍命中 parse_skipped 前的旧检索文档", func() bool {
		return !searchSnippetMatches(ctx, run.Svc, alphaFQN, "helperA")
	})
	waitFor(t, 10*time.Second, "调用图仍返回 parse_skipped 文件的旧调用边", func() bool {
		g, err := run.Analyzer.BuildCallGraph(ctx)
		return err == nil && !callGraphHasSuffix(g, "helperA")
	})
}
