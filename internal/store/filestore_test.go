package store

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
)

// TestFileStore_OpenRejectsCorruptStore 负控（R3-12）：store.json 损坏时 Open 必须
// 返回错误，而不是静默当作空库——否则下一次写入会用空数据覆盖唯一副本。
func TestFileStore_OpenRejectsCorruptStore(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "store.json"), []byte(`{"files": {`), 0o600); err != nil {
		t.Fatalf("write corrupt store: %v", err)
	}

	fs := &FileStore{}
	if err := fs.Open(context.Background(), dir); err == nil {
		t.Fatal("期望 Open 对损坏的 store.json 返回错误，实际 nil（会静默清空数据）")
	}
}

// TestFileStore_SaveIsAtomic 负控（R3-12）：保存应 fsync 文件内容后再原子 rename，
// 且不残留临时文件；崩溃后要么旧版本完整、要么新版本完整。
func TestFileStore_SaveIsAtomic(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStore{}
	if err := fs.Open(context.Background(), dir); err != nil {
		t.Fatalf("Open: %v", err)
	}

	if _, err := fs.UpsertFile(context.Background(), "/x/a.go", "h", 1, 10); err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if err := fs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	if _, err := os.Stat(filepath.Join(dir, "store.json.tmp")); !os.IsNotExist(err) {
		t.Errorf("保存后不应残留 store.json.tmp: %v", err)
	}
	info, err := os.Stat(filepath.Join(dir, "store.json"))
	if err != nil {
		t.Fatalf("stat store.json: %v", err)
	}
	if info.Size() == 0 {
		t.Errorf("store.json 不应为空")
	}
}

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

// TestFileStore_DeleteFileClearsMethodTags 负控（P2-10）：
// DeleteFile 过去用 class ID 删 methodTags，真实键是 method ID，标签会残留。
func TestFileStore_DeleteFileClearsMethodTags(t *testing.T) {
	ctx := context.Background()
	fs := &FileStore{}
	if err := fs.Open(ctx, t.TempDir()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer fs.Close()

	fileID, err := fs.UpsertFile(ctx, "/repo/svc.go", "h1", 20, 200)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	if err := fs.UpsertClasses(ctx, fileID, []parser.ClassIR{{Name: "Svc", FullName: "pkg.Svc", Type: "CLASS"}}); err != nil {
		t.Fatalf("UpsertClasses: %v", err)
	}
	classes, _ := fs.GetClassesByFileID(ctx, fileID)
	if len(classes) != 1 {
		t.Fatalf("expected 1 class, got %d", len(classes))
	}
	classID := classes[0].ID
	if err := fs.UpsertMethods(ctx, classID, []parser.MethodIR{{Name: "Run", ClassFQN: "pkg.Svc"}}); err != nil {
		t.Fatalf("UpsertMethods: %v", err)
	}
	methods, _ := fs.GetMethodsByClassID(ctx, classID)
	if len(methods) != 1 {
		t.Fatalf("expected 1 method, got %d", len(methods))
	}
	methodID := methods[0].ID
	if err := fs.UpsertMethodTags(ctx, methodID, []string{"hot"}); err != nil {
		t.Fatalf("UpsertMethodTags: %v", err)
	}

	if err := fs.DeleteFile(ctx, fileID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	tags, err := fs.GetTagsByMethodID(ctx, methodID)
	if err != nil {
		t.Fatalf("GetTagsByMethodID: %v", err)
	}
	if len(tags) != 0 {
		t.Fatalf("expected method tags cleared, got %v", tags)
	}
}

// TestFileStore_BulkUpsertClearsCallsWhenEmpty 负控（P1-6）：
// 新 IR 无调用时应清空旧调用边，否则重索引留下悬空边。
func TestFileStore_BulkUpsertClearsCallsWhenEmpty(t *testing.T) {
	ctx := context.Background()
	fs := &FileStore{}
	if err := fs.Open(ctx, t.TempDir()); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer fs.Close()

	withCalls := &parser.IRDocument{
		Source: "treesitter", Language: "go", FilePath: "/repo/a.go", FileHash: "h1",
		Classes: []parser.ClassIR{{Name: "A", FullName: "pkg.A", Type: "CLASS"}},
		Methods: []parser.MethodIR{{Name: "Run", ClassFQN: "pkg.A"}},
		Calls:   []parser.CallIR{{CallerFQN: "pkg.A.Run", CalleeFQN: "pkg.B.Helper"}},
	}
	if err := fs.BulkUpsert(ctx, []*parser.IRDocument{withCalls}); err != nil {
		t.Fatalf("BulkUpsert(with calls): %v", err)
	}
	f, _ := fs.GetFileByPath(ctx, "/repo/a.go")
	if f == nil {
		t.Fatal("file not found")
	}
	noCalls := &parser.IRDocument{
		Source: "treesitter", Language: "go", FilePath: "/repo/a.go", FileHash: "h2",
		Classes: []parser.ClassIR{{Name: "A", FullName: "pkg.A", Type: "CLASS"}},
		Methods: []parser.MethodIR{{Name: "Run", ClassFQN: "pkg.A"}},
	}
	if err := fs.BulkUpsert(ctx, []*parser.IRDocument{noCalls}); err != nil {
		t.Fatalf("BulkUpsert(no calls): %v", err)
	}
	f, _ = fs.GetFileByPath(ctx, "/repo/a.go")
	if f == nil {
		t.Fatal("file not found after reindex")
	}
	calls, err := fs.GetCallsByFileID(ctx, f.ID)
	if err != nil {
		t.Fatalf("GetCallsByFileID: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("expected stale calls cleared, got %v", calls)
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
		Source:    "treesitter",
		Language:  "go",
		FilePath:  "/test/service.go",
		FileHash:  "sha256hash",
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

// TestFileStore_ReindexRemovesStaleMethods 负控（P1-6）：
// 重索引后旧 class ID 下的 method 映射必须清理，否则内存/落盘都留孤儿。
func TestFileStore_ReindexRemovesStaleMethods(t *testing.T) {
	dir := t.TempDir()
	fs := &FileStore{}
	ctx := context.Background()
	if err := fs.Open(ctx, dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer fs.Close()

	ir := func(methods []parser.MethodIR, classes []parser.ClassIR) *parser.IRDocument {
		return &parser.IRDocument{
			Source: "treesitter", Language: "go", FilePath: "/repo/svc.go",
			FileHash: "h", LineCount: 10, ByteSize: 100,
			Classes: classes, Methods: methods,
		}
	}
	svc := []parser.ClassIR{{Name: "Svc", FullName: "pkg.Svc", Type: "CLASS"}}

	totalMethods := func() int {
		fs.mu.RLock()
		defer fs.mu.RUnlock()
		n := 0
		for _, ms := range fs.methods {
			n += len(ms)
		}
		return n
	}

	if err := fs.UpsertIR(ctx, ir([]parser.MethodIR{
		{Name: "Old", ClassFQN: "pkg.Svc"},
		{Name: "Kept", ClassFQN: "pkg.Svc"},
	}, svc)); err != nil {
		t.Fatalf("upsert #1: %v", err)
	}
	if err := fs.UpsertIR(ctx, ir([]parser.MethodIR{
		{Name: "Kept", ClassFQN: "pkg.Svc"},
	}, svc)); err != nil {
		t.Fatalf("upsert #2: %v", err)
	}
	if got := totalMethods(); got != 1 {
		t.Fatalf("stale methods after UpsertIR reindex: got %d, want 1", got)
	}

	// 类整体消失。
	if err := fs.UpsertIR(ctx, ir(nil, nil)); err != nil {
		t.Fatalf("upsert #3: %v", err)
	}
	if got := totalMethods(); got != 0 {
		t.Fatalf("stale methods after class removal: got %d, want 0", got)
	}

	// BulkUpsert 同样适用。
	if err := fs.BulkUpsert(ctx, []*parser.IRDocument{{
		Source: "treesitter", Language: "go", FilePath: "/repo/svc.go",
		FileHash: "h2", LineCount: 10, ByteSize: 100,
		Classes: svc,
		Methods: []parser.MethodIR{{Name: "A", ClassFQN: "pkg.Svc"}, {Name: "B", ClassFQN: "pkg.Svc"}},
	}}); err != nil {
		t.Fatalf("bulk #1: %v", err)
	}
	if err := fs.BulkUpsert(ctx, []*parser.IRDocument{{
		Source: "treesitter", Language: "go", FilePath: "/repo/svc.go",
		FileHash: "h3", LineCount: 10, ByteSize: 100,
		Classes: svc,
		Methods: []parser.MethodIR{{Name: "A", ClassFQN: "pkg.Svc"}},
	}}); err != nil {
		t.Fatalf("bulk #2: %v", err)
	}
	if got := totalMethods(); got != 1 {
		t.Fatalf("stale methods after BulkUpsert reindex: got %d, want 1", got)
	}
}

// TestFileStore_CallMetaRoundTrip 负控（R5-3）：调用边来源/置信度必须随
// store.json 持久化，重启后仍然可读（否则每次重启边精度信息归零）。
func TestFileStore_CallMetaRoundTrip(t *testing.T) {
	dir := t.TempDir()
	ctx := context.Background()

	fs := &FileStore{}
	if err := fs.Open(ctx, dir); err != nil {
		t.Fatalf("Open: %v", err)
	}
	fid, err := fs.UpsertFile(ctx, "/repo/a.go", "h", 1, 10)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	err = fs.UpsertCalls(ctx, fid, []parser.CallIR{{
		CallerFQN: "pkg.A.Run", CalleeFQN: "pkg.B.Helper", CallType: "direct", LineNumber: 3,
		Source: parser.CallSourceSCIP, Confidence: parser.ConfidenceSCIP,
	}})
	if err != nil {
		t.Fatalf("UpsertCalls: %v", err)
	}
	if err := fs.Close(); err != nil {
		t.Fatalf("Close: %v", err)
	}

	reopened := &FileStore{}
	if err := reopened.Open(ctx, dir); err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer reopened.Close()

	calls, err := reopened.GetCallsByFileID(ctx, fid)
	if err != nil || len(calls) != 1 {
		t.Fatalf("GetCallsByFileID: %v len=%d", err, len(calls))
	}
	if calls[0].Source != parser.CallSourceSCIP || calls[0].Confidence != parser.ConfidenceSCIP {
		t.Errorf("call provenance lost after restart: %+v", calls[0])
	}
}
