package codegraph

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// TestCodeGraphAdapter_RealCodegraph 用真实 @optave/codegraph 工具链生成 SQLite 图谱
// 验证端到端：最小 Go 工程 → codegraph build → .codegraph/graph.db → 适配器解析 → 断言。
//
// 工具缺失时 SKIP（CI 绿）；镜像 cs-lsp-test:scip 内含 @optave/codegraph。
func TestCodeGraphAdapter_RealCodegraph(t *testing.T) {
	cg, err := exec.LookPath("codegraph")
	if err != nil {
		t.Skip("codegraph not installed; skipping real-db verification")
	}

	proj := t.TempDir()
	if err := os.WriteFile(filepath.Join(proj, "go.mod"), []byte("module demo\n\ngo 1.22\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "service.go"), []byte(`package demo

type Service struct{}

func (s *Service) Run() string {
	return s.helper()
}

func (s *Service) helper() string {
	return "ok"
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(cg, "build")
	cmd.Dir = proj
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("codegraph build: %v: %s", err, out)
	}

	// 产物路径探测：.codegraph/graph.db 或 .codegraph/codegraph.db
	dbPath := ""
	for _, name := range []string{"graph.db", "codegraph.db"} {
		cand := filepath.Join(proj, ".codegraph", name)
		if _, err := os.Stat(cand); err == nil {
			dbPath = cand
			break
		}
	}
	if dbPath == "" {
		entries, _ := os.ReadDir(filepath.Join(proj, ".codegraph"))
		t.Fatalf("codegraph db not found in .codegraph: %v", entries)
	}

	a := NewCodeGraphAdapter(dbPath)
	if err := a.Init(context.Background(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer a.Close()

	ch, err := a.ParseAll(context.Background(), []string{filepath.Join(proj, "service.go")})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	ir := <-ch
	if ir == nil {
		t.Fatal("expected non-nil IR for indexed file")
	}
	if len(ir.Classes) < 1 {
		t.Errorf("classes = %d, want >=1 (Service)", len(ir.Classes))
	}
	if len(ir.Methods) < 2 {
		t.Errorf("methods = %d, want >=2 (Run/helper)", len(ir.Methods))
	}
	found := map[string]bool{}
	for _, m := range ir.Methods {
		found[m.Name] = true
	}
	if !found["Run"] || !found["helper"] {
		t.Errorf("methods missing: Run=%v helper=%v (got %v)", found["Run"], found["helper"], found)
	}
	t.Logf("real codegraph db: classes=%d methods=%d calls=%d (db=%s)", len(ir.Classes), len(ir.Methods), len(ir.Calls), filepath.Base(dbPath))
}
