package search

import (
	"context"
	"errors"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/vector"
)

// failClassReadStore 注入「某个文件的类读取失败」，用于区分
// 「读失败」与「该文件确实没有类」。
type failClassReadStore struct {
	store.Store
	fileID int64
}

func (f failClassReadStore) GetClassesByFileID(ctx context.Context, fileID int64) ([]store.ClassRecord, error) {
	if fileID == f.fileID {
		return nil, errors.New("injected class read failure")
	}
	return f.Store.GetClassesByFileID(ctx, fileID)
}

// failMethodReadStore 注入方法读取失败，用于验证增量构建失败后追踪表不丢失。
type failMethodReadStore struct {
	store.Store
}

func (f failMethodReadStore) GetMethodsByClassID(ctx context.Context, classID int64) ([]store.MethodRecord, error) {
	return nil, errors.New("injected method read failure")
}

// newFailureTestStore 打开一个内存 FileStore。
func newFailureTestStore(t *testing.T) store.Store {
	t.Helper()
	st := store.NewStore("file")
	if err := st.Open(context.Background(), t.TempDir()); err != nil {
		t.Fatalf("open store: %v", err)
	}
	t.Cleanup(func() { _ = st.Close() })
	return st
}

// newFailureTestBuilder 构造只用于断言的内存索引构建器。
func newFailureTestBuilder(t *testing.T) (*IndexBuilder, *MemoryFTS) {
	t.Helper()
	fts := NewMemoryFTS()
	vs := vector.NewMemoryStore()
	em := vector.NewLocalEmbedder(128)
	idx := vector.NewIndexer(vs, em, 2)
	return NewIndexBuilder(fts, idx, em), fts
}

// ftsMatches 判断 FTS 中是否还有命中 query 的文档。
func ftsMatches(t *testing.T, fts *MemoryFTS, query string) bool {
	t.Helper()
	results, err := fts.Search(context.Background(), query, FTSModeFuzzy, 20)
	if err != nil {
		t.Fatalf("fts search %q: %v", query, err)
	}
	return len(results) > 0
}

// TestBuildFromStoreKeepsDocsWhenClassReadFails C1：
// GetClassesByFileID 报错不得被当成「文件已删除」——检索文档必须保留。
func TestBuildFromStoreKeepsDocsWhenClassReadFails(t *testing.T) {
	ctx := context.Background()
	st := newFailureTestStore(t)
	b, fts := newFailureTestBuilder(t)

	goodID, err := st.UpsertFile(ctx, "/repo/good.go", "h1", 10, 100)
	if err != nil {
		t.Fatalf("upsert good file: %v", err)
	}
	if err := st.UpsertClasses(ctx, goodID, []parser.ClassIR{
		{Name: "Good", FullName: "pkg.Good", Type: "CLASS"},
	}); err != nil {
		t.Fatalf("upsert good class: %v", err)
	}
	flakyID, err := st.UpsertFile(ctx, "/repo/flaky.go", "h2", 10, 100)
	if err != nil {
		t.Fatalf("upsert flaky file: %v", err)
	}
	if err := st.UpsertClasses(ctx, flakyID, []parser.ClassIR{
		{Name: "Flaky", FullName: "pkg.Flaky", Type: "CLASS"},
	}); err != nil {
		t.Fatalf("upsert flaky class: %v", err)
	}

	// 基线：两个文件的检索文档都存在。
	if _, err := b.BuildFromStore(ctx, st); err != nil {
		t.Fatalf("baseline BuildFromStore: %v", err)
	}
	if !ftsMatches(t, fts, "Flaky") {
		t.Fatalf("基线索引缺少 Flaky，用例前提不成立")
	}

	// 第二次构建：Flaky 文件的类读取失败（Store 仍在）。
	result, err := b.BuildFromStore(ctx, failClassReadStore{Store: st, fileID: flakyID})
	if err != nil {
		t.Fatalf("BuildFromStore(failing): %v", err)
	}
	if result.Errors != 1 {
		t.Fatalf("result.Errors = %d，期望 1", result.Errors)
	}

	if !ftsMatches(t, fts, "Flaky") {
		t.Fatal("读类失败被当成文件已删除：Flaky 的检索文档被摘除，但 Store 仍在")
	}
	if !ftsMatches(t, fts, "Good") {
		t.Fatal("正常文件的检索文档不应受影响")
	}
}

// TestBuildAndIndexKeepsTrackingAfterFailure C2：
// takeFileDocs 之后提前返回不得丢失追踪表项，否则旧文档永久残留。
func TestBuildAndIndexKeepsTrackingAfterFailure(t *testing.T) {
	ctx := context.Background()
	st := newFailureTestStore(t)
	b, fts := newFailureTestBuilder(t)

	path := "/repo/evolving.go"
	oldIR := &parser.IRDocument{
		Source: "fixture", Language: "go", FilePath: path, FileHash: "h-old",
		Classes: []parser.ClassIR{{Name: "OldClass", FullName: "pkg.OldClass", Type: "CLASS"}},
		Methods: []parser.MethodIR{{Name: "OldMethod", ClassFQN: "pkg.OldClass"}},
	}
	if err := st.UpsertIR(ctx, oldIR); err != nil {
		t.Fatalf("upsert old ir: %v", err)
	}
	if _, err := b.BuildFromStore(ctx, st); err != nil {
		t.Fatalf("baseline BuildFromStore: %v", err)
	}
	if !ftsMatches(t, fts, "OldClass") {
		t.Fatalf("基线索引缺少 OldClass，用例前提不成立")
	}

	// 重扫：类/方法换新 ID（旧文档需靠追踪表摘除）。
	newIR := &parser.IRDocument{
		Source: "fixture", Language: "go", FilePath: path, FileHash: "h-new",
		Classes: []parser.ClassIR{{Name: "NewClass", FullName: "pkg.NewClass", Type: "CLASS"}},
		Methods: []parser.MethodIR{{Name: "NewMethod", ClassFQN: "pkg.NewClass"}},
	}
	if err := st.UpsertIR(ctx, newIR); err != nil {
		t.Fatalf("upsert new ir: %v", err)
	}

	// 第一次增量：读方法失败（在摘除旧文档之前返回）。
	if err := b.BuildAndIndex(ctx, failMethodReadStore{Store: st}, path); err == nil {
		t.Fatal("注入方法读失败后 BuildAndIndex 应返回错误")
	}

	// 第二次增量：存储恢复正常，必须能摘除旧文档并写入新文档。
	if err := b.BuildAndIndex(ctx, st, path); err != nil {
		t.Fatalf("BuildAndIndex(retry): %v", err)
	}
	if ftsMatches(t, fts, "OldClass") {
		t.Fatal("增量构建失败后追踪表项丢失：旧类文档永久残留")
	}
	if !ftsMatches(t, fts, "NewClass") {
		t.Fatal("重试后新类文档未写入索引")
	}
}
