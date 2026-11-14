//go:build redis

package redis

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/parser"
)

// memBackend 是 kvBackend 的内存实现，用于确定性验证命名空间/TTL/Flush 范围。
type memBackend struct {
	mu     sync.Mutex
	hashes map[string]map[string]string
	sets   map[string]map[string]struct{}
	strs   map[string]string
	ttl    map[string]time.Duration
}

func newMemBackend() *memBackend {
	return &memBackend{
		hashes: map[string]map[string]string{},
		sets:   map[string]map[string]struct{}{},
		strs:   map[string]string{},
		ttl:    map[string]time.Duration{},
	}
}

func (m *memBackend) HSet(_ context.Context, key string, values ...any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.hashes[key] == nil {
		m.hashes[key] = map[string]string{}
	}
	for i := 0; i+1 < len(values); i += 2 {
		field, ok := values[i].(string)
		if !ok {
			return fmt.Errorf("field must be string")
		}
		switch v := values[i+1].(type) {
		case string:
			m.hashes[key][field] = v
		case []byte:
			m.hashes[key][field] = string(v)
		default:
			m.hashes[key][field] = fmt.Sprint(v)
		}
	}
	return nil
}

func (m *memBackend) HGet(_ context.Context, key, field string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	h, ok := m.hashes[key]
	if !ok {
		return "", errKeyNotFound
	}
	v, ok := h[field]
	if !ok {
		return "", errKeyNotFound
	}
	return v, nil
}

func (m *memBackend) SAdd(_ context.Context, key string, members ...any) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.sets[key] == nil {
		m.sets[key] = map[string]struct{}{}
	}
	for _, raw := range members {
		switch v := raw.(type) {
		case string:
			m.sets[key][v] = struct{}{}
		case []string:
			for _, s := range v {
				m.sets[key][s] = struct{}{}
			}
		default:
			m.sets[key][fmt.Sprint(v)] = struct{}{}
		}
	}
	return nil
}

func (m *memBackend) SMembers(_ context.Context, key string) ([]string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]string, 0, len(m.sets[key]))
	for v := range m.sets[key] {
		out = append(out, v)
	}
	return out, nil
}

func (m *memBackend) Set(_ context.Context, key, value string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.strs[key] = value
	if ttl > 0 {
		m.ttl[key] = ttl
	}
	return nil
}

func (m *memBackend) Get(_ context.Context, key string) (string, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	v, ok := m.strs[key]
	if !ok {
		return "", errKeyNotFound
	}
	return v, nil
}

func (m *memBackend) Del(_ context.Context, keys ...string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range keys {
		delete(m.hashes, k)
		delete(m.sets, k)
		delete(m.strs, k)
		delete(m.ttl, k)
	}
	return nil
}

func (m *memBackend) Scan(_ context.Context, cursor uint64, match string, _ int64) ([]string, uint64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	if cursor != 0 {
		return nil, 0, nil
	}
	prefix := strings.TrimSuffix(match, "*")
	keys := make([]string, 0, len(m.hashes)+len(m.sets)+len(m.strs))
	seen := map[string]bool{}
	for _, set := range []map[string]bool{
		keysOf(m.hashes), keysOfSets(m.sets), keysOfStrs(m.strs),
	} {
		for k := range set {
			if seen[k] || !strings.HasPrefix(k, prefix) {
				continue
			}
			seen[k] = true
			keys = append(keys, k)
		}
	}
	return keys, 0, nil
}

func keysOf(m map[string]map[string]string) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func keysOfSets(m map[string]map[string]struct{}) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func keysOfStrs(m map[string]string) map[string]bool {
	out := make(map[string]bool, len(m))
	for k := range m {
		out[k] = true
	}
	return out
}

func (m *memBackend) Expire(_ context.Context, key string, ttl time.Duration) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.ttl[key] = ttl
	return nil
}

func (m *memBackend) Ping(context.Context) error { return nil }
func (m *memBackend) Close() error               { return nil }

func (m *memBackend) keys() []string {
	m.mu.Lock()
	defer m.mu.Unlock()
	seen := map[string]bool{}
	for _, set := range []map[string]bool{keysOf(m.hashes), keysOfSets(m.sets), keysOfStrs(m.strs)} {
		for k := range set {
			seen[k] = true
		}
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	return out
}

func (m *memBackend) ttlOf(key string) time.Duration {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.ttl[key]
}

// TestRedisCache_NamespaceIsolatesTenants 负控（P1-4）：
// 两个租户共用同一 Redis 时，同名 FQN 不得互相覆盖。
func TestRedisCache_NamespaceIsolatesTenants(t *testing.T) {
	ctx := context.Background()
	backend := newMemBackend()
	a := newRedisCache(backend, "tenant-a")
	b := newRedisCache(backend, "tenant-b")

	if err := a.PutClass(ctx, &parser.ClassIR{Name: "Svc", FullName: "pkg.Svc"}); err != nil {
		t.Fatalf("put class: %v", err)
	}
	if err := a.PutClassPath(ctx, "pkg.Svc", "/a/svc.go"); err != nil {
		t.Fatalf("put class path: %v", err)
	}

	if got, err := b.GetClass(ctx, "pkg.Svc"); err != nil || got != nil {
		t.Fatalf("tenant-b must not see tenant-a class: got=%+v err=%v", got, err)
	}
	if path, ok := b.ClassPath(ctx, "pkg.Svc"); ok {
		t.Fatalf("tenant-b must not see tenant-a path: %s", path)
	}

	got, err := a.GetClass(ctx, "pkg.Svc")
	if err != nil || got == nil || got.FullName != "pkg.Svc" {
		t.Fatalf("tenant-a should read own class: got=%+v err=%v", got, err)
	}
	if got, _ := b.GetClass(ctx, "pkg.Svc"); got != nil {
		t.Fatalf("namespace prefix missing, tenant-b read %+v", got)
	}
}

// TestRedisCache_SetsTTL 负控（P1-4）：
// 所有缓存键都必须带 TTL，避免删除/重建后残留键永久存在。
func TestRedisCache_SetsTTL(t *testing.T) {
	ctx := context.Background()
	backend := newMemBackend()
	c := newRedisCache(backend, "tenant-a")

	if err := c.PutClass(ctx, &parser.ClassIR{Name: "Svc", FullName: "pkg.Svc"}); err != nil {
		t.Fatalf("put class: %v", err)
	}
	if err := c.PutClassPath(ctx, "pkg.Svc", "/repo/svc.go"); err != nil {
		t.Fatalf("put class path: %v", err)
	}
	if err := c.PutMethod(ctx, &parser.MethodIR{Name: "Run", ClassFQN: "pkg.Svc"}); err != nil {
		t.Fatalf("put method: %v", err)
	}
	if err := c.PutMethodPath(ctx, "pkg.Svc.Run", "/repo/svc.go"); err != nil {
		t.Fatalf("put method path: %v", err)
	}
	if err := c.PutCall(ctx, &parser.CallIR{CallerFQN: "pkg.A", CalleeFQN: "pkg.B"}); err != nil {
		t.Fatalf("put call: %v", err)
	}
	if err := c.PutFileClasses(ctx, "/repo/svc.go", []string{"pkg.Svc"}); err != nil {
		t.Fatalf("put file classes: %v", err)
	}

	keys := backend.keys()
	if len(keys) == 0 {
		t.Fatal("no keys written")
	}
	for _, k := range keys {
		if backend.ttlOf(k) <= 0 {
			t.Errorf("key %q has no TTL", k)
		}
	}
}

// TestRedisCache_FlushScopedToNamespace 负控（P1-4）：
// Flush 只能清理本命名空间的键，不得 SCAN "*" 清空整个库。
func TestRedisCache_FlushScopedToNamespace(t *testing.T) {
	ctx := context.Background()
	backend := newMemBackend()
	c := newRedisCache(backend, "tenant-a")

	if err := backend.Set(ctx, "unrelated:key", "keep-me", 0); err != nil {
		t.Fatalf("seed unrelated key: %v", err)
	}
	if err := c.PutClass(ctx, &parser.ClassIR{Name: "Svc", FullName: "pkg.Svc"}); err != nil {
		t.Fatalf("put class: %v", err)
	}

	if err := c.Flush(ctx); err != nil {
		t.Fatalf("flush: %v", err)
	}
	keys := backend.keys()
	if len(keys) != 1 || keys[0] != "unrelated:key" {
		t.Fatalf("flush must only remove own namespace keys, remaining: %v", keys)
	}
}
