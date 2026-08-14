package vector

import "testing"

func TestLookupModelRegistry(t *testing.T) {
	cases := []struct {
		name string
		want bool
	}{
		{"bge-small-zh", true},
		{"bge-small-zh-v1.5", true},
		{"bge-base-zh", true},
		{"unknown-model", false},
		{"", false},
	}
	for _, c := range cases {
		entry, ok := LookupModelRegistry(c.name)
		if ok != c.want {
			t.Errorf("LookupModelRegistry(%q) ok=%v want %v", c.name, ok, c.want)
		}
		if ok && entry.DownloadURL == "" {
			t.Errorf("LookupModelRegistry(%q): expected non-empty DownloadURL", c.name)
		}
	}
}

func TestResolveDownloadConfig(t *testing.T) {
	// 显式 URL 优先
	url, sha, known := ResolveDownloadConfig("bge-small-zh", "https://explicit.example/m.tar.gz", "abc")
	if !known || url != "https://explicit.example/m.tar.gz" || sha != "abc" {
		t.Errorf("explicit config not honored: url=%q sha=%q known=%v", url, sha, known)
	}

	// 注册表命中（无显式 URL）
	url, sha, known = ResolveDownloadConfig("bge-small-zh", "", "")
	if !known || url == "" {
		t.Errorf("registry lookup failed: url=%q known=%v", url, known)
	}

	// 未知模型且无显式 URL → 未命中
	url, _, known = ResolveDownloadConfig("unknown-model", "", "")
	if known || url != "" {
		t.Errorf("expected unknown model to be unresolved, got url=%q known=%v", url, known)
	}
}

func TestModelDownloader_ResolveFromRegistry(t *testing.T) {
	// 无显式 URL → 注册表回填成功
	dl := NewModelDownloader(t.TempDir(), "", "")
	if !dl.ResolveFromRegistry("bge-small-zh") {
		t.Fatal("expected registry resolution success for known model")
	}
	if dl.URL == "" {
		t.Fatal("expected URL filled from registry")
	}

	// 未知模型 → 回填失败
	dl2 := NewModelDownloader(t.TempDir(), "", "")
	if dl2.ResolveFromRegistry("unknown-model") {
		t.Fatal("expected registry resolution failure for unknown model")
	}

	// 已有显式 URL → 直接成功（不覆盖）
	dl3 := NewModelDownloader(t.TempDir(), "https://x/m.tar.gz", "abc")
	if !dl3.ResolveFromRegistry("bge-small-zh") {
		t.Fatal("expected success with explicit URL")
	}
	if dl3.URL != "https://x/m.tar.gz" {
		t.Errorf("expected explicit URL preserved, got %q", dl3.URL)
	}
}
