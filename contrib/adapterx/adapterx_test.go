package adapterx

import (
	"context"
	"testing"
)

// stubPlugin 测试用最小 ParserPlugin 实现。
type stubPlugin struct {
	name string
}

func (s *stubPlugin) Name() string                                  { return s.name }
func (s *stubPlugin) Supports(lang string) bool                     { return lang == "go" }
func (s *stubPlugin) Init(ctx context.Context, c map[string]any) error { return nil }
func (s *stubPlugin) Close() error                                  { return nil }
func (s *stubPlugin) Parse(ctx context.Context, path string) (*IRDocument, error) {
	return &IRDocument{Source: s.name, FilePath: path}, nil
}

func TestRegistry_RegisterAndGet(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubPlugin{name: "a"}); err != nil {
		t.Fatalf("register a: %v", err)
	}
	if err := r.Register(&stubPlugin{name: "b"}); err != nil {
		t.Fatalf("register b: %v", err)
	}
	if got := r.Names(); len(got) != 2 || got[0] != "a" || got[1] != "b" {
		t.Fatalf("names = %v, want [a b]", got)
	}
	if r.Get("a") == nil || r.Get("missing") != nil {
		t.Error("Get(a) should be non-nil, Get(missing) should be nil")
	}
}

func TestRegistry_DuplicateRejected(t *testing.T) {
	r := NewRegistry()
	if err := r.Register(&stubPlugin{name: "dup"}); err != nil {
		t.Fatalf("first register: %v", err)
	}
	if err := r.Register(&stubPlugin{name: "dup"}); err == nil {
		t.Fatal("duplicate register should error")
	}
	if err := r.Register(nil); err == nil {
		t.Fatal("register nil should error")
	}
}

func TestBuiltinAdapters_List(t *testing.T) {
	adapters := BuiltinAdapters()
	if len(adapters) != 4 {
		t.Fatalf("builtin adapters = %d, want 4", len(adapters))
	}
	seen := map[string]bool{}
	for _, a := range adapters {
		if a.Name == "" || a.Kind == "" || a.Description == "" {
			t.Errorf("adapter %q has empty metadata fields", a.Name)
		}
		if seen[a.Name] {
			t.Errorf("duplicate builtin adapter name %q", a.Name)
		}
		seen[a.Name] = true
	}
}

func TestStubParse_Bridge(t *testing.T) {
	// 验证契约可被任何实现满足并产出 IRDocument。
	p := &stubPlugin{name: "treesitter"}
	doc, err := p.Parse(context.Background(), "/repo/a.go")
	if err != nil {
		t.Fatalf("parse: %v", err)
	}
	if doc.Source != "treesitter" || doc.FilePath != "/repo/a.go" {
		t.Errorf("doc = %+v, want source=treesitter path=/repo/a.go", doc)
	}
}
