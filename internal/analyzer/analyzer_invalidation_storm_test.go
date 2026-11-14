package analyzer

import (
	"context"
	"fmt"
	"sync"
	"testing"
	"time"
)

// TestBuildCallGraph_InvalidationStormNeverReturnsPartial 对抗性用例（场景 1）：
// 重索引进行中（代次频繁变化）连续查询，调用图必须始终是完整图（5 节点 / 4 边），
// 不得返回 nil、空图或构建中的半成品。
//
// 这是"先写用例"的守护：当前失效/发布实现若退化为边构建边发布，本用例会红。
func TestBuildCallGraph_InvalidationStormNeverReturnsPartial(t *testing.T) {
	a := NewAnalyzer(newTestStore())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	var storm sync.WaitGroup
	storm.Add(1)
	go func() {
		defer storm.Done()
		for {
			select {
			case <-ctx.Done():
				return
			default:
				a.InvalidateCallGraph()
				time.Sleep(200 * time.Microsecond)
			}
		}
	}()

	const readers = 8
	const iterations = 150
	errCh := make(chan error, readers*iterations)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func(id int) {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				cg, err := a.BuildCallGraph(context.Background())
				if err != nil {
					errCh <- fmt.Errorf("reader %d: %w", id, err)
					return
				}
				if cg == nil {
					errCh <- fmt.Errorf("reader %d: nil graph", id)
					return
				}
				if cg.NodeCount() != 5 || cg.EdgeCount() != 4 {
					errCh <- fmt.Errorf("reader %d: incomplete graph nodes=%d edges=%d",
						id, cg.NodeCount(), cg.EdgeCount())
					return
				}
			}
		}(i)
	}
	wg.Wait()
	cancel()
	storm.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}

	// 风暴停止后必须能发布并被复用（不因代次抖动永久放弃缓存）。
	if _, err := a.BuildCallGraph(context.Background()); err != nil {
		t.Fatalf("BuildCallGraph after storm: %v", err)
	}
	first, _ := a.BuildCallGraph(context.Background())
	second, _ := a.BuildCallGraph(context.Background())
	if first != second {
		t.Fatal("expected cached graph reuse after invalidation storm settled")
	}
}
