// Package integration 提供端到端性能基准测试。
//
// 覆盖核心模块的关键路径：
//   - FTS 索引/搜索（MemoryFTS）
//   - LocalEmbedder Embedding
//   - IndexBuilder 全量构建
//   - 双路检索器搜索
//   - 完整流水线（scan → store → index → search）
//
// 运行方式：go test -bench=. -benchmem ./internal/integration/
package integration

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/scanner"
	"github.com/idcu/codeschema/internal/search"
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
	"github.com/idcu/codeschema/internal/vector"
)

// ---------------------------------------------------------------------------
// Helper: 生成测试数据
// ---------------------------------------------------------------------------

// benchmarkGenerateDocuments 生成 n 个文档，每个文档包含 docSize 个 token。
// 返回 ids 和 contents 列表。
func benchmarkGenerateDocuments(n, docSize int) (ids []string, contents []string) {
	ids = make([]string, n)
	contents = make([]string, n)
	wordPool := []string{
		"User", "Order", "Service", "Controller", "Repository", "Entity",
		"Handler", "Manager", "Factory", "Builder", "Strategy", "Adapter",
		"Proxy", "Decorator", "Observer", "Visitor", "Command", "Event",
		"Query", "Mutation", "Resolver", "Provider", "Consumer", "Pipeline",
		"Filter", "Interceptor", "Listener", "Task", "Job", "Worker",
		"Config", "Registry", "Cache", "Session", "Context", "Request",
		"Response", "Model", "View", "Template", "Resource", "Endpoint",
		"Client", "Server", "Transport", "Protocol", "Format", "Parser",
		"Generator", "Validator", "Sanitizer", "Normalizer", "Indexer",
		"Searcher", "Ranker", "Scorer", "Tokenizer", "Analyzer", "Compiler",
		"Interpreter", "Runtime", "Loader", "Emitter", "Optimizer", "Linter",
	}
	for i := 0; i < n; i++ {
		id := fmt.Sprintf("doc:%d", i)
		ids[i] = id
		var sb strings.Builder
		for j := 0; j < docSize; j++ {
			if j > 0 {
				sb.WriteByte(' ')
			}
			sb.WriteString(wordPool[rand.Intn(len(wordPool))])
		}
		contents[i] = sb.String()
	}
	return
}

// benchmarkGenerateSourceFiles 生成 n 个 Go 源文件到 dir 目录。
// 每个文件包含一个类和一个方法。
func benchmarkGenerateSourceFiles(dir string, n int) {
	for i := 0; i < n; i++ {
		className := fmt.Sprintf("Class%d", i)
		methodName := fmt.Sprintf("Method%d", i)
		content := fmt.Sprintf(`package bench

type %s struct {
	ID   string
	Name string
	Data []byte
}

func (c *%s) %s() string {
	return "result"
}

func New%s() *%s {
	return &%s{}
}
`, className, className, methodName, className, className, className)
		path := filepath.Join(dir, fmt.Sprintf("file_%d.go", i))
		if err := os.WriteFile(path, []byte(content), 0644); err != nil {
			panic(err)
		}
	}
}

// ---------------------------------------------------------------------------
// Benchmark: MemoryFTS 索引
// ---------------------------------------------------------------------------

// BenchmarkMemoryFTS_Index 测试 MemoryFTS 在不同文档数量下的索引性能。
func BenchmarkMemoryFTS_Index(b *testing.B) {
	sizes := []int{100, 1000, 5000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("docs=%d", n), func(b *testing.B) {
			ids, contents := benchmarkGenerateDocuments(n, 50)
			ctx := context.Background()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				fts := search.NewMemoryFTS()
				_ = fts.BatchIndex(ctx, ids, contents)
			}
		})
	}
}

// BenchmarkMemoryFTS_Search 测试 MemoryFTS 在不同文档数量下的搜索性能。
func BenchmarkMemoryFTS_Search(b *testing.B) {
	sizes := []int{100, 1000, 5000}
	queries := []string{"User", "Service", "Indexer"}
	for _, n := range sizes {
		ids, contents := benchmarkGenerateDocuments(n, 50)
		ctx := context.Background()
		fts := search.NewMemoryFTS()
		_ = fts.BatchIndex(ctx, ids, contents)

		for _, q := range queries {
			b.Run(fmt.Sprintf("docs=%d/q=%s", n, q), func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_, _ = fts.Search(ctx, q, search.FTSModeFuzzy, 20)
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Benchmark: LocalEmbedder
// ---------------------------------------------------------------------------

// BenchmarkLocalEmbedder_Embed 测试 LocalEmbedder 的 Embed 性能。
func BenchmarkLocalEmbedder_Embed(b *testing.B) {
	sizes := []int{128, 512, 1024}
	textSizes := []int{10, 50, 200}
	for _, dim := range sizes {
		for _, words := range textSizes {
			b.Run(fmt.Sprintf("dim=%d/words=%d", dim, words), func(b *testing.B) {
				em := vector.NewLocalEmbedder(dim)
				ctx := context.Background()
				// 先 Observe 一批文档以建立 IDF 词典
				_, observeContents := benchmarkGenerateDocuments(100, 50)
				for _, t := range observeContents {
					em.Observe(t)
				}

				_, texts := benchmarkGenerateDocuments(1, words)
				text := texts[0]
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_, _ = em.Embed(ctx, text)
				}
			})
		}
	}
}

// BenchmarkLocalEmbedder_Observe 测试 LocalEmbedder 的 Observe 性能。
func BenchmarkLocalEmbedder_Observe(b *testing.B) {
	sizes := []int{100, 1000, 5000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("docs=%d", n), func(b *testing.B) {
			_, contents := benchmarkGenerateDocuments(n, 50)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				em := vector.NewLocalEmbedder(128)
				for _, c := range contents {
					em.Observe(c)
				}
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Benchmark: IndexBuilder 全量构建
// ---------------------------------------------------------------------------

// BenchmarkIndexBuilder_BuildFromStore 测试 IndexBuilder 从 Store 全量构建索引的性能。
func BenchmarkIndexBuilder_BuildFromStore(b *testing.B) {
	fileSizes := []int{10, 50, 200}
	storeDir := b.TempDir()

	for _, n := range fileSizes {
		// 准备测试数据
		dir := b.TempDir()
		benchmarkGenerateSourceFiles(dir, n)

		// 先扫描到 Store
		ctx := context.Background()
		st := store.NewStore("file")
		if err := st.Open(ctx, storeDir); err != nil {
			b.Fatalf("open store: %v", err)
		}

		reg := parser.NewRegistry()
		// 使用 mock parser 模拟多文件解析
		reg.Register(&mockParser{
			parseFn: func(_ context.Context, path string) (*parser.IRDocument, error) {
				base := filepath.Base(path)
				className := strings.TrimSuffix(base, ".go")
				return &parser.IRDocument{
					Source:   "mock",
					Language: "go",
					FilePath: path,
					Classes: []parser.ClassIR{
						{Name: className, FullName: fmt.Sprintf("bench.%s", className), Type: "CLASS"},
					},
					Methods: []parser.MethodIR{
						{Name: "DoSomething", Signature: "DoSomething() string", ReturnType: "string", ClassFQN: fmt.Sprintf("bench.%s", className)},
					},
				}, nil
			},
		})

		s := scanner.NewScanner(st, reg, 4)
		_ = s.ScanAll(ctx, dir)

		b.Run(fmt.Sprintf("files=%d", n), func(b *testing.B) {
			fts := search.NewMemoryFTS()
			vs := vector.NewMemoryStore()
			em := vector.NewLocalEmbedder(128)
			idx := vector.NewIndexer(vs, em, 4)

			builder := search.NewIndexBuilder(fts, idx, em)
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_, _ = builder.BuildFromStore(ctx, st)
			}
		})

		st.Close()
	}
}

// ---------------------------------------------------------------------------
// Benchmark: Searcher 双路检索
// ---------------------------------------------------------------------------

// BenchmarkSearcher_Search 测试双路检索器在不同模式下的搜索性能。
func BenchmarkSearcher_Search(b *testing.B) {
	sizes := []int{100, 1000}
	modes := []search.SearchMode{search.SearchModeExact, search.SearchModeSemantic, search.SearchModeBoth}

	for _, n := range sizes {
		ids, contents := benchmarkGenerateDocuments(n, 50)
		ctx := context.Background()

		// 准备 FTS
		fts := search.NewMemoryFTS()
		_ = fts.BatchIndex(ctx, ids, contents)

		// 准备向量存储
		vs := vector.NewMemoryStore()
		em := vector.NewLocalEmbedder(128)
		for _, c := range contents {
			em.Observe(c)
		}
		idx := vector.NewIndexer(vs, em, 4)
		for i := range ids {
			vec, _ := em.Embed(ctx, contents[i])
			_ = vs.Add(ctx, ids[i], vec)
		}
		searcher := search.NewSearcher(fts, search.NewVectorAdapter(idx), nil)

		for _, mode := range modes {
			b.Run(fmt.Sprintf("docs=%d/mode=%s", n, mode), func(b *testing.B) {
				b.ResetTimer()
				for i := 0; i < b.N; i++ {
					_, _ = searcher.Search(ctx, "User", mode, 20)
				}
			})
		}
	}
}

// ---------------------------------------------------------------------------
// Benchmark: 完整流水线
// ---------------------------------------------------------------------------

// BenchmarkFullPipeline 测试完整流水线（scan → store → index → search）的性能。
func BenchmarkFullPipeline(b *testing.B) {
	fileSizes := []int{10, 50, 100}
	storeDir := b.TempDir()

	for _, n := range fileSizes {
		dir := b.TempDir()
		benchmarkGenerateSourceFiles(dir, n)

		b.Run(fmt.Sprintf("files=%d", n), func(b *testing.B) {
			b.StopTimer()

			// 每次迭代重新创建所有组件
			for i := 0; i < b.N; i++ {
				ctx := context.Background()
				st := store.NewStore("file")
				if err := st.Open(ctx, storeDir); err != nil {
					b.Fatalf("open store: %v", err)
				}

				reg := parser.NewRegistry()
				reg.Register(&mockParser{
					parseFn: func(_ context.Context, path string) (*parser.IRDocument, error) {
						base := filepath.Base(path)
						className := strings.TrimSuffix(base, ".go")
						return &parser.IRDocument{
							Source:   "mock",
							Language: "go",
							FilePath: path,
							Classes: []parser.ClassIR{
								{Name: className, FullName: fmt.Sprintf("bench.%s", className), Type: "CLASS"},
							},
							Methods: []parser.MethodIR{
								{Name: "DoSomething", Signature: "DoSomething() string", ReturnType: "string", ClassFQN: fmt.Sprintf("bench.%s", className)},
							},
						}, nil
					},
				})

				s := scanner.NewScanner(st, reg, 4)
				_ = s.ScanAll(ctx, dir)

				fts := search.NewMemoryFTS()
				vs := vector.NewMemoryStore()
				em := vector.NewLocalEmbedder(128)
				idx := vector.NewIndexer(vs, em, 4)
				builder := search.NewIndexBuilder(fts, idx, em)
				_, _ = builder.BuildFromStore(ctx, st)

				searcher := search.NewSearcher(fts, search.NewVectorAdapter(idx), nil)
				svc := service.NewService(st)
				svc.WithSearcher(searcher).WithIndexBuilder(builder)

				b.StartTimer()
				// 执行搜索
				results, _ := svc.Search(ctx, "Class", "both", 10)
				_ = results
				b.StopTimer()

				st.Close()
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Benchmark: 异步索引队列
// ---------------------------------------------------------------------------

// BenchmarkIndexBuilder_AsyncIndex 测试异步索引队列的吞吐量。
func BenchmarkIndexBuilder_AsyncIndex(b *testing.B) {
	workerCounts := []int{1, 2, 4, 8}
	docCount := 1000

	for _, workers := range workerCounts {
		b.Run(fmt.Sprintf("workers=%d/docs=%d", workers, docCount), func(b *testing.B) {
			ids, contents := benchmarkGenerateDocuments(docCount, 50)
			ctx := context.Background()

			for i := 0; i < b.N; i++ {
				fts := search.NewMemoryFTS()
				vs := vector.NewMemoryStore()
				em := vector.NewLocalEmbedder(128)
				idx := vector.NewIndexer(vs, em, workers)
				builder := search.NewIndexBuilder(fts, idx, em)

				builder.StartAsync(ctx, 4096, workers)
				for j := range ids {
					builder.EnqueueIndex(ctx, ids[j], contents[j])
				}
				builder.StopAsync()
			}
		})
	}
}

// ---------------------------------------------------------------------------
// Benchmark: 重排器
// ---------------------------------------------------------------------------

// BenchmarkReranker_Rerank 测试融合重排器的性能。
func BenchmarkReranker_Rerank(b *testing.B) {
	sizes := []int{100, 500, 1000}
	for _, n := range sizes {
		b.Run(fmt.Sprintf("results=%d", n), func(b *testing.B) {
			ftsResults := make([]search.SearchResult, n)
			vectorResults := make([]search.SearchResult, n)
			for i := 0; i < n; i++ {
				ftsResults[i] = search.SearchResult{
					Symbol: strconv.Itoa(i),
					Score:  float64(rand.Intn(1000)) / 1000,
					Source: "fts",
				}
				vectorResults[i] = search.SearchResult{
					Symbol: strconv.Itoa(i + n),
					Score:  float64(rand.Intn(1000)) / 1000,
					Source: "vector",
				}
			}

			reranker := search.DefaultReranker()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				_ = reranker.Rerank(ftsResults, vectorResults, 20)
			}
		})
	}
}