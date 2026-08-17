package tenant

import (
	"context"
	"path/filepath"
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
