// file 后端内存归因（结构性问题 2）。
//
// 门控（默认不参与 make test）：
//
//	CODESCHEMA_MEM_ATTRIBUTION=1 go test -run '^TestMemoryAttribution_FileBackend$' -v -timeout 1800s ./internal/scalebench
//
// 输出：
//   - docs/bench/2026-11-14/mem-attribution-large.json（分阶段 live heap 与峰值）
//   - 临时目录下的 heap profile（steady / peak-*），供 `go tool pprof -top -inuse_space` 按类型归因
//
// 仅做测量，不修改任何生产行为。
package scalebench

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"runtime/pprof"
	"testing"
	"time"

	rt "github.com/idcu/codeschema/internal/runtime"
	"github.com/idcu/codeschema/internal/store"
)

const memAttributionGate = "CODESCHEMA_MEM_ATTRIBUTION"

type attributionResult struct {
	Corpus  string            `json:"corpus"`
	Root    string            `json:"root"`
	Files   int               `json:"files"`
	LoC     int               `json:"loc"`
	Stages  map[string]uint64 `json:"stages_bytes"`
	Notes   []string          `json:"notes"`
	Profile []string          `json:"profile_files"`
}

// TestMemoryAttribution_FileBackend 分阶段测量 file 后端在 large 语料上的堆内存：
// 打开存储后基线 → scan 后（IR 入库）→ BuildIndex 峰值 → 索引完成后常驻。
func TestMemoryAttribution_FileBackend(t *testing.T) {
	if os.Getenv(memAttributionGate) == "" {
		t.Skip("set CODESCHEMA_MEM_ATTRIBUTION=1 to run the file-backend memory attribution")
	}
	c := perfCorpora()[3]
	if _, err := os.Stat(c.Root); err != nil {
		t.Skipf("corpus not found: %s", c.Root)
	}
	scanRoot := perfScanRoot(t, c.Root)
	files, _, loc, _ := statPerfCorpus(scanRoot, c.Exts)

	dir := t.TempDir()
	profileDir := filepath.Join(os.TempDir(), "codeschema-mem-attribution")
	if err := os.MkdirAll(profileDir, 0o755); err != nil {
		t.Fatalf("mkdir profile dir: %v", err)
	}
	// 清理上一轮同类 profile，避免误读旧数据。
	old, _ := filepath.Glob(filepath.Join(profileDir, "*.pprof"))
	for _, p := range old {
		_ = os.Remove(p)
	}

	cfg := perfConfig(c, dir)
	cfg.Project.Root = scanRoot
	st := store.NewStore("file")
	if err := st.Open(context.Background(), cfg.Storage.DSN); err != nil {
		t.Fatalf("open store: %v", err)
	}
	defer st.Close()

	res := attributionResult{
		Corpus: c.Key, Root: c.Root, Files: files, LoC: loc,
		Stages: map[string]uint64{},
		Notes: []string{
			"live heap = runtime.GC() 后 HeapAlloc，仅本进程、不含子进程与 OS 页缓存",
			"peak 由 20ms 采样得到；peak-*.pprof 为构建期按新峰值抓取的堆快照",
		},
	}

	res.Stages["baseline_open"] = liveHeapStable()

	ctx := context.Background()
	// 峰值采样覆盖 scan + BuildIndex 全流程：1.9GiB 峰值是两阶段叠加的结果。
	sampler := startHeapPeakSampler(profileDir, 20*time.Millisecond, 12, 200*time.Millisecond)
	if err := rt.ScanRepository(ctx, st, cfg, scanRoot); err != nil {
		t.Logf("scan returned non-nil (per-file errors aggregated): %v", err)
	}
	// 这里是「scan 结束后的常驻」而不是整体峰值：主动 GC 会打断 scan 期垃圾，
	// 从而让下一次峰值反映 BuildIndex 自身的分配。
	res.Stages["after_scan"] = liveHeapStable()
	profSteady := filepath.Join(profileDir, "after-scan.pprof")
	writeHeapProfile(t, profSteady)

	run, err := rt.BuildRuntime(ctx, st, cfg)
	peak, peakProfile, snaps := sampler.Stop()
	if err != nil {
		t.Fatalf("build runtime: %v", err)
	}
	res.Stages["build_peak"] = peak
	// 等待异步落盘/索引收尾释放临时缓冲，再测「索引完成后的常驻内存」。
	// 否则会把仍在飞行中的 JSON 快照缓冲误记为常驻。
	time.Sleep(500 * time.Millisecond)
	res.Stages["after_index"] = liveHeapStable()
	profPeak := peakProfile
	if profPeak != "" {
		res.Profile = append(res.Profile, profPeak)
	}
	res.Profile = append(res.Profile, profSteady)
	res.Profile = append(res.Profile, snaps...)

	steadyProfile := filepath.Join(profileDir, "after-index.pprof")
	writeHeapProfile(t, steadyProfile)
	res.Profile = append(res.Profile, steadyProfile)

	// 调用图缓存驻留（结构性问题 1）的精确增量：索引后 GC → 首次建图 → GC。
	// GC 后 live heap 有数 MB 抖动，取 3 次最小值，负增量按 0 记录并注明。
	beforeGraph := liveHeapStable()
	cg, err := run.Analyzer.BuildCallGraph(ctx)
	if err != nil {
		t.Fatalf("warm call graph: %v", err)
	}
	time.Sleep(200 * time.Millisecond)
	afterGraph := liveHeapStable()
	profAfterGraph := filepath.Join(profileDir, "after-graph.pprof")
	writeHeapProfile(t, profAfterGraph)
	res.Profile = append(res.Profile, profAfterGraph)
	t.Logf("call graph: nodes=%d edges=%d before=%.1fMB after=%.1fMB",
		cg.NodeCount(), cg.EdgeCount(), mb(beforeGraph), mb(afterGraph))
	res.Stages["before_call_graph_cache"] = beforeGraph
	res.Stages["after_call_graph_cache"] = afterGraph
	graphDelta := int64(afterGraph) - int64(beforeGraph)
	if graphDelta < 0 {
		graphDelta = 0
		res.Notes = append(res.Notes, "调用图驻留增量低于 GC 抖动，按 0 记录")
	}
	res.Stages["call_graph_retained"] = uint64(graphDelta)

	outDir := filepath.Join(repoRoot(), perfOutSubdir)
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		t.Fatalf("mkdir out dir: %v", err)
	}
	writePerfJSON(t, filepath.Join(outDir, memAttributionFile()), res)

	t.Logf("attribution: baseline=%.1fMB afterScan=%.1fMB buildPeak=%.1fMB afterIndex=%.1fMB graphRetained=%.1fMB",
		mb(res.Stages["baseline_open"]), mb(res.Stages["after_scan"]), mb(res.Stages["build_peak"]),
		mb(res.Stages["after_index"]), mb(res.Stages["call_graph_retained"]))
	t.Logf("profiles in %s", profileDir)
	// 防止 GC 在最后一次使用后回收 run（Searcher/FTS/向量），
	// 否则「建图后常驻内存」会把整个检索索引误算成被调用图替换/释放。
	runtime.KeepAlive(run)
}

// memAttributionFile 允许用 MEM_ATTRIBUTION_TAG 区分实验批次（如 gogc20），
// 默认写 mem-attribution-large.json。
func memAttributionFile() string {
	if tag := os.Getenv("MEM_ATTRIBUTION_TAG"); tag != "" {
		return "mem-attribution-large-" + tag + ".json"
	}
	return "mem-attribution-large.json"
}

// startHeapPeakSampler 每 interval 采样 HeapAlloc；每出现新高且距上次写盘超过
// minGap 时写一份堆快照（最多 maxProfiles 份），用于事后按类型归因。
func startHeapPeakSampler(dir string, interval time.Duration, maxProfiles int, minGap time.Duration) *heapPeakSampler {
	s := &heapPeakSampler{stop: make(chan struct{}), done: make(chan struct{}), dir: dir, max: maxProfiles, minGap: minGap}
	go func() {
		defer close(s.done)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > s.peak {
				s.peak = m.HeapAlloc
				if s.n < s.max && time.Since(s.lastWrite) >= s.minGap {
					s.n++
					s.lastWrite = time.Now()
					path := filepath.Join(dir, fmt.Sprintf("peak-%02d.pprof", s.n))
					if err := writeHeapProfileFile(path); err == nil {
						s.paths = append(s.paths, path)
						s.peakProfile = path
					}
				}
			}
			select {
			case <-s.stop:
				return
			case <-ticker.C:
			}
		}
	}()
	return s
}

type heapPeakSampler struct {
	stop        chan struct{}
	done        chan struct{}
	dir         string
	max         int
	minGap      time.Duration
	peak        uint64
	peakProfile string
	paths       []string
	n           int
	lastWrite   time.Time
}

func (s *heapPeakSampler) Stop() (uint64, string, []string) {
	close(s.stop)
	<-s.done
	return s.peak, s.peakProfile, s.paths
}

func writeHeapProfile(t *testing.T, path string) {
	t.Helper()
	if err := writeHeapProfileFile(path); err != nil {
		t.Fatalf("write heap profile %s: %v", path, err)
	}
}

func writeHeapProfileFile(path string) error {
	f, err := os.Create(path)
	if err != nil {
		return err
	}
	defer f.Close()
	return pprof.WriteHeapProfile(f)
}

func mb(b uint64) float64 { return float64(b) / 1024 / 1024 }

// liveHeapStable 连续 3 次 GC 后取 HeapAlloc 最小值，降低测量抖动。
func liveHeapStable() uint64 {
	var min uint64
	for i := 0; i < 3; i++ {
		runtime.GC()
		var m runtime.MemStats
		runtime.ReadMemStats(&m)
		if i == 0 || m.HeapAlloc < min {
			min = m.HeapAlloc
		}
	}
	return min
}
