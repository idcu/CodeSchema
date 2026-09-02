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
//  2. 调用边 CallerFQN 应带包前缀限定（FQN 命名空间对齐，使影响面分析可查）：
//     - NewWatcher（顶层函数）体内的 w.ReloadNow() → caller=config.NewWatcher；
//       （w 是局部变量而非 receiver，类型不可消歧，callee 保持 w.ReloadNow）
//     - ReloadNow（Watcher 方法，receiver w）体内的 loadConfig() →
//       caller=config.Watcher.ReloadNow、callee=config.loadConfig（包级函数包限定）；
//  3. 自接收者调用（recv == 当前方法 receiver 变量）会被限定为 pkg.RecvType.Method；
//     其余 receiver.Method（如局部变量 w.ReloadNow）保持原样（类型推导范畴）；
//  4. 顶层函数 NewWatcher 的 ClassFQN 为空（不再被误挂到最近 class）。
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

	// 2. 调用归属精确断言（包限定 FQN）：
	//    config.NewWatcher -> w.ReloadNow（w 为局部变量，callee 不可消歧，保持 w.ReloadNow）；
	//    config.Watcher.ReloadNow -> config.loadConfig（包级函数包限定）。
	hasNewWatcherCall := false
	hasReloadNowCall := false
	for _, c := range doc.Calls {
		if c.CallerFQN == "config.NewWatcher" && c.CalleeFQN == "w.ReloadNow" {
			hasNewWatcherCall = true
		}
		if c.CallerFQN == "config.Watcher.ReloadNow" && c.CalleeFQN == "config.loadConfig" {
			hasReloadNowCall = true
		}
	}
	if !hasNewWatcherCall {
		t.Errorf("期望调用 config.NewWatcher -> w.ReloadNow；当前 calls=%v", doc.Calls)
	}
	if !hasReloadNowCall {
		t.Errorf("期望调用 config.Watcher.ReloadNow -> config.loadConfig；当前 calls=%v", doc.Calls)
	}

	// 3. 顶层函数不再被误归属到 class（ClassFQN 为空）
	for _, m := range doc.Methods {
		if m.Name == "NewWatcher" && m.ClassFQN != "" {
			t.Errorf("顶层函数 NewWatcher 不应挂 ClassFQN=%q（当前 methods=%v）", m.ClassFQN, doc.Methods)
		}
	}

	var _ *parser.IRDocument = doc
}
