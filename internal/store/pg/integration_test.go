//go:build pg

// PG 真实实例集成测试（T3-2）。
//
// 三档来源（按优先级）：
//  1. CODESCHEMA_PG_DSN 环境变量指向的 PostgreSQL 实例（如 docker compose --profile pg up -d）；
//  2. 本机 localhost:5432 已有实例（docker run 或原生 PG）；
//  3. 以上均不可达时，自动降级为嵌入式 PostgreSQL（fergusstrange/embedded-postgres，
//     首次运行会下载 PG 二进制，之后缓存复用）——真实 PG 内核，完整验证
//     InitSchema → UpsertIR → 查询 → 清理 全链路。
//
// 运行：go test -tags pg -run TestPGStore_EndToEnd ./internal/store/pg/ -v
// Redis 集成测试见 redis_integration_test.go（需 -tags 'pg redis' 双 tag 构建）。
package pg

import (
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	embeddedpostgres "github.com/fergusstrange/embedded-postgres"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// pgDSN 从环境变量读取 PG 连接串（默认本地 compose 实例）。
func pgDSN() string {
	if v := os.Getenv("CODESCHEMA_PG_DSN"); v != "" {
		return v
	}
	return "postgres://codeschema:codeschema@localhost:5432/codeschema?sslmode=disable"
}

// withPG 获取可用的 PostgreSQL 实例：优先外部实例，不可达则启动嵌入式 PG。
// 返回 (dsn, cleanup)。cleanup 停止嵌入式实例（外部实例不清理）。
func withPG(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	// 探测外部实例
	s := NewPGStore()
	if err := s.Open(ctx, pgDSN()); err == nil {
		s.Close()
		t.Logf("using external PostgreSQL at %s", pgDSN())
		return pgDSN(), func() {}
	}

	// 降级嵌入式 PG（真实内核）
	t.Log("external PostgreSQL unavailable, starting embedded PostgreSQL...")
	port := uint32(15432)
	ep := embeddedpostgres.NewDatabase(
		embeddedpostgres.DefaultConfig().
			Port(port).
			Username("codeschema").
			Password("codeschema").
			Database("codeschema"),
	)
	if err := ep.Start(); err != nil {
		t.Fatalf("start embedded postgres: %v", err)
	}
	dsn := fmt.Sprintf("postgres://codeschema:codeschema@localhost:%d/codeschema?sslmode=disable", port)
	// 等待就绪
	deadline := time.Now().Add(30 * time.Second)
	for time.Now().Before(deadline) {
		st := NewPGStore()
		if err := st.Open(ctx, dsn); err == nil {
			st.Close()
			break
		}
		time.Sleep(500 * time.Millisecond)
	}
	return dsn, func() { _ = ep.Stop() }
}

// TestPGStore_EndToEnd 验证真实 PG 实例（外部或嵌入式）上 scan→store→query 全链路。
func TestPGStore_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dsn, cleanup := withPG(t, ctx)
	defer cleanup()

	s := NewPGStore()
	if err := s.Open(ctx, dsn); err != nil {
		t.Fatalf("PG open: %v", err)
	}
	defer s.Close()

	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	if err := s.HealthCheck(ctx); err != nil {
		t.Fatalf("HealthCheck: %v", err)
	}

	// 写入一个合成 IR（1 文件 1 类 2 方法 2 调用）
	// 用唯一路径避免嵌入式 PG 持久化数据残留（ON CONFLICT DO NOTHING 导致重复运行不插新数据）
	uniquePath := fmt.Sprintf("/tmp/pg-it/file-%d.go", time.Now().UnixNano())
	ir := &parser.IRDocument{
		Source: "pg-integration", Language: "go",
		FilePath: uniquePath, FileHash: "hash-pg-1",
		LineCount: 50, ByteSize: 1024,
	}
	ir.Classes = []parser.ClassIR{{Name: "Svc", FullName: "pkg.Svc", Type: "CLASS"}}
	ir.Methods = []parser.MethodIR{
		{Name: "Run", ClassFQN: "pkg.Svc"},
		{Name: "Stop", ClassFQN: "pkg.Svc"},
	}
	ir.Calls = []parser.CallIR{
		{CallerFQN: "pkg.Svc.Run", CalleeFQN: "pkg.Svc.Stop", CallType: "direct"},
		{CallerFQN: "pkg.Svc.Stop", CalleeFQN: "pkg.Svc.Run", CallType: "direct"},
	}
	if err := s.UpsertIR(ctx, ir); err != nil {
		t.Fatalf("UpsertIR: %v", err)
	}

	// 查询验证：文件 / 类 / 方法 / 调用
	file, err := s.GetFileByPath(ctx, uniquePath)
	if err != nil || file == nil {
		t.Fatalf("GetFileByPath: %v (file=%v)", err, file)
	}
	classes, err := s.GetClassesByFileID(ctx, file.ID)
	if err != nil || len(classes) != 1 {
		t.Fatalf("GetClassesByFileID: %v (n=%d)", err, len(classes))
	}
	if classes[0].Name != "Svc" {
		t.Fatalf("class name = %s, want Svc", classes[0].Name)
	}
	methods, err := s.GetMethodsByClassID(ctx, classes[0].ID)
	if err != nil || len(methods) != 2 {
		t.Fatalf("GetMethodsByClassID: %v (n=%d)", err, len(methods))
	}
	calls, err := s.GetCallsByFileID(ctx, file.ID)
	if err != nil || len(calls) != 2 {
		t.Fatalf("GetCallsByFileID: %v (n=%d)", err, len(calls))
	}
	t.Logf("PG end-to-end OK: file=%d class=%d methods=%d calls=%d",
		file.ID, len(classes), len(methods), len(calls))

	// 清理（删除测试文件，幂等）
	_ = s.DeleteFile(ctx, file.ID)
}

// TestPGStore_ReindexClearsWhenEmpty 负控（P1-6）：
// 重索引后类/方法/调用集合为空时，PG 必须清掉旧行（旧实现仅在 len>0 时执行 upsert）。
func TestPGStore_ReindexClearsWhenEmpty(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dsn, cleanup := withPG(t, ctx)
	defer cleanup()

	s := NewPGStore()
	if err := s.Open(ctx, dsn); err != nil {
		t.Fatalf("PG open: %v", err)
	}
	defer s.Close()
	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	path := fmt.Sprintf("/tmp/pg-it/reindex-%d.go", time.Now().UnixNano())
	withData := &parser.IRDocument{
		Source: "pg-integration", Language: "go", FilePath: path, FileHash: "h1",
	}
	withData.Classes = []parser.ClassIR{{Name: "Reindex", FullName: "pkg.Reindex", Type: "CLASS"}}
	withData.Methods = []parser.MethodIR{{Name: "Gone", ClassFQN: "pkg.Reindex"}}
	withData.Calls = []parser.CallIR{{CallerFQN: "pkg.Reindex.Gone", CalleeFQN: "pkg.Other.Run"}}
	if err := s.UpsertIR(ctx, withData); err != nil {
		t.Fatalf("UpsertIR(with data): %v", err)
	}
	file, err := s.GetFileByPath(ctx, path)
	if err != nil || file == nil {
		t.Fatalf("GetFileByPath: %v (file=%v)", err, file)
	}

	empty := &parser.IRDocument{
		Source: "pg-integration", Language: "go", FilePath: path, FileHash: "h2",
	}
	if err := s.UpsertIR(ctx, empty); err != nil {
		t.Fatalf("UpsertIR(empty): %v", err)
	}

	classes, err := s.GetClassesByFileID(ctx, file.ID)
	if err != nil {
		t.Fatalf("GetClassesByFileID: %v", err)
	}
	if len(classes) != 0 {
		t.Fatalf("stale classes after reindex: %v", classes)
	}
	calls, err := s.GetCallsByFileID(ctx, file.ID)
	if err != nil {
		t.Fatalf("GetCallsByFileID: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("stale calls after reindex: %v", calls)
	}
	var methodCount int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM method WHERE full_name LIKE 'pkg.Reindex.%'`).Scan(&methodCount); err != nil {
		t.Fatalf("count method: %v", err)
	}
	if methodCount != 0 {
		t.Fatalf("stale methods after reindex: %d", methodCount)
	}
	_ = s.DeleteFile(ctx, file.ID)
}

// TestPGStore_FKConstraints 验证 FK 约束与 ON DELETE CASCADE（2026-09-03 新增）：
//  1. 引用不存在父记录的写入被 PG 拒绝（FK 生效）；
//  2. 正常写入后 DeleteFile → class/method/call 级联清空（CASCADE 生效）。
func TestPGStore_FKConstraints(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dsn, cleanup := withPG(t, ctx)
	defer cleanup()

	s := NewPGStore()
	if err := s.Open(ctx, dsn); err != nil {
		t.Fatalf("PG open: %v", err)
	}
	defer s.Close()
	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// 1) FK 生效：method 引用不存在的 class_id 应被拒绝
	if _, err := s.db.ExecContext(ctx, `INSERT INTO method (class_id, name) VALUES (999999, 'Orphan')`); err == nil {
		t.Fatalf("expected FK violation for orphan method.class_id, got nil error")
	} else {
		t.Logf("FK violation on orphan method.class_id as expected: %v", err)
	}

	// 2) CASCADE 生效：正常写入后 DeleteFile 应级联清空 class/method/call
	uniquePath := fmt.Sprintf("/tmp/pg-fk/file-%d.go", time.Now().UnixNano())
	ir := &parser.IRDocument{
		Source: "pg-fk", Language: "go",
		FilePath: uniquePath, FileHash: "hash-fk-1",
		LineCount: 10, ByteSize: 100,
	}
	ir.Classes = []parser.ClassIR{{Name: "FK", FullName: "pkg.FK", Type: "CLASS"}}
	ir.Methods = []parser.MethodIR{{Name: "M", ClassFQN: "pkg.FK"}}
	ir.Calls = []parser.CallIR{{CallerFQN: "pkg.FK.M", CalleeFQN: "pkg.FK.M", CallType: "direct"}}
	if err := s.UpsertIR(ctx, ir); err != nil {
		t.Fatalf("UpsertIR: %v", err)
	}
	file, err := s.GetFileByPath(ctx, uniquePath)
	if err != nil || file == nil {
		t.Fatalf("GetFileByPath: %v (file=%v)", err, file)
	}
	// 标签也挂在被删文件上：DeleteFile 后必须随 class/method 级联清空。
	tag := fmt.Sprintf("unit-%d", time.Now().UnixNano())
	clsRecs, err := s.GetClassesByFileID(ctx, file.ID)
	if err != nil || len(clsRecs) != 1 {
		t.Fatalf("GetClassesByFileID: %v len=%d", err, len(clsRecs))
	}
	if err := s.UpsertTags(ctx, clsRecs[0].ID, []string{tag}); err != nil {
		t.Fatalf("UpsertTags: %v", err)
	}
	methRecs, err := s.GetMethodsByClassID(ctx, clsRecs[0].ID)
	if err != nil || len(methRecs) != 1 {
		t.Fatalf("GetMethodsByClassID: %v len=%d", err, len(methRecs))
	}
	if err := s.UpsertMethodTags(ctx, methRecs[0].ID, []string{tag}); err != nil {
		t.Fatalf("UpsertMethodTags: %v", err)
	}
	if err := s.DeleteFile(ctx, file.ID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	// 级联后：file/class/call 均应为空
	if _, err := s.GetFileByPath(ctx, uniquePath); err == nil {
		t.Fatalf("expected file gone after DeleteFile (CASCADE), got no error")
	}
	classes, err := s.GetClassesByFileID(ctx, file.ID)
	if err != nil {
		t.Fatalf("GetClassesByFileID after delete: %v", err)
	}
	if len(classes) != 0 {
		t.Fatalf("expected classes cascaded to empty, got %d", len(classes))
	}
	calls, err := s.GetCallsByFileID(ctx, file.ID)
	if err != nil {
		t.Fatalf("GetCallsByFileID after delete: %v", err)
	}
	if len(calls) != 0 {
		t.Fatalf("expected calls cascaded to empty, got %d", len(calls))
	}
	if cids, mids, err := s.SearchByTags(ctx, []string{tag}); err != nil {
		t.Fatalf("SearchByTags after delete: %v", err)
	} else if len(cids) != 0 || len(mids) != 0 {
		t.Fatalf("expected tags cascaded to empty, got classes=%v methods=%v", cids, mids)
	}
	t.Log("FK constraints + ON DELETE CASCADE OK")
}

// TestPGStore_FieldConstantTables 验证常量表/变量表 DDL（2026-09-03 新增）：
//  1. InitSchema 后 field/constant 表存在；
//  2. field 正常写入成员变量(class_id) / 局部变量(method_id)，二选一 CHECK 拒绝
//     双 NULL 与双非空，FK 拒绝引用不存在的 class；
//  3. constant 正常写入包级(file_id) / 类级(class_id)，二选一 CHECK 拒绝双 NULL，
//     FK 拒绝引用不存在的 class；
//  4. ON DELETE CASCADE：删 file 后 field/constant 从属数据级联清空。
func TestPGStore_HotIndexes(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dsn, cleanup := withPG(t, ctx)
	defer cleanup()

	s := NewPGStore()
	if err := s.Open(ctx, dsn); err != nil {
		t.Fatalf("PG open: %v", err)
	}
	defer s.Close()
	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// R3-10：按文件清调用与标签反查必须走索引（表可能很小，关掉 seqscan 以保证
	// 计划能体现索引可用性；索引不存在时仍会给出 Seq Scan）。
	if _, err := s.db.ExecContext(ctx, `SET enable_seqscan = off`); err != nil {
		t.Fatalf("set enable_seqscan: %v", err)
	}
	cases := []struct {
		index string
		query string
	}{
		{"idx_call_file", "SELECT id FROM call WHERE file_id = 1"},
		{"idx_class_tag_tag", "SELECT class_id FROM class_tag WHERE tag_id = 1"},
		{"idx_method_tag_tag", "SELECT method_id FROM method_tag WHERE tag_id = 1"},
	}
	for _, c := range cases {
		var name string
		if err := s.db.QueryRowContext(ctx,
			`SELECT indexname FROM pg_indexes WHERE schemaname = current_schema() AND indexname = $1`, c.index,
		).Scan(&name); err != nil {
			t.Errorf("索引 %s 缺失: %v", c.index, err)
			continue
		}
		rows, err := s.db.QueryContext(ctx, "EXPLAIN "+c.query)
		if err != nil {
			t.Errorf("explain %s: %v", c.index, err)
			continue
		}
		plan := ""
		for rows.Next() {
			var line string
			if err := rows.Scan(&line); err != nil {
				t.Fatalf("scan plan: %v", err)
			}
			plan += line + "; "
		}
		_ = rows.Close()
		t.Logf("%s → %s", c.index, strings.TrimSuffix(plan, "; "))
		if !strings.Contains(plan, c.index) {
			t.Errorf("查询计划未使用索引 %s: %s", c.index, plan)
		}
	}
}

func TestPGStore_FieldConstantTables(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dsn, cleanup := withPG(t, ctx)
	defer cleanup()

	s := NewPGStore()
	if err := s.Open(ctx, dsn); err != nil {
		t.Fatalf("PG open: %v", err)
	}
	defer s.Close()
	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// 1) 表存在
	for _, tbl := range []string{"field", "constant"} {
		var n int
		if err := s.db.QueryRowContext(ctx,
			`SELECT COUNT(*) FROM information_schema.tables WHERE table_schema='public' AND table_name=$1`, tbl).Scan(&n); err != nil {
			t.Fatalf("check table %s: %v", tbl, err)
		}
		if n != 1 {
			t.Fatalf("table %s not created (n=%d)", tbl, n)
		}
	}

	// 准备父数据：file → class → method（直接 SQL，控制 FK 顺序）
	uniquePath := fmt.Sprintf("/tmp/pg-fc/file-%d.go", time.Now().UnixNano())
	var fileID int64
	if err := s.db.QueryRowContext(ctx, `INSERT INTO file (absolute_path) VALUES ($1) RETURNING id`, uniquePath).Scan(&fileID); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	var classID int64
	if err := s.db.QueryRowContext(ctx, `INSERT INTO class (file_id, name, full_name) VALUES ($1,'C','pkg.C') RETURNING id`, fileID).Scan(&classID); err != nil {
		t.Fatalf("insert class: %v", err)
	}
	var methodID int64
	if err := s.db.QueryRowContext(ctx, `INSERT INTO method (class_id, name) VALUES ($1,'M') RETURNING id`, classID).Scan(&methodID); err != nil {
		t.Fatalf("insert method: %v", err)
	}

	// 2) field 正常写入：成员变量 + 局部变量
	if _, err := s.db.ExecContext(ctx, `INSERT INTO field (class_id, name, type, is_static) VALUES ($1,'count','int',1)`, classID); err != nil {
		t.Fatalf("insert member field: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO field (method_id, name, type) VALUES ($1,'tmp','string')`, methodID); err != nil {
		t.Fatalf("insert local field: %v", err)
	}
	// 二选一 CHECK：双 NULL
	if _, err := s.db.ExecContext(ctx, `INSERT INTO field (name, type) VALUES ('x','int')`); err == nil {
		t.Fatal("expected CHECK violation for field with no owner, got nil")
	}
	// 二选一 CHECK：双非空
	if _, err := s.db.ExecContext(ctx, `INSERT INTO field (class_id, method_id, name) VALUES ($1,$1,'x')`, classID); err == nil {
		t.Fatal("expected CHECK violation for field with dual owner, got nil")
	}
	// FK：引用不存在的 class
	if _, err := s.db.ExecContext(ctx, `INSERT INTO field (class_id, name) VALUES (999999,'x')`); err == nil {
		t.Fatal("expected FK violation for orphan field.class_id, got nil")
	}

	// 3) constant 正常写入：包级 + 类级
	if _, err := s.db.ExecContext(ctx, `INSERT INTO constant (file_id, name, type, value) VALUES ($1,'Pi','float64','3.14')`, fileID); err != nil {
		t.Fatalf("insert package constant: %v", err)
	}
	if _, err := s.db.ExecContext(ctx, `INSERT INTO constant (class_id, name, type, value) VALUES ($1,'Max','int','100')`, classID); err != nil {
		t.Fatalf("insert class constant: %v", err)
	}
	// 二选一 CHECK：双 NULL
	if _, err := s.db.ExecContext(ctx, `INSERT INTO constant (name, value) VALUES ('x','1')`); err == nil {
		t.Fatal("expected CHECK violation for constant with no owner, got nil")
	}
	// FK：引用不存在的 class
	if _, err := s.db.ExecContext(ctx, `INSERT INTO constant (class_id, name, value) VALUES (999999,'x','1')`); err == nil {
		t.Fatal("expected FK violation for orphan constant.class_id, got nil")
	}

	// 4) CASCADE：删 file → class 级联清 field/constant（file 删除级联 class）
	if err := s.DeleteFile(ctx, fileID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	var fields, consts int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM field WHERE class_id=$1 OR method_id=$1`, classID).Scan(&fields); err != nil {
		t.Fatalf("count field after delete: %v", err)
	}
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM constant WHERE class_id=$1`, classID).Scan(&consts); err != nil {
		t.Fatalf("count constant after delete: %v", err)
	}
	if fields != 0 || consts != 0 {
		t.Fatalf("expected cascade cleanup, got fields=%d consts=%d", fields, consts)
	}
	t.Logf("field/constant tables OK: file=%d class=%d method=%d", fileID, classID, methodID)
}

// TestPGStore_FieldConstantIO 验证 field/constant 读写接口（FieldConstantStore，2026-09-03 新增）：
//  1. PGStore 实现 store.FieldConstantStore 接口断言；
//  2. UpsertClassFields/GetClassFields、UpsertMethodFields/GetMethodFields 读写一致
//     （含 is_static/is_const/行列号），且再次 Upsert 全量替换清掉旧行；
//  3. UpsertFileConstants/GetFileConstants、UpsertClassConstants/GetClassConstants 读写一致，
//     且全量替换生效。
func TestPGStore_FieldConstantIO(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dsn, cleanup := withPG(t, ctx)
	defer cleanup()

	s := NewPGStore()
	if err := s.Open(ctx, dsn); err != nil {
		t.Fatalf("PG open: %v", err)
	}
	defer s.Close()
	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// 1) 接口断言（编译期：PGStore 必须实现 store.FieldConstantStore）
	var _ store.FieldConstantStore = s

	// 准备父数据
	uniquePath := fmt.Sprintf("/tmp/pg-io/file-%d.go", time.Now().UnixNano())
	var fileID int64
	if err := s.db.QueryRowContext(ctx, `INSERT INTO file (absolute_path) VALUES ($1) RETURNING id`, uniquePath).Scan(&fileID); err != nil {
		t.Fatalf("insert file: %v", err)
	}
	var classID int64
	if err := s.db.QueryRowContext(ctx, `INSERT INTO class (file_id, name, full_name) VALUES ($1,'Svc','pkg.Svc') RETURNING id`, fileID).Scan(&classID); err != nil {
		t.Fatalf("insert class: %v", err)
	}
	var methodID int64
	if err := s.db.QueryRowContext(ctx, `INSERT INTO method (class_id, name) VALUES ($1,'Run') RETURNING id`, classID).Scan(&methodID); err != nil {
		t.Fatalf("insert method: %v", err)
	}

	// 2) field 读写
	memberFields := []store.FieldRecord{
		{Name: "count", Type: "int", IsStatic: true, StartLine: 3, EndLine: 3},
		{Name: "max", Type: "int", IsConst: true, Modifier: "const", StartLine: 4, EndLine: 4},
	}
	if err := s.UpsertClassFields(ctx, classID, memberFields); err != nil {
		t.Fatalf("UpsertClassFields: %v", err)
	}
	got, err := s.GetClassFields(ctx, classID)
	if err != nil {
		t.Fatalf("GetClassFields: %v", err)
	}
	if len(got) != 2 || got[0].Name != "count" || !got[0].IsStatic || got[0].Type != "int" ||
		got[1].Name != "max" || !got[1].IsConst {
		t.Fatalf("member fields mismatch: %+v", got)
	}
	// 全量替换：重写为单条，旧行应清空
	if err := s.UpsertClassFields(ctx, classID, []store.FieldRecord{{Name: "only", Type: "string"}}); err != nil {
		t.Fatalf("re-UpsertClassFields: %v", err)
	}
	got, err = s.GetClassFields(ctx, classID)
	if err != nil || len(got) != 1 || got[0].Name != "only" {
		t.Fatalf("member fields replace failed: %+v err=%v", got, err)
	}

	localFields := []store.FieldRecord{{Name: "tmp", Type: "string", StartLine: 10, EndLine: 12}}
	if err := s.UpsertMethodFields(ctx, methodID, localFields); err != nil {
		t.Fatalf("UpsertMethodFields: %v", err)
	}
	got, err = s.GetMethodFields(ctx, methodID)
	if err != nil || len(got) != 1 || got[0].Name != "tmp" || got[0].StartLine != 10 || got[0].EndLine != 12 {
		t.Fatalf("local fields mismatch: %+v err=%v", got, err)
	}

	// 3) constant 读写
	pkgConsts := []store.ConstantRecord{{Name: "Pi", Type: "float64", Value: "3.14"}}
	if err := s.UpsertFileConstants(ctx, fileID, pkgConsts); err != nil {
		t.Fatalf("UpsertFileConstants: %v", err)
	}
	gotc, err := s.GetFileConstants(ctx, fileID)
	if err != nil || len(gotc) != 1 || gotc[0].Name != "Pi" || gotc[0].Value != "3.14" {
		t.Fatalf("package constants mismatch: %+v err=%v", gotc, err)
	}
	clsConsts := []store.ConstantRecord{
		{Name: "Max", Type: "int", Value: "100"},
		{Name: "Min", Type: "int", Value: "0"},
	}
	if err := s.UpsertClassConstants(ctx, classID, clsConsts); err != nil {
		t.Fatalf("UpsertClassConstants: %v", err)
	}
	gotc, err = s.GetClassConstants(ctx, classID)
	if err != nil || len(gotc) != 2 {
		t.Fatalf("class constants count mismatch: %+v err=%v", gotc, err)
	}
	if err := s.UpsertClassConstants(ctx, classID, []store.ConstantRecord{{Name: "Only", Type: "int", Value: "1"}}); err != nil {
		t.Fatalf("re-UpsertClassConstants: %v", err)
	}
	gotc, err = s.GetClassConstants(ctx, classID)
	if err != nil || len(gotc) != 1 || gotc[0].Name != "Only" {
		t.Fatalf("class constants replace failed: %+v err=%v", gotc, err)
	}
	t.Log("field/constant read-write interfaces OK")
}

// TestPGStore_UpsertIRFieldsConstants 验证解析器产出的 FieldIR/ConstantIR 经
// UpsertIR 自动落库（2026-09-03 新增）：
//  1. 成员变量（FieldIR.ClassFQN）落 field.class_id，局部变量
//     （FieldIR.MethodFQN=ClassFQN.MethodName）落 field.method_id；
//  2. 包/文件级常量（ConstantIR.FilePath）落 constant.file_id，
//     类级常量（ConstantIR.ClassFQN）落 constant.class_id；
//  3. 再次 UpsertIR（字段/常量收敛）按类/文件全量替换，无残留旧行。
func TestPGStore_UpsertIRFieldsConstants(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()

	dsn, cleanup := withPG(t, ctx)
	defer cleanup()

	s := NewPGStore()
	if err := s.Open(ctx, dsn); err != nil {
		t.Fatalf("PG open: %v", err)
	}
	defer s.Close()
	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	uniquePath := fmt.Sprintf("/tmp/pg-irfc/file-%d.go", time.Now().UnixNano())
	ir := &parser.IRDocument{
		Source: "pg-irfc", Language: "go",
		FilePath: uniquePath, FileHash: "hash-irfc-1",
		LineCount: 20, ByteSize: 200,
	}
	ir.Classes = []parser.ClassIR{{Name: "Svc", FullName: "pkg.Svc", Type: "CLASS"}}
	ir.Methods = []parser.MethodIR{{Name: "Run", ClassFQN: "pkg.Svc"}}
	ir.Fields = []parser.FieldIR{
		{Name: "count", Type: "int", ClassFQN: "pkg.Svc", StartLine: 3},
		{Name: "tmp", Type: "string", MethodFQN: "pkg.Svc.Run", StartLine: 9},
	}
	ir.Constants = []parser.ConstantIR{
		{Name: "Pi", Type: "float64", Value: "3.14", FilePath: uniquePath},
		{Name: "Max", Type: "int", Value: "100", ClassFQN: "pkg.Svc"},
	}
	if err := s.UpsertIR(ctx, ir); err != nil {
		t.Fatalf("UpsertIR: %v", err)
	}

	file, err := s.GetFileByPath(ctx, uniquePath)
	if err != nil || file == nil {
		t.Fatalf("GetFileByPath: %v (file=%v)", err, file)
	}
	var classID int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM class WHERE file_id=$1 AND full_name='pkg.Svc'`, file.ID).Scan(&classID); err != nil {
		t.Fatalf("query class id: %v", err)
	}

	fcs, ok := any(s).(store.FieldConstantStore)
	if !ok {
		t.Fatal("PGStore must implement store.FieldConstantStore")
	}

	// 1) 成员变量 / 局部变量已落库
	mf, err := fcs.GetClassFields(ctx, classID)
	if err != nil || len(mf) != 1 || mf[0].Name != "count" || mf[0].Type != "int" || mf[0].StartLine != 3 {
		t.Fatalf("member field via UpsertIR mismatch: %+v err=%v", mf, err)
	}
	var methodID int64
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM method WHERE class_id=$1 AND name='Run'`, classID).Scan(&methodID); err != nil {
		t.Fatalf("query method id: %v", err)
	}
	lf, err := fcs.GetMethodFields(ctx, methodID)
	if err != nil || len(lf) != 1 || lf[0].Name != "tmp" || lf[0].Type != "string" {
		t.Fatalf("local field via UpsertIR mismatch: %+v err=%v", lf, err)
	}

	// 2) 包级 / 类级常量已落库
	pc, err := fcs.GetFileConstants(ctx, file.ID)
	if err != nil || len(pc) != 1 || pc[0].Name != "Pi" || pc[0].Value != "3.14" {
		t.Fatalf("package constant via UpsertIR mismatch: %+v err=%v", pc, err)
	}
	cc, err := fcs.GetClassConstants(ctx, classID)
	if err != nil || len(cc) != 1 || cc[0].Name != "Max" || cc[0].Value != "100" {
		t.Fatalf("class constant via UpsertIR mismatch: %+v err=%v", cc, err)
	}

	// 3) 再次 UpsertIR（字段/常量收敛）→ 全量替换，无残留
	ir2 := &parser.IRDocument{
		Source: "pg-irfc", Language: "go",
		FilePath: uniquePath, FileHash: "hash-irfc-2",
		LineCount: 20, ByteSize: 200,
	}
	ir2.Classes = []parser.ClassIR{{Name: "Svc", FullName: "pkg.Svc", Type: "CLASS"}}
	ir2.Methods = []parser.MethodIR{{Name: "Run", ClassFQN: "pkg.Svc"}}
	ir2.Fields = []parser.FieldIR{{Name: "only", Type: "string", ClassFQN: "pkg.Svc"}}
	ir2.Constants = []parser.ConstantIR{{Name: "Only", Type: "int", Value: "1", FilePath: uniquePath}}
	if err := s.UpsertIR(ctx, ir2); err != nil {
		t.Fatalf("re-UpsertIR: %v", err)
	}
	// 类被全量重插（新 ID），field/constant 随旧类级联删除；重新定位类 ID。
	classID2 := int64(0)
	if err := s.db.QueryRowContext(ctx, `SELECT id FROM class WHERE file_id=$1 AND full_name='pkg.Svc'`, file.ID).Scan(&classID2); err != nil {
		t.Fatalf("query new class id: %v", err)
	}
	mf, err = fcs.GetClassFields(ctx, classID2)
	if err != nil || len(mf) != 1 || mf[0].Name != "only" {
		t.Fatalf("member field replace failed: %+v err=%v", mf, err)
	}
	lf, err = fcs.GetMethodFields(ctx, methodID)
	if err != nil || len(lf) != 0 {
		t.Fatalf("local field should be cleared after replace, got %+v err=%v", lf, err)
	}
	pc, err = fcs.GetFileConstants(ctx, file.ID)
	if err != nil || len(pc) != 1 || pc[0].Name != "Only" {
		t.Fatalf("package constant replace failed: %+v err=%v", pc, err)
	}
	cc, err = fcs.GetClassConstants(ctx, classID2)
	if err != nil || len(cc) != 0 {
		t.Fatalf("class constant should be cleared after replace, got %+v err=%v", cc, err)
	}
	t.Log("UpsertIR field/constant auto-persistence OK")

	// 清理
	_ = s.DeleteFile(ctx, file.ID)
}

// TestPGStore_TagsBulkAndMisc 补齐嵌入式 PG 未覆盖的读写路径（2026-11-14）：
// DriverName / SetPoolLimits / PoolStats、MarkParseSkipped + GetFileByID、
// UpsertFile、标签读写与 AND 检索、BulkUpsert 多文件全量替换。
func TestPGStore_TagsBulkAndMisc(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	dsn, cleanup := withPG(t, ctx)
	defer cleanup()

	s := NewPGStore()
	if err := s.Open(ctx, dsn); err != nil {
		t.Fatalf("PG open: %v", err)
	}
	defer s.Close()
	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	// 1) 驱动名与连接池
	if got := s.DriverName(); got != "pg" {
		t.Fatalf("DriverName = %q, want pg", got)
	}
	if err := s.SetPoolLimits(4, 2); err != nil {
		t.Fatalf("SetPoolLimits: %v", err)
	}
	if got := s.PoolStats().MaxOpenConnections; got != 4 {
		t.Fatalf("MaxOpenConnections = %d, want 4", got)
	}

	// 2) 旁路文件：MarkParseSkipped 写入 parse_skipped，GetFileByID 可回读
	skippedPath := fmt.Sprintf("/tmp/pg-skip/file-%d.go", time.Now().UnixNano())
	skippedID, err := s.MarkParseSkipped(ctx, skippedPath, 4096, 77)
	if err != nil {
		t.Fatalf("MarkParseSkipped: %v", err)
	}
	skipped, err := s.GetFileByID(ctx, skippedID)
	if err != nil || skipped == nil {
		t.Fatalf("GetFileByID: %v (file=%v)", err, skipped)
	}
	if skipped.AbsolutePath != skippedPath || skipped.ParseStatus != "parse_skipped" ||
		skipped.ByteSize != 4096 || skipped.LineCount != 77 {
		t.Fatalf("skipped file mismatch: %+v", skipped)
	}

	// 3) UpsertFile 直接写入 parse_ok
	upsertPath := fmt.Sprintf("/tmp/pg-up/file-%d.go", time.Now().UnixNano())
	upsertID, err := s.UpsertFile(ctx, upsertPath, "hash-up", 12, 345)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	up, err := s.GetFileByID(ctx, upsertID)
	if err != nil || up == nil {
		t.Fatalf("GetFileByID(upsert): %v", err)
	}
	if up.ParseStatus != "parse_ok" || up.ContentHash != "hash-up" || up.LineCount != 12 || up.ByteSize != 345 {
		t.Fatalf("upsert file mismatch: %+v", up)
	}

	// 4) 标签读写：类/方法标签、AND 检索、分类映射、全量替换
	file, classID, alphaID, betaID := seedTaggedIR(t, ctx, s)
	authTag := fmt.Sprintf("auth-%d", time.Now().UnixNano())
	handlerTag := fmt.Sprintf("handler-%d", time.Now().UnixNano())
	asyncTag := fmt.Sprintf("async-%d", time.Now().UnixNano())
	if err := s.UpsertTags(ctx, classID, []string{authTag, handlerTag}); err != nil {
		t.Fatalf("UpsertTags: %v", err)
	}
	if err := s.UpsertMethodTags(ctx, alphaID, []string{asyncTag}); err != nil {
		t.Fatalf("UpsertMethodTags: %v", err)
	}
	classTags, err := s.GetTagsByClassID(ctx, classID)
	if err != nil || !sameSet(classTags, []string{authTag, handlerTag}) {
		t.Fatalf("GetTagsByClassID = %v (%v)", classTags, err)
	}
	methodTags, err := s.GetTagsByMethodID(ctx, alphaID)
	if err != nil || !sameSet(methodTags, []string{asyncTag}) {
		t.Fatalf("GetTagsByMethodID = %v (%v)", methodTags, err)
	}
	cids, mids, err := s.SearchByTag(ctx, authTag)
	if err != nil || !hasID(cids, classID) || len(mids) != 0 {
		t.Fatalf("SearchByTag = %v/%v (%v)", cids, mids, err)
	}
	cids, mids, err = s.SearchByTags(ctx, []string{authTag, handlerTag})
	if err != nil || !hasID(cids, classID) || len(mids) != 0 {
		t.Fatalf("SearchByTags AND = %v/%v (%v)", cids, mids, err)
	}
	cids, _, err = s.SearchByTags(ctx, []string{authTag, asyncTag})
	if err != nil || len(cids) != 0 {
		t.Fatalf("SearchByTags 跨类/方法不应命中: %v (%v)", cids, err)
	}
	all, err := s.GetAllTagsWithCategories(ctx)
	if err != nil {
		t.Fatalf("GetAllTagsWithCategories: %v", err)
	}
	for _, tag := range []string{authTag, handlerTag, asyncTag} {
		if all[tag] == "" {
			t.Fatalf("tag %q missing category: %v", tag, all)
		}
	}
	// 全量替换：旧标签应清空
	if err := s.UpsertTags(ctx, classID, []string{authTag}); err != nil {
		t.Fatalf("re-UpsertTags: %v", err)
	}
	classTags, err = s.GetTagsByClassID(ctx, classID)
	if err != nil || !sameSet(classTags, []string{authTag}) {
		t.Fatalf("class tags replace failed: %v (%v)", classTags, err)
	}
	_ = betaID

	// 5) BulkUpsert：多文件写入 + 再次调用全量替换（无残留旧方法）
	p1 := fmt.Sprintf("/tmp/pg-bulk/a-%d.go", time.Now().UnixNano())
	p2 := fmt.Sprintf("/tmp/pg-bulk/b-%d.go", time.Now().UnixNano())
	bulk := []*parser.IRDocument{
		{
			Source: "pg-bulk", Language: "go", FilePath: p1, FileHash: "b1",
			Classes: []parser.ClassIR{{Name: "A", FullName: "pkg.A", Type: "CLASS"}},
			Methods: []parser.MethodIR{{Name: "Old", ClassFQN: "pkg.A"}},
		},
		{
			Source: "pg-bulk", Language: "go", FilePath: p2, FileHash: "b2",
			Classes: []parser.ClassIR{{Name: "B", FullName: "pkg.B", Type: "CLASS"}},
			Methods: []parser.MethodIR{{Name: "Run", ClassFQN: "pkg.B"}},
		},
	}
	if err := s.BulkUpsert(ctx, bulk); err != nil {
		t.Fatalf("BulkUpsert: %v", err)
	}
	f1, err := s.GetFileByPath(ctx, p1)
	if err != nil || f1 == nil {
		t.Fatalf("bulk file1: %v", err)
	}
	cls1, err := s.GetClassesByFileID(ctx, f1.ID)
	if err != nil || len(cls1) != 1 {
		t.Fatalf("bulk classes: %v n=%d", err, len(cls1))
	}
	ms1, err := s.GetMethodsByClassID(ctx, cls1[0].ID)
	if err != nil || len(ms1) != 1 || ms1[0].Name != "Old" {
		t.Fatalf("bulk methods: %v %+v", err, ms1)
	}
	// 第二次 BulkUpsert：A 收敛为单个 New 方法
	bulk[0].FileHash = "b1-v2"
	bulk[0].Methods = []parser.MethodIR{{Name: "New", ClassFQN: "pkg.A"}}
	if err := s.BulkUpsert(ctx, bulk); err != nil {
		t.Fatalf("re-BulkUpsert: %v", err)
	}
	cls1, err = s.GetClassesByFileID(ctx, f1.ID)
	if err != nil || len(cls1) != 1 {
		t.Fatalf("re-bulk classes: %v n=%d", err, len(cls1))
	}
	ms1, err = s.GetMethodsByClassID(ctx, cls1[0].ID)
	if err != nil || len(ms1) != 1 || ms1[0].Name != "New" {
		t.Fatalf("bulk replace failed: %v %+v", err, ms1)
	}

	// 清理
	_ = s.DeleteFile(ctx, file.ID)
	_ = s.DeleteFile(ctx, skippedID)
	_ = s.DeleteFile(ctx, upsertID)
	_ = s.DeleteFile(ctx, f1.ID)
	if f2, err := s.GetFileByPath(ctx, p2); err == nil && f2 != nil {
		_ = s.DeleteFile(ctx, f2.ID)
	}
}

// TestPGStore_DirectUpsertClassesMethodsCalls 覆盖 Store 接口中三个「全量替换」
// 写入口的独立调用路径：此前只有 UpsertIR/BulkUpsert 的内部事务路径被验证，
// 直接调用 UpsertClasses/UpsertMethods/UpsertCalls 的行为（含二次调用清空旧数据、
// UpsertClasses 级联清理旧方法）没有断言。
func TestPGStore_DirectUpsertClassesMethodsCalls(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	dsn, cleanup := withPG(t, ctx)
	defer cleanup()

	s := NewPGStore()
	if err := s.Open(ctx, dsn); err != nil {
		t.Fatalf("PG open: %v", err)
	}
	defer s.Close()
	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	path := fmt.Sprintf("/tmp/pg-direct/direct-%d.go", time.Now().UnixNano())
	fileID, err := s.UpsertFile(ctx, path, "direct-hash", 2, 64)
	if err != nil {
		t.Fatalf("UpsertFile: %v", err)
	}
	defer func() { _ = s.DeleteFile(ctx, fileID) }()

	// 1) UpsertClasses 直接调用
	if err := s.UpsertClasses(ctx, fileID, []parser.ClassIR{
		{Name: "Alpha", FullName: "pkg.Alpha", Type: "CLASS", StartLine: 1, EndLine: 20},
	}); err != nil {
		t.Fatalf("UpsertClasses: %v", err)
	}
	classes, err := s.GetClassesByFileID(ctx, fileID)
	if err != nil || len(classes) != 1 || classes[0].FullName != "pkg.Alpha" {
		t.Fatalf("classes after UpsertClasses: %v %+v", err, classes)
	}
	classID := classes[0].ID

	// 2) UpsertMethods 直接调用 + 二次全量替换
	if err := s.UpsertMethods(ctx, classID, []parser.MethodIR{
		{Name: "Old", ClassFQN: "pkg.Alpha"},
		{Name: "Keep", ClassFQN: "pkg.Alpha"},
	}); err != nil {
		t.Fatalf("UpsertMethods: %v", err)
	}
	methods, err := s.GetMethodsByClassID(ctx, classID)
	if err != nil || len(methods) != 2 {
		t.Fatalf("methods after UpsertMethods: %v %+v", err, methods)
	}
	if err := s.UpsertMethods(ctx, classID, []parser.MethodIR{
		{Name: "Keep", ClassFQN: "pkg.Alpha"},
	}); err != nil {
		t.Fatalf("re-UpsertMethods: %v", err)
	}
	methods, err = s.GetMethodsByClassID(ctx, classID)
	if err != nil || len(methods) != 1 || methods[0].Name != "Keep" {
		t.Fatalf("methods replace failed: %v %+v", err, methods)
	}

	// 3) UpsertCalls 直接调用 + 二次全量替换
	if err := s.UpsertCalls(ctx, fileID, []parser.CallIR{
		{CallerFQN: "pkg.Alpha.Old", CalleeFQN: "pkg.Alpha.Keep", CallType: "direct", LineNumber: 3, Confidence: 90},
	}); err != nil {
		t.Fatalf("UpsertCalls: %v", err)
	}
	calls, err := s.GetCallsByFileID(ctx, fileID)
	if err != nil || len(calls) != 1 || calls[0].CallerFQN != "pkg.Alpha.Old" {
		t.Fatalf("calls after UpsertCalls: %v %+v", err, calls)
	}
	if err := s.UpsertCalls(ctx, fileID, []parser.CallIR{
		{CallerFQN: "pkg.Alpha.Keep", CalleeFQN: "pkg.Alpha.Keep", CallType: "direct", LineNumber: 4, Confidence: 90},
	}); err != nil {
		t.Fatalf("re-UpsertCalls: %v", err)
	}
	calls, err = s.GetCallsByFileID(ctx, fileID)
	if err != nil || len(calls) != 1 || calls[0].CallerFQN != "pkg.Alpha.Keep" {
		t.Fatalf("calls replace failed: %v %+v", err, calls)
	}

	// 4) UpsertClasses 空集合：类与方法一并清空（全量替换 + 级联）
	if err := s.UpsertClasses(ctx, fileID, nil); err != nil {
		t.Fatalf("clear classes: %v", err)
	}
	classes, err = s.GetClassesByFileID(ctx, fileID)
	if err != nil || len(classes) != 0 {
		t.Fatalf("classes not cleared: %v %+v", err, classes)
	}
	methods, err = s.GetMethodsByClassID(ctx, classID)
	if err != nil || len(methods) != 0 {
		t.Fatalf("methods not cascaded: %v %+v", err, methods)
	}
}

// seedTaggedIR 写入一个含两个方法的合成 IR，返回文件/类/方法 ID。
func seedTaggedIR(t *testing.T, ctx context.Context, s *PGStore) (*store.FileRecord, int64, int64, int64) {
	t.Helper()
	path := fmt.Sprintf("/tmp/pg-tags/file-%d.go", time.Now().UnixNano())
	ir := &parser.IRDocument{
		Source: "pg-tags", Language: "go", FilePath: path, FileHash: "tag-hash",
		Classes: []parser.ClassIR{{Name: "Tagged", FullName: "pkg.Tagged", Type: "CLASS"}},
		Methods: []parser.MethodIR{
			{Name: "Alpha", ClassFQN: "pkg.Tagged"},
			{Name: "Beta", ClassFQN: "pkg.Tagged"},
		},
	}
	if err := s.UpsertIR(ctx, ir); err != nil {
		t.Fatalf("seed UpsertIR: %v", err)
	}
	file, err := s.GetFileByPath(ctx, path)
	if err != nil || file == nil {
		t.Fatalf("seed GetFileByPath: %v", err)
	}
	classes, err := s.GetClassesByFileID(ctx, file.ID)
	if err != nil || len(classes) != 1 {
		t.Fatalf("seed classes: %v n=%d", err, len(classes))
	}
	methods, err := s.GetMethodsByClassID(ctx, classes[0].ID)
	if err != nil || len(methods) != 2 {
		t.Fatalf("seed methods: %v n=%d", err, len(methods))
	}
	alpha, beta := int64(0), int64(0)
	for _, m := range methods {
		switch m.Name {
		case "Alpha":
			alpha = m.ID
		case "Beta":
			beta = m.ID
		}
	}
	if alpha == 0 || beta == 0 {
		t.Fatalf("seed method ids not found: %+v", methods)
	}
	return file, classes[0].ID, alpha, beta
}

func sameSet(got, want []string) bool {
	if len(got) != len(want) {
		return false
	}
	m := make(map[string]int, len(got))
	for _, s := range got {
		m[s]++
	}
	for _, s := range want {
		m[s]--
		if m[s] < 0 {
			return false
		}
	}
	return true
}

func hasID(ids []int64, want int64) bool {
	for _, id := range ids {
		if id == want {
			return true
		}
	}
	return false
}

// TestPGStore_MethodFullNameRoundTrip 负控（P1-5）：
// method.full_name 必须持久化并可回读；否则测试关联/标签/context 在 PG 上失效。
func TestPGStore_MethodFullNameRoundTrip(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()

	dsn, cleanup := withPG(t, ctx)
	defer cleanup()

	s := NewPGStore()
	if err := s.Open(ctx, dsn); err != nil {
		t.Fatalf("open pg: %v", err)
	}
	defer s.Close()
	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("init schema: %v", err)
	}

	filePath := fmt.Sprintf("/repo/p15_%d/svc.go", time.Now().UnixNano())
	ir := &parser.IRDocument{
		Source: "treesitter", Language: "go", FilePath: filePath,
		FileHash: "h", LineCount: 20, ByteSize: 200,
		Classes: []parser.ClassIR{{Name: "Svc", FullName: "p15.Svc", Type: "CLASS"}},
		Methods: []parser.MethodIR{{Name: "Run", ClassFQN: "p15.Svc", Signature: "Run()"}},
	}
	if err := s.UpsertIR(ctx, ir); err != nil {
		t.Fatalf("upsert IR: %v", err)
	}

	files, err := s.GetAllFiles(ctx)
	if err != nil {
		t.Fatalf("get files: %v", err)
	}
	var fileID int64
	for _, f := range files {
		if f.AbsolutePath == filePath {
			fileID = f.ID
			break
		}
	}
	if fileID == 0 {
		t.Fatalf("file not found: %s", filePath)
	}
	classes, err := s.GetClassesByFileID(ctx, fileID)
	if err != nil || len(classes) != 1 {
		t.Fatalf("get classes: %v %d", err, len(classes))
	}
	methods, err := s.GetMethodsByClassID(ctx, classes[0].ID)
	if err != nil || len(methods) != 1 {
		t.Fatalf("get methods: %v %d", err, len(methods))
	}
	if methods[0].FullName != "p15.Svc.Run" {
		t.Fatalf("full_name not persisted: got %q, want %q", methods[0].FullName, "p15.Svc.Run")
	}
}

// TestPGStore_ConcurrentWrites 验证多 worker 并发写 PG（文档「并发写」缺口）。
//
// 两个子场景：
//   - disjoint:  8 worker × 6 个互不相交的文件并发 UpsertIR，验证无死锁/无丢行；
//   - same_path: 6 worker 各重索引同一文件 5 轮，验证 file 行锁串行化后，
//     全量替换（DELETE + INSERT）不会留下重复的类/方法/调用。
func TestPGStore_ConcurrentWrites(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 180*time.Second)
	defer cancel()

	dsn, cleanup := withPG(t, ctx)
	defer cleanup()

	s := NewPGStore()
	if err := s.Open(ctx, dsn); err != nil {
		t.Fatalf("PG open: %v", err)
	}
	defer s.Close()
	if err := s.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	makeIR := func(path, hash, prefix string) *parser.IRDocument {
		ir := &parser.IRDocument{
			Source: "pg-concurrency", Language: "go",
			FilePath: path, FileHash: hash, LineCount: 10, ByteSize: 128,
		}
		ir.Classes = []parser.ClassIR{{Name: "Svc", FullName: prefix + ".Svc", Type: "CLASS"}}
		for i := 0; i < 3; i++ {
			name := fmt.Sprintf("M%d", i)
			ir.Methods = append(ir.Methods, parser.MethodIR{Name: name, ClassFQN: prefix + ".Svc"})
			if i > 0 {
				ir.Calls = append(ir.Calls, parser.CallIR{
					CallerFQN: prefix + ".Svc." + name,
					CalleeFQN: prefix + ".Svc.M0",
					CallType:  "direct",
				})
			}
		}
		return ir
	}

	verify := func(t *testing.T, path string) {
		t.Helper()
		f, err := s.GetFileByPath(ctx, path)
		if err != nil || f == nil {
			t.Fatalf("GetFileByPath(%s): %v", path, err)
		}
		classes, err := s.GetClassesByFileID(ctx, f.ID)
		if err != nil || len(classes) != 1 {
			t.Fatalf("%s: classes=%d err=%v, want 1", path, len(classes), err)
		}
		methods, err := s.GetMethodsByClassID(ctx, classes[0].ID)
		if err != nil || len(methods) != 3 {
			t.Fatalf("%s: methods=%d err=%v, want 3", path, len(methods), err)
		}
		calls, err := s.GetCallsByFileID(ctx, f.ID)
		if err != nil || len(calls) != 2 {
			t.Fatalf("%s: calls=%d err=%v, want 2", path, len(calls), err)
		}
		_ = s.DeleteFile(ctx, f.ID)
	}

	t.Run("disjoint", func(t *testing.T) {
		const workers, perWorker = 8, 6
		stamp := time.Now().UnixNano()
		paths := make([]string, 0, workers*perWorker)
		var wg sync.WaitGroup
		errCh := make(chan error, workers*perWorker)
		start := make(chan struct{})
		for w := 0; w < workers; w++ {
			for i := 0; i < perWorker; i++ {
				path := fmt.Sprintf("/tmp/pg-it/conc-disjoint-%d-%d-%d.go", stamp, w, i)
				prefix := fmt.Sprintf("conc.d%d_%d", w, i)
				paths = append(paths, path)
				ir := makeIR(path, fmt.Sprintf("hash-%d-%d", w, i), prefix)
				wg.Add(1)
				go func(ir *parser.IRDocument) {
					defer wg.Done()
					<-start
					if err := s.UpsertIR(ctx, ir); err != nil {
						errCh <- fmt.Errorf("%s: %w", ir.FilePath, err)
					}
				}(ir)
			}
		}
		close(start)
		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Errorf("concurrent UpsertIR: %v", err)
		}
		for _, path := range paths {
			verify(t, path)
		}
	})

	t.Run("bulk_batches", func(t *testing.T) {
		const workers, perBatch = 4, 3
		stamp := time.Now().UnixNano()
		paths := make([]string, 0, workers*perBatch)
		var wg sync.WaitGroup
		errCh := make(chan error, workers)
		start := make(chan struct{})
		for w := 0; w < workers; w++ {
			batch := make([]*parser.IRDocument, 0, perBatch)
			for i := 0; i < perBatch; i++ {
				path := fmt.Sprintf("/tmp/pg-it/conc-bulk-%d-%d-%d.go", stamp, w, i)
				paths = append(paths, path)
				batch = append(batch, makeIR(path, fmt.Sprintf("bulk-%d-%d", w, i), fmt.Sprintf("conc.b%d_%d", w, i)))
			}
			wg.Add(1)
			go func(batch []*parser.IRDocument) {
				defer wg.Done()
				<-start
				if err := s.BulkUpsert(ctx, batch); err != nil {
					errCh <- fmt.Errorf("bulk worker: %w", err)
				}
			}(batch)
		}
		close(start)
		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Errorf("concurrent BulkUpsert: %v", err)
		}
		for _, path := range paths {
			verify(t, path)
		}
	})

	t.Run("same_path", func(t *testing.T) {
		const workers, rounds = 6, 5
		path := fmt.Sprintf("/tmp/pg-it/conc-same-%d.go", time.Now().UnixNano())
		prefix := "conc.same"
		var wg sync.WaitGroup
		errCh := make(chan error, workers*rounds)
		start := make(chan struct{})
		for w := 0; w < workers; w++ {
			wg.Add(1)
			go func(w int) {
				defer wg.Done()
				<-start
				for r := 0; r < rounds; r++ {
					ir := makeIR(path, fmt.Sprintf("hash-%d-%d", w, r), prefix)
					if err := s.UpsertIR(ctx, ir); err != nil {
						errCh <- fmt.Errorf("worker %d round %d: %w", w, r, err)
					}
				}
			}(w)
		}
		close(start)
		wg.Wait()
		close(errCh)
		for err := range errCh {
			t.Errorf("same-path concurrent UpsertIR: %v", err)
		}
		verify(t, path)
	})
}
