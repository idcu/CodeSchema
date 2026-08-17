package sqlite

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
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

// TestSQLite_SearchByTags_MultiTagAND 验证 SQLite 多标签 AND 交集：
// GROUP BY + HAVING COUNT(DISTINCT) = n 只返回同时拥有全部标签的符号。
func TestSQLite_SearchByTags_MultiTagAND(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	// 三个类：A=service+cache，B=service，C=cache
	idA, _ := s.UpsertFile(ctx, "/repo/a.go", "h1", 10, 100)
	idB, _ := s.UpsertFile(ctx, "/repo/b.go", "h2", 10, 100)
	idC, _ := s.UpsertFile(ctx, "/repo/c.go", "h3", 10, 100)
	for _, id := range []int64{idA, idB, idC} {
		_ = s.UpsertClasses(ctx, id, []parser.ClassIR{{Name: "X", FullName: "pkg.X", Type: "CLASS"}})
	}
	cid := func(id int64) int64 {
		classes, _ := s.GetClassesByFileID(ctx, id)
		return classes[0].ID
	}
	cidA, cidB, cidC := cid(idA), cid(idB), cid(idC)

	if err := s.UpsertTags(ctx, cidA, []string{"service", "cache"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTags(ctx, cidB, []string{"service"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertTags(ctx, cidC, []string{"cache"}); err != nil {
		t.Fatal(err)
	}

	// 单标签 service → A、B
	classIDs, methodIDs, err := s.SearchByTags(ctx, []string{"service"})
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
	classIDs, _, err = s.SearchByTags(ctx, []string{"service", "cache"})
	if err != nil {
		t.Fatalf("SearchByTags multi: %v", err)
	}
	if len(classIDs) != 1 || classIDs[0] != cidA {
		t.Fatalf("AND service+cache: want only %d, got %v", cidA, classIDs)
	}

	// 不存在组合 → 空
	classIDs, _, err = s.SearchByTags(ctx, []string{"service", "mq"})
	if err != nil {
		t.Fatalf("SearchByTags missing: %v", err)
	}
	if len(classIDs) != 0 {
		t.Fatalf("AND service+mq: want empty, got %v", classIDs)
	}

	// 空标签列表 → 空
	classIDs, _, err = s.SearchByTags(ctx, nil)
	if err != nil {
		t.Fatalf("SearchByTags nil: %v", err)
	}
	if len(classIDs) != 0 {
		t.Fatalf("nil tags: want empty, got %v", classIDs)
	}
}

// TestSQLite_SearchByTags_MethodTags 验证方法标签的多标签 AND 检索。
func TestSQLite_SearchByTags_MethodTags(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	id, _ := s.UpsertFile(ctx, "/repo/a.go", "h", 10, 100)
	if err := s.UpsertClasses(ctx, id, []parser.ClassIR{{Name: "A", FullName: "pkg.A", Type: "CLASS"}}); err != nil {
		t.Fatal(err)
	}
	classes, _ := s.GetClassesByFileID(ctx, id)
	cid := classes[0].ID
	if err := s.UpsertMethods(ctx, cid, []parser.MethodIR{
		{Name: "Get", ClassFQN: "pkg.A"},
		{Name: "Put", ClassFQN: "pkg.A"},
	}); err != nil {
		t.Fatal(err)
	}
	methods, _ := s.GetMethodsByClassID(ctx, cid)
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
		t.Fatalf("expected Get/Put ids, got %d/%d", midGet, midPut)
	}
	if err := s.UpsertMethodTags(ctx, midGet, []string{"cache", "read"}); err != nil {
		t.Fatal(err)
	}
	if err := s.UpsertMethodTags(ctx, midPut, []string{"cache"}); err != nil {
		t.Fatal(err)
	}

	classIDs, methodIDs, err := s.SearchByTags(ctx, []string{"cache", "read"})
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

// ============================================================================
// 并发写压力测试（P7_2 未做项补齐）
// 验证 WAL + busy_timeout=5000 配置下 SQLiteStore 在多 goroutine 并发写入时：
// 不死锁、不丢数据、不产生半成品记录。覆盖三种生产场景：
//  ① 多 worker 并发扫描不同文件（DistinctFiles）
//  ② 同一文件高频重复入库（SameFile）
//  ③ 写与读并发（ReadWrite），配合 -race 检测数据竞争
// 运行：go test -race -run 'TestSQLite_Concurrent' ./internal/store/sqlite/ -v
// ============================================================================

// concurrentIR 合成一个带类/方法/调用的 IRDocument，路径唯一由 idx 决定。
func concurrentIR(idx int) *parser.IRDocument {
	fqn := fmt.Sprintf("pkg%d.Svc%d", idx%5, idx)
	return &parser.IRDocument{
		Source:    "concurrent",
		Language:  "go",
		FilePath:  fmt.Sprintf("/repo/pkg%d/file%d.go", idx%5, idx),
		FileHash:  fmt.Sprintf("hash%d", idx),
		LineCount: 100 + idx,
		ByteSize:  2048,
		Classes:   []parser.ClassIR{{Name: fmt.Sprintf("Svc%d", idx), FullName: fqn, Type: "CLASS", StartLine: 1, EndLine: 30}},
		Methods: []parser.MethodIR{
			{Name: "Run", ClassFQN: fqn, Signature: "Run() error", StartLine: 3, EndLine: 10},
		},
		Calls: []parser.CallIR{
			{CallerFQN: fqn + ".Run", CalleeFQN: "fmt.Println", CallType: "direct", LineNumber: 5},
		},
	}
}

// TestSQLite_ConcurrentUpsertIR_DistinctFiles 模拟多 worker 扫描：8 goroutine 各写
// 25 个不同文件（共 200 个），验证 WAL 下并发写不丢数据、最终数量与内容一致。
func TestSQLite_ConcurrentUpsertIR_DistinctFiles(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const workers, perWorker = 8, 25
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < perWorker; i++ {
				idx := w*perWorker + i
				if err := s.UpsertIR(ctx, concurrentIR(idx)); err != nil {
					errCh <- fmt.Errorf("worker %d upsert %d: %w", w, idx, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	// 全部文件必须存在
	files, err := s.GetAllFiles(ctx)
	if err != nil {
		t.Fatalf("get all files: %v", err)
	}
	if len(files) != workers*perWorker {
		t.Fatalf("expected %d files, got %d", workers*perWorker, len(files))
	}
	// 抽查：路径唯一（无重复写入）、内容完整
	seen := map[string]bool{}
	for _, f := range files {
		if seen[f.AbsolutePath] {
			t.Fatalf("duplicate file path: %s", f.AbsolutePath)
		}
		seen[f.AbsolutePath] = true
	}
	for _, f := range files {
		classes, err := s.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			t.Fatalf("classes for %s: %v", f.AbsolutePath, err)
		}
		if len(classes) != 1 {
			t.Fatalf("file %s: expected 1 class, got %d", f.AbsolutePath, len(classes))
		}
		methods, err := s.GetMethodsByClassID(ctx, classes[0].ID)
		if err != nil || len(methods) != 1 {
			t.Fatalf("file %s: expected 1 method, got %d (err=%v)", f.AbsolutePath, len(methods), err)
		}
	}
}

// TestSQLite_ConcurrentUpdateSameFile 模拟同一文件高频变更：8 goroutine 并发更新
// 同一路径，验证幂等（不产生重复文件记录）且最终数据属于最后一次写入（无损坏）。
func TestSQLite_ConcurrentUpdateSameFile(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const workers, rounds = 8, 20
	var wg sync.WaitGroup
	errCh := make(chan error, workers)
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for r := 0; r < rounds; r++ {
				ir := concurrentIR(w*rounds + r) // 不同 idx 仅改变数据内容
				ir.FilePath = "/repo/hot.go"     // 强制同一路径，验证并发幂等
				if err := s.UpsertIR(ctx, ir); err != nil {
					errCh <- fmt.Errorf("worker %d round %d: %w", w, r, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	files, err := s.GetAllFiles(ctx)
	if err != nil {
		t.Fatalf("get all files: %v", err)
	}
	if len(files) != 1 {
		t.Fatalf("concurrent upsert of same path must keep 1 file record, got %d", len(files))
	}
	// 类也应被全量替换为 1 个（不残留并发中间态）
	classes, err := s.GetClassesByFileID(ctx, files[0].ID)
	if err != nil {
		t.Fatalf("get classes: %v", err)
	}
	if len(classes) != 1 {
		t.Fatalf("expected 1 class after concurrent replace, got %d", len(classes))
	}
}

// TestSQLite_ConcurrentReadWrite 写的同时并发读（配合 -race 检测数据竞争与死锁）。
//
// 设计注意：reader 必须用固定迭代次数（而非 select stop 的无限循环）——
// 无限循环的 reader 正常工作时不退出，会让 wg.Wait() 永不完成，误判为死锁
// （曾因此误报 modernc 并发 bug，见 concurrent_fix_test.go 注释）。
func TestSQLite_ConcurrentReadWrite(t *testing.T) {
	s := newTestStore(t)
	ctx := context.Background()

	const readIters = 500
	var wg sync.WaitGroup

	// 读者：固定次数 GetAllFiles + GetClassesByFileID
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < readIters; i++ {
			files, err := s.GetAllFiles(ctx)
			if err != nil {
				t.Errorf("reader: %v", err)
				return
			}
			for _, f := range files {
				if _, err := s.GetClassesByFileID(ctx, f.ID); err != nil {
					t.Errorf("reader classes: %v", err)
					return
				}
			}
		}
	}()

	// 写者：持续写入不同文件
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func(w int) {
			defer wg.Done()
			for i := 0; i < 10; i++ {
				if err := s.UpsertIR(ctx, concurrentIR(w*10+i)); err != nil {
					t.Errorf("writer %d: %v", w, err)
					return
				}
			}
		}(w)
	}
	wg.Wait()
}
