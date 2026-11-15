package runtime

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/idcu/codeschema/internal/config"
	lspadapter "github.com/idcu/codeschema/internal/parser/adapter/lsp"
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

// TestNewParserRegistry_LSPGoplsRegistered 真实验证：LSP 启用且 gopls 可解析时，
// registry 对 "go" 语言实际选中 gopls（而非静默降级到 tree-sitter）。
// 这正是 ResolveServerPath 修复前失效的路径（gopls 在 $GOPATH/bin 不在 PATH → 注册被 skip）。
func TestNewParserRegistry_LSPGoplsRegistered(t *testing.T) {
	if lspadapter.ResolveServerPath("gopls") == "" {
		t.Skip("gopls not installed in this environment")
	}
	cfg := config.DefaultConfig()
	cfg.Parser.LSP.Enabled = true
	reg := NewParserRegistry(context.Background(), cfg, t.TempDir())
	p, err := reg.Select("go")
	if err != nil {
		t.Fatalf("Select(go) error: %v", err)
	}
	if p.Name() != "gopls" {
		t.Fatalf("expected gopls selected for go, got %q (LSP 未实际注册?)", p.Name())
	}
}

// TestNewParserRegistry_CodeGraphNotDefault 负控（R5-1）：CodeGraph/JCodeIndexer
// 不得因「配置了非默认 db 路径」就自动进入生产注册表——两个适配器的 Parse 恒返回
// ErrSourceUnavailable（只实现 ParseAll），生产扫描只调 Parse，注册后只是遮蔽
// 真正可用的高精度适配器。必须显式 enabled=true 才注册。
func TestNewParserRegistry_CodeGraphNotDefault(t *testing.T) {
	cfg := config.DefaultConfig()
	// 仅配 db 路径、不配 enabled：旧实现会注册 codegraph 并抢在 scip 前被选中。
	cfg.Parser.CodeGraph.DB = filepath.Join(t.TempDir(), "codegraph.db")
	cfg.Parser.JCodeIndexer.DB = filepath.Join(t.TempDir(), "jcodeindexer.db")

	reg := NewParserRegistry(context.Background(), cfg, t.TempDir())
	p, err := reg.Select("go")
	if err != nil {
		t.Fatalf("Select(go) error: %v", err)
	}
	if p.Name() != "treesitter" {
		t.Fatalf("未显式 enabled 时不应注册 codegraph/jcodeindexer，期望 treesitter 兜底，实际选中 %q", p.Name())
	}
}

// TestNewParserRegistry_CodeGraphEnabledDefaultDB P1 负控：enabled=true 且 db 为默认路径时，
// 适配器必须真正注册。旧实现要求 db 非默认值，导致「只打开开关」的配置静默无效。
func TestNewParserRegistry_CodeGraphEnabledDefaultDB(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)

	cfg := config.DefaultConfig()
	cfg.Parser.CodeGraph.Enabled = true

	reg := NewParserRegistry(context.Background(), cfg, t.TempDir())
	p, err := reg.Select("go")
	if err != nil {
		t.Fatalf("Select(go) error: %v", err)
	}
	if p.Name() != "codegraph" {
		t.Fatalf("enabled=true + 默认 db 时应注册 codegraph，实际选中 %q", p.Name())
	}
}

// TestNewParserRegistry_SCIPBeforeCodeGraph 负控（R5-1）：SCIP 与 CodeGraph 同时
// 可用时（CodeGraph 显式 enabled），go 语言必须选中 scip——SCIP 的 Parse 有实际实现，
// CodeGraph 的 Parse 恒不可用。旧优先级表把 codegraph 排在 scip 前，导致选中的是
// 永远回退 tree-sitter 的适配器。
func TestNewParserRegistry_SCIPBeforeCodeGraph(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.Parser.SCIP.IndexDir = filepath.Join(t.TempDir(), "scipout")
	cfg.Parser.CodeGraph.Enabled = true
	cfg.Parser.CodeGraph.DB = filepath.Join(t.TempDir(), "codegraph.db")

	reg := NewParserRegistry(context.Background(), cfg, t.TempDir())
	p, err := reg.Select("go")
	if err != nil {
		t.Fatalf("Select(go) error: %v", err)
	}
	if p.Name() != "scip" {
		t.Fatalf("SCIP 与 CodeGraph 同时可用时应选 scip，实际选中 %q", p.Name())
	}
}

func TestSearcherWithStore_NonNil(t *testing.T) {
	cfg := noNetConfig(t, t.TempDir())
	s, b, v := newSearcherWithStore(cfg, nil)
	if s == nil || b == nil || v == nil {
		t.Fatal("newSearcherWithStore returned nil component")
	}
}

// TestSearcherWithStore_ChromemDriver 验证：显式 driver=chromem 时启用持久化 ChromemStore。
func TestSearcherWithStore_ChromemDriver(t *testing.T) {
	cfg := noNetConfig(t, t.TempDir())
	cfg.Storage.Vector.Driver = "chromem"
	cfg.Storage.Vector.DSN = filepath.Join(t.TempDir(), "chromem.db")
	// 默认 EmbeddingModel 已被 noNetConfig 置空 → 回退 LocalEmbedder，无网络依赖
	_, _, v := newSearcherWithStore(cfg, nil)
	if v == nil {
		t.Fatal("newSearcherWithStore returned nil vector store")
	}
	if _, ok := v.(*vector.ChromemStore); !ok {
		t.Fatalf("expected *vector.ChromemStore, got %T", v)
	}
}

// TestSearcherWithStore_DefaultFileBackend 验证：未配置 driver（默认）走文件 PersistentStore。
func TestSearcherWithStore_DefaultFileBackend(t *testing.T) {
	cfg := noNetConfig(t, t.TempDir())
	_, _, v := newSearcherWithStore(cfg, nil)
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
