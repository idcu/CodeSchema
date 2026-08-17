package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/idcu/codeschema/internal/config"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/vector"
)

// writeFixture 写一个最小 Go 源文件作为待扫描仓库。
func writeFixture(t *testing.T) string {
	t.Helper()
	dir := t.TempDir()
	src := `package fixture

type Calculator struct {
	Name string
}

func (c *Calculator) Add(a, b int) int {
	return a + b
}

func Sub(a, b int) int {
	return a - b
}
`
	p := filepath.Join(dir, "calc.go")
	if err := os.WriteFile(p, []byte(src), 0644); err != nil {
		t.Fatal(err)
	}
	return dir
}

// noNetConfig 返回跳过 ONNX 远程下载的最小配置（EmbeddingModel 置空回退 LocalEmbedder），
// 并把索引目录指到临时目录，避免污染 CWD 的 ./data。
func noNetConfig(t *testing.T, storeDir string) *config.Config {
	t.Helper()
	td := t.TempDir()
	cfg := config.DefaultConfig()
	cfg.Storage.Vector.EmbeddingModel = ""
	cfg.Storage.DSN = storeDir
	cfg.Storage.Search.FTSDir = filepath.Join(td, "fts")
	cfg.Storage.Search.VectorDir = filepath.Join(td, "vector")
	cfg.Storage.Search.IDFDir = filepath.Join(td, "idf")
	return cfg
}

func openFileStore(t *testing.T, dsn string) store.Store {
	t.Helper()
	fs := &store.FileStore{}
	if err := fs.Open(context.Background(), dsn); err != nil {
		t.Fatalf("open store: %v", err)
	}
	return fs
}

func TestNewParserRegistry_NonNil(t *testing.T) {
	cfg := config.DefaultConfig()
	reg := NewParserRegistry(context.Background(), cfg, t.TempDir())
	if reg == nil {
		t.Fatal("NewParserRegistry returned nil")
	}
}

func TestNewSearcherWithStore_NonNil(t *testing.T) {
	cfg := noNetConfig(t, t.TempDir())
	s, b, v := NewSearcherWithStore(cfg)
	if s == nil || b == nil || v == nil {
		t.Fatal("NewSearcherWithStore returned nil component")
	}
}

// TestNewSearcherWithStore_ChromemDriver 验证：显式 driver=chromem 时启用持久化 ChromemStore。
func TestNewSearcherWithStore_ChromemDriver(t *testing.T) {
	cfg := noNetConfig(t, t.TempDir())
	cfg.Storage.Vector.Driver = "chromem"
	cfg.Storage.Vector.DSN = filepath.Join(t.TempDir(), "chromem.db")
	// 默认 EmbeddingModel 已被 noNetConfig 置空 → 回退 LocalEmbedder，无网络依赖
	_, _, v := NewSearcherWithStore(cfg)
	if v == nil {
		t.Fatal("NewSearcherWithStore returned nil vector store")
	}
	if _, ok := v.(*vector.ChromemStore); !ok {
		t.Fatalf("expected *vector.ChromemStore, got %T", v)
	}
}

// TestNewSearcherWithStore_DefaultFileBackend 验证：未配置 driver（默认）走文件 PersistentStore。
func TestNewSearcherWithStore_DefaultFileBackend(t *testing.T) {
	cfg := noNetConfig(t, t.TempDir())
	_, _, v := NewSearcherWithStore(cfg)
	if _, ok := v.(*vector.PersistentStore); !ok {
		t.Fatalf("expected *vector.PersistentStore, got %T", v)
	}
}

// TestBuildRuntime_FullPipeline 验证 scan → 入库 → 装配 → 首轮索引 → 检索 全链路。
func TestBuildRuntime_FullPipeline(t *testing.T) {
	ctx := context.Background()
	repoDir := writeFixture(t)
	storeDir := t.TempDir()
	cfg := noNetConfig(t, storeDir)
	cfg.Project.Root = repoDir

	st := openFileStore(t, storeDir)
	defer st.Close()

	if err := ScanRepository(ctx, st, cfg, repoDir); err != nil {
		t.Fatalf("ScanRepository: %v", err)
	}
	run, err := BuildRuntime(ctx, st, cfg)
	if err != nil {
		t.Fatalf("BuildRuntime: %v", err)
	}
	if run.Svc == nil || run.Searcher == nil || run.Analyzer == nil {
		t.Fatal("runtime Service/Searcher/Analyzer must be non-nil")
	}
	res, err := run.Svc.Search(ctx, "Calculator", "both", 10)
	if err != nil {
		t.Fatalf("Search: %v", err)
	}
	if len(res) == 0 {
		t.Error("expected at least one search result for 'Calculator'")
	}
}
