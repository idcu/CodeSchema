package service

import (
	"context"
	"testing"

	"codeschema/internal/parser"
	"codeschema/internal/store"
)

// mockStoreWithTestData 实现 Store 接口，用于测试关联测试。
type mockStoreWithTestData struct {
	files   []*store.FileRecord
	classes map[int64][]store.ClassRecord
	methods map[int64][]store.MethodRecord
	calls   map[int64][]store.CallRecord
	classTags  map[int64][]string
	methodTags map[int64][]string
	tagCats    map[string]string
}

func (m *mockStoreWithTestData) Open(_ context.Context, _ string) error    { return nil }
func (m *mockStoreWithTestData) Close() error                               { return nil }
func (m *mockStoreWithTestData) HealthCheck(_ context.Context) error        { return nil }
func (m *mockStoreWithTestData) UpsertFile(_ context.Context, _ string, _ string, _ int, _ int64) (int64, error) { return 0, nil }
func (m *mockStoreWithTestData) GetFileByPath(_ context.Context, _ string) (*store.FileRecord, error) { return nil, nil }
func (m *mockStoreWithTestData) GetFileByID(_ context.Context, _ int64) (*store.FileRecord, error)    { return nil, nil }
func (m *mockStoreWithTestData) DeleteFile(_ context.Context, _ int64) error                          { return nil }
func (m *mockStoreWithTestData) UpsertClasses(_ context.Context, _ int64, _ []parser.ClassIR) error   { return nil }
func (m *mockStoreWithTestData) UpsertMethods(_ context.Context, _ int64, _ []parser.MethodIR) error  { return nil }
func (m *mockStoreWithTestData) UpsertCalls(_ context.Context, _ int64, _ []parser.CallIR) error      { return nil }
func (m *mockStoreWithTestData) UpsertIR(_ context.Context, _ *parser.IRDocument) error               { return nil }
func (m *mockStoreWithTestData) UpsertTags(_ context.Context, classID int64, tags []string) error {
	if m.classTags == nil {
		m.classTags = make(map[int64][]string)
	}
	m.classTags[classID] = tags
	return nil
}
func (m *mockStoreWithTestData) UpsertMethodTags(_ context.Context, _ int64, _ []string) error { return nil }
func (m *mockStoreWithTestData) GetTagsByClassID(_ context.Context, classID int64) ([]string, error) {
	if m.classTags == nil {
		return nil, nil
	}
	return m.classTags[classID], nil
}
func (m *mockStoreWithTestData) GetTagsByMethodID(_ context.Context, _ int64) ([]string, error) { return nil, nil }
func (m *mockStoreWithTestData) SearchByTag(_ context.Context, _ string) ([]int64, []int64, error) { return nil, nil, nil }
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

func (m *mockStoreWithTestData) GetCallsByFileID(_ context.Context, _ int64) ([]store.CallRecord, error) {
	return nil, nil
}

func TestFindTestLinks_Naming(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/internal/order/service.go", Language: "go"},
			{ID: 2, AbsolutePath: "/project/internal/order/service_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "codeschema/internal/order.OrderService"}},
			2: {{ID: 20, FileID: 2, Name: "OrderServiceTest", FullName: "codeschema/internal/order.OrderServiceTest"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {
				{ID: 100, ClassID: 10, Name: "getOrder", FullName: "codeschema/internal/order.OrderService.getOrder"},
				{ID: 101, ClassID: 10, Name: "createOrder", FullName: "codeschema/internal/order.OrderService.createOrder"},
			},
			20: {
				{ID: 200, ClassID: 20, Name: "TestGetOrder", FullName: "codeschema/internal/order.OrderServiceTest.TestGetOrder"},
				{ID: 201, ClassID: 20, Name: "TestCreateOrder", FullName: "codeschema/internal/order.OrderServiceTest.TestCreateOrder"},
			},
		},
	}

	svc := NewService(st)

	// 测试 getOrder 的关联单测
	links, err := svc.FindTestLinks(ctx, "codeschema/internal/order.OrderService.getOrder", 60)
	if err != nil {
		t.Fatalf("FindTestLinks: %v", err)
	}

	if len(links) == 0 {
		t.Fatal("expected at least one test link")
	}

	// 验证命名策略匹配
	found := false
	for _, link := range links {
		if link.TestMethod == "codeschema/internal/order.OrderServiceTest.TestGetOrder" &&
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

func TestFindTestLinks_SameTag(t *testing.T) {
	ctx := context.Background()
	st := &mockStoreWithTestData{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/internal/order/service.go", Language: "go"},
			{ID: 2, AbsolutePath: "/project/internal/order/service_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "codeschema/internal/order.OrderService"}},
			2: {{ID: 20, FileID: 2, Name: "OrderServiceTest", FullName: "codeschema/internal/order.OrderServiceTest"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {
				{ID: 100, ClassID: 10, Name: "getOrder", FullName: "codeschema/internal/order.OrderService.getOrder"},
			},
			20: {
				{ID: 200, ClassID: 20, Name: "TestGetOrder", FullName: "codeschema/internal/order.OrderServiceTest.TestGetOrder"},
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

	links, err := svc.FindTestLinks(ctx, "codeschema/internal/order.OrderService.getOrder", 60)
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
			{ID: 1, AbsolutePath: "/project/internal/order/service.go", Language: "go", Imports: []string{"codeschema/internal/payment"}},
			{ID: 2, AbsolutePath: "/project/internal/payment/service.go", Language: "go"},
			{ID: 3, AbsolutePath: "/project/internal/payment/service_test.go", Language: "go"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "codeschema/internal/order.OrderService"}},
			2: {{ID: 20, FileID: 2, Name: "PaymentService", FullName: "codeschema/internal/payment.PaymentService"}},
			3: {{ID: 30, FileID: 3, Name: "PaymentServiceTest", FullName: "codeschema/internal/payment.PaymentServiceTest"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {
				{ID: 100, ClassID: 10, Name: "getOrder", FullName: "codeschema/internal/order.OrderService.getOrder"},
			},
			20: {
				{ID: 200, ClassID: 20, Name: "processPayment", FullName: "codeschema/internal/payment.PaymentService.processPayment"},
			},
			30: {
				{ID: 300, ClassID: 30, Name: "TestProcessPayment", FullName: "codeschema/internal/payment.PaymentServiceTest.TestProcessPayment"},
			},
		},
	}

	svc := NewService(st)

	// OrderService 引用了 payment 包，所以 processPayment 应关联到 TestProcessPayment
	links, err := svc.FindTestLinks(ctx, "codeschema/internal/payment.PaymentService.processPayment", 60)
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
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "codeschema/internal/order.OrderService"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {{ID: 100, ClassID: 10, Name: "getOrder", FullName: "codeschema/internal/order.OrderService.getOrder"}},
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
			1: {{ID: 10, FileID: 1, Name: "OrderService", FullName: "codeschema/internal/order.OrderService"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {{ID: 100, ClassID: 10, Name: "getOrder", FullName: "codeschema/internal/order.OrderService.getOrder"}},
		},
	}

	svc := NewService(st)
	links, err := svc.FindTestLinks(ctx, "codeschema/internal/order.OrderService.getOrder", 60)
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
		{"codeschema/internal/order.OrderService.getOrder", "codeschema/internal/order.OrderService"},
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
		expected bool
	}{
		{name: "Test suffix", cls: store.ClassRecord{Name: "OrderServiceTest"}, expected: true},
		{name: "Test prefix", cls: store.ClassRecord{Name: "TestOrderService"}, expected: true},
		{name: "Tests suffix", cls: store.ClassRecord{Name: "OrderServiceTests"}, expected: true},
		{name: "Spec suffix", cls: store.ClassRecord{Name: "OrderServiceSpec"}, expected: true},
		{name: "Not test", cls: store.ClassRecord{Name: "OrderService"}, expected: false},
		{name: "Empty", cls: store.ClassRecord{Name: ""}, expected: false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := isTestClass(tt.cls)
			if got != tt.expected {
				t.Errorf("isTestClass(%q) = %v, want %v", tt.cls.Name, got, tt.expected)
			}
		})
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