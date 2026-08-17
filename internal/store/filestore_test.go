package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
)

func TestFileStore_OpenClose(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStore{}

	if err := fs.Open(context.Background(), dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	if err := fs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	// 验证数据文件已创建
	if _, err := os.Stat(filepath.Join(dir, "store.json")); err != nil {
		t.Fatalf("store.json not created: %v", err)
	}
}

func TestFileStore_UpsertAndGetFile(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStore{}
	ctx := context.Background()

	if err := fs.Open(ctx, dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer fs.Close()

	// 插入文件
	id, err := fs.UpsertFile(ctx, "/test/main.go", "abc123", 100, 2048)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if id != 1 {
		t.Errorf("expected id 1, got %d", id)
	}

	// 查询文件
	f, err := fs.GetFileByPath(ctx, "/test/main.go")
	if err != nil {
		t.Fatalf("GetFileByPath: %v", err)
	}
	if f == nil {
		t.Fatal("file not found")
	}
	if f.ContentHash != "abc123" {
		t.Errorf("expected hash abc123, got %s", f.ContentHash)
	}

	// 再次插入（更新）
	id2, err := fs.UpsertFile(ctx, "/test/main.go", "def456", 120, 4096)
	if err != nil {
		t.Fatalf("UpsertFile (update): %v", err)
	}
	if id2 != id {
		t.Errorf("expected same id %d, got %d", id, id2)
	}

	// 验证已更新
	f, _ = fs.GetFileByPath(ctx, "/test/main.go")
	if f.ContentHash != "def456" {
		t.Errorf("expected hash def456, got %s", f.ContentHash)
	}
	if f.LineCount != 120 {
		t.Errorf("expected line count 120, got %d", f.LineCount)
	}
}

func TestFileStore_DeleteFile(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStore{}
	ctx := context.Background()

	if err := fs.Open(ctx, dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer fs.Close()

	id, _ := fs.UpsertFile(ctx, "/test/main.go", "hash1", 100, 1024)
	if err := fs.DeleteFile(ctx, id); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}

	f, _ := fs.GetFileByPath(ctx, "/test/main.go")
	if f != nil {
		t.Error("file should be deleted")
	}
}

func TestFileStore_UpsertIR(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStore{}
	ctx := context.Background()

	if err := fs.Open(ctx, dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer fs.Close()

	// 模拟一个 IR 文档
	ir := &parser.IRDocument{
		Source:   "treesitter",
		Language: "go",
		FilePath: "/test/service.go",
		FileHash: "sha256hash",
		LineCount: 200,
		ByteSize:  8192,
		Classes: []parser.ClassIR{
			{Name: "UserService", FullName: "com.example.UserService", Type: "CLASS", StartLine: 1, EndLine: 50},
			{Name: "UserRepository", FullName: "com.example.UserRepository", Type: "INTERFACE", StartLine: 52, EndLine: 60},
		},
		Calls: []parser.CallIR{
			{CallerFQN: "com.example.UserService.GetUser", CalleeFQN: "com.example.UserRepository.FindByID", CallType: "direct", LineNumber: 30},
		},
	}

	if err := fs.UpsertIR(ctx, ir); err != nil {
		t.Fatalf("UpsertIR: %v", err)
	}

	// 验证文件已创建
	f, _ := fs.GetFileByPath(ctx, "/test/service.go")
	if f == nil {
		t.Fatal("file not found after UpsertIR")
	}
	if f.LineCount != 200 {
		t.Errorf("expected line count 200, got %d", f.LineCount)
	}
}

func TestFileStore_Persistence(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	// 第一次写入
	fs1 := &FileStore{}
	if err := fs1.Open(ctx, dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	fs1.UpsertFile(ctx, "/test/main.go", "hash1", 100, 1024)
	fs1.UpsertFile(ctx, "/test/util.go", "hash2", 50, 512)
	fs1.Close()

	// 第二次读取
	fs2 := &FileStore{}
	if err := fs2.Open(ctx, dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer fs2.Close()

	f1, _ := fs2.GetFileByPath(ctx, "/test/main.go")
	if f1 == nil || f1.ContentHash != "hash1" {
		t.Error("persistence failed for main.go")
	}

	f2, _ := fs2.GetFileByPath(ctx, "/test/util.go")
	if f2 == nil || f2.ContentHash != "hash2" {
		t.Error("persistence failed for util.go")
	}
}

func TestFileStore_HealthCheck(t *testing.T) {
	fs := &FileStore{}
	ctx := context.Background()

	// 未初始化时应返回错误
	if err := fs.HealthCheck(ctx); err == nil {
		t.Error("expected error for uninitialized store")
	}

	dir := t.TempDir()
	fs.Open(ctx, dir)
	if err := fs.HealthCheck(ctx); err != nil {
		t.Errorf("HealthCheck: %v", err)
	}
	fs.Close()
}

// TestFileStore_SearchByTags_MultiTagAND 验证多标签 AND 交集：
// 只有同时拥有全部查询标签的符号才会被命中。
func TestFileStore_SearchByTags_MultiTagAND(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStore{}
	ctx := context.Background()
	if err := fs.Open(ctx, dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer fs.Close()

	// 三个类：A=service+cache，B=service，C=cache
	fidA, _ := fs.UpsertFile(ctx, "/a.go", "h1", 10, 100)
	fidB, _ := fs.UpsertFile(ctx, "/b.go", "h2", 10, 100)
	fidC, _ := fs.UpsertFile(ctx, "/c.go", "h3", 10, 100)
	if err := fs.UpsertClasses(ctx, fidA, []parser.ClassIR{{Name: "A", FullName: "pkg.A", Type: "CLASS"}}); err != nil {
		t.Fatal(err)
	}
	if err := fs.UpsertClasses(ctx, fidB, []parser.ClassIR{{Name: "B", FullName: "pkg.B", Type: "CLASS"}}); err != nil {
		t.Fatal(err)
	}
	if err := fs.UpsertClasses(ctx, fidC, []parser.ClassIR{{Name: "C", FullName: "pkg.C", Type: "CLASS"}}); err != nil {
		t.Fatal(err)
	}
	cid := func(fid int64) int64 {
		classes, _ := fs.GetClassesByFileID(ctx, fid)
		return classes[0].ID
	}
	cidA, cidB, cidC := cid(fidA), cid(fidB), cid(fidC)

	if err := fs.UpsertTags(ctx, cidA, []string{"service", "cache"}); err != nil {
		t.Fatal(err)
	}
	if err := fs.UpsertTags(ctx, cidB, []string{"service"}); err != nil {
		t.Fatal(err)
	}
	if err := fs.UpsertTags(ctx, cidC, []string{"cache"}); err != nil {
		t.Fatal(err)
	}

	// 单标签 service → A、B
	classIDs, methodIDs, err := fs.SearchByTags(ctx, []string{"service"})
	if err != nil {
		t.Fatalf("SearchByTags: %v", err)
	}
	if !hasInt64(classIDs, cidA) || !hasInt64(classIDs, cidB) || hasInt64(classIDs, cidC) {
		t.Fatalf("single tag service: want A,B got %v", classIDs)
	}
	if len(methodIDs) != 0 {
		t.Fatalf("unexpected method ids: %v", methodIDs)
	}

	// 双标签 service+cache（AND）→ 仅 A
	classIDs, _, err = fs.SearchByTags(ctx, []string{"service", "cache"})
	if err != nil {
		t.Fatalf("SearchByTags multi: %v", err)
	}
	if len(classIDs) != 1 || classIDs[0] != cidA {
		t.Fatalf("AND service+cache: want only %d, got %v", cidA, classIDs)
	}

	// 不存在组合 → 空
	classIDs, _, err = fs.SearchByTags(ctx, []string{"service", "mq"})
	if err != nil {
		t.Fatalf("SearchByTags missing: %v", err)
	}
	if len(classIDs) != 0 {
		t.Fatalf("AND service+mq: want empty, got %v", classIDs)
	}

	// 空标签列表 → 空（不命中一切）
	classIDs, _, err = fs.SearchByTags(ctx, nil)
	if err != nil {
		t.Fatalf("SearchByTags nil: %v", err)
	}
	if len(classIDs) != 0 {
		t.Fatalf("nil tags: want empty, got %v", classIDs)
	}
}

// TestFileStore_SearchByTags_MethodTags 验证方法标签的多标签 AND 检索。
func TestFileStore_SearchByTags_MethodTags(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStore{}
	ctx := context.Background()
	if err := fs.Open(ctx, dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer fs.Close()

	fid, _ := fs.UpsertFile(ctx, "/a.go", "h1", 10, 100)
	if err := fs.UpsertClasses(ctx, fid, []parser.ClassIR{{Name: "A", FullName: "pkg.A", Type: "CLASS"}}); err != nil {
		t.Fatal(err)
	}
	classes, _ := fs.GetClassesByFileID(ctx, fid)
	cid := classes[0].ID
	if err := fs.UpsertMethods(ctx, cid, []parser.MethodIR{
		{Name: "Get", ClassFQN: "pkg.A"},
		{Name: "Put", ClassFQN: "pkg.A"},
	}); err != nil {
		t.Fatal(err)
	}
	methods, _ := fs.GetMethodsByClassID(ctx, cid)
	// 按名称匹配，避免依赖固定 ID
	var midGet, midPut int64
	for _, m := range methods {
		switch m.Name {
		case "Get":
			midGet = m.ID
		case "Put":
			midPut = m.ID
		}
	}
	if midGet == 0 || midPut == 0 {
		t.Fatalf("expected Get/Put method ids, got %d/%d", midGet, midPut)
	}

	if err := fs.UpsertMethodTags(ctx, midGet, []string{"cache", "read"}); err != nil {
		t.Fatal(err)
	}
	if err := fs.UpsertMethodTags(ctx, midPut, []string{"cache"}); err != nil {
		t.Fatal(err)
	}

	// cache+read（AND）→ 仅 Get
	classIDs, methodIDs, err := fs.SearchByTags(ctx, []string{"cache", "read"})
	if err != nil {
		t.Fatalf("SearchByTags: %v", err)
	}
	if len(classIDs) != 0 {
		t.Fatalf("expected no class, got %v", classIDs)
	}
	if len(methodIDs) != 1 || methodIDs[0] != midGet {
		t.Fatalf("AND cache+read: want only Get(%d), got %v", midGet, methodIDs)
	}
}

func hasInt64(s []int64, v int64) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

// TestContainsAll 验证多标签包含判断的边界行为。
func TestContainsAll(t *testing.T) {
	need := map[string]struct{}{"a": {}, "b": {}}
	if !containsAll([]string{"a", "b", "c"}, need) {
		t.Error("want true when all tags present")
	}
	if containsAll([]string{"a", "c"}, need) {
		t.Error("want false when a tag missing")
	}
	if containsAll([]string{}, need) {
		t.Error("want false when have is empty")
	}
	// 空 need：恒真（len(remaining)==0）
	if !containsAll([]string{}, map[string]struct{}{}) {
		t.Error("want true for empty need")
	}
	// 不污染调用方 map
	if _, ok := need["a"]; !ok {
		t.Error("caller map should not be mutated")
	}
}