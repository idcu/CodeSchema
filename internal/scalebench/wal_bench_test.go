// WAL 检查点/同步参数对单事务批量写入的耗时与落盘影响（P7_2 未做项）。
//
// 背景：SQLiteStore 默认 `journal_mode=WAL + synchronous=NORMAL + busy_timeout=5000`。
// 本基准对比 synchronous 三级（NORMAL/FULL/OFF）在 BulkUpsert 等价负载
// （单事务 + 预编译语句 + file/class 双表批量插入）下的耗时与 db 文件大小，
// 验证默认 NORMAL 是否合理、OFF/FULL 的取舍边界。
//
// 运行：go test -bench=BenchmarkSQLiteWALConfigs -benchtime=1x ./internal/scalebench
package scalebench

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	_ "modernc.org/sqlite"
)

// BenchmarkSQLiteWALConfigs 对比 synchronous 级别对批量写入的影响。
func BenchmarkSQLiteWALConfigs(b *testing.B) {
	const n = 10000
	configs := []struct {
		name   string
		pragma string
	}{
		{"sync_normal_default", "PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL"},
		{"sync_full", "PRAGMA journal_mode=WAL; PRAGMA synchronous=FULL"},
		{"sync_off", "PRAGMA journal_mode=WAL; PRAGMA synchronous=OFF"},
	}
	for _, c := range configs {
		b.Run(c.name, func(b *testing.B) {
			b.ReportAllocs()
			for i := 0; i < b.N; i++ {
				dir := b.TempDir()
				dsn := filepath.Join(dir, "wal.db")
				db, err := sql.Open("sqlite", dsn)
				if err != nil {
					b.Fatalf("open: %v", err)
				}
				if _, err := db.Exec(c.pragma); err != nil {
					b.Fatalf("pragma %q: %v", c.pragma, err)
				}
				if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS file (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					absolute_path TEXT UNIQUE,
					content_hash TEXT, line_count INTEGER DEFAULT 0, byte_size INTEGER DEFAULT 0, parse_status TEXT DEFAULT 'parse_ok');
					CREATE TABLE IF NOT EXISTS class (
					id INTEGER PRIMARY KEY AUTOINCREMENT,
					file_id INTEGER, name TEXT, full_name TEXT, type TEXT DEFAULT 'CLASS');`); err != nil {
					b.Fatalf("schema: %v", err)
				}

				start := time.Now()
				tx, err := db.Begin()
				if err != nil {
					b.Fatalf("begin: %v", err)
				}
				stmtFile, err := tx.Prepare(`INSERT INTO file (absolute_path, content_hash, line_count, byte_size) VALUES (?,?,?,?)`)
				if err != nil {
					b.Fatalf("prepare file: %v", err)
				}
				stmtClass, err := tx.Prepare(`INSERT INTO class (file_id, name, full_name) VALUES (?,?,?)`)
				if err != nil {
					b.Fatalf("prepare class: %v", err)
				}
				for j := 0; j < n; j++ {
					path := fmt.Sprintf("/repo/file-%d.go", j)
					res, err := stmtFile.Exec(path, "hash", 50, 1024)
					if err != nil {
						b.Fatalf("insert file %d: %v", j, err)
					}
					id, _ := res.LastInsertId()
					if _, err := stmtClass.Exec(id, fmt.Sprintf("Class%d", j), fmt.Sprintf("pkg.Class%d", j)); err != nil {
						b.Fatalf("insert class %d: %v", j, err)
					}
				}
				_ = stmtClass.Close()
				_ = stmtFile.Close()
				if err := tx.Commit(); err != nil {
					b.Fatalf("commit: %v", err)
				}
				el := time.Since(start)
				_ = db.Close()

				var size int64
				if st, err := os.Stat(dsn); err == nil {
					size = st.Size()
				}
				b.ReportMetric(el.Seconds(), "wall_s")
				b.ReportMetric(float64(size)/1024, "db_kib")
			}
		})
	}
}
