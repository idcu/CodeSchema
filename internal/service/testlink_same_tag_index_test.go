package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/idcu/codeschema/internal/store"
)

// TestSameTagIndexMatchesBruteForce 锁定预计算索引与逐条匹配函数的语义等价。
//
// same_tag 的查询路径不再调用 matchesTestMethod，而是查四张反向表；
// 这里对全部 (目标, 测试名) 组合逐一比对，避免索引漏掉某条命名规则或改变顺序。
//
// 本文件不在人工真值数据集的标注范围内（见 manual-truth.json 的 test_files），
// 避免守护用例本身给 /tests 增加人工口径 pair。
func TestSameTagIndexMatchesBruteForce(t *testing.T) {
	testNames := []string{
		"getOrder", "TestGetOrder", "testGetOrder", "test_get_order",
		"ShouldReturnOrder", "TestWhenGetOrderShouldReturn",
		"processCreationFormSuccess", "addPetAddsPersistedPet",
		"validateWithInvalidBirthDate", "TestAnalyzer_BuildAll",
		"getById", "TestProcessPayment", "when_get_order_called",
		"create_order", "BenchmarkGetOrder", "benchmarkGetOrder",
		"ExampleGetOrder", "Example_get_order", "exampleGetOrder",
		"TestGetOrder_Extra", "getOrderExtra", "Test_get_order",
		"TestOrderService_GetOrder", "TestGetOrderShould_Return",
		"TestWhenGetOrderShouldReturn0Case0", "get-order", "GET_ORDER",
	}
	targets := []string{
		"getOrder", "GetOrder", "get_order", "GET_ORDER", "ReturnOrder",
		"processPayment", "processCreationForm", "addPet", "validate",
		"Analyze", "get", "TestGetOrder", "Order", "getOrderExtra",
		"TestGetOrder_Extra", "test_get_order", "get-order", "",
	}

	methods := make([]store.MethodRecord, 0, len(testNames))
	for i, name := range testNames {
		methods = append(methods, store.MethodRecord{
			ID: int64(i + 1), ClassID: 1, Name: name,
			FullName: "example.com/order.CaseTest." + name,
		})
	}
	snap := &testLinkSnapshot{
		st:          &mockStoreWithTestData{classTags: map[int64][]string{1: {"order"}}},
		tagsByClass: map[int64][]string{},
		tagsLoaded:  map[int64]bool{},
		testOwners:  []*testLinkClassOwner{{cls: store.ClassRecord{ID: 1, Name: "CaseTest"}, methods: methods}},
	}
	idx := snap.sameTagIndex(context.Background())

	for _, target := range targets {
		gotNames := make([]string, 0, 8)
		for _, pos := range idx.candidates(target, nil) {
			gotNames = append(gotNames, testNames[pos])
		}
		wantNames := make([]string, 0, 8)
		for _, name := range testNames {
			if matchesTestMethod(target, name) {
				wantNames = append(wantNames, name)
			}
		}
		if !reflect.DeepEqual(gotNames, wantNames) {
			t.Errorf("candidates(%q) = %v，逐条匹配 = %v（顺序也必须一致）",
				target, gotNames, wantNames)
		}
	}
}
