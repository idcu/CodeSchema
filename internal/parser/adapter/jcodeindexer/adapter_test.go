package jcodeindexer

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	_ "modernc.org/sqlite"

	cerrors "github.com/idcu/codeschema/internal/errors"
)

// newTestDB 创建含 contract 契约（symbols+edges）的临时 SQLite 库，返回 db 路径与清理函数。
func newTestDB(t *testing.T) (string, func()) {
	t.Helper()
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "jcodeindexer.db")

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatalf("open test db: %v", err)
	}
	defer db.Close()

	const ddl = `
CREATE TABLE symbols (
	name TEXT, qualified_name TEXT, kind TEXT, file_path TEXT,
	signature TEXT, line INTEGER
);
CREATE TABLE edges (
	caller TEXT, callee TEXT, type TEXT
);
INSERT INTO symbols VALUES
	('OrderService', 'com.example.OrderService', 'class', 'OrderService.java', '', 5),
	('createOrder', 'com.example.OrderService.createOrder', 'method', 'OrderService.java', 'createOrder(Order)', 10),
	('UserService', 'com.example.UserService', 'class', 'UserService.kt', '', 3),
	('getUser', 'com.example.UserService.getUser', 'method', 'UserService.kt', 'getUser(Long)', 6);
INSERT INTO edges VALUES
	('com.example.OrderService.createOrder', 'com.example.UserService.getUser', 'calls'),
	('com.example.OrderService', 'com.example.IPayment', 'implements');
`
	if _, err := db.Exec(ddl); err != nil {
		t.Fatalf("seed test db: %v", err)
	}
	return dbPath, func() {}
}

func TestJCodeIndexerAdapter_Supports(t *testing.T) {
	a := NewJCodeIndexerAdapter("")
	for _, lang := range []string{"java", "kotlin", "scala"} {
		if !a.Supports(lang) {
			t.Errorf("expected %s supported", lang)
		}
	}
	if a.Supports("go") {
		t.Error("expected go unsupported (JVM-focused)")
	}
}

func TestJCodeIndexerAdapter_ParseAll_ReadsSymbolsAndEdges(t *testing.T) {
	dbPath, cleanup := newTestDB(t)
	defer cleanup()

	a := NewJCodeIndexerAdapter(dbPath)
	ch, err := a.ParseAll(context.Background(), nil)
	if err != nil {
		t.Fatalf("ParseAll: %v", err)
	}

	type docAgg struct {
		classes int
		methods int
		calls   int
		lang    string
	}
	got := map[string]*docAgg{}
	for d := range ch {
		agg := got[d.FilePath]
		if agg == nil {
			agg = &docAgg{lang: d.Language}
			got[d.FilePath] = agg
		}
		agg.classes += len(d.Classes)
		agg.methods += len(d.Methods)
		agg.calls += len(d.Calls)
	}

	if len(got) != 2 {
		t.Fatalf("expected 2 files, got %d: %+v", len(got), got)
	}
	java := got["OrderService.java"]
	if java == nil || java.classes != 1 || java.methods != 1 {
		t.Fatalf("OrderService.java: %+v", java)
	}
	if java.lang != "java" {
		t.Errorf("OrderService.java language = %q, want java", java.lang)
	}
	kotlin := got["UserService.kt"]
	if kotlin == nil || kotlin.classes != 1 || kotlin.methods != 1 {
		t.Fatalf("UserService.kt: %+v", kotlin)
	}
	if kotlin.lang != "kotlin" {
		t.Errorf("UserService.kt language = %q, want kotlin", kotlin.lang)
	}
}

func TestJCodeIndexerAdapter_ParseAll_MissingDB_Degrades(t *testing.T) {
	a := NewJCodeIndexerAdapter(filepath.Join(t.TempDir(), "nope.db"))
	_, err := a.ParseAll(context.Background(), nil)
	if !errors.Is(err, cerrors.ErrSourceUnavailable) {
		t.Fatalf("err = %v, want ErrSourceUnavailable", err)
	}
}

func TestJCodeIndexerAdapter_ParseAll_SchemaMismatch_Degrades(t *testing.T) {
	dir := t.TempDir()
	dbPath := filepath.Join(dir, "other.db")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE unrelated (id INTEGER);`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	a := NewJCodeIndexerAdapter(dbPath)
	_, err = a.ParseAll(context.Background(), nil)
	if !errors.Is(err, cerrors.ErrSourceUnavailable) {
		t.Fatalf("err = %v, want ErrSourceUnavailable (schema contract unmet)", err)
	}
}
