package codegraph

import (
	"context"
	"database/sql"
	"os"
	"strings"
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

// setupRealCodeGraphDB 创建符合「真实 CodeGraph schema」（nodes/edges + source_id/target_id）
// 的临时 SQLite 数据库，模拟 optave/codegraph 等主流实现的真实 DDL。
func setupRealCodeGraphDB(t *testing.T, nodes []realNode, edges []realEdge) string {
	t.Helper()
	dbPath := filepath.Join(t.TempDir(), "codegraph.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// 真实 CodeGraph DDL：nodes 主表（id 外键被 edges 引用）+ edges 关系表
	if _, err := db.Exec(`
		CREATE TABLE nodes (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			name TEXT NOT NULL,
			kind TEXT NOT NULL,
			file TEXT NOT NULL,
			line INTEGER DEFAULT 0,
			end_line INTEGER,
			role TEXT
		);
		CREATE TABLE edges (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			source_id INTEGER NOT NULL,
			target_id INTEGER NOT NULL,
			kind TEXT NOT NULL,
			confidence REAL DEFAULT 1.0,
			FOREIGN KEY(source_id) REFERENCES nodes(id),
			FOREIGN KEY(target_id) REFERENCES nodes(id)
		);
		CREATE INDEX idx_nodes_name ON nodes(name);
		CREATE INDEX idx_nodes_file ON nodes(file);
		CREATE INDEX idx_edges_source ON edges(source_id);
	`); err != nil {
		t.Fatalf("create real schema: %v", err)
	}

	// 插入 nodes，记录 id 以便 edges 引用
	idByName := make(map[string]int64)
	for _, n := range nodes {
		res, err := db.Exec(`INSERT INTO nodes (name, kind, file, line) VALUES (?,?,?,?)`,
			n.name, n.kind, n.file, n.line)
		if err != nil {
			t.Fatalf("insert node %s: %v", n.name, err)
		}
		id, _ := res.LastInsertId()
		idByName[n.name] = id
	}
	for _, e := range edges {
		src, ok1 := idByName[e.source]
		dst, ok2 := idByName[e.target]
		if !ok1 || !ok2 {
			t.Fatalf("edge references unknown node: %s -> %s", e.source, e.target)
		}
		if _, err := db.Exec(`INSERT INTO edges (source_id, target_id, kind) VALUES (?,?,?)`,
			src, dst, e.kind); err != nil {
			t.Fatalf("insert edge %s->%s: %v", e.source, e.target, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	return dbPath
}

type realNode struct {
	name, kind, file string
	line             int
}
type realEdge struct {
	source, target, kind string
}

// TestCodeGraphAdapter_ParseAll_RealSchema 验证：真实 CodeGraph schema（nodes/edges）
// 端到端读取——类/方法/调用关系按文件正确提取。
func TestCodeGraphAdapter_ParseAll_RealSchema(t *testing.T) {
	dbPath := setupRealCodeGraphDB(t, []realNode{
		{"OrderService", "class", "order/service.go", 5},
		{"CreateOrder", "method", "order/service.go", 12},
		{"PaymentService", "class", "payment/service.go", 3},
		{"Charge", "method", "payment/service.go", 9},
		{"OrderController", "class", "web/controller.go", 1},
		{"helper", "function", "util/helper.go", 1},
	}, []realEdge{
		{"CreateOrder", "Charge", "calls"},
		{"OrderController", "OrderService", "references"},
		{"CreateOrder", "helper", "calls"},
	})
	a := NewCodeGraphAdapter(dbPath)
	ctx := context.Background()

	ch, err := a.ParseAll(ctx, []string{"order/service.go", "payment/service.go", "web/controller.go", "util/helper.go"})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	docs := map[string]*parser.IRDocument{}
	for d := range ch {
		docs[d.FilePath] = d
	}
	// 4 个有符号的文件应各产生 1 个文档
	if len(docs) != 4 {
		t.Fatalf("expected 4 docs, got %d", len(docs))
	}

	svc := docs["order/service.go"]
	if svc == nil {
		t.Fatal("missing order/service.go doc")
	}
	if len(svc.Classes) != 1 || svc.Classes[0].Name != "OrderService" || svc.Classes[0].Type != "CLASS" {
		t.Errorf("service.go class mismatch: %+v", svc.Classes)
	}
	if len(svc.Methods) != 1 || svc.Methods[0].Name != "CreateOrder" {
		t.Errorf("service.go method mismatch: %+v", svc.Methods)
	}
	// 调用边按 caller 文件归属：CreateOrder（service.go）→ Charge（payment）与 helper
	if len(svc.Calls) != 2 {
		t.Errorf("service.go expected 2 calls, got %d: %+v", len(svc.Calls), svc.Calls)
	}
	foundCharge := false
	for _, c := range svc.Calls {
		if c.CalleeFQN == "Charge" && c.CallType == "direct" {
			foundCharge = true
		}
	}
	if !foundCharge {
		t.Errorf("expected call to Charge with type direct: %+v", svc.Calls)
	}

	pay := docs["payment/service.go"]
	if pay == nil || len(pay.Classes) != 1 || pay.Classes[0].Name != "PaymentService" {
		t.Errorf("payment doc mismatch: %+v", pay)
	}

	// function 节点不产生类，但文档应存在
	if docs["util/helper.go"] == nil || len(docs["util/helper.go"].Classes) != 0 {
		t.Errorf("helper.go should exist with 0 classes (function only): %+v", docs["util/helper.go"])
	}
}

// TestCodeGraphAdapter_ParseAll_RealSchema_MissingTables 验证：真实 schema 缺表仍显式报错。
func TestCodeGraphAdapter_ParseAll_RealSchema_MissingTables(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "codegraph.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE only_nodes (id INTEGER)`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}
	a := NewCodeGraphAdapter(dbPath)
	_, err = a.ParseAll(context.Background(), []string{"x.go"})
	if err == nil {
		t.Fatal("expected error for missing edges table, got nil")
	}
}

// TestCodeGraphAdapter_DetectSchema 验证 schema 形态检测优先级（真实 nodes/edges 优先）。
func TestCodeGraphAdapter_DetectSchema(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "codegraph.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	if _, err := db.Exec(`CREATE TABLE nodes (id INTEGER); CREATE TABLE edges (id INTEGER);`); err != nil {
		t.Fatalf("create: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	db2, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db2.Close()
	v, err := detectSchema(context.Background(), db2)
	if err != nil {
		t.Fatalf("detectSchema: %v", err)
	}
	if v != schemaReal {
		t.Fatalf("expected schemaReal, got %d", v)
	}
}

// TestCodeGraphAdapter_DetectSchema_Legacy 验证：仅 symbols+edges 时走 legacy 契约。
func TestCodeGraphAdapter_DetectSchema_Legacy(t *testing.T) {
	dbPath := setupCodeGraphDB(t, nil, nil)
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	v, err := detectSchema(context.Background(), db)
	if err != nil {
		t.Fatalf("detectSchema: %v", err)
	}
	if v != schemaLegacy {
		t.Fatalf("expected schemaLegacy, got %d", v)
	}
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
// TestCodeGraphAdapter_ParseAll_KindVariants 验证 CodeGraph kind 变体的类型映射
// （P3_5 未做项：图 Schema 变体兼容覆盖）：interface/enum/trait/module/record
// 应映射为对应 ClassIR.Type，constructor 应映射为方法。
func TestCodeGraphAdapter_ParseAll_KindVariants(t *testing.T) {
	dbPath := setupRealCodeGraphDB(t, []realNode{
		{"ServiceAPI", "interface", "api/service.go", 1},
		{"Color", "enum", "api/color.go", 2},
		{"BaseService", "trait", "api/base.go", 3},
		{"Logger", "module", "api/logger.go", 4},
		{"UserRecord", "record", "api/user.go", 5},
		{"Init", "constructor", "api/service.go", 20},
	}, nil)
	a := NewCodeGraphAdapter(dbPath)
	ctx := context.Background()

	ch, err := a.ParseAll(ctx, []string{"api/service.go", "api/color.go", "api/base.go", "api/logger.go", "api/user.go"})
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}
	docs := map[string]*parser.IRDocument{}
	for d := range ch {
		docs[d.FilePath] = d
	}

	wantType := map[string]string{
		"api/service.go": "INTERFACE",
		"api/color.go":   "ENUM",
		"api/base.go":    "ABSTRACT",
		"api/logger.go":  "OBJECT",
		"api/user.go":    "OBJECT",
	}
	for file, want := range wantType {
		d := docs[file]
		if d == nil || len(d.Classes) != 1 {
			t.Fatalf("%s: expected 1 class, got %+v", file, d)
		}
		if d.Classes[0].Type != want {
			t.Errorf("%s: kind type=%q want %q", file, d.Classes[0].Type, want)
		}
	}
	// constructor → 方法
	svc := docs["api/service.go"]
	if len(svc.Methods) != 1 || svc.Methods[0].Name != "Init" {
		t.Errorf("constructor should map to method: %+v", svc.Methods)
	}
}

// TestCodeGraphAdapter_ParseAll_RealSchema_ColumnDrift 验证 schema 漂移显式报错：
// nodes 表缺 file 列时，适配器必须显式报错（schema drift），不得静默返回空 IR。
func TestCodeGraphAdapter_ParseAll_RealSchema_ColumnDrift(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "codegraph.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open: %v", err)
	}
	// 缺 file 列（未来 CodeGraph 若改名/去列，即触发）
	if _, err := db.Exec(`
		CREATE TABLE nodes (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, kind TEXT, line INTEGER);
		CREATE TABLE edges (id INTEGER PRIMARY KEY AUTOINCREMENT, source_id INTEGER, target_id INTEGER, kind TEXT);
		INSERT INTO nodes (name, kind, line) VALUES ('Service', 'class', 1);
	`); err != nil {
		t.Fatalf("create drifted schema: %v", err)
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	a := NewCodeGraphAdapter(dbPath)
	ch, err := a.ParseAll(context.Background(), []string{"service.go"})
	if err == nil {
		// 若未报错，读 channel 确认没有静默产出
		n := 0
		for range ch {
			n++
		}
		t.Fatalf("expected schema drift error, got %d docs (silent empty result)", n)
	}
	if !strings.Contains(err.Error(), "schema drift") {
		t.Errorf("expected 'schema drift' in error, got %v", err)
	}
}
