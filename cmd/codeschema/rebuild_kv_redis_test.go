//go:build redis

package main

import (
	"testing"

	"github.com/idcu/codeschema/internal/store"
)

// TestClassRecordToIR 验证存储层 ClassRecord → parser.ClassIR 转换（rebuild-kv 核心）。
func TestClassRecordToIR(t *testing.T) {
	r := &store.ClassRecord{
		ID: 1, FileID: 2,
		Name: "Service", FullName: "pkg.Service", Type: "CLASS",
		ParentFQNs: []string{"Base"}, StartLine: 3, StartCol: 4, EndLine: 9, EndCol: 10,
		Modifier: "public", Doc: "doc",
	}
	ir := classRecordToIR(r)
	if ir.Name != "Service" || ir.FullName != "pkg.Service" || ir.Type != "CLASS" {
		t.Errorf("basic fields mismatch: %+v", ir)
	}
	if len(ir.ParentFQNs) != 1 || ir.ParentFQNs[0] != "Base" {
		t.Errorf("parent fqns mismatch: %+v", ir.ParentFQNs)
	}
	if ir.StartLine != 3 || ir.StartCol != 4 || ir.EndLine != 9 || ir.EndCol != 10 {
		t.Errorf("line/col mismatch: %+v", ir)
	}
	if ir.Modifier != "public" || ir.Doc != "doc" {
		t.Errorf("modifier/doc mismatch: %+v", ir)
	}
}

// TestCallRecordToIR 验证存储层 CallRecord → parser.CallIR 转换。
func TestCallRecordToIR(t *testing.T) {
	r := &store.CallRecord{
		CallerFQN: "A.B", CalleeFQN: "C.D", CallType: "direct", LineNumber: 42,
	}
	ir := callRecordToIR(r)
	if ir.CallerFQN != "A.B" || ir.CalleeFQN != "C.D" || ir.CallType != "direct" || ir.LineNumber != 42 {
		t.Errorf("call conversion mismatch: %+v", ir)
	}
}
