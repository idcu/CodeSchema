package search

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	log "gitee.com/idcu-go/log"
	"github.com/idcu/codeschema/internal/fsperm"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/vector"
)

// IndexBuilder 自动索引构建器，从 Store 读取数据构建 FTS 和向量索引。
//
// 职责：
//   - BuildFromStore: 启动时全量构建索引
//   - IndexDocument: 扫描后增量更新索引
//   - 构建前自动 Observe 建立 IDF 词典（仅 LocalEmbedder）
//   - 支持异步索引队列，避免阻塞扫描流程
//   - 支持 IDF 自动持久化，避免重启后 IDF 统计丢失
type IndexBuilder struct {
	fts      FTSEngine
	indexer  *vector.Indexer
	embedder vector.Embedder

	// IDF 词典（仅 LocalEmbedder 使用，ONNX Embedder 不需要）
	idf *vector.LocalEmbedder

	// 异步索引队列（可选启用）
	queue   chan asyncJob
	wg      sync.WaitGroup
	async   bool
	mu      sync.Mutex
	started bool

	// 回调钩子，可用于错误通知
	onError func(id string, err error)

	// IDF 自动持久化
	autoSaveIDFPath string
	autoSaveStopCh  chan struct{}

	// docsMu/fileDocs 记录每个文件产出的检索文档 ID（绝对路径 → 文档 ID）。
	//
	// FileStore 每次重扫都会给类/方法分配新 ID；增量更新若只新增不摘除，
	// 旧符号的 FTS/向量文档会永久残留，/search 与 /viz 会命中已不存在的代码。
	docsMu   sync.Mutex
	fileDocs map[string][]string

	// 存储版本：索引是 Store 的派生视图，indexedVersion 记录已对齐到的变更版本。
	// 查询入口经 EnsureFresh 比对，版本落后即全量重建——写入方即使忘记调用
	// InvalidateDerivedCaches，也不会让 /search、/viz 永久陈旧。
	st             store.Store
	indexedVersion atomic.Uint64
	freshMu        sync.Mutex // 串行化全量/增量构建，避免跨代互相覆盖
}

type asyncJob struct {
	ctx  context.Context
	id   string
	text string
}

// NewIndexBuilder 创建索引构建器。
func NewIndexBuilder(fts FTSEngine, idx *vector.Indexer, emb vector.Embedder) *IndexBuilder {
	b := &IndexBuilder{
		fts:      fts,
		indexer:  idx,
		embedder: emb,
		fileDocs: make(map[string][]string),
	}
	// 如果 embedder 是 LocalEmbedder，保存引用用于 IDF 操作
	if le, ok := emb.(*vector.LocalEmbedder); ok {
		b.idf = le
	}
	return b
}

// BuildResult 构建结果统计。
type BuildResult struct {
	TotalDocs   int           `json:"total_docs"`
	IndexedDocs int           `json:"indexed_docs"`
	Errors      int           `json:"errors"`
	Duration    time.Duration `json:"duration"`
}

// BindStore 绑定索引服务的存储：EnsureFresh 据此判断索引是否落后于存储版本。
//
// 只应由运行时唯一装配点调用（见 runtime.newSearcherWithStore）；未绑定时
// EnsureFresh 是空操作（仅测试场景会构造无存储的构建器）。
func (b *IndexBuilder) BindStore(st store.Store) { b.st = st }

// versionOf 返回存储变更版本；nil 存储恒为 0。
func versionOf(st store.Store) uint64 {
	if st == nil {
		return 0
	}
	return st.ChangeVersion()
}

// EnsureFresh 保证检索索引与存储版本一致：版本落后时全量重建。
//
// 正确性不再依赖写入方调用失效钩子：BuildAndIndex 失败、markSkipped 清 IR、
// 删除/改名等任何已落库的变更都推进存储版本，查询入口据此自愈。
func (b *IndexBuilder) EnsureFresh(ctx context.Context) error {
	if b.st == nil {
		return nil
	}
	if b.indexedVersion.Load() == b.st.ChangeVersion() {
		return nil
	}
	b.freshMu.Lock()
	defer b.freshMu.Unlock()
	if b.indexedVersion.Load() == b.st.ChangeVersion() {
		return nil
	}
	if _, err := b.buildFromStore(ctx, b.st); err != nil {
		return fmt.Errorf("refresh index: %w", err)
	}
	return nil
}

// BuildFromStore 从 Store 读取所有数据，构建 FTS 和向量索引。
//
// 流程：
//  1. 读取所有文件记录
//  2. 对每个文件读取类和方法
//  3. 先 Observe 所有文档建立 IDF 词典
//  4. 再批量写入 FTS 和向量索引
func (b *IndexBuilder) BuildFromStore(ctx context.Context, st store.Store) (*BuildResult, error) {
	b.freshMu.Lock()
	defer b.freshMu.Unlock()
	return b.buildFromStore(ctx, st)
}

// buildFromStore 执行一次全量构建（调用方须持有 freshMu）。
func (b *IndexBuilder) buildFromStore(ctx context.Context, st store.Store) (*BuildResult, error) {
	// 版本在读取前捕获：构建期间发生的写入不会被本次快照覆盖，
	// 构建结束后版本仍落后，下一次查询会再次对齐（宁可多重建，不可陈旧）。
	ver := versionOf(st)
	start := time.Now()

	files, err := st.GetAllFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all files: %w", err)
	}

	result := &BuildResult{}

	// 第一阶段：收集所有文档 ID 和文本
	type doc struct {
		id   string
		text string
	}

	var docs []doc
	// nextFileDocs 记录本次构建每个文件产出的文档 ID，用于摘除上一代残留。
	nextFileDocs := make(map[string][]string, len(files))
	totalFiles := len(files)

	for fi, f := range files {
		// 进度：文件遍历阶段
		if (fi+1)%50 == 0 || fi == totalFiles-1 {
			fmt.Fprintf(os.Stderr, "\r  scanning files: %d/%d", fi+1, totalFiles)
		}

		classes, err := st.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			// 读失败 ≠ 文件已删除：沿用上一代追踪，避免 swap 把仍存在的
			// 检索文档当成「本代消失」而摘除（检索文档被删、Store 仍在）。
			result.Errors++
			nextFileDocs[f.AbsolutePath] = b.fileDocsFor(f.AbsolutePath)
			continue
		}
		if len(classes) == 0 {
			// 文件没有类，用文件路径作为文档
			fileDocID := "file:" + f.AbsolutePath
			docs = append(docs, doc{id: fileDocID, text: f.AbsolutePath})
			nextFileDocs[f.AbsolutePath] = []string{fileDocID}
			result.TotalDocs++
			continue
		}

		// 单文件先收集到局部切片：任一读失败则整文件沿用上一代追踪，
		// 不把半代文档混进本代索引（否则旧文档会被误判为过期）。
		var fileDocs []doc
		fileIDs := make([]string, 0, len(classes))
		readFailed := false
		for _, c := range classes {
			classID := "class:" + formatClassID(c.ID)
			classText := buildClassIndexText(c, f.AbsolutePath)
			fileDocs = append(fileDocs, doc{id: classID, text: classText})
			fileIDs = append(fileIDs, classID)

			// 索引方法
			methods, err := st.GetMethodsByClassID(ctx, c.ID)
			if err != nil {
				result.Errors++
				readFailed = true
				break
			}
			for _, m := range methods {
				methodID := "method:" + formatClassID(m.ID)
				methodText := buildMethodIndexText(c, m, f.AbsolutePath)
				fileDocs = append(fileDocs, doc{id: methodID, text: methodText})
				fileIDs = append(fileIDs, methodID)
			}
		}
		if readFailed {
			nextFileDocs[f.AbsolutePath] = b.fileDocsFor(f.AbsolutePath)
			continue
		}
		docs = append(docs, fileDocs...)
		result.TotalDocs += len(fileDocs)
		nextFileDocs[f.AbsolutePath] = fileIDs
	}
	fmt.Fprintln(os.Stderr) // 换行结束文件扫描进度

	// 全量重建也要摘除上一代独有的检索文档（例如重扫后 ID 变化或文件已被删除）。
	for _, id := range b.swapFileDocs(nextFileDocs) {
		if err := b.RemoveDocument(ctx, id); err != nil {
			log.WithModule("search.index_builder").Warn("remove stale doc", "id", id, "error", err.Error())
		}
	}

	if len(docs) == 0 {
		result.Duration = time.Since(start)
		b.indexedVersion.Store(ver)
		return result, nil
	}

	fmt.Fprintf(os.Stderr, "  collected %d docs for indexing\n", len(docs))

	// 第二阶段：Observe 建立 IDF 词典（仅 LocalEmbedder 需要，ONNX 跳过）
	if b.idf != nil {
		if b.idf.HasIDF() {
			fmt.Fprintln(os.Stderr, "  skipping IDF build (loaded from persistence)")
		} else {
			totalDocs := len(docs)
			for i, d := range docs {
				b.idf.Observe(d.text)
				if (i+1)%100 == 0 || i == totalDocs-1 {
					pct := float64(i+1) / float64(totalDocs) * 100
					fmt.Fprintf(os.Stderr, "\r  building IDF: %d/%d docs (%.0f%%)", i+1, totalDocs, pct)
				}
			}
			fmt.Fprintln(os.Stderr) // 换行结束 IDF 进度
		}
	} else {
		fmt.Fprintln(os.Stderr, "  skipping IDF build (ONNX embedder, no IDF needed)")
	}

	// 第三阶段：批量写入 FTS 和向量索引
	ids := make([]string, 0, len(docs))
	texts := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.id)
		texts = append(texts, d.text)
	}

	// 批量写入 FTS
	fmt.Fprint(os.Stderr, "  writing FTS index...")
	if err := b.fts.BatchIndex(ctx, ids, texts); err != nil {
		fmt.Fprintln(os.Stderr, " FAIL")
		return nil, fmt.Errorf("batch index fts: %w", err)
	}
	fmt.Fprintln(os.Stderr, " OK")

	// 批量写入向量索引
	fmt.Fprint(os.Stderr, "  writing vector index...")
	embeddableDocs := make([]vector.TextEmbeddable, 0, len(docs))
	for _, d := range docs {
		embeddableDocs = append(embeddableDocs, &docEmbeddable{id: d.id, text: d.text})
	}
	if err := b.indexer.BatchBuild(ctx, embeddableDocs); err != nil {
		fmt.Fprintln(os.Stderr, " FAIL")
		return nil, fmt.Errorf("batch build vector: %w", err)
	}
	fmt.Fprintln(os.Stderr, " OK")

	// 保存文档原文（/viz 可视化展示；底层不支持的后端静默跳过）。
	// 构建期必须批量提交：逐篇 SetContent 会让持久化后端每 10 篇全量重写索引。
	if err := b.indexer.BatchSetContents(ctx, ids, texts); err != nil {
		// 原文保存失败不影响索引主流程（仅可视化缺失）
		log.WithModule("search.index_builder").Warn("batch set doc content failed", "docs", len(ids), "error", err.Error())
	}

	result.IndexedDocs = len(docs)
	result.Duration = time.Since(start)
	b.indexedVersion.Store(ver)
	fmt.Fprintf(os.Stderr, "  index build complete: %d docs in %s\n", result.IndexedDocs, result.Duration.Round(time.Millisecond))
	return result, nil
}

// IndexDocument 为单个文档增量构建索引。
//
// 先 Observe 更新 IDF 词典（仅 LocalEmbedder），再写入 FTS 和向量索引。
func (b *IndexBuilder) IndexDocument(ctx context.Context, id, text string) error {
	if b.idf != nil {
		b.idf.Observe(text)
	}

	if err := b.fts.Index(ctx, id, text); err != nil {
		return fmt.Errorf("index fts %s: %w", id, err)
	}

	if err := b.indexer.BuildIndex(ctx, &docEmbeddable{id: id, text: text}); err != nil {
		return fmt.Errorf("index vector %s: %w", id, err)
	}
	// 保存文档原文（/viz 可视化；不支持的后端静默跳过）
	if err := b.indexer.SetDocContent(ctx, id, text); err != nil {
		log.WithModule("search.index_builder").Warn("set doc content failed", "id", id, "error", err.Error())
	}

	return nil
}

// BuildAndIndex 从 Store 读取单个文件的类和方法，构建索引文档并入库。
//
// 用于扫描后的增量更新，避免重复全量扫描。
// filePath 用于生成文档 ID 前缀。
func (b *IndexBuilder) BuildAndIndex(ctx context.Context, st store.Store, filePath string) error {
	b.freshMu.Lock()
	defer b.freshMu.Unlock()
	ver := versionOf(st)
	file, err := st.GetFileByPath(ctx, filePath)
	if err != nil || file == nil {
		return fmt.Errorf("get file %s: %w", filePath, err)
	}

	classes, err := st.GetClassesByFileID(ctx, file.ID)
	if err != nil {
		return fmt.Errorf("get classes for file %d: %w", file.ID, err)
	}

	// 先读取（不摘除）该文件上一代检索文档：重扫后类/方法会拿到新 ID，
	// 只新增不删除会让旧符号永久残留在 FTS/向量索引里。追踪表只在
	// 全部删除+重建成功后才覆盖，失败时旧值仍在，下一次可继续摘除。
	oldIDs := b.fileDocsFor(file.AbsolutePath)

	if len(classes) == 0 {
		// 没有类，索引文件路径
		for _, id := range oldIDs {
			if err := b.RemoveDocument(ctx, id); err != nil {
				return err
			}
		}
		fileDocID := "file:" + file.AbsolutePath
		if err := b.IndexDocument(ctx, fileDocID, file.AbsolutePath); err != nil {
			return err
		}
		b.setFileDocs(file.AbsolutePath, []string{fileDocID})
		b.indexedVersion.Store(ver)
		return nil
	}

	type pendingDoc struct {
		id   string
		text string
	}
	var pending []pendingDoc
	for _, c := range classes {
		pending = append(pending, pendingDoc{
			id:   "class:" + formatClassID(c.ID),
			text: buildClassIndexText(c, file.AbsolutePath),
		})

		methods, err := st.GetMethodsByClassID(ctx, c.ID)
		if err != nil {
			return fmt.Errorf("get methods for class %d: %w", c.ID, err)
		}
		for _, m := range methods {
			pending = append(pending, pendingDoc{
				id:   "method:" + formatClassID(m.ID),
				text: buildMethodIndexText(c, m, file.AbsolutePath),
			})
		}
	}

	for _, id := range oldIDs {
		if err := b.RemoveDocument(ctx, id); err != nil {
			return err
		}
	}

	fileIDs := make([]string, 0, len(pending))
	for _, d := range pending {
		if err := b.IndexDocument(ctx, d.id, d.text); err != nil {
			return err
		}
		fileIDs = append(fileIDs, d.id)
	}

	b.setFileDocs(file.AbsolutePath, fileIDs)
	b.indexedVersion.Store(ver)
	return nil
}

// StartAsync 启动异步索引队列，在后台上索引文档。
//
// queueSize 为队列缓冲区大小，传 0 使用默认值 64。
// numWorkers 为 worker 数量，传 0 使用默认值 2。
// 启动后，EnqueueIndex 将文档放入队列由后台 worker 异步处理。
func (b *IndexBuilder) StartAsync(ctx context.Context, queueSize, numWorkers int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.started {
		return
	}
	if queueSize <= 0 {
		queueSize = 64
	}
	if numWorkers <= 0 {
		numWorkers = 2
	}
	b.queue = make(chan asyncJob, queueSize)
	b.started = true
	b.async = true

	b.wg.Add(numWorkers)
	for i := 0; i < numWorkers; i++ {
		go b.asyncWorker(ctx)
	}
}

// StopAsync 停止异步索引队列，等待队列中所有任务完成。
func (b *IndexBuilder) StopAsync() {
	b.mu.Lock()
	if !b.started {
		b.mu.Unlock()
		return
	}
	b.started = false
	b.mu.Unlock()

	close(b.queue)
	b.wg.Wait()
}

// EnqueueIndex 将文档异步入队索引。
//
// 如果异步队列未启动（StartAsync 未调用），则同步执行。
func (b *IndexBuilder) EnqueueIndex(ctx context.Context, id, text string) {
	if !b.isAsync() {
		// 降级为同步
		if err := b.IndexDocument(ctx, id, text); err != nil {
			if b.onError != nil {
				b.onError(id, err)
			}
		}
		return
	}

	select {
	case b.queue <- asyncJob{ctx: ctx, id: id, text: text}:
	case <-ctx.Done():
	}
}

// SetOnError 设置异步索引错误回调。
func (b *IndexBuilder) SetOnError(fn func(id string, err error)) {
	b.onError = fn
}

// isAsync 返回是否已启动异步模式。
func (b *IndexBuilder) isAsync() bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.async && b.started
}

// asyncWorker 后台异步索引 worker。
func (b *IndexBuilder) asyncWorker(ctx context.Context) {
	defer b.wg.Done()
	logger := log.WithModule("search.index_builder")
	for job := range b.queue {
		if err := b.IndexDocument(job.ctx, job.id, job.text); err != nil {
			if b.onError != nil {
				b.onError(job.id, err)
			}
			logger.Error("async index failed", "id", job.id, "error", err)
		}
	}
}

// RemoveDocument 从 FTS 和向量索引中删除指定文档。
//
// 用于文件被删除时的索引同步清理。
func (b *IndexBuilder) RemoveDocument(ctx context.Context, docID string) error {
	if err := b.fts.Remove(ctx, docID); err != nil {
		return fmt.Errorf("remove fts %s: %w", docID, err)
	}
	if err := b.indexer.RemoveDocument(ctx, docID); err != nil {
		return fmt.Errorf("remove vector %s: %w", docID, err)
	}
	return nil
}

// SaveIDF 持久化 IDF 词典到文件（仅 LocalEmbedder）。
func (b *IndexBuilder) SaveIDF(path string) error {
	if b.idf != nil {
		return b.idf.SaveIDF(path)
	}
	return nil
}

// AutoSaveIDF 启动 IDF 自动持久化，定期将 IDF 词典保存到文件。
//
// interval 为保存间隔，不小于 10 秒。当 interval <= 0 时使用默认值 60 秒。
// 首次保存会在启动时立即执行一次。
// 返回的 stop 函数用于停止自动保存，可多次调用，幂等。
func (b *IndexBuilder) AutoSaveIDF(path string, interval time.Duration) func() {
	b.autoSaveIDFPath = path
	if interval <= 0 {
		interval = 60 * time.Second
	} else if interval < 10*time.Second {
		interval = 10 * time.Second
	}

	ch := make(chan struct{})
	b.autoSaveStopCh = ch

	// 保存一次（立即执行）
	dir := filepath.Dir(path)
	if err := fsperm.MkdirAll(dir); err == nil && b.idf != nil {
		b.idf.SaveIDF(path) // 忽略错误，后续定时保存会重试
	}

	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		logger := log.WithModule("search.index_builder")
		for {
			select {
			case <-ticker.C:
				dir := filepath.Dir(path)
				if b.idf != nil {
					if err := fsperm.MkdirAll(dir); err != nil {
						logger.Warn("auto save IDF: mkdir", "dir", dir, "error", err)
						continue
					}
					if err := b.idf.SaveIDF(path); err != nil {
						logger.Warn("auto save IDF failed", "path", path, "error", err)
					} else {
						logger.Debug("auto saved IDF", "path", path)
					}
				}
			case <-ch:
				return
			}
		}
	}()

	var once sync.Once
	return func() {
		once.Do(func() {
			close(ch)
			b.autoSaveStopCh = nil
		})
	}
}

// StopAutoSaveIDF 停止 IDF 自动持久化。幂等操作，可多次调用。
func (b *IndexBuilder) StopAutoSaveIDF() {
	if b.autoSaveStopCh != nil {
		close(b.autoSaveStopCh)
		b.autoSaveStopCh = nil
	}
}

// BuildAndRemove 从 Store 读取单个文件信息，删除该文件的检索文档与主存储记录。
//
// 用于文件被删除后的增量同步。检索文档（FTS/向量）与主存储（文件行、类、方法、
// 调用边、标签）必须同时清理：只删检索文档会让 /context、/impact、/tests
// 继续把已删除的代码当成存在（§8.8 缺口）。
func (b *IndexBuilder) BuildAndRemove(ctx context.Context, st store.Store, filePath string) error {
	b.freshMu.Lock()
	defer b.freshMu.Unlock()
	ver := versionOf(st)
	// 先按追踪表摘除该文件上一代检索文档。重扫/改名后类与方法 ID 已变化，
	// 仅按当前 Store 行删除会漏掉旧 ID 的文档。追踪表只在全部成功后清理。
	tracked := b.fileDocsFor(filePath)
	file, err := st.GetFileByPath(ctx, filePath)
	if err == nil && file != nil && file.AbsolutePath != filePath {
		tracked = append(tracked, b.fileDocsFor(file.AbsolutePath)...)
	}

	if err != nil || file == nil {
		for _, id := range tracked {
			if err := b.RemoveDocument(ctx, id); err != nil {
				return err
			}
		}
		// 文件不存在，删除文件本身（如果索引过）
		if err := b.RemoveDocument(ctx, "file:"+filePath); err != nil {
			return err
		}
		b.clearFileDocs(filePath)
		b.indexedVersion.Store(ver)
		return nil
	}

	for _, id := range tracked {
		if err := b.RemoveDocument(ctx, id); err != nil {
			return err
		}
	}

	// 删除所有类和方法
	classes, err := st.GetClassesByFileID(ctx, file.ID)
	if err != nil {
		return fmt.Errorf("get classes for file %d: %w", file.ID, err)
	}

	for _, c := range classes {
		classID := "class:" + formatClassID(c.ID)
		if err := b.RemoveDocument(ctx, classID); err != nil {
			return err
		}

		methods, err := st.GetMethodsByClassID(ctx, c.ID)
		if err != nil {
			return fmt.Errorf("get methods for class %d: %w", c.ID, err)
		}
		for _, m := range methods {
			methodID := "method:" + formatClassID(m.ID)
			if err := b.RemoveDocument(ctx, methodID); err != nil {
				return err
			}
		}
	}

	// 删除文件文档本身（如果存在）
	if err := b.RemoveDocument(ctx, "file:"+file.AbsolutePath); err != nil {
		return err
	}

	// 清理主存储：文件行、类、方法、调用边与标签。
	// 放在检索文档之后：RemoveDocument 依赖类/方法 ID，必须先读后删。
	if err := st.DeleteFile(ctx, file.ID); err != nil {
		return fmt.Errorf("delete file %d (%s): %w", file.ID, filePath, err)
	}
	b.clearFileDocs(filePath)
	if file.AbsolutePath != filePath {
		b.clearFileDocs(file.AbsolutePath)
	}
	b.indexedVersion.Store(ver)
	return nil
}

// swapFileDocs 用 next 替换文件→文档 ID 追踪表，返回上一代有、next 中已消失的文档 ID。
//
// 用于全量重建：重扫后类/方法 ID 会变化，必须摘除旧 ID 的检索文档，
// 否则 /search 与 /viz 会继续命中已不存在的符号。
func (b *IndexBuilder) swapFileDocs(next map[string][]string) []string {
	b.docsMu.Lock()
	defer b.docsMu.Unlock()

	prev := b.fileDocs
	b.fileDocs = next
	if len(prev) == 0 {
		return nil
	}

	keep := make(map[string]struct{}, len(next)*2)
	for _, ids := range next {
		for _, id := range ids {
			keep[id] = struct{}{}
		}
	}

	var stale []string
	for _, ids := range prev {
		for _, id := range ids {
			if _, ok := keep[id]; !ok {
				stale = append(stale, id)
			}
		}
	}
	return stale
}

// fileDocsFor 返回指定文件当前追踪的文档 ID（只读，不摘除）。
//
// 增量构建失败时追踪必须保持旧值，下一次构建才能继续摘除旧文档；
// 先前 takeFileDocs 的「先摘再删」在任一失败分支都会永久丢失追踪项。
func (b *IndexBuilder) fileDocsFor(filePath string) []string {
	b.docsMu.Lock()
	defer b.docsMu.Unlock()
	return append([]string(nil), b.fileDocs[filePath]...)
}

// clearFileDocs 删除指定文件的追踪记录（仅在文档已全部摘除后调用）。
func (b *IndexBuilder) clearFileDocs(filePath string) {
	b.docsMu.Lock()
	defer b.docsMu.Unlock()
	delete(b.fileDocs, filePath)
}

// setFileDocs 覆盖指定文件的文档 ID 追踪记录。
func (b *IndexBuilder) setFileDocs(filePath string, ids []string) {
	b.docsMu.Lock()
	defer b.docsMu.Unlock()
	if b.fileDocs == nil {
		b.fileDocs = make(map[string][]string)
	}
	b.fileDocs[filePath] = ids
}

// buildClassIndexText 构建类的索引文本。
func buildClassIndexText(c store.ClassRecord, filePath string) string {
	var b strings.Builder
	b.WriteString(c.FullName)
	b.WriteString(" ")
	b.WriteString(filePath)
	if c.Doc != "" {
		b.WriteString("\n")
		b.WriteString(c.Doc)
	}
	if c.Source != "" {
		b.WriteString("\n")
		b.WriteString(c.Source)
	}
	return b.String()
}

// buildMethodIndexText 构建方法的索引文本。
func buildMethodIndexText(c store.ClassRecord, m store.MethodRecord, filePath string) string {
	var b strings.Builder
	b.WriteString(c.FullName)
	b.WriteString(".")
	b.WriteString(m.Name)
	b.WriteString(" ")
	b.WriteString(filePath)
	if m.Signature != "" {
		b.WriteString(" ")
		b.WriteString(m.Signature)
	}
	if m.ReturnType != "" {
		b.WriteString(" ")
		b.WriteString(m.ReturnType)
	}
	if m.Doc != "" {
		b.WriteString("\n")
		b.WriteString(m.Doc)
	}
	return b.String()
}

// formatClassID 将 int64 转换为字符串 ID。
func formatClassID(id int64) string {
	return fmt.Sprintf("%d", id)
}

// docEmbeddable 实现 vector.TextEmbeddable 接口。
type docEmbeddable struct {
	id   string
	text string
}

func (d *docEmbeddable) ID() string   { return d.id }
func (d *docEmbeddable) Text() string { return d.text }
