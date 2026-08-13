package vector

import (
	"context"
	"fmt"
	"sync"
)

// Indexer 向量索引构建器。
//
// 使用 Embedder 对实体生成 embedding 向量，存入 VectorStore。
// 支持异步 worker pool 批量处理。
type Indexer struct {
	store   VectorStore
	model   Embedder
	workers int
	queue   chan indexJob
	wg      sync.WaitGroup
	started bool
	mu      sync.Mutex
}

type indexJob struct {
	ctx  context.Context
	ent  TextEmbeddable
	errC chan error
}

// NewIndexer 创建向量索引器。
//
// workers 为并发 embedding 工作数，传 0 使用默认值 2。
func NewIndexer(store VectorStore, model Embedder, workers int) *Indexer {
	if workers <= 0 {
		workers = 2
	}
	return &Indexer{
		store:   store,
		model:   model,
		workers: workers,
		queue:   make(chan indexJob, 100),
	}
}

// Start 启动后台 worker pool。
func (idx *Indexer) Start(ctx context.Context) {
	idx.mu.Lock()
	defer idx.mu.Unlock()

	if idx.started {
		return
	}
	idx.started = true

	for i := 0; i < idx.workers; i++ {
		idx.wg.Add(1)
		go idx.worker(ctx)
	}
}

// Stop 停止 worker pool，等待所有任务完成。
func (idx *Indexer) Stop() {
	idx.mu.Lock()
	if !idx.started {
		idx.mu.Unlock()
		return
	}
	idx.started = false
	idx.mu.Unlock()

	close(idx.queue)
	idx.wg.Wait()
}

// BuildIndex 同步构建单个实体的向量索引。
func (idx *Indexer) BuildIndex(ctx context.Context, ent TextEmbeddable) error {
	vec, err := idx.model.Embed(ctx, ent.Text())
	if err != nil {
		return fmt.Errorf("embedding %s: %w", ent.ID(), err)
	}
	return idx.store.Add(ctx, ent.ID(), vec)
}

// RemoveDocument 从向量存储中删除指定 ID 的文档。
func (idx *Indexer) RemoveDocument(ctx context.Context, id string) error {
	return idx.store.Delete(ctx, id)
}

// Enqueue 异步入队一个实体的索引构建任务。
func (idx *Indexer) Enqueue(ctx context.Context, ent TextEmbeddable) <-chan error {
	errC := make(chan error, 1)
	select {
	case idx.queue <- indexJob{ctx: ctx, ent: ent, errC: errC}:
	case <-ctx.Done():
		errC <- ctx.Err()
	}
	return errC
}

// BatchBuild 同步批量构建实体的向量索引。
func (idx *Indexer) BatchBuild(ctx context.Context, ents []TextEmbeddable) error {
	ids := make([]string, 0, len(ents))
	vectors := make([][]float32, 0, len(ents))

	for _, ent := range ents {
		vec, err := idx.model.Embed(ctx, ent.Text())
		if err != nil {
			return fmt.Errorf("embedding %s: %w", ent.ID(), err)
		}
		ids = append(ids, ent.ID())
		vectors = append(vectors, vec)
	}

	return idx.store.BatchAdd(ctx, ids, vectors)
}

// Search 对查询文本执行语义搜索。
func (idx *Indexer) Search(ctx context.Context, query string, k int) ([]SearchResult, error) {
	vec, err := idx.model.Embed(ctx, query)
	if err != nil {
		return nil, fmt.Errorf("query embedding: %w", err)
	}
	return idx.store.Search(ctx, vec, k)
}

func (idx *Indexer) worker(ctx context.Context) {
	defer idx.wg.Done()
	for job := range idx.queue {
		err := idx.BuildIndex(job.ctx, job.ent)
		job.errC <- err
	}
}