package parser

import (
	"testing"

	"github.com/idcu/codeschema/contrib/adapterx"
)

func TestAdapterXRoundTrip(t *testing.T) {
	doc := &IRDocument{
		Source: "treesitter", Language: "go", FilePath: "/repo/a.go",
		FileHash: "abc", CommitHash: "def", LineCount: 10, ByteSize: 1024,
		ReferencedBy: []string{"b.go"}, Imports: []string{"fmt"},
		Classes: []ClassIR{{
			Name: "A", FullName: "pkg.A", Type: "CLASS", ParentFQNs: []string{"pkg.B"},
			StartLine: 1, StartCol: 0, EndLine: 5, EndCol: 1, Modifier: "public",
			Doc: "doc", Annotations: []string{"@Api"},
		}},
		Methods: []MethodIR{{
			Name: "M", Signature: "M()", ReturnType: "int", ClassFQN: "pkg.A",
			StartLine: 2, EndLine: 4, IsStatic: true, IsAbstract: false, IsConstructor: false,
			Params: []ParamIR{{Name: "x", Type: "int", Index: 0}},
		}},
		Calls: []CallIR{{CallerFQN: "pkg.A.M", CalleeFQN: "pkg.B.N", CallType: "direct", LineNumber: 3}},
	}

	back := FromAdapterX(ToAdapterX(doc))
	if back.Source != doc.Source || back.Language != doc.Language || back.FilePath != doc.FilePath {
		t.Errorf("scalar mismatch: %+v", back)
	}
	if len(back.Classes) != 1 || back.Classes[0].Name != "A" || back.Classes[0].ParentFQNs[0] != "pkg.B" {
		t.Errorf("class mismatch: %+v", back.Classes)
	}
	if len(back.Methods) != 1 || len(back.Methods[0].Params) != 1 || back.Methods[0].Params[0].Name != "x" {
		t.Errorf("method/params mismatch: %+v", back.Methods)
	}
	if len(back.Calls) != 1 || back.Calls[0].CalleeFQN != "pkg.B.N" {
		t.Errorf("calls mismatch: %+v", back.Calls)
	}
}

func TestAdapterX_NilSafe(t *testing.T) {
	if ToAdapterX(nil) != nil {
		t.Error("ToAdapterX(nil) should be nil")
	}
	if FromAdapterX(nil) != nil {
		t.Error("FromAdapterX(nil) should be nil")
	}
}

func TestAdapterX_TypeAliasCompat(t *testing.T) {
	// 契约类型可被直接构造，第三方无需依赖 internal。
	doc := &adapterx.IRDocument{Source: "third-party", Language: "rust", FilePath: "x.rs"}
	inner := FromAdapterX(doc)
	if inner.Source != "third-party" || inner.Language != "rust" {
		t.Errorf("contract to internal mismatch: %+v", inner)
	}
}
