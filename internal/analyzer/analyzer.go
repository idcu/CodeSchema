package analyzer

import (
	"context"
	"fmt"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"sync"
	"sync/atomic"

	log "gitee.com/idcu-go/log"
	"gitee.com/idcu-go/metrics"
	trace "gitee.com/idcu-go/trace"
	"github.com/idcu/codeschema/internal/ai"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// init 注册分析器模块的监控指标。
func init() {
	metrics.RegisterCounter("analyzer_build_total", "Total analyzer build operations", "operation")
	metrics.RegisterCounter("analyzer_cache_hits_total", "Analyzer cache hits", "cache")
	metrics.RegisterGauge("analyzer_files_total", "Total files indexed by analyzer")
	metrics.RegisterGauge("analyzer_nodes_total", "Total graph nodes", "graph_type")
	metrics.RegisterCounter("analyzer_errors_total", "Total analyzer errors", "operation")
}

// Analyzer 是代码图分析器，负责构建跨文件的代码关系图。
//
// 从 Store 中读取已解析的 IR 数据，构建以下关系图：
//   - 调用图（CallGraph）：方法间的调用关系
//   - 类层次（ClassHierarchy）：继承/实现关系
//   - 反向引用索引（ReverseIndex）：文件被哪些文件引用
//   - 文件依赖图（FileGraph）：文件间的依赖关系
type Analyzer struct {
	store          store.Store
	resolver       *CompositeResolver // 多语言 import 解析器
	goResolver     *GoResolver        // Go 模块路径解析器
	javaResolver   *JavaResolver      // Java 包路径解析器
	gradleResolver *GradleResolver    // Gradle 多模块路径解析器
	logger         *log.Logger
	enhancer       *ai.Enhancer // AI 增强层（可选，TagAll 时叠加 AI 标签/文档补全）

	// 调用图按索引代次缓存（结构性问题 1）：
	//   - callGraphGen 在重索引 / 增量更新后递增，旧代次条目立即不可见；
	//   - callGraphCache 原子发布"已完整构建"的图，读者永远看不到半成品；
	//   - callGraphBuildMu 串行化建图，并发请求共享同一次全量读盘。
	callGraphGen     atomic.Uint64
	callGraphCache   atomic.Pointer[callGraphEntry]
	callGraphBuildMu sync.Mutex
}

// callGraphEntry 一代索引对应的不可变调用图快照。
type callGraphEntry struct {
	gen   uint64
	graph *CallGraph
}

// NewAnalyzer 创建分析器实例。
//
// 初始化多语言 import 解析器，默认包含：
//   - GoResolver（未设置模块路径时始终回退）
//   - JavaResolver（使用默认源根目录，标准库过滤）
//   - GradleResolver（多模块 : 路径解析）
//   - heuristicResolver（最终的启发式回退）
func NewAnalyzer(st store.Store) *Analyzer {
	goR := NewGoResolver("")
	javaR := NewJavaResolver(nil)
	gradleR := NewGradleResolver(nil, nil)
	composite := NewCompositeResolver(goR, javaR, gradleR, &heuristicResolver{})
	return &Analyzer{
		store:          st,
		resolver:       composite,
		goResolver:     goR,
		javaResolver:   javaR,
		gradleResolver: gradleR,
		logger:         log.WithModule("analyzer"),
	}
}

// SetEnhancer 注入 AI 增强层（可选）。
//
// 注入后 TagAll 会在规则标签之上叠加 AI 标签/文档补全，受 Budget 硬限管控，
// LLM 失败或预算超限时跳过增强、不影响主流程（索引与规则标签始终可用）。
func (a *Analyzer) SetEnhancer(e *ai.Enhancer) *Analyzer {
	a.enhancer = e
	return a
}

// SetModulePath 设置 Go 模块路径，用于精确 import 解析。
// 例如：go.mod 中的 module 声明 "codeschema"。
func (a *Analyzer) SetModulePath(mp string) {
	a.goResolver.modulePath = mp
}

// SetJavaSourceRoots 设置 Java 源根目录，用于精确 import 解析。
// 默认值：["src/main/java", "src/main/kotlin", "src/test/java", "src/test/kotlin"]。
func (a *Analyzer) SetJavaSourceRoots(roots []string) {
	if len(roots) > 0 {
		a.javaResolver.sourceRoots = roots
	}
}

// SetJavaStdlibPrefixes 设置 Java 标准库/框架前缀列表。
//
// 覆盖默认的 23 个前缀。传入 nil 或空切片表示不过滤任何 import。
func (a *Analyzer) SetJavaStdlibPrefixes(prefixes []string) {
	a.javaResolver.SetStdlibPrefixes(prefixes)
}

// SetGradleModuleNames 设置 Gradle 模块名白名单。
//
// 当设置了白名单后，只有白名单中的模块会被解析。
// 例如：a.SetGradleModuleNames([]string{"app", "core", "lib"})
// 传入 nil 表示不限制模块名。
func (a *Analyzer) SetGradleModuleNames(names []string) {
	a.gradleResolver.moduleNames = names
}

// TagAll 对所有已索引的实体执行标签自动推导并存储。
//
// 使用 ai.Tagger 的规则引擎，基于类名、方法名、目录路径、
// 文档注释和文件语言推导六类标签（layer/biz/tech/risk/test/lang）。
// 标签通过 Store 接口持久化。
//
// 若已注入 AI 增强层（SetEnhancer），在规则标签之上叠加 AI 标签补全
// （EnhanceTag）与文档补全（EnhanceDoc，仅当原 Doc 为空时），受 Budget 硬限管控；
// LLM 失败 / 预算超限时跳过增强并记录日志，不影响主流程。
func (a *Analyzer) TagAll(ctx context.Context) error {
	span := trace.Start("tag_all")
	defer span.End()

	a.logger.Info("starting tag derivation")

	tagger := ai.NewTagger(a.store)
	if err := tagger.DeriveAllTags(ctx); err != nil {
		metrics.IncCounter("analyzer_errors_total", "tag_all")
		a.logger.Error("tag derivation failed", "error", err)
		return err
	}

	// AI 增强层（可选）：预算超限/LLM 失败时优雅跳过
	if a.enhancer != nil {
		a.enhancer.ResetScanBudget()
		a.enhanceTagsWithAI(ctx, tagger)
	}

	a.logger.Info("tag derivation completed")
	return nil
}

// docUpdater 可选接口：支持文档注释更新的存储实现。
//
// 采用 Go 可选接口模式（不在 Store 主接口扩展），FileStore 等已实现的存储可写回
// AI 补全的文档；未实现（如 sqlite/pg 当前版本）时优雅跳过 Doc 补全（标签仍可写回）。
type docUpdater interface {
	UpdateClassDoc(ctx context.Context, classID int64, doc string) error
	UpdateMethodDoc(ctx context.Context, methodID int64, doc string) error
}

// aiWorkers 并发 LLM 调用的 worker 数（CPU 数，上限封顶防过载）。
// 由增强并发化逻辑内部按实体总量取 min，避免启动过多空转 worker。
const aiWorkersCap = 4

// enhanceJob 描述一个待 AI 增强的实体（类或方法）及其写回目标。
type enhanceJob struct {
	isMethod bool
	classID  int64
	methodID int64
	ent      ai.IRable
	needDoc  bool // 原 Doc 为空且 store 支持 Doc 写回
}

// enhanceTagsWithAI 在规则标签之上叠加 AI 标签与文档补全（失败隔离）。
//
// 并发化（P21 生产化）：把串行逐实体调用 LLM 改为 worker pool 并发，
// 显著缩短大仓扫描耗时。预算按原子计数（Budget）硬限，并发不会超发；
// 失败（网络 / 限流 / 解析）隔离，不影响主流程。store 读写并发安全
// （FileStore 等以 sync.RWMutex 保护）。
func (a *Analyzer) enhanceTagsWithAI(ctx context.Context, _ *ai.Tagger) {
	files, err := a.store.GetAllFiles(ctx)
	if err != nil {
		a.logger.Warn("AI enhance: get all files failed", "error", err)
		return
	}

	// 可选：Doc 补全写回能力（FileStore 支持，sqlite/pg 未实现时跳过）
	du, _ := a.store.(docUpdater)

	// 先收集全部待增强实体（不阻塞 LLM 阶段）。
	jobs := make([]enhanceJob, 0, 256)
	for _, f := range files {
		classes, err := a.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			continue
		}
		for _, cls := range classes {
			jobs = append(jobs, enhanceJob{
				classID: cls.ID,
				ent:     ai.NewClassEntity(cls),
				needDoc: cls.Doc == "" && du != nil,
			})
			methods, err := a.store.GetMethodsByClassID(ctx, cls.ID)
			if err != nil {
				continue
			}
			for _, m := range methods {
				jobs = append(jobs, enhanceJob{
					isMethod: true,
					classID:  cls.ID,
					methodID: m.ID,
					ent:      ai.NewMethodEntity(m),
					needDoc:  m.Doc == "" && du != nil,
				})
			}
		}
	}

	if len(jobs) == 0 {
		return
	}

	// worker 数：按实体量收敛，避免小实体集启动过多空转 goroutine。
	workers := runtime.NumCPU()
	if workers > aiWorkersCap {
		workers = aiWorkersCap
	}
	if workers > len(jobs) {
		workers = len(jobs)
	}
	if workers < 1 {
		workers = 1
	}

	// 统计计数（并发写，用原子 + 互斥量保护）。
	var aiTagged, aiDocd, skipped atomic.Int64
	var jobCh = make(chan enhanceJob, len(jobs))
	for _, j := range jobs {
		jobCh <- j
	}
	close(jobCh)

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for job := range jobCh {
				a.enhanceOne(ctx, job, du, &aiTagged, &aiDocd, &skipped)
			}
		}()
	}
	wg.Wait()

	a.logger.Info("AI enhancement completed",
		"ai_tagged", aiTagged.Load(), "ai_doc_completed", aiDocd.Load(),
		"skipped_budget", skipped.Load(), "workers", workers, "entities", len(jobs))
}

// enhanceOne 处理单个实体的 AI 标签 / 文档补全（失败隔离）。
// budget 由 Enhancer 内部原子 consume 门禁，并发下不会超发。
func (a *Analyzer) enhanceOne(ctx context.Context, job enhanceJob, du docUpdater, aiTagged, aiDocd, skipped *atomic.Int64) {
	// 标签补全：合并已有规则标签后写回。
	if a.enhancer.BudgetRemaining() > 0 {
		if tags, err := a.enhancer.EnhanceTag(ctx, job.ent); err == nil && len(tags) > 0 {
			if job.isMethod {
				existing, _ := a.store.GetTagsByMethodID(ctx, job.methodID)
				if merged := mergeUnique(existing, tags); len(merged) > 0 {
					if err := a.store.UpsertMethodTags(ctx, job.methodID, merged); err != nil {
						a.logger.Warn("upsert method tags failed", "method", job.ent.QualifiedName(), "error", err)
					}
					aiTagged.Add(1)
				}
			} else {
				existing, _ := a.store.GetTagsByClassID(ctx, job.classID)
				if merged := mergeUnique(existing, tags); len(merged) > 0 {
					if err := a.store.UpsertTags(ctx, job.classID, merged); err != nil {
						a.logger.Warn("upsert class tags failed", "class", job.ent.QualifiedName(), "error", err)
					}
					aiTagged.Add(1)
				}
			}
		}
	} else {
		skipped.Add(1)
	}

	// 文档补全：仅当原 Doc 为空且 store 支持写回。
	if job.needDoc && a.enhancer.BudgetRemaining() > 0 {
		if doc, err := a.enhancer.EnhanceDoc(ctx, job.ent); err == nil && doc != "" {
			if job.isMethod {
				if err := du.UpdateMethodDoc(ctx, job.methodID, doc); err != nil {
					a.logger.Warn("update method doc failed", "method", job.ent.QualifiedName(), "error", err)
				}
			} else {
				if err := du.UpdateClassDoc(ctx, job.classID, doc); err != nil {
					a.logger.Warn("update class doc failed", "class", job.ent.QualifiedName(), "error", err)
				}
			}
			aiDocd.Add(1)
		}
	}
}

// mergeUnique 合并两组标签并去重（保持顺序：先已有、后新增）。
func mergeUnique(existing, added []string) []string {
	seen := make(map[string]bool, len(existing)+len(added))
	merged := make([]string, 0, len(existing)+len(added))
	for _, t := range existing {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		merged = append(merged, t)
	}
	for _, t := range added {
		if t == "" || seen[t] {
			continue
		}
		seen[t] = true
		merged = append(merged, t)
	}
	return merged
}

// isCallRelation 判断 CallIR.CallType 是否代表真实调用。
//
// 适配器会把 imports/references/implements/extends 等非调用关系如实标注
// （P1-7），这些边只属于文件依赖/类层次，不应进入调用图，否则 /impact 与
// /affected 会出现假影响面。空值按 direct 兼容历史记录。
func isCallRelation(callType string) bool {
	switch strings.ToLower(callType) {
	case "", "direct", "interface", "dynamic":
		return true
	default:
		return false
	}
}

// BuildAll 构建所有代码图（单次遍历）。
//
// 返回所有四种图结构。如果某个图构建失败，返回错误。
func (a *Analyzer) BuildAll(ctx context.Context) (*CallGraph, *ClassHierarchy, *ReverseIndex, *FileGraph, error) {
	span := trace.Start("build_all")
	defer span.End()

	operations := []string{"callgraph", "classhierarchy", "reverseindex", "filegraph"}
	for _, op := range operations {
		metrics.IncCounter("analyzer_build_total", op)
	}

	callGraph := NewCallGraph()
	classHierarchy := NewClassHierarchy()
	reverseIndex := NewReverseIndex()
	fileGraph := NewFileGraph()

	// 1. 读取所有文件
	files, err := a.store.GetAllFiles(ctx)
	if err != nil {
		metrics.IncCounter("analyzer_errors_total", "get_all_files")
		return nil, nil, nil, nil, fmt.Errorf("get all files: %w", err)
	}

	metrics.SetGauge("analyzer_files_total", float64(len(files)))
	a.logger.Info("building all graphs", "files", len(files))

	// 2. 预构建 import 索引（仅依赖文件路径，无需遍历）
	importIdx := buildImportIndex(files)

	// 3. 单次遍历：构建所有图结构
	for _, f := range files {
		// 构建文件图节点
		fgNode := fileGraph.AddNode(f.AbsolutePath)
		fgNode.FileID = f.ID
		fgNode.Language = f.Language

		// 读取类记录
		classes, err := a.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			metrics.IncCounter("analyzer_errors_total", "get_classes")
			return nil, nil, nil, nil, fmt.Errorf("get classes for file %d: %w", f.ID, err)
		}
		fgNode.ClassCount = len(classes)

		// 构建类层次
		for _, cls := range classes {
			a.buildClassHierarchyNode(classHierarchy, cls, f.ID)
		}

		// 读取调用记录
		calls, err := a.store.GetCallsByFileID(ctx, f.ID)
		if err != nil {
			metrics.IncCounter("analyzer_errors_total", "get_calls")
			return nil, nil, nil, nil, fmt.Errorf("get calls for file %d: %w", f.ID, err)
		}

		// 构建调用图
		for _, call := range calls {
			if call.CallerFQN != "" && call.CalleeFQN != "" && isCallRelation(call.CallType) {
				callGraph.AddEdge(call.CallerFQN, call.CalleeFQN)
			}
		}

		// 统计方法数
		fgNode.MethodCount = a.countMethodsByFileID(ctx, f.ID)

		// 基于 imports 构建反向引用索引和文件依赖边
		for _, imp := range f.Imports {
			imp = strings.TrimSpace(imp)
			if imp == "" {
				continue
			}
			reverseIndex.AddImport(f.AbsolutePath, imp)
			if targets := a.resolveImport(imp, importIdx); len(targets) > 0 {
				for _, target := range targets {
					reverseIndex.AddReference(target, f.AbsolutePath)
					fileGraph.AddEdge(f.AbsolutePath, target)
				}
			}
		}
	}

	// 更新图节点数指标
	metrics.SetGauge("analyzer_nodes_total", float64(callGraph.NodeCount()), "callgraph")
	metrics.SetGauge("analyzer_nodes_total", float64(classHierarchy.NodeCount()), "classhierarchy")
	metrics.SetGauge("analyzer_nodes_total", float64(fileGraph.NodeCount()), "filegraph")

	a.logger.Info("build all completed",
		"files", len(files),
		"callgraph_nodes", callGraph.NodeCount(),
		"classhierarchy_nodes", classHierarchy.NodeCount(),
		"filegraph_nodes", fileGraph.NodeCount(),
	)

	return callGraph, classHierarchy, reverseIndex, fileGraph, nil
}

// callGraphCtxKey 请求级调用图缓存键：同一请求链路上避免重复建图。
type callGraphCtxKey struct{}

// WithCallGraph 把已构建的调用图挂到 ctx，后续同请求的 BuildCallGraph 直接复用。
func WithCallGraph(ctx context.Context, cg *CallGraph) context.Context {
	return context.WithValue(ctx, callGraphCtxKey{}, cg)
}

// CallGraphFromContext 取出请求级调用图；不存在时返回 nil。
func CallGraphFromContext(ctx context.Context) *CallGraph {
	cg, _ := ctx.Value(callGraphCtxKey{}).(*CallGraph)
	return cg
}

// InvalidateCallGraph 让调用图缓存失效（重索引 / 增量更新后调用）。
//
// 先递增代次再清空指针：递增让并发读者立刻判定缓存条目过期，
// 清空则立即释放旧图内存（读者持有时由 GC 兜底）。
func (a *Analyzer) InvalidateCallGraph() {
	a.callGraphGen.Add(1)
	a.callGraphCache.Store(nil)
}

// cachedCallGraph 返回当前代次已完整发布的调用图；无缓存或代次过期时返回 nil。
//
// 先读条目再读代次：若失效发生在两次读取之间，代次已递增，判定为不命中；
// 若失效发生在读取代次之后，本次查询视为"失效前开始"，返回旧图仍是线性化一致的。
func (a *Analyzer) cachedCallGraph() *CallGraph {
	e := a.callGraphCache.Load()
	if e == nil || e.gen != a.callGraphGen.Load() {
		return nil
	}
	return e.graph
}

// BuildCallGraph 构建调用图。
//
// 从 Store 中读取所有调用记录，构建方法间的调用关系图。
// 每个调用记录生成一条边（caller -> callee）。
//
// 结果按索引代次缓存（见 InvalidateCallGraph）：同代次内重复请求直接复用，
// 并发请求由 callGraphBuildMu 串行化后共享同一次构建结果，不会观察到半成品。
func (a *Analyzer) BuildCallGraph(ctx context.Context) (*CallGraph, error) {
	if cg := CallGraphFromContext(ctx); cg != nil {
		return cg, nil
	}
	if cg := a.cachedCallGraph(); cg != nil {
		metrics.IncCounter("analyzer_cache_hits_total", "callgraph")
		return cg, nil
	}

	a.callGraphBuildMu.Lock()
	defer a.callGraphBuildMu.Unlock()
	// 双检：等待建图锁期间，前一个请求可能已经发布了当前代次的完整图。
	if cg := a.cachedCallGraph(); cg != nil {
		metrics.IncCounter("analyzer_cache_hits_total", "callgraph")
		return cg, nil
	}

	// 建图期间若发生失效，本次结果可能混合两代数据：重试一次以拿到稳定快照；
	// 持续写入下不再无限重试，直接返回本次结果且不写缓存（宁可不缓存，不发布脏图）。
	for attempt := 0; ; attempt++ {
		gen := a.callGraphGen.Load()
		cg, err := a.buildCallGraph(ctx)
		if err != nil {
			return nil, err
		}
		if a.callGraphGen.Load() == gen {
			a.callGraphCache.Store(&callGraphEntry{gen: gen, graph: cg})
			return cg, nil
		}
		if attempt >= 1 {
			return cg, nil
		}
	}
}

// buildCallGraph 执行一次全量建图（不做缓存判断），供 BuildCallGraph 调用。
func (a *Analyzer) buildCallGraph(ctx context.Context) (*CallGraph, error) {
	span := trace.Start("build_call_graph")
	defer span.End()

	metrics.IncCounter("analyzer_build_total", "callgraph")
	a.logger.Debug("building call graph")

	cg := NewCallGraph()

	files, err := a.store.GetAllFiles(ctx)
	if err != nil {
		metrics.IncCounter("analyzer_errors_total", "get_all_files")
		return nil, fmt.Errorf("get all files: %w", err)
	}

	for _, f := range files {
		// 长扫描期间允许调用方取消：每文件检查一次，避免取消后仍在全量遍历。
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		calls, err := a.store.GetCallsByFileID(ctx, f.ID)
		if err != nil {
			metrics.IncCounter("analyzer_errors_total", "get_calls")
			return nil, fmt.Errorf("get calls for file %d: %w", f.ID, err)
		}
		for _, call := range calls {
			if call.CallerFQN != "" && call.CalleeFQN != "" && isCallRelation(call.CallType) {
				source, confidence := parser.NormalizeCallProvenance(call.Source, call.Confidence)
				cg.AddEdgeWithMeta(call.CallerFQN, call.CalleeFQN, source, confidence)
			}
		}
	}

	if err := ctx.Err(); err != nil {
		return nil, err
	}

	metrics.SetGauge("analyzer_nodes_total", float64(cg.NodeCount()), "callgraph")
	a.logger.Debug("call graph built", "nodes", cg.NodeCount(), "edges", cg.EdgeCount())

	return cg, nil
}

// BuildClassHierarchy 构建类层次结构。
//
// 从 Store 中读取所有类记录，构建继承/实现关系树。
// 通过 ClassIR.ParentFQNs 字段建立父子关系。
func (a *Analyzer) BuildClassHierarchy(ctx context.Context) (*ClassHierarchy, error) {
	span := trace.Start("build_class_hierarchy")
	defer span.End()

	metrics.IncCounter("analyzer_build_total", "classhierarchy")
	a.logger.Debug("building class hierarchy")

	ch := NewClassHierarchy()

	files, err := a.store.GetAllFiles(ctx)
	if err != nil {
		metrics.IncCounter("analyzer_errors_total", "get_all_files")
		return nil, fmt.Errorf("get all files: %w", err)
	}

	for _, f := range files {
		classes, err := a.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			metrics.IncCounter("analyzer_errors_total", "get_classes")
			return nil, fmt.Errorf("get classes for file %d: %w", f.ID, err)
		}
		for _, cls := range classes {
			a.buildClassHierarchyNode(ch, cls, f.ID)
		}
	}

	metrics.SetGauge("analyzer_nodes_total", float64(ch.NodeCount()), "classhierarchy")
	a.logger.Debug("class hierarchy built", "nodes", ch.NodeCount())

	return ch, nil
}

// buildClassHierarchyNode 将一条类记录添加到类层次结构中。
func (a *Analyzer) buildClassHierarchyNode(ch *ClassHierarchy, cls store.ClassRecord, fileID int64) {
	node := ch.AddNode(cls.FullName)
	node.Type = cls.Type
	node.FileID = fileID

	// 通过 ParentFQNs 建立父子关系
	for _, parent := range cls.ParentFQNs {
		if parent != "" {
			ch.AddParent(cls.FullName, parent)
		}
	}
}

// BuildReverseIndex 构建反向引用索引。
//
// 通过分析每个文件存储的 Imports 元数据，建立文件间的引用关系。
// 对于每个文件的 import 路径，尝试匹配到 Store 中已知的文件路径。
func (a *Analyzer) BuildReverseIndex(ctx context.Context) (*ReverseIndex, error) {
	span := trace.Start("build_reverse_index")
	defer span.End()

	metrics.IncCounter("analyzer_build_total", "reverseindex")
	a.logger.Debug("building reverse index")

	ri := NewReverseIndex()

	files, err := a.store.GetAllFiles(ctx)
	if err != nil {
		metrics.IncCounter("analyzer_errors_total", "get_all_files")
		return nil, fmt.Errorf("get all files: %w", err)
	}

	// 1. 构建 import 路径到文件路径的查找映射
	importIndex := buildImportIndex(files)

	// 2. 遍历每个文件，解析其 imports
	for _, f := range files {
		if len(f.Imports) == 0 {
			continue
		}
		for _, imp := range f.Imports {
			imp = strings.TrimSpace(imp)
			if imp == "" {
				continue
			}
			ri.AddImport(f.AbsolutePath, imp)
			if targets := a.resolveImport(imp, importIndex); len(targets) > 0 {
				for _, target := range targets {
					ri.AddReference(target, f.AbsolutePath)
				}
			}
		}
	}

	a.logger.Debug("reverse index built", "imports", len(ri.Imports), "references", len(ri.References))

	return ri, nil
}

// buildImportIndex 构建 import 路径到文件路径的快速查找索引。
//
// 对于每个文件，提取其路径中的关键路径段作为索引键。
// 例如："/project/internal/store/store.go" 会生成
// "store"、"internal/store"、"github.com/idcu/codeschema/internal/store" 等索引键。
func buildImportIndex(files []*store.FileRecord) map[string][]string {
	idx := make(map[string][]string)
	for _, f := range files {
		path := f.AbsolutePath
		normalized := strings.ReplaceAll(path, "\\", "/")
		parts := strings.Split(normalized, "/")
		for i := len(parts) - 1; i >= 0; i-- {
			suffix := strings.Join(parts[i:], "/")
			dirSuffix := suffix
			for _, ext := range []string{".go", ".java", ".ts", ".py", ".rs", ".cpp", ".h"} {
				dirSuffix = strings.TrimSuffix(dirSuffix, ext)
			}
			if dirSuffix != "" && !strings.HasSuffix(dirSuffix, "/") {
				idx[dirSuffix] = append(idx[dirSuffix], f.AbsolutePath)
			}
		}
		base := filepath.Base(path)
		ext := filepath.Ext(base)
		if ext != "" {
			pkgName := strings.TrimSuffix(base, ext)
			idx[pkgName] = append(idx[pkgName], f.AbsolutePath)
		}
	}
	return idx
}

// resolveImport 尝试将 import 路径解析为 Store 中已知的文件路径。
//
// 使用多语言解析器链（CompositeResolver）：
//   - GoResolver: 模块路径精确解析
//   - JavaResolver: Java FQCN/通配符/源根目录解析
//   - heuristicResolver: 启发式回退匹配
func (a *Analyzer) resolveImport(imp string, importIndex map[string][]string) []string {
	return a.resolver.Resolve(imp, importIndex)
}

// BuildFileGraph 构建文件依赖图。
//
// 从 Store 中读取所有文件记录，构建文件间的依赖关系图。
// 包含每个文件的类/方法数量信息。
func (a *Analyzer) BuildFileGraph(ctx context.Context) (*FileGraph, error) {
	span := trace.Start("build_file_graph")
	defer span.End()

	metrics.IncCounter("analyzer_build_total", "filegraph")
	a.logger.Debug("building file graph")

	fg := NewFileGraph()

	files, err := a.store.GetAllFiles(ctx)
	if err != nil {
		metrics.IncCounter("analyzer_errors_total", "get_all_files")
		return nil, fmt.Errorf("get all files: %w", err)
	}

	for _, f := range files {
		node := fg.AddNode(f.AbsolutePath)
		node.FileID = f.ID
		node.Language = f.Language

		classes, err := a.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			metrics.IncCounter("analyzer_errors_total", "get_classes")
			return nil, fmt.Errorf("get classes for file %d: %w", f.ID, err)
		}
		node.ClassCount = len(classes)
		node.MethodCount = a.countMethodsByFileID(ctx, f.ID)
	}

	metrics.SetGauge("analyzer_nodes_total", float64(fg.NodeCount()), "filegraph")
	a.logger.Debug("file graph built", "nodes", fg.NodeCount())

	return fg, nil
}

// countMethodsByFileID 统计指定文件中的方法总数。
func (a *Analyzer) countMethodsByFileID(ctx context.Context, fileID int64) int {
	classes, err := a.store.GetClassesByFileID(ctx, fileID)
	if err != nil {
		return 0
	}
	count := 0
	for _, cls := range classes {
		methods, err := a.store.GetMethodsByClassID(ctx, cls.ID)
		if err != nil {
			continue
		}
		count += len(methods)
	}
	return count
}

// FindImpactNodes 查找指定方法的影响面。
//
// depth 控制递归深度，0 表示不限制。
// 返回所有受影响的调用者和被调用者。
func (a *Analyzer) FindImpactNodes(ctx context.Context, methodFQN string, depth int) (callers, callees []string, err error) {
	span := trace.Start("find_impact_nodes", "method", methodFQN)
	defer span.End()

	a.logger.Debug("finding impact nodes", "method", methodFQN, "depth", depth)

	cg, err := a.BuildCallGraph(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("build call graph: %w", err)
	}

	callersN, calleesN, resolved, err := ImpactWithDepthInGraph(ctx, cg, methodFQN, depth)
	if err != nil {
		return nil, nil, fmt.Errorf("impact in graph: %w", err)
	}
	if resolved != "" {
		methodFQN = resolved
	}
	callers = make([]string, 0, len(callersN))
	for _, n := range callersN {
		callers = append(callers, n.Method)
	}
	callees = make([]string, 0, len(calleesN))
	for _, n := range calleesN {
		callees = append(callees, n.Method)
	}

	a.logger.Debug("impact analysis complete", "method", methodFQN, "callers", len(callers), "callees", len(callees))

	return callers, callees, nil
}

// normalizeImpactSymbol 生成查询符号的候选 FQN 列表，从完全限定到逐级剥离前缀段。
//
// 调用图节点 FQN 的命名空间可能因解析适配器而异：
//   - Go 默认正则路径产出「包限定全名」(config.Watcher.ReloadNow)
//   - 其它语言或旧数据可能存「裸名」(Watcher.ReloadNow / ReloadNow)
//
// 为最大化匹配概率，按从长到短逐级剥离最外层一段，精确匹配优先。
func normalizeImpactSymbol(methodFQN string) []string {
	candidates := []string{methodFQN}
	parts := strings.Split(methodFQN, ".")
	for len(parts) > 1 {
		parts = parts[1:] // 剥离最外层一段
		candidates = append(candidates, strings.Join(parts, "."))
	}
	return candidates
}

// ResolveImpactNode 暴露双向归一化定位能力，供 service 层 GetCallGraph 等
// 「先定位节点再展开」场景复用，避免逻辑在包外重复。
func (a *Analyzer) ResolveImpactNode(ctx context.Context, symbol string) (string, bool) {
	cg, err := a.BuildCallGraph(ctx)
	if err != nil {
		return "", false
	}
	return resolveImpactNode(cg, symbol)
}

// ResolveImpactSymbol 与 ResolveImpactNode 同义，但把建图错误显式暴露给调用方，
// 供「需要区分未命中与建图失败」的场景（如 GetAffected 的种子归一化）使用。
func (a *Analyzer) ResolveImpactSymbol(ctx context.Context, symbol string) (string, bool, error) {
	cg, err := a.BuildCallGraph(ctx)
	if err != nil {
		return "", false, fmt.Errorf("build call graph: %w", err)
	}
	resolved, ok := resolveImpactNode(cg, symbol)
	return resolved, ok, nil
}

// resolveImpactNode 在调用图中定位查询符号命中的节点 FQN，返回命中的 FQN。
//
// 双向归一化，覆盖调用图节点与查询符号命名空间不一致的所有情况：
//   - 去前缀：查询比节点更限定（config.Watcher.ReloadNow → 命中 Watcher.ReloadNow）
//   - 后缀：节点比查询更限定（查询 Watcher.ReloadNow → 命中 config.Watcher.ReloadNow）
//
// 先精确/去前缀（确定性高），后后缀（节点多包前缀时可能多命中，按 FQN
// 字典序取最小者，保证同一查询跨请求结果稳定；调用方可传更精确的 FQN 消歧）。
func resolveImpactNode(cg *CallGraph, fqn string) (string, bool) {
	// 1) 精确匹配 + 逐级去前缀
	for _, cand := range normalizeImpactSymbol(fqn) {
		if _, ok := cg.Nodes[cand]; ok {
			return cand, true
		}
	}
	// 2) 后缀匹配（节点比查询多包前缀）：config.Watcher.ReloadNow 命中查询 Watcher.ReloadNow / ReloadNow
	//    多命中时排序后取首个，避免 map 遍历顺序导致结果跳变。
	var matches []string
	for nodeFQN := range cg.Nodes {
		if strings.HasSuffix(nodeFQN, "."+fqn) {
			matches = append(matches, nodeFQN)
		}
	}
	if len(matches) == 0 {
		return "", false
	}
	sort.Strings(matches)
	return matches[0], true
}

// ImpactNode 影响面节点（带深度层级，供编排层直接消费）。
type ImpactNode struct {
	Method string `json:"method"`
	Depth  int    `json:"depth"`
}

// MaxImpactDepth 影响面 / 调用图的最大展开深度。
// 没有上限时大 depth 会对全图逐节点做全仓扫描，长尾延迟不可控。
const MaxImpactDepth = 10

// ClampImpactDepth 收口深度参数：<=0 视为直接关系（1），超过上限收敛到 MaxImpactDepth。
func ClampImpactDepth(depth int) int {
	if depth <= 0 {
		return 1
	}
	if depth > MaxImpactDepth {
		return MaxImpactDepth
	}
	return depth
}

// ImpactWithDepthInGraph 在已构建的调用图上执行双向 BFS，
// 返回带层级的 callers/callees 与归一化后的 FQN。
//
// 供同一请求内需要多次查询影响面的场景复用同一张图，避免重复 BuildCallGraph。
func ImpactWithDepthInGraph(ctx context.Context, cg *CallGraph, symbol string, depth int) (callers, callees []ImpactNode, resolved string, err error) {
	if cg == nil {
		return nil, nil, "", fmt.Errorf("nil call graph")
	}
	if depth <= 0 {
		depth = MaxImpactDepth
	}
	r, ok := resolveImpactNode(cg, symbol)
	if !ok {
		return nil, nil, "", nil
	}
	resolved = r
	callers, err = cg.GetCallersWithDepthContext(ctx, resolved, depth)
	if err != nil {
		return nil, nil, resolved, err
	}
	callees, err = cg.GetCalleesWithDepthContext(ctx, resolved, depth)
	if err != nil {
		return nil, nil, resolved, err
	}
	return callers, callees, resolved, nil
}

// FindImpactNodesWithDepth 查找指定方法的影响面，返回带深度的调用者与被调用者。
//
// 与 FindImpactNodes 的区别：每个节点附带其距目标方法的层级距离（深度 1 为直接调用者/被调用者），
// 供影响面 API 直接渲染，避免编排层二次 BFS。
func (a *Analyzer) FindImpactNodesWithDepth(ctx context.Context, methodFQN string, depth int) (callers, callees []ImpactNode, err error) {
	span := trace.Start("find_impact_nodes_with_depth", "method", methodFQN)
	defer span.End()

	a.logger.Debug("finding impact nodes with depth", "method", methodFQN, "depth", depth)

	cg, err := a.BuildCallGraph(ctx)
	if err != nil {
		return nil, nil, fmt.Errorf("build call graph: %w", err)
	}

	callers, callees, resolved, err := ImpactWithDepthInGraph(ctx, cg, methodFQN, depth)
	if err != nil {
		return nil, nil, fmt.Errorf("impact in graph: %w", err)
	}
	if resolved != "" {
		methodFQN = resolved
	}

	a.logger.Debug("impact analysis (depth) complete", "method", methodFQN,
		"callers", len(callers), "callees", len(callees))

	return callers, callees, nil
}

// ShortestPath 查找两个方法之间的最短调用路径（BFS）。
//
// 返回路径上的方法 FQN 列表（含起点和终点），
// 如果不存在路径则返回 nil。
func (a *Analyzer) ShortestPath(ctx context.Context, from, to string) ([]string, error) {
	span := trace.Start("shortest_path", "from", from, "to", to)
	defer span.End()

	a.logger.Debug("finding shortest path", "from", from, "to", to)

	cg, err := a.BuildCallGraph(ctx)
	if err != nil {
		return nil, fmt.Errorf("build call graph: %w", err)
	}

	// 源/目标 FQN 归一化（命名空间可能因解析适配器而异）。
	fromResolved, fromOK := resolveImpactNode(cg, from)
	if !fromOK {
		a.logger.Debug("shortest path: source not found", "from", from)
		return nil, nil
	}
	toResolved, toOK := resolveImpactNode(cg, to)
	if !toOK {
		a.logger.Debug("shortest path: target not found", "to", to)
		return nil, nil
	}

	// BFS 查找最短路径
	visited := make(map[string]bool)
	prev := make(map[string]string)
	queue := []string{fromResolved}
	visited[fromResolved] = true

	for len(queue) > 0 {
		current := queue[0]
		queue = queue[1:]

		if current == toResolved {
			path := reconstructPath(prev, fromResolved, toResolved)
			a.logger.Debug("shortest path found", "from", from, "to", to, "path_length", len(path))
			return path, nil
		}

		node, ok := cg.Nodes[current]
		if !ok {
			continue
		}

		for _, callee := range node.Callees {
			if !visited[callee] {
				visited[callee] = true
				prev[callee] = current
				queue = append(queue, callee)
			}
		}
	}

	a.logger.Debug("shortest path: no path found", "from", from, "to", to)
	return nil, nil // 无路径
}

// reconstructPath 从 prev 映射中重建路径。
func reconstructPath(prev map[string]string, from, to string) []string {
	path := make([]string, 0)
	for cur := to; cur != ""; cur = prev[cur] {
		path = append([]string{cur}, path...)
	}
	// 只有路径以 from 开头时才有效
	if len(path) > 0 && path[0] == from {
		return path
	}
	return nil
}

// hotItem 用于热点方法排序。
type hotItem struct {
	fqn      string
	nCallers int
}

// ModuleSummary 模块概要信息，用于文件图统计。
type ModuleSummary struct {
	TotalFiles    int            `json:"total_files"`
	TotalClasses  int            `json:"total_classes"`
	TotalMethods  int            `json:"total_methods"`
	TotalCalls    int            `json:"total_calls"`
	Languages     map[string]int `json:"languages"` // language -> count
	FileGraph     *FileGraph     `json:"file_graph"`
	OrphanMethods []string       `json:"orphan_methods"` // 无调用者的方法
	HotMethods    []string       `json:"hot_methods"`    // 调用者最多的方法
}

// Analyze 对仓库执行完整分析，返回模块概要。
func (a *Analyzer) Analyze(ctx context.Context) (*ModuleSummary, error) {
	span := trace.Start("analyze")
	defer span.End()

	a.logger.Info("starting full analysis")

	cg, ch, _, fg, err := a.BuildAll(ctx)
	if err != nil {
		metrics.IncCounter("analyzer_errors_total", "build_all")
		return nil, fmt.Errorf("build all graphs: %w", err)
	}

	summary := &ModuleSummary{
		TotalFiles:   fg.NodeCount(),
		TotalClasses: ch.NodeCount(),
		Languages:    make(map[string]int),
		FileGraph:    fg,
	}

	// 统计
	for _, node := range fg.Nodes {
		summary.TotalMethods += node.MethodCount
		summary.TotalCalls += len(node.Imports) // 近似

		if node.Language != "" {
			summary.Languages[node.Language]++
		}
	}

	// 找出热点方法（被调用最多的 Top 20）
	var hotList []hotItem
	for fqn, node := range cg.Nodes {
		if len(node.Callers) == 0 {
			summary.OrphanMethods = append(summary.OrphanMethods, fqn)
		}
		hotList = append(hotList, hotItem{fqn, len(node.Callers)})
	}

	// 排序简化：取前 20
	hotList = sortByCallers(hotList)
	limit := 20
	if len(hotList) < limit {
		limit = len(hotList)
	}
	for _, h := range hotList[:limit] {
		summary.HotMethods = append(summary.HotMethods, h.fqn)
	}

	a.logger.Info("analysis completed",
		"files", summary.TotalFiles,
		"classes", summary.TotalClasses,
		"methods", summary.TotalMethods,
		"calls", summary.TotalCalls,
		"languages", len(summary.Languages),
		"orphan_methods", len(summary.OrphanMethods),
		"hot_methods", len(summary.HotMethods),
	)

	return summary, nil
}

// sortByCallers 按调用者数量降序排序。
func sortByCallers(items []hotItem) []hotItem {
	// 简单选择排序
	for i := 0; i < len(items); i++ {
		maxIdx := i
		for j := i + 1; j < len(items); j++ {
			if items[j].nCallers > items[maxIdx].nCallers {
				maxIdx = j
			}
		}
		items[i], items[maxIdx] = items[maxIdx], items[i]
	}
	return items
}
