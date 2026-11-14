package runtime

import (
	"context"
	"strings"
	"sync"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// seedTenantIR 写入一个租户的最小生产类 + 测试类 + 一条调用边。
func seedTenantIR(t *testing.T, ctx context.Context, st store.Store, pkg, dir string) {
	t.Helper()
	prod := &parser.IRDocument{
		Source: "test", Language: "go", FilePath: dir + "/service.go", FileHash: "prod-" + pkg,
		Classes: []parser.ClassIR{{Name: "Service", FullName: pkg + ".Service", Type: "CLASS", StartLine: 1, EndLine: 1}},
		Methods: []parser.MethodIR{{Name: "Run", ClassFQN: pkg + ".Service", StartLine: 3, EndLine: 5}},
		Calls:   []parser.CallIR{{CallerFQN: pkg + ".Service.Run", CalleeFQN: pkg + ".Helper", CallType: "direct"}},
	}
	test := &parser.IRDocument{
		Source: "test", Language: "go", FilePath: dir + "/service_test.go", FileHash: "test-" + pkg,
		Classes: []parser.ClassIR{{Name: "ServiceTest", FullName: pkg + ".ServiceTest", Type: "CLASS", StartLine: 1, EndLine: 1}},
		Methods: []parser.MethodIR{{Name: "TestRun", ClassFQN: pkg + ".ServiceTest", StartLine: 3, EndLine: 5}},
	}
	for _, ir := range []*parser.IRDocument{prod, test} {
		if err := st.UpsertIR(ctx, ir); err != nil {
			t.Fatalf("UpsertIR(%s): %v", ir.FilePath, err)
		}
	}
}

// TestRuntimeCachesIsolatedAcrossTenants 对抗性用例（场景 3）：
// 两个租户各自 Runtime（独立 store / Analyzer / Service）并发重索引时，
// 任一侧的失效与数据都不得跨到另一侧；未被重索引的一侧缓存应保持复用。
func TestRuntimeCachesIsolatedAcrossTenants(t *testing.T) {
	ctx := context.Background()
	dirA, dirB := t.TempDir(), t.TempDir()

	stA := openFileStore(t, dirA)
	defer stA.Close()
	stB := openFileStore(t, dirB)
	defer stB.Close()

	seedTenantIR(t, ctx, stA, "pkga", "/repo-a")
	seedTenantIR(t, ctx, stB, "pkgb", "/repo-b")

	cfgA, cfgB := noNetConfig(t, dirA), noNetConfig(t, dirB)
	runA, err := BuildRuntime(ctx, stA, cfgA)
	if err != nil {
		t.Fatalf("BuildRuntime(A): %v", err)
	}
	runB, err := BuildRuntime(ctx, stB, cfgB)
	if err != nil {
		t.Fatalf("BuildRuntime(B): %v", err)
	}

	linksB, err := runB.Svc.FindTestLinks(ctx, "pkgb.Service.Run", 60)
	if err != nil {
		t.Fatalf("FindTestLinks(B): %v", err)
	}
	if len(linksB) == 0 {
		t.Fatal("租户 B 基线关联为空，用例前提不成立")
	}
	for _, l := range linksB {
		if !strings.HasPrefix(l.TestMethod, "pkgb.") {
			t.Fatalf("租户 B 关联出现外部数据: %+v", l)
		}
	}

	cgB, err := runB.Analyzer.BuildCallGraph(ctx)
	if err != nil {
		t.Fatalf("BuildCallGraph(B): %v", err)
	}
	for _, n := range cgB.Nodes {
		if !strings.HasPrefix(n.MethodFQN, "pkgb.") {
			t.Fatalf("租户 B 调用图出现外部节点: %s", n.MethodFQN)
		}
	}

	var wg sync.WaitGroup
	errCh := make(chan error, 1)
	wg.Add(2)
	// A：并发重索引 + 自查询（BuildIndex 会失效 A 自己的派生缓存）。
	go func() {
		defer wg.Done()
		for i := 0; i < 6; i++ {
			if _, err := runA.Svc.BuildIndex(ctx); err != nil {
				errCh <- err
				return
			}
			if _, err := runA.Analyzer.BuildCallGraph(ctx); err != nil {
				errCh <- err
				return
			}
		}
	}()
	// B：持续查询，期间不得被 A 的失效波及。
	go func() {
		defer wg.Done()
		for i := 0; i < 60; i++ {
			got, err := runB.Svc.FindTestLinks(ctx, "pkgb.Service.Run", 60)
			if err != nil {
				errCh <- err
				return
			}
			if len(got) != len(linksB) {
				errCh <- errStr("租户 B 关联条数被跨租户重索引影响")
				return
			}
			cg, err := runB.Analyzer.BuildCallGraph(ctx)
			if err != nil {
				errCh <- err
				return
			}
			if cg != cgB {
				errCh <- errStr("租户 B 调用图缓存被租户 A 的重索引失效")
				return
			}
		}
	}()
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	// A 侧仍只含自己的数据。
	cgA, err := runA.Analyzer.BuildCallGraph(ctx)
	if err != nil {
		t.Fatalf("BuildCallGraph(A): %v", err)
	}
	for _, n := range cgA.Nodes {
		if strings.HasPrefix(n.MethodFQN, "pkgb.") {
			t.Fatalf("租户 A 调用图串入租户 B 数据: %s", n.MethodFQN)
		}
	}
}

type errStr string

func (e errStr) Error() string { return string(e) }
