package scip

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestSCIPAdapter_RealScipTypeScript 用真实 scip-typescript 工具链生成 index 验证端到端：
// 最小 TS 工程 → scip-typescript index → SCIPAdapter 解析 → 断言类/方法/调用。
//
// 工具缺失时 SKIP（CI 绿）；镜像 cs-lsp-test:scip 内含 scip-typescript@0.4.0。
func TestSCIPAdapter_RealScipTypeScript(t *testing.T) {
	scipTS, err := exec.LookPath("scip-typescript")
	if err != nil {
		t.Skip("scip-typescript not installed; skipping real-index verification")
	}

	proj := t.TempDir()
	srcDir := filepath.Join(proj, "src")
	if err := os.MkdirAll(srcDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(proj, "tsconfig.json"), []byte(`{
  "compilerOptions": {"target": "ES2020", "module": "commonjs", "strict": true},
  "include": ["src"]
}`), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(srcDir, "calculator.ts"), []byte(`export class Calculator {
  add(a: number, b: number): number {
    return a + b;
  }
  double(a: number): number {
    return this.add(a, a);
  }
}
`), 0644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(scipTS, "index", "--output", "index.scip")
	cmd.Dir = proj
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("scip-typescript index: %v: %s", err, out)
	}
	indexPath := filepath.Join(proj, "index.scip")
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("index.scip not generated at %s", indexPath)
	}

	a := NewSCIPAdapter(proj)
	if err := a.Init(context.Background(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer a.Close()

	ch, err := a.ParseAll(context.Background(), []string{filepath.Join(proj, "src/calculator.ts")})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	ir := <-ch
	if ir == nil {
		t.Fatal("expected non-nil IR for indexed file")
	}
	if ir.Source != "scip" {
		t.Errorf("source = %q, want scip", ir.Source)
	}
	if len(ir.Classes) < 1 {
		t.Errorf("classes = %d, want >=1 (Calculator)", len(ir.Classes))
	}
	if len(ir.Methods) < 2 {
		t.Errorf("methods = %d, want >=2 (add/double)", len(ir.Methods))
	}
	// 真实 scip-typescript 产物应至少覆盖 add/double 两个方法名
	found := map[string]bool{}
	for _, m := range ir.Methods {
		found[m.Name] = true
	}
	if !found["add"] || !found["double"] {
		t.Errorf("methods missing: add=%v double=%v (got %v)", found["add"], found["double"], found)
	}
	// double() 内 this.add(...) 是真实调用。历史缺陷：occurrences 解码字段
	// 错位 + 引用角色误判（2=Import 而非引用），导致真实产物 calls=0——
	// 断言 calls>=1 且存在 callee 含 add 的调用边，杜绝缺陷回归。
	if len(ir.Calls) < 1 {
		t.Errorf("calls = %d, want >=1 (double() calls this.add())", len(ir.Calls))
	}
	calleeOK := false
	for _, c := range ir.Calls {
		if strings.Contains(c.CalleeFQN, "add") && c.CallerFQN != "" {
			calleeOK = true
		}
	}
	if !calleeOK {
		t.Errorf("expected a call double→add (caller/callee FQN non-empty), got %v", ir.Calls)
	}
	t.Logf("real scip-typescript index: classes=%d methods=%d calls=%d", len(ir.Classes), len(ir.Methods), len(ir.Calls))
}
