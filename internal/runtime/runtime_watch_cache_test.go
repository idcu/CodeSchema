package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/analyzer"
	"github.com/idcu/codeschema/internal/store"
)

// writeWatchFixture 写一个可切换被调方的 Go 源文件，用于观测调用图是否更新。
func writeWatchFixture(t *testing.T, path, callee, pad string) {
	t.Helper()
	src := `package fixture

func Target() int {
	return ` + callee + `()
}

func helperA() int { return 1 }

func helperB() int { return 2 }

// ` + pad + `
`
	if err := os.WriteFile(path, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
}

// callGraphHasSuffix 判断调用图中是否存在以 suffix 结尾的被调用方。
func callGraphHasSuffix(cg *analyzer.CallGraph, suffix string) bool {
	if cg == nil {
		return false
	}
	for _, n := range cg.Nodes {
		for _, callee := range n.Callees {
			if strings.HasSuffix(callee, suffix) {
				return true
			}
		}
	}
	return false
}

// storeHasCalleeSuffix 直接读主存储的调用边，不经过任何缓存。
func storeHasCalleeSuffix(t *testing.T, ctx context.Context, st store.Store, suffix string) bool {
	t.Helper()
	files, err := st.GetAllFiles(ctx)
	if err != nil {
		return false
	}
	for _, f := range files {
		calls, err := st.GetCallsByFileID(ctx, f.ID)
		if err != nil {
			continue
		}
		for _, c := range calls {
			if strings.HasSuffix(c.CalleeFQN, suffix) {
				return true
			}
		}
	}
	return false
}

// TestWatchIncrementalUpdateInvalidatesRuntimeCaches 对抗性用例（场景 2）：
// watch 增量更新与手动全量重建交替触发后，查询侧 Runtime 的派生缓存
// （调用图 / 测试关联快照）必须失效，不能继续返回上一代数据。
//
// 该用例针对多租户 Runtime 的实际接线：tenant.buildTenant 把 run.Svc 交给 HTTP
// 查询，而 StartWatchBackground 若自建 Service，失效就落在另一个实例上。
func TestWatchIncrementalUpdateInvalidatesRuntimeCaches(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repoDir := t.TempDir()
	srcPath := filepath.Join(repoDir, "target.go")
	writeWatchFixture(t, srcPath, "helperA", "padding-v1")

	storeDir := t.TempDir()
	cfg := noNetConfig(t, storeDir)
	cfg.Project.Root = repoDir
	cfg.Watcher.DebounceMs = 20

	st := openFileStore(t, storeDir)
	defer st.Close()
	if err := ScanRepository(ctx, st, cfg, repoDir); err != nil {
		t.Fatalf("ScanRepository: %v", err)
	}
	run, err := BuildRuntime(ctx, st, cfg)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}

	// 预热查询侧调用图缓存：此时必须是 helperA。
	first, err := run.Analyzer.BuildCallGraph(ctx)
	if err != nil {
		t.Fatalf("BuildCallGraph(before): %v", err)
	}
	if !callGraphHasSuffix(first, "helperA") {
		t.Fatalf("初始调用图缺少 helperA，用例前提不成立")
	}

	stop, err := run.StartWatchBackground(ctx, st, cfg, repoDir)
	if err != nil {
		t.Fatalf("StartWatchBackground: %v", err)
	}
	defer stop()

	// 等监听器完成首轮基线快照（buildInitialState 在自身 goroutine 中执行），
	// 否则紧接着的写文件会被当成"初始状态"而漏掉变更事件。
	time.Sleep(500 * time.Millisecond)

	// 增量更新：Target 改为调用 helperB（长度不同，确保监听器判定为变更）。
	writeWatchFixture(t, srcPath, "helperB", "padding-v2-longer")

	// 先等主存储可见新边——这一步不依赖任何缓存。
	waitUntil(t, 15*time.Second, func() bool {
		return storeHasCalleeSuffix(t, ctx, st, "helperB")
	})

	// 主存储已更新，watcher 回调随即失效派生缓存；查询侧必须能读到新图。
	waitUntil(t, 3*time.Second, func() bool {
		cg, err := run.Analyzer.BuildCallGraph(ctx)
		return err == nil && callGraphHasSuffix(cg, "helperB")
	})

	// 交替触发手动全量重建后，再来一次增量更新，仍需立即失效。
	if _, err := run.Svc.BuildIndex(ctx); err != nil {
		t.Fatalf("BuildIndex: %v", err)
	}
	writeWatchFixture(t, srcPath, "helperA", "padding-v3-longer-still")
	waitUntil(t, 15*time.Second, func() bool {
		return storeHasCalleeSuffix(t, ctx, st, "helperA") && !storeHasCalleeSuffix(t, ctx, st, "helperB")
	})
	waitUntil(t, 3*time.Second, func() bool {
		cg, err := run.Analyzer.BuildCallGraph(ctx)
		return err == nil && callGraphHasSuffix(cg, "helperA") && !callGraphHasSuffix(cg, "helperB")
	})
}

// waitUntil 轮询直到条件成立或超时；超时直接让用例失败。
func waitUntil(t *testing.T, timeout time.Duration, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("条件在 %s 内未成立", timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}
