package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestApplyPreset_Minimal(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Preset = PresetMinimal
	ApplyPreset(cfg)

	if cfg.Storage.Search.Semantic {
		t.Error("minimal preset should disable semantic search")
	}
	if !cfg.Storage.Search.FTS {
		t.Error("minimal preset should keep FTS on")
	}
	if cfg.Storage.Vector.Driver != "" {
		t.Errorf("minimal preset should disable vector driver, got %q", cfg.Storage.Vector.Driver)
	}
	if cfg.AI.BudgetPerScan != 0 || cfg.AI.BudgetPerQuery != 0 {
		t.Errorf("minimal preset should zero AI budget, got scan=%d query=%d", cfg.AI.BudgetPerScan, cfg.AI.BudgetPerQuery)
	}
}

func TestApplyPreset_Semantic(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Preset = PresetSemantic
	ApplyPreset(cfg)

	if !cfg.Storage.Search.FTS || !cfg.Storage.Search.Semantic {
		t.Error("semantic preset should enable both FTS and semantic search")
	}
}

func TestApplyPreset_Multitenant(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Preset = PresetMultitenant
	ApplyPreset(cfg)

	if !cfg.Storage.Search.Semantic {
		t.Error("multitenant preset should keep semantic search on")
	}
	if !cfg.Watcher.Enabled {
		t.Error("multitenant preset should enable watcher")
	}
}

func TestApplyPreset_Default_NoChange(t *testing.T) {
	cfg := DefaultConfig()
	before := cfg.Storage.Search.Semantic
	cfg.Preset = PresetDefault
	ApplyPreset(cfg)
	if cfg.Storage.Search.Semantic != before {
		t.Error("default preset should not change capabilities")
	}
}

func TestApplyPreset_Authoritative(t *testing.T) {
	// preset 是能力组合的权威来源：即使默认值/同层字段相反，也以 preset 为准
	cfg := DefaultConfig()
	cfg.Preset = PresetMinimal
	cfg.Storage.Search.Semantic = true // 与 preset 冲突的字段被 preset 覆盖
	ApplyPreset(cfg)
	if cfg.Storage.Search.Semantic {
		t.Error("minimal preset should override semantic=true")
	}
}

func TestValidPreset(t *testing.T) {
	valid := []string{"", "minimal", "semantic", "multitenant"}
	for _, v := range valid {
		if !ValidPreset(v) {
			t.Errorf("expected %q valid", v)
		}
	}
	invalid := []string{"full", "max", "MINIMAL", "semantic "}
	for _, v := range invalid {
		if ValidPreset(v) {
			t.Errorf("expected %q invalid", v)
		}
	}
}

func TestLoad_YAMLPreset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "preset: minimal\nproject:\n  root: /tmp/repo\nstorage:\n  dsn: ./data\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Preset != PresetMinimal {
		t.Errorf("expected preset minimal, got %q", cfg.Preset)
	}
	if cfg.Storage.Search.Semantic {
		t.Error("YAML preset minimal should disable semantic")
	}
	if cfg.AI.BudgetPerScan != 0 {
		t.Errorf("YAML preset minimal should zero scan budget, got %d", cfg.AI.BudgetPerScan)
	}
}

func TestLoad_JSONPreset(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	content := `{"preset":"semantic","project":{"root":"/tmp/repo"},"storage":{"dsn":"./data"}}`
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	if cfg.Preset != PresetSemantic {
		t.Errorf("expected preset semantic, got %q", cfg.Preset)
	}
	if !cfg.Storage.Search.Semantic {
		t.Error("JSON preset semantic should enable semantic")
	}
}

func TestLoad_InvalidPresetIgnored(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	content := "preset: unknown-preset\nproject:\n  root: /tmp/repo\n"
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("Load: %v", err)
	}
	// 未知 preset 被忽略（不应用），保持默认能力
	if cfg.Preset != "" {
		t.Errorf("expected empty preset for unknown value, got %q", cfg.Preset)
	}
	if !cfg.Storage.Search.Semantic {
		t.Error("invalid preset should not change default capabilities")
	}
}

func TestValidate_InvalidPreset(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Preset = Preset("bogus")
	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if len(e.Error()) > 0 && e.Error()[:6] == "preset" {
			found = true
		}
	}
	if !found {
		t.Errorf("expected preset validation error, got %v", errs)
	}
}

func TestMerge_PresetOverlay(t *testing.T) {
	base := DefaultConfig()
	overlay := DefaultConfig()
	overlay.Preset = PresetMinimal
	merged := Merge(base, overlay)
	if merged.Preset != PresetMinimal {
		t.Errorf("expected merged preset minimal, got %q", merged.Preset)
	}
	if merged.Storage.Search.Semantic {
		t.Error("merged minimal preset should disable semantic")
	}
	// base 不应被修改
	if base.Preset != "" {
		t.Errorf("base preset should remain empty, got %q", base.Preset)
	}
}

func TestCloneConfig_PreservesPreset(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Preset = PresetSemantic
	clone := cloneConfig(cfg)
	if clone.Preset != PresetSemantic {
		t.Errorf("expected cloned preset semantic, got %q", clone.Preset)
	}
}
