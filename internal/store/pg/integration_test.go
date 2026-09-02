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
	t.Log("FK constraints + ON DELETE CASCADE OK")
}

// TestPGStore_FieldConstantTables 验证常量表/变量表 DDL（2026-09-03 新增）：
//  1. InitSchema 后 field/constant 表存在；
//  2. field 正常写入成员变量(class_id) / 局部变量(method_id)，二选一 CHECK 拒绝
//     双 NULL 与双非空，FK 拒绝引用不存在的 class；
//  3. constant 正常写入包级(file_id) / 类级(class_id)，二选一 CHECK 拒绝双 NULL，
//     FK 拒绝引用不存在的 class；
//  4. ON DELETE CASCADE：删 file 后 field/constant 从属数据级联清空。
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
