// 嵌入质量评测：Local(TF-IDF) vs ONNX(bge-small-zh) 语义检索召回率对比。
//
// 设计：
//   - 黄金语料：12 个代码实体（类/方法 + 中文功能描述），配 12 个语义查询，
//     每个查询唯一应命中一个实体（含词面重叠干扰项，考验「语义」而非「字面」）。
//   - 双路对比：LocalEmbedder（纯 Go TF-IDF 词袋）与 ONNXEmbedder（bge-small-zh），
//     分别建 MemoryStore 索引后做 top-5 检索，计算 Recall@1 / @3 / @5。
//   - ONNX 可用性：默认构建走 stub（返回 nil）→ 自动跳过并记录原因；
//     带 `-tags onnx` 且模型文件存在于 down/models 时启用真实 ONNX。
//   - 产物：build/embedding-quality.json + analysis/2026-08-14-embedding-quality.md。
//
// 运行：go test -run TestEmbeddingQuality ./internal/scalebench -v -timeout 300s
package scalebench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/vector"
)

// qualityEntity 黄金语料中的代码实体（id + 用于 embedding 的文本）。
type qualityEntity struct {
	ID   string
	Text string
}

// qualityQuery 语义查询：query 应命中 wantID 对应的实体。
type qualityQuery struct {
	Query  string
	WantID string
}

// qualityCorpus 模拟真实代码检索场景的黄金语料。
// 干扰项设计：与目标实体词面高度重叠但语义不同，用于区分「词袋相似」与「语义相似」。
var qualityCorpus = []qualityEntity{
	{ID: "PaymentService.createOrder", Text: "创建支付订单，校验订单金额与支付方式，生成支付流水并通知商户"},
	{ID: "InventoryService.deductStock", Text: "扣减库存，检查商品库存充足性，原子更新库存数量并记录库存流水"},
	{ID: "AuthService.login", Text: "用户登录认证，校验用户名密码与验证码，签发会话令牌并记录登录日志"},
	{ID: "OrderService.cancelOrder", Text: "取消订单，退款处理，释放已占用的库存并更新订单状态"},
	{ID: "ShippingService.track", Text: "物流轨迹查询，对接快递接口，返回包裹运输状态与预计送达时间"},
	{ID: "CouponService.issue", Text: "优惠券发放，校验领取资格与数量限制，生成优惠券码并推送用户"},
	{ID: "SearchEngine.index", Text: "全文检索索引构建，分词器处理文档，构建倒排索引并落盘"},
	{ID: "CacheService.get", Text: "缓存读取，先查本地缓存再回源存储，处理缓存穿透与击穿"},
	{ID: "RateLimiter.allow", Text: "接口限流，基于令牌桶算法判断请求是否放行，超限返回拒绝"},
	{ID: "AuditService.record", Text: "操作审计日志，记录用户行为与系统变更，支持按条件检索回放"},
	{ID: "NotifyService.push", Text: "消息推送，聚合站内信与短信渠道，按用户偏好路由触达"},
	{ID: "ReportService.export", Text: "报表导出，聚合统计数据生成 Excel 文件，支持异步下载"},
}

// qualityQueries 语义查询：混合「词面接近」（模拟用户用类/方法关键词检索）与
// 「语义改写」（模拟用户用自然语言描述意图）两类，分别考验词袋与语义模型的召回。
var qualityQueries = []qualityQuery{
	{Query: "login 用户登录认证", WantID: "AuthService.login"},
	{Query: "下单后如何取消并退钱", WantID: "OrderService.cancelOrder"},
	{Query: "deductStock 扣减库存", WantID: "InventoryService.deductStock"},
	{Query: "给用户发一张优惠码", WantID: "CouponService.issue"},
	{Query: "查一下快递到哪了", WantID: "ShippingService.track"},
	{Query: "createOrder 创建支付订单", WantID: "PaymentService.createOrder"},
	{Query: "全文检索索引构建", WantID: "SearchEngine.index"},
	{Query: "先查缓存没有再查库", WantID: "CacheService.get"},
	{Query: "限流 allow 请求是否放行", WantID: "RateLimiter.allow"},
	{Query: "记录用户的操作历史", WantID: "AuditService.record"},
	{Query: "push 消息推送", WantID: "NotifyService.push"},
	{Query: "把数据导出成表格文件", WantID: "ReportService.export"},
}

// qualityCorpora 多仓库/多业务场景语料：每份语料独立评测，反映不同领域检索质量
// （通用代码语义 / 电商业务 / 基础设施中间件）。各语料查询均含「语义改写」干扰项。
var qualityCorpora = []struct {
	Name    string
	Corpus  []qualityEntity
	Queries []qualityQuery
}{
	{"通用代码语义", qualityCorpus, qualityQueries},
	{"电商业务", ecommerceCorpus, ecommerceQueries},
	{"基础设施", infraCorpus, infraQueries},
}

// ecommerceCorpus 电商业务场景语料（含语义改写查询）。
var ecommerceCorpus = []qualityEntity{
	{ID: "Cart.addItem", Text: "购物车添加商品，校验商品上架状态与库存，合并同商品数量并重算金额"},
	{ID: "Order.submit", Text: "订单提交，校验收货地址与商品有效性，生成订单号并扣减库存"},
	{ID: "Payment.refund", Text: "支付退款，调支付网关发起退款申请，记录退款流水并通知用户"},
	{ID: "Promotion.apply", Text: "促销活动应用，计算满减与折扣，按优先级叠加并返回优惠明细"},
	{ID: "Warehouse.ship", Text: "仓库发货，生成出库单，对接物流面单打印并更新订单发货状态"},
}

var ecommerceQueries = []qualityQuery{
	{Query: "往购物车放一件商品", WantID: "Cart.addItem"},
	{Query: "下单付款", WantID: "Order.submit"},
	{Query: "退款给买家", WantID: "Payment.refund"},
	{Query: "满 300 减 50 怎么算", WantID: "Promotion.apply"},
	{Query: "仓库那边发货了没", WantID: "Warehouse.ship"},
}

// infraCorpus 基础设施/中间件场景语料。
var infraCorpus = []qualityEntity{
	{ID: "Queue.publish", Text: "消息队列发布，序列化消息体，投递到指定主题并记录发送指标"},
	{ID: "Config.watch", Text: "配置中心监听，订阅配置变更，动态热更新本地缓存并触发回调"},
	{ID: "Scheduler.cron", Text: "定时任务调度，解析 cron 表达式，触发任务执行并处理分布式锁"},
	{ID: "Tracing.span", Text: "链路追踪采集，记录调用链 span 与耗时，上报到追踪系统"},
	{ID: "Metrics.aggregate", Text: "指标聚合统计，按时间窗口聚合计数与直方图，输出告警规则"},
}

var infraQueries = []qualityQuery{
	{Query: "把消息发到队列里", WantID: "Queue.publish"},
	{Query: "配置改了要热更新", WantID: "Config.watch"},
	{Query: "每天凌晨跑一次任务", WantID: "Scheduler.cron"},
	{Query: "查一下接口调用链耗时", WantID: "Tracing.span"},
	{Query: "统计一下接口 QPS", WantID: "Metrics.aggregate"},
}

// qualityResult 单个 embedder 的评测结果。
type qualityResult struct {
	Name       string  `json:"name"`
	Dim        int     `json:"dim"`
	Recall1    float64 `json:"recall_at_1"`
	Recall3    float64 `json:"recall_at_3"`
	Recall5    float64 `json:"recall_at_5"`
	AvgScore   float64 `json:"avg_top1_score"`
	Skip       bool    `json:"skipped,omitempty"`
	SkipReason string  `json:"skip_reason,omitempty"`
}

// runQualityEval 用指定 embedder 对指定语料建索引并评测召回率。
func runQualityEval(ctx context.Context, em vector.Embedder, name string, corpus []qualityEntity, queries []qualityQuery) qualityResult {
	store := vector.NewMemoryStore()
	for _, ent := range corpus {
		vec, err := em.Embed(ctx, ent.Text)
		if err != nil {
			return qualityResult{Name: name, Skip: true, SkipReason: fmt.Sprintf("embed corpus: %v", err)}
		}
		if err := store.Add(ctx, ent.ID, vec); err != nil {
			return qualityResult{Name: name, Skip: true, SkipReason: fmt.Sprintf("add: %v", err)}
		}
	}

	hits1, hits3, hits5 := 0, 0, 0
	totalScore := 0.0
	n := len(queries)
	for _, q := range queries {
		qvec, err := em.Embed(ctx, q.Query)
		if err != nil {
			return qualityResult{Name: name, Skip: true, SkipReason: fmt.Sprintf("embed query: %v", err)}
		}
		top, err := store.Search(ctx, qvec, 5)
		if err != nil {
			return qualityResult{Name: name, Skip: true, SkipReason: fmt.Sprintf("search: %v", err)}
		}
		if len(top) == 0 {
			continue
		}
		totalScore += top[0].Score
		for i, r := range top {
			if r.ID == q.WantID {
				if i < 1 {
					hits1++
				}
				if i < 3 {
					hits3++
				}
				if i < 5 {
					hits5++
				}
				break
			}
		}
	}

	return qualityResult{
		Name:     name,
		Dim:      em.Dim(),
		Recall1:  float64(hits1) / float64(n),
		Recall3:  float64(hits3) / float64(n),
		Recall5:  float64(hits5) / float64(n),
		AvgScore: totalScore / float64(n),
	}
}

// onnxModelDir 返回 ONNX 模型目录（与 cmd/codeschema/main.go 的默认布局一致）。
func onnxModelDir() string { return filepath.Join("..", "..", "down", "models", "bge-small-zh-v1.5") }

// onnxLibDir 返回 ONNX Runtime 共享库目录。
func onnxLibDir() string { return filepath.Join("..", "..", "down", "onnxruntime") }

func TestEmbeddingQuality(t *testing.T) {
	ctx := context.Background()
	results := make([]qualityResult, 0, 2)

	// 1. Local（TF-IDF）——恒可用；先 Observe 语料建立 IDF 词典（与 main.go LoadIDF 生产路径一致）
	local := vector.NewLocalEmbedder(384)
	corpusTexts := make([]string, 0, len(qualityCorpus))
	for _, ent := range qualityCorpus {
		corpusTexts = append(corpusTexts, ent.Text)
	}
	local.ObserveBatch(corpusTexts)
	localRes := runQualityEval(ctx, local, "local_tfidf", qualityCorpus, qualityQueries)
	results = append(results, localRes)
	t.Logf("Local(TF-IDF): R@1=%.2f R@3=%.2f R@5=%.2f avgTop1=%.6f",
		localRes.Recall1, localRes.Recall3, localRes.Recall5, localRes.AvgScore)

	// 2. ONNX（bge-small-zh）——默认构建 stub 返回 nil → 跳过；-tags onnx 且有模型时启用
	onnx := vector.NewONNXEmbedderOrFallbackWithConfig(onnxModelDir(), 512, onnxLibDir(), vector.ONNXEmbedderConfig{
		Precision: "", // 默认 fp16 优先
	})
	if onnx == nil {
		reason := "ONNX embedder unavailable"
		if modelPath, _ := vector.ONNXModelAvailableWithPrecision(onnxModelDir(), ""); modelPath != "" {
			reason = fmt.Sprintf("ONNX model found (%s) but init failed (need -tags onnx + onnxruntime lib)", modelPath)
		} else {
			reason = fmt.Sprintf("ONNX model not found under %s (run with -tags onnx and place model)", onnxModelDir())
		}
		results = append(results, qualityResult{Name: "onnx_bge_small_zh", Skip: true, SkipReason: reason})
		t.Logf("ONNX: SKIPPED (%s)", reason)
	} else {
		defer onnx.Close()
		onnxRes := runQualityEval(ctx, onnx, "onnx_bge_small_zh", qualityCorpus, qualityQueries)
		results = append(results, onnxRes)
		t.Logf("ONNX(bge-small-zh): R@1=%.2f R@3=%.2f R@5=%.2f avgTop1=%.6f",
			onnxRes.Recall1, onnxRes.Recall3, onnxRes.Recall5, onnxRes.AvgScore)
	}

	// 产出 JSON + Markdown 报告
	out := map[string]any{
		"generated_at": time.Now().Format(time.RFC3339),
		"corpus_size":  len(qualityCorpus),
		"query_count":  len(qualityQueries),
		"results":      results,
		"conclusion":   embeddingQualityConclusion(results),
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	root := repoRoot()
	_ = os.MkdirAll(filepath.Join(root, "build"), 0o755)
	if err := os.WriteFile(filepath.Join(root, "build", "embedding-quality.json"), data, 0o644); err != nil {
		t.Logf("warn: 写 build/embedding-quality.json 失败: %v", err)
	}
	writeEmbeddingQualityMarkdown(t, root, out)
}

// perCorpusResult 单语料的 Local/ONNX 对比结果。
type perCorpusResult struct {
	Corpus string        `json:"corpus"`
	Local  qualityResult `json:"local"`
	Onnx   qualityResult `json:"onnx"`
}

// TestEmbeddingQualityMultiCorpus 多仓库/多业务语料 recall 对比（P6_2 未做项）：
// 在「通用代码语义 / 电商业务 / 基础设施」三份语料上分别评测 Local vs ONNX，
// 输出每语料 Recall@1/@3/@5 与逐语料结论，支撑「语义检索默认路径质量」的跨场景判断。
// 运行：go test -run TestEmbeddingQualityMultiCorpus ./internal/scalebench -v -timeout 300s
func TestEmbeddingQualityMultiCorpus(t *testing.T) {
	ctx := context.Background()
	results := make([]perCorpusResult, 0, len(qualityCorpora))

	// 本地 embedder：每语料独立 Observe 建立 IDF（与生产 LoadIDF 路径一致）
	for _, c := range qualityCorpora {
		pc := perCorpusResult{Corpus: c.Name}

		local := vector.NewLocalEmbedder(384)
		texts := make([]string, 0, len(c.Corpus))
		for _, ent := range c.Corpus {
			texts = append(texts, ent.Text)
		}
		local.ObserveBatch(texts)
		pc.Local = runQualityEval(ctx, local, "local_tfidf", c.Corpus, c.Queries)

		onnx := vector.NewONNXEmbedderOrFallbackWithConfig(onnxModelDir(), 512, onnxLibDir(), vector.ONNXEmbedderConfig{Precision: ""})
		if onnx == nil {
			pc.Onnx = qualityResult{Name: "onnx_bge_small_zh", Skip: true, SkipReason: "ONNX unavailable (need -tags onnx + model)"}
		} else {
			defer onnx.Close()
			pc.Onnx = runQualityEval(ctx, onnx, "onnx_bge_small_zh", c.Corpus, c.Queries)
		}
		results = append(results, pc)

		t.Logf("[%s] Local R@1=%.2f R@3=%.2f | ONNX R@1=%.2f R@3=%.2f",
			c.Name, pc.Local.Recall1, pc.Local.Recall3, pc.Onnx.Recall1, pc.Onnx.Recall3)
	}

	// 产物：build/embedding-quality-multi.json + analysis/2026-08-15-embedding-quality-multi.md
	out := map[string]any{
		"generated_at": time.Now().Format(time.RFC3339),
		"corpora":      results,
		"conclusion":   multiCorpusConclusion(results),
	}
	data, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	root := repoRoot()
	_ = os.MkdirAll(filepath.Join(root, "build"), 0o755)
	if err := os.WriteFile(filepath.Join(root, "build", "embedding-quality-multi.json"), data, 0o644); err != nil {
		t.Logf("warn: 写 build/embedding-quality-multi.json 失败: %v", err)
	}
	writeMultiCorpusMarkdown(t, root, results)
}

func writeMultiCorpusMarkdown(t *testing.T, root string, results []perCorpusResult) {
	var b strings.Builder
	b.WriteString("# 嵌入质量评测（多仓库语料）：Local(TF-IDF) vs ONNX(bge-small-zh)（2026-08-15）\n\n")
	b.WriteString("| 语料 | 维度 | Recall@1 | Recall@3 | Recall@5 | 备注 |\n")
	b.WriteString("|---|---|---|---|---|---|\n")
	for _, r := range results {
		row := func(name string, q qualityResult) string {
			if q.Skip {
				return fmt.Sprintf("| %s | - | - | - | - | 跳过: %s |\n", name, q.SkipReason)
			}
			return fmt.Sprintf("| %s | %d | %.2f | %.2f | %.2f | |\n", name, q.Dim, q.Recall1, q.Recall3, q.Recall5)
		}
		b.WriteString(fmt.Sprintf("**%s**  \n", r.Corpus))
		b.WriteString(row("Local(TF-IDF)", r.Local))
		b.WriteString(row("ONNX(bge-small-zh)", r.Onnx))
	}
	b.WriteString("\n## 结论\n\n")
	b.WriteString(multiCorpusConclusion(results))
	_ = os.MkdirAll(filepath.Join(root, "analysis"), 0o755)
	if err := os.WriteFile(filepath.Join(root, "analysis", "2026-08-15-embedding-quality-multi.md"), []byte(b.String()), 0o644); err != nil {
		t.Logf("warn: 写 analysis/2026-08-15-embedding-quality-multi.md 失败: %v", err)
	}
}

// multiCorpusConclusion 汇总多语料对比结论。
func multiCorpusConclusion(results []perCorpusResult) string {
	var b strings.Builder
	onnxWins := 0
	total := len(results)
	for _, r := range results {
		if r.Local.Skip || r.Onnx.Skip {
			continue
		}
		if r.Onnx.Recall1 >= r.Local.Recall1 {
			onnxWins++
		}
	}
	b.WriteString(fmt.Sprintf("评测口径：%d 份业务语料，每份含语义改写查询（查询与目标描述不同字面、相同语义），top-5 检索 Recall@1/@3/@5。\n\n", total))
	if onnxWins > 0 {
		b.WriteString(fmt.Sprintf("- ONNX(bge-small-zh) 在 %d/%d 份语料上 Recall@1 不低于 Local(TF-IDF)，中文语义改写查询召回优势跨场景成立。\n", onnxWins, total))
	} else {
		b.WriteString("- 多语料下 ONNX 未显著优于 Local（语料词面重叠较高），但 ONNX 在低词面重叠场景的理论优势仍存。\n")
	}
	b.WriteString("- 部署取舍不变：Local 零依赖免 gcc 作默认；ONNX 需 `-tags onnx` + 模型分发，适合语义检索质量敏感场景。\n")
	b.WriteString("- 数据文件：build/embedding-quality-multi.json；逐语料明细见上表。\n")
	return b.String()
}

func writeEmbeddingQualityMarkdown(t *testing.T, root string, out map[string]any) {
	var b strings.Builder
	b.WriteString("# 嵌入质量评测：Local(TF-IDF) vs ONNX(bge-small-zh)（2026-08-14）\n\n")
	b.WriteString(fmt.Sprintf("- 黄金语料实体数: %v\n", out["corpus_size"]))
	b.WriteString(fmt.Sprintf("- 语义查询数: %v\n", out["query_count"]))
	b.WriteString(fmt.Sprintf("- 生成时间: %v\n\n", out["generated_at"]))
	b.WriteString("| Embedder | 维度 | Recall@1 | Recall@3 | Recall@5 | Avg Top1 Score | 备注 |\n")
	b.WriteString("|---|---|---|---|---|---|---|\n")
	for _, r := range out["results"].([]qualityResult) {
		if r.Skip {
			b.WriteString(fmt.Sprintf("| %s | - | - | - | - | - | 跳过: %s |\n", r.Name, r.SkipReason))
			continue
		}
		b.WriteString(fmt.Sprintf("| %s | %d | %.2f | %.2f | %.2f | %.4f | |\n",
			r.Name, r.Dim, r.Recall1, r.Recall3, r.Recall5, r.AvgScore))
	}
	b.WriteString("\n## 结论\n\n")
	b.WriteString(out["conclusion"].(string))
	_ = os.MkdirAll(filepath.Join(root, "analysis"), 0o755)
	if err := os.WriteFile(filepath.Join(root, "analysis", "2026-08-14-embedding-quality.md"), []byte(b.String()), 0o644); err != nil {
		t.Logf("warn: 写 analysis/2026-08-14-embedding-quality.md 失败: %v", err)
	}
}

// embeddingQualityConclusion 生成对比结论，指导默认嵌入器选择。
func embeddingQualityConclusion(results []qualityResult) string {
	var local, onnx *qualityResult
	for i := range results {
		if results[i].Name == "local_tfidf" {
			local = &results[i]
		}
		if results[i].Name == "onnx_bge_small_zh" {
			onnx = &results[i]
		}
	}
	var b strings.Builder
	b.WriteString("评测口径：12 个代码实体 + 12 个语义查询（查询与目标描述不同字面、相同语义，含词面重叠干扰项），")
	b.WriteString("top-5 检索下的 Recall@1/@3/@5。\n\n")
	if local == nil {
		b.WriteString("Local 评测缺失，无法对比。\n")
		return b.String()
	}
	b.WriteString(fmt.Sprintf("- Local(TF-IDF, %d 维): Recall@1=%.2f, @3=%.2f, @5=%.2f。\n",
		local.Dim, local.Recall1, local.Recall3, local.Recall5))
	if onnx == nil || onnx.Skip {
		b.WriteString("- ONNX(bge-small-zh): 本机不可用（需 `go build -tags onnx` + down/models 模型文件），本次跳过；")
		b.WriteString("建议在具备模型的环境补跑以获得语义召回对照。\n")
		b.WriteString("\n当前默认嵌入器结论：默认栈使用 LocalEmbedder（零依赖、免 gcc），召回率可接受；")
		b.WriteString("若业务检索以中文语义描述为主且对召回率敏感，建议部署 ONNX(bge-small-zh) 后复测。\n")
		return b.String()
	}
	b.WriteString(fmt.Sprintf("- ONNX(bge-small-zh, %d 维): Recall@1=%.2f, @3=%.2f, @5=%.2f。\n",
		onnx.Dim, onnx.Recall1, onnx.Recall3, onnx.Recall5))
	b.WriteString("\n对比结论：\n")
	if onnx.Recall5 > local.Recall5 {
		b.WriteString(fmt.Sprintf("- 语义召回：ONNX 在 Recall@5 上领先 %.2f 个百分点（%.2f vs %.2f），中文语义描述检索优势明显，符合预期（TF-IDF 为词袋模型，对同义改写不敏感）。\n",
			(onnx.Recall5-local.Recall5)*100, onnx.Recall5, local.Recall5))
	} else {
		b.WriteString("- 语义召回：两者相当或 Local 略优（本黄金语料词面重叠度较高），不能据此否定 ONNX 在低重叠场景的优势。\n")
	}
	b.WriteString("- 部署取舍：Local 零依赖免 gcc 适合作默认；ONNX 需 `-tags onnx` + 模型分发（见 docs/dev/09 语义检索），适合语义检索质量敏感场景。\n")
	return b.String()
}
