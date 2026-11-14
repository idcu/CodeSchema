// 向量快照落盘 A/B：同一份真实语料数据上对比
//
//	(a) 旧实现复刻：整索引 json.MarshalIndent + fsperm.WriteFile（非原子）
//	(b) 新实现：流式 writeJSON + 原子替换
//
// 门控（默认不参与 make test）：
//
//	CODESCHEMA_MEM_ATTRIBUTION=1 MEM_ATTRIBUTION_TAG=<tag> go test -run '^TestMemoryAttribution_FileBackend$' ./internal/scalebench
//	CODESCHEMA_SNAPSHOT_DIR=<结果里的 snapshot_dir> go test -run '^TestPersistentStore_SaveAB$' -v -timeout 900s ./internal/embedding
//
// 旧路径为测试内逐字复刻，不是生产代码。产物：docs/bench/2026-11-14/save-ab-vector.json。
package embedding

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/fsperm"
)

const saveABGate = "CODESCHEMA_SNAPSHOT_DIR"

var abSink []byte

type abSample struct {
	DurationMS float64 `json:"duration_ms"`
	PeakDelta  uint64  `json:"peak_heap_delta_bytes"`
	AllocDelta uint64  `json:"total_alloc_delta_bytes"`
	NumGC      uint32  `json:"num_gc"`
}

type abStats struct {
	MinMS        float64    `json:"min_ms"`
	MedianMS     float64    `json:"median_ms"`
	MaxMS        float64    `json:"max_ms"`
	MinPeakDelta uint64     `json:"min_peak_delta_bytes"`
	MedPeakDelta uint64     `json:"median_peak_delta_bytes"`
	MaxPeakDelta uint64     `json:"max_peak_delta_bytes"`
	MedAlloc     uint64     `json:"median_alloc_bytes"`
	Samples      []abSample `json:"samples"`
}

type abReport struct {
	Component   string  `json:"component"`
	SourceFile  string  `json:"source_file"`
	SourceBytes int64   `json:"source_bytes"`
	Rounds      int     `json:"rounds"`
	GOGC        string  `json:"gogc"`
	LegacySer   abStats `json:"legacy_serialize_only"`
	LegacyFull  abStats `json:"legacy_marshalindent_plus_write"`
	StreamSer   abStats `json:"stream_serialize_only"`
	StreamFull  abStats `json:"stream_atomic_write"`
	Note        string  `json:"note"`
}

// TestPersistentStore_SaveAB 在真实 GOROOT 向量文件上做落盘 A/B 测量。
func TestPersistentStore_SaveAB(t *testing.T) {
	dir := os.Getenv(saveABGate)
	if dir == "" {
		t.Skip("set CODESCHEMA_SNAPSHOT_DIR to the snapshot_dir produced by TestMemoryAttribution_FileBackend")
	}
	src := filepath.Join(dir, "vector", "vector.json")
	if _, err := os.Stat(src); err != nil {
		t.Skipf("vector snapshot not found: %s", src)
	}
	fi, err := os.Stat(src)
	if err != nil {
		t.Fatal(err)
	}
	ps, err := NewPersistentStoreWithModel(src, "", 0)
	if err != nil {
		t.Fatalf("load snapshot: %v", err)
	}
	tmp := t.TempDir()
	report := abReport{
		Component: "vector", SourceFile: src, SourceBytes: fi.Size(),
		Rounds: 5, GOGC: os.Getenv("GOGC"),
		Note: "旧路径为测试内逐字复刻（整索引 MarshalIndent + fsperm.WriteFile）；峰值由 2ms 采样 HeapAlloc 得到，GC 后取基线",
	}

	legacyPath := filepath.Join(tmp, "legacy-vector.json")
	newPath := filepath.Join(tmp, "stream-vector.json")
	ps.filePath = newPath

	legacyData := func() persistentData {
		return persistentData{Vectors: ps.vecs, Contents: ps.contents, ModelID: ps.modelID, Dim: ps.dim}
	}
	legacySerialize := func() error {
		raw, err := json.MarshalIndent(legacyData(), "", "  ")
		if err != nil {
			return err
		}
		abSink = raw
		return nil
	}
	legacyFull := func() error {
		raw, err := json.MarshalIndent(legacyData(), "", "  ")
		if err != nil {
			return err
		}
		abSink = raw
		return fsperm.WriteFile(legacyPath, raw)
	}
	streamSerialize := func() error { return ps.writeJSON(io.Discard) }
	streamFull := func() error { return ps.Save() }

	report.LegacySer = measureAB(t, legacySerialize)
	report.LegacyFull = measureAB(t, legacyFull)
	report.StreamSer = measureAB(t, streamSerialize)
	report.StreamFull = measureAB(t, streamFull)

	out := filepath.Join("..", "..", "docs", "bench", "2026-11-14", "save-ab-vector.json")
	writeAB(t, out, report)
	t.Logf("vector A/B: legacy marshal %.0fms (peak +%.1fMB), stream atomic %.0fms (peak +%.1fMB)",
		report.LegacyFull.MedianMS, float64(report.LegacyFull.MedPeakDelta)/1024/1024,
		report.StreamFull.MedianMS, float64(report.StreamFull.MedPeakDelta)/1024/1024)
}

// measureAB 连续测量 5 轮（丢弃首轮），采样堆峰值与分配量。
func measureAB(t *testing.T, fn func() error) abStats {
	t.Helper()
	const rounds = 5
	samples := make([]abSample, 0, rounds-1)
	for i := 0; i < rounds; i++ {
		s := measureOnce(t, fn)
		if i == 0 {
			continue // 丢弃首轮，规避页缓存/首次分配抖动
		}
		samples = append(samples, s)
	}
	return summarize(samples)
}

func measureOnce(t *testing.T, fn func() error) abSample {
	t.Helper()
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	peak := before.HeapAlloc

	stop := make(chan struct{})
	done := make(chan struct{})
	go func() {
		defer close(done)
		tick := time.NewTicker(2 * time.Millisecond)
		defer tick.Stop()
		var m runtime.MemStats
		for {
			runtime.ReadMemStats(&m)
			if m.HeapAlloc > peak {
				peak = m.HeapAlloc
			}
			select {
			case <-stop:
				return
			case <-tick.C:
			}
		}
	}()

	start := time.Now()
	err := fn()
	dur := time.Since(start)
	close(stop)
	<-done
	runtime.ReadMemStats(&after)
	if err != nil {
		t.Fatalf("measure fn: %v", err)
	}
	return abSample{
		DurationMS: float64(dur.Microseconds()) / 1000,
		PeakDelta:  peak - before.HeapAlloc,
		AllocDelta: after.TotalAlloc - before.TotalAlloc,
		NumGC:      after.NumGC - before.NumGC,
	}
}

func summarize(samples []abSample) abStats {
	durs := make([]float64, len(samples))
	peaks := make([]uint64, len(samples))
	allocs := make([]uint64, len(samples))
	for i, s := range samples {
		durs[i] = s.DurationMS
		peaks[i] = s.PeakDelta
		allocs[i] = s.AllocDelta
	}
	sort.Float64s(durs)
	sort.Slice(peaks, func(i, j int) bool { return peaks[i] < peaks[j] })
	sort.Slice(allocs, func(i, j int) bool { return allocs[i] < allocs[j] })
	return abStats{
		MinMS: durs[0], MedianMS: durs[len(durs)/2], MaxMS: durs[len(durs)-1],
		MinPeakDelta: peaks[0], MedPeakDelta: peaks[len(peaks)/2], MaxPeakDelta: peaks[len(peaks)-1],
		MedAlloc: allocs[len(allocs)/2],
		Samples:  samples,
	}
}

func writeAB(t *testing.T, path string, report abReport) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir out dir: %v", err)
	}
	raw, err := json.MarshalIndent(report, "", "  ")
	if err != nil {
		t.Fatalf("marshal report: %v", err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatalf("write report: %v", err)
	}
}
