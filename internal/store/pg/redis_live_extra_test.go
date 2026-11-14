//go:build pg && redis

// PG + Redis 真实实例扩展验证：命名空间隔离、陈旧键窗口与失效、PG/Redis 并发、
// 冷启动（空 Redis 命名空间）首次查询。
//
// 运行：
//
//	CODESCHEMA_REDIS_ADDR=redis://127.0.0.1:6379/0 \
//	go test -tags 'pg redis' -run 'Test(RedisCache_NamespaceIsolation|RedisCache_StaleKey|PGAndRedis_Concurrent|ColdStart)' -v -timeout 300s ./internal/store/pg
//
// PG 走 CODESCHEMA_PG_DSN / localhost:5432 / embedded-postgres 三档回退；
// Redis 不可达时 Skip，不做模拟替代。产物：docs/bench/2026-11-14/redis-*.json
// 与 pg-redis-concurrency.json（仅真实跑通时写出）。
package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/store/redis"
	goredis "github.com/redis/go-redis/v9"
)

// redisTestURL 归一化 Redis 地址：允许 "host:port" 或 redis:// URL。
func redisTestURL() string {
	raw := os.Getenv("CODESCHEMA_REDIS_ADDR")
	if raw == "" {
		return "redis://localhost:6379/0"
	}
	if !strings.Contains(raw, "://") {
		return "redis://" + raw + "/0"
	}
	return raw
}

// newLiveCache 连接真实 Redis；不可达时跳过，不用模拟实例顶替。
func newLiveCache(t *testing.T, namespace string) *redis.RedisCache {
	t.Helper()
	c, err := redis.NewRedisCacheFromURLWithNamespace(redisTestURL(), namespace)
	if err != nil {
		t.Skipf("Redis 不可达（%v），跳过活实例测试", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := c.HealthCheck(ctx); err != nil {
		_ = c.Close()
		t.Skipf("Redis 不可达（%v），跳过活实例测试", err)
	}
	return c
}

func writeBenchJSON(t *testing.T, name string, v any) {
	t.Helper()
	dir := filepath.Join("..", "..", "..", "docs", "bench", "2026-11-14")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatalf("mkdir bench dir: %v", err)
	}
	raw, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatalf("marshal %s: %v", name, err)
	}
	if err := os.WriteFile(filepath.Join(dir, name), raw, 0o644); err != nil {
		t.Fatalf("write %s: %v", name, err)
	}
}

// replaceDBName 把 DSN 的库名替换为 db（兼容 URL 与 keyword 两种写法）。
func replaceDBName(dsn, db string) (string, error) {
	if u, err := url.Parse(dsn); err == nil && (u.Scheme == "postgres" || u.Scheme == "postgresql") {
		u.Path = "/" + db
		return u.String(), nil
	}
	if strings.Contains(dsn, "://") {
		return "", fmt.Errorf("unsupported dsn: %s", dsn)
	}
	return dsn + " dbname=" + db, nil
}

// withIsolatedPG 在可用实例上建一个临时库，避免与共享库已有数据互相干扰
// （共享库可能有数万文件，冷启动全表回退会把测试拖到超时）。
// 返回 (dsn, cleanup)；cleanup 关连接并 DROP DATABASE。
func withIsolatedPG(t *testing.T, ctx context.Context) (string, func()) {
	t.Helper()
	baseDSN, baseCleanup := withPG(t, ctx)
	dbName := fmt.Sprintf("cs_it_%d", time.Now().UnixNano())
	adminDSN, err := replaceDBName(baseDSN, "postgres")
	if err != nil {
		baseCleanup()
		t.Fatalf("admin dsn: %v", err)
	}
	admin, err := sql.Open("postgres", adminDSN)
	if err != nil {
		baseCleanup()
		t.Fatalf("admin open: %v", err)
	}
	if _, err := admin.ExecContext(ctx, "CREATE DATABASE "+dbName); err != nil {
		admin.Close()
		baseCleanup()
		t.Fatalf("create temp database: %v", err)
	}
	admin.Close()

	dsn, err := replaceDBName(baseDSN, dbName)
	if err != nil {
		baseCleanup()
		t.Fatalf("temp dsn: %v", err)
	}
	cleanup := func() {
		if admin, err := sql.Open("postgres", adminDSN); err == nil {
			_, _ = admin.ExecContext(context.Background(),
				"SELECT pg_terminate_backend(pid) FROM pg_stat_activity WHERE datname = $1", dbName)
			_, _ = admin.ExecContext(context.Background(), "DROP DATABASE IF EXISTS "+dbName)
			admin.Close()
		}
		baseCleanup()
	}
	return dsn, cleanup
}

// TestRedisCache_NamespaceIsolation 活实例读写隔离：同名 FQN 在不同命名空间互不覆盖，
// Flush 只影响本命名空间。
func TestRedisCache_NamespaceIsolation(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	stamp := time.Now().UnixNano()
	ns1 := fmt.Sprintf("it-ns1-%d", stamp)
	ns2 := fmt.Sprintf("it-ns2-%d", stamp)
	c1 := newLiveCache(t, ns1)
	defer c1.Close()
	defer func() { _ = c1.Flush(ctx) }()
	c2 := newLiveCache(t, ns2)
	defer c2.Close()
	defer func() { _ = c2.Flush(ctx) }()

	const fqn = "it.Shared.Svc"
	if err := c1.PutClass(ctx, &parser.ClassIR{Name: "One", FullName: fqn, Type: "CLASS"}); err != nil {
		t.Fatalf("ns1 PutClass: %v", err)
	}
	if err := c2.PutClass(ctx, &parser.ClassIR{Name: "Two", FullName: fqn, Type: "CLASS"}); err != nil {
		t.Fatalf("ns2 PutClass: %v", err)
	}
	if err := c1.PutClassPath(ctx, fqn, "/repo/a.go"); err != nil {
		t.Fatalf("ns1 PutClassPath: %v", err)
	}
	if err := c2.PutClassPath(ctx, fqn, "/repo/b.go"); err != nil {
		t.Fatalf("ns2 PutClassPath: %v", err)
	}

	got1, _ := c1.GetClass(ctx, fqn)
	got2, _ := c2.GetClass(ctx, fqn)
	if got1 == nil || got1.Name != "One" {
		t.Fatalf("ns1 类被覆盖: %+v", got1)
	}
	if got2 == nil || got2.Name != "Two" {
		t.Fatalf("ns2 类被覆盖: %+v", got2)
	}
	if p, _ := c1.ClassPath(ctx, fqn); p != "/repo/a.go" {
		t.Fatalf("ns1 路径被覆盖: %q", p)
	}
	if p, _ := c2.ClassPath(ctx, fqn); p != "/repo/b.go" {
		t.Fatalf("ns2 路径被覆盖: %q", p)
	}

	// Flush ns1 不得影响 ns2。
	if err := c1.Flush(ctx); err != nil {
		t.Fatalf("ns1 Flush: %v", err)
	}
	if got, _ := c1.GetClass(ctx, fqn); got != nil {
		t.Fatalf("ns1 Flush 后仍可读: %+v", got)
	}
	if got, _ := c2.GetClass(ctx, fqn); got == nil || got.Name != "Two" {
		t.Fatalf("ns1 Flush 误伤 ns2: %+v", got)
	}

	writeBenchJSON(t, "redis-namespace-isolation.json", map[string]any{
		"verified_at": time.Now().Format(time.RFC3339),
		"redis":       redisTestURL(),
		"namespaces":  []string{ns1, ns2},
		"result":      "两个命名空间同名 FQN 互不覆盖；Flush 仅清理本命名空间",
	})
}

// cacheReaderPG 把 PG 主存储与 Redis 缓存组装成 store.CacheReader，
// 与 cmd/codeschema 的 redisCacheStore 读路径同构（测试内适配器，避免跨包依赖）。
type cacheReaderPG struct {
	store.Store
	cache *redis.RedisCache
}

func (c *cacheReaderPG) GetClass(ctx context.Context, fqn string) (*parser.ClassIR, error) {
	return c.cache.GetClass(ctx, fqn)
}
func (c *cacheReaderPG) ClassFilePath(ctx context.Context, fqn string) (string, bool) {
	return c.cache.ClassPath(ctx, fqn)
}
func (c *cacheReaderPG) GetMethod(ctx context.Context, fqn string) (*parser.MethodIR, error) {
	return c.cache.GetMethod(ctx, fqn)
}
func (c *cacheReaderPG) MethodFilePath(ctx context.Context, fqn string) (string, bool) {
	return c.cache.MethodPath(ctx, fqn)
}
func (c *cacheReaderPG) CallersOf(ctx context.Context, fqn string) ([]string, error) {
	return c.cache.CallersOf(ctx, fqn)
}
func (c *cacheReaderPG) CalleesOf(ctx context.Context, fqn string) ([]string, error) {
	return c.cache.CalleesOf(ctx, fqn)
}
func (c *cacheReaderPG) ClassesOfFile(ctx context.Context, path string) ([]string, error) {
	return c.cache.ClassesOfFile(ctx, path)
}

// TestRedisCache_StaleKeyWindowAndColdStart 活实例验证：
//  1. 空主存储 + 空 Redis 首次查询返回 not found 而非报错；
//  2. 冷启动（Redis 空）首次查询回退 PG 并返回正确源码，且耗时随索引规模增长
//     （回退路径是全表遍历，非 O(1)）；
//  3. 缓存写入后删除 PG 行，查询仍命中 Redis（陈旧键窗口，由 TTL 兜底）；
//  4. InvalidateFile 后回退 PG，返回 ERR_SYMBOL_NOT_FOUND（失效闭环）。
func TestRedisCache_StaleKeyWindowAndColdStart(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	dsn, cleanup := withIsolatedPG(t, ctx)
	defer cleanup()

	pgStore := NewPGStore()
	if err := pgStore.Open(ctx, dsn); err != nil {
		t.Fatalf("PG open: %v", err)
	}
	defer pgStore.Close()
	if err := pgStore.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}

	ns := fmt.Sprintf("it-cold-%d", time.Now().UnixNano())
	cache := newLiveCache(t, ns)
	defer cache.Close()
	defer func() { _ = cache.Flush(ctx) }()
	if err := cache.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	srcPath := filepath.Join(t.TempDir(), "svc.go")
	src := "package it\n\n// Svc 是测试类。\ntype Svc struct{}\n\n// Run 是测试方法。\nfunc Run() error {\n\treturn nil\n}\n"
	if err := os.WriteFile(srcPath, []byte(src), 0o600); err != nil {
		t.Fatal(err)
	}
	methodFQN := "it.Svc.Run"
	ir := &parser.IRDocument{
		Source: "pg-redis-it", Language: "go", FilePath: srcPath, FileHash: "h1",
		LineCount: 9, ByteSize: int64(len(src)),
		Classes: []parser.ClassIR{{Name: "Svc", FullName: "it.Svc", Type: "CLASS", StartLine: 4, EndLine: 9}},
		Methods: []parser.MethodIR{{Name: "Run", ClassFQN: "it.Svc", Signature: "Run() error", StartLine: 7, EndLine: 9}},
	}

	// 1) 空主存储 + 空缓存：首次查询 not found，不报错。
	start := time.Now()
	_, err := service.NewService(&cacheReaderPG{Store: pgStore, cache: cache}).GetContext(ctx, methodFQN, 1)
	emptyDur := time.Since(start)
	if se, ok := err.(*service.ServiceError); !ok || se.Code != "ERR_SYMBOL_NOT_FOUND" {
		t.Fatalf("空索引查询应返回 ERR_SYMBOL_NOT_FOUND，实际 %v", err)
	}

	// 2) 冷启动：仅 1 文件索引。
	if err := pgStore.UpsertIR(ctx, ir); err != nil {
		t.Fatalf("UpsertIR: %v", err)
	}
	if m, _ := cache.GetMethod(ctx, methodFQN); m != nil {
		t.Fatalf("冷启动前缓存不应命中: %+v", m)
	}
	start = time.Now()
	got, err := service.NewService(&cacheReaderPG{Store: pgStore, cache: cache}).GetContext(ctx, methodFQN, 1)
	singleFileDur := time.Since(start)
	if err != nil {
		t.Fatalf("冷启动首次查询: %v", err)
	}
	if !strings.Contains(got.Source, "func Run() error") || got.FilePath != srcPath {
		t.Fatalf("冷启动查询结果错误: path=%q source=%q", got.FilePath, got.Source)
	}

	// 2b) 冷启动：扩充到 1000 文件索引，验证回退成本随规模增长。
	const fillerFiles = 999
	fillerDir := filepath.Join(t.TempDir(), "filler")
	for i := 0; i < fillerFiles; i++ {
		fqn := fmt.Sprintf("it.Filler.F%d", i)
		fir := &parser.IRDocument{
			Source: "pg-redis-it", Language: "go",
			FilePath: filepath.Join(fillerDir, fmt.Sprintf("f%d.go", i)), FileHash: "hf",
			Classes: []parser.ClassIR{{Name: fqn, FullName: fqn, Type: "CLASS"}},
			Methods: []parser.MethodIR{{Name: "Run", ClassFQN: fqn}},
		}
		if err := pgStore.UpsertIR(ctx, fir); err != nil {
			t.Fatalf("filler upsert %d: %v", i, err)
		}
	}
	if err := cache.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	start = time.Now()
	got, err = service.NewService(&cacheReaderPG{Store: pgStore, cache: cache}).GetContext(ctx, methodFQN, 1)
	scaledDur := time.Since(start)
	if err != nil {
		t.Fatalf("1000 文件冷启动查询: %v", err)
	}
	if !strings.Contains(got.Source, "func Run() error") {
		t.Fatalf("1000 文件冷启动结果错误: %q", got.Source)
	}
	// 2c) 最小模式冷启动：只测定位路径（不读源码、不扫关联单测），
	// 与随后的缓存命中对比，隔离出「全表回退 vs Redis 直接命中」的差异。
	start = time.Now()
	if _, err := service.NewService(&cacheReaderPG{Store: pgStore, cache: cache}).
		GetContextMode(ctx, methodFQN, service.ContextOptions{Mode: service.ModeMinimal}); err != nil {
		t.Fatalf("最小模式冷启动查询: %v", err)
	}
	coldMinimalDur := time.Since(start)

	// 3) 写入缓存（等价生产 populate），删除 PG 行后仍命中 Redis：陈旧键窗口。
	if err := cache.SyncIR(ctx, ir); err != nil {
		t.Fatalf("SyncIR: %v", err)
	}
	opt, err := goredis.ParseURL(redisTestURL())
	if err != nil {
		t.Fatalf("parse redis url: %v", err)
	}
	raw := goredis.NewClient(opt)
	defer raw.Close()
	ttl, err := raw.TTL(ctx, ns+":method:"+methodFQN).Result()
	if err != nil {
		t.Fatalf("TTL: %v", err)
	}
	if ttl <= 0 {
		t.Fatalf("缓存键 TTL 应为正数，实际 %v", ttl)
	}
	f, err := pgStore.GetFileByPath(ctx, srcPath)
	if err != nil || f == nil {
		t.Fatalf("GetFileByPath: %v (%v)", err, f)
	}
	if err := pgStore.DeleteFile(ctx, f.ID); err != nil {
		t.Fatalf("DeleteFile: %v", err)
	}
	staleSvc := service.NewService(&cacheReaderPG{Store: pgStore, cache: cache})
	// 缓存命中（最小模式）：定位只走 Redis + 路径反查，不触全表。
	start = time.Now()
	if _, err := staleSvc.GetContextMode(ctx, methodFQN, service.ContextOptions{Mode: service.ModeMinimal}); err != nil {
		t.Fatalf("陈旧键窗口内最小模式查询应命中 Redis: %v", err)
	}
	staleMinimalDur := time.Since(start)
	// 缓存命中（完整模式）：验证内容来自 Redis 记录的路径。
	start = time.Now()
	stale, err := staleSvc.GetContext(ctx, methodFQN, 1)
	staleDur := time.Since(start)
	if err != nil {
		t.Fatalf("陈旧键窗口内查询应命中 Redis: %v", err)
	}
	if !strings.Contains(stale.Source, "func Run() error") {
		t.Fatalf("陈旧键命中结果错误: %q", stale.Source)
	}

	// 4) 失效后回退 PG（已删除）→ not found。
	if err := cache.InvalidateFile(ctx, srcPath); err != nil {
		t.Fatalf("InvalidateFile: %v", err)
	}
	afterSvc := service.NewService(&cacheReaderPG{Store: pgStore, cache: cache})
	if _, err := afterSvc.GetContext(ctx, methodFQN, 1); err == nil {
		t.Fatal("失效后应返回 ERR_SYMBOL_NOT_FOUND，实际成功")
	} else if se, ok := err.(*service.ServiceError); !ok || se.Code != "ERR_SYMBOL_NOT_FOUND" {
		t.Fatalf("失效后错误码不符: %v", err)
	}

	writeBenchJSON(t, "redis-stale-coldstart.json", map[string]any{
		"verified_at":              time.Now().Format(time.RFC3339),
		"redis":                    redisTestURL(),
		"pg_dsn":                   dsn,
		"namespace":                ns,
		"empty_index_ms":           float64(emptyDur.Microseconds()) / 1000,
		"cold_start_1_file_ms":     float64(singleFileDur.Microseconds()) / 1000,
		"cold_start_1000_files_ms": float64(scaledDur.Microseconds()) / 1000,
		"cold_start_minimal_ms":    float64(coldMinimalDur.Microseconds()) / 1000,
		"stale_hit_minimal_ms":     float64(staleMinimalDur.Microseconds()) / 1000,
		"stale_hit_full_ms":        float64(staleDur.Microseconds()) / 1000,
		"cache_key_ttl_seconds":    ttl.Seconds(),
		"cold_start_fallback":      "全表遍历（GetAllFiles + 每文件查类/方法），成本随索引规模线性增长",
		"stale_hit_note":           "完整模式含关联单测扫描（FindTestLinksWithStats），与缓存命中无关；最小模式隔离定位路径",
		"result":                   "冷启动回退 PG 正确；陈旧键窗口存在且由 InvalidateFile 闭环；空索引首次查询 not found",
	})
	t.Logf("cold1=%.2fms cold1000=%.2fms coldMinimal=%.2fms staleMinimal=%.2fms staleFull=%.2fms empty=%.2fms ttl=%.0fs",
		float64(singleFileDur.Microseconds())/1000, float64(scaledDur.Microseconds())/1000,
		float64(coldMinimalDur.Microseconds())/1000, float64(staleMinimalDur.Microseconds())/1000,
		float64(staleDur.Microseconds())/1000, float64(emptyDur.Microseconds())/1000, ttl.Seconds())
}

// TestPGAndRedis_Concurrent PG 与 Redis 并发：多写多读混合，断言零错误与终态一致，
// 记录 P50/P95。
func TestPGAndRedis_Concurrent(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Second)
	defer cancel()
	dsn, cleanup := withPG(t, ctx)
	defer cleanup()

	pgStore := NewPGStore()
	if err := pgStore.Open(ctx, dsn); err != nil {
		t.Fatalf("PG open: %v", err)
	}
	defer pgStore.Close()
	if err := pgStore.InitSchema(ctx); err != nil {
		t.Fatalf("InitSchema: %v", err)
	}
	ns := fmt.Sprintf("it-conc-%d", time.Now().UnixNano())
	cache := newLiveCache(t, ns)
	defer cache.Close()
	defer func() { _ = cache.Flush(ctx) }()
	if err := cache.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}

	const (
		writers      = 8
		filesPerW    = 10
		readers      = 8
		readsPerRead = 50
	)
	stamp := time.Now().UnixNano()
	anchorIRs := make([]*parser.IRDocument, writers)
	for w := 0; w < writers; w++ {
		path := fmt.Sprintf("/tmp/pg-redis-it/%d/anchor-%d.go", stamp, w)
		fqn := fmt.Sprintf("it.Concurrent.Anchor%d", w)
		anchorIRs[w] = &parser.IRDocument{
			Source: "pg-redis-it", Language: "go", FilePath: path, FileHash: "anchor",
			Classes: []parser.ClassIR{{Name: fqn, FullName: fqn, Type: "CLASS"}},
			Methods: []parser.MethodIR{{Name: "Run", ClassFQN: fqn}},
		}
		if err := pgStore.UpsertIR(ctx, anchorIRs[w]); err != nil {
			t.Fatalf("anchor PG upsert: %v", err)
		}
		if err := cache.SyncIR(ctx, anchorIRs[w]); err != nil {
			t.Fatalf("anchor cache sync: %v", err)
		}
	}

	var (
		mu        sync.Mutex
		errs      []string
		latencies []time.Duration
		wg        sync.WaitGroup
	)
	record := func(d time.Duration, err error) {
		mu.Lock()
		defer mu.Unlock()
		if err != nil {
			errs = append(errs, err.Error())
			return
		}
		latencies = append(latencies, d)
	}
	fail := func(format string, args ...any) {
		mu.Lock()
		errs = append(errs, fmt.Sprintf(format, args...))
		mu.Unlock()
	}

	for w := 0; w < writers; w++ {
		w := w
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < filesPerW; i++ {
				path := fmt.Sprintf("/tmp/pg-redis-it/%d/w%d-%d.go", stamp, w, i)
				fqn := fmt.Sprintf("it.Concurrent.W%d.F%d", w, i)
				ir := &parser.IRDocument{
					Source: "pg-redis-it", Language: "go", FilePath: path, FileHash: "h",
					Classes: []parser.ClassIR{{Name: fqn, FullName: fqn, Type: "CLASS"}},
					Methods: []parser.MethodIR{{Name: "Run", ClassFQN: fqn}},
					Calls:   []parser.CallIR{{CallerFQN: fqn + ".Run", CalleeFQN: "it.Target"}},
				}
				start := time.Now()
				if err := pgStore.UpsertIR(ctx, ir); err != nil {
					fail("writer %d upsert %s: %v", w, path, err)
					continue
				}
				if err := cache.SyncIR(ctx, ir); err != nil {
					fail("writer %d cache sync %s: %v", w, path, err)
					continue
				}
				record(time.Since(start), nil)
			}
		}()
	}
	for r := 0; r < readers; r++ {
		r := r
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < readsPerRead; i++ {
				anchor := anchorIRs[(r+i)%writers]
				start := time.Now()
				f, err := pgStore.GetFileByPath(ctx, anchor.FilePath)
				if err != nil || f == nil {
					fail("reader %d GetFileByPath %s: %v (%v)", r, anchor.FilePath, err, f)
					continue
				}
				cls, err := cache.GetClass(ctx, anchor.Classes[0].FullName)
				if err != nil || cls == nil {
					fail("reader %d GetClass: %v (%v)", r, err, cls)
					continue
				}
				if _, err := cache.CalleesOf(ctx, anchor.Classes[0].FullName+".Run"); err != nil {
					fail("reader %d CalleesOf: %v", r, err)
					continue
				}
				record(time.Since(start), nil)
			}
		}()
	}
	wg.Wait()

	mu.Lock()
	gotErrs := append([]string(nil), errs...)
	gotLat := append([]time.Duration(nil), latencies...)
	mu.Unlock()
	if len(gotErrs) != 0 {
		t.Fatalf("并发错误 %d 例，首例: %s", len(gotErrs), gotErrs[0])
	}

	// 终态一致：所有写入类在 PG 与 Redis 均可读。
	for w := 0; w < writers; w++ {
		for i := 0; i < filesPerW; i++ {
			path := fmt.Sprintf("/tmp/pg-redis-it/%d/w%d-%d.go", stamp, w, i)
			fqn := fmt.Sprintf("it.Concurrent.W%d.F%d", w, i)
			if f, err := pgStore.GetFileByPath(ctx, path); err != nil || f == nil {
				t.Fatalf("终态 PG 缺失 %s: %v", path, err)
			}
			if cls, err := cache.GetClass(ctx, fqn); err != nil || cls == nil {
				t.Fatalf("终态 Redis 缺失 %s: %v", fqn, err)
			}
			if callees, err := cache.CalleesOf(ctx, fqn+".Run"); err != nil || len(callees) != 1 {
				t.Fatalf("终态调用边不一致 %s: %v (%v)", fqn, err, callees)
			}
			// 清理，避免共享 PG 实例累积数据。
			if f, _ := pgStore.GetFileByPath(ctx, path); f != nil {
				_ = pgStore.DeleteFile(ctx, f.ID)
			}
		}
	}
	for _, ir := range anchorIRs {
		if f, _ := pgStore.GetFileByPath(ctx, ir.FilePath); f != nil {
			_ = pgStore.DeleteFile(ctx, f.ID)
		}
	}

	sort.Slice(gotLat, func(i, j int) bool { return gotLat[i] < gotLat[j] })
	writeBenchJSON(t, "pg-redis-concurrency.json", map[string]any{
		"verified_at":  time.Now().Format(time.RFC3339),
		"redis":        redisTestURL(),
		"pg_dsn":       dsn,
		"writers":      writers,
		"files_per_w":  filesPerW,
		"readers":      readers,
		"reads_per_rd": readsPerRead,
		"ops":          len(gotLat),
		"errors":       len(gotErrs),
		"p50_ms":       float64(percentile(gotLat, 0.50).Microseconds()) / 1000,
		"p95_ms":       float64(percentile(gotLat, 0.95).Microseconds()) / 1000,
		"result":       "零错误；PG 与 Redis 终态一致（类/调用边可读）",
	})
	t.Logf("concurrent ops=%d p50=%.2fms p95=%.2fms errs=%d", len(gotLat),
		float64(percentile(gotLat, 0.50).Microseconds())/1000,
		float64(percentile(gotLat, 0.95).Microseconds())/1000, len(gotErrs))
}

func percentile(sorted []time.Duration, p float64) time.Duration {
	if len(sorted) == 0 {
		return 0
	}
	idx := int(float64(len(sorted)-1) * p)
	return sorted[idx]
}
