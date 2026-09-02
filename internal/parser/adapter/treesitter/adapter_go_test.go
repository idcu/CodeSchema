package treesitter

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
)

// TestGoCallAndTypeExtraction 实证 + 回归：验证 Go 适配器
//  1. 泛型类型 type X[T any] struct 应被识别为 class；
//  2. 调用边应带 CallerFQN，且归属正确：
//     - NewWatcher（顶层函数）体内的 w.ReloadNow() → caller=NewWatcher；
//     - ReloadNow（Watcher 方法）体内的 loadConfig() → caller=Watcher.ReloadNow；
//  3. callee 保持仓库既定形式：receiver 调用为 w.ReloadNow、包级函数为裸名 loadConfig
//     （与 Java paymentService.pay / realCall 断言一致；带包前缀的解析属 CGO AST 版
//     或类型推导范畴，超出正则启发式边界）；
//  4. 顶层函数 NewWatcher 的 ClassFQN 为空（不再被误挂到最近 class）。
//
// 先以当前实现跑出真实产出（观察），再据预期实现修复。
func TestGoCallAndTypeExtraction(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "watcher.go")
	src := `package config

type Watcher[T any] struct {
	path string
}

func NewWatcher(path string) *Watcher[T] {
	w := &Watcher[T]{path: path}
	w.ReloadNow()
	return w
}

func (w *Watcher[T]) ReloadNow() {
	loadConfig(w.path)
}
`
	if err := os.WriteFile(p, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}

	a := NewTreeSitterAdapter()
	doc, err := a.Parse(context.Background(), p)
	if err != nil {
		t.Fatalf("parse: %v", err)
	}

	t.Logf("CLASSES:")
	for _, c := range doc.Classes {
		t.Logf("  name=%q full_name=%q type=%q", c.Name, c.FullName, c.Type)
	}
	t.Logf("METHODS:")
	for _, m := range doc.Methods {
		t.Logf("  name=%q class_fqn=%q sig=%q", m.Name, m.ClassFQN, m.Signature)
	}
	t.Logf("CALLS:")
	for _, c := range doc.Calls {
		t.Logf("  caller=%q callee=%q type=%q line=%d", c.CallerFQN, c.CalleeFQN, c.CallType, c.LineNumber)
	}

	// 1. 泛型类型被识别为 class
	hasWatcherClass := false
	for _, c := range doc.Classes {
		if c.Name == "Watcher" {
			hasWatcherClass = true
		}
	}
	if !hasWatcherClass {
		t.Errorf("泛型类型 Watcher[T] 未被识别为 class；当前 classes=%v", doc.Classes)
	}

	// 2. 调用归属精确断言：NewWatcher → w.ReloadNow；Watcher.ReloadNow → loadConfig
	hasNewWatcherCall := false
	hasReloadNowCall := false
	for _, c := range doc.Calls {
		if c.CallerFQN == "NewWatcher" && c.CalleeFQN == "w.ReloadNow" {
			hasNewWatcherCall = true
		}
		if c.CallerFQN == "Watcher.ReloadNow" && c.CalleeFQN == "loadConfig" {
			hasReloadNowCall = true
		}
	}
	if !hasNewWatcherCall {
		t.Errorf("期望调用 NewWatcher -> w.ReloadNow；当前 calls=%v", doc.Calls)
	}
	if !hasReloadNowCall {
		t.Errorf("期望调用 Watcher.ReloadNow -> loadConfig；当前 calls=%v", doc.Calls)
	}

	// 3. 顶层函数不再被误归属到 class（ClassFQN 为空）
	for _, m := range doc.Methods {
		if m.Name == "NewWatcher" && m.ClassFQN != "" {
			t.Errorf("顶层函数 NewWatcher 不应挂 ClassFQN=%q（当前 methods=%v）", m.ClassFQN, doc.Methods)
		}
	}

	var _ *parser.IRDocument = doc
}
