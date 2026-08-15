// 百万级（N=100万）SQLite BulkUpsert 实测（P9_2 未做项）。
//
// 背景：现有全量基准 TestScaleBench 到 100k；百万级常态化受 CI 时长约束
// （CI bench job 用 -run '^$' + -bench=BenchmarkScaleBulk 只跑 1 万回归）。
// 本基准显式验证 100 万文件单事务落库成本，支撑「亿级走 PG」的规模决策边界。
//
// 运行（本地，受内存约束建议 >=16GB）：
//   CODESCHEMA_SCALE_BENCH=1 go test -bench=BenchmarkScaleBulkMillion -benchtime=1x -timeout 900s ./internal/scalebench
package scalebench

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/parser"
	sqlitestore "github.com/idcu/codeschema/internal/store/sqlite"
)

const millionN = 1000000

// BenchmarkScaleBulkMillion 100 万文件 SQLite BulkUpsert（单事务）实测。
func BenchmarkScaleBulkMillion(b *testing.B) {
	if os.Getenv("CODESCHEMA_SCALE_BENCH") == "" {
		b.Skip("set CODESCHEMA_SCALE_BENCH=1 to run the 1M bulk bench (local, memory-heavy)")
	}
	for i := 0; i < b.N; i++ {
		dsn := filepath.Join(b.TempDir(), "million.db")
		st := sqlitestore.NewSQLiteStore()
		if err := st.Open(context.Background(), dsn); err != nil {
			b.Fatalf("open: %v", err)
		}
		irs := make([]*parser.IRDocument, millionN)
		for j := 0; j < millionN; j++ {
			irs[j] = synthIR(j)
		}
		runtime.GC()
		var m1 runtime.MemStats
		runtime.ReadMemStats(&m1)
		start := time.Now()
		if err := st.BulkUpsert(context.Background(), irs); err != nil {
			b.Fatalf("bulk upsert: %v", err)
		}
		el := time.Since(start)
		var m2 runtime.MemStats
		runtime.ReadMemStats(&m2)
		_ = st.Close()
		var size int64
		if f, err := os.Stat(dsn); err == nil {
			size = f.Size()
		}
		b.ReportMetric(el.Seconds(), "bulk_wall_s")
		b.ReportMetric(float64(m2.TotalAlloc-m1.TotalAlloc)/1e6, "alloc_mb")
		b.ReportMetric(float64(size)/1e6, "db_mb")
		b.ReportMetric(el.Seconds()/float64(millionN)*1e9, "ns_per_file")
	}
}
