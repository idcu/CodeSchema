package scip

import (
	"os"
	"path/filepath"
	"testing"
)

// TestSCIPAdapter_ConvertDocument_Calls 验证 SCIP 适配器的「引用→调用关系」提取逻辑。
//
// 这是生产路径中的关键能力：当同一个符号同时存在定义 occurrence（SymbolRole=0）
// 与引用 occurrence（SymbolRole=1）时，convertDocument 应生成一条 CallIR。
// 该路径在此前的单测中未被覆盖。
func TestSCIPAdapter_ConvertDocument_Calls(t *testing.T) {
	doc := &SCIPDocument{
		RelativePath: "src/main/Foo.java",
		Language:     "Java",
		Symbols: []*SCIPSymbol{
			{ID: "com.example.Foo", Name: "Foo", Kind: 0, EnclosingSymbol: ""},
			{ID: "com.example.Foo.bar", Name: "bar", Kind: 1, EnclosingSymbol: "com.example.Foo"},
		},
		Occurrences: []*SCIPOccurrence{
			// 类定义
			{Symbol: "com.example.Foo", SymbolRole: 0, Range: []int{1, 0, 50, 0}},
			// 方法定义
			{Symbol: "com.example.Foo.bar", SymbolRole: 0, Range: []int{10, 0, 20, 0}},
			// 对方法的引用（应生成一条调用）
			{Symbol: "com.example.Foo.bar", SymbolRole: 1, Range: []int{3, 4, 3, 7}},
		},
	}
	a := NewSCIPAdapter("")
	ir := a.convertDocument(doc)

	if len(ir.Classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(ir.Classes))
	}
	if ir.Classes[0].FullName != "com.example.Foo" {
		t.Errorf("class fullName = %q", ir.Classes[0].FullName)
	}
	if len(ir.Methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(ir.Methods))
	}
	if ir.Methods[0].Name != "bar" || ir.Methods[0].ClassFQN != "com.example.Foo" {
		t.Errorf("method = name=%q classFQN=%q, want bar/com.example.Foo", ir.Methods[0].Name, ir.Methods[0].ClassFQN)
	}
	if len(ir.Calls) != 1 {
		t.Fatalf("expected 1 call (reference→call), got %d", len(ir.Calls))
	}
	c := ir.Calls[0]
	if c.CallerFQN != "src/main/Foo.java" {
		t.Errorf("callerFQN = %q, want file relative_path", c.CallerFQN)
	}
	if c.CalleeFQN != "com.example.Foo.bar" {
		t.Errorf("calleeFQN = %q, want method symbol id", c.CalleeFQN)
	}
	if c.CallType != "direct" {
		t.Errorf("callType = %q, want direct", c.CallType)
	}
	if c.LineNumber != 3 {
		t.Errorf("lineNumber = %d, want 3", c.LineNumber)
	}
}

// TestSCIPAdapter_ConvertDocument_NoCallWithoutDefinition 验证：
// 仅存在引用、无对应定义时，不应生成调用（避免悬空调用边）。
func TestSCIPAdapter_ConvertDocument_NoCallWithoutDefinition(t *testing.T) {
	doc := &SCIPDocument{
		RelativePath: "src/main/Foo.java",
		Language:     "Java",
		Symbols:      []*SCIPSymbol{},
		Occurrences: []*SCIPOccurrence{
			{Symbol: "com.example.Unknown.baz", SymbolRole: 1, Range: []int{3, 4, 3, 7}},
		},
	}
	a := NewSCIPAdapter("")
	ir := a.convertDocument(doc)
	if len(ir.Calls) != 0 {
		t.Fatalf("expected 0 calls without definition, got %d", len(ir.Calls))
	}
}

// TestSCIPAdapter_ParseRealIndexFile 端到端：从磁盘加载真实感 .scip 文件并解析。
//
// 覆盖「loadIndex → 路径匹配 → convertDocument」完整链路，确保 ParseAll 在真实
// 多文件 index 下能正确分发 IR。同时验证降级行为：不在 index 中的文件返回空 IR。
func TestSCIPAdapter_ParseRealIndexFile(t *testing.T) {
	dir := t.TempDir()
	indexJSON := `{
		"metadata": {"tool_info": {"name": "scip-go", "version": "0.3.0"}},
		"documents": [
			{
				"relative_path": "internal/service/svc.go",
				"language": "Go",
				"symbols": [
					{"id": "codeschema/internal/service.Service", "name": "Service", "kind": 0, "enclosing_symbol": ""},
					{"id": "codeschema/internal/service.Service.Run", "name": "Run", "kind": 1, "enclosing_symbol": "codeschema/internal/service.Service"}
				],
				"occurrences": [
					{"symbol": "codeschema/internal/service.Service", "symbol_role": 0, "range": [5, 0, 30, 0]},
					{"symbol": "codeschema/internal/service.Service.Run", "symbol_role": 0, "range": [12, 0, 22, 0]},
					{"symbol": "codeschema/internal/service.Service.Run", "symbol_role": 1, "range": [45, 2, 45, 5]}
				]
			}
		]
	}`
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), []byte(indexJSON), 0644); err != nil {
		t.Fatal(err)
	}

	a := NewSCIPAdapter(dir)
	if err := a.Init(t.Context(), nil); err != nil {
		t.Fatalf("init: %v", err)
	}
	defer a.Close()

	// 命中 index 的文件
	ch, err := a.ParseAll(t.Context(), []string{filepath.Join(dir, "internal/service/svc.go")})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	ir := <-ch
	if ir == nil {
		t.Fatal("expected non-nil IR for indexed file")
	}
	if ir.Source != "scip" {
		t.Errorf("source = %q", ir.Source)
	}
	if ir.Language != "go" {
		t.Errorf("language = %q, want go", ir.Language)
	}
	if len(ir.Classes) != 1 || len(ir.Methods) != 1 || len(ir.Calls) != 1 {
		t.Errorf("got classes=%d methods=%d calls=%d, want 1/1/1", len(ir.Classes), len(ir.Methods), len(ir.Calls))
	}

	// 未命中 index 的文件 → 返回空 IR（降级，非错误）
	ch2, err := a.ParseAll(t.Context(), []string{filepath.Join(dir, "internal/other/missing.go")})
	if err != nil {
		t.Fatalf("ParseAll missing: %v", err)
	}
	ir2 := <-ch2
	if ir2 == nil {
		t.Fatal("expected non-nil IR for non-indexed file")
	}
	if len(ir2.Classes) != 0 || len(ir2.Methods) != 0 {
		t.Errorf("non-indexed file should yield empty IR, got classes=%d methods=%d", len(ir2.Classes), len(ir2.Methods))
	}
}
