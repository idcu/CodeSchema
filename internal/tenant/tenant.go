// Package tenant 实现 CodeSchema 的单实例多租户能力。
//
// 设计：单个 serve/mcp 进程持有若干个隔离的单租户运行实例（每个租户一份独立
// store + Service + 检索索引），按租户 ID 路由请求。存储隔离通过「每租户独立
// store」实现，不修改 internal/store.Store 接口；多租户仅是「路由层 + 多份单租户
// 实例」的组合，因此既有单项目行为（无 tenants 配置时）完全向后兼容。
//
// 热重载：Manager.Apply 接收新配置，与当前租户列表做增量 diff —— 新增的租户
// 自动构建，移除的租户关闭释放，变更的租户替换实例。配合 config.ConfigWatcher
// 的 OnReload 回调即可实现配置文件变更时自动热更新，无需重启进程。
package tenant

import (
	"context"
	"errors"
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
	AutoScan  bool // 构建时是否执行启动全量扫描
	Watch     bool // 构建时是否启动后台增量监听
	stopWatch func()
}

// Info 租户元信息（供 list_projects / /projects 返回）。
type Info struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Root string `json:"root"`
}

// tenantTarget 描述一个待构建的租户目标（NewManager 与 Apply 共用的规格）。
type tenantTarget struct {
	id       string
	name     string
	cfg      *config.Config
	autoScan bool
	watch    bool
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
	for _, tgt := range resolveTargets(base) {
		t, err := m.buildTenant(ctx, tgt)
		if err != nil {
			return nil, fmt.Errorf("tenant %q: %w", tgt.id, err)
		}
		m.tenants[tgt.id] = t
		m.order = append(m.order, tgt.id)
	}
	if len(m.order) > 0 {
		m.defaultID = m.order[0]
	}
	return m, nil
}

// resolveTargets 从全局配置解析租户构建目标列表。
//
//   - 若 base.Tenants 为空：返回单个隐式 "default" 目标（沿用全局配置），
//     完全向后兼容单项目模式。
//   - 否则：为每个租户解析独立 Config 并派生隔离的检索/向量索引目录。
func resolveTargets(base *config.Config) []tenantTarget {
	if len(base.Tenants) == 0 {
		return []tenantTarget{{id: "default", name: base.Project.Name, cfg: base}}
	}
	out := make([]tenantTarget, 0, len(base.Tenants))
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
		out = append(out, tenantTarget{
			id:       tc.ID,
			name:     tc.Name,
			cfg:      tcfg,
			autoScan: tc.AutoScan,
			watch:    tc.Watch,
		})
	}
	return out
}

func (m *Manager) buildTenant(ctx context.Context, tgt tenantTarget) (*Tenant, error) {
	st, err := m.openStore(ctx, tgt.cfg, tgt.cfg.Storage.DSN)
	if err != nil {
		return nil, fmt.Errorf("open store: %w", err)
	}
	if tgt.autoScan && tgt.cfg.Project.Root != "" {
		log.Printf("tenant %q: auto-scan %s", tgt.id, tgt.cfg.Project.Root)
		if err := rt.ScanRepository(ctx, st, tgt.cfg, tgt.cfg.Project.Root); err != nil {
			log.Printf("tenant %q: auto-scan failed: %v", tgt.id, err)
		}
	}
	run, err := rt.BuildRuntime(ctx, st, tgt.cfg)
	if err != nil {
		_ = st.Close()
		return nil, fmt.Errorf("build runtime: %w", err)
	}
	t := &Tenant{
		ID: tgt.id, Name: tgt.name, Cfg: tgt.cfg,
		Store: st, Runtime: run, AutoScan: tgt.autoScan, Watch: tgt.watch,
	}
	if tgt.watch && tgt.cfg.Project.Root != "" {
		stop, err := rt.StartWatchBackground(ctx, st, tgt.cfg, tgt.cfg.Project.Root)
		if err != nil {
			log.Printf("tenant %q: watch failed (ignored): %v", tgt.id, err)
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

// Apply 依据新的全局配置对租户集合做增量热重载（配合 config.ConfigWatcher 使用，
// 配置变更时无需重启进程）。
//
//   - 新增租户 → 构建并加入路由表；
//   - 移除租户 → 停止后台监听并关闭 store，从路由表删除；
//   - 关键配置变化（DSN / Root / Name / autoScan / watch）→ 重建实例；
//   - 未变化的租户保持原实例，避免打断在途请求。
//
// 任一租户构建失败只影响该租户（保留旧实例继续服务），返回 errors.Join 聚合错误，
// 其余租户照常应用。
func (m *Manager) Apply(ctx context.Context, base *config.Config) error {
	targets := resolveTargets(base)
	wanted := make(map[string]tenantTarget, len(targets))
	for _, t := range targets {
		wanted[t.id] = t
	}

	// 锁外构建新实例（构建含 IO/扫描，避免持锁）。
	m.mu.RLock()
	cur := make(map[string]*Tenant, len(m.tenants))
	for id, t := range m.tenants {
		cur[id] = t
	}
	m.mu.RUnlock()

	var errs []error
	upsert := make(map[string]*Tenant) // 新增或重建后的新实例
	release := make([]string, 0)       // 待释放的旧实例 id（被替换或被移除）

	for _, tgt := range targets {
		old, ok := cur[tgt.id]
		if ok && !tenantDirty(old, tgt) {
			continue // 未变化，保持原实例
		}
		t, err := m.buildTenant(ctx, tgt)
		if err != nil {
			errs = append(errs, fmt.Errorf("tenant %q: %w", tgt.id, err))
			continue
		}
		upsert[tgt.id] = t
		if ok {
			release = append(release, tgt.id) // 替换旧实例
		}
	}
	for id := range cur {
		if _, ok := wanted[id]; !ok {
			release = append(release, id) // 已从配置移除
		}
	}

	// 提交：更新路由表（锁内，仅内存操作）。
	// 按 targets 顺序追加（而非遍历 upsert map），确保 order 稳定有序。
	m.mu.Lock()
	for _, tgt := range targets {
		t, ok := upsert[tgt.id]
		if !ok {
			continue
		}
		m.tenants[tgt.id] = t
		if !sliceContains(m.order, tgt.id) {
			m.order = append(m.order, tgt.id)
		}
	}
	for _, id := range release {
		if _, replaced := upsert[id]; !replaced {
			delete(m.tenants, id)
			m.order = sliceRemove(m.order, id)
		}
	}
	if len(m.order) > 0 {
		m.defaultID = m.order[0]
	}
	m.mu.Unlock()

	// 锁外释放旧实例资源。
	for _, id := range release {
		old, ok := cur[id]
		if !ok {
			continue
		}
		if old.stopWatch != nil {
			old.stopWatch()
		}
		if old.Store != nil {
			_ = old.Store.Close()
		}
	}
	return errors.Join(errs...)
}

// tenantDirty 判断租户关键配置是否发生变化，需要重建实例。
// 仅比较影响运行期行为的字段，避免日志级别等无关差异触发无谓重建。
func tenantDirty(old *Tenant, tgt tenantTarget) bool {
	return old.Name != tgt.name ||
		old.AutoScan != tgt.autoScan ||
		old.Watch != tgt.watch ||
		old.Cfg.Project.Name != tgt.cfg.Project.Name ||
		old.Cfg.Project.Root != tgt.cfg.Project.Root ||
		old.Cfg.Storage.DSN != tgt.cfg.Storage.DSN ||
		old.Cfg.Scanner.Workers != tgt.cfg.Scanner.Workers ||
		old.Cfg.Scanner.FileSizeLimitMB != tgt.cfg.Scanner.FileSizeLimitMB ||
		old.Cfg.Scanner.LineCountLimit != tgt.cfg.Scanner.LineCountLimit
}

func sliceContains(s []string, v string) bool {
	for _, x := range s {
		if x == v {
			return true
		}
	}
	return false
}

func sliceRemove(s []string, v string) []string {
	out := make([]string, 0, len(s))
	for _, x := range s {
		if x != v {
			out = append(out, x)
		}
	}
	return out
}
