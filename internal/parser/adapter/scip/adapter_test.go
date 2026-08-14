package scip

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestSCIPAdapter_Name(t *testing.T) {
	a := NewSCIPAdapter("")
	if a.Name() != "scip" {
		t.Errorf("expected 'scip', got '%s'", a.Name())
	}
}

func TestSCIPAdapter_Supports(t *testing.T) {
	a := NewSCIPAdapter("")
	cases := []struct {
		lang string
		want bool
	}{
		{"go", true},
		{"java", true},
		{"ts", true},
		{"py", true},
		{"rust", true},
		{"cpp", true},
		{"ruby", false},
		{"unknown", false},
	}
	for _, c := range cases {
		got := a.Supports(c.lang)
		if got != c.want {
			t.Errorf("Supports(%q) = %v, want %v", c.lang, got, c.want)
		}
	}
}

func TestSCIPAdapter_Init_NoIndexDir(t *testing.T) {
	a := &SCIPAdapter{}
	err := a.Init(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for empty index dir")
	}
}

func TestSCIPAdapter_Init_WithConfig(t *testing.T) {
	a := &SCIPAdapter{}
	cfg := map[string]any{"index_dir": "/tmp/scip-index"}
	err := a.Init(context.Background(), cfg)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.indexDir != "/tmp/scip-index" {
		t.Errorf("expected indexDir=/tmp/scip-index, got %s", a.indexDir)
	}
}

func TestSCIPAdapter_Parse_SourceUnavailable(t *testing.T) {
	a := NewSCIPAdapter("")
	_, err := a.Parse(context.Background(), "test.java")
	if err == nil {
		t.Fatal("expected error for unconfigured adapter")
	}
}

func TestSCIPAdapter_LoadIndex_NoFiles(t *testing.T) {
	dir := t.TempDir()
	a := NewSCIPAdapter(dir)
	err := a.loadIndex()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.loaded != true {
		t.Fatal("expected loaded=true")
	}
	if len(a.documents) != 0 {
		t.Errorf("expected 0 documents, got %d", len(a.documents))
	}
}

func TestSCIPAdapter_LoadIndex_InvalidFile(t *testing.T) {
	dir := t.TempDir()
	err := os.WriteFile(filepath.Join(dir, "index.scip"), []byte("not json"), 0644)
	if err != nil {
		t.Fatal(err)
	}
	a := NewSCIPAdapter(dir)
	err = a.loadIndex()
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestSCIPAdapter_LoadIndex_ValidFile(t *testing.T) {
	dir := t.TempDir()
	indexJSON := `{
		"metadata": {"tool_info": {"name": "scip-java", "version": "1.0"}},
		"documents": [
			{
				"relative_path": "src/main/Foo.java",
				"language": "Java",
				"symbols": [
					{"id": "com.example.Foo", "name": "Foo", "kind": 0, "enclosing_symbol": ""}
				],
				"occurrences": [
					{"symbol": "com.example.Foo", "symbol_role": 0, "range": [1, 0, 50, 0]}
				]
			}
		]
	}`
	err := os.WriteFile(filepath.Join(dir, "index.scip"), []byte(indexJSON), 0644)
	if err != nil {
		t.Fatal(err)
	}
	a := NewSCIPAdapter(dir)
	err = a.loadIndex()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.documents) != 1 {
		t.Fatalf("expected 1 document, got %d", len(a.documents))
	}
	if a.documents[0].RelativePath != "src/main/Foo.java" {
		t.Errorf("expected relative_path=src/main/Foo.java, got %s", a.documents[0].RelativePath)
	}
}

func TestSCIPAdapter_ConvertDocument_Class(t *testing.T) {
	doc := &SCIPDocument{
		RelativePath: "src/main/Foo.java",
		Language:     "Java",
		Symbols: []*SCIPSymbol{
			{ID: "com.example.Foo", Name: "Foo", Kind: 0},
		},
		Occurrences: []*SCIPOccurrence{
			{Symbol: "com.example.Foo", SymbolRole: 0, Range: []int{1, 0, 50, 0}},
		},
	}
	a := NewSCIPAdapter("")
	ir := a.convertDocument(doc)
	if ir.Source != "scip" {
		t.Errorf("expected source=scip, got %s", ir.Source)
	}
	if ir.Language != "java" {
		t.Errorf("expected language=java, got %s", ir.Language)
	}
	if ir.FilePath != "src/main/Foo.java" {
		t.Errorf("expected filepath=src/main/Foo.java, got %s", ir.FilePath)
	}
	if len(ir.Classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(ir.Classes))
	}
	if ir.Classes[0].Name != "Foo" {
		t.Errorf("expected class name=Foo, got %s", ir.Classes[0].Name)
	}
}

func TestSCIPAdapter_ConvertDocument_Method(t *testing.T) {
	doc := &SCIPDocument{
		RelativePath: "src/main/Foo.java",
		Language:     "Java",
		Symbols: []*SCIPSymbol{
			{ID: "com.example.Foo.bar", Name: "bar", Kind: 1, EnclosingSymbol: "com.example.Foo"},
		},
		Occurrences: []*SCIPOccurrence{
			{Symbol: "com.example.Foo.bar", SymbolRole: 0, Range: []int{10, 0, 20, 0}},
		},
	}
	a := NewSCIPAdapter("")
	ir := a.convertDocument(doc)
	if len(ir.Methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(ir.Methods))
	}
	if ir.Methods[0].Name != "bar" {
		t.Errorf("expected method name=bar, got %s", ir.Methods[0].Name)
	}
	if ir.Methods[0].ClassFQN != "com.example.Foo" {
		t.Errorf("expected classFQN=com.example.Foo, got %s", ir.Methods[0].ClassFQN)
	}
}

func TestSCIPAdapter_ParseAll_NoIndexDir(t *testing.T) {
	a := NewSCIPAdapter("")
	_, err := a.ParseAll(context.Background(), []string{"test.java"})
	if err == nil {
		t.Fatal("expected error for empty index dir")
	}
}

func TestSCIPAdapter_ParseAll_NonExistentDir(t *testing.T) {
	a := NewSCIPAdapter("/nonexistent/path")
	_, err := a.ParseAll(context.Background(), []string{"test.java"})
	if err == nil {
		t.Fatal("expected error for non-existent dir")
	}
}

func TestSCIPAdapter_Close(t *testing.T) {
	a := NewSCIPAdapter("/tmp")
	a.loaded = true
	a.documents = []*SCIPDocument{{RelativePath: "test.java"}}
	err := a.Close()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if a.loaded != false {
		t.Error("expected loaded=false after close")
	}
	if len(a.documents) != 0 {
		t.Error("expected empty documents after close")
	}
}

func TestSCIPLangToCodeLang(t *testing.T) {
	cases := []struct {
		scipLang string
		want     string
	}{
		{"Go", "go"},
		{"Java", "java"},
		{"TypeScript", "ts"},
		{"JavaScript", "ts"},
		{"Python", "py"},
		{"Rust", "rust"},
		{"C++", "cpp"},
		{"C", "cpp"},
		{"Ruby", "ruby"},
	}
	for _, c := range cases {
		got := scipLangToCodeLang(c.scipLang)
		if got != c.want {
			t.Errorf("scipLangToCodeLang(%q) = %q, want %q", c.scipLang, got, c.want)
		}
	}
}

func TestSCIPAdapter_LoadIndex_MultipleFiles(t *testing.T) {
	dir := t.TempDir()

	index1 := `{"documents": [{"relative_path": "a.java", "language": "Java", "symbols": [], "occurrences": []}]}`
	index2 := `{"documents": [{"relative_path": "b.go", "language": "Go", "symbols": [], "occurrences": []}]}`

	os.WriteFile(filepath.Join(dir, "index1.scip"), []byte(index1), 0644)
	os.WriteFile(filepath.Join(dir, "index2.scip"), []byte(index2), 0644)

	a := NewSCIPAdapter(dir)
	err := a.loadIndex()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.documents) != 2 {
		t.Errorf("expected 2 documents, got %d", len(a.documents))
	}
}

func TestSCIPAdapter_LoadIndex_SkipNonScipFiles(t *testing.T) {
	dir := t.TempDir()

	indexJSON := `{"documents": [{"relative_path": "a.java", "language": "Java", "symbols": [], "occurrences": []}]}`
	os.WriteFile(filepath.Join(dir, "index.scip"), []byte(indexJSON), 0644)
	os.WriteFile(filepath.Join(dir, "readme.txt"), []byte("not an index"), 0644)

	a := NewSCIPAdapter(dir)
	err := a.loadIndex()
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.documents) != 1 {
		t.Errorf("expected 1 document (skip .txt), got %d", len(a.documents))
	}
}
func TestSCIPAdapter_LoadIndex_StreamingBackpressure(t *testing.T) {
	dir := t.TempDir()
	// 构造一个包含 5 个文档的 index，验证 maxDocs 背压截断（流式加载不整读内存）
	var b strings.Builder
	b.WriteString(`{"metadata": {"tool_info": {"name": "scip-java"}}, "documents": [`)
	for i := 0; i < 5; i++ {
		if i > 0 {
			b.WriteString(",")
		}
		b.WriteString(fmt.Sprintf(`{"relative_path": "f%d.java", "language": "Java", "symbols": [], "occurrences": []}`, i))
	}
	b.WriteString(`]}`)
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), []byte(b.String()), 0644); err != nil {
		t.Fatal(err)
	}

	a := NewSCIPAdapter(dir)
	a.SetMaxDocs(2)
	if err := a.loadIndex(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a.documents) != 2 {
		t.Fatalf("expected 2 documents (backpressure), got %d", len(a.documents))
	}
	if !a.Truncated() {
		t.Fatal("expected truncated=true when maxDocs exceeded")
	}

	// 不限流时应全量加载且不截断
	a2 := NewSCIPAdapter(dir)
	if err := a2.loadIndex(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(a2.documents) != 5 {
		t.Fatalf("expected 5 documents, got %d", len(a2.documents))
	}
	if a2.Truncated() {
		t.Fatal("expected truncated=false without maxDocs")
	}
}

func TestSCIPAdapter_LoadIndex_ReloadIdempotent(t *testing.T) {
	dir := t.TempDir()
	indexJSON := `{"documents": [{"relative_path": "a.java", "language": "Java", "symbols": [], "occurrences": []}]}`
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), []byte(indexJSON), 0644); err != nil {
		t.Fatal(err)
	}
	a := NewSCIPAdapter(dir)
	if err := a.loadIndex(); err != nil {
		t.Fatalf("first load: %v", err)
	}
	// 重复 Init → loadIndex 应幂等（清空后重载，不累积重复文档）
	if err := a.loadIndex(); err != nil {
		t.Fatalf("second load: %v", err)
	}
	if len(a.documents) != 1 {
		t.Fatalf("expected 1 document after reload, got %d", len(a.documents))
	}
}
