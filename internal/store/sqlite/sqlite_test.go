package sqlite

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
)

// newTestStore 创建临时目录下的 SQLiteStore，测试结束后清理。
func newTestStore(t *testing.T) *SQLiteStore {
	t.Helper()
	dir := t.TempDir()
	s := NewSQLiteStore()
	if err := s.Open(context.Background(), dir); err != nil {
		t.Fatalf("open: %v", err)
	}
	t.Cleanup(func() { _ = s.Close() })
	return s
}

func TestSQLite_UpsertFileAndGet(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, err := s.UpsertFile(ctx, "/repo/a.go", "hash1", 100, 2048)
	if err != nil {
		t.Fatalf("upsert file: %v", err)
	}
	if id <= 0 {
		t.Fatalf("expected positive id, got %d", id)
	}

	// 更新应返回同一 id
	id2, err := s.UpsertFile(ctx, "/repo/a.go", "hash2", 120, 3000)
	if err != nil {
		t.Fatalf("upsert file again: %v", err)
	}
	if id2 != id {
		t.Fatalf("upsert should keep id, got %d want %d", id2, id)
	}

	byPath, err := s.GetFileByPath(ctx, "/repo/a.go")
	if err != nil || byPath == nil {
		t.Fatalf("get by path: %v %v", err, byPath)
	}
	if byPath.ContentHash != "hash2" || byPath.LineCount != 120 {
		t.Fatalf("stale data: %+v", byPath)
	}
	byID, err := s.GetFileByID(ctx, id)
	if err != nil || byID == nil || byID.AbsolutePath != "/repo/a.go" {
		t.Fatalf("get by id: %v %v", err, byID)
	}
}

func TestSQLite_UpsertIRAndQuery(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	ir := &parser.IRDocument{
		Source:    "treesitter",
		Language:  "go",
		FilePath:  "/repo/svc.go",
		FileHash:  "h",
		LineCount: 50,
		ByteSize:  1024,
		Imports:   []string{"fmt", "context"},
		Classes: []parser.ClassIR{
			{Name: "Service", FullName: "pkg.Service", Type: "CLASS", ParentFQNs: []string{"pkg.Base"}, StartLine: 1, EndLine: 20},
		},
		Methods: []parser.MethodIR{
			{Name: "Run", ClassFQN: "pkg.Service", Signature: "Run() error", ReturnType: "error", StartLine: 3, EndLine: 10},
		},
		Calls: []parser.CallIR{
			{CallerFQN: "pkg.Service.Run", CalleeFQN: "fmt.Println", CallType: "direct", LineNumber: 5},
		},
	}
	if err := s.UpsertIR(ctx, ir); err != nil {
		t.Fatalf("upsert ir: %v", err)
	}

	files, err := s.GetAllFiles(ctx)
	if err != nil || len(files) != 1 {
		t.Fatalf("get all files: %v len=%d", err, len(files))
	}
	if files[0].Imports[0] != "fmt" {
		t.Fatalf("imports not stored: %+v", files[0])
	}

	classes, err := s.GetClassesByFileID(ctx, files[0].ID)
	if err != nil || len(classes) != 1 {
		t.Fatalf("get classes: %v len=%d", err, len(classes))
	}
	if classes[0].FullName != "pkg.Service" || len(classes[0].ParentFQNs) != 1 {
		t.Fatalf("class data wrong: %+v", classes[0])
	}

	methods, err := s.GetMethodsByClassID(ctx, classes[0].ID)
	if err != nil || len(methods) != 1 {
		t.Fatalf("get methods: %v len=%d", err, len(methods))
	}
	if methods[0].Name != "Run" || methods[0].ReturnType != "error" {
		t.Fatalf("method data wrong: %+v", methods[0])
	}

	calls, err := s.GetCallsByFileID(ctx, files[0].ID)
	if err != nil || len(calls) != 1 {
		t.Fatalf("get calls: %v len=%d", err, len(calls))
	}
	if calls[0].CalleeFQN != "fmt.Println" {
		t.Fatalf("call data wrong: %+v", calls[0])
	}
}

func TestSQLite_TagsAndSearch(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, _ := s.UpsertFile(ctx, "/repo/x.go", "h", 10, 100)
	_ = s.UpsertClasses(ctx, id, []parser.ClassIR{{Name: "X", FullName: "pkg.X", Type: "CLASS"}})
	classes, _ := s.GetClassesByFileID(ctx, id)
	if len(classes) != 1 {
		t.Fatalf("need 1 class")
	}
	cid := classes[0].ID

	if err := s.UpsertTags(ctx, cid, []string{"service", "controller", "service"}); err != nil {
		t.Fatalf("upsert tags: %v", err)
	}
	tags, _ := s.GetTagsByClassID(ctx, cid)
	if len(tags) != 2 {
		t.Fatalf("tags should be deduped to 2, got %v", tags)
	}

	classIDs, methodIDs, err := s.SearchByTag(ctx, "service")
	if err != nil || len(classIDs) != 1 || len(methodIDs) != 0 {
		t.Fatalf("search by tag: %v %v %v", err, classIDs, methodIDs)
	}

	cats, err := s.GetAllTagsWithCategories(ctx)
	if err != nil {
		t.Fatalf("all tags: %v", err)
	}
	if cats["service"] != "layer" {
		t.Fatalf("category wrong: %v", cats)
	}
}

func TestSQLite_ReplaceSemantics(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, _ := s.UpsertFile(ctx, "/repo/y.go", "h", 10, 100)
	if err := s.UpsertClasses(ctx, id, []parser.ClassIR{
		{Name: "A", FullName: "pkg.A", Type: "CLASS"},
		{Name: "B", FullName: "pkg.B", Type: "CLASS"},
	}); err != nil {
		t.Fatal(err)
	}
	// 再次 Upsert 应全量替换（数量回到 1）
	if err := s.UpsertClasses(ctx, id, []parser.ClassIR{
		{Name: "C", FullName: "pkg.C", Type: "CLASS"},
	}); err != nil {
		t.Fatal(err)
	}
	classes, _ := s.GetClassesByFileID(ctx, id)
	if len(classes) != 1 || classes[0].FullName != "pkg.C" {
		t.Fatalf("replace failed: %+v", classes)
	}
}

func TestSQLite_HealthCheck(t *testing.T) {
	s := newTestStore(t)
	if err := s.HealthCheck(context.Background()); err != nil {
		t.Fatalf("health: %v", err)
	}
	_ = filepath.Join // keep import used if needed
}
