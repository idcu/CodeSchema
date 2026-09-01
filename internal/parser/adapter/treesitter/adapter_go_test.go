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
//  2. 调用边应带 CallerFQN（调用方）且 callee 带包前缀，与 method FQN 对齐；
//  3. 函数级调用（NewWatcher）与方法级调用（w.ReloadNow）都被记录。
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

	// 期望（修复后）：泛型类型被识别为 class
	hasWatcherClass := false
	for _, c := range doc.Classes {
		if c.Name == "Watcher" {
			hasWatcherClass = true
		}
	}
	if !hasWatcherClass {
		t.Errorf("泛型类型 Watcher[T] 未被识别为 class；当前 classes=%v", doc.Classes)
	}

	// 期望：至少一条调用带非空 CallerFQN（调用方被记录）
	hasCaller := false
	for _, c := range doc.Calls {
		if c.CallerFQN != "" {
			hasCaller = true
		}
	}
	if !hasCaller {
		t.Errorf("调用边 CallerFQN 全为空；当前 calls=%v", doc.Calls)
	}

	// 期望：callee 带包前缀 config.（与 method FQN 对齐）
	hasQualifiedCallee := false
	for _, c := range doc.Calls {
		if c.CalleeFQN == "config.ReloadNow" || c.CalleeFQN == "config.loadConfig" {
			hasQualifiedCallee = true
		}
	}
	if !hasQualifiedCallee {
		t.Errorf("callee 未带包前缀 config.；当前 calls=%v", doc.Calls)
	}

	var _ *parser.IRDocument = doc
}
