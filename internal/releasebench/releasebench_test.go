package releasebench

import (
	"context"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func writeFile(t *testing.T, root, rel, content string) {
	t.Helper()
	path := filepath.Join(root, rel)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

// newFixtureRepo 生成单语言可复现夹具：生产类 Calculator 的方法 Add 调用 helper，
// 测试类 CalculatorTest 的方法 testAdd 调用 Add。
func newFixtureRepo(t *testing.T, lang string) string {
	t.Helper()
	dir := t.TempDir()
	switch lang {
	case "go":
		writeFile(t, dir, "calc/calc.go", `package calc

type Calculator struct{}

func (c *Calculator) Add(a, b int) int { return c.helper(a) }

func (c *Calculator) helper(a int) int { return a }
`)
		writeFile(t, dir, "calc/calc_test.go", `package calc

import "testing"

type CalculatorTest struct{}

func (s *CalculatorTest) TestAdd(t *testing.T) {
	c := &Calculator{}
	_ = c.Add(1, 2)
}
`)
	case "java":
		writeFile(t, dir, "src/main/java/demo/Calculator.java", `package demo;

public class Calculator {
    public int add(int a, int b) {
        return helper(a);
    }

    private int helper(int a) {
        return a;
    }
}
`)
		writeFile(t, dir, "src/test/java/demo/CalculatorTest.java", `package demo;

public class CalculatorTest {
    public void testAdd() {
        new Calculator().add(1, 2);
    }
}
`)
	case "ts":
		writeFile(t, dir, "src/calculator.ts", `export class Calculator {
  add(a: number, b: number): number { return this.helper(a); }
  private helper(a: number): number { return a; }
}
`)
		writeFile(t, dir, "src/calculator.test.ts", `import { Calculator } from "./calculator";

export class CalculatorTest {
  testAdd(): void {
    const c = new Calculator();
    c.add(1, 2);
  }
}
`)
	}
	return dir
}

// TestRun_MultiLanguageFixtures 验证三种语言夹具都能产出真值 pair、非零 TP 与调用图指标。
func TestRun_MultiLanguageFixtures(t *testing.T) {
	ctx := context.Background()
	for _, lang := range []string{"go", "java", "ts"} {
		t.Run(lang, func(t *testing.T) {
			repo := newFixtureRepo(t, lang)
			report, err := Run(ctx, repo, Options{Language: lang, MaxPairs: 200, Workers: 2})
			if err != nil {
				t.Fatalf("Run: %v", err)
			}
			if report.GroundTruthPairs < 1 {
				t.Fatalf("ground truth pairs = %d, want >= 1", report.GroundTruthPairs)
			}
			if report.TestLink.TP < 1 {
				t.Fatalf("TP = %d, want >= 1 (report=%+v)", report.TestLink.TP, report.TestLink)
			}
			if report.TestLink.Precision > 1 || report.TestLink.Recall > 1 {
				t.Fatalf("invalid PRF: %+v", report.TestLink)
			}
			if report.CallGraph.Calls < 1 {
				t.Fatalf("call edges = %d, want >= 1", report.CallGraph.Calls)
			}
			if report.CallGraph.ResolutionRate <= 0 {
				t.Fatalf("resolution rate = %f, want > 0", report.CallGraph.ResolutionRate)
			}

			// 可复现：同输入二次运行核心指标一致（Timestamp 除外）。
			again, err := Run(ctx, repo, Options{Language: lang, MaxPairs: 200, Workers: 2})
			if err != nil {
				t.Fatalf("Run again: %v", err)
			}
			again.Timestamp = report.Timestamp
			if !reflect.DeepEqual(report, again) {
				t.Fatalf("non-deterministic report:\nfirst=%+v\nsecond=%+v", report, again)
			}
		})
	}
}

// TestAffectedNegativeControl_DeleteTest 负控：删除测试文件后 GetAffected 不再返回该测试。
func TestAffectedNegativeControl_DeleteTest(t *testing.T) {
	ctx := context.Background()
	repo := newFixtureRepo(t, "go")

	svc, cleanup, err := newService(ctx, repo, 2)
	if err != nil {
		t.Fatalf("newService: %v", err)
	}
	got, err := svc.GetAffected(ctx, "Calculator.Add", false)
	cleanup()
	if err != nil {
		t.Fatalf("GetAffected: %v", err)
	}
	if !contains(got, "CalculatorTest.TestAdd") {
		t.Fatalf("before deletion affected = %v, want CalculatorTest.TestAdd", got)
	}

	// 复制夹具时剔除测试文件，模拟「测试被删除」。
	withoutTest := t.TempDir()
	if err := copyTree(repo, withoutTest, func(path string) bool {
		return filepath.Base(path) == "calc_test.go"
	}); err != nil {
		t.Fatal(err)
	}
	svc2, cleanup2, err := newService(ctx, withoutTest, 2)
	if err != nil {
		t.Fatalf("newService(after): %v", err)
	}
	defer cleanup2()
	after, err := svc2.GetAffected(ctx, "Calculator.Add", false)
	if err != nil {
		t.Fatalf("GetAffected(after): %v", err)
	}
	if contains(after, "CalculatorTest.TestAdd") {
		t.Fatalf("after deletion affected still contains deleted test: %v", after)
	}
}

// TestSamplePairs_Deterministic 验证采样等距且不超上限。
func TestSamplePairs_Deterministic(t *testing.T) {
	pairs := make([]Pair, 0, 1000)
	for i := 0; i < 1000; i++ {
		pairs = append(pairs, Pair{Production: "A", Test: string(rune('a' + i%26))})
	}
	got, truncated := samplePairs(pairs, 200)
	if !truncated {
		t.Fatal("expected truncated=true")
	}
	if len(got) > 200 {
		t.Fatalf("sampled %d > 200", len(got))
	}
	again, _ := samplePairs(pairs, 200)
	if !reflect.DeepEqual(got, again) {
		t.Fatal("sampling not deterministic")
	}
}

func contains(items []string, want string) bool {
	for _, s := range items {
		if s == want {
			return true
		}
	}
	return false
}

func copyTree(src, dst string, skip func(string) bool) error {
	return filepath.WalkDir(src, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		rel, err := filepath.Rel(src, path)
		if err != nil {
			return err
		}
		if rel == "." {
			return nil
		}
		if skip != nil && skip(path) {
			return nil
		}
		target := filepath.Join(dst, rel)
		if d.IsDir() {
			return os.MkdirAll(target, 0o755)
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		return os.WriteFile(target, data, 0o644)
	})
}
