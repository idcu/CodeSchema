package runtime

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/vector"
)

// writeProbeSource 写一个只含单个包级函数的 Go 源文件（检索/可视化用例 fixture）。
func writeProbeSource(t *testing.T, path, funcName string) {
	t.Helper()
	writeGoSource(t, path, "package fixture\n\nfunc "+funcName+"() int {\n\treturn 1\n}\n")
}

// storeHasMethodNamed 直接读主存储，判断是否存在指定名称的方法（不经过任何缓存）。
func storeHasMethodNamed(ctx context.Context, st store.Store, name string) bool {
	files, err := st.GetAllFiles(ctx)
	if err != nil {
		return false
	}
	for _, f := range files {
		classes, _ := st.GetClassesByFileID(ctx, f.ID)
		for _, c := range classes {
			methods, _ := st.GetMethodsByClassID(ctx, c.ID)
			for _, m := range methods {
				if m.Name == name {
					return true
				}
			}
		}
	}
	return false
}

// searchSnippetMatches 判断 /search（exact）结果里是否有摘要包含 substr。
//
// 摘要取自检索文档原文；即使符号行已从主存储删除，陈旧检索文档仍会带着旧摘要命中，
// 因此该断言能同时覆盖「新内容不可见」与「旧内容未消失」。
func searchSnippetMatches(ctx context.Context, svc *service.Service, query, substr string) bool {
	results, err := svc.Search(ctx, query, "exact", 50)
	if err != nil {
		return false
	}
	for _, r := range results {
		if strings.Contains(r.Snippet, substr) {
			return true
		}
	}
	return false
}

// searchFileForSnippet 返回摘要包含 substr 的检索结果对应的文件路径。
func searchFileForSnippet(ctx context.Context, svc *service.Service, query, substr string) string {
	results, err := svc.Search(ctx, query, "exact", 50)
	if err != nil {
		return ""
	}
	for _, r := range results {
		if strings.Contains(r.Snippet, substr) {
			return r.File
		}
	}
	return ""
}

// vecDocsContain 判断 /viz 使用的向量库文档原文中是否有包含 substr 的文档。
//
// 只枚举仍在向量库中的 ID：删除后的文档即使原文 map 仍有残留，也不会出现在这里。
func vecDocsContain(ctx context.Context, vs vector.VectorStore, substr string) bool {
	ids, err := vs.ListIDs(ctx)
	if err != nil {
		return false
	}
	cs, ok := vs.(vector.DocContentStore)
	if !ok {
		return false
	}
	for _, id := range ids {
		if content, ok := cs.Content(ctx, id); ok && strings.Contains(content, substr) {
			return true
		}
	}
	return false
}

// waitFor 轮询直到条件成立；超时带上诊断描述失败（便于先红证据定位）。
func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if cond() {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s（%s 内未满足）", desc, timeout)
		}
		time.Sleep(50 * time.Millisecond)
	}
}

// TestWatchIncrementalUpdateVisibleToSearchAndViz 对抗性用例（结构性缺陷：副本实例）。
//
// watcher 的增量索引必须落在查询侧同一个 Searcher/IndexBuilder 上，
// 否则 /search 与 /viz 永远读到构造时的快照：
//  1. 改文件：新符号可见、旧符号消失；
//  2. 删除文件：新符号也不再可见；
//  3. 改名：/viz 文档原文里的旧路径消失、新路径出现。
func TestWatchIncrementalUpdateVisibleToSearchAndViz(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	repoDir := t.TempDir()
	probePath := filepath.Join(repoDir, "probe.go")
	writeProbeSource(t, probePath, "AlphaProbe")

	cfg := noNetConfig(t, t.TempDir())
	cfg.Project.Root = repoDir
	cfg.Watcher.UseFsnotify = false // 默认 poll 路径
	cfg.Watcher.DebounceMs = 20

	st := openFileStore(t, cfg.Storage.DSN)
	defer st.Close()
	if err := ScanRepository(ctx, st, cfg, repoDir); err != nil {
		t.Fatalf("ScanRepository: %v", err)
	}
	run, err := BuildRuntime(ctx, st, cfg)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}

	alphaFQN := methodFQNByName(ctx, t, st, "AlphaProbe")
	if !searchSnippetMatches(ctx, run.Svc, alphaFQN, "AlphaProbe") {
		t.Fatalf("用例前提不成立：初始 /search 看不到 AlphaProbe")
	}
	if !vecDocsContain(ctx, run.VecStore, "AlphaProbe") {
		t.Fatalf("用例前提不成立：初始 /viz 文档看不到 AlphaProbe")
	}

	stop, err := run.StartWatchBackground(ctx, cfg, repoDir)
	if err != nil {
		t.Fatalf("StartWatchBackground: %v", err)
	}
	defer stop()

	// 等 poll watcher 完成首轮基线快照。
	time.Sleep(500 * time.Millisecond)

	// 1) 改文件：旧符号移除、新符号写入。
	writeProbeSource(t, probePath, "BetaProbe")
	waitFor(t, 20*time.Second, "主存储未出现 BetaProbe", func() bool {
		return storeHasMethodNamed(ctx, st, "BetaProbe")
	})
	betaFQN := methodFQNByName(ctx, t, st, "BetaProbe")

	waitFor(t, 10*time.Second, "/search 看不到增量新内容 BetaProbe（增量落在副本检索器上）", func() bool {
		return searchSnippetMatches(ctx, run.Svc, betaFQN, "BetaProbe")
	})
	waitFor(t, 10*time.Second, "/search 仍能命中已删除符号 AlphaProbe（旧检索文档残留）", func() bool {
		return !searchSnippetMatches(ctx, run.Svc, alphaFQN, "AlphaProbe")
	})
	waitFor(t, 10*time.Second, "/viz 文档看不到增量新内容 BetaProbe（增量落在副本向量库上）", func() bool {
		return vecDocsContain(ctx, run.VecStore, "BetaProbe")
	})
	waitFor(t, 10*time.Second, "/viz 文档仍含已删除符号 AlphaProbe（旧文档残留）", func() bool {
		return !vecDocsContain(ctx, run.VecStore, "AlphaProbe")
	})

	// 2) 删除文件：检索与可视化都不得再命中。
	if err := os.Remove(probePath); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "主存储未清理已删除文件", func() bool {
		return storeFileGone(ctx, st, probePath)
	})
	waitFor(t, 10*time.Second, "/search 仍能命中已删除文件中的 BetaProbe", func() bool {
		return !searchSnippetMatches(ctx, run.Svc, betaFQN, "BetaProbe")
	})
	waitFor(t, 10*time.Second, "/viz 仍保留已删除文件中的 BetaProbe", func() bool {
		return !vecDocsContain(ctx, run.VecStore, "BetaProbe")
	})

	// 3) 改名：旧路径文本必须从 /viz 文档消失，新路径出现。
	renameOld := filepath.Join(repoDir, "before_rename.go")
	renameNew := filepath.Join(repoDir, "after_rename.go")
	writeProbeSource(t, renameOld, "RenameProbe")
	waitFor(t, 20*time.Second, "主存储未出现 RenameProbe", func() bool {
		return storeHasMethodNamed(ctx, st, "RenameProbe")
	})
	renameFQN := methodFQNByName(ctx, t, st, "RenameProbe")
	waitFor(t, 10*time.Second, "/viz 文档看不到改名前的路径", func() bool {
		return vecDocsContain(ctx, run.VecStore, "before_rename.go")
	})

	if err := os.Rename(renameOld, renameNew); err != nil {
		t.Fatal(err)
	}
	waitFor(t, 20*time.Second, "改名后主存储旧路径未消失/新路径未出现", func() bool {
		return storeFileGone(ctx, st, renameOld) && !storeFileGone(ctx, st, renameNew)
	})
	waitFor(t, 10*time.Second, "/viz 文档仍残留改名前的旧路径", func() bool {
		return !vecDocsContain(ctx, run.VecStore, "before_rename.go")
	})
	waitFor(t, 10*time.Second, "/viz 文档未出现改名后的新路径", func() bool {
		return vecDocsContain(ctx, run.VecStore, "after_rename.go")
	})
	waitFor(t, 10*time.Second, "/search 结果未指向改名后的新路径", func() bool {
		return searchFileForSnippet(ctx, run.Svc, renameFQN, "RenameProbe") == renameNew
	})
}
