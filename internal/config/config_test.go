package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestDefaultConfig(t *testing.T) {
	cfg := DefaultConfig()

	if cfg.Project.Root != "." {
		t.Errorf("expected default root '.', got %q", cfg.Project.Root)
	}
	if cfg.Storage.Driver != "file" {
		t.Errorf("expected default storage driver 'file', got %q", cfg.Storage.Driver)
	}
	// 默认 EmbeddingModel 必须与真实本地制品 / 注册表键一致（bge-small-zh-v1.5），
	// 否则离线环境无法命中本地模型而静默降级到 LocalEmbedder（已知问题 #6）。
	if cfg.Storage.Vector.EmbeddingModel != "bge-small-zh-v1.5" {
		t.Errorf("expected default embedding_model 'bge-small-zh-v1.5', got %q", cfg.Storage.Vector.EmbeddingModel)
	}
	if cfg.Server.MCPAddr != ":8080" {
		t.Errorf("expected default mcp addr ':8080', got %q", cfg.Server.MCPAddr)
	}
	if cfg.Server.HTTPAddr != ":8081" {
		t.Errorf("expected default http addr ':8081', got %q", cfg.Server.HTTPAddr)
	}
	if cfg.Scanner.Workers != 4 {
		t.Errorf("expected default workers 4, got %d", cfg.Scanner.Workers)
	}
	if cfg.Watcher.DebounceMs != 300 {
		t.Errorf("expected default debounce_ms 300, got %d", cfg.Watcher.DebounceMs)
	}
	if cfg.Watcher.Enabled != true {
		t.Errorf("expected watcher enabled=true by default")
	}
	if len(cfg.Project.Languages) != 6 {
		t.Errorf("expected 6 default languages, got %d: %v", len(cfg.Project.Languages), cfg.Project.Languages)
	}
	if len(cfg.Parser.Adapters) != 1 || cfg.Parser.Adapters[0] != "treesitter" {
		t.Errorf("expected default adapters [treesitter], got %v", cfg.Parser.Adapters)
	}
}

func TestLoad_EmptyPath(t *testing.T) {
	cfg, err := Load("")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
	if cfg.Scanner.Workers != 4 {
		t.Errorf("expected default workers 4, got %d", cfg.Scanner.Workers)
	}
}

func TestLoad_NonExistentFile(t *testing.T) {
	cfg, err := Load("./nonexistent_config_file.yaml")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if cfg == nil {
		t.Fatal("expected non-nil config")
	}
}

func TestLoad_YAML(t *testing.T) {
	yamlContent := `
project:
  name: myrepo
  root: /home/user/repo
  languages: [go, java, rust]

storage:
  driver: sqlite
  dsn: /data/codeschema.db
  kv: redis://localhost:6379/0
  vector:
    driver: chromem
    dsn: /data/vector.db
    embedding_model: bge-large-zh
  search:
    fts: false
    semantic: true

parser:
  adapters: [treesitter, codegraph]
  scip:
    index_dir: /data/scipout
  codegraph:
    db: /data/codegraph.db
  jcodeindexer:
    db: /data/jci.db
    config_file: /data/.jindexer/config.yaml
    env:
      INDEXER_EMBEDDING_ENABLED: "true"
      LOG_LEVEL: debug

ai:
  provider: anthropic
  model: claude-3-opus
  budget_per_scan: 200
  budget_per_query: 20

server:
  mcp_addr: ":9090"
  http_addr: ":9091"
  auth_token: my-secret-token

watcher:
  enabled: false
  debounce_ms: 500
  ignore_dirs: [.git, node_modules, dist]
  batch_size: 100

scanner:
  workers: 8
  file_size_limit_mb: 20
  line_count_limit: 100000
`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// Project
	if cfg.Project.Name != "myrepo" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "myrepo")
	}
	if cfg.Project.Root != "/home/user/repo" {
		t.Errorf("project.root = %q, want %q", cfg.Project.Root, "/home/user/repo")
	}
	if len(cfg.Project.Languages) != 3 || cfg.Project.Languages[0] != "go" {
		t.Errorf("project.languages = %v, want [go java rust]", cfg.Project.Languages)
	}

	// Storage
	if cfg.Storage.Driver != "sqlite" {
		t.Errorf("storage.driver = %q, want %q", cfg.Storage.Driver, "sqlite")
	}
	if cfg.Storage.DSN != "/data/codeschema.db" {
		t.Errorf("storage.dsn = %q, want %q", cfg.Storage.DSN, "/data/codeschema.db")
	}
	if cfg.Storage.KV != "redis://localhost:6379/0" {
		t.Errorf("storage.kv = %q, want %q", cfg.Storage.KV, "redis://localhost:6379/0")
	}
	if cfg.Storage.Vector.Driver != "chromem" {
		t.Errorf("storage.vector.driver = %q, want %q", cfg.Storage.Vector.Driver, "chromem")
	}
	if cfg.Storage.Vector.EmbeddingModel != "bge-large-zh" {
		t.Errorf("storage.vector.embedding_model = %q, want %q", cfg.Storage.Vector.EmbeddingModel, "bge-large-zh")
	}
	if cfg.Storage.Search.FTS != false {
		t.Errorf("storage.search.fts = %v, want %v", cfg.Storage.Search.FTS, false)
	}
	if cfg.Storage.Search.Semantic != true {
		t.Errorf("storage.search.semantic = %v, want %v", cfg.Storage.Search.Semantic, true)
	}

	// Parser
	if len(cfg.Parser.Adapters) != 2 || cfg.Parser.Adapters[0] != "treesitter" || cfg.Parser.Adapters[1] != "codegraph" {
		t.Errorf("parser.adapters = %v, want [treesitter codegraph]", cfg.Parser.Adapters)
	}
	if cfg.Parser.SCIP.IndexDir != "/data/scipout" {
		t.Errorf("parser.scip.index_dir = %q, want %q", cfg.Parser.SCIP.IndexDir, "/data/scipout")
	}
	if cfg.Parser.CodeGraph.DB != "/data/codegraph.db" {
		t.Errorf("parser.codegraph.db = %q, want %q", cfg.Parser.CodeGraph.DB, "/data/codegraph.db")
	}
	if cfg.Parser.JCodeIndexer.DB != "/data/jci.db" {
		t.Errorf("parser.jcodeindexer.db = %q, want %q", cfg.Parser.JCodeIndexer.DB, "/data/jci.db")
	}
	if cfg.Parser.JCodeIndexer.Env["INDEXER_EMBEDDING_ENABLED"] != "true" {
		t.Errorf("parser.jcodeindexer.env.INDEXER_EMBEDDING_ENABLED = %q, want %q", cfg.Parser.JCodeIndexer.Env["INDEXER_EMBEDDING_ENABLED"], "true")
	}
	if cfg.Parser.JCodeIndexer.Env["LOG_LEVEL"] != "debug" {
		t.Errorf("parser.jcodeindexer.env.LOG_LEVEL = %q, want %q", cfg.Parser.JCodeIndexer.Env["LOG_LEVEL"], "debug")
	}

	// AI
	if cfg.AI.Provider != "anthropic" {
		t.Errorf("ai.provider = %q, want %q", cfg.AI.Provider, "anthropic")
	}
	if cfg.AI.Model != "claude-3-opus" {
		t.Errorf("ai.model = %q, want %q", cfg.AI.Model, "claude-3-opus")
	}
	if cfg.AI.BudgetPerScan != 200 {
		t.Errorf("ai.budget_per_scan = %d, want %d", cfg.AI.BudgetPerScan, 200)
	}
	if cfg.AI.BudgetPerQuery != 20 {
		t.Errorf("ai.budget_per_query = %d, want %d", cfg.AI.BudgetPerQuery, 20)
	}

	// Server
	if cfg.Server.MCPAddr != ":9090" {
		t.Errorf("server.mcp_addr = %q, want %q", cfg.Server.MCPAddr, ":9090")
	}
	if cfg.Server.HTTPAddr != ":9091" {
		t.Errorf("server.http_addr = %q, want %q", cfg.Server.HTTPAddr, ":9091")
	}
	if cfg.Server.AuthToken != "my-secret-token" {
		t.Errorf("server.auth_token = %q, want %q", cfg.Server.AuthToken, "my-secret-token")
	}

	// Watcher
	if cfg.Watcher.Enabled != false {
		t.Errorf("watcher.enabled = %v, want %v", cfg.Watcher.Enabled, false)
	}
	if cfg.Watcher.DebounceMs != 500 {
		t.Errorf("watcher.debounce_ms = %d, want %d", cfg.Watcher.DebounceMs, 500)
	}
	if len(cfg.Watcher.IgnoreDirs) != 3 {
		t.Errorf("watcher.ignore_dirs length = %d, want 3", len(cfg.Watcher.IgnoreDirs))
	}
	if cfg.Watcher.BatchSize != 100 {
		t.Errorf("watcher.batch_size = %d, want %d", cfg.Watcher.BatchSize, 100)
	}

	// Scanner
	if cfg.Scanner.Workers != 8 {
		t.Errorf("scanner.workers = %d, want %d", cfg.Scanner.Workers, 8)
	}
	if cfg.Scanner.FileSizeLimitMB != 20 {
		t.Errorf("scanner.file_size_limit_mb = %d, want %d", cfg.Scanner.FileSizeLimitMB, 20)
	}
	if cfg.Scanner.LineCountLimit != 100000 {
		t.Errorf("scanner.line_count_limit = %d, want %d", cfg.Scanner.LineCountLimit, 100000)
	}
}

func TestLoad_JSON(t *testing.T) {
	jsonContent := `{
		"project": { "name": "json-repo", "root": "/json", "languages": ["go", "java"] },
		"server": { "mcp_addr": ":9090", "http_addr": ":9091" },
		"scanner": { "workers": 16 }
	}`

	dir := t.TempDir()
	path := filepath.Join(dir, "config.json")
	if err := os.WriteFile(path, []byte(jsonContent), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Project.Name != "json-repo" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "json-repo")
	}
	if cfg.Project.Root != "/json" {
		t.Errorf("project.root = %q, want %q", cfg.Project.Root, "/json")
	}
	if cfg.Scanner.Workers != 16 {
		t.Errorf("scanner.workers = %d, want %d", cfg.Scanner.Workers, 16)
	}
	// Default should be preserved for non-overridden fields
	if cfg.Storage.Driver != "file" {
		t.Errorf("storage.driver should be default 'file', got %q", cfg.Storage.Driver)
	}
}

func TestLoad_PartialYAML(t *testing.T) {
	yamlContent := `
project:
  name: partial-repo

scanner:
  workers: 2
`

	dir := t.TempDir()
	path := filepath.Join(dir, "partial.yaml")
	if err := os.WriteFile(path, []byte(yamlContent), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}

	cfg, err := Load(path)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if cfg.Project.Name != "partial-repo" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "partial-repo")
	}
	// Default root should be preserved
	if cfg.Project.Root != "." {
		t.Errorf("project.root should be default '.', got %q", cfg.Project.Root)
	}
	// Default languages should be preserved
	if len(cfg.Project.Languages) != 6 {
		t.Errorf("project.languages should have 6 defaults, got %d", len(cfg.Project.Languages))
	}
	if cfg.Scanner.Workers != 2 {
		t.Errorf("scanner.workers = %d, want %d", cfg.Scanner.Workers, 2)
	}
	// Default servers should be preserved
	if cfg.Server.MCPAddr != ":8080" {
		t.Errorf("server.mcp_addr should be default ':8080', got %q", cfg.Server.MCPAddr)
	}
}

func TestLoad_UnsupportedFormat(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.toml")
	if err := os.WriteFile(path, []byte("key = value"), 0644); err != nil {
		t.Fatalf("write config: %v", err)
	}
	_, err := Load(path)
	if err == nil {
		t.Fatal("expected error for unsupported format")
	}
}

func TestValidate_Valid(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Project.Root = "/test"
	cfg.Storage.DSN = "./data"

	errs := Validate(cfg)
	if len(errs) > 0 {
		t.Errorf("expected no validation errors, got %v", errs)
	}
}

func TestValidate_EmptyRoot(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Project.Root = ""

	errs := Validate(cfg)
	if len(errs) == 0 {
		t.Fatal("expected validation errors")
	}
	found := false
	for _, e := range errs {
		if e.Error() == "project.root must not be empty" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about empty root, got %v", errs)
	}
}

func TestValidate_EmptyDSN(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Project.Root = "/test"
	cfg.Storage.DSN = ""

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if e.Error() == "storage.dsn must not be empty" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about empty dsn, got %v", errs)
	}
}

func TestValidate_InvalidWorkers(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Project.Root = "/test"
	cfg.Storage.DSN = "./data"
	cfg.Scanner.Workers = 0

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if e.Error() == "scanner.workers must be > 0" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about workers, got %v", errs)
	}
}

func TestValidate_NoAddr(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Project.Root = "/test"
	cfg.Storage.DSN = "./data"
	cfg.Server.MCPAddr = ""
	cfg.Server.HTTPAddr = ""

	errs := Validate(cfg)
	found := false
	for _, e := range errs {
		if e.Error() == "at least one of server.mcp_addr or server.http_addr must be set" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected error about missing addr, got %v", errs)
	}
}

func TestScannerConfig_DefaultsPreserved(t *testing.T) {
	// Ensure that when only scanner.workers is set, other fields keep defaults
	cfg := DefaultConfig()
	cfg.Scanner.Workers = 8
	cfg.Scanner.FileSizeLimitMB = 20
	// LineCountLimit should remain default
	if cfg.Scanner.LineCountLimit != 50000 {
		t.Errorf("line_count_limit should be default 50000, got %d", cfg.Scanner.LineCountLimit)
	}
}

// ---------------------------------------------------------------------------
// P9: 多配置源支持测试
// ---------------------------------------------------------------------------

func TestLoadFromEnv(t *testing.T) {
	cfg := DefaultConfig()

	// 设置环境变量
	os.Setenv("CODESCHEMA_PROJECT_ROOT", "/test/root")
	os.Setenv("CODESCHEMA_PROJECT_NAME", "test-repo")
	os.Setenv("CODESCHEMA_STORAGE_DRIVER", "sqlite")
	os.Setenv("CODESCHEMA_SERVER_MCP_ADDR", ":9090")
	os.Setenv("CODESCHEMA_SCANNER_WORKERS", "8")
	os.Setenv("CODESCHEMA_WATCHER_DEBOUNCE_MS", "500")
	os.Setenv("CODESCHEMA_AI_BUDGET_PER_SCAN", "200")
	os.Setenv("CODESCHEMA_WATCHER_ENABLED", "false")
	defer func() {
		os.Unsetenv("CODESCHEMA_PROJECT_ROOT")
		os.Unsetenv("CODESCHEMA_PROJECT_NAME")
		os.Unsetenv("CODESCHEMA_STORAGE_DRIVER")
		os.Unsetenv("CODESCHEMA_SERVER_MCP_ADDR")
		os.Unsetenv("CODESCHEMA_SCANNER_WORKERS")
		os.Unsetenv("CODESCHEMA_WATCHER_DEBOUNCE_MS")
		os.Unsetenv("CODESCHEMA_AI_BUDGET_PER_SCAN")
		os.Unsetenv("CODESCHEMA_WATCHER_ENABLED")
	}()

	LoadFromEnv(cfg)

	if cfg.Project.Root != "/test/root" {
		t.Errorf("project.root = %q, want %q", cfg.Project.Root, "/test/root")
	}
	if cfg.Project.Name != "test-repo" {
		t.Errorf("project.name = %q, want %q", cfg.Project.Name, "test-repo")
	}
	if cfg.Storage.Driver != "sqlite" {
		t.Errorf("storage.driver = %q, want %q", cfg.Storage.Driver, "sqlite")
	}
	if cfg.Server.MCPAddr != ":9090" {
		t.Errorf("server.mcp_addr = %q, want %q", cfg.Server.MCPAddr, ":9090")
	}
	if cfg.Scanner.Workers != 8 {
		t.Errorf("scanner.workers = %d, want %d", cfg.Scanner.Workers, 8)
	}
	if cfg.Watcher.DebounceMs != 500 {
		t.Errorf("watcher.debounce_ms = %d, want %d", cfg.Watcher.DebounceMs, 500)
	}
	if cfg.AI.BudgetPerScan != 200 {
		t.Errorf("ai.budget_per_scan = %d, want %d", cfg.AI.BudgetPerScan, 200)
	}
	if cfg.Watcher.Enabled != false {
		t.Errorf("watcher.enabled = %v, want %v", cfg.Watcher.Enabled, false)
	}
}

func TestLoadFromEnv_InvalidInt(t *testing.T) {
	cfg := DefaultConfig()
	origWorkers := cfg.Scanner.Workers

	os.Setenv("CODESCHEMA_SCANNER_WORKERS", "not-a-number")
	defer os.Unsetenv("CODESCHEMA_SCANNER_WORKERS")

	LoadFromEnv(cfg)

	// Should keep original value
	if cfg.Scanner.Workers != origWorkers {
		t.Errorf("scanner.workers should remain %d, got %d", origWorkers, cfg.Scanner.Workers)
	}
}

func TestMerge_BaseNil(t *testing.T) {
	overlay := &Config{
		Project: ProjectConfig{
			Name: "test",
			Root: "/test",
		},
	}
	merged := Merge(nil, overlay)
	if merged.Project.Name != "test" {
		t.Errorf("merged.project.name = %q, want %q", merged.Project.Name, "test")
	}
	if merged.Project.Root != "/test" {
		t.Errorf("merged.project.root = %q, want %q", merged.Project.Root, "/test")
	}
	// Defaults should be preserved for other fields
	if merged.Scanner.Workers != 4 {
		t.Errorf("merged.scanner.workers should be default 4, got %d", merged.Scanner.Workers)
	}
}

func TestMerge_OverlayNil(t *testing.T) {
	base := DefaultConfig()
	merged := Merge(base, nil)
	if merged.Scanner.Workers != base.Scanner.Workers {
		t.Errorf("merged.scanner.workers = %d, want %d", merged.Scanner.Workers, base.Scanner.Workers)
	}
}

func TestMerge_FullOverlay(t *testing.T) {
	base := DefaultConfig()
	overlay := &Config{
		Project: ProjectConfig{
			Name:      "overlaid",
			Root:      "/overlaid/root",
			Languages: []string{"go", "java"},
		},
		Storage: StorageConfig{
			Driver: "sqlite",
			DSN:    "/data/db.sqlite",
			Search: SearchConfig{
				FTS:    false,
				IDFDir: "/custom/idf",
			},
		},
		Server: ServerConfig{
			MCPAddr:  ":9090",
			HTTPAddr: ":9091",
		},
		Scanner: ScannerConfig{
			Workers: 16,
		},
	}

	merged := Merge(base, overlay)

	if merged.Project.Name != "overlaid" {
		t.Errorf("project.name = %q, want %q", merged.Project.Name, "overlaid")
	}
	if merged.Project.Root != "/overlaid/root" {
		t.Errorf("project.root = %q, want %q", merged.Project.Root, "/overlaid/root")
	}
	if len(merged.Project.Languages) != 2 {
		t.Errorf("project.languages length = %d, want 2", len(merged.Project.Languages))
	}
	if merged.Storage.Driver != "sqlite" {
		t.Errorf("storage.driver = %q, want %q", merged.Storage.Driver, "sqlite")
	}
	if merged.Storage.DSN != "/data/db.sqlite" {
		t.Errorf("storage.dsn = %q, want %q", merged.Storage.DSN, "/data/db.sqlite")
	}
	// FTS 为 false 是零值，Merge 策略不覆盖（零值 false 不覆盖），保留默认值 true
	if merged.Storage.Search.FTS != true {
		t.Errorf("storage.search.fts = %v, want %v (default preserved, zero-value false not overridden)", merged.Storage.Search.FTS, true)
	}
	if merged.Storage.Search.IDFDir != "/custom/idf" {
		t.Errorf("storage.search.idf_dir = %q, want %q", merged.Storage.Search.IDFDir, "/custom/idf")
	}
	if merged.Server.MCPAddr != ":9090" {
		t.Errorf("server.mcp_addr = %q, want %q", merged.Server.MCPAddr, ":9090")
	}
	if merged.Server.HTTPAddr != ":9091" {
		t.Errorf("server.http_addr = %q, want %q", merged.Server.HTTPAddr, ":9091")
	}
	if merged.Scanner.Workers != 16 {
		t.Errorf("scanner.workers = %d, want %d", merged.Scanner.Workers, 16)
	}
	// Defaults preserved
	if merged.Watcher.DebounceMs != 300 {
		t.Errorf("watcher.debounce_ms should be default 300, got %d", merged.Watcher.DebounceMs)
	}
}

func TestMerge_PartialOverlay(t *testing.T) {
	base := DefaultConfig()
	overlay := &Config{
		Scanner: ScannerConfig{
			Workers: 8,
		},
	}

	merged := Merge(base, overlay)

	if merged.Scanner.Workers != 8 {
		t.Errorf("scanner.workers = %d, want %d", merged.Scanner.Workers, 8)
	}
	if merged.Scanner.FileSizeLimitMB != base.Scanner.FileSizeLimitMB {
		t.Errorf("scanner.file_size_limit_mb should preserve default %d, got %d", base.Scanner.FileSizeLimitMB, merged.Scanner.FileSizeLimitMB)
	}
	if merged.Scanner.LineCountLimit != base.Scanner.LineCountLimit {
		t.Errorf("scanner.line_count_limit should preserve default %d, got %d", base.Scanner.LineCountLimit, merged.Scanner.LineCountLimit)
	}
}

func TestCloneConfig_DeepCopy(t *testing.T) {
	original := DefaultConfig()
	clone := cloneConfig(original)

	if clone == original {
		t.Fatal("clone should be a different pointer")
	}

	// Modify clone, original should not change
	clone.Project.Name = "modified"
	if original.Project.Name == "modified" {
		t.Error("original should not be modified when clone changes")
	}
}

func TestConfigWatcher_New(t *testing.T) {
	cfg := DefaultConfig()
	cw := NewConfigWatcher("/tmp/nonexistent.yaml", cfg, nil)

	if cw.GetConfig() != cfg {
		t.Error("GetConfig should return the initial config")
	}
}
