package releasebench

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/idcu/codeschema/internal/parser"
	adapter "github.com/idcu/codeschema/internal/parser/adapter/treesitter"
	"github.com/idcu/codeschema/internal/service"
)

// ManualEntry 一条人工标注：生产方法 ← 标注范围内验证它的测试方法。
//
// TestFiles 是标注的闭合范围（仓库相对路径）；Tests 由人工逐个阅读测试体得出，
// 不来自 IR 调用边。
type ManualEntry struct {
	Production string   `json:"production"`
	TestFiles  []string `json:"test_files"`
	Tests      []string `json:"tests"`
}

// ManualRepo 单个仓库的人工标注集合。
type ManualRepo struct {
	Language string        `json:"language"`
	Entries  []ManualEntry `json:"entries"`
}

// ManualDataset 人工真值数据集（仓库名 → 标注）。
type ManualDataset struct {
	Version int                   `json:"version"`
	Note    string                `json:"note"`
	Repos   map[string]ManualRepo `json:"repos"`
}

// ManualEntryResult 单条生产方法的人工真值评估结果。
type ManualEntryResult struct {
	Production string   `json:"production"`
	Truth      []string `json:"truth"`
	Predicted  []string `json:"predicted"`
	TP         int      `json:"tp"`
	FP         int      `json:"fp"`
	FN         int      `json:"fn"`
	Precision  float64  `json:"precision"`
	Recall     float64  `json:"recall"`
	F1         float64  `json:"f1"`
}

// ManualResult 人工真值独立评估结果。
//
// 与 Report.TestLink 的口径差异：
//   - 真值来源：人工阅读测试方法体（本结果）vs IR 直接调用边（自动口径）；
//   - 评估范围：仅标注的测试文件（闭合世界）vs 仓库全部测试文件；
//   - callee 消歧：人工按语义归属 vs methodIndex 全名/唯一短名。
type ManualResult struct {
	Dataset      string              `json:"dataset"`
	Repo         string              `json:"repo"`
	Commit       string              `json:"commit"`
	Language     string              `json:"language"`
	BuildVariant string              `json:"build_variant"`
	TestFiles    []string            `json:"test_files"`
	InScopeTests int                 `json:"in_scope_tests"`
	TruthPairs   int                 `json:"truth_pairs"`
	Predicted    int                 `json:"predicted_in_scope"`
	Entries      []ManualEntryResult `json:"entries"`
	Micro        PRF                 `json:"micro"`
}

// LoadManualDataset 读取人工真值数据集。
func LoadManualDataset(path string) (*ManualDataset, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var ds ManualDataset
	if err := json.Unmarshal(data, &ds); err != nil {
		return nil, fmt.Errorf("parse manual truth %s: %w", path, err)
	}
	if len(ds.Repos) == 0 {
		return nil, fmt.Errorf("manual truth %s: no repos", path)
	}
	return &ds, nil
}

// evaluateManual 在指定仓库上运行人工真值评估。
//
// inScope 由标注测试文件的 IR 方法集合构成：它只决定「哪些预测属于标注范围」，
// 不参与真值构造；真值 Tests 完全来自数据集。
func evaluateManual(ctx context.Context, svc *service.Service, repoPath, datasetPath string, ad parser.ParserPlugin, idx *methodIndex, minConf int) (*ManualResult, error) {
	ds, err := LoadManualDataset(datasetPath)
	if err != nil {
		return nil, err
	}
	key := filepath.Base(filepath.Clean(repoPath))
	repo, ok := ds.Repos[key]
	if !ok {
		keys := make([]string, 0, len(ds.Repos))
		for k := range ds.Repos {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		return nil, fmt.Errorf("manual truth: repo %q not in dataset (have %v)", key, keys)
	}

	files := uniqueStrings(collectTestFiles(repo.Entries))
	inScope := make(map[string]struct{})
	for _, rel := range files {
		ir, perr := ad.Parse(ctx, filepath.Join(repoPath, rel))
		if perr != nil || ir == nil {
			return nil, fmt.Errorf("manual truth: parse %s: %w", rel, perr)
		}
		for _, m := range ir.Methods {
			inScope[joinFQN(m.ClassFQN, m.Name)] = struct{}{}
		}
	}

	// 数据集自检：真值测试必须在标注文件内，生产方法必须在方法索引内。
	// 否则标注与代码漂移会静默变成 FN/全零，无法区分「策略漏召」与「数据写错」。
	for _, e := range repo.Entries {
		if idx.count[e.Production] == 0 {
			return nil, fmt.Errorf("manual truth: production %q not found in %s", e.Production, key)
		}
		for _, t := range e.Tests {
			if _, ok := inScope[t]; !ok {
				return nil, fmt.Errorf("manual truth: test %q not in annotated test files of %q", t, e.Production)
			}
		}
	}

	query := func(prod string) ([]string, error) {
		links, lerr := svc.FindTestLinks(ctx, prod, minConf)
		if lerr != nil {
			return nil, lerr
		}
		out := make([]string, 0, len(links))
		for _, l := range links {
			out = append(out, l.TestMethod)
		}
		return out, nil
	}

	entries, micro, predicted, err := scoreManual(repo.Entries, inScope, query)
	if err != nil {
		return nil, err
	}
	truth := 0
	for _, e := range repo.Entries {
		truth += len(e.Tests)
	}
	return &ManualResult{
		Dataset:      datasetPath,
		Repo:         key,
		Commit:       gitHead(repoPath),
		Language:     repo.Language,
		BuildVariant: adapter.BuildVariant(),
		TestFiles:    files,
		InScopeTests: len(inScope),
		TruthPairs:   truth,
		Predicted:    predicted,
		Entries:      entries,
		Micro:        micro,
	}, nil
}

// scoreManual 按「标注范围闭合」计算逐条与微平均 P/R/F1。
//
// 范围外的预测被忽略（不计 FP）：人工只对 listed test_files 做了完整标注，
// 其它文件是否关联未标注，不应计入分母。
func scoreManual(entries []ManualEntry, inScope map[string]struct{}, query func(string) ([]string, error)) ([]ManualEntryResult, PRF, int, error) {
	var micro PRF
	predicted := 0
	results := make([]ManualEntryResult, 0, len(entries))
	for _, e := range entries {
		links, err := query(e.Production)
		if err != nil {
			return nil, PRF{}, 0, fmt.Errorf("query %s: %w", e.Production, err)
		}
		predSet := make(map[string]struct{}, len(links))
		for _, t := range links {
			if _, ok := inScope[t]; ok {
				predSet[t] = struct{}{}
			}
		}
		truthSet := make(map[string]struct{}, len(e.Tests))
		for _, t := range e.Tests {
			truthSet[t] = struct{}{}
		}
		var r ManualEntryResult
		r.Production = e.Production
		for t := range predSet {
			r.Predicted = append(r.Predicted, t)
			if _, ok := truthSet[t]; ok {
				r.TP++
			} else {
				r.FP++
			}
		}
		for t := range truthSet {
			if _, ok := predSet[t]; !ok {
				r.FN++
			}
		}
		r.Truth = append(r.Truth, e.Tests...)
		sort.Strings(r.Predicted)
		sort.Strings(r.Truth)
		r.Precision = ratio(r.TP, r.TP+r.FP)
		r.Recall = ratio(r.TP, r.TP+r.FN)
		r.F1 = f1(r.Precision, r.Recall)

		micro.TP += r.TP
		micro.FP += r.FP
		micro.FN += r.FN
		predicted += len(predSet)
		results = append(results, r)
	}
	micro.Precision = ratio(micro.TP, micro.TP+micro.FP)
	micro.Recall = ratio(micro.TP, micro.TP+micro.FN)
	micro.F1 = f1(micro.Precision, micro.Recall)
	return results, micro, predicted, nil
}

func ratio(num, den int) float64 {
	if den == 0 {
		return 0
	}
	return round4(float64(num) / float64(den))
}

func f1(p, r float64) float64 {
	if p+r == 0 {
		return 0
	}
	return round4(2 * p * r / (p + r))
}

func collectTestFiles(entries []ManualEntry) []string {
	var out []string
	for _, e := range entries {
		out = append(out, e.TestFiles...)
	}
	return out
}

func uniqueStrings(in []string) []string {
	seen := make(map[string]struct{}, len(in))
	out := make([]string, 0, len(in))
	for _, s := range in {
		if s == "" {
			continue
		}
		if _, ok := seen[s]; ok {
			continue
		}
		seen[s] = struct{}{}
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}

// manualMarkdown 生成人工真值评估段落（与自动口径并排展示）。
func manualMarkdown(m *ManualResult) string {
	var b strings.Builder
	fmt.Fprintf(&b, "## 人工真值（独立来源）：%s（%s）\n\n", m.Repo, m.Language)
	fmt.Fprintf(&b, "- 数据集: `%s`\n", m.Dataset)
	fmt.Fprintf(&b, "- commit: `%s`（%s）\n", shortCommit(m.Commit), m.BuildVariant)
	fmt.Fprintf(&b, "- 标注范围: %s（范围内方法 %d）\n", strings.Join(m.TestFiles, ", "), m.InScopeTests)
	fmt.Fprintf(&b, "- 真值 pair: %d（人工）；范围内预测 %d\n\n", m.TruthPairs, m.Predicted)
	fmt.Fprintf(&b, "| 生产方法 | TP | FP | FN | Precision | Recall | F1 |\n")
	fmt.Fprintf(&b, "|---|---:|---:|---:|---:|---:|---:|\n")
	for _, e := range m.Entries {
		fmt.Fprintf(&b, "| `%s` | %d | %d | %d | %.4f | %.4f | %.4f |\n",
			e.Production, e.TP, e.FP, e.FN, e.Precision, e.Recall, e.F1)
	}
	fmt.Fprintf(&b, "| **微平均** | %d | %d | %d | %.4f | %.4f | %.4f |\n\n",
		m.Micro.TP, m.Micro.FP, m.Micro.FN, m.Micro.Precision, m.Micro.Recall, m.Micro.F1)
	return b.String()
}
