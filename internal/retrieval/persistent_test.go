package retrieval

import (
	"context"
	"fmt"
	"path/filepath"
	"sync"
	"testing"
	"time"
)

// TestPersistentFTS_ConcurrentIndexSearch 负控（P1-10）：
// Search 不得在释放 RLock 后遍历共享 docs map，否则并发 Index/BatchIndex
// 会触发 Go fatal「concurrent map read and map write」（不可 recover）。
func TestPersistentFTS_ConcurrentIndexSearch(t *testing.T) {
	pf, err := NewPersistentFTS(filepath.Join(t.TempDir(), "fts.json"))
	if err != nil {
		t.Fatalf("new persistent fts: %v", err)
	}
	ctx := context.Background()
	for i := 0; i < 500; i++ {
		if err := pf.Index(ctx, fmt.Sprintf("doc-%d", i), "alpha beta gamma"); err != nil {
			t.Fatalf("seed index: %v", err)
		}
	}

	done := make(chan struct{})
	var wg sync.WaitGroup

	// 写侧：持续批量索引新文档。
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; ; i++ {
			select {
			case <-done:
				return
			default:
			}
			if err := pf.BatchIndex(ctx, []string{fmt.Sprintf("new-%d", i)}, []string{"alpha delta"}); err != nil {
				return
			}
		}
	}()

	// 读侧：持续全文搜索（遍历 docs map）。
	for w := 0; w < 4; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				select {
				case <-done:
					return
				default:
				}
				if _, err := pf.Search(ctx, "alpha", FTSModeFuzzy, 10); err != nil {
					return
				}
			}
		}()
	}

	time.Sleep(300 * time.Millisecond)
	close(done)
	wg.Wait()
}
