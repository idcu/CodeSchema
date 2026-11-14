package service

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/idcu/codeschema/internal/store"
)

// 本文件锁定测试关联的「逐条一致」与「索引失效」契约：
//   - golden：用改动前实现生成的完整输出作为基线，任何策略/顺序变化都会失败；
//   - 负控：删除测试后必须消失，新增测试后必须出现（含不显式失效的指纹路径）。

// goldenCase 单个查询的完整输出（links 顺序 + filtered 计数）。
type goldenCase struct {
	Target        string     `json:"target"`
	MinConfidence int        `json:"min_confidence"`
	Links         []TestLink `json:"links"`
	Filtered      int        `json:"filtered"`
}

// goldenFile golden 文件结构。
type goldenFile struct {
	Version int          `json:"version"`
	Corpus  string       `json:"corpus"`
	Cases   []goldenCase `json:"cases"`
}

// goldenTargets 覆盖五策略与边界（歧义、重载、跨类调用、显式注解、导入依赖）。
var goldenTargets = []string{
	"com.example.OrderService.getOrder",
	"com.example.OrderService.createOrder",
	"com.example.OrderService.cancel",
	"com.example.PaymentService.pay",
	"com.example.JavaResolver.Resolve",
	"com.example.GradleResolver.Resolve",
}

// newGoldenStore 构造覆盖五策略的混合语料。
func newGoldenStore() *mockStoreWithTestData {
	return &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/repo/order/service.go", Language: "go", ContentHash: "h1"},
			{ID: 2, AbsolutePath: "/repo/order/service_test.go", Language: "go", ContentHash: "h2"},
			{ID: 3, AbsolutePath: "/repo/payment/payment.go", Language: "go", ContentHash: "h3"},
			{ID: 4, AbsolutePath: "/repo/payment/payment_test.go", Language: "go", ContentHash: "h4"},
			{ID: 5, AbsolutePath: "/repo/explicit/ExplicitTest.java", Language: "java", ContentHash: "h5"},
			{ID: 6, AbsolutePath: "/repo/cross/cross.spec.ts", Language: "ts", ContentHash: "h6"},
			{ID: 7, AbsolutePath: "/repo/order/helper_test.go", Language: "go", ContentHash: "h7"},
			{ID: 8, AbsolutePath: "/repo/consumer/consumer_test.go", Language: "go", ContentHash: "h8", Imports: []string{"com.example.order"}},
			{ID: 9, AbsolutePath: "/repo/ambiguous/a.go", Language: "go", ContentHash: "h9"},
			{ID: 10, AbsolutePath: "/repo/ambiguous/a_test.go", Language: "go", ContentHash: "h10"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "com.example.OrderService", Type: "CLASS"}},
			2: {
				{ID: 20, FileID: 2, Name: "order", FullName: "example.com/order", Type: "PACKAGE"},
				{ID: 21, FileID: 2, Name: "OrderServiceTest", FullName: "com.example.OrderServiceTest", Type: "CLASS"},
			},
			3: {{ID: 30, FileID: 3, Name: "PaymentService", FullName: "com.example.PaymentService", Type: "CLASS"}},
			4: {{ID: 31, FileID: 4, Name: "PaymentServiceTest", FullName: "com.example.PaymentServiceTest", Type: "CLASS"}},
			5: {{ID: 40, FileID: 5, Name: "ExplicitTest", FullName: "com.example.ExplicitTest", Type: "CLASS", Doc: "@TestFor(com.example.OrderService)"}},
			6: {{ID: 50, FileID: 6, Name: "CrossSpec", FullName: "CrossSpec", Type: "CLASS"}},
			7: {{ID: 60, FileID: 7, Name: "order", FullName: "example.com/order", Type: "PACKAGE"}},
			8: {{ID: 70, FileID: 8, Name: "ConsumerTest", FullName: "com.example.ConsumerTest", Type: "CLASS"}},
			9: {
				{ID: 80, FileID: 9, Name: "JavaResolver", FullName: "com.example.JavaResolver", Type: "CLASS"},
				{ID: 81, FileID: 9, Name: "GradleResolver", FullName: "com.example.GradleResolver", Type: "CLASS"},
			},
			10: {{ID: 82, FileID: 10, Name: "ambiguous", FullName: "com.example/ambiguous", Type: "PACKAGE"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {
				{ID: 100, ClassID: 10, Name: "getOrder", FullName: "com.example.OrderService.getOrder"},
				{ID: 101, ClassID: 10, Name: "createOrder", FullName: "com.example.OrderService.createOrder"},
				{ID: 102, ClassID: 10, Name: "cancel", FullName: "com.example.OrderService.cancel"},
				// 重载：同一 FQN 注册两次，验证短名唯一性按 FQN 去重。
				{ID: 103, ClassID: 10, Name: "createOrder", FullName: "com.example.OrderService.createOrder"},
			},
			20: {
				{ID: 200, ClassID: 20, Name: "TestGetOrder", FullName: "example.com/order.TestGetOrder"},
				{ID: 201, ClassID: 20, Name: "TestCreateOrder", FullName: "example.com/order.TestCreateOrder"},
				{ID: 202, ClassID: 20, Name: "helper", FullName: "example.com/order.helper"},
			},
			21: {
				{ID: 210, ClassID: 21, Name: "TestGetOrder", FullName: "com.example.OrderServiceTest.TestGetOrder"},
				{ID: 211, ClassID: 21, Name: "TestCancel_FromTestClass", FullName: "com.example.OrderServiceTest.TestCancel_FromTestClass"},
			},
			30: {{ID: 300, ClassID: 30, Name: "pay", FullName: "com.example.PaymentService.pay"}},
			31: {{ID: 310, ClassID: 31, Name: "TestPay", FullName: "com.example.PaymentServiceTest.TestPay"}},
			40: {
				{ID: 400, ClassID: 40, Name: "checksOrderDoc", FullName: "com.example.ExplicitTest.checksOrderDoc"},
				{ID: 401, ClassID: 40, Name: "checksOrderState", FullName: "com.example.ExplicitTest.checksOrderState"},
			},
			50: {{ID: 500, ClassID: 50, Name: "works", FullName: "CrossSpec.works"}},
			60: {
				{ID: 600, ClassID: 60, Name: "TestHelperCallingCancel", FullName: "example.com/order.TestHelperCallingCancel"},
				{ID: 601, ClassID: 60, Name: "helper2", FullName: "example.com/order.helper2"},
			},
			70: {{ID: 700, ClassID: 70, Name: "TestConsume", FullName: "com.example.ConsumerTest.TestConsume"}},
			80: {{ID: 800, ClassID: 80, Name: "Resolve", FullName: "com.example.JavaResolver.Resolve"}},
			81: {{ID: 801, ClassID: 81, Name: "Resolve", FullName: "com.example.GradleResolver.Resolve"}},
			82: {{ID: 820, ClassID: 82, Name: "TestResolve", FullName: "com.example/ambiguous.TestResolve"}},
		},
		calls: map[int64][]store.CallRecord{
			2: {
				{CallerFQN: "example.com/order.TestGetOrder", CalleeFQN: "com.example.OrderService.getOrder", CallType: "direct", LineNumber: 10},
				{CallerFQN: "example.com/order.TestCreateOrder", CalleeFQN: "com.example.OrderService.createOrder", CallType: "direct", LineNumber: 20},
				{CallerFQN: "com.example.OrderServiceTest.TestGetOrder", CalleeFQN: "com.example.OrderService.getOrder", CallType: "direct", LineNumber: 30},
			},
			6: {
				{CallerFQN: "CrossSpec.works", CalleeFQN: "com.example.PaymentService.pay", CallType: "direct", LineNumber: 5},
			},
			7: {
				{CallerFQN: "example.com/order.TestHelperCallingCancel", CalleeFQN: "com.example.OrderService.cancel", CallType: "direct", LineNumber: 8},
			},
			10: {
				{CallerFQN: "com.example/ambiguous.TestResolve", CalleeFQN: "com.example.JavaResolver.Resolve", CallType: "direct", LineNumber: 9},
			},
		},
		classTags: map[int64][]string{
			10: {"order"},
			20: {"order"},
			21: {"order"},
			30: {"payment"},
			31: {"payment"},
		},
	}
}

// TestFindTestLinksGolden 逐条比对改动前后的完整关联输出。
//
// 更新基线：UPDATE_TESTLINK_GOLDEN=1 go test ./internal/service -run TestFindTestLinksGolden
// 基线必须用「改动前」实现生成并提交，改动后只读比对。
func TestFindTestLinksGolden(t *testing.T) {
	svc := NewService(newGoldenStore())
	ctx := context.Background()

	got := goldenFile{Version: 1, Corpus: "testlink-golden", Cases: []goldenCase{}}
	for _, target := range goldenTargets {
		for _, mc := range []int{60, 80, 100} {
			links, filtered, err := svc.FindTestLinksWithStats(ctx, target, mc)
			if err != nil {
				t.Fatalf("find %s (min=%d): %v", target, mc, err)
			}
			if links == nil {
				links = []TestLink{}
			}
			got.Cases = append(got.Cases, goldenCase{
				Target: target, MinConfidence: mc, Links: links, Filtered: filtered,
			})
		}
	}

	path := filepath.Join("testdata", "testlink_golden.json")
	if os.Getenv("UPDATE_TESTLINK_GOLDEN") == "1" {
		if err := os.MkdirAll("testdata", 0o755); err != nil {
			t.Fatalf("mkdir testdata: %v", err)
		}
		buf, err := json.MarshalIndent(got, "", "  ")
		if err != nil {
			t.Fatalf("marshal golden: %v", err)
		}
		if err := os.WriteFile(path, append(buf, '\n'), 0o644); err != nil {
			t.Fatalf("write golden: %v", err)
		}
		t.Logf("golden updated: %s (%d cases)", path, len(got.Cases))
		return
	}

	buf, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read golden (generate with UPDATE_TESTLINK_GOLDEN=1): %v", err)
	}
	var want goldenFile
	if err := json.Unmarshal(buf, &want); err != nil {
		t.Fatalf("parse golden: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		gotJSON, _ := json.MarshalIndent(got, "", "  ")
		wantJSON, _ := json.MarshalIndent(want, "", "  ")
		t.Fatalf("test link 输出与基线不一致\n--- got ---\n%s\n--- want ---\n%s", gotJSON, wantJSON)
	}
}

// TestFindTestLinksNegativeControl 证明索引不会让删除/新增测试“隐身”。
func TestFindTestLinksNegativeControl(t *testing.T) {
	ctx := context.Background()
	const target = "com.example.OrderService.getOrder"

	hasLink := func(links []TestLink, testMethod string) bool {
		for _, l := range links {
			if l.TestMethod == testMethod {
				return true
			}
		}
		return false
	}
	assertLinks := func(t *testing.T, svc *Service, want bool, testMethod string) {
		t.Helper()
		links, err := svc.FindTestLinks(ctx, target, 60)
		if err != nil {
			t.Fatalf("find test links: %v", err)
		}
		if got := hasLink(links, testMethod); got != want {
			t.Fatalf("%s 可见性=%v，期望 %v；links=%+v", testMethod, got, want, links)
		}
	}

	// 删除整个测试文件（生产删除路径：DeleteFile + InvalidateDerivedCaches）。
	t.Run("delete_with_invalidation", func(t *testing.T) {
		st := newGoldenStore()
		svc := NewService(st)
		assertLinks(t, svc, true, "com.example.OrderServiceTest.TestGetOrder")

		removeStoreFile(st, 2)
		svc.InvalidateDerivedCaches()
		assertLinks(t, svc, false, "com.example.OrderServiceTest.TestGetOrder")
	})

	// 删除但不显式失效：文件指纹变化必须自愈，不能返回上一代快照。
	t.Run("delete_without_invalidation", func(t *testing.T) {
		st := newGoldenStore()
		svc := NewService(st)
		assertLinks(t, svc, true, "example.com/order.TestGetOrder")

		removeStoreFile(st, 2)
		assertLinks(t, svc, false, "example.com/order.TestGetOrder")
	})

	// 新增测试文件（生产新增路径：UpsertIR + InvalidateDerivedCaches）。
	t.Run("add_with_invalidation", func(t *testing.T) {
		st := newGoldenStore()
		svc := NewService(st)
		assertLinks(t, svc, false, "com.example.TestOrderService.TestGetOrder")

		addTestFile(st, 11, "/repo/order/new_test.go", 90, "TestOrderService",
			"com.example.TestOrderService", 900, "TestGetOrder",
			"com.example.TestOrderService.TestGetOrder", "com.example.OrderService.getOrder")
		svc.InvalidateDerivedCaches()
		assertLinks(t, svc, true, "com.example.TestOrderService.TestGetOrder")
	})

	// 新增但不显式失效：同样必须可见（指纹自愈）。
	t.Run("add_without_invalidation", func(t *testing.T) {
		st := newGoldenStore()
		svc := NewService(st)
		assertLinks(t, svc, false, "com.example.TestOrderService2.TestGetOrder")

		addTestFile(st, 12, "/repo/order/new2_test.go", 91, "TestOrderService",
			"com.example.TestOrderService2", 910, "TestGetOrder",
			"com.example.TestOrderService2.TestGetOrder", "com.example.OrderService.getOrder")
		assertLinks(t, svc, true, "com.example.TestOrderService2.TestGetOrder")
	})
}

// removeStoreFile 模拟 DeleteFile：文件、类、方法、调用一并移除。
func removeStoreFile(st *mockStoreWithTestData, fileID int64) {
	files := st.files[:0]
	for _, f := range st.files {
		if f.ID != fileID {
			files = append(files, f)
		}
	}
	st.files = files

	for _, cls := range st.classes[fileID] {
		delete(st.methods, cls.ID)
		delete(st.classTags, cls.ID)
	}
	delete(st.classes, fileID)
	delete(st.calls, fileID)
}

// addTestFile 追加一个 Go 测试文件及其测试类/方法/调用边。
func addTestFile(st *mockStoreWithTestData, fileID int64, path string, classID int64, className, classFQN string, methodID int64, methodName, methodFQN, calleeFQN string) {
	st.files = append(st.files, &store.FileRecord{
		ID: fileID, AbsolutePath: path, Language: "go", ContentHash: "h" + methodFQN,
	})
	st.classes[fileID] = []store.ClassRecord{{
		ID: classID, FileID: fileID, Name: className, FullName: classFQN, Type: "CLASS",
	}}
	st.methods[classID] = []store.MethodRecord{{
		ID: methodID, ClassID: classID, Name: methodName, FullName: methodFQN,
	}}
	st.calls[fileID] = []store.CallRecord{{
		CallerFQN: methodFQN, CalleeFQN: calleeFQN, CallType: "direct", LineNumber: 1,
	}}
}
