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
