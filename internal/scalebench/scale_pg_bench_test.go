//go:build pg

package scalebench

import (
	"context"
	"fmt"
	"os"
	"runtime"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/parser"
	pgstore "github.com/idcu/codeschema/internal/store/pg"
)

// pgBenchMaxN PG 基准的测量规模上限。
//
// 实测单实例 Docker PG 写吞吐远低于 SQLite：N=10k 单点约 220s（逐语句网络往返 +
// 容器内 fsync，见 scale-bench 结论）。继续测 50k/100k 会让整体基准超 CI/manual 超时，
// 且横向扩展价值在「多实例/查询/索引」而非单点写摄入，故超大 N 只取到 10k 的「大规模」点。
const pgBenchMaxN = 10000

// init 将 main 文件中的 benchPGStores 占位重绑定为真实 PG 基准实现。
// 需 -tags pg 构建（PGStore 受该 build tag 约束）；无此 tag 时走默认占位（不阻断其余后端）。
func init() {
	benchPGStores = benchPGImpl
}

// benchPGImpl 度量 PG 后端的 UpsertIR（逐文件单事务，T3-2）与 BulkUpsert（单事务批量，T3-2，
// P23 之后生产化推荐）落库成本，与 FileStore/SQLite 横向对比——关系型后端大规模横向扩展生产验证。
// 连接串读 CODESCHEMA_PG_DSN（默认本机 compose 实例）；实例不可达或 N>pgBenchMaxN 时返回 Note 跳过。
func benchPGImpl(ctx context.Context, t *testing.T, n int) (upsert, bulk storeResult) {
	if n > pgBenchMaxN {
		note := fmt.Sprintf("PG 基准测量上限 %d（超时考量，横向扩展看增量），跳过", pgBenchMaxN)
		return storeResult{Note: note}, storeResult{Note: note}
	}
	dsn := os.Getenv("CODESCHEMA_PG_DSN")
	if dsn == "" {
		dsn = "postgres://codeschema:codeschema@localhost:5432/codeschema?sslmode=disable"
	}
	skipNote := "PG 不可达（未启动），跳过"

	probe := pgstore.NewPGStore()
	if err := probe.Open(ctx, dsn); err != nil {
		return storeResult{Note: skipNote + "：" + err.Error()}, storeResult{Note: skipNote}
	}
	if err := probe.InitSchema(ctx); err != nil {
		probe.Close()
		return storeResult{Note: "init schema: " + err.Error()}, storeResult{Note: "init schema: " + err.Error()}
	}
	probe.Close()

	runtime.GC()
	var m1 runtime.MemStats
	runtime.ReadMemStats(&m1)

	// UpsertIR：逐文件单事务（每文件 1 类/3 方法/2 调用）
	st := pgstore.NewPGStore()
	if err := st.Open(ctx, dsn); err != nil {
		return storeResult{Note: "open: " + err.Error()}, storeResult{}
	}
	pstart := time.Now()
	for i := 0; i < n; i++ {
		if err := st.UpsertIR(ctx, synthIR(i)); err != nil {
			st.Close()
			return storeResult{Note: "upsert: " + err.Error()}, storeResult{}
		}
	}
	st.Close()
	upsert = storeResult{MS: time.Since(pstart).Seconds() * 1000}

	// BulkUpsert：单事务批量（语义幂等 upsert，跨 N 不残留旧数据）
	irs := make([]*parser.IRDocument, n)
	for i := 0; i < n; i++ {
		irs[i] = synthIR(i)
	}
	bst := pgstore.NewPGStore()
	if err := bst.Open(ctx, dsn); err != nil {
		return upsert, storeResult{Note: "open: " + err.Error()}
	}
	if err := bst.InitSchema(ctx); err != nil {
		bst.Close()
		return upsert, storeResult{Note: "schema: " + err.Error()}
	}
	bstart := time.Now()
	if err := bst.BulkUpsert(ctx, irs); err != nil {
		bst.Close()
		return upsert, storeResult{Note: "bulk: " + err.Error()}
	}
	bulk = storeResult{MS: time.Since(bstart).Seconds() * 1000}
	bst.Close()

	var m2 runtime.MemStats
	runtime.ReadMemStats(&m2)
	upsert.Alloc = float64(m2.TotalAlloc-m1.TotalAlloc) / 1e6
	bulk.Alloc = upsert.Alloc
	return upsert, bulk
}
