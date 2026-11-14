package service

import (
	"context"
	"reflect"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// mockStoreWithTestData 实现 Store 接口，用于测试关联测试。
type mockStoreWithTestData struct {
	files      []*store.FileRecord
	classes    map[int64][]store.ClassRecord
	methods    map[int64][]store.MethodRecord
	calls      map[int64][]store.CallRecord
	classTags  map[int64][]string
	methodTags map[int64][]string
	tagCats    map[string]string
}

func (m *mockStoreWithTestData) Open(_ context.Context, _ string) error { return nil }
func (m *mockStoreWithTestData) Close() error                           { return nil }
func (m *mockStoreWithTestData) HealthCheck(_ context.Context) error    { return nil }
func (m *mockStoreWithTestData) UpsertFile(_ context.Context, _ string, _ string, _ int, _ int64) (int64, error) {
	return 0, nil
}
func (m *mockStoreWithTestData) GetFileByPath(_ context.Context, _ string) (*store.FileRecord, error) {
	return nil, nil
}
func (m *mockStoreWithTestData) GetFileByID(_ context.Context, _ int64) (*store.FileRecord, error) {
	return nil, nil
}
func (m *mockStoreWithTestData) DeleteFile(_ context.Context, _ int64) error { return nil }
func (m *mockStoreWithTestData) UpsertClasses(_ context.Context, _ int64, _ []parser.ClassIR) error {
	return nil
}
func (m *mockStoreWithTestData) UpsertMethods(_ context.Context, _ int64, _ []parser.MethodIR) error {
	return nil
}
func (m *mockStoreWithTestData) UpsertCalls(_ context.Context, _ int64, _ []parser.CallIR) error {
	return nil
}
func (m *mockStoreWithTestData) UpsertIR(_ context.Context, _ *parser.IRDocument) error { return nil }
func (m *mockStoreWithTestData) BulkUpsert(_ context.Context, _ []*parser.IRDocument) error {
	return nil
}
func (m *mockStoreWithTestData) UpsertTags(_ context.Context, classID int64, tags []string) error {
	if m.classTags == nil {
		m.classTags = make(map[int64][]string)
	}
	m.classTags[classID] = tags
	return nil
}
func (m *mockStoreWithTestData) UpsertMethodTags(_ context.Context, _ int64, _ []string) error {
	return nil
}
func (m *mockStoreWithTestData) GetTagsByClassID(_ context.Context, classID int64) ([]string, error) {
	if m.classTags == nil {
		return nil, nil
	}
	return m.classTags[classID], nil
}
func (m *mockStoreWithTestData) GetTagsByMethodID(_ context.Context, _ int64) ([]string, error) {
	return nil, nil
}
func (m *mockStoreWithTestData) SearchByTag(_ context.Context, _ string) ([]int64, []int64, error) {
	return nil, nil, nil
}
func (m *mockStoreWithTestData) SearchByTags(_ context.Context, _ []string) ([]int64, []int64, error) {
	return nil, nil, nil
}
func (m *mockStoreWithTestData) GetAllTagsWithCategories(_ context.Context) (map[string]string, error) {
	return m.tagCats, nil
}

func (m *mockStoreWithTestData) GetAllFiles(_ context.Context) ([]*store.FileRecord, error) {
	return m.files, nil
}

func (m *mockStoreWithTestData) GetClassesByFileID(_ context.Context, fileID int64) ([]store.ClassRecord, error) {
	return m.classes[fileID], nil
}

func (m *mockStoreWithTestData) GetMethodsByClassID(_ context.Context, classID int64) ([]store.MethodRecord, error) {
	return m.methods[classID], nil
}

func (m *mockStoreWithTestData) GetCallsByFileID(_ context.Context, fileID int64) ([]store.CallRecord, error) {
	return m.calls[fileID], nil
}

func TestFindTestLinks_Naming(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/internal/order/service.go", Language: "go"},
			{ID: 2, AbsolutePath: "/project/internal/order/service_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "github.com/idcu/codeschema/internal/order.OrderService"}},
			2: {{ID: 20, FileID: 2, Name: "OrderServiceTest", FullName: "github.com/idcu/codeschema/internal/order.OrderServiceTest"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {
				{ID: 100, ClassID: 10, Name: "getOrder", FullName: "github.com/idcu/codeschema/internal/order.OrderService.getOrder"},
				{ID: 101, ClassID: 10, Name: "createOrder", FullName: "github.com/idcu/codeschema/internal/order.OrderService.createOrder"},
			},
			20: {
				{ID: 200, ClassID: 20, Name: "TestGetOrder", FullName: "github.com/idcu/codeschema/internal/order.OrderServiceTest.TestGetOrder"},
				{ID: 201, ClassID: 20, Name: "TestCreateOrder", FullName: "github.com/idcu/codeschema/internal/order.OrderServiceTest.TestCreateOrder"},
			},
		},
	}

	svc := NewService(st)

	// 测试 getOrder 的关联单测
	links, err := svc.FindTestLinks(ctx, "github.com/idcu/codeschema/internal/order.OrderService.getOrder", 60)
	if err != nil {
		t.Fatalf("FindTestLinks: %v", err)
	}

	if len(links) == 0 {
		t.Fatal("expected at least one test link")
	}

	// 验证命名策略匹配
	found := false
	for _, link := range links {
		if link.TestMethod == "github.com/idcu/codeschema/internal/order.OrderServiceTest.TestGetOrder" &&
			link.Strategy == "naming" {
			found = true
			if link.Confidence != 70 {
				t.Errorf("expected confidence 70, got %d", link.Confidence)
			}
			break
		}
	}
	if !found {
		t.Errorf("expected naming link for TestGetOrder, got %+v", links)
	}
}

// TestFindTestLinks_GoPackageLevelTests 负控：Go 包级测试函数（TestXxx/
// BenchmarkXxx/ExampleXxx）挂在合成 PACKAGE 容器下，容器名是包名而不是
// 「*Test/Test*」类名形态，旧 discoverTestClasses 直接漏掉，导致真实 Go 仓
// 测试关联零召回（公开基准 S-2 实测 PredictedLinks=0）。
func TestFindTestLinks_GoPackageLevelTests(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/internal/order/service.go", Language: "go"},
			{ID: 2, AbsolutePath: "/project/internal/order/service_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			// 生产文件：一个类型 + 合成包级容器（顶层函数）。
			1: {
				{ID: 10, FileID: 1, Name: "OrderService", FullName: "OrderService", Type: "CLASS"},
				{ID: 11, FileID: 1, Name: "order", FullName: "order", Type: "PACKAGE"},
			},
			// 测试文件：同包同目录，测试函数挂在 PACKAGE 容器下。
			2: {{ID: 20, FileID: 2, Name: "order", FullName: "order", Type: "PACKAGE"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {{ID: 100, ClassID: 10, Name: "GetOrder", FullName: "OrderService.GetOrder"}},
			11: {{ID: 110, ClassID: 11, Name: "NewOrderService", FullName: "order.NewOrderService"}},
			20: {
				{ID: 200, ClassID: 20, Name: "TestGetOrder", FullName: "order.TestGetOrder"},
				{ID: 201, ClassID: 20, Name: "BenchmarkGetOrder", FullName: "order.BenchmarkGetOrder"},
				{ID: 202, ClassID: 20, Name: "ExampleOrderService_GetOrder", FullName: "order.ExampleOrderService_GetOrder"},
				{ID: 203, ClassID: 20, Name: "TestNewOrderService", FullName: "order.TestNewOrderService"},
			},
		},
	}

	svc := NewService(st)

	cases := []struct {
		target string
		want   []string
	}{
		{
			target: "OrderService.GetOrder",
			want: []string{
				"order.TestGetOrder",
				"order.BenchmarkGetOrder",
				"order.ExampleOrderService_GetOrder",
			},
		},
		{target: "order.NewOrderService", want: []string{"order.TestNewOrderService"}},
	}

	for _, tc := range cases {
		links, err := svc.FindTestLinks(ctx, tc.target, 60)
		if err != nil {
			t.Fatalf("FindTestLinks(%s): %v", tc.target, err)
		}
		got := make(map[string]bool, len(links))
		for _, l := range links {
			if l.Strategy == "naming" {
				got[l.TestMethod] = true
			}
		}
		for _, w := range tc.want {
			if !got[w] {
				t.Errorf("%s: missing naming link %q, got %+v", tc.target, w, links)
			}
		}
	}
}

// TestFindTestLinks_GoPackageHelpersNotNaming 负控：Go 包级容器里的同包 helper
// （keysOfSets/newMockLSPAdapterMode）不是测试函数，不得以 naming 关联到生产方法；
// 同时生产文件的 PACKAGE 容器不得被当作测试类扫描。
func TestFindTestLinks_GoPackageHelpersNotNaming(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/proj/redis/keys.go", Language: "go"},
			{ID: 2, AbsolutePath: "/proj/redis/keys_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "redis", FullName: "redis", Type: "PACKAGE"}},
			2: {{ID: 20, FileID: 2, Name: "redis", FullName: "redis", Type: "PACKAGE"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {{ID: 100, ClassID: 10, Name: "keysOf", FullName: "redis.keysOf"}},
			20: {
				{ID: 200, ClassID: 20, Name: "TestKeysOf", FullName: "redis.TestKeysOf"},
				{ID: 201, ClassID: 20, Name: "keysOfSets", FullName: "redis.keysOfSets"},
				{ID: 202, ClassID: 20, Name: "keysOfStrs", FullName: "redis.keysOfStrs"},
			},
		},
	}
	svc := NewService(st)

	links, err := svc.FindTestLinks(ctx, "redis.keysOf", 60)
	if err != nil {
		t.Fatalf("FindTestLinks: %v", err)
	}
	for _, l := range links {
		if l.Strategy != "naming" {
			continue
		}
		if l.TestMethod == "redis.keysOfSets" || l.TestMethod == "redis.keysOfStrs" {
			t.Fatalf("Go package helper leaked into naming links: %+v", l)
		}
	}
}

// TestFindTestLinks_CallGraphBehavioralName 负控：测试方法名是行为描述、不含目标
// 方法名时（TestCollection_Delete → DB.CreateCollection），命名策略必然漏召；
// 直接调用边必须补上关联。Java 侧同时覆盖包名前缀 caller 与接收者变量 callee 的 FQN 归一。
func TestFindTestLinks_CallGraphBehavioralName(t *testing.T) {
	ctx := context.Background()

	t.Run("go", func(t *testing.T) {
		st := &mockStoreWithTestData{
			files: []*store.FileRecord{
				{ID: 1, AbsolutePath: "/proj/chromem/db.go", Language: "go"},
				{ID: 2, AbsolutePath: "/proj/chromem/db_test.go", Language: "go"},
			},
			classes: map[int64][]store.ClassRecord{
				1: {{ID: 10, FileID: 1, Name: "DB", FullName: "DB", Type: "CLASS"}},
				2: {{ID: 20, FileID: 2, Name: "chromem", FullName: "chromem", Type: "PACKAGE"}},
			},
			methods: map[int64][]store.MethodRecord{
				10: {{ID: 100, ClassID: 10, Name: "CreateCollection", FullName: "DB.CreateCollection"}},
				20: {{ID: 200, ClassID: 20, Name: "TestCollection_Delete", FullName: "chromem.TestCollection_Delete"}},
			},
			calls: map[int64][]store.CallRecord{
				2: {{CallerFQN: "chromem.TestCollection_Delete", CalleeFQN: "DB.CreateCollection", CallType: "direct"}},
			},
		}
		links, err := NewService(st).FindTestLinks(ctx, "DB.CreateCollection", 60)
		if err != nil {
			t.Fatalf("FindTestLinks: %v", err)
		}
		assertCallGraphLink(t, links, "chromem.TestCollection_Delete")
	})

	t.Run("java", func(t *testing.T) {
		st := &mockStoreWithTestData{
			files: []*store.FileRecord{
				{ID: 1, AbsolutePath: "/proj/model/NamedEntity.java", Language: "java"},
				{ID: 2, AbsolutePath: "/proj/service/ClinicServiceTests.java", Language: "java"},
			},
			classes: map[int64][]store.ClassRecord{
				1: {{ID: 10, FileID: 1, Name: "NamedEntity", FullName: "NamedEntity", Type: "CLASS"}},
				2: {{ID: 20, FileID: 2, Name: "ClinicServiceTests", FullName: "ClinicServiceTests", Type: "CLASS"}},
			},
			methods: map[int64][]store.MethodRecord{
				10: {{ID: 100, ClassID: 10, Name: "setName", FullName: "NamedEntity.setName"}},
				20: {{ID: 200, ClassID: 20, Name: "shouldFindVets", FullName: "ClinicServiceTests.shouldFindVets"}},
			},
			calls: map[int64][]store.CallRecord{
				2: {{
					CallerFQN: "org.springframework.samples.petclinic.service.ClinicServiceTests.shouldFindVets",
					CalleeFQN: "entity.setName",
					CallType:  "direct",
				}},
			},
		}
		links, err := NewService(st).FindTestLinks(ctx, "NamedEntity.setName", 60)
		if err != nil {
			t.Fatalf("FindTestLinks: %v", err)
		}
		assertCallGraphLink(t, links, "ClinicServiceTests.shouldFindVets")
	})
}

func assertCallGraphLink(t *testing.T, links []TestLink, want string) {
	t.Helper()
	for _, l := range links {
		if l.TestMethod == want && l.Strategy == "call_graph" {
			return
		}
	}
	t.Fatalf("missing call_graph link %q; got %+v", want, links)
}

// TestFindTestLinks_JavaTestsSuffix 负控：JUnit/Spring 常见的 `*Tests` 后缀
// （OwnerControllerTests）此前不被 matchTestClass 识别，而 isTestClass 已支持，
// 两处口径不一致导致 Spring 仓测试关联零召回（公开基准 S-2 实测）。
func TestFindTestLinks_JavaTestsSuffix(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/repo/src/main/java/org/demo/OwnerController.java", Language: "java"},
			{ID: 2, AbsolutePath: "/repo/src/test/java/org/demo/OwnerControllerTests.java", Language: "java"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OwnerController", FullName: "OwnerController"}},
			2: {{ID: 20, FileID: 2, Name: "OwnerControllerTests", FullName: "OwnerControllerTests"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {{ID: 100, ClassID: 10, Name: "processCreationForm", FullName: "OwnerController.processCreationForm"}},
			20: {{ID: 200, ClassID: 20, Name: "testProcessCreationForm", FullName: "OwnerControllerTests.testProcessCreationForm"}},
		},
	}

	svc := NewService(st)
	links, err := svc.FindTestLinks(ctx, "OwnerController.processCreationForm", 60)
	if err != nil {
		t.Fatalf("FindTestLinks: %v", err)
	}
	for _, l := range links {
		if l.Strategy == "naming" && l.TestMethod == "OwnerControllerTests.testProcessCreationForm" {
			if l.Confidence != 70 {
				t.Errorf("confidence = %d, want 70", l.Confidence)
			}
			return
		}
	}
	t.Fatalf("missing naming link for *Tests class, got %+v", links)
}

func TestFindTestLinks_SameTag(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/internal/order/service.go", Language: "go"},
			{ID: 2, AbsolutePath: "/project/internal/order/service_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "github.com/idcu/codeschema/internal/order.OrderService"}},
			2: {{ID: 20, FileID: 2, Name: "OrderServiceTest", FullName: "github.com/idcu/codeschema/internal/order.OrderServiceTest"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {
				{ID: 100, ClassID: 10, Name: "getOrder", FullName: "github.com/idcu/codeschema/internal/order.OrderService.getOrder"},
			},
			20: {
				{ID: 200, ClassID: 20, Name: "TestGetOrder", FullName: "github.com/idcu/codeschema/internal/order.OrderServiceTest.TestGetOrder"},
			},
		},
		classTags: map[int64][]string{
			10: {"service", "order", "go"},
			20: {"service", "order", "go"},
		},
		tagCats: map[string]string{
			"service": "layer", "order": "biz", "go": "lang",
		},
	}

	svc := NewService(st)

	links, err := svc.FindTestLinks(ctx, "github.com/idcu/codeschema/internal/order.OrderService.getOrder", 60)
	if err != nil {
		t.Fatalf("FindTestLinks: %v", err)
	}

	if len(links) == 0 {
		t.Fatal("expected at least one test link via same_tag")
	}

	// 验证 same_tag 策略匹配
	found := false
	for _, link := range links {
		if link.Strategy == "same_tag" {
			found = true
			break
		}
	}
	if !found {
		t.Errorf("expected same_tag strategy link, got %+v", links)
	}
}

func TestFindTestLinks_Dependency(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/internal/order/service.go", Language: "go", Imports: []string{"github.com/idcu/codeschema/internal/payment"}},
			{ID: 2, AbsolutePath: "/project/internal/payment/service.go", Language: "go"},
			{ID: 3, AbsolutePath: "/project/internal/payment/service_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "github.com/idcu/codeschema/internal/order.OrderService"}},
			2: {{ID: 20, FileID: 2, Name: "PaymentService", FullName: "github.com/idcu/codeschema/internal/payment.PaymentService"}},
			3: {{ID: 30, FileID: 3, Name: "PaymentServiceTest", FullName: "github.com/idcu/codeschema/internal/payment.PaymentServiceTest"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {
				{ID: 100, ClassID: 10, Name: "getOrder", FullName: "github.com/idcu/codeschema/internal/order.OrderService.getOrder"},
			},
			20: {
				{ID: 200, ClassID: 20, Name: "processPayment", FullName: "github.com/idcu/codeschema/internal/payment.PaymentService.processPayment"},
			},
			30: {
				{ID: 300, ClassID: 30, Name: "TestProcessPayment", FullName: "github.com/idcu/codeschema/internal/payment.PaymentServiceTest.TestProcessPayment"},
			},
		},
	}

	svc := NewService(st)

	// OrderService 引用了 payment 包，所以 processPayment 应关联到 TestProcessPayment
	links, err := svc.FindTestLinks(ctx, "github.com/idcu/codeschema/internal/payment.PaymentService.processPayment", 60)
	if err != nil {
		t.Fatalf("FindTestLinks: %v", err)
	}

	if len(links) == 0 {
		t.Fatal("expected at least one test link via dependency")
	}

	// 验证 dependency 策略匹配（可能同时有 naming 匹配）
	foundDep := false
	foundNam := false
	for _, link := range links {
		if link.Strategy == "dependency" && link.Confidence == 80 {
			foundDep = true
		}
		if link.Strategy == "naming" {
			foundNam = true
		}
	}
	if !foundDep {
		t.Errorf("expected dependency strategy link, got %+v", links)
	}
	if !foundNam {
		t.Errorf("expected naming strategy link (PaymentServiceTest ↔ PaymentService), got %+v", links)
	}
}

func TestFindTestLinks_EmptyMethod(t *testing.T) {
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/internal/order/service.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "github.com/idcu/codeschema/internal/order.OrderService"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {{ID: 100, ClassID: 10, Name: "getOrder", FullName: "github.com/idcu/codeschema/internal/order.OrderService.getOrder"}},
		},
	}

	svc := NewService(st)
	_, err := svc.FindTestLinks(context.Background(), "", 60)
	if err == nil {
		t.Fatal("expected error for empty method")
	}
}

func TestFindTestLinks_NoTestFiles(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/internal/order/service.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "github.com/idcu/codeschema/internal/order.OrderService"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {{ID: 100, ClassID: 10, Name: "getOrder", FullName: "github.com/idcu/codeschema/internal/order.OrderService.getOrder"}},
		},
	}

	svc := NewService(st)
	links, err := svc.FindTestLinks(ctx, "github.com/idcu/codeschema/internal/order.OrderService.getOrder", 60)
	if err != nil {
		t.Fatalf("FindTestLinks: %v", err)
	}
	if len(links) != 0 {
		t.Errorf("expected empty links, got %d", len(links))
	}
}

func TestExtractClassFQN(t *testing.T) {
	tests := []struct {
		methodFQN string
		expected  string
	}{
		{"com.example.OrderService.getOrder", "com.example.OrderService"},
		{"github.com/idcu/codeschema/internal/order.OrderService.getOrder", "github.com/idcu/codeschema/internal/order.OrderService"},
		{"OrderService", "OrderService"},
		{"", ""},
	}
	for _, tt := range tests {
		got := extractClassFQN(tt.methodFQN)
		if got != tt.expected {
			t.Errorf("extractClassFQN(%q) = %q, want %q", tt.methodFQN, got, tt.expected)
		}
	}
}

func TestIsTestClass(t *testing.T) {
	tests := []struct {
		name     string
		cls      store.ClassRecord
		path     string
		expected bool
	}{
		{name: "Test suffix", cls: store.ClassRecord{Name: "OrderServiceTest"}, path: "/x/OrderServiceTest.java", expected: true},
		{name: "Test prefix", cls: store.ClassRecord{Name: "TestOrderService"}, path: "/x/TestOrderService.java", expected: true},
		{name: "Tests suffix", cls: store.ClassRecord{Name: "OrderServiceTests"}, path: "/x/OrderServiceTests.java", expected: true},
		{name: "Spec suffix", cls: store.ClassRecord{Name: "OrderServiceSpec"}, path: "/x/OrderServiceSpec.java", expected: true},
		{name: "Not test", cls: store.ClassRecord{Name: "OrderService"}, path: "/x/service.go", expected: false},
		{name: "Empty", cls: store.ClassRecord{Name: ""}, path: "/x/service.go", expected: false},
		{name: "Go test file", cls: store.ClassRecord{Name: "OrderService"}, path: "/x/service_test.go", expected: true},
		// Go 的测试函数只能位于 *_test.go；生产文件里的 Test* 类型（TestLink/TestResult）
		// 不是测试类，否则整个文件被误判为测试，文件内生产调用会被当作「测试方法」。
		{name: "Go production Test* type", cls: store.ClassRecord{Name: "TestResult"}, path: "/x/service.go", expected: false},
		{name: "Go test file Test* type", cls: store.ClassRecord{Name: "TestResult"}, path: "/x/service_test.go", expected: true},
		{name: "pytest module file", cls: store.ClassRecord{Name: "OrderService"}, path: "/x/test_order.py", expected: true},
		{name: "pytest module file 2", cls: store.ClassRecord{Name: "OrderService"}, path: "/x/order_test.py", expected: true},
		{name: "Jest test file", cls: store.ClassRecord{Name: "OrderService"}, path: "/x/order.test.ts", expected: true},
		{name: "Jest spec file", cls: store.ClassRecord{Name: "OrderService"}, path: "/x/order.spec.js", expected: true},
		{name: "plural test file", cls: store.ClassRecord{Name: "OrderService"}, path: "/x/order.tests.py", expected: false},
		{name: "contest false positive", cls: store.ClassRecord{Name: "OrderService"}, path: "/x/latest_file.go", expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTestClass(tt.cls, tt.path)
			if got != tt.expected {
				t.Errorf("isTestClass(%q, %q) = %v, want %v", tt.cls.Name, tt.path, got, tt.expected)
			}
		})
	}
}

func TestIsTestFileName(t *testing.T) {
	tests := []struct {
		path string
		want bool
	}{
		{"/x/service_test.go", true},
		{"/x/test_order.py", true},
		{"/x/order_test.py", true},
		{"/x/order.test.ts", true},
		{"/x/order.spec.js", true},
		{"/x/service.go", false},
		{"/x/latest_file.go", false},
		{"", false},
	}
	for _, tt := range tests {
		if got := isTestFileName(tt.path); got != tt.want {
			t.Errorf("isTestFileName(%q) = %v, want %v", tt.path, got, tt.want)
		}
	}
}

func TestMatchesTestMethod(t *testing.T) {
	tests := []struct {
		target string
		test   string
		want   bool
	}{
		{"getOrder", "getOrder", true},
		{"getOrder", "TestGetOrder", true},
		{"getOrder", "testGetOrder", true},
		{"ReturnOrder", "ShouldReturnOrder", true},
		{"getOrder", "createOrder", false},
		{"processPayment", "TestProcessPayment", true},
		// pytest snake_case
		{"get_order", "test_get_order", true},
		{"get_order", "should_get_order", true},
		{"get_order", "when_get_order_called", true},
		// JUnit/BDD camelCase 兼容 snake 语义（包含兜底）
		{"getOrder", "testGetOrderShouldReturn", true},
		{"get_order", "create_order", false},
		// 描述型测试名：目标方法名是测试名前缀（同一被测类内）。
		{"processCreationForm", "processCreationFormSuccess", true},
		{"addPet", "addPetAddsPersistedPet", true},
		{"validate", "validateWithInvalidBirthDate", true},
		// 子串必须落在词边界上：Analyzer 不是 Analyze。
		{"Analyze", "TestAnalyzer_BuildAll", false},
		{"Analyze", "TestAnalyzer_TagAll_WithEnhancer_AppendsAITags", false},
		// 过短目标名不做前缀匹配（get 之于 getById）。
		{"get", "getById", false},
	}
	for _, tt := range tests {
		got := matchesTestMethod(tt.target, tt.test)
		if got != tt.want {
			t.Errorf("matchesTestMethod(%q, %q) = %v, want %v", tt.target, tt.test, got, tt.want)
		}
	}
}

func TestSortByConfidenceDesc(t *testing.T) {
	links := []TestLink{
		{TestMethod: "A", Confidence: 60},
		{TestMethod: "B", Confidence: 80},
		{TestMethod: "C", Confidence: 70},
	}
	sortByConfidenceDesc(links)
	if links[0].Confidence != 80 || links[1].Confidence != 70 || links[2].Confidence != 60 {
		t.Errorf("expected sorted [80, 70, 60], got %+v", links)
	}
}

func TestDirFromPath(t *testing.T) {
	tests := []struct {
		path     string
		expected string
	}{
		{"/project/internal/payment/service.go", "/project/internal/payment"},
		{"/project/service.go", "/project"},
		{"service.go", ""},
		{"", ""},
	}
	for _, tt := range tests {
		got := dirFromPath(tt.path)
		if got != tt.expected {
			t.Errorf("dirFromPath(%q) = %q, want %q", tt.path, got, tt.expected)
		}
	}
}

func TestFindTestLinks_Explicit(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/internal/order/OrderService.java", Language: "java"},
			{ID: 2, AbsolutePath: "/project/internal/order/OrderServiceSpec.java", Language: "java"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "com.example.OrderService"}},
			2: {{ID: 20, FileID: 2, Name: "OrderServiceSpec", FullName: "com.example.OrderServiceSpec", Doc: "@TestFor(OrderService)"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {
				{ID: 100, ClassID: 10, Name: "getOrder", FullName: "com.example.OrderService.getOrder"},
				{ID: 101, ClassID: 10, Name: "createOrder", FullName: "com.example.OrderService.createOrder"},
			},
			20: {
				{ID: 200, ClassID: 20, Name: "shouldGetOrder", FullName: "com.example.OrderServiceSpec.shouldGetOrder"},
			},
		},
	}

	svc := NewService(st)
	links, err := svc.FindTestLinks(ctx, "com.example.OrderService.getOrder", 60)
	if err != nil {
		t.Fatalf("FindTestLinks: %v", err)
	}

	found := false
	for _, link := range links {
		if link.Strategy == "explicit" && link.TestMethod == "com.example.OrderServiceSpec.shouldGetOrder" {
			found = true
			if link.Confidence != 100 {
				t.Errorf("expected explicit confidence 100, got %d", link.Confidence)
			}
			if link.MethodName != "getOrder" {
				t.Errorf("expected target method getOrder, got %q", link.MethodName)
			}
		}
	}
	if !found {
		t.Errorf("expected explicit link via @TestFor, got %+v", links)
	}
}

func TestFindTestLinks_Explicit_MethodLevel(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/internal/user/service.go", Language: "go"},
			{ID: 2, AbsolutePath: "/project/internal/user/service_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "UserService", FullName: "com.example.UserService"}},
			2: {{ID: 20, FileID: 2, Name: "UserServiceTest", FullName: "com.example.UserServiceTest"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {
				{ID: 100, ClassID: 10, Name: "deleteUser", FullName: "com.example.UserService.deleteUser"},
			},
			20: {
				// 方法级 @TestFor 注解（而非类级）
				{ID: 200, ClassID: 20, Name: "TestDeleteUser", FullName: "com.example.UserServiceTest.TestDeleteUser", Doc: "@TestFor(com.example.UserService)"},
			},
		},
	}

	svc := NewService(st)
	links, err := svc.FindTestLinks(ctx, "com.example.UserService.deleteUser", 60)
	if err != nil {
		t.Fatalf("FindTestLinks: %v", err)
	}

	found := false
	for _, link := range links {
		if link.Strategy == "explicit" && link.TestMethod == "com.example.UserServiceTest.TestDeleteUser" && link.Confidence == 100 {
			found = true
		}
	}
	if !found {
		t.Errorf("expected method-level explicit link, got %+v", links)
	}
}

// TestCoverageStrategyRemoved 负控（R6-8）：coverage 关联策略在生产从未接线
// ——`SetCoverage`/`LoadCoverageJSON`/`LoadGoCoverProfile` 全仓零调用点，
// `s.coverage` 恒 nil，`linkByCoverage` 永远直接返回。按裁决直接删除，
// 不保留 experimental 名义能力。本用例守住「对外 API 不得重新出现而不接线」。
func TestCoverageStrategyRemoved(t *testing.T) {
	typ := reflect.TypeOf(NewService(&mockStoreWithTestData{}))
	for _, name := range []string{"SetCoverage", "LoadCoverageJSON", "LoadGoCoverProfile", "ParseGoCoverProfile"} {
		if _, ok := typ.MethodByName(name); ok {
			t.Errorf("coverage 能力应已删除，但 Service 仍暴露 %s", name)
		}
	}
}

func TestParseExplicitTargets(t *testing.T) {
	tests := []struct {
		doc      string
		expected []string
	}{
		{"@TestFor(OrderService.class)", []string{"OrderService.class"}},
		{"@TestFor(com.example.OrderService)", []string{"com.example.OrderService"}},
		{"@TestFor OrderService", []string{"OrderService"}},
		{"@testfor: order.OrderService", []string{"order.OrderService"}},
		{"no annotation here", nil},
		{"@TestFor(A) and @TestFor(B)", []string{"A", "B"}},
	}
	for _, tt := range tests {
		got := parseExplicitTargets(tt.doc)
		if len(got) != len(tt.expected) {
			t.Errorf("parseExplicitTargets(%q) = %v, want %v", tt.doc, got, tt.expected)
			continue
		}
		for i := range got {
			if got[i] != tt.expected[i] {
				t.Errorf("parseExplicitTargets(%q)[%d] = %q, want %q", tt.doc, i, got[i], tt.expected[i])
			}
		}
	}
}

func TestResolveExplicitClass(t *testing.T) {
	all := map[string]store.ClassRecord{
		"com.example.OrderService":     {ID: 1, Name: "OrderService", FullName: "com.example.OrderService"},
		"com.example.user.UserService": {ID: 2, Name: "UserService", FullName: "com.example.user.UserService"},
	}
	tests := []struct {
		token    string
		expected string
	}{
		{"com.example.OrderService", "com.example.OrderService"}, // 精确
		{"OrderService", "com.example.OrderService"},             // 简单名
		{"UserService", "com.example.user.UserService"},          // 简单名
		{"NonExistent", ""}, // 解析失败
	}
	for _, tt := range tests {
		got := resolveExplicitClass(tt.token, all)
		if got != tt.expected {
			t.Errorf("resolveExplicitClass(%q) = %q, want %q", tt.token, got, tt.expected)
		}
	}
}

// TestFindTestLinks_ExplicitScopedToQueriedMethod 负控（P1-12）：
// explicit 策略必须按被查询方法过滤，不得把任意 @TestFor 的测试以 100 置信度
// 关联给所有生产方法。
func TestFindTestLinks_ExplicitScopedToQueriedMethod(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/proj/A.java", Language: "java"},
			{ID: 2, AbsolutePath: "/proj/ATest.java", Language: "java"},
			{ID: 3, AbsolutePath: "/proj/B.java", Language: "java"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "A", FullName: "com.example.A"}},
			2: {{ID: 20, FileID: 2, Name: "ATest", FullName: "com.example.ATest"}},
			3: {{ID: 30, FileID: 3, Name: "B", FullName: "com.example.B"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {
				{ID: 100, ClassID: 10, Name: "M1", FullName: "com.example.A.M1"},
				{ID: 101, ClassID: 10, Name: "M2", FullName: "com.example.A.M2"},
			},
			20: {{ID: 200, ClassID: 20, Name: "TestA", FullName: "com.example.ATest.TestA", Doc: "@TestFor(com.example.A)"}},
			30: {{ID: 300, ClassID: 30, Name: "N", FullName: "com.example.B.N"}},
		},
	}
	svc := NewService(st)

	// 查询 A.M1：只应返回 M1 的 explicit 关联（不得把 M2 也塞进来）。
	links, err := svc.FindTestLinks(ctx, "com.example.A.M1", 100)
	if err != nil {
		t.Fatalf("FindTestLinks(M1): %v", err)
	}
	if len(links) != 1 {
		t.Fatalf("expected exactly 1 explicit link for M1, got %d: %+v", len(links), links)
	}
	if links[0].TestMethod != "com.example.ATest.TestA" || links[0].MethodName != "M1" || links[0].Confidence != 100 {
		t.Fatalf("unexpected explicit link: %+v", links[0])
	}

	// 查询 B.N：@TestFor(A) 与该类无关，不得产生 explicit 关联。
	links, err = svc.FindTestLinks(ctx, "com.example.B.N", 100)
	if err != nil {
		t.Fatalf("FindTestLinks(N): %v", err)
	}
	if len(links) != 0 {
		t.Fatalf("expected no explicit link for unrelated class, got %+v", links)
	}
}

// TestFindTestLinks_MultipleTestClassesPerSource 负控（P2-4）：
// 同一源类存在多个测试类时，全部都要参与命名关联，不能让后者覆盖前者。
func TestFindTestLinks_MultipleTestClassesPerSource(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/proj/order/service.go", Language: "go"},
			{ID: 2, AbsolutePath: "/proj/tests/order_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "proj/order.OrderService"}},
			2: {
				{ID: 20, FileID: 2, Name: "OrderServiceTest", FullName: "proj/order.OrderServiceTest"},
				{ID: 21, FileID: 2, Name: "TestOrderService", FullName: "proj/order.TestOrderService"},
			},
		},
		methods: map[int64][]store.MethodRecord{
			10: {{ID: 100, ClassID: 10, Name: "GetOrder", FullName: "proj/order.OrderService.GetOrder"}},
			20: {{ID: 200, ClassID: 20, Name: "TestGetOrder", FullName: "proj/order.OrderServiceTest.TestGetOrder"}},
			21: {{ID: 210, ClassID: 21, Name: "TestGetOrder", FullName: "proj/order.TestOrderService.TestGetOrder"}},
		},
	}
	svc := NewService(st)

	links, err := svc.FindTestLinks(ctx, "proj/order.OrderService.GetOrder", 70)
	if err != nil {
		t.Fatalf("FindTestLinks: %v", err)
	}
	got := map[string]bool{}
	for _, l := range links {
		if l.Strategy == "naming" {
			got[l.TestMethod] = true
		}
	}
	for _, want := range []string{
		"proj/order.OrderServiceTest.TestGetOrder",
		"proj/order.TestOrderService.TestGetOrder",
	} {
		if !got[want] {
			t.Errorf("missing naming link for %s; got %+v", want, got)
		}
	}
}

// TestFindTestLinks_NoSubstringNamingFalsePositive 负控（P2-4）：
// 命名策略的 Contains 兜底过宽（目标 Get 命中测试名 TargetGetter），必须收敛。
func TestFindTestLinks_NoSubstringNamingFalsePositive(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/proj/svc/service.go", Language: "go"},
			{ID: 2, AbsolutePath: "/proj/tests/service_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "Svc", FullName: "proj/svc.Svc"}},
			2: {{ID: 20, FileID: 2, Name: "SvcTest", FullName: "proj/svc.SvcTest"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {{ID: 100, ClassID: 10, Name: "Get", FullName: "proj/svc.Svc.Get"}},
			20: {{ID: 200, ClassID: 20, Name: "TargetGetter", FullName: "proj/tests.SvcTest.TargetGetter"}},
		},
	}
	svc := NewService(st)

	links, err := svc.FindTestLinks(ctx, "proj/svc.Svc.Get", 70)
	if err != nil {
		t.Fatalf("FindTestLinks: %v", err)
	}
	for _, l := range links {
		if l.Strategy == "naming" {
			t.Fatalf("substring false positive leaked: %+v", l)
		}
	}
}

// TestFindTestLinks_SameDirNonGoNotDependency 负控（P2-4）：
// 同目录只对 Go（同包测试）构成 dependency 证据，其它语言同目录不得给 80。
func TestFindTestLinks_SameDirNonGoNotDependency(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/proj/payment/service.java", Language: "java"},
			{ID: 2, AbsolutePath: "/proj/payment/PaymentServiceTest.java", Language: "java"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "PaymentService", FullName: "com.proj.PaymentService"}},
			2: {{ID: 20, FileID: 2, Name: "PaymentServiceTest", FullName: "com.proj.PaymentServiceTest"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {{ID: 100, ClassID: 10, Name: "pay", FullName: "com.proj.PaymentService.pay"}},
			20: {{ID: 200, ClassID: 20, Name: "TestPay", FullName: "com.proj.PaymentServiceTest.TestPay"}},
		},
	}
	svc := NewService(st)

	links, err := svc.FindTestLinks(ctx, "com.proj.PaymentService.pay", 80)
	if err != nil {
		t.Fatalf("FindTestLinks: %v", err)
	}
	for _, l := range links {
		if l.Strategy == "dependency" {
			t.Fatalf("non-go same-dir must not be dependency evidence: %+v", l)
		}
	}
}

// TestFindTestLinks_SameTagRequiresMethodNameMatch 负控（P2-4）：
// same_tag 不得把同 tag 测试类的所有方法都关联给目标方法。
func TestFindTestLinks_SameTagRequiresMethodNameMatch(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/proj/order/service.go", Language: "go"},
			{ID: 2, AbsolutePath: "/proj/other/order_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "proj/order.OrderService"}},
			2: {{ID: 20, FileID: 2, Name: "OrderChecks", FullName: "proj/other.OrderChecks"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {{ID: 100, ClassID: 10, Name: "GetOrder", FullName: "proj/order.OrderService.GetOrder"}},
			20: {
				{ID: 200, ClassID: 20, Name: "TestGetOrder", FullName: "proj/other.OrderChecks.TestGetOrder"},
				{ID: 201, ClassID: 20, Name: "TestUnrelated", FullName: "proj/other.OrderChecks.TestUnrelated"},
			},
		},
		classTags: map[int64][]string{
			10: {"order", "service"},
			20: {"order", "service"},
		},
	}
	svc := NewService(st)

	links, err := svc.FindTestLinks(ctx, "proj/order.OrderService.GetOrder", 60)
	if err != nil {
		t.Fatalf("FindTestLinks: %v", err)
	}
	gotSameTag := map[string]bool{}
	for _, l := range links {
		if l.Strategy == "same_tag" {
			gotSameTag[l.TestMethod] = true
		}
	}
	if !gotSameTag["proj/other.OrderChecks.TestGetOrder"] {
		t.Errorf("expected same_tag link for name-matching test; got %+v", gotSameTag)
	}
	if gotSameTag["proj/other.OrderChecks.TestUnrelated"] {
		t.Errorf("same_tag must not link unrelated test method: %+v", gotSameTag)
	}
}
