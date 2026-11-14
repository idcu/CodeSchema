package service

import (
	"context"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/store"
)

// newTestLinkStormStore 构造含一条命名关联（getOrder ← TestGetOrder）的最小库。
func newTestLinkStormStore() *mockStoreWithTestData {
	return &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/internal/order/service.go", Language: "go"},
			{ID: 2, AbsolutePath: "/project/internal/order/service_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "order.OrderService"}},
			2: {{ID: 20, FileID: 2, Name: "OrderServiceTest", FullName: "order.OrderServiceTest"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {{ID: 100, ClassID: 10, Name: "getOrder", FullName: "order.OrderService.getOrder"}},
			20: {{ID: 200, ClassID: 20, Name: "TestGetOrder", FullName: "order.OrderServiceTest.TestGetOrder"}},
		},
	}
}

// TestFindTestLinks_SnapshotRebuildUnderInvalidationStorm 对抗性用例（场景 1）：
// 重索引进行中（InvalidateDerivedCaches 频繁触发）连续查询测试关联，
// 结果必须与稳定态逐条一致，且不得因代次连续变化返回空集。
func TestFindTestLinks_SnapshotRebuildUnderInvalidationStorm(t *testing.T) {
	svc := NewService(newTestLinkStormStore())
	ctx := context.Background()
	const target = "order.OrderService.getOrder"

	want, err := svc.FindTestLinks(ctx, target, 60)
	if err != nil {
		t.Fatalf("FindTestLinks(baseline): %v", err)
	}
	if len(want) == 0 {
		t.Fatal("基线结果为空，用例前提不成立")
	}

	stormCtx, cancel := context.WithCancel(ctx)
	var storm sync.WaitGroup
	storm.Add(1)
	go func() {
		defer storm.Done()
		for {
			select {
			case <-stormCtx.Done():
				return
			default:
				svc.InvalidateDerivedCaches()
				time.Sleep(100 * time.Microsecond)
			}
		}
	}()

	const readers = 6
	const iterations = 120
	errCh := make(chan error, readers*iterations)
	var wg sync.WaitGroup
	for i := 0; i < readers; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for j := 0; j < iterations; j++ {
				got, err := svc.FindTestLinks(context.Background(), target, 60)
				if err != nil {
					errCh <- err
					return
				}
				if len(got) != len(want) {
					errCh <- errEmptyOrDrift(len(got), len(want))
					return
				}
				if !reflect.DeepEqual(got, want) {
					errCh <- errSnapshotDrift
					return
				}
			}
		}()
	}
	wg.Wait()
	cancel()
	storm.Wait()
	close(errCh)
	for err := range errCh {
		t.Fatal(err)
	}
}

type stormErr string

func (e stormErr) Error() string { return string(e) }

const errSnapshotDrift = stormErr("测试关联结果在失效风暴中发生漂移")

func errEmptyOrDrift(got, want int) error {
	if got == 0 && want > 0 {
		return stormErr("失效风暴中返回空集（代次抖动导致快照未发布）")
	}
	return stormErr("测试关联条数漂移")
}
