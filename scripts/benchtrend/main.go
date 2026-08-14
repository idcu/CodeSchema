// benchtrend 读取 build/treesitter-bench-history.jsonl 生成 HTML 精度趋势报告。
//
// 用法：go run ./scripts/benchtrend（或 make bench-trend）
// 产出：build/treesitter-bench-trend.html（含 simple/complex/overall 的 Precision/Recall 趋势表）。
//
// 纯标准库实现：JSONL 解析 + HTML 生成，无第三方依赖。
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"html"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// historyPoint 与 internal/adapterbench 的 benchHistoryPoint 字段一致。
type historyPoint struct {
	GeneratedAt string  `json:"generated_at"`
	GitSHA      string  `json:"git_sha,omitempty"`
	SimpleP     float64 `json:"simple_precision"`
	SimpleR     float64 `json:"simple_recall"`
	ComplexP    float64 `json:"complex_precision"`
	ComplexR    float64 `json:"complex_recall"`
	OverallP    float64 `json:"overall_precision"`
	OverallR    float64 `json:"overall_recall"`
	TP          int     `json:"true_positive"`
	FP          int     `json:"false_positive"`
	FN          int     `json:"false_negative"`
}

func main() {
	root := repoRoot()
	historyPath := filepath.Join(root, "build", "treesitter-bench-history.jsonl")
	points, err := loadHistory(historyPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "error: %v\n", err)
		os.Exit(1)
	}
	if len(points) == 0 {
		fmt.Fprintln(os.Stderr, "no history points found (run the bench first: go test -run TestTreeSitterCallGraphBench ./internal/adapterbench)")
		os.Exit(1)
	}

	html := renderReport(points)
	outPath := filepath.Join(root, "build", "treesitter-bench-trend.html")
	if err := os.WriteFile(outPath, []byte(html), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "write %s: %v\n", outPath, err)
		os.Exit(1)
	}
	fmt.Printf("trend report written: %s (%d history points)\n", outPath, len(points))
}

// loadHistory 解析 JSONL（跳过 # 注释行）。
func loadHistory(path string) ([]historyPoint, error) {
	f, err := os.Open(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil, fmt.Errorf("history file not found: %s", path)
		}
		return nil, err
	}
	defer f.Close()

	var points []historyPoint
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		line := strings.TrimSpace(sc.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		var p historyPoint
		if err := json.Unmarshal([]byte(line), &p); err != nil {
			return nil, fmt.Errorf("parse history line: %v", err)
		}
		points = append(points, p)
	}
	return points, sc.Err()
}

// renderReport 生成 HTML 报告（趋势表 + SVG 折线图 + 末次快照摘要）。
func renderReport(points []historyPoint) string {
	var b strings.Builder
	b.WriteString("<!DOCTYPE html>\n<html lang=\"zh\"><head><meta charset=\"utf-8\">")
	b.WriteString("<title>CodeSchema 调用图精度趋势</title><style>")
	b.WriteString("body{font-family:-apple-system,sans-serif;margin:2rem;color:#1f2328}")
	b.WriteString("h1{font-size:1.4rem}h2{font-size:1.1rem;margin-top:1.5rem}")
	b.WriteString("table{border-collapse:collapse;width:100%;margin-top:1rem}")
	b.WriteString("th,td{border:1px solid #d0d7de;padding:6px 10px;text-align:right;font-size:0.85rem}")
	b.WriteString("th{background:#f6f8fa}td:first-child,th:first-child{text-align:left}")
	b.WriteString(".ok{color:#1a7f37;font-weight:600}.warn{color:#9a6700;font-weight:600}")
	b.WriteString(".legend{font-size:0.8rem;color:#57606a;margin:0.5rem 0}")
	b.WriteString("</style></head><body>")
	b.WriteString("<h1>CodeSchema treesitter 调用图精度趋势</h1>")
	b.WriteString(fmt.Sprintf("<p>历史点数: %d ｜ 时间范围: %s ~ %s</p>",
		len(points), html.EscapeString(points[0].GeneratedAt), html.EscapeString(points[len(points)-1].GeneratedAt)))

	// SVG 折线图（Overall Precision/Recall 随历史点变化）
	b.WriteString("<h2>Overall 精度趋势</h2>")
	b.WriteString(renderLineChart(points))

	// 趋势表（新→旧）
	b.WriteString("<h2>明细</h2>")
	b.WriteString("<table><tr><th>时间</th><th>git</th><th>Simple P</th><th>Simple R</th><th>Complex P</th><th>Complex R</th><th>Overall P</th><th>Overall R</th><th>TP/FP/FN</th></tr>")
	for i := len(points) - 1; i >= 0; i-- {
		p := points[i]
		b.WriteString("<tr>")
		b.WriteString(fmt.Sprintf("<td>%s</td><td>%s</td>", html.EscapeString(shortTime(p.GeneratedAt)), html.EscapeString(shortSHA(p.GitSHA))))
		b.WriteString(fmt.Sprintf("<td>%.3f</td><td>%.3f</td><td>%.3f</td><td>%.3f</td><td>%.3f</td><td>%.3f</td>",
			p.SimpleP, p.SimpleR, p.ComplexP, p.ComplexR, p.OverallP, p.OverallR))
		b.WriteString(fmt.Sprintf("<td>%d/%d/%d</td>", p.TP, p.FP, p.FN))
		b.WriteString("</tr>")
	}
	b.WriteString("</table>")

	// 末次快照摘要
	last := points[len(points)-1]
	b.WriteString("<h2>末次快照</h2><p>")
	b.WriteString(fmt.Sprintf("Overall Precision=%.3f Recall=%.3f（TP=%d FP=%d FN=%d）; ", last.OverallP, last.OverallR, last.TP, last.FP, last.FN))
	if last.OverallP == 1 && last.OverallR == 1 {
		b.WriteString("<span class=\"ok\">全满分 ✓</span>")
	} else if last.OverallR < 0.9 {
		b.WriteString("<span class=\"warn\">Recall 偏低，建议检查正则/真语法树回归</span>")
	} else {
		b.WriteString("<span class=\"ok\">精度正常</span>")
	}
	b.WriteString("</p>")
	b.WriteString("<p style=\"color:#57606a;font-size:0.8rem\">生成时间: " + html.EscapeString(time.Now().Format(time.RFC3339)) + "</p>")
	b.WriteString("</body></html>")
	return b.String()
}

// renderLineChart 生成 Overall Precision/Recall 的 SVG 折线图（新→旧从左到右）。
// 纯手写 SVG polyline，无 JS/外部依赖。
func renderLineChart(points []historyPoint) string {
	const (
		w    = 720
		h    = 200
		padL = 40
		padB = 30
		padT = 10
		padR = 20
	)
	plotW := w - padL - padR
	plotH := h - padT - padB
	n := len(points)
	if n < 2 {
		return "<p class=\"legend\">历史点数不足 2，无法绘制折线图。</p>"
	}

	// x 映射（新→旧：index 0 是最新）
	x := func(i int) int { return padL + i*(plotW/(n-1)) }
	// y 映射（0..1 → 底部到顶部）
	y := func(v float64) int { return padT + plotH - int(v*float64(plotH)) }

	// 生成两个 polyline（Precision 红 / Recall 蓝）
	var polyP, polyR []string
	for i := range points {
		polyP = append(polyP, fmt.Sprintf("%d,%d", x(i), y(points[i].OverallP)))
		polyR = append(polyR, fmt.Sprintf("%d,%d", x(i), y(points[i].OverallR)))
	}

	var b strings.Builder
	b.WriteString(fmt.Sprintf("<svg viewBox=\"0 0 %d %d\" xmlns=\"http://www.w3.org/2000/svg\" style=\"max-width:%dpx;border:1px solid #d0d7de;border-radius:6px\">", w, h, w))
	// 网格线 0 / 0.5 / 1.0
	for _, v := range []float64{0, 0.5, 1.0} {
		yy := y(v)
		b.WriteString(fmt.Sprintf("<line x1=\"%d\" y1=\"%d\" x2=\"%d\" y2=\"%d\" stroke=\"#eaeef2\" stroke-width=\"1\"/>", padL, yy, w-padR, yy))
		b.WriteString(fmt.Sprintf("<text x=\"%d\" y=\"%d\" font-size=\"10\" fill=\"#57606a\">%.1f</text>", padL-4, yy+3, v))
	}
	// 折线
	b.WriteString(fmt.Sprintf("<polyline points=\"%s\" fill=\"none\" stroke=\"#cf222e\" stroke-width=\"2\"/>", strings.Join(polyP, " ")))
	b.WriteString(fmt.Sprintf("<polyline points=\"%s\" fill=\"none\" stroke=\"#0969da\" stroke-width=\"2\"/>", strings.Join(polyR, " ")))
	// 数据点
	for i := range points {
		b.WriteString(fmt.Sprintf("<circle cx=\"%d\" cy=\"%d\" r=\"3\" fill=\"#cf222e\"/>", x(i), y(points[i].OverallP)))
		b.WriteString(fmt.Sprintf("<circle cx=\"%d\" cy=\"%d\" r=\"3\" fill=\"#0969da\"/>", x(i), y(points[i].OverallR)))
	}
	b.WriteString("</svg>")
	b.WriteString("<p class=\"legend\"><span style=\"color:#cf222e\">■</span> Overall Precision ｜ <span style=\"color:#0969da\">■</span> Overall Recall（新→旧）</p>")
	return b.String()
}

func shortTime(iso string) string {
	if len(iso) >= 19 {
		return iso[:19]
	}
	return iso
}

func shortSHA(sha string) string {
	if len(sha) <= 12 {
		return sha
	}
	return sha[len(sha)-7:]
}

// repoRoot 向上查找 go.mod 定位仓库根（与测试包一致）。
func repoRoot() string {
	dir, _ := os.Getwd()
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "."
		}
		dir = parent
	}
}
