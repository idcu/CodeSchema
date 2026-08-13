package parser

import (
	"context"
	"testing"
)

// mockAdapter 实现 ParserPlugin 接口的测试适配器。
type mockAdapter struct {
	name     string
	supports map[string]bool
}

func (m *mockAdapter) Name() string                               { return m.name }
func (m *mockAdapter) Supports(lang string) bool                   { return m.supports[lang] }
func (m *mockAdapter) Init(ctx context.Context, config map[string]any) error { return nil }
func (m *mockAdapter) Close() error                               { return nil }
func (m *mockAdapter) Parse(ctx context.Context, path string) (*IRDocument, error) {
	return &IRDocument{Source: m.name}, nil
}

func TestRegistry_RegisterAndSelect(t *testing.T) {
	r := NewRegistry()

	adapterGo := &mockAdapter{
		name: "gopls",
		supports: map[string]bool{"go": true},
	}
	adapterJava := &mockAdapter{
		name: "jdtls",
		supports: map[string]bool{"java": true},
	}
	adapterGeneric := &mockAdapter{
		name: "treesitter",
		supports: map[string]bool{"go": true, "java": true, "py": true},
	}

	r.Register(adapterGo)
	r.Register(adapterJava)
	r.Register(adapterGeneric)

	// 测试：无优先级设置时，按注册顺序选择
	p, err := r.Select("go")
	if err != nil {
		t.Fatalf("Select go: %v", err)
	}
	if p.Name() != "gopls" {
		t.Errorf("expected gopls, got %s", p.Name())
	}

	p, err = r.Select("java")
	if err != nil {
		t.Fatalf("Select java: %v", err)
	}
	if p.Name() != "jdtls" {
		t.Errorf("expected jdtls, got %s", p.Name())
	}

	// 测试：不存在的语言
	_, err = r.Select("rust")
	if err == nil {
		t.Fatal("expected error for unsupported language")
	}
}

func TestRegistry_SetPriority(t *testing.T) {
	r := NewRegistry()

	ts := &mockAdapter{name: "treesitter", supports: map[string]bool{"go": true, "java": true}}
	exact := &mockAdapter{name: "gopls", supports: map[string]bool{"go": true}}

	r.Register(ts)
	r.Register(exact)

	// 默认按注册顺序，treesitter 优先
	p, _ := r.Select("go")
	if p.Name() != "treesitter" {
		t.Errorf("expected treesitter, got %s", p.Name())
	}

	// 设置优先级后，gopls 优先
	r.SetPriority("go", []string{"gopls", "treesitter"})
	p, _ = r.Select("go")
	if p.Name() != "gopls" {
		t.Errorf("expected gopls, got %s", p.Name())
	}
}

func TestRegistry_Degradation(t *testing.T) {
	r := NewRegistry()

	primary := &mockAdapter{name: "scip-java", supports: map[string]bool{"java": true}}
	fallback := &mockAdapter{name: "treesitter", supports: map[string]bool{"java": true}}

	r.Register(primary)
	r.Register(fallback)

	// 设置优先级：首选 scip-java
	r.SetPriority("java", []string{"scip-java", "treesitter"})

	p, err := r.Select("java")
	if err != nil {
		t.Fatalf("Select java: %v", err)
	}
	if p.Name() != "scip-java" {
		t.Errorf("expected scip-java, got %s", p.Name())
	}

	// 模拟降级：移除首选适配器
	r2 := NewRegistry()
	r2.Register(fallback)
	p, err = r2.Select("java")
	if err != nil {
		t.Fatalf("Select java: %v", err)
	}
	if p.Name() != "treesitter" {
		t.Errorf("expected treesitter, got %s", p.Name())
	}
}

func TestBatchParser(t *testing.T) {
	r := NewRegistry()

	batch := &mockBatchParser{
		name: "scip",
		supports: map[string]bool{"go": true},
	}
	r.Register(batch)

	p, err := r.Select("go")
	if err != nil {
		t.Fatalf("Select go: %v", err)
	}

	bp, ok := p.(BatchParser)
	if !ok {
		t.Fatal("expected BatchParser interface")
	}

	ctx := context.Background()
	ch, err := bp.ParseAll(ctx, []string{"file1.go", "file2.go"})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}

	count := 0
	for doc := range ch {
		_ = doc
		count++
	}
	if count != 2 {
		t.Errorf("expected 2 docs, got %d", count)
	}
}

// mockBatchParser 实现 BatchParser 接口的测试适配器。
type mockBatchParser struct {
	name     string
	supports map[string]bool
}

func (m *mockBatchParser) Name() string                               { return m.name }
func (m *mockBatchParser) Supports(lang string) bool                   { return m.supports[lang] }
func (m *mockBatchParser) Init(ctx context.Context, config map[string]any) error { return nil }
func (m *mockBatchParser) Close() error                               { return nil }
func (m *mockBatchParser) Parse(ctx context.Context, path string) (*IRDocument, error) {
	return &IRDocument{Source: m.name, FilePath: path}, nil
}
func (m *mockBatchParser) ParseAll(ctx context.Context, paths []string) (<-chan *IRDocument, error) {
	ch := make(chan *IRDocument)
	go func() {
		defer close(ch)
		for _, path := range paths {
			select {
			case <-ctx.Done():
				return
			case ch <- &IRDocument{Source: m.name, FilePath: path}:
			}
		}
	}()
	return ch, nil
}