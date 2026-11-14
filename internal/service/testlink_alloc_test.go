package service

import (
	"context"
	"fmt"
	"os"
	"testing"

	"github.com/idcu/codeschema/internal/store"
)

// maxAllocsPerSameTagQuery 是单次关联查询允许的堆分配次数上限。
//
// 该值与结果条数同阶（360 个匹配测试），但不随测试类数量线性增长：
// 修复前每个候选都重新切词/建 Builder，实测 8,317 次/查询；预计算后 38 次。
// 若有人重新引入「逐测试类切词」，这里会立刻回到数千次。
const maxAllocsPerSameTagQuery = 64

// TestFindTestLinksSameTagAllocs 锁定 same_tag 热路径的分配量：
// 测试名切词、词连接集合与标签集合必须在快照上预计算，符号循环只做查表。
//
// 先红后绿的证据见 docs/bench/2026-11-14/perf-baseline.md §8.8。
func TestFindTestLinksSameTagAllocs(t *testing.T) {
	if os.Getenv("CODESCHEMA_TESTLINK_ALLOC_GUARD") == "" {
		t.Skip("set CODESCHEMA_TESTLINK_ALLOC_GUARD=1 to run allocation guard")
	}
	const testClasses = 120
	const methodsPerClass = 3

	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/repo/order/service.go", Language: "go"},
			{ID: 2, AbsolutePath: "/repo/order/all_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "example.com/order.OrderService", Type: "CLASS"}},
			2: make([]store.ClassRecord, 0, testClasses),
		},
		methods: map[int64][]store.MethodRecord{
			10: {{ID: 100, ClassID: 10, Name: "getOrder", FullName: "example.com/order.OrderService.getOrder"}},
		},
		classTags: map[int64][]string{10: {"order"}},
	}
	for i := 0; i < testClasses; i++ {
		classID := int64(1000 + i)
		st.classes[2] = append(st.classes[2], store.ClassRecord{
			ID: classID, FileID: 2,
			Name:     fmt.Sprintf("Case%dTest", i),
			FullName: fmt.Sprintf("example.com/order.Case%dTest", i),
			Type:     "CLASS",
		})
		methods := make([]store.MethodRecord, 0, methodsPerClass)
		for j := 0; j < methodsPerClass; j++ {
			name := fmt.Sprintf("TestWhenGetOrderShouldReturn%dCase%d", j, i)
			methods = append(methods, store.MethodRecord{
				ID:       classID*10 + int64(j),
				ClassID:  classID,
				Name:     name,
				FullName: fmt.Sprintf("example.com/order.Case%dTest.%s", i, name),
			})
		}
		st.methods[classID] = methods
		st.classTags[classID] = []string{"order"}
	}

	svc := NewService(st)
	ctx := context.Background()
	const target = "example.com/order.OrderService.getOrder"
	if _, err := svc.FindTestLinks(ctx, target, 60); err != nil { // 预热快照
		t.Fatalf("warm up: %v", err)
	}

	allocs := testing.AllocsPerRun(10, func() {
		if _, err := svc.FindTestLinks(ctx, target, 60); err != nil {
			t.Fatalf("find test links: %v", err)
		}
	})
	if allocs > maxAllocsPerSameTagQuery {
		t.Fatalf("same_tag 单次查询分配 %.0f 次，超过上限 %d："+
			"测试名切词/标签集合应在快照上预计算，而不是逐符号重建",
			allocs, maxAllocsPerSameTagQuery)
	}
	t.Logf("allocs/query=%.0f (limit %d)", allocs, maxAllocsPerSameTagQuery)
}
