// 多语言真实仓库端到端验证（P9_4 未做项）。
//
// 对本地真实仓库（多语言混合）跑 scan→store→统计，验证 scanner 的 30 语言
// 正则适配器在真实仓库上的解析覆盖（不依赖外部网络/CI 无这些仓库）。
//
// 运行（本地）：
//   CODESCHEMA_MULTILANG_REPOS="/Volumes/Data/lytd;/Volumes/Data/deepseek-harness-master;/Volumes/Data/code-schema" \
//     go test -run TestMultiLangRealRepos -v -timeout 600s ./internal/integration/
package integration

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// TestMultiLangRealRepos 多语言真实仓库扫描覆盖验证。
// 仓库路径经 CODESCHEMA_MULTILANG_REPOS（分号分隔）指定；未配置时跳过（CI 无这些仓库）。
func TestMultiLangRealRepos(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping multi-lang real repo scan in short mode")
	}
	env := os.Getenv("CODESCHEMA_MULTILANG_REPOS")
	if env == "" {
		t.Skip("multi-lang real repos not configured; set CODESCHEMA_MULTILANG_REPOS=\"repo1;repo2\" to run")
	}

	ctx := context.Background()
	for _, repo := range strings.Split(env, ";") {
		repo = strings.TrimSpace(repo)
		if repo == "" {
			continue
		}
		t.Run(filepath.Base(repo), func(t *testing.T) {
			if _, err := os.Stat(repo); err != nil {
				t.Skipf("repo path not found: %s", repo)
				return
			}
			setup, cleanup := NewBenchSetup(t, repo)
			defer cleanup()

			if err := setup.Scanner.ScanAll(ctx, repo); err != nil {
				t.Fatalf("ScanAll(%s): %v", repo, err)
			}

			files, err := setup.Store.GetAllFiles(ctx)
			if err != nil {
				t.Fatalf("GetAllFiles: %v", err)
			}
			classes, methods := 0, 0
			for _, f := range files {
				cs, err := setup.Store.GetClassesByFileID(ctx, f.ID)
				if err != nil {
					t.Fatalf("GetClassesByFileID(%d): %v", f.ID, err)
				}
				classes += len(cs)
				for _, c := range cs {
					ms, err := setup.Store.GetMethodsByClassID(ctx, c.ID)
					if err != nil {
						t.Fatalf("GetMethodsByClassID(%d): %v", c.ID, err)
					}
					methods += len(ms)
				}
			}
			t.Logf("%s: scanned_files=%d classes=%d methods=%d", filepath.Base(repo), len(files), classes, methods)
			if len(files) == 0 {
				t.Errorf("%s: no files parsed (registry empty or adapter missing?)", repo)
			}
		})
	}
}
