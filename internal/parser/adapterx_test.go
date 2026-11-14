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
		Calls: []CallIR{{
			CallerFQN: "pkg.A.M", CalleeFQN: "pkg.B.N", CallType: "direct", LineNumber: 3,
			Source: CallSourceTreeSitter, Confidence: ConfidenceTreeSitter,
		}},
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
	// R5-3：跨契约桥接必须保留调用边的来源与置信度。
	if back.Calls[0].Source != CallSourceTreeSitter || back.Calls[0].Confidence != ConfidenceTreeSitter {
		t.Errorf("call provenance lost in round-trip: %+v", back.Calls[0])
	}
}

// TestNormalizeCallProvenance 负控（R5-3）：历史数据 source 为空时按低置信度
// treesitter/60 解释；有来源但置信度缺省时回落到该来源的默认值。
func TestNormalizeCallProvenance(t *testing.T) {
	cases := []struct {
		source string
		conf   int
		want   string
		wantC  int
	}{
		{"", 0, CallSourceTreeSitter, ConfidenceExternal},
		{CallSourceRegex, 0, CallSourceRegex, ConfidenceRegex},
		{CallSourceTreeSitter, 0, CallSourceTreeSitter, ConfidenceTreeSitter},
		{CallSourceLSP, 0, CallSourceLSP, ConfidenceLSP},
		{CallSourceSCIP, 0, CallSourceSCIP, ConfidenceSCIP},
		{CallSourceSCIP, 42, CallSourceSCIP, 42},
	}
	for _, c := range cases {
		got, gotC := NormalizeCallProvenance(c.source, c.conf)
		if got != c.want || gotC != c.wantC {
			t.Errorf("NormalizeCallProvenance(%q,%d) = (%q,%d), want (%q,%d)",
				c.source, c.conf, got, gotC, c.want, c.wantC)
		}
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
