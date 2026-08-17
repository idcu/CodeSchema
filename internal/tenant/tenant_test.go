package tenant

import (
	"context"
	"path/filepath"
	"slices"
	"testing"

	"github.com/idcu/codeschema/internal/config"
	"github.com/idcu/codeschema/internal/store"
)

// openFileStore 测试用：在 openTarget 目录打开一个 FileStore。
func openFileStore(_ context.Context, _ *config.Config, openTarget string) (store.Store, error) {
	fs := &store.FileStore{}
	if err := fs.Open(context.Background(), openTarget); err != nil {
		return nil, err
	}
	return fs, nil
}

// noNetConfig 返回跳过 ONNX 远程下载的最小配置（EmbeddingModel 置空，
// ResolveFromRegistry 会因模型名空而跳过 github 下载，回退 LocalEmbedder）。
// 同时把索引目录指到临时目录，避免污染 CWD 的 ./data。
func noNetConfig(t *testing.T) *config.Config {
	t.Helper()
	td := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Storage.Vector.EmbeddingModel = ""
	cfg.Storage.DSN = td
	cfg.Storage.Search.FTSDir = filepath.Join(td, "fts")
	cfg.Storage.Search.VectorDir = filepath.Join(td, "vector")
	cfg.Storage.Search.IDFDir = filepath.Join(td, "idf")
	return cfg
}

// TestDeriveIndexDirs_DefaultsFromDSN 验证：未显式配置时按 store 目录派生隔离子目录。
func TestDeriveIndexDirs_DefaultsFromDSN(t *testing.T) {
	s := &config.StorageConfig{Driver: "file", DSN: "/var/lib/cs/mt-a"}
	deriveIndexDirs(s, s.DSN, "", "", "")
	wantFTS := filepath.Join("/var/lib/cs/mt-a", "fts")
	wantVec := filepath.Join("/var/lib/cs/mt-a", "vector")
	wantIDF := filepath.Join("/var/lib/cs/mt-a", "idf")
	if s.Search.FTSDir != wantFTS {
		t.Errorf("FTSDir = %q, want %q", s.Search.FTSDir, wantFTS)
	}
	if s.Search.VectorDir != wantVec {
		t.Errorf("VectorDir = %q, want %q", s.Search.VectorDir, wantVec)
	}
	if s.Search.IDFDir != wantIDF {
		t.Errorf("IDFDir = %q, want %q", s.Search.IDFDir, wantIDF)
	}
}

// TestDeriveIndexDirs_ExplicitOverride 验证：显式设置的目录不被派生覆盖。
// 真实流程中 explicit 值已由 ToConfig 预填进 s.Search.*，deriveIndexDirs 仅在其为空时派生。
func TestDeriveIndexDirs_ExplicitOverride(t *testing.T) {
	s := &config.StorageConfig{Driver: "file", DSN: "/var/lib/cs/mt-a"}
	s.Search.FTSDir = "/custom/fts"   // 模拟 ToConfig 已叠加的显式值
	s.Search.IDFDir = "/custom/idf"
	deriveIndexDirs(s, s.DSN, "/custom/fts", "", "/custom/idf")
	if s.Search.FTSDir != "/custom/fts" {
		t.Errorf("FTSDir = %q, want explicit /custom/fts preserved", s.Search.FTSDir)
	}
	if s.Search.VectorDir != filepath.Join("/var/lib/cs/mt-a", "vector") {
		t.Errorf("VectorDir = %q, want derived (unset)", s.Search.VectorDir)
	}
	if s.Search.IDFDir != "/custom/idf" {
		t.Errorf("IDFDir = %q, want explicit /custom/idf preserved", s.Search.IDFDir)
	}
}

// TestDeriveIndexDirs_NonDirBackend 验证：非目录型后端（如 pg）不派生。
func TestDeriveIndexDirs_NonDirBackend(t *testing.T) {
	s := &config.StorageConfig{Driver: "pg", DSN: "postgres://localhost/cs"}
	deriveIndexDirs(s, s.DSN, "", "", "")
	if s.Search.FTSDir != "" {
		t.Errorf("pg backend should not derive FTSDir, got %q", s.Search.FTSDir)
	}
}

// TestManager_SingleTenantDefault 验证：无 tenants 时退化为单 default 租户。
func TestManager_SingleTenantDefault(t *testing.T) {
	ctx := context.Background()
	cfg := noNetConfig(t)
	m, err := NewManager(ctx, cfg, openFileStore)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	if m.DefaultID() != "default" {
		t.Errorf("DefaultID = %q, want default", m.DefaultID())
	}
	infos := m.List()
	if len(infos) != 1 || infos[0].ID != "default" {
		t.Errorf("List = %+v, want single default tenant", infos)
	}
	svc, err := m.Service(ctx, "")
	if err != nil {
		t.Fatalf("Service(default): %v", err)
	}
	if svc == nil {
		t.Fatal("Service returned nil")
	}
}

// TestManager_MultiTenantRouting 验证：多租户 List/DefaultID/Service 路由正确。
func TestManager_MultiTenantRouting(t *testing.T) {
	ctx := context.Background()
	cfg := noNetConfig(t)
	cfg.Tenants = []config.TenantConfig{
		{ID: "a", Name: "A", Root: "./cmd", Storage: config.StorageConfig{Driver: "file", DSN: t.TempDir()}},
		{ID: "b", Name: "B", Root: "./internal/config", Storage: config.StorageConfig{Driver: "file", DSN: t.TempDir()}},
	}
	m, err := NewManager(ctx, cfg, openFileStore)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	if m.DefaultID() != "a" {
		t.Errorf("DefaultID = %q, want first tenant a", m.DefaultID())
	}
	infos := m.List()
	if len(infos) != 2 || infos[0].ID != "a" || infos[1].ID != "b" {
		t.Errorf("List = %+v, want [a b]", infos)
	}
	sa, err := m.Service(ctx, "a")
	if err != nil || sa == nil {
		t.Errorf("Service(a) = %v, %v", sa, err)
	}
	sb, err := m.Service(ctx, "b")
	if err != nil || sb == nil {
		t.Errorf("Service(b) = %v, %v", sb, err)
	}
	if sa == sb {
		t.Error("Service(a) and Service(b) should be distinct instances")
	}
	// 未知租户应报错
	if _, err := m.Service(ctx, "nope"); err == nil {
		t.Error("Service(unknown) should error")
	}
}

// TestManager_DerivesIndexDirsPerTenant 回归：显式多租户未配置索引目录时，
// 必须按各自 storage.dsn 派生隔离目录（修复前共享 ./data/* 导致索引被覆盖）。
func TestManager_DerivesIndexDirsPerTenant(t *testing.T) {
	ctx := context.Background()
	cfg := noNetConfig(t)
	dsnA := t.TempDir()
	dsnB := t.TempDir()
	// 租户不显式设置 fts/vector/idf，预期按 dsn 派生
	cfg.Tenants = []config.TenantConfig{
		{ID: "a", Storage: config.StorageConfig{Driver: "file", DSN: dsnA}},
		{ID: "b", Storage: config.StorageConfig{Driver: "file", DSN: dsnB}},
	}
	m, err := NewManager(ctx, cfg, openFileStore)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	ca, err := m.Config("a")
	if err != nil {
		t.Fatalf("Config(a): %v", err)
	}
	cb, err := m.Config("b")
	if err != nil {
		t.Fatalf("Config(b): %v", err)
	}
	if ca.Storage.Search.FTSDir != filepath.Join(dsnA, "fts") {
		t.Errorf("tenant a FTSDir = %q, want %q", ca.Storage.Search.FTSDir, filepath.Join(dsnA, "fts"))
	}
	if cb.Storage.Search.FTSDir != filepath.Join(dsnB, "fts") {
		t.Errorf("tenant b FTSDir = %q, want %q", cb.Storage.Search.FTSDir, filepath.Join(dsnB, "fts"))
	}
	if ca.Storage.Search.FTSDir == cb.Storage.Search.FTSDir {
		t.Error("tenant a and b must have distinct FTS index dirs")
	}
}

// multiTenantCfg 返回含 tenants a/b 的多租户配置（各自独立临时 DSN）。
func multiTenantCfg(t *testing.T, ids ...string) *config.Config {
	t.Helper()
	cfg := noNetConfig(t)
	for _, id := range ids {
		cfg.Tenants = append(cfg.Tenants, config.TenantConfig{
			ID:      id,
			Name:    "Tenant-" + id,
			Root:    "./internal/config",
			Storage: config.StorageConfig{Driver: "file", DSN: t.TempDir()},
		})
	}
	return cfg
}

// tenantIDs 返回 Manager.List 的租户 ID 序列。
func tenantIDs(m *Manager) []string {
	infos := m.List()
	out := make([]string, 0, len(infos))
	for _, in := range infos {
		out = append(out, in.ID)
	}
	return out
}

// TestManager_Apply_AddRemoveTenant 验证热重载：新增/移除租户无需重启进程。
func TestManager_Apply_AddRemoveTenant(t *testing.T) {
	ctx := context.Background()

	// 初始：a、b 两个租户
	cfg1 := multiTenantCfg(t, "a", "b")
	m, err := NewManager(ctx, cfg1, openFileStore)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	if got := tenantIDs(m); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("initial tenants = %v, want [a b]", got)
	}

	// 热重载 1：新增 c，移除 b → [a c]
	cfg2 := multiTenantCfg(t, "a", "c")
	if err := m.Apply(ctx, cfg2); err != nil {
		t.Fatalf("Apply(add/remove): %v", err)
	}
	if got := tenantIDs(m); !slices.Equal(got, []string{"a", "c"}) {
		t.Fatalf("after apply tenants = %v, want [a c]", got)
	}
	if _, err := m.Service(ctx, "c"); err != nil {
		t.Errorf("Service(c) after add: %v", err)
	}
	if _, err := m.Service(ctx, "b"); err == nil {
		t.Error("Service(b) after remove should error")
	}

	// 热重载 2：全部移除 → 回退单租户 default（空 tenants 即单项目模式）。
	cfg3 := multiTenantCfg(t)
	if err := m.Apply(ctx, cfg3); err != nil {
		t.Fatalf("Apply(remove all): %v", err)
	}
	if got := tenantIDs(m); !slices.Equal(got, []string{"default"}) {
		t.Fatalf("after remove all tenants = %v, want [default]", got)
	}
}

// TestManager_Apply_UnchangedKept 验证热重载：配置未变化的租户保持原实例。
func TestManager_Apply_UnchangedKept(t *testing.T) {
	ctx := context.Background()
	cfg := multiTenantCfg(t, "a")
	m, err := NewManager(ctx, cfg, openFileStore)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	oldSvc, _ := m.Service(ctx, "a")
	oldRT, _ := m.Runtime("a")

	// 相同配置 Apply：应保持原实例（实例指针不变）。
	if err := m.Apply(ctx, cfg); err != nil {
		t.Fatalf("Apply(same): %v", err)
	}
	newSvc, _ := m.Service(ctx, "a")
	newRT, _ := m.Runtime("a")
	if oldSvc != newSvc {
		t.Error("unchanged tenant Service instance should be kept")
	}
	if oldRT != newRT {
		t.Error("unchanged tenant Runtime instance should be kept")
	}
}

// TestManager_Apply_ChangeTriggersRebuild 验证热重载：DSN 变化触发实例重建。
func TestManager_Apply_ChangeTriggersRebuild(t *testing.T) {
	ctx := context.Background()
	cfg1 := multiTenantCfg(t, "a")
	m, err := NewManager(ctx, cfg1, openFileStore)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()

	oldRT, _ := m.Runtime("a")

	// 改 DSN 的同一租户 a：应重建为新实例。
	cfg2 := multiTenantCfg(t, "a")
	cfg2.Tenants[0].Storage.DSN = t.TempDir()
	if err := m.Apply(ctx, cfg2); err != nil {
		t.Fatalf("Apply(changed): %v", err)
	}
	newRT, _ := m.Runtime("a")
	if oldRT == newRT {
		t.Error("changed tenant (DSN) should be rebuilt to a new instance")
	}
	// 租户 ID 不变，仍在路由表中。
	if got := tenantIDs(m); !slices.Equal(got, []string{"a"}) {
		t.Fatalf("tenants after rebuild = %v, want [a]", got)
	}
}

// TestManager_Apply_ScannerWorkersTriggersRebuild 验证热重载：scanner.workers /
// 旁路限额变化同样触发实例重建（服务级配置热重载补齐，Commit 121）。
func TestManager_Apply_ScannerWorkersTriggersRebuild(t *testing.T) {
	ctx := context.Background()

	// 初始：workers=4（默认）。
	cfg1 := multiTenantCfg(t, "a")
	if cfg1.Scanner.Workers != 4 {
		t.Fatalf("default workers = %d, want 4", cfg1.Scanner.Workers)
	}
	m, err := NewManager(ctx, cfg1, openFileStore)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()
	oldRT, _ := m.Runtime("a")

	// 改 workers：应重建为新实例。
	cfg2 := multiTenantCfg(t, "a")
	cfg2.Scanner.Workers = 8
	if err := m.Apply(ctx, cfg2); err != nil {
		t.Fatalf("Apply(workers changed): %v", err)
	}
	newRT, _ := m.Runtime("a")
	if oldRT == newRT {
		t.Error("changed tenant (scanner.workers) should be rebuilt to a new instance")
	}
	if got, _ := m.Config("a"); got.Scanner.Workers != 8 {
		t.Errorf("tenant a workers = %d, want 8", got.Scanner.Workers)
	}

	// 改旁路限额（line_count_limit）：同样触发重建。
	cfg3 := multiTenantCfg(t, "a")
	cfg3.Scanner.LineCountLimit = 100000
	if err := m.Apply(ctx, cfg3); err != nil {
		t.Fatalf("Apply(line limit changed): %v", err)
	}
	rt3, _ := m.Runtime("a")
	if newRT == rt3 {
		t.Error("changed tenant (scanner.line_count_limit) should be rebuilt")
	}
	if got, _ := m.Config("a"); got.Scanner.LineCountLimit != 100000 {
		t.Errorf("tenant a line_count_limit = %d, want 100000", got.Scanner.LineCountLimit)
	}
}

// TestManager_Apply_SingleToMulti 验证热重载：单租户 default ↔ 显式多租户切换。
func TestManager_Apply_SingleToMulti(t *testing.T) {
	ctx := context.Background()

	// 单租户模式起步。
	cfg1 := noNetConfig(t)
	m, err := NewManager(ctx, cfg1, openFileStore)
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	defer m.Close()
	if m.DefaultID() != "default" {
		t.Fatalf("DefaultID = %q, want default", m.DefaultID())
	}

	// 热重载切换到显式多租户 [a b]。
	cfg2 := multiTenantCfg(t, "a", "b")
	if err := m.Apply(ctx, cfg2); err != nil {
		t.Fatalf("Apply(single→multi): %v", err)
	}
	if got := tenantIDs(m); !slices.Equal(got, []string{"a", "b"}) {
		t.Fatalf("after single→multi tenants = %v, want [a b]", got)
	}
	if m.DefaultID() != "a" {
		t.Errorf("DefaultID = %q, want a", m.DefaultID())
	}

	// 再热重载切回单租户模式。
	if err := m.Apply(ctx, noNetConfig(t)); err != nil {
		t.Fatalf("Apply(multi→single): %v", err)
	}
	if got := tenantIDs(m); !slices.Equal(got, []string{"default"}) {
		t.Fatalf("after multi→single tenants = %v, want [default]", got)
	}
	if m.DefaultID() != "default" {
		t.Errorf("DefaultID = %q, want default", m.DefaultID())
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
