// 报告生成：多仓库 benchmark 对比（Markdown / JSON）。
package benchmark

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
)

// BenchComparison 多仓库对比结果。
type BenchComparison struct {
	Results    []BenchResult `json:"results"`
	Baseline   string        `json:"baseline"`
	ConfigDesc string        `json:"config_desc"`
}

// GenerateComparisonMarkdown 生成 Markdown 对比表格。
// baseline 为基准仓库名称，用于计算相对百分比。
func GenerateComparisonMarkdown(results []BenchResult, baseline string) string {
	if len(results) == 0 {
		return "(no benchmark results)"
	}

	// 查找基线
	var base *BenchResult
	for i := range results {
		if results[i].RepoName == baseline {
			base = &results[i]
			break
		}
	}
	if base == nil {
		base = &results[0]
	}

	var b strings.Builder
	b.WriteString("## 多仓库 Benchmark 对比\n\n")
	b.WriteString("| 指标 |")
	for _, r := range results {
		b.WriteString(fmt.Sprintf(" %s |", r.RepoName))
	}
	b.WriteString("\n|")
	b.WriteString(strings.Repeat(" --- |", len(results)))
	b.WriteString("\n")

	type metricRow struct {
		name   string
		values []string
	}
	rows := []metricRow{
		{"文件数", make([]string, len(results))},
		{"扫描耗时 (ms)", make([]string, len(results))},
		{"索引构建 (ms)", make([]string, len(results))},
		{"堆内存 (MB)", make([]string, len(results))},
		{"P50 延迟 (ms)", make([]string, len(results))},
		{"P95 延迟 (ms)", make([]string, len(results))},
		{"P99 延迟 (ms)", make([]string, len(results))},
		{"平均延迟 (ms)", make([]string, len(results))},
	}

	for i, r := range results {
		rows[0].values[i] = fmt.Sprintf("%d", r.FileCount)
		rows[1].values[i] = fmt.Sprintf("%d", r.ScanTimeMs)
		rows[2].values[i] = fmt.Sprintf("%d", r.IndexTimeMs)
		rows[3].values[i] = fmt.Sprintf("%.1f", r.HeapMB)
		rows[4].values[i] = fmt.Sprintf("%.2f", r.SearchP50Ms)
		rows[5].values[i] = fmt.Sprintf("%.2f", r.SearchP95Ms)
		rows[6].values[i] = fmt.Sprintf("%.2f", r.SearchP99Ms)
		rows[7].values[i] = fmt.Sprintf("%.2f", r.SearchAvgMs)
	}

	for _, row := range rows {
		b.WriteString(fmt.Sprintf("| %s |", row.name))
		for _, v := range row.values {
			b.WriteString(fmt.Sprintf(" %s |", v))
		}
		b.WriteString("\n")
	}

	// 相对性能对比（vs baseline）
	if len(results) > 1 {
		b.WriteString("\n### 相对性能（vs " + baseline + "）\n\n")
		b.WriteString("| 指标 |")
		for _, r := range results {
			b.WriteString(fmt.Sprintf(" %s |", r.RepoName))
		}
		b.WriteString("\n|")
		b.WriteString(strings.Repeat(" --- |", len(results)))
		b.WriteString("\n")

		type ratioRow struct {
			name   string
			values []string
		}
		ratioRows := []ratioRow{
			{"扫描耗时", make([]string, len(results))},
			{"索引构建", make([]string, len(results))},
			{"堆内存", make([]string, len(results))},
			{"P95 延迟", make([]string, len(results))},
		}

		for i, r := range results {
			ratioRows[0].values[i] = pctStr(r.ScanTimeMs, base.ScanTimeMs)
			ratioRows[1].values[i] = pctStr(r.IndexTimeMs, base.IndexTimeMs)
			ratioRows[2].values[i] = pctStr(int64(r.HeapMB*100), int64(base.HeapMB*100))
			ratioRows[3].values[i] = pctStr(int64(r.SearchP95Ms*100), int64(base.SearchP95Ms*100))
		}

		for _, row := range ratioRows {
			b.WriteString(fmt.Sprintf("| %s |", row.name))
			for _, v := range row.values {
				b.WriteString(fmt.Sprintf(" %s |", v))
			}
			b.WriteString("\n")
		}
	}

	return b.String()
}

// GenerateComparisonJSON 生成 JSON 格式的对比报告。
func GenerateComparisonJSON(results []BenchResult, baseline, configDesc string) ([]byte, error) {
	comp := BenchComparison{
		Results:    results,
		Baseline:   baseline,
		ConfigDesc: configDesc,
	}
	return json.MarshalIndent(comp, "", "  ")
}

// SortBenchResults 按仓库名排序。
func SortBenchResults(results []BenchResult) {
	sort.Slice(results, func(i, j int) bool {
		return results[i].RepoName < results[j].RepoName
	})
}

// pctStr 计算相对百分比字符串。
func pctStr(val, base int64) string {
	if base == 0 {
		return "N/A"
	}
	pct := float64(val) / float64(base) * 100
	sign := ""
	if pct > 100 {
		sign = "+"
	}
	return fmt.Sprintf("%s%.1f%%", sign, pct)
}
