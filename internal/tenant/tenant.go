// Package tenant 实现 CodeSchema 的单实例多租户能力。
//
// 设计：单个 serve/mcp 进程持有若干个隔离的单租户运行实例（每个租户一份独立
// store + Service + 检索索引），按租户 ID 路由请求。存储隔离通过「每租户独立
// store」实现，不修改 internal/store.Store 接口；多租户仅是「路由层 + 多份单租户
// 实例」的组合，因此既有单项目行为（无 tenants 配置时）完全向后兼容。
package tenant

import (
	"context"
	"fmt"
	"log"
	"path/filepath"
	"sync"

	"github.com/idcu/codeschema/internal/config"
	rt "github.com/idcu/codeschema/internal/runtime"
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
)

// OpenStoreFunc 打开（并按需叠加缓存层）一个存储后端的工厂函数。
// 由 cmd 层注入，使 tenant 包无需依赖 build-tagged 的 pg/redis 存储分发逻辑，
// 也就不会破坏 internal/store 的循环依赖隔离约束。
type OpenStoreFunc func(ctx context.Context, cfg *config.Config, openTarget string) (store.Store, error)

// Tenant 单个项目（仓库）的运行期实例。
type Tenant struct {
	ID        string
	Name      string
	Cfg       *config.Config
	Store     store.Store
	Runtime   *rt.Runtime
	stopWatch func()
}

// Info 租户元信息（供 list_projects / /projects 返回）。
type Info struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Root string `json:"root"`
}

// Manager 多租户管理器：持有若干个隔离的单租户运行实例，按 ID 路由。
type Manager struct {
	mu        sync.RWMutex
	tenants   map[string]*Tenant
	order     []string
	defaultID string
	openStore OpenStoreFunc
}

// NewManager 从全局配置构建管理器。
//
//   - 若 cfg.Tenants 为空：构造单个隐式租户 "default"（沿用全局配置），
//     完全向后兼容单项目模式。
//   - 否则：为每个租户解析独立 Config、打开独立 store、装配运行期组件；
//     配置了 auto_scan 的租户在启动时全量扫描，配置了 watch 的租户后台增量监听。
func NewManager(ctx context.Context, base *config.Config, openStore OpenStoreFunc) (*Manager, error) {
	m := &Manager{
		tenants:   map[string]*Tenant{},
		openStore: openStore,
		defaultID: "default",
	}
	if len(base.Tenants) == 0 {
		t, err := m.buildTenant(ctx, "default", base.Project.Name, base, false, false)
		if err != nil {
			return nil, err
		}
		m.tenants["default"] = t
		m.order = []string{"default"}
		return m, nil
	}
	for _, tc := range base.Tenants {
		tcfg := tc.ToConfig(base)
		if tcfg.Storage.DSN == "" {
			tcfg.Storage.DSN = "./data/" + tc.ID
		}
		// 检索/向量索引目录需与 store 隔离，否则多租户会共享同一份索引
		// （后写入者覆盖前者）。仅当租户未显式配置时，按 store 目录派生隔离子目录。
		// 注意：需用租户原始配置 tc.Storage.Search 判断是否显式设置，因为 base
		// 的 DefaultConfig 已预填 ./data/fts 等默认值，merged 永远不会为空。
		deriveIndexDirs(&tcfg.Storage, tcfg.Storage.DSN,
			tc.Storage.Search.FTSDir, tc.Storage.Search.VectorDir, tc.Storage.Search.IDFDir)
		t, err := m.buildTenant(ctx, tc.ID, tc.Name, tcfg, tc.AutoScan, tc.Watch)
		if err != nil {
			return nil, fmt.Errorf("tenant %q: %w", tc.ID, err)
		}
		m.tenants[tc.ID] = t
		m.order = append(m.order, tc.ID)
	}
	if len(m.order) > 0 {
		m.defaultID = m.order[0]
	}
	return m, nil
}

func (m *Manager) buildTenant(ctx context.Context, id, name string, cfg *config.Config, autoScan, watch bool) (*Tenant, error) {
	st, err := m.openStore(ctx, cfg, cfg.Storage.DSN)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	if autoScan && cfg.Project.Root != "" {
		log.Printf("tenant %q: auto-scan %s", id, cfg.Project.Root)
		if err := rt.ScanRepository(ctx, st, cfg, cfg.Project.Root); err != nil {
			log.Printf("tenant %q: auto-scan failed: %v", id, err)
		}
	}
	run, err := rt.BuildRuntime(ctx, st, cfg)
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("build runtime: %w", err)
	}
	t := &Tenant{ID: id, Name: name, Cfg: cfg, Store: st, Runtime: run}
	if watch && cfg.Project.Root != "" {
		stop, err := rt.StartWatchBackground(ctx, st, cfg, cfg.Project.Root)
		if err != nil {
			log.Printf("tenant %q: watch failed (ignored): %v", id, err)
		} else {
			t.stopWatch = stop
		}
	}
	return t, nil
}

// deriveIndexDirs 为显式多租户派生隔离的检索/向量索引目录。
// explicitFTS/Vec/IDF 为租户原始配置中是否显式设置了对应目录；显式设置优先，
// 未设置则按 store 目录派生隔离子目录，保证多租户各自持有独立索引
// （避免共享索引被后写入者覆盖）。仅对目录型后端（file/sqlite/空）生效；
// 其余后端（如 pg）保持原样由用户显式配置。
func deriveIndexDirs(s *config.StorageConfig, dsn, explicitFTS, explicitVec, explicitIDF string) {
	switch s.Driver {
	case "file", "sqlite", "":
		if explicitFTS == "" {
			s.Search.FTSDir = filepath.Join(dsn, "fts")
		}
		if explicitVec == "" {
			s.Search.VectorDir = filepath.Join(dsn, "vector")
		}
		if explicitIDF == "" {
			s.Search.IDFDir = filepath.Join(dsn, "idf")
		}
	}
}

// Service 返回指定租户的运行期 Service；id 为空时返回默认租户。
func (m *Manager) Service(ctx context.Context, id string) (*service.Service, error) {
	if id == "" {
		id = m.DefaultID()
	}
	m.mu.RLock()
	t, ok := m.tenants[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("tenant %q not found", id)
	}
	return t.Runtime.Svc, nil
}

// Runtime 返回指定租户的运行期组件（含向量存储/检索器，供可视化等使用）；
// id 为空时返回默认租户。
func (m *Manager) Runtime(id string) (*rt.Runtime, error) {
	if id == "" {
		id = m.DefaultID()
	}
	m.mu.RLock()
	t, ok := m.tenants[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("tenant %q not found", id)
	}
	return t.Runtime, nil
}

// Config 返回指定租户的完整配置（供可视化等需要 VectorDim/VectorDir 的场景）；
// id 为空时返回默认租户。
func (m *Manager) Config(id string) (*config.Config, error) {
	if id == "" {
		id = m.DefaultID()
	}
	m.mu.RLock()
	t, ok := m.tenants[id]
	m.mu.RUnlock()
	if !ok {
		return nil, fmt.Errorf("tenant %q not found", id)
	}
	return t.Cfg, nil
}

// DefaultID 返回默认租户 ID（多租户时为注册表中的第一个，单项目模式为 "default"）。
func (m *Manager) DefaultID() string {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.defaultID
}

// List 返回所有租户的元信息（按注册顺序）。
func (m *Manager) List() []Info {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]Info, 0, len(m.order))
	for _, id := range m.order {
		if t, ok := m.tenants[id]; ok {
			out = append(out, Info{ID: t.ID, Name: t.Name, Root: t.Cfg.Project.Root})
		}
	}
	return out
}

// Close 释放所有租户资源（停止后台监听并关闭 store）。
func (m *Manager) Close() {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, t := range m.tenants {
		if t.stopWatch != nil {
			t.stopWatch()
		}
		if t.Store != nil {
			_ = t.Store.Close()
		}
	}
	m.tenants = map[string]*Tenant{}
}
