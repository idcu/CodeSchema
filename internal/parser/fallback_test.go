package parser

import (
	"context"
	"errors"
	"testing"
)

// mockPlugin 实现 ParserPlugin 的测试桩。
type mockPlugin struct {
	name     string
	supports []string
	parseErr error
	initErr  error
	parsed   []string
}

func (m *mockPlugin) Name() string { return m.name }
func (m *mockPlugin) Supports(lang string) bool {
	for _, l := range m.supports {
		if l == lang {
			return true
		}
	}
	return false
}
func (m *mockPlugin) Init(ctx context.Context, cfg map[string]any) error { return m.initErr }
func (m *mockPlugin) Close() error                                        { return nil }
func (m *mockPlugin) Parse(ctx context.Context, path string) (*IRDocument, error) {
	m.parsed = append(m.parsed, path)
	if m.parseErr != nil {
		return nil, m.parseErr
	}
	return &IRDocument{Source: m.name, FilePath: path}, nil
}

func TestFallbackParser_Name(t *testing.T) {
	fp := NewFallbackParser(&mockPlugin{name: "gopls"}, &mockPlugin{name: "treesitter"})
	if fp.Name() != "gopls" {
		t.Fatalf("Name() = %s, want gopls (与优先级映射一致)", fp.Name())
	}
}

func TestFallbackParser_Supports(t *testing.T) {
	primary := &mockPlugin{name: "gopls", supports: []string{"go"}}
	fp := NewFallbackParser(primary, &mockPlugin{name: "treesitter", supports: []string{"go", "java"}})
	if !fp.Supports("go") {
		t.Fatal("should support go")
	}
	if fp.Supports("java") {
		t.Fatal("should not support java (委托给主适配器)")
	}
}

// TestFallbackParser_Parse_PrimaryOK 主适配器成功时直接返回，不回退。
func TestFallbackParser_Parse_PrimaryOK(t *testing.T) {
	primary := &mockPlugin{name: "gopls", supports: []string{"go"}}
	fallback := &mockPlugin{name: "treesitter", supports: []string{"go"}}
	fp := NewFallbackParser(primary, fallback)

	ir, err := fp.Parse(context.Background(), "a.go")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ir.Source != "gopls" {
		t.Fatalf("Source = %s, want gopls", ir.Source)
	}
	if len(fallback.parsed) != 0 {
		t.Fatal("fallback should not be invoked when primary succeeds")
	}
}

// TestFallbackParser_Parse_PrimaryFails 主适配器失败时自动回退兜底。
func TestFallbackParser_Parse_PrimaryFails(t *testing.T) {
	primary := &mockPlugin{name: "gopls", supports: []string{"go"}, parseErr: errors.New("LSP timeout")}
	fallback := &mockPlugin{name: "treesitter", supports: []string{"go"}}
	fp := NewFallbackParser(primary, fallback)

	ir, err := fp.Parse(context.Background(), "a.go")
	if err != nil {
		t.Fatalf("Parse: %v", err)
	}
	if ir.Source != "treesitter" {
		t.Fatalf("Source = %s, want treesitter (fallback)", ir.Source)
	}
	if len(primary.parsed) != 1 || len(fallback.parsed) != 1 {
		t.Fatalf("primary parsed %d, fallback parsed %d; want 1 and 1",
			len(primary.parsed), len(fallback.parsed))
	}
}

// TestFallbackParser_Parse_BothFail 主与兜底都失败时返回兜底错误。
func TestFallbackParser_Parse_BothFail(t *testing.T) {
	primary := &mockPlugin{name: "gopls", parseErr: errors.New("LSP dead")}
	fallback := &mockPlugin{name: "treesitter", parseErr: errors.New("regex broken")}
	fp := NewFallbackParser(primary, fallback)

	_, err := fp.Parse(context.Background(), "a.go")
	if err == nil {
		t.Fatal("expected error when both fail")
	}
}

// TestFallbackParser_Init_PrimaryFails 主适配器初始化失败时降级到兜底（不视为错误）。
func TestFallbackParser_Init_PrimaryFails(t *testing.T) {
	primary := &mockPlugin{name: "clangd", initErr: errors.New("no compile_commands")}
	fallback := &mockPlugin{name: "treesitter"}
	fp := NewFallbackParser(primary, fallback)

	if err := fp.Init(context.Background(), nil); err != nil {
		t.Fatalf("Init should succeed via fallback, got: %v", err)
	}
	// 初始化失败后 primary 已被替换为 fallback，Parse 走兜底
	ir, err := fp.Parse(context.Background(), "a.c")
	if err != nil {
		t.Fatalf("Parse after degraded init: %v", err)
	}
	if ir.Source != "treesitter" {
		t.Fatalf("Source = %s, want treesitter", ir.Source)
	}
}

// TestFallbackParser_Init_BothFail 主与兜底都初始化失败时返回错误。
func TestFallbackParser_Init_BothFail(t *testing.T) {
	primary := &mockPlugin{name: "gopls", initErr: errors.New("start failed")}
	fallback := &mockPlugin{name: "treesitter", initErr: errors.New("init failed")}
	fp := NewFallbackParser(primary, fallback)

	if err := fp.Init(context.Background(), nil); err == nil {
		t.Fatal("expected error when both init fail")
	}
}
