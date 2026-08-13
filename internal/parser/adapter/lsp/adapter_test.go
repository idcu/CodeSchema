package lsp

import (
	"context"
	"testing"
	"time"

	"codeschema/internal/parser"
)

func TestLSPAdapter_Name(t *testing.T) {
	a := NewLSPAdapter("test-lsp", "echo", nil, "go", 0)
	if a.Name() != "test-lsp" {
		t.Errorf("expected 'test-lsp', got '%s'", a.Name())
	}
}

func TestLSPAdapter_Supports(t *testing.T) {
	a := NewLSPAdapter("gopls", "gopls", nil, "go", 0)
	if !a.Supports("go") {
		t.Error("expected Supports(go)=true")
	}
	if a.Supports("java") {
		t.Error("expected Supports(java)=false")
	}
}

func TestNewGoplsAdapter(t *testing.T) {
	a := NewGoplsAdapter()
	if a.Name() != "gopls" {
		t.Errorf("expected name=gopls, got %s", a.Name())
	}
	if !a.Supports("go") {
		t.Error("expected gopls to support go")
	}
}

func TestNewJDTLSAdapter(t *testing.T) {
	a := NewJDTLSAdapter()
	if a.Name() != "jdtls" {
		t.Errorf("expected name=jdtls, got %s", a.Name())
	}
	if !a.Supports("java") {
		t.Error("expected jdtls to support java")
	}
}

func TestNewClangdAdapter(t *testing.T) {
	a := NewClangdAdapter()
	if a.Name() != "clangd" {
		t.Errorf("expected name=clangd, got %s", a.Name())
	}
	if !a.Supports("cpp") {
		t.Error("expected clangd to support cpp")
	}
}

func TestLSPAdapter_Init_CommandNotFound(t *testing.T) {
	a := NewLSPAdapter("nonexistent", "command-that-does-not-exist-12345", nil, "go", 0)
	err := a.Init(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for non-existent command")
	}
}

func TestLSPAdapter_Parse_NotInitialized(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 0)
	_, err := a.Parse(context.Background(), "test.go")
	if err == nil {
		t.Fatal("expected error for uninitialized adapter")
	}
}

func TestLSPAdapter_Close_Uninitialized(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 0)
	err := a.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestLSPAdapter_DefaultTimeout(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 0)
	if a.timeout != 10*time.Second {
		t.Errorf("expected default timeout 10s, got %v", a.timeout)
	}
}

func TestLSPAdapter_CustomTimeout(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 30*time.Second)
	if a.timeout != 30*time.Second {
		t.Errorf("expected custom timeout 30s, got %v", a.timeout)
	}
}

func TestAddSymbolInfo_Class(t *testing.T) {
	a := NewGoplsAdapter()
	ir := &parser.IRDocument{}
	sym := symbolInfo{
		Name: "Foo",
		Kind: 5, // Class
		Location: struct {
			URI   string `json:"uri"`
			Range struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
				End struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"end"`
			} `json:"range"`
		}{
			URI: "file:///test.go",
			Range: struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
				End struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"end"`
			}{
				Start: struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				}{Line: 0, Character: 0},
				End: struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				}{Line: 10, Character: 0},
			},
		},
		ContainerName: "pkg",
	}
	ir = a.addSymbolInfo(ir, sym)
	if len(ir.Classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(ir.Classes))
	}
	if ir.Classes[0].Name != "Foo" {
		t.Errorf("expected class name=Foo, got %s", ir.Classes[0].Name)
	}
	if ir.Classes[0].StartLine != 1 {
		t.Errorf("expected StartLine=1, got %d", ir.Classes[0].StartLine)
	}
}

func TestAddSymbolInfo_Method(t *testing.T) {
	a := NewGoplsAdapter()
	ir := &parser.IRDocument{}
	sym := symbolInfo{
		Name: "bar",
		Kind: 6, // Method
		Location: struct {
			URI   string `json:"uri"`
			Range struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
				End struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"end"`
			} `json:"range"`
		}{
			URI: "file:///test.go",
			Range: struct {
				Start struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"start"`
				End struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				} `json:"end"`
			}{
				Start: struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				}{Line: 5, Character: 0},
				End: struct {
					Line      int `json:"line"`
					Character int `json:"character"`
				}{Line: 8, Character: 0},
			},
		},
		ContainerName: "pkg.Foo",
	}
	ir = a.addSymbolInfo(ir, sym)
	if len(ir.Methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(ir.Methods))
	}
	if ir.Methods[0].Name != "bar" {
		t.Errorf("expected method name=bar, got %s", ir.Methods[0].Name)
	}
	if ir.Methods[0].ClassFQN != "pkg.Foo" {
		t.Errorf("expected ClassFQN=pkg.Foo, got %s", ir.Methods[0].ClassFQN)
	}
}

func TestAddDocumentSymbol_ClassWithChildren(t *testing.T) {
	a := NewGoplsAdapter()
	ir := &parser.IRDocument{}
	ds := documentSymbol{
		Name: "Foo",
		Kind: 5,
		Range: documentRange{
			Start: struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			}{Line: 0, Character: 0},
			End: struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			}{Line: 20, Character: 0},
		},
		Children: []documentSymbol{
			{
				Name: "bar",
				Kind: 6,
				Range: documentRange{
					Start: struct {
						Line      int `json:"line"`
						Character int `json:"character"`
					}{Line: 5, Character: 0},
					End: struct {
						Line      int `json:"line"`
						Character int `json:"character"`
					}{Line: 10, Character: 0},
				},
			},
		},
	}
	ir = a.addDocumentSymbol(ir, ds)
	if len(ir.Classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(ir.Classes))
	}
	if len(ir.Methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(ir.Methods))
	}
	if ir.Methods[0].Name != "bar" {
		t.Errorf("expected method name=bar, got %s", ir.Methods[0].Name)
	}
}

func TestLSPAdapter_SendNotification(t *testing.T) {
	a := NewLSPAdapter("test", "echo", nil, "go", 0)
	a.sendNotification("test/method", map[string]string{"key": "value"})
}

func TestLSPAdapter_ReadResponses(t *testing.T) {
	// 不 panic 即通过（stdout 关闭后正常退出）
	a := NewLSPAdapter("test", "echo", nil, "go", 0)
	go a.readResponses()
}