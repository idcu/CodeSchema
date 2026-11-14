package ai

import (
	"context"
	"testing"

	"github.com/idcu/codeschema/internal/store"
)

// TestComputeTagCoverage 验证标签覆盖率统计：实体总数、已打标签数、覆盖率、
// 分类分布与 TopTags 排名均正确。复用 tagger_test.go 的 mockStoreWithTags。
func TestComputeTagCoverage(t *testing.T) {
	ctx := context.Background()

	m := &mockStoreWithTags{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/p/order/service.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {
				{ID: 101, FileID: 1, Name: "OrderService"},
				{ID: 102, FileID: 1, Name: "OrderRepository"},
			},
		},
		methods: map[int64][]store.MethodRecord{
			101: {
				{ID: 1001, ClassID: 101, Name: "CreateOrder"},
				{ID: 1002, ClassID: 101, Name: "GetOrder"},
			},
		},
		classTags: map[int64][]string{
			101: {"service", "biz:order", "go"}, // 分类 layer + biz + lang
			// 102 无标签
		},
		methodTags: map[int64][]string{
			1001: {"transactional", "biz:order"}, // 分类 tech + biz
			// 1002 无标签
		},
	}

	stats, err := ComputeTagCoverage(ctx, m)
	if err != nil {
		t.Fatalf("ComputeTagCoverage: %v", err)
	}

	// 实体统计：类2 + 方法2 = 4；打标签类1（101）+ 打标签方法1（1001）= 2
	if stats.TotalEntities != 4 {
		t.Errorf("TotalEntities = %d, want 4", stats.TotalEntities)
	}
	if stats.TaggedEntities != 2 {
		t.Errorf("TaggedEntities = %d, want 2", stats.TaggedEntities)
	}
	if stats.ClassTotal != 2 || stats.ClassTagged != 1 {
		t.Errorf("class stats: total=%d tagged=%d, want 2/1", stats.ClassTotal, stats.ClassTagged)
	}
	if stats.MethodTotal != 2 || stats.MethodTagged != 1 {
		t.Errorf("method stats: total=%d tagged=%d, want 2/1", stats.MethodTotal, stats.MethodTagged)
	}
	if stats.CoverageRate != 0.5 {
		t.Errorf("CoverageRate = %v, want 0.5", stats.CoverageRate)
	}

	// 分类分布：
	//  - 类101：service(layer)、biz:order(biz)、go(lang) → layer/biz/lang 各+1
	//  - 方法1001：transactional(tech)、biz:order(biz) → tech/biz 各+1
	// 汇总：layer=1, biz=2, lang=1, tech=1；risk=0, test=0
	cc := stats.CategoryCounts
	if cc["layer"] != 1 || cc["biz"] != 2 || cc["lang"] != 1 || cc["tech"] != 1 {
		t.Errorf("CategoryCounts = %v, want layer=1 biz=2 lang=1 tech=1", cc)
	}
	if cc["risk"] != 0 || cc["test"] != 0 {
		t.Errorf("CategoryCounts risk/test should be 0, got %v", cc)
	}

	// 分类命中实体数：layer 仅类101 → 1；biz 类101+方法1001 → 2；lang 仅类101 → 1；tech 仅方法1001 → 1
	ct := stats.CategoryTagged
	if ct["layer"] != 1 || ct["biz"] != 2 || ct["lang"] != 1 || ct["tech"] != 1 {
		t.Errorf("CategoryTagged = %v, want layer=1 biz=2 lang=1 tech=1", ct)
	}

	// TopTags：按频次降序。biz:order 出现2次排第一；其余（service/go/transactional）各1次。
	if len(stats.TopTags) != 4 {
		t.Fatalf("TopTags len = %d, want 4", len(stats.TopTags))
	}
	if stats.TopTags[0].Tag != "biz:order" || stats.TopTags[0].Freq != 2 {
		t.Errorf("TopTags[0] = %+v, want biz:order freq=2", stats.TopTags[0])
	}
}

// TestComputeTagCoverage_EmptyStore 空库统计不崩溃且覆盖率 0。
func TestComputeTagCoverage_EmptyStore(t *testing.T) {
	m := &mockStoreWithTags{} // 无文件/类/方法
	stats, err := ComputeTagCoverage(context.Background(), m)
	if err != nil {
		t.Fatalf("ComputeTagCoverage(empty): %v", err)
	}
	if stats.TotalEntities != 0 || stats.TaggedEntities != 0 {
		t.Errorf("empty store: total=%d tagged=%d, want 0/0", stats.TotalEntities, stats.TaggedEntities)
	}
	if stats.CoverageRate != 0 {
		t.Errorf("empty store CoverageRate = %v, want 0", stats.CoverageRate)
	}
	if len(stats.TopTags) != 0 {
		t.Errorf("empty store TopTags = %v, want empty", stats.TopTags)
	}
}
