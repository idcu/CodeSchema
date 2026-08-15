//go:build pg

// PG 真实实例集成测试（T3-2）。
//
// 前提：本地已有 PostgreSQL 实例（推荐用 docker-compose：
//   docker compose --profile pg up -d
// 或：
//   docker run -d --name codeschema-pg -e POSTGRES_PASSWORD=codeschema -e POSTGRES_DB=codeschema -p 5432:5432 postgres:16-alpine
//
// 实例不可达时优雅跳过（不阻塞 CI/本地无服务环境）。
// Redis 集成测试见 redis_integration_test.go（需 -tags 'pg redis' 双 tag 构建）。
package pg

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/parser"
)

// pgDSN 从环境变量读取 PG 连接串（默认本地 compose 实例）。
func pgDSN() string {
	if v := os.Getenv("CODESCHEMA_PG_DSN"); v != "" {
		return v
	}
	return "postgres://codeschema:codeschema@localhost:5432/codeschema?sslmode=disable"
}

// skipIfPGUnavailable 探测 PG 可达性，不可达则跳过。
func skipIfPGUnavailable(t *testing.T, ctx context.Context) {
	t.Helper()
	s := NewPGStore()
	if err := s.Open(ctx, pgDSN()); err != nil {
		t.Skipf("PostgreSQL 不可达（%v），跳过 PG 集成测试；启动方式见文件头注释", err)
	}
	defer s.Close()
}

// TestPGStore_EndToEnd 验证真实 PG 实例上 scan→store→query 全链路。
func TestPGStore_EndToEnd(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	skipIfPGUnavailable(t, ctx)

	s := NewPGStore()
	if err := s.Open(ctx, pgDSN()); err != nil {
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
	ir := &parser.IRDocument{
		Source: "pg-integration", Language: "go",
		FilePath: "/tmp/pg-it/file1.go", FileHash: "hash-pg-1",
		LineCount: 50, ByteSize: 1024,
	}
	ir.Classes = []parser.ClassIR{{Name: "Svc", FullName: "pkg.Svc", Type: "CLASS"}}
	ir.Methods = []parser.MethodIR{
		{Name: "Run", ClassFQN: "pkg.Svc"},
		{Name: "Stop", ClassFQN: "pkg.Svc"},
	}
	ir.Calls = []parser.CallIR{
		{CallerFQN: "pkg.Svc.Run", CalleeFQN: "pkg.Svc.Stop", CallType: "direct"},
	}
	if err := s.UpsertIR(ctx, ir); err != nil {
		t.Fatalf("UpsertIR: %v", err)
	}

	// 查询验证：文件 / 类 / 方法 / 调用
	file, err := s.GetFileByPath(ctx, "/tmp/pg-it/file1.go")
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
