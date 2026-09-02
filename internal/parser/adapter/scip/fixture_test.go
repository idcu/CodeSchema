package scip

import (
	"os"
	"path/filepath"
	"testing"
)

// fixture 语义与真实 scip-typescript 0.4.0 产物一致（见 scipwire.go 头注释）：
//   - SymbolRole 位掩码 Definition=1；普通引用 occurrence 的 SymbolRole=0；
//   - Range 为 [line, startChar, endChar]（3 元素）；
//   - 方法定义 occurrence 的 EnclosingRange（field 7）覆盖整个方法体；
//   - 符号为完整 SCIP symbol（descriptor 语法：`Foo#` 类、`Foo#bar().` 方法）。

// TestSCIPAdapter_ConvertDocument_Calls 验证「方法体引用 → 方法级调用」提取。
//
// 历史缺陷回归锚点：旧实现把 SymbolRole=2 当「引用」（实为 Import），
// 且 caller 直接记文件路径、引用需命中同文件定义——真实产物下
// （引用 roles=0）调用关系完全丢失。新语义：Definition 之外的引用 +
// 方法级 callee + 引用行落在方法 enclosing_range 内 → 生成 CallIR。
func TestSCIPAdapter_ConvertDocument_Calls(t *testing.T) {
	doc := &SCIPDocument{
		RelativePath: "src/calc.ts",
		Symbols: []*SCIPSymbol{
			{ID: "scip-typescript npm . . src/calc.ts/Calculator#"},
			{ID: "scip-typescript npm . . src/calc.ts/Calculator#add()."},
			{ID: "scip-typescript npm . . src/calc.ts/Calculator#double()."},
		},
		Occurrences: []*SCIPOccurrence{
			// add 方法定义（enclosing_range 覆盖方法体 行1-3）
			{Symbol: "scip-typescript npm . . src/calc.ts/Calculator#add().", SymbolRole: 1, Range: []int{1, 2, 5}, EnclosingRange: []int{1, 0, 3, 1}},
			// double 方法定义（方法体 行5-7）
			{Symbol: "scip-typescript npm . . src/calc.ts/Calculator#double().", SymbolRole: 1, Range: []int{5, 2, 8}, EnclosingRange: []int{5, 0, 7, 1}},
			// 类定义
			{Symbol: "scip-typescript npm . . src/calc.ts/Calculator#", SymbolRole: 1, Range: []int{0, 13, 23}, EnclosingRange: []int{0, 0, 8, 1}},
			// double 方法体内 this.add(...) 引用（roles=0，行6）→ 调用 double→add
			{Symbol: "scip-typescript npm . . src/calc.ts/Calculator#add().", SymbolRole: 0, Range: []int{6, 11, 14}},
		},
	}
	a := NewSCIPAdapter("")
	ir := a.convertDocument(doc)

	if len(ir.Classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(ir.Classes))
	}
	if ir.Classes[0].FullName != "scip-typescript npm . . src/calc.ts/Calculator#" {
		t.Errorf("class FullName = %q", ir.Classes[0].FullName)
	}
	if len(ir.Methods) != 2 {
		t.Fatalf("expected 2 methods (add/double), got %d", len(ir.Methods))
	}
	if len(ir.Calls) != 1 {
		t.Fatalf("expected 1 call (double→add), got %d: %v", len(ir.Calls), ir.Calls)
	}
	c := ir.Calls[0]
	wantCaller := "scip-typescript npm . . src/calc.ts/Calculator#double()."
	if c.CallerFQN != wantCaller {
		t.Errorf("callerFQN = %q, want method FQN %q", c.CallerFQN, wantCaller)
	}
	if c.CalleeFQN != "scip-typescript npm . . src/calc.ts/Calculator#add()." {
		t.Errorf("calleeFQN = %q, want add method symbol", c.CalleeFQN)
	}
	if c.CallType != "direct" {
		t.Errorf("callType = %q, want direct", c.CallType)
	}
	if c.LineNumber != 6 {
		t.Errorf("lineNumber = %d, want 6", c.LineNumber)
	}
}

// TestSCIPAdapter_ConvertDocument_NoCallOutsideMethod 验证：
// 类级引用（如 new Calculator()、类型注解）与不在任何方法体内的引用
// 不应生成调用（避免把「引用」误当「调用」）。
func TestSCIPAdapter_ConvertDocument_NoCallOutsideMethod(t *testing.T) {
	doc := &SCIPDocument{
		RelativePath: "src/calc.ts",
		Symbols: []*SCIPSymbol{
			{ID: "scip-typescript npm . . src/calc.ts/Calculator#"},
			{ID: "scip-typescript npm . . src/calc.ts/Calculator#add()."},
			{ID: "scip-typescript npm . . src/calc.ts/Calculator#double()."},
		},
		Occurrences: []*SCIPOccurrence{
			// double 方法定义（方法体 行5-7）
			{Symbol: "scip-typescript npm . . src/calc.ts/Calculator#double().", SymbolRole: 1, Range: []int{5, 2, 8}, EnclosingRange: []int{5, 0, 7, 1}},
			// 类引用（行0 export class 之外无方法包裹——如模块顶层类型注解）
			{Symbol: "scip-typescript npm . . src/calc.ts/Calculator#", SymbolRole: 0, Range: []int{0, 20, 31}},
			// 方法级引用但落在任何方法体之外（行9 顶层调用）
			{Symbol: "scip-typescript npm . . src/calc.ts/Calculator#add().", SymbolRole: 0, Range: []int{9, 0, 3}},
		},
	}
	a := NewSCIPAdapter("")
	ir := a.convertDocument(doc)
	if len(ir.Calls) != 0 {
		t.Fatalf("expected 0 calls (refs outside method bodies), got %d: %v", len(ir.Calls), ir.Calls)
	}
}

// TestSCIPAdapter_ParseRealIndexFile 端到端：从磁盘加载真实形态二进制 .scip
// 并解析，覆盖「loadIndex → 路径匹配 → convertDocument」完整链路；
// 同时验证不在 index 中的文件返回空 IR（降级，非错误）。
func TestSCIPAdapter_ParseRealIndexFile(t *testing.T) {
	dir := t.TempDir()
	idx := encIndex(encDocument("internal/service/svc.go", []string{
		"scip-typescript npm . . internal/service/svc.go/Service#",
		"scip-typescript npm . . internal/service/svc.go/Service#Run().",
		"scip-typescript npm . . internal/service/svc.go/Service#helper().",
	},
		// Service 类定义
		&SCIPOccurrence{Symbol: "scip-typescript npm . . internal/service/svc.go/Service#", SymbolRole: 1, Range: []int{5, 6, 14}, EnclosingRange: []int{5, 0, 30, 1}},
		// helper 方法定义（方法体 行12-22）
		&SCIPOccurrence{Symbol: "scip-typescript npm . . internal/service/svc.go/Service#helper().", SymbolRole: 1, Range: []int{12, 2, 8}, EnclosingRange: []int{12, 0, 22, 1}},
		// Run 方法定义（方法体 行30-50）
		&SCIPOccurrence{Symbol: "scip-typescript npm . . internal/service/svc.go/Service#Run().", SymbolRole: 1, Range: []int{30, 2, 5}, EnclosingRange: []int{30, 0, 50, 1}},
		// Run 方法体内调用 helper（行45）→ Run→helper
		&SCIPOccurrence{Symbol: "scip-typescript npm . . internal/service/svc.go/Service#helper().", SymbolRole: 0, Range: []int{45, 2, 8}},
	))
	if err := os.WriteFile(filepath.Join(dir, "index.scip"), idx, 0644); err != nil {
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
	if len(ir.Classes) != 1 || len(ir.Methods) != 2 || len(ir.Calls) != 1 {
		t.Errorf("got classes=%d methods=%d calls=%d, want 1/2/1", len(ir.Classes), len(ir.Methods), len(ir.Calls))
	}
	if c := ir.Calls[0]; c.CalleeFQN != "scip-typescript npm . . internal/service/svc.go/Service#helper()." {
		t.Errorf("call callee = %q, want helper method symbol", c.CalleeFQN)
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
