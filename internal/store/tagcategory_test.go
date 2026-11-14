package store

import "testing"

// TestDeriveTagCategory_BareLabels 验证裸标签（规则引擎产出）的精确分类。
func TestDeriveTagCategory_BareLabels(t *testing.T) {
	cases := map[string]string{
		"service": "layer", "controller": "layer", "dao": "layer", "domain": "layer",
		"infra": "layer", "repository": "layer", "handler": "layer", "middleware": "layer", "config": "layer",
		"unit": "test", "integration": "test", "e2e": "test", "mock": "test",
		"go": "lang", "java": "lang", "python": "lang", "rust": "lang",
		"legacy": "risk", "todo": "risk", "deprecated": "risk", "performance": "risk", "security": "risk",
		"cache": "tech", "mq": "tech", "retry": "tech", "transactional": "tech", "async": "tech",
	}
	for tag, want := range cases {
		if got := DeriveTagCategory(tag); got != want {
			t.Errorf("DeriveTagCategory(%q) want %q, got %q", tag, want, got)
		}
	}
	// 未知裸标签回退 biz
	if got := DeriveTagCategory("order"); got != "biz" {
		t.Errorf("DeriveTagCategory(order) want biz, got %q", got)
	}
}

// TestDeriveTagCategory_PrefixedLabels 验证 AI 增强产出的「分类:值」前缀标签分类。
// P21 修复：此前前缀标签被精确匹配遗漏、一律归入 biz，导致 stats 分类口径失真。
func TestDeriveTagCategory_PrefixedLabels(t *testing.T) {
	cases := map[string]string{
		"biz:订单中心": "biz", "biz:LSP适配器": "biz", "biz:代码分析": "biz",
		"tech:缓存": "tech", "tech:配置管理": "tech", "tech:异步队列": "tech",
		"test:单元测试": "test", "test:集成测试": "test",
		"risk:性能风险": "risk", "risk:待废弃": "risk",
		"layer:服务层": "layer", "lang:go": "lang",
	}
	for tag, want := range cases {
		if got := DeriveTagCategory(tag); got != want {
			t.Errorf("DeriveTagCategory(%q) want %q, got %q", tag, want, got)
		}
	}
	// 未知前缀：回退裸标签精确匹配
	if got := DeriveTagCategory("cache"); got != "tech" {
		t.Errorf("DeriveTagCategory(cache) want tech, got %q", got)
	}
	if got := DeriveTagCategory("foo:bar"); got != "biz" {
		t.Errorf("DeriveTagCategory(foo:bar) want biz (unknown prefix), got %q", got)
	}
}

// TestDeriveTagCategory_EdgeCases 验证边界：空串、无冒号、仅冒号、冒号在首/尾。
func TestDeriveTagCategory_EdgeCases(t *testing.T) {
	if got := DeriveTagCategory(""); got != "biz" {
		t.Errorf("DeriveTagCategory(\"\") want biz, got %q", got)
	}
	if got := DeriveTagCategory(":foo"); got != "biz" { // 冒号在首位，非合法前缀
		t.Errorf("DeriveTagCategory(:foo) want biz, got %q", got)
	}
	if got := DeriveTagCategory("biz:"); got != "biz" { // 冒号在末位，前缀为 biz
		t.Errorf("DeriveTagCategory(biz:) want biz, got %q", got)
	}
}
