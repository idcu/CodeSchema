package search

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"gitee.com/idcu-go/pathsafe"
	log "gitee.com/idcu-go/log"
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

// BuildFromStore 从 Store 读取所有数据，构建 FTS 和向量索引。
//
// 流程：
//  1. 读取所有文件记录
//  2. 对每个文件读取类和方法
//  3. 先 Observe 所有文档建立 IDF 词典
//  4. 再批量写入 FTS 和向量索引
func (b *IndexBuilder) BuildFromStore(ctx context.Context, st store.Store) (*BuildResult, error) {
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
	totalFiles := len(files)

	for fi, f := range files {
		// 进度：文件遍历阶段
		if (fi+1)%50 == 0 || fi == totalFiles-1 {
			fmt.Printf("\r  scanning files: %d/%d", fi+1, totalFiles)
		}

		classes, err := st.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			result.Errors++
			continue
		}
		if len(classes) == 0 {
			// 文件没有类，用文件路径作为文档
			docs = append(docs, doc{
				id:   "file:" + f.AbsolutePath,
				text: f.AbsolutePath,
			})
			result.TotalDocs++
			continue
		}

		for _, c := range classes {
			result.TotalDocs++
			classID := "class:" + formatClassID(c.ID)
			classText := buildClassIndexText(c, f.AbsolutePath)
			docs = append(docs, doc{id: classID, text: classText})

			// 索引方法
			methods, err := st.GetMethodsByClassID(ctx, c.ID)
			if err != nil {
				result.Errors++
				continue
			}
			for _, m := range methods {
				result.TotalDocs++
				methodID := "method:" + formatClassID(m.ID)
				methodText := buildMethodIndexText(c, m, f.AbsolutePath)
				docs = append(docs, doc{id: methodID, text: methodText})
			}
		}
	}
	fmt.Println() // 换行结束文件扫描进度

	if len(docs) == 0 {
		result.Duration = time.Since(start)
		return result, nil
	}

	fmt.Printf("  collected %d docs for indexing\n", len(docs))

	// 第二阶段：Observe 建立 IDF 词典（仅 LocalEmbedder 需要，ONNX 跳过）
	if b.idf != nil {
		if b.idf.HasIDF() {
			fmt.Println("  skipping IDF build (loaded from persistence)")
		} else {
			totalDocs := len(docs)
			for i, d := range docs {
				b.idf.Observe(d.text)
				if (i+1)%100 == 0 || i == totalDocs-1 {
					pct := float64(i+1) / float64(totalDocs) * 100
					fmt.Printf("\r  building IDF: %d/%d docs (%.0f%%)", i+1, totalDocs, pct)
				}
			}
			fmt.Println() // 换行结束 IDF 进度
		}
	} else {
		fmt.Println("  skipping IDF build (ONNX embedder, no IDF needed)")
	}

	// 第三阶段：批量写入 FTS 和向量索引
	ids := make([]string, 0, len(docs))
	texts := make([]string, 0, len(docs))
	for _, d := range docs {
		ids = append(ids, d.id)
		texts = append(texts, d.text)
	}

	// 批量写入 FTS
	fmt.Print("  writing FTS index...")
	if err := b.fts.BatchIndex(ctx, ids, texts); err != nil {
		fmt.Println(" FAIL")
		return nil, fmt.Errorf("batch index fts: %w", err)
	}
	fmt.Println(" OK")

	// 批量写入向量索引
	fmt.Print("  writing vector index...")
	embeddableDocs := make([]vector.TextEmbeddable, 0, len(docs))
	for _, d := range docs {
		embeddableDocs = append(embeddableDocs, &docEmbeddable{id: d.id, text: d.text})
	}
	if err := b.indexer.BatchBuild(ctx, embeddableDocs); err != nil {
		fmt.Println(" FAIL")
		return nil, fmt.Errorf("batch build vector: %w", err)
	}
	fmt.Println(" OK")

	// 保存文档原文（/viz 可视化展示；底层不支持的后端静默跳过）
	for _, d := range docs {
		if err := b.indexer.SetDocContent(ctx, d.id, d.text); err != nil {
			// 原文保存失败不影响索引主流程（仅可视化缺失）
			log.WithModule("search.index_builder").Warn("set doc content failed", "id", d.id, "error", err.Error())
		}
	}

	result.IndexedDocs = len(docs)
	result.Duration = time.Since(start)
	fmt.Printf("  index build complete: %d docs in %s\n", result.IndexedDocs, result.Duration.Round(time.Millisecond))
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
	file, err := st.GetFileByPath(ctx, filePath)
	if err != nil || file == nil {
		return fmt.Errorf("get file %s: %w", filePath, err)
	}

	classes, err := st.GetClassesByFileID(ctx, file.ID)
	if err != nil {
		return fmt.Errorf("get classes for file %d: %w", file.ID, err)
	}

	if len(classes) == 0 {
		// 没有类，索引文件路径
		return b.IndexDocument(ctx, "file:"+file.AbsolutePath, file.AbsolutePath)
	}

	for _, c := range classes {
		classID := "class:" + formatClassID(c.ID)
		classText := buildClassIndexText(c, file.AbsolutePath)
		if err := b.IndexDocument(ctx, classID, classText); err != nil {
			return err
		}

		methods, err := st.GetMethodsByClassID(ctx, c.ID)
		if err != nil {
			return fmt.Errorf("get methods for class %d: %w", c.ID, err)
		}
		for _, m := range methods {
			methodID := "method:" + formatClassID(m.ID)
			methodText := buildMethodIndexText(c, m, file.AbsolutePath)
			if err := b.IndexDocument(ctx, methodID, methodText); err != nil {
				return err
			}
		}
	}

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
	if err := pathsafe.MkdirAll(dir); err == nil && b.idf != nil {
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
				if err := pathsafe.MkdirAll(dir); err != nil {
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

// BuildAndRemove 从 Store 读取单个文件信息，删除该文件所有类和方法的索引文档。
//
// 用于文件被删除后的增量同步。
func (b *IndexBuilder) BuildAndRemove(ctx context.Context, st store.Store, filePath string) error {
	file, err := st.GetFileByPath(ctx, filePath)
	if err != nil || file == nil {
		// 文件不存在，删除文件本身（如果索引过）
		return b.RemoveDocument(ctx, "file:"+filePath)
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
	return b.RemoveDocument(ctx, "file:"+filePath)
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