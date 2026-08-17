package agentbench

import (
	"context"
	"os"
	"path/filepath"

	"github.com/idcu/codeschema/internal/parser"
	adapter "github.com/idcu/codeschema/internal/parser/adapter/treesitter"
	"github.com/idcu/codeschema/internal/scanner"
	"github.com/idcu/codeschema/internal/search"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/vector"
)

// components 一次 agent-bench 运行的组件集合（与 internal/benchmark 同构，
// 不依赖 testing.TB，纯生产代码）。
type components struct {
	store    store.Store
	scanner  *scanner.Scanner
	builder  *search.IndexBuilder
	searcher *search.Searcher
	storeDir string
}

// newComponents 创建组件：临时目录 + FileStore + tree-sitter + 内存 FTS/向量。
func newComponents(ctx context.Context, workers int) (*components, error) {
	st := store.NewStore("file")
	storeDir, err := os.MkdirTemp("", "codeschema-agentbench-*")
	if err != nil {
		return nil, err
	}
	if err := st.Open(ctx, storeDir); err != nil {
		return nil, err
	}

	reg := parser.NewRegistry()
	reg.Register(adapter.NewTreeSitterAdapter())
	sc := scanner.NewScanner(st, reg, workers)

	fts := search.NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	builder := search.NewIndexBuilder(fts, idx, em)
	searcher := search.NewSearcher(fts, search.NewVectorAdapter(idx), nil)

	return &components{
		store:    st,
		scanner:  sc,
		builder:  builder,
		searcher: searcher,
		storeDir: storeDir,
	}, nil
}

// close 关闭组件并清理临时目录。
func (c *components) close() {
	if c.store != nil {
		_ = c.store.Close()
	}
	if c.storeDir != "" {
		_ = os.RemoveAll(c.storeDir)
	}
}

// countSourceFiles 统计仓库源码文件数（口径与 scanner.listFiles 一致：
// 忽略 .git/vendor/node_modules 等依赖目录）。
func countSourceFiles(root string) int {
	count := 0
	_ = filepath.Walk(root, func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return nil
		}
		if info.IsDir() {
			switch info.Name() {
			case ".git", "node_modules", "target", "build", "vendor", ".idea", ".vscode", "__pycache__":
				return filepath.SkipDir
			}
			return nil
		}
		// 只统计可识别源码（与 scanner.detectLang 一致口径）。
		ext := filepath.Ext(path)
		if ext == ".go" || ext == ".java" || ext == ".py" || ext == ".ts" || ext == ".tsx" ||
			ext == ".js" || ext == ".jsx" || ext == ".rs" || ext == ".cpp" || ext == ".cc" ||
			ext == ".c" || ext == ".h" || ext == ".hpp" || ext == ".kt" || ext == ".swift" ||
			ext == ".php" || ext == ".cs" || ext == ".rb" || ext == ".sh" || ext == ".scala" ||
			ext == ".sql" || ext == ".lua" || ext == ".css" || ext == ".toml" || ext == ".yaml" ||
			ext == ".yml" || ext == ".proto" || ext == ".html" || ext == ".md" {
			count++
		}
		return nil
	})
	return count
}
