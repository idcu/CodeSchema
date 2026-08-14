//go:build !treesitter

package treesitter

import (
	"testing"
)

// TestCodeSanitizer 验证字符串/注释剔除状态机（T2-1 补强①）。
func TestCodeSanitizer(t *testing.T) {
	var s codeSanitizer
	cases := []struct {
		lang string
		in   string
		want string
	}{
		// 行内字符串中的伪调用应被剔除（引号与内容替换为空格）
		{"go", `msg := "foo(bar)"`, `msg :=           `},
		// 行注释后的伪调用应被剔除（// 之后全部替换）
		{"go", `foo(); // bar(baz)`, `foo();            `},
		// 真实调用应保留
		{"go", `foo(bar)`, `foo(bar)`},
		// 单引号字符串（Java/TS）：引号内替换、引号外保留
		{"java", `String s = 'a(b)';`, `String s =       ;`},
		// Python 行注释：# 之后全部替换
		{"py", `foo() # bar(baz)`, `foo()           `},
	}
	for _, c := range cases {
		got := s.clean(c.in, c.lang)
		if got != c.want {
			t.Errorf("clean(%q, %s) = %q, want %q", c.in, c.lang, got, c.want)
		}
	}
}

// TestCodeSanitizer_BlockComment 验证跨行块注释状态（/* ... */ 跨多行）。
func TestCodeSanitizer_BlockComment(t *testing.T) {
	s := codeSanitizer{}
	// 第一行进入块注释
	got1 := s.clean(`/* foo(bar)`, "java")
	if s.inBlockComment != true {
		t.Fatal("expected inBlockComment after /*")
	}
	_ = got1
	// 第二行仍在块注释内，伪调用被剔除
	got2 := s.clean(`   baz(qux)`, "java")
	_ = got2
	if s.inBlockComment != true {
		t.Fatal("expected still inBlockComment")
	}
	// 第三行闭合
	got3 := s.clean(`   */ foo()`, "java")
	if s.inBlockComment != false {
		t.Fatal("expected block comment closed")
	}
	// 闭合后真实调用应保留
	if got3 != "      foo()" {
		t.Errorf("expected trailing foo() preserved, got %q", got3)
	}
}

// TestCodeSanitizer_TripleQuote 验证 Python 三引号跨行状态。
func TestCodeSanitizer_TripleQuote(t *testing.T) {
	s := codeSanitizer{}
	s.clean(`doc = """foo(bar)`, "py")
	if s.inTripleQuote != `"""` {
		t.Fatalf("expected inTripleQuote, got %q", s.inTripleQuote)
	}
	got := s.clean(`   baz(qux)""" real()`, "py")
	if s.inTripleQuote != "" {
		t.Fatal("expected triple quote closed")
	}
	if got != `               real()` {
		t.Errorf("expected real() preserved after triple quote close, got %q", got)
	}
}
