package ai

import (
	"context"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// ---------- DeriveClassTags 测试 ----------

func TestDeriveClassTags_Layer(t *testing.T) {
	tests := []struct {
		name     string
		cls      store.ClassRecord
		file     *store.FileRecord
		expected string // 期望的 layer 标签
	}{
		{
			name: "controller from directory",
			cls:  store.ClassRecord{Name: "OrderController"},
			file: &store.FileRecord{AbsolutePath: "/project/src/main/java/com/example/controller/OrderController.java", Language: "java"},
			expected: "controller",
		},
		{
			name: "service from directory",
			cls:  store.ClassRecord{Name: "UserService"},
			file: &store.FileRecord{AbsolutePath: "/project/src/main/java/com/example/service/UserService.java", Language: "java"},
			expected: "service",
		},
		{
			name: "dao from class suffix",
			cls:  store.ClassRecord{Name: "ProductRepository"},
			file: &store.FileRecord{AbsolutePath: "/project/src/main/java/com/example/repo/ProductRepository.java", Language: "java"},
			expected: "dao",
		},
		{
			name: "domain from directory",
			cls:  store.ClassRecord{Name: "Order"},
			file: &store.FileRecord{AbsolutePath: "/project/src/main/java/com/example/domain/Order.java", Language: "java"},
			expected: "domain",
		},
		{
			name: "infra from directory",
			cls:  store.ClassRecord{Name: "DatabaseConfig"},
			file: &store.FileRecord{AbsolutePath: "/project/src/main/java/com/example/infra/DatabaseConfig.java", Language: "java"},
			expected: "infra",
		},
		{
			name: "middleware from class suffix",
			cls:  store.ClassRecord{Name: "AuthMiddleware"},
			file: &store.FileRecord{AbsolutePath: "/project/src/main/java/com/example/middleware/AuthMiddleware.java", Language: "java"},
			expected: "middleware",
		},
		{
			name: "handler from directory",
			cls:  store.ClassRecord{Name: "EventHandler"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/handler/event.go", Language: "go"},
			expected: "handler",
		},
		{
			name: "no layer match",
			cls:  store.ClassRecord{Name: "Utils"},
			file: &store.FileRecord{AbsolutePath: "/project/pkg/util/helper.go", Language: "go"},
			expected: "",
		},
		{
			name: "nil file",
			cls:  store.ClassRecord{Name: "OrderService"},
			file: nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := DeriveClassTags(tt.cls, tt.file)
			found := false
			for _, tag := range tags {
				if tag == tt.expected {
					found = true
					break
				}
			}
			if tt.expected != "" && !found {
				t.Errorf("expected layer tag %q, got tags %v", tt.expected, tags)
			}
			if tt.expected == "" {
				// 确认没有 layer 标签（即不在 layerKeywords 中）
				for _, tag := range tags {
					if tag == "controller" || tag == "service" || tag == "dao" ||
						tag == "domain" || tag == "infra" || tag == "middleware" || tag == "handler" {
						t.Errorf("unexpected layer tag %q in tags %v", tag, tags)
					}
				}
			}
		})
	}
}

func TestDeriveClassTags_Biz(t *testing.T) {
	tests := []struct {
		name     string
		cls      store.ClassRecord
		file     *store.FileRecord
		expected string
	}{
		{
			name: "order business domain",
			cls:  store.ClassRecord{Name: "OrderService"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/order/service.go", Language: "go"},
			expected: "order",
		},
		{
			name: "user business domain",
			cls:  store.ClassRecord{Name: "UserController"},
			file: &store.FileRecord{AbsolutePath: "/project/src/main/java/com/example/user/UserController.java", Language: "java"},
			expected: "user",
		},
		{
			name: "payment business domain",
			cls:  store.ClassRecord{Name: "PaymentService"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/payment/service.go", Language: "go"},
			expected: "payment",
		},
		{
			name: "notification business domain",
			cls:  store.ClassRecord{Name: "NotificationHandler"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/notification/handler.go", Language: "go"},
			expected: "notification",
		},
		{
			name: "no business domain (common path)",
			cls:  store.ClassRecord{Name: "StringUtils"},
			file: &store.FileRecord{AbsolutePath: "/project/pkg/util/string.go", Language: "go"},
			expected: "",
		},
		{
			name: "nil file",
			cls:  store.ClassRecord{Name: "OrderService"},
			file: nil,
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := DeriveClassTags(tt.cls, tt.file)
			found := false
			for _, tag := range tags {
				if tag == tt.expected {
					found = true
					break
				}
			}
			if tt.expected != "" && !found {
				t.Errorf("expected biz tag %q, got tags %v", tt.expected, tags)
			}
		})
	}
}

func TestDeriveClassTags_Tech(t *testing.T) {
	tests := []struct {
		name     string
		cls      store.ClassRecord
		file     *store.FileRecord
		expected string
	}{
		{
			name: "cache class",
			cls:  store.ClassRecord{Name: "RedisCache"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/cache/redis.go", Language: "go"},
			expected: "cache",
		},
		{
			name: "mq class",
			cls:  store.ClassRecord{Name: "MessageQueue"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/mq/producer.go", Language: "go"},
			expected: "mq",
		},
		{
			name: "retry class",
			cls:  store.ClassRecord{Name: "RetryHandler"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/retry/handler.go", Language: "go"},
			expected: "retry",
		},
		{
			name: "transactional class",
			cls:  store.ClassRecord{Name: "TransactionManager"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/tx/manager.go", Language: "go"},
			expected: "transactional",
		},
		{
			name: "async class",
			cls:  store.ClassRecord{Name: "AsyncProcessor"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/async/processor.go", Language: "go"},
			expected: "async",
		},
		{
			name: "schedule class",
			cls:  store.ClassRecord{Name: "TaskScheduler"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/schedule/task.go", Language: "go"},
			expected: "schedule",
		},
		{
			name: "batch class",
			cls:  store.ClassRecord{Name: "BatchProcessor"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/batch/processor.go", Language: "go"},
			expected: "batch",
		},
		{
			name: "no tech tag",
			cls:  store.ClassRecord{Name: "OrderService"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/order/service.go", Language: "go"},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := DeriveClassTags(tt.cls, tt.file)
			found := false
			for _, tag := range tags {
				if tag == tt.expected {
					found = true
					break
				}
			}
			if tt.expected != "" && !found {
				t.Errorf("expected tech tag %q, got tags %v", tt.expected, tags)
			}
			if tt.expected == "" {
				for _, tag := range tags {
					if tag == "cache" || tag == "mq" || tag == "retry" ||
						tag == "transactional" || tag == "async" ||
						tag == "schedule" || tag == "batch" {
						t.Errorf("unexpected tech tag %q in tags %v", tag, tags)
					}
				}
			}
		})
	}
}

func TestDeriveClassTags_Risk(t *testing.T) {
	tests := []struct {
		name     string
		cls      store.ClassRecord
		file     *store.FileRecord
		expected string
	}{
		{
			name: "legacy class name",
			cls:  store.ClassRecord{Name: "LegacyOrderSystem"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/legacy/order.go", Language: "go"},
			expected: "legacy",
		},
		{
			name: "deprecated class name",
			cls:  store.ClassRecord{Name: "DeprecatedApi"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/deprecated/api.go", Language: "go"},
			expected: "deprecated",
		},
		{
			name: "performance class name",
			cls:  store.ClassRecord{Name: "PerformanceMonitor"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/monitor/perf.go", Language: "go"},
			expected: "performance",
		},
		{
			name: "security class name",
			cls:  store.ClassRecord{Name: "SecurityManager"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/security/manager.go", Language: "go"},
			expected: "security",
		},
		{
			name: "todo in doc",
			cls:  store.ClassRecord{Name: "OldService", Doc: "// TODO: refactor this service"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/old/service.go", Language: "go"},
			expected: "todo",
		},
		{
			name: "fixme in doc",
			cls:  store.ClassRecord{Name: "BuggyClass", Doc: "// FIXME: race condition here"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/buggy/class.go", Language: "go"},
			expected: "todo",
		},
		{
			name: "deprecated in doc",
			cls:  store.ClassRecord{Name: "OldApi", Doc: "/** @deprecated use NewApi instead */"},
			file: &store.FileRecord{AbsolutePath: "/project/src/main/java/com/example/old/OldApi.java", Language: "java"},
			expected: "deprecated",
		},
		{
			name: "no risk tag",
			cls:  store.ClassRecord{Name: "OrderService", Doc: "// Handles order operations"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/order/service.go", Language: "go"},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := DeriveClassTags(tt.cls, tt.file)
			found := false
			for _, tag := range tags {
				if tag == tt.expected {
					found = true
					break
				}
			}
			if tt.expected != "" && !found {
				t.Errorf("expected risk tag %q, got tags %v", tt.expected, tags)
			}
		})
	}
}

func TestDeriveClassTags_Test(t *testing.T) {
	tests := []struct {
		name     string
		cls      store.ClassRecord
		file     *store.FileRecord
		expected string
	}{
		{
			name: "go test file",
			cls:  store.ClassRecord{Name: "OrderServiceTest"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/order/service_test.go", Language: "go"},
			expected: "unit",
		},
		{
			name: "python test file",
			cls:  store.ClassRecord{Name: "TestOrderService"},
			file: &store.FileRecord{AbsolutePath: "/project/tests/test_order_service.py", Language: "python"},
			expected: "unit",
		},
		{
			name: "test directory",
			cls:  store.ClassRecord{Name: "TestHelper"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/test/helper.go", Language: "go"},
			expected: "unit",
		},
		{
			name: "integration test directory",
			cls:  store.ClassRecord{Name: "OrderIntegrationTest"},
			file: &store.FileRecord{AbsolutePath: "/project/src/integration/java/com/example/OrderIntegrationTest.java", Language: "java"},
			expected: "integration",
		},
		{
			name: "mock class name",
			cls:  store.ClassRecord{Name: "MockUserService"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/mock/user_service.go", Language: "go"},
			expected: "mock",
		},
		{
			name: "no test tag",
			cls:  store.ClassRecord{Name: "OrderService"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/order/service.go", Language: "go"},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := DeriveClassTags(tt.cls, tt.file)
			found := false
			for _, tag := range tags {
				if tag == tt.expected {
					found = true
					break
				}
			}
			if tt.expected != "" && !found {
				t.Errorf("expected test tag %q, got tags %v", tt.expected, tags)
			}
		})
	}
}

func TestDeriveClassTags_Lang(t *testing.T) {
	tests := []struct {
		name     string
		cls      store.ClassRecord
		file     *store.FileRecord
		expected string
	}{
		{
			name: "go language",
			cls:  store.ClassRecord{Name: "OrderService"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/order/service.go", Language: "go"},
			expected: "go",
		},
		{
			name: "java language",
			cls:  store.ClassRecord{Name: "OrderService"},
			file: &store.FileRecord{AbsolutePath: "/project/src/main/java/com/example/OrderService.java", Language: "java"},
			expected: "java",
		},
		{
			name: "python language",
			cls:  store.ClassRecord{Name: "OrderService"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/order/service.py", Language: "python"},
			expected: "python",
		},
		{
			name: "empty language",
			cls:  store.ClassRecord{Name: "OrderService"},
			file: &store.FileRecord{AbsolutePath: "/project/internal/order/service.go", Language: ""},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := DeriveClassTags(tt.cls, tt.file)
			found := false
			for _, tag := range tags {
				if tag == tt.expected {
					found = true
					break
				}
			}
			if tt.expected != "" && !found {
				t.Errorf("expected lang tag %q, got tags %v", tt.expected, tags)
			}
		})
	}
}

// ---------- DeriveMethodTags 测试 ----------

func TestDeriveMethodTags_Tech(t *testing.T) {
	tests := []struct {
		name     string
		method   store.MethodRecord
		expected string
	}{
		{name: "getCache", method: store.MethodRecord{Name: "getCache"}, expected: "cache"},
		{name: "sendMessage", method: store.MethodRecord{Name: "sendMessage"}, expected: "mq"},
		{name: "retryOperation", method: store.MethodRecord{Name: "retryOperation"}, expected: "retry"},
		{name: "beginTransaction", method: store.MethodRecord{Name: "beginTransaction"}, expected: "transactional"},
		{name: "asyncProcess", method: store.MethodRecord{Name: "asyncProcess"}, expected: "async"},
		{name: "scheduleTask", method: store.MethodRecord{Name: "scheduleTask"}, expected: "schedule"},
		{name: "batchImport", method: store.MethodRecord{Name: "batchImport"}, expected: "batch"},
		{name: "getOrder", method: store.MethodRecord{Name: "getOrder"}, expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := DeriveMethodTags(tt.method, store.ClassRecord{}, &store.FileRecord{})
			found := false
			for _, tag := range tags {
				if tag == tt.expected {
					found = true
					break
				}
			}
			if tt.expected != "" && !found {
				t.Errorf("expected tech tag %q, got tags %v", tt.expected, tags)
			}
		})
	}
}

func TestDeriveMethodTags_Risk(t *testing.T) {
	tests := []struct {
		name     string
		method   store.MethodRecord
		expected string
	}{
		{
			name:     "todo in doc",
			method:   store.MethodRecord{Name: "oldMethod", Doc: "// TODO: optimize this method"},
			expected: "todo",
		},
		{
			name:     "fixme in doc",
			method:   store.MethodRecord{Name: "buggyMethod", Doc: "// FIXME: fix null pointer"},
			expected: "todo",
		},
		{
			name:     "deprecated in doc",
			method:   store.MethodRecord{Name: "oldApi", Doc: "/** @deprecated use newMethod instead */"},
			expected: "deprecated",
		},
		{
			name:     "no risk",
			method:   store.MethodRecord{Name: "getOrder", Doc: "// Returns the order"},
			expected: "",
		},
		{
			name:     "empty doc",
			method:   store.MethodRecord{Name: "getOrder", Doc: ""},
			expected: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := DeriveMethodTags(tt.method, store.ClassRecord{}, &store.FileRecord{})
			found := false
			for _, tag := range tags {
				if tag == tt.expected {
					found = true
					break
				}
			}
			if tt.expected != "" && !found {
				t.Errorf("expected risk tag %q, got tags %v", tt.expected, tags)
			}
		})
	}
}

func TestDeriveMethodTags_Test(t *testing.T) {
	tests := []struct {
		name     string
		method   store.MethodRecord
		expected string
	}{
		{name: "TestGetOrder", method: store.MethodRecord{Name: "TestGetOrder"}, expected: "mock"},
		{name: "testGetOrder", method: store.MethodRecord{Name: "testGetOrder"}, expected: "mock"},
		{name: "test_get_order", method: store.MethodRecord{Name: "test_get_order"}, expected: "mock"},
		{name: "ShouldReturnOrder", method: store.MethodRecord{Name: "ShouldReturnOrder"}, expected: "mock"},
		{name: "should_return_order", method: store.MethodRecord{Name: "should_return_order"}, expected: "mock"},
		{name: "mockUserService", method: store.MethodRecord{Name: "mockUserService"}, expected: "mock"},
		{name: "getOrder", method: store.MethodRecord{Name: "getOrder"}, expected: ""},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			tags := DeriveMethodTags(tt.method, store.ClassRecord{}, &store.FileRecord{})
			found := false
			for _, tag := range tags {
				if tag == tt.expected {
					found = true
					break
				}
			}
			if tt.expected != "" && !found {
				t.Errorf("expected test tag %q, got tags %v", tt.expected, tags)
			}
		})
	}
}

// ---------- DeriveAllTags 集成测试 ----------

// mockStoreWithTags 实现 Store 接口，用于测试 DeriveAllTags。
type mockStoreWithTags struct {
	files      []*store.FileRecord
	classes    map[int64][]store.ClassRecord
	methods    map[int64][]store.MethodRecord
	classTags  map[int64][]string
	methodTags map[int64][]string
}

func (m *mockStoreWithTags) Open(_ context.Context, _ string) error    { return nil }
func (m *mockStoreWithTags) Close() error                               { return nil }
func (m *mockStoreWithTags) HealthCheck(_ context.Context) error        { return nil }
func (m *mockStoreWithTags) UpsertFile(_ context.Context, _ string, _ string, _ int, _ int64) (int64, error) { return 0, nil }
func (m *mockStoreWithTags) GetFileByPath(_ context.Context, _ string) (*store.FileRecord, error) { return nil, nil }
func (m *mockStoreWithTags) GetFileByID(_ context.Context, _ int64) (*store.FileRecord, error)    { return nil, nil }
func (m *mockStoreWithTags) DeleteFile(_ context.Context, _ int64) error                          { return nil }
func (m *mockStoreWithTags) UpsertClasses(_ context.Context, _ int64, _ []parser.ClassIR) error   { return nil }
func (m *mockStoreWithTags) UpsertMethods(_ context.Context, _ int64, _ []parser.MethodIR) error  { return nil }
func (m *mockStoreWithTags) UpsertCalls(_ context.Context, _ int64, _ []parser.CallIR) error      { return nil }
func (m *mockStoreWithTags) UpsertIR(_ context.Context, _ *parser.IRDocument) error               { return nil }

func (m *mockStoreWithTags) UpsertTags(_ context.Context, classID int64, tags []string) error {
	if m.classTags == nil {
		m.classTags = make(map[int64][]string)
	}
	m.classTags[classID] = tags
	return nil
}

func (m *mockStoreWithTags) UpsertMethodTags(_ context.Context, methodID int64, tags []string) error {
	if m.methodTags == nil {
		m.methodTags = make(map[int64][]string)
	}
	m.methodTags[methodID] = tags
	return nil
}

func (m *mockStoreWithTags) GetTagsByClassID(_ context.Context, classID int64) ([]string, error) {
	if m.classTags == nil {
		return nil, nil
	}
	return m.classTags[classID], nil
}

func (m *mockStoreWithTags) GetTagsByMethodID(_ context.Context, methodID int64) ([]string, error) {
	if m.methodTags == nil {
		return nil, nil
	}
	return m.methodTags[methodID], nil
}

func (m *mockStoreWithTags) SearchByTag(_ context.Context, _ string) ([]int64, []int64, error) { return nil, nil, nil }
func (m *mockStoreWithTags) GetAllTagsWithCategories(_ context.Context) (map[string]string, error) { return nil, nil }

func (m *mockStoreWithTags) GetAllFiles(_ context.Context) ([]*store.FileRecord, error) {
	return m.files, nil
}

func (m *mockStoreWithTags) GetClassesByFileID(_ context.Context, fileID int64) ([]store.ClassRecord, error) {
	return m.classes[fileID], nil
}

func (m *mockStoreWithTags) GetMethodsByClassID(_ context.Context, classID int64) ([]store.MethodRecord, error) {
	return m.methods[classID], nil
}

func (m *mockStoreWithTags) GetCallsByFileID(_ context.Context, _ int64) ([]store.CallRecord, error) {
	return nil, nil
}

func TestDeriveAllTags(t *testing.T) {
	ctx := context.Background()

	store := &mockStoreWithTags{
		files: []*store.FileRecord{
			{ID: 1, AbsolutePath: "/project/internal/order/service.go", Language: "go"},
			{ID: 2, AbsolutePath: "/project/src/main/java/com/example/user/UserController.java", Language: "java"},
		},
		classes: map[int64][]store.ClassRecord{
			1: {{ID: 10, FileID: 1, Name: "OrderService", Doc: "// Handles order operations"}},
			2: {{ID: 20, FileID: 2, Name: "UserController", Doc: "// REST controller for user management"}},
		},
		methods: map[int64][]store.MethodRecord{
			10: {
				{ID: 100, ClassID: 10, Name: "getOrder", Doc: "// Returns order by ID"},
				{ID: 101, ClassID: 10, Name: "retryPayment", Doc: "// TODO: add timeout handling"},
			},
			20: {
				{ID: 200, ClassID: 20, Name: "getUser", Doc: "// Returns user by ID"},
				{ID: 201, ClassID: 20, Name: "cacheUser", Doc: "// Caches user data"},
			},
		},
	}

	tagger := NewTagger(store)
	if err := tagger.DeriveAllTags(ctx); err != nil {
		t.Fatalf("DeriveAllTags failed: %v", err)
	}

	// 验证 OrderService 的标签
	orderTags, err := store.GetTagsByClassID(ctx, 10)
	if err != nil {
		t.Fatalf("GetTagsByClassID failed: %v", err)
	}
	assertContainsTag(t, orderTags, "service", "OrderService 应有 layer=service")
	assertContainsTag(t, orderTags, "order", "OrderService 应有 biz=order")
	assertContainsTag(t, orderTags, "go", "OrderService 应有 lang=go")

	// 验证 UserController 的标签
	userTags, err := store.GetTagsByClassID(ctx, 20)
	if err != nil {
		t.Fatalf("GetTagsByClassID failed: %v", err)
	}
	assertContainsTag(t, userTags, "controller", "UserController 应有 layer=controller")
	assertContainsTag(t, userTags, "user", "UserController 应有 biz=user")
	assertContainsTag(t, userTags, "java", "UserController 应有 lang=java")

	// 验证方法标签
	methodTags100, err := store.GetTagsByMethodID(ctx, 100)
	if err != nil {
		t.Fatalf("GetTagsByMethodID failed: %v", err)
	}
	// getOrder 没有 tech 特征，不应有 tech 标签
	techTags := map[string]bool{"cache": true, "mq": true, "retry": true, "transactional": true,
		"async": true, "schedule": true, "batch": true}
	for _, tag := range methodTags100 {
		if techTags[tag] {
			t.Errorf("getOrder 不应有 tech 标签 %q, 实际 tags %v", tag, methodTags100)
		}
	}

	// retryPayment 应有 tech=retry 和 risk=todo
	methodTags101, err := store.GetTagsByMethodID(ctx, 101)
	if err != nil {
		t.Fatalf("GetTagsByMethodID failed: %v", err)
	}
	assertContainsTag(t, methodTags101, "retry", "retryPayment 应有 tech=retry")
	assertContainsTag(t, methodTags101, "todo", "retryPayment 应有 risk=todo")

	// cacheUser 应有 tech=cache
	methodTags201, err := store.GetTagsByMethodID(ctx, 201)
	if err != nil {
		t.Fatalf("GetTagsByMethodID failed: %v", err)
	}
	assertContainsTag(t, methodTags201, "cache", "cacheUser 应有 tech=cache")
}

func assertContainsTag(t *testing.T, tags []string, expected string, msg string) {
	t.Helper()
	for _, tag := range tags {
		if tag == expected {
			return
		}
	}
	t.Errorf("%s: 期望包含 %q, 实际 %v", msg, expected, tags)
}

