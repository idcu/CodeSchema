package codegraph

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	"github.com/idcu/codeschema/internal/errors"
	"github.com/idcu/codeschema/internal/parser"
)

func TestCodeGraphAdapter_Name(t *testing.T) {
	a := NewCodeGraphAdapter("")
	if a.Name() != "codegraph" {
		t.Errorf("expected codegraph, got %s", a.Name())
	}
}

func TestCodeGraphAdapter_Supports(t *testing.T) {
	a := NewCodeGraphAdapter("")
	supported := []string{"go", "java", "ts", "py", "rust", "cpp", "c"}
	unsupported := []string{"ruby", "php", "swift"}

	for _, lang := range supported {
		if !a.Supports(lang) {
			t.Errorf("expected %s to be supported", lang)
		}
	}
	for _, lang := range unsupported {
		if a.Supports(lang) {
			t.Errorf("expected %s to be unsupported", lang)
		}
	}
}

func TestCodeGraphAdapter_Parse_NoDatabase(t *testing.T) {
	a := NewCodeGraphAdapter("/nonexistent/path/db.sqlite")
	ctx := context.Background()

	_, err := a.Parse(ctx, "test.go")
	if err == nil {
		t.Fatal("expected error for missing database")
	}
	if err != errors.ErrSourceUnavailable {
		t.Logf("got error: %v (expected ErrSourceUnavailable)", err)
	}
}

func TestCodeGraphAdapter_Parse_EmptyPath(t *testing.T) {
	a := NewCodeGraphAdapter("")
	ctx := context.Background()

	_, err := a.Parse(ctx, "test.go")
	if err == nil {
		t.Fatal("expected error for empty db path")
	}
}

func TestCodeGraphAdapter_ParseAll_NoDatabase(t *testing.T) {
	a := NewCodeGraphAdapter("/nonexistent/db.sqlite")
	ctx := context.Background()

	_, err := a.ParseAll(ctx, []string{"test.go"})
	if err == nil {
		t.Fatal("expected error for missing database")
	}
}

// setupCodeGraphDB 创建一个符合 CodeGraph 契约（symbols/edges 表）的临时 SQLite 数据库。
func setupCodeGraphDB(t *testing.T, symbols []symbolRow, edges []edgeRow) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "codegraph.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE symbols (name TEXT, qualified_name TEXT, kind TEXT, file_path TEXT, language TEXT)`); err != nil {
		t.Fatalf("create symbols: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE edges (caller TEXT, callee TEXT, type TEXT)`); err != nil {
		t.Fatalf("create edges: %v", err)
	}
	for _, s := range symbols {
		if _, err := db.Exec(`INSERT INTO symbols (name, qualified_name, kind, file_path, language) VALUES (?,?,?,?,?)`,
			s.name, s.qname, s.kind, s.filePath, s.lang); err != nil {
			t.Fatalf("insert symbol: %v", err)
		}
	}
	for _, e := range edges {
		if _, err := db.Exec(`INSERT INTO edges (caller, callee, type) VALUES (?,?,?)`, e.caller, e.callee, e.etype); err != nil {
			t.Fatalf("insert edge: %v", err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return dbPath
}

type symbolRow struct{ name, qname, kind, filePath, lang string }
type edgeRow struct{ caller, callee, etype string }

func TestCodeGraphAdapter_ParseAll_EmptyPaths(t *testing.T) {
	dbPath := setupCodeGraphDB(t, nil, nil) // 有效但空数据库
	a := NewCodeGraphAdapter(dbPath)
	ctx := context.Background()

	ch, err := a.ParseAll(ctx, []string{})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}

	count := 0
	for range ch {
		count++
	}
	if count != 0 {
		t.Errorf("expected 0 docs for empty db, got %d", count)
	}
}

func TestCodeGraphAdapter_ParseAll_RealSymbols(t *testing.T) {
	dbPath := setupCodeGraphDB(t, []symbolRow{
		{"Svc", "pkg.Svc", "CLASS", "repo/svc.go", "go"},
		{"Run", "pkg.Svc.Run", "METHOD", "repo/svc.go", "go"},
		{"Util", "pkg.Util", "CLASS", "repo/util.java", "java"},
	}, []edgeRow{
		{"pkg.Svc.Run", "pkg.Util.Help", "direct"},
	})
	a := NewCodeGraphAdapter(dbPath)
	ctx := context.Background()

	ch, err := a.ParseAll(ctx, []string{"repo/svc.go", "repo/util.java"})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	docs := map[string]*parser.IRDocument{}
	for d := range ch {
		docs[d.FilePath] = d
	}
	if len(docs) != 2 {
		t.Fatalf("expected 2 docs, got %d", len(docs))
	}
	svc := docs["repo/svc.go"]
	if svc == nil || len(svc.Classes) != 2 {
		t.Errorf("svc.go: expected 2 classes, got %d", lenSafe(svc))
	}
	// 调用关系按 caller 前缀归属到 svc.go
	if svc == nil || len(svc.Calls) != 1 {
		t.Errorf("svc.go: expected 1 call, got %d", callsSafe(svc))
	} else if c := svc.Calls[0]; c.CallerFQN != "pkg.Svc.Run" || c.CalleeFQN != "pkg.Util.Help" {
		t.Errorf("call mismatch: %+v", c)
	}
	util := docs["repo/util.java"]
	if util == nil || len(util.Classes) != 1 {
		t.Errorf("util.java: expected 1 class, got %d", lenSafe(util))
	}
}

// TestCodeGraphAdapter_ParseAll_InvalidDB 验证：非 SQLite 文件不再被静默当作空 IR，
// 而是显式返回 ErrSourceUnavailable 触发降级。
func TestCodeGraphAdapter_ParseAll_InvalidDB(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "codegraph.db")
	os.WriteFile(dbPath, []byte("this is not a sqlite db"), 0644)

	a := NewCodeGraphAdapter(dbPath)
	_, err := a.ParseAll(context.Background(), []string{"x.go"})
	if err == nil {
		t.Fatal("expected error for non-sqlite file, got nil")
	}
}

// TestCodeGraphAdapter_ParseAll_MissingTable 验证：缺 symbols/edges 表时显式报错（不静默空 IR）。
func TestCodeGraphAdapter_ParseAll_MissingTable(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "codegraph.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE other (id INTEGER)`); err != nil {
		t.Fatalf("create other: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	a := NewCodeGraphAdapter(dbPath)
	_, err = a.ParseAll(context.Background(), []string{"x.go"})
	if err == nil {
		t.Fatal("expected error for missing symbols/edges tables, got nil")
	}
}

func lenSafe(d *parser.IRDocument) int {
	if d == nil {
		return 0
	}
	return len(d.Classes)
}

func callsSafe(d *parser.IRDocument) int {
	if d == nil {
		return 0
	}
	return len(d.Calls)
}

func TestCodeGraphAdapter_InitClose(t *testing.T) {
	a := NewCodeGraphAdapter("")
	ctx := context.Background()
	if err := a.Init(ctx, nil); err != nil {
		t.Fatalf("Init: %v", err)
	}
	if err := a.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}
}

func TestCodeGraphAdapter_InitWithConfig(t *testing.T) {
	a := NewCodeGraphAdapter("")
	ctx := context.Background()

	if err := a.Init(ctx, map[string]any{"db_path": "/custom/path/db.sqlite"}); err != nil {
		t.Fatalf("Init: %v", err)
	}

	// ParseAll 应检测到路径变化
	_, err := a.ParseAll(ctx, []string{"test.go"})
	if err == nil {
		t.Fatal("expected error since custom path still doesn't exist")
	}
}

func TestGroupByExt(t *testing.T) {
	paths := []string{
		"/path/main.go",
		"/path/util.go",
		"/path/service.java",
		"/path/helper.ts",
		"/path/Makefile",
	}

	groups := groupByExt(paths)
	if len(groups[".go"]) != 2 {
		t.Errorf("expected 2 .go files, got %d", len(groups[".go"]))
	}
	if len(groups[".java"]) != 1 {
		t.Errorf("expected 1 .java file, got %d", len(groups[".java"]))
	}
	if len(groups[".ts"]) != 1 {
		t.Errorf("expected 1 .ts file, got %d", len(groups[".ts"]))
	}
}