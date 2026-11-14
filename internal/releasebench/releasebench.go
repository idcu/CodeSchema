// Package releasebench 提供可复现的公开基准：测试关联 P/R/F1 与调用图结构指标。
//
// 设计约束：
//   - 真值独立于被测启发式：以 IR 中「测试方法 → 生产方法」的直接调用边作为真值，
//     避免用命名约定评估命名约定（会自我实现）。
//   - 固定输入 + 确定性采样：文件顺序、pair 排序、stride 采样均固定，报告可跨机器 diff。
//   - 只报告不设阈值：首轮基线只描述现状，不进入 CI 门禁。
//   - 调用图同时给两套口径：全量 resolution 与「仓内可解析边」resolution；
//     后者分母只算 callee 在索引中有精确/后缀/唯一短名候选的边，用于区分
//     「引擎解析失败」与「本就不可解析（外部 API/框架）」。
package releasebench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/idcu/codeschema/internal/parser"
	adapter "github.com/idcu/codeschema/internal/parser/adapter/treesitter"
	"github.com/idcu/codeschema/internal/scanner"
	"github.com/idcu/codeschema/internal/service"
	"github.com/idcu/codeschema/internal/store"
)

// Options 基准运行参数。
type Options struct {
	Language      string // 语言过滤（go/java/ts/...；空=全部）
	MaxPairs      int    // 真值 pair 采样上限（默认 200）
	MinConfidence int    // 测试关联置信度下限（默认 60）
	Workers       int    // 扫描并发（默认 4）
	ManualTruth   string // 人工真值数据集路径（可选；给出时追加独立 P/R/F1）
}

// Pair 一条真值：测试方法直接调用了生产方法。
type Pair struct {
	Production string `json:"production"`
	Test       string `json:"test"`
}

// PRF 微平均精确率/召回率/F1。
type PRF struct {
	TP        int     `json:"tp"`
	FP        int     `json:"fp"`
	FN        int     `json:"fn"`
	Precision float64 `json:"precision"`
	Recall    float64 `json:"recall"`
	F1        float64 `json:"f1"`
}

// CallGraph 调用图结构指标（可跨构建变体对照）。
type CallGraph struct {
	Adapter        string  `json:"adapter"`
	BuildVariant   string  `json:"build_variant"`
	Calls          int     `json:"calls"`
	UniqueCallers  int     `json:"unique_callers"`
	UniqueCallees  int     `json:"unique_callees"`
	ResolvedCalls  int     `json:"resolved_calls"`
	ResolutionRate float64 `json:"resolution_rate"`
	// RepoResolvable 是「仓内确有定义候选」的口径：分母只算 callee 能在索引中
	// 按精确名或包/模块后缀命中的边，用于区分「引擎解析失败」与「本就不可解析」。
	// 与上面的全量口径并排展示，旧的 Calls/ResolutionRate 定义不变。
	RepoResolvable         int     `json:"repo_resolvable"`
	RepoResolvableResolved int     `json:"repo_resolvable_resolved"`
	RepoResolutionRate     float64 `json:"repo_resolution_rate"`
}

// Report 单仓基准报告。
type Report struct {
	Timestamp        string        `json:"timestamp"`
	Repo             string        `json:"repo"`
	Commit           string        `json:"commit"`
	Language         string        `json:"language"`
	BuildVariant     string        `json:"build_variant"`
	Files            int           `json:"files"`
	ProdFiles        int           `json:"prod_files"`
	TestFiles        int           `json:"test_files"`
	ProdMethods      int           `json:"prod_methods"`
	TestMethods      int           `json:"test_methods"`
	GroundTruthPairs int           `json:"ground_truth_pairs"`
	SampledPairs     int           `json:"sampled_pairs"`
	Sampled          bool          `json:"sampled"`
	ProdsEvaluated   int           `json:"prods_evaluated"`
	PredictedLinks   int           `json:"predicted_links"`
	TestLink         PRF           `json:"test_link"`
	CallGraph        CallGraph     `json:"call_graph"`
	Manual           *ManualResult `json:"manual_truth,omitempty"`
}

// Run 在指定仓库上执行基准。
func Run(ctx context.Context, repoPath string, opts Options) (*Report, error) {
	if opts.MaxPairs <= 0 {
		opts.MaxPairs = 200
	}
	if opts.MinConfidence <= 0 {
		opts.MinConfidence = 60
	}
	if opts.Workers <= 0 {
		opts.Workers = 4
	}

	files, err := listFiles(repoPath, opts.Language)
	if err != nil {
		return nil, err
	}
	if len(files) == 0 {
		return nil, fmt.Errorf("no %s source files under %s", opts.Language, repoPath)
	}

	ad := adapter.NewTreeSitterAdapter()
	if err := ad.Init(ctx, nil); err != nil {
		return nil, fmt.Errorf("init parser: %w", err)
	}
	defer ad.Close()

	report := &Report{
		Timestamp:    time.Now().UTC().Format(time.RFC3339),
		Repo:         filepath.Base(repoPath),
		Commit:       gitHead(repoPath),
		Language:     opts.Language,
		BuildVariant: adapter.BuildVariant(),
		Files:        len(files),
		CallGraph:    CallGraph{Adapter: ad.Name(), BuildVariant: adapter.BuildVariant()},
	}

	// 第一遍：生产文件 → 方法索引 + 调用图规模。
	idx := newMethodIndex()
	testFQNs := make(map[string]struct{})
	var prodCalls, testCalls []parser.CallIR
	for _, f := range files {
		ir, perr := ad.Parse(ctx, f.path)
		if perr != nil || ir == nil {
			continue
		}
		if f.test {
			report.TestFiles++
			report.TestMethods += len(ir.Methods)
			testCalls = append(testCalls, ir.Calls...)
			for _, m := range ir.Methods {
				fqn := joinFQN(m.ClassFQN, m.Name)
				idx.add(fqn)
				testFQNs[fqn] = struct{}{}
			}
			continue
		}
		report.ProdFiles++
		report.ProdMethods += len(ir.Methods)
		prodCalls = append(prodCalls, ir.Calls...)
		for _, m := range ir.Methods {
			idx.add(joinFQN(m.ClassFQN, m.Name))
		}
	}

	// 第二遍：测试文件 → 直接调用边作为测试关联真值。
	pairs := collectTruth(ctx, ad, files, idx, testFQNs)
	report.GroundTruthPairs = len(pairs)
	sampled, truncated := samplePairs(pairs, opts.MaxPairs)
	report.SampledPairs = len(sampled)
	report.Sampled = truncated

	// 被测启发式：生产 FindTestLinks，按生产方法逐个查询。
	svc, cleanup, err := newService(ctx, repoPath, opts.Workers)
	if err != nil {
		return nil, err
	}
	defer cleanup()

	truth, prods := prepareEvaluation(pairs, sampled)
	report.TestLink, report.ProdsEvaluated, report.PredictedLinks = evaluate(ctx, svc, truth, prods, opts.MinConfidence)

	// 人工真值（独立来源）：只评估标注的生产方法，口径见 manual.go。
	if opts.ManualTruth != "" {
		report.Manual, err = evaluateManual(ctx, svc, repoPath, opts.ManualTruth, ad, idx, opts.MinConfidence)
		if err != nil {
			return nil, err
		}
	}

	// 调用图结构指标：解析率 = callee 能落到索引方法的边占比。
	allCalls := append(append([]parser.CallIR{}, prodCalls...), testCalls...)
	report.CallGraph.Calls = len(allCalls)
	callers := make(map[string]struct{}, len(allCalls))
	callees := make(map[string]struct{}, len(allCalls))
	for _, c := range allCalls {
		if c.CallerFQN != "" {
			callers[c.CallerFQN] = struct{}{}
		}
		if c.CalleeFQN == "" {
			continue
		}
		callees[c.CalleeFQN] = struct{}{}
		resolved := false
		if _, ok := idx.resolve(c.CalleeFQN); ok {
			report.CallGraph.ResolvedCalls++
			resolved = true
		}
		if len(idx.candidates(c.CalleeFQN)) > 0 {
			report.CallGraph.RepoResolvable++
			if resolved {
				report.CallGraph.RepoResolvableResolved++
			}
		}
	}
	report.CallGraph.UniqueCallers = len(callers)
	report.CallGraph.UniqueCallees = len(callees)
	if report.CallGraph.Calls > 0 {
		report.CallGraph.ResolutionRate = round4(float64(report.CallGraph.ResolvedCalls) / float64(report.CallGraph.Calls))
	}
	if report.CallGraph.RepoResolvable > 0 {
		report.CallGraph.RepoResolutionRate = round4(float64(report.CallGraph.RepoResolvableResolved) / float64(report.CallGraph.RepoResolvable))
	}

	return report, nil
}

type sourceFile struct {
	path string
	test bool
}

// listFiles 列出参与基准的源码文件（固定排序，保证可复现）。
func listFiles(root, lang string) ([]sourceFile, error) {
	var files []sourceFile
	err := filepath.WalkDir(root, func(path string, d os.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() {
			if path == root {
				return nil
			}
			if ignoredDir[d.Name()] {
				return filepath.SkipDir
			}
			return nil
		}
		l, ok := extLang[strings.ToLower(filepath.Ext(path))]
		if !ok || (lang != "" && l != lang) {
			return nil
		}
		files = append(files, sourceFile{path: path, test: isTestFile(path, l)})
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(files, func(i, j int) bool { return files[i].path < files[j].path })
	return files, nil
}

var ignoredDir = map[string]bool{
	".git": true, "vendor": true, "node_modules": true, "build": true,
	"dist": true, "target": true, ".idea": true, ".vscode": true,
}

var extLang = map[string]string{
	".go": "go", ".java": "java", ".ts": "ts", ".tsx": "ts", ".js": "ts", ".jsx": "ts",
	".py": "py", ".rs": "rust", ".cpp": "cpp", ".cc": "cpp", ".c": "cpp",
	".h": "cpp", ".hpp": "cpp", ".kt": "kotlin",
}

// isTestFile 按语言约定识别测试文件。
func isTestFile(path, lang string) bool {
	base := strings.ToLower(filepath.Base(path))
	switch lang {
	case "go":
		return strings.HasSuffix(base, "_test.go")
	case "java", "kotlin":
		return strings.HasSuffix(base, "test.java") || strings.HasSuffix(base, "tests.java") ||
			strings.HasSuffix(base, "it.java") || strings.HasSuffix(base, "test.kt")
	case "py":
		return strings.HasPrefix(base, "test_") || strings.HasSuffix(base, "_test.py")
	case "ts":
		return strings.Contains(base, ".test.") || strings.Contains(base, ".spec.") || strings.Contains(base, "_test.")
	}
	parts := strings.Split(strings.ToLower(filepath.ToSlash(path)), "/")
	for _, p := range parts {
		if p == "test" || p == "tests" {
			return true
		}
	}
	return false
}

// methodIndex 生产/测试方法索引；同名多义时视为不可解析，避免虚假匹配。
type methodIndex struct {
	count map[string]int
	short map[string][]string
}

func newMethodIndex() *methodIndex {
	return &methodIndex{count: make(map[string]int), short: make(map[string][]string)}
}

func (idx *methodIndex) add(fqn string) {
	if fqn == "" {
		return
	}
	if idx.count[fqn] == 0 {
		idx.short[shortName(fqn)] = append(idx.short[shortName(fqn)], fqn)
	}
	idx.count[fqn]++
}

func (idx *methodIndex) resolve(name string) (string, bool) {
	// 同一 FQN 可能被多次登记（Java 重载、同名多声明）；精确命中即唯一，
	// 不能用 count==1 判歧义，否则会把真实方法误判为不可解析。
	if idx.count[name] > 0 {
		return name, true
	}
	if cands := idx.short[shortName(name)]; len(cands) == 1 {
		return cands[0], true
	}
	return "", false
}

// candidates 返回 callee 在索引中的定义候选（精确名或去掉包/模块前缀后的后缀）。
// 用于「仓内确有定义」判定与真值抽取，不改变 resolve 的严格分辨率口径。
func (idx *methodIndex) candidates(callee string) []string {
	if idx.count[callee] > 0 {
		return []string{callee}
	}
	parts := strings.Split(callee, ".")
	for i := 1; i < len(parts); i++ {
		suffix := strings.Join(parts[i:], ".")
		if idx.count[suffix] > 0 {
			return []string{suffix}
		}
	}
	// 与 resolve 的短名回退保持一致：短名全局唯一时才算仓内有候选。
	if cands := idx.short[shortName(callee)]; len(cands) == 1 {
		return cands
	}
	return nil
}

func shortName(fqn string) string {
	if i := strings.LastIndex(fqn, "."); i >= 0 {
		return fqn[i+1:]
	}
	return fqn
}

func joinFQN(container, name string) string {
	if container == "" {
		return name
	}
	return container + "." + name
}

// collectTruth 从测试文件 IR 抽取「测试方法 → 生产方法」直接调用边。
func collectTruth(ctx context.Context, ad parser.ParserPlugin, files []sourceFile, idx *methodIndex, testFQNs map[string]struct{}) []Pair {
	seen := make(map[string]struct{})
	var pairs []Pair
	for _, f := range files {
		if !f.test {
			continue
		}
		ir, err := ad.Parse(ctx, f.path)
		if err != nil || ir == nil {
			continue
		}
		for _, c := range ir.Calls {
			if c.CallerFQN == "" || c.CalleeFQN == "" {
				continue
			}
			caller := normalizeTestFQN(c.CallerFQN, testFQNs)
			// 真值抽取按「仓内确有定义」消歧：callee 常带包/模块前缀
			// （store.FileStore.GetFileByPath），严格 resolve 会因短名多义而漏掉真实边。
			// 这里只影响真值集合，不改 §3.3 的严格分辨率口径。
			cands := idx.candidates(c.CalleeFQN)
			if len(cands) != 1 || cands[0] == caller {
				continue
			}
			prod := cands[0]
			key := prod + "\x00" + caller
			if _, dup := seen[key]; dup {
				continue
			}
			seen[key] = struct{}{}
			pairs = append(pairs, Pair{Production: prod, Test: caller})
		}
	}
	sort.Slice(pairs, func(i, j int) bool {
		if pairs[i].Production != pairs[j].Production {
			return pairs[i].Production < pairs[j].Production
		}
		return pairs[i].Test < pairs[j].Test
	})
	return pairs
}

// normalizeTestFQN 把调用边里的 caller（可能带包名前缀，如 demo.CalculatorTest.testAdd）
// 对齐到 store 中的方法 FQN（CalculatorTest.testAdd），避免因命名空间口径不同漏配。
func normalizeTestFQN(caller string, known map[string]struct{}) string {
	if _, ok := known[caller]; ok {
		return caller
	}
	for i := 0; i < len(caller); i++ {
		if caller[i] != '.' {
			continue
		}
		if _, ok := known[caller[i+1:]]; ok {
			return caller[i+1:]
		}
	}
	return caller
}

// samplePairs 确定性等距采样，避免大仓真值规模失控。
func samplePairs(pairs []Pair, max int) ([]Pair, bool) {
	if len(pairs) <= max {
		return pairs, false
	}
	stride := (len(pairs) + max - 1) / max
	out := make([]Pair, 0, max)
	for i := 0; i < len(pairs) && len(out) < max; i += stride {
		out = append(out, pairs[i])
	}
	return out, true
}

// prepareEvaluation 返回「评估用真值」与「待评估生产方法」。
//
// 采样只用于缩小评估的生产方法集合，真值必须来自全部 pair：
// 否则同一生产方法未被采样到的真实测试会被计成 FP，同时拉低 P 与 R
// （基准口径缺陷，不是策略变差）。
func prepareEvaluation(all, sampled []Pair) (map[string]map[string]struct{}, []string) {
	truth := make(map[string]map[string]struct{})
	for _, p := range all {
		if truth[p.Production] == nil {
			truth[p.Production] = make(map[string]struct{})
		}
		truth[p.Production][p.Test] = struct{}{}
	}
	seen := make(map[string]struct{})
	prods := make([]string, 0, len(sampled))
	for _, p := range sampled {
		if _, ok := seen[p.Production]; ok {
			continue
		}
		seen[p.Production] = struct{}{}
		prods = append(prods, p.Production)
	}
	sort.Strings(prods)
	return truth, prods
}

// evaluate 对真值中的每个生产方法查询 FindTestLinks，计算微平均 P/R/F1。
// 返回 PRF、评估的生产方法数、预测链接总数（用于区分「策略无输出」与「FQN 口径不一致」）。
func evaluate(ctx context.Context, svc *service.Service, truth map[string]map[string]struct{}, prods []string, minConf int) (PRF, int, int) {
	var out PRF
	predicted := 0
	for _, prod := range prods {
		links, err := svc.FindTestLinks(ctx, prod, minConf)
		if err != nil {
			continue
		}
		predicted += len(links)
		pred := make(map[string]struct{}, len(links))
		for _, l := range links {
			pred[l.TestMethod] = struct{}{}
		}
		for t := range pred {
			if _, ok := truth[prod][t]; ok {
				out.TP++
			} else {
				out.FP++
			}
		}
		for t := range truth[prod] {
			if _, ok := pred[t]; !ok {
				out.FN++
			}
		}
	}
	if out.TP+out.FP > 0 {
		out.Precision = round4(float64(out.TP) / float64(out.TP+out.FP))
	}
	if out.TP+out.FN > 0 {
		out.Recall = round4(float64(out.TP) / float64(out.TP+out.FN))
	}
	if out.Precision+out.Recall > 0 {
		out.F1 = round4(2 * out.Precision * out.Recall / (out.Precision + out.Recall))
	}
	return out, len(prods), predicted
}

// newService 用生产组件（FileStore + tree-sitter + scanner）构建只读 Service。
func newService(ctx context.Context, root string, workers int) (*service.Service, func(), error) {
	st := store.NewStore("file")
	dir, err := os.MkdirTemp("", "codeschema-releasebench-*")
	if err != nil {
		return nil, nil, err
	}
	cleanup := func() {
		_ = st.Close()
		_ = os.RemoveAll(dir)
	}
	if err := st.Open(ctx, dir); err != nil {
		cleanup()
		return nil, nil, err
	}
	reg := parser.NewRegistry()
	reg.Register(adapter.NewTreeSitterAdapter())
	sc := scanner.NewScanner(st, reg, workers)
	if err := sc.ScanAll(ctx, root); err != nil {
		cleanup()
		return nil, nil, err
	}
	return service.NewService(st), cleanup, nil
}

func gitHead(repo string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, "git", "-C", repo, "rev-parse", "HEAD").Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

func round4(v float64) float64 {
	return float64(int64(v*10000+0.5)) / 10000
}

// GenerateJSON 输出稳定缩进的 JSON。
func GenerateJSON(r *Report) ([]byte, error) {
	return json.MarshalIndent(r, "", "  ")
}

// GenerateMarkdown 生成单仓可读报告。
func GenerateMarkdown(r *Report) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## 公开基准：%s（%s）\n\n", r.Repo, r.Language)
	fmt.Fprintf(&b, "- commit: `%s`\n", shortCommit(r.Commit))
	fmt.Fprintf(&b, "- 适配器: `%s`（%s）\n", r.CallGraph.Adapter, r.BuildVariant)
	fmt.Fprintf(&b, "- 文件: %d（生产 %d / 测试 %d）\n", r.Files, r.ProdFiles, r.TestFiles)
	fmt.Fprintf(&b, "- 方法: 生产 %d / 测试 %d\n", r.ProdMethods, r.TestMethods)
	fmt.Fprintf(&b, "- 真值 pair: %d（采样 %d，sampled=%v，评估生产方法 %d，预测链接 %d）\n\n",
		r.GroundTruthPairs, r.SampledPairs, r.Sampled, r.ProdsEvaluated, r.PredictedLinks)
	fmt.Fprintf(&b, "| 测试关联 | TP | FP | FN | Precision | Recall | F1 |\n")
	fmt.Fprintf(&b, "|---|---:|---:|---:|---:|---:|---:|\n")
	fmt.Fprintf(&b, "| 微平均 | %d | %d | %d | %.4f | %.4f | %.4f |\n\n",
		r.TestLink.TP, r.TestLink.FP, r.TestLink.FN,
		r.TestLink.Precision, r.TestLink.Recall, r.TestLink.F1)
	fmt.Fprintf(&b, "| 调用图 | calls | callers | callees | resolved | resolution | 仓内候选边 | 仓内已解析 | 仓内 resolution |\n")
	fmt.Fprintf(&b, "|---|---:|---:|---:|---:|---:|---:|---:|---:|\n")
	fmt.Fprintf(&b, "| %s | %d | %d | %d | %d | %.4f | %d | %d | %.4f |\n",
		r.BuildVariant, r.CallGraph.Calls, r.CallGraph.UniqueCallers,
		r.CallGraph.UniqueCallees, r.CallGraph.ResolvedCalls, r.CallGraph.ResolutionRate,
		r.CallGraph.RepoResolvable, r.CallGraph.RepoResolvableResolved, r.CallGraph.RepoResolutionRate)
	if r.Manual != nil {
		fmt.Fprintf(&b, "\n%s", manualMarkdown(r.Manual))
	}
	return b.String()
}

func shortCommit(commit string) string {
	if len(commit) > 12 {
		return commit[:12]
	}
	return commit
}

// WriteOutput 把 report.json / report.md 写入 outDir。
func WriteOutput(r *Report, outDir string) ([]string, error) {
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return nil, err
	}
	jsonPath := filepath.Join(outDir, "report.json")
	data, err := GenerateJSON(r)
	if err != nil {
		return nil, err
	}
	if err := os.WriteFile(jsonPath, data, 0o600); err != nil {
		return nil, err
	}
	mdPath := filepath.Join(outDir, "report.md")
	if err := os.WriteFile(mdPath, []byte(GenerateMarkdown(r)), 0o600); err != nil {
		return nil, err
	}
	return []string{jsonPath, mdPath}, nil
}
