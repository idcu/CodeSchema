package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
)

// writeGoSource 写一个 Go 源文件（测试 fixture 用）。
func writeGoSource(t *testing.T, path, src string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
}

// methodFQNByName 从主存储取方法的 FullName，避免依赖适配器的 FQN 生成细节。
func methodFQNByName(ctx context.Context, t *testing.T, st store.Store, name string) string {
	t.Helper()
	files, err := st.GetAllFiles(ctx)
	if err != nil {
		t.Fatalf("GetAllFiles: %v", err)
	}
	for _, f := range files {
		classes, _ := st.GetClassesByFileID(ctx, f.ID)
		for _, c := range classes {
			methods, _ := st.GetMethodsByClassID(ctx, c.ID)
			for _, m := range methods {
				if m.Name == name {
					return m.FullName
				}
			}
		}
	}
	t.Fatalf("method %q not found in store", name)
	return ""
}

// serviceErrorCode 提取 ServiceError 的稳定错误码（非 ServiceError 返回空串）。
func serviceErrorCode(err error) string {
	var se *service.ServiceError
	if errors.As(err, &se) {
		return se.Code
	}
	return ""
}

// impactHasMethodSuffix 判断影响面节点中是否存在以 suffix 结尾的方法。
func impactHasMethodSuffix(imp *service.ImpactResult, suffix string) bool {
	if imp == nil {
		return false
	}
	for _, n := range imp.Callers {
		if strings.HasSuffix(n.Method, suffix) {
			return true
		}
	}
	for _, n := range imp.Callees {
		if strings.HasSuffix(n.Method, suffix) {
			return true
		}
	}
	return false
}

// testResultsContain 判断关联单测中是否存在以 suffix 结尾的测试方法。
func testResultsContain(links []service.TestResult, suffix string) bool {
	for _, l := range links {
		if strings.HasSuffix(l.TestMethod, suffix) {
			return true
		}
	}
	return false
}

// storeCallEdges 返回主存储中的全部调用边（仅用于失败诊断）。
func storeCallEdges(ctx context.Context, st store.Store) []string {
	files, err := st.GetAllFiles(ctx)
	if err != nil {
		return nil
	}
	var out []string
	for _, f := range files {
		calls, _ := st.GetCallsByFileID(ctx, f.ID)
		for _, c := range calls {
			out = append(out, c.CallerFQN+"->"+c.CalleeFQN)
		}
	}
	return out
}

// storeFileGone 判断主存储中已无该路径的文件记录。
func storeFileGone(ctx context.Context, st store.Store, path string) bool {
	f, err := st.GetFileByPath(ctx, path)
	return err == nil && f == nil
}

// TestWatchPollDeletionRemovesIndexedFile 对抗性用例（§8.8 缺口）：
// poll watcher 识别删除 → 清理必须落到主存储/检索文档/派生缓存，
// /context 返回 not found、/impact 不再有已删除文件的边、/tests 不再关联已删除测试。
//
// 同一用例续做改名（旧路径消失、新路径出现），旧路径不得残留任何条目。
func TestWatchPollDeletionRemovesIndexedFile(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repoDir := t.TempDir()
	targetPath := filepath.Join(repoDir, "target.go")
	callerPath := filepath.Join(repoDir, "caller.go")
	testPath := filepath.Join(repoDir, "target_test.go")
	writeGoSource(t, targetPath, "package fixture\n\nfunc Target() int {\n\treturn 1\n}\n")
	writeGoSource(t, callerPath, "package fixture\n\nfunc Caller() int {\n\treturn Target()\n}\n")
	writeGoSource(t, testPath, "package fixture\n\nimport \"testing\"\n\nfunc TestTarget(t *testing.T) {\n\t_ = Target()\n}\n")

	cfg := noNetConfig(t, t.TempDir())
	cfg.Project.Root = repoDir
	cfg.Watcher.UseFsnotify = false // 默认 poll 路径
	cfg.Watcher.DebounceMs = 20
	// 打开查询缓存：删除后缓存键必须被失效，而不是命中旧上下文。
	cfg.Context.QueryCache.Enabled = true
	cfg.Context.QueryCache.TTLMS = 600000
	cfg.Context.QueryCache.MaxEntries = 128

	st := openFileStore(t, cfg.Storage.DSN)
	defer st.Close()
	if err := ScanRepository(ctx, st, cfg, repoDir); err != nil {
		t.Fatalf("ScanRepository: %v", err)
	}
	run, err := BuildRuntime(ctx, st, cfg)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}

	callerFQN := methodFQNByName(ctx, t, st, "Caller")
	targetFQN := methodFQNByName(ctx, t, st, "Target")

	// 预热三类查询缓存：查询缓存、调用图缓存、测试关联快照。
	if _, err := run.Svc.GetContextMode(ctx, callerFQN, service.ContextOptions{Mode: service.ModeMinimal}); err != nil {
		t.Fatalf("删除前 GetContext 应成功（用例前提）：%v", err)
	}
	beforeImpact, err := run.Svc.GetImpact(ctx, targetFQN, 2)
	if err != nil {
		t.Fatalf("删除前 GetImpact: %v", err)
	}
	if !impactHasMethodSuffix(beforeImpact, "Caller") {
		t.Fatalf("用例前提不成立：删除前影响面缺少 Caller，callers=%+v；Store 调用边=%v",
			beforeImpact.Callers, storeCallEdges(ctx, st))
	}
	beforeTests, err := run.Svc.GetTests(ctx, targetFQN, 60)
	if err != nil {
		t.Fatalf("删除前 GetTests: %v", err)
	}
	if !testResultsContain(beforeTests, "TestTarget") {
		t.Fatalf("用例前提不成立：删除前未关联 TestTarget，实际 %+v", beforeTests)
	}

	stop, err := run.StartWatchBackground(ctx, st, cfg, repoDir)
	if err != nil {
		t.Fatalf("StartWatchBackground: %v", err)
	}
	defer stop()

	// 等 poll watcher 完成首轮基线快照。
	time.Sleep(500 * time.Millisecond)

	if err := os.Remove(callerPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(testPath); err != nil {
		t.Fatal(err)
	}

	// 主存储行必须先消失——这是后续所有查询清理的前提。
	waitUntil(t, 10*time.Second, func() bool {
		return storeFileGone(ctx, st, callerPath) && storeFileGone(ctx, st, testPath)
	})

	// /context：已删除文件中的符号必须 not found（不能返回缓存或残留 IR）。
	waitUntil(t, 5*time.Second, func() bool {
		_, err := run.Svc.GetContextMode(ctx, callerFQN, service.ContextOptions{Mode: service.ModeMinimal})
		return serviceErrorCode(err) == "ERR_SYMBOL_NOT_FOUND"
	})

	// /impact：不得再有已删除文件的调用边。
	waitUntil(t, 5*time.Second, func() bool {
		imp, err := run.Svc.GetImpact(ctx, targetFQN, 2)
		return err == nil && !impactHasMethodSuffix(imp, "Caller")
	})

	// /tests：不得再关联已删除的测试文件。
	waitUntil(t, 5*time.Second, func() bool {
		links, err := run.Svc.GetTests(ctx, targetFQN, 60)
		return err == nil && !testResultsContain(links, "TestTarget")
	})

	// 标签：已删除符号的标签查询必须 not found（标签表无残留）。
	waitUntil(t, 5*time.Second, func() bool {
		_, err := run.Svc.GetTags(ctx, callerFQN)
		return serviceErrorCode(err) == "ERR_SYMBOL_NOT_FOUND"
	})

	// 改名：旧路径消失 + 新路径出现，旧条目不得残留。
	renamedPath := filepath.Join(repoDir, "renamed.go")
	if err := os.Rename(targetPath, renamedPath); err != nil {
		t.Fatal(err)
	}
	waitUntil(t, 10*time.Second, func() bool {
		return storeFileGone(ctx, st, targetPath) && !storeFileGone(ctx, st, renamedPath)
	})
	waitUntil(t, 5*time.Second, func() bool {
		got, err := run.Svc.GetContextMode(ctx, targetFQN, service.ContextOptions{Mode: service.ModeMinimal})
		return err == nil && got != nil && got.FilePath == renamedPath
	})

	files, err := st.GetAllFiles(ctx)
	if err != nil {
		t.Fatalf("GetAllFiles: %v", err)
	}
	for _, f := range files {
		if f.AbsolutePath == targetPath {
			t.Fatalf("改名后旧路径仍残留在 Store：%s", f.AbsolutePath)
		}
	}
}
