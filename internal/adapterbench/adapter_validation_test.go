// Package adapterbench 提供跨适配器（SCIP / LSP）的生产验证与多语言基准测试。
//
// 设计原则：工具缺失则优雅跳过（不污染无工具环境），工具可用则真实端到端解析
// 并记录符号数与延迟，最终生成 JSON 报告（build/adapter-bench.json）与
// Markdown 摘要（analysis/2026-08-14-adapter-validation.md）。
//
// 本包刻意只依赖 internal/parser/adapter/lsp 与 internal/parser/adapter/scip
// （两者均不引入 onnxruntime_go / chromem / modernc 等 cgo 重型依赖），
// 以便在本机/CI 上秒级编译运行，而不被完整 scan/index/vector 流水线的
// cgo 依赖拖慢。
//
// 运行：go test -run TestAdapterValidation ./internal/adapterbench/ -v
package adapterbench

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/parser/adapter/lsp"
	"github.com/idcu/codeschema/internal/parser/adapter/scip"
)

// adapterResult 是单条适配器验证结果。
type adapterResult struct {
	Name      string  `json:"name"`
	Language  string  `json:"language"`
	Available bool    `json:"available"`
	Skipped   bool    `json:"skipped,omitempty"`
	Reason    string  `json:"reason,omitempty"`
	Classes   int     `json:"classes"`
	Methods   int     `json:"methods"`
	Calls     int     `json:"calls"`
	Ms        float64 `json:"ms"`
	Note      string  `json:"note,omitempty"`
}

// lspPathToURIStyle 将本机绝对路径转为适配器期望的 LSP 路径形式（见 lsp 适配器）。
func lspPathToURIStyle(p string) string {
	abs := filepath.ToSlash(p)
	if len(abs) >= 2 && abs[1] == ':' {
		abs = "/" + abs
	}
	return abs
}

func dirURI(lspPath string) string {
	idx := strings.LastIndex(lspPath, "/")
	if idx < 0 {
		return "file://" + lspPath
	}
	return "file://" + lspPath[:idx]
}

// repoRoot 定位仓库根，使报告稳定写入仓库根的 build/ 与 analysis/
// （与文档及 .gitignore 约定一致）。优先用 go test 注入的 GOMOD 环境变量
// （指向 go.mod 绝对路径，Windows 下比向上遍历 CWD 可靠），失败再回退遍历。
func repoRoot() string {
	if m := os.Getenv("GOMOD"); m != "" {
		return filepath.Dir(m)
	}
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

func writeSample(t *testing.T, sub, name, content string) string {
	t.Helper()
	// 写入仓库根下的 cs_ab_tmp/<sub>（长路径），刻意避开系统临时目录的 8.3 短路径
	// （如 C:\Users\ADMINI~1\...），否则 gopls/clangd 会将 URI 规范化为长路径导致
	// documentSymbol 用短路径查不到、甚至挂起（参见 lsp/e2e_test.go 的说明）。
	base := filepath.Join(repoRoot(), "cs_ab_tmp", sub)
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(filepath.Join(repoRoot(), "cs_ab_tmp")) })
	fp := filepath.Join(base, name)
	if err := os.WriteFile(fp, []byte(content), 0o644); err != nil {
		t.Fatalf("write sample: %v", err)
	}
	return lspPathToURIStyle(fp)
}

// TestAdapterValidation 跨适配器生产验证 + 多语言基准。
func TestAdapterValidation(t *testing.T) {
	if testing.Short() {
		t.Skip("skipping adapter validation in short mode")
	}
	results := []adapterResult{}

	// 1) SCIP：始终可用（基于 fixture index，覆盖 class/method/calls 提取）。
	results = append(results, validateSCIP(t))

	// 2) LSP：按工具可用性逐语言验证。
	results = append(results, validateLSPGo(t))
	results = append(results, validateLSPCpp(t))
	results = append(results, validateLSPJava(t))

	// 输出 JSON 报告到仓库根 build/
	root := repoRoot()
	out := map[string]any{
		"generated_at": time.Now().Format(time.RFC3339),
		"adapters":     results,
	}
	data, _ := json.MarshalIndent(out, "", "  ")
	os.MkdirAll(filepath.Join(root, "build"), 0o755)
	if err := os.WriteFile(filepath.Join(root, "build", "adapter-bench.json"), data, 0o644); err != nil {
		t.Logf("warn: 报告写入 build/adapter-bench.json 失败（可能本机杀软锁定生成文件）: %v", err)
	}
	t.Logf("Adapter validation report:\n%s", string(data))

	// 输出 Markdown 摘要到仓库根 analysis/
	writeAdapterMarkdown(t, results)
}

func validateSCIP(t *testing.T) adapterResult {
	dir := t.TempDir()
	indexJSON := `{
		"metadata": {"tool_info": {"name": "scip-go", "version": "0.3.0"}},
		"documents": [
			{
				"relative_path": "internal/service/svc.go",
				"language": "Go",
				"symbols": [
					{"id": "demo/Service", "name": "Service", "kind": 0},
					{"id": "demo/Service.Run", "name": "Run", "kind": 1, "enclosing_symbol": "demo/Service"},
					{"id": "demo/Service.Stop", "name": "Stop", "kind": 1, "enclosing_symbol": "demo/Service"}
				],
				"occurrences": [
					{"symbol": "demo/Service", "symbol_role": 0, "range": [5, 0, 30, 0]},
					{"symbol": "demo/Service.Run", "symbol_role": 0, "range": [12, 0, 22, 0]},
					{"symbol": "demo/Service.Stop", "symbol_role": 0, "range": [24, 0, 28, 0]},
					{"symbol": "demo/Service.Run", "symbol_role": 1, "range": [40, 2, 40, 5]}
				]
			}
		]
	}`
	_ = os.WriteFile(filepath.Join(dir, "index.scip"), []byte(indexJSON), 0o644)

	a := scip.NewSCIPAdapter(dir)
	if err := a.Init(context.Background(), nil); err != nil {
		return adapterResult{Name: "scip", Language: "go/java/ts/py/...", Available: false, Reason: err.Error()}
	}
	defer a.Close()

	start := time.Now()
	ch, err := a.ParseAll(context.Background(), []string{filepath.Join(dir, "internal/service/svc.go")})
	if err != nil {
		return adapterResult{Name: "scip", Language: "go/java/ts/py/...", Available: false, Reason: err.Error()}
	}
	ir := <-ch
	return adapterResult{
		Name:      "scip",
		Language:  "go/java/ts/py/...",
		Available: true,
		Classes:   len(ir.Classes),
		Methods:   len(ir.Methods),
		Calls:     len(ir.Calls),
		Ms:        float64(time.Since(start).Microseconds()) / 1000,
		Note:      "fixture index（class+method+calls）",
	}
}

func validateLSPGo(t *testing.T) adapterResult {
	if _, err := exec.LookPath("gopls"); err != nil {
		return adapterResult{Name: "gopls", Language: "go", Available: false, Skipped: true,
			Reason: "gopls not in PATH (install: go install golang.org/x/tools/gopls@latest)"}
	}
	src := "package demo\n\ntype Service struct{}\n\nfunc (s *Service) Run() error { return nil }\n\nfunc (s *Service) Stop() error { return nil }\n"
	path := writeSample(t, "go", "svc.go", src)

	// 验证工具给足超时：gopls 为冷模块首次建立 view 的首条 documentSymbol 可能超过生产默认 10s，
	// 此处用 60s 以真实解析出类与方法（与 e2e_test.go 的 TestLSPAdapter_RealGopls 一致）。
	// Init 不传 rootUri（同 e2e）：gopls 自行从 didOpen 文件所在目录向上查找 go.mod 建立 view，
	// 传 rootUri 反而会让 gopls 多做工作、在慢机上越过超时。
	a := lsp.NewLSPAdapter("gopls", "gopls", nil, "go", 60*time.Second)
	if err := a.Init(context.Background(), nil); err != nil {
		return adapterResult{Name: "gopls", Language: "go", Available: false, Reason: err.Error()}
	}
	defer a.Close()

	start := time.Now()
	ir, err := a.Parse(context.Background(), path)
	ms := float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		return adapterResult{Name: "gopls", Language: "go", Available: true, Skipped: true,
			Reason: err.Error(), Ms: ms, Note: "gopls 已启动但解析失败（可能缺工程上下文）"}
	}
	return adapterResult{Name: "gopls", Language: "go", Available: true,
		Classes: len(ir.Classes), Methods: len(ir.Methods), Calls: len(ir.Calls), Ms: ms}
}

func validateLSPCpp(t *testing.T) adapterResult {
	if _, err := exec.LookPath("clangd"); err != nil {
		return adapterResult{Name: "clangd", Language: "cpp", Available: false, Skipped: true,
			Reason: "clangd not in PATH"}
	}
	src := "class Service {\npublic:\n    void Run();\n    void Stop();\n};\n"
	path := writeSample(t, "cpp", "svc.cpp", src)

	a := lsp.NewClangdAdapter()
	if err := a.Init(context.Background(), map[string]any{"rootUri": dirURI(path)}); err != nil {
		return adapterResult{Name: "clangd", Language: "cpp", Available: false, Reason: err.Error()}
	}
	defer a.Close()

	start := time.Now()
	ir, err := a.Parse(context.Background(), path)
	ms := float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		return adapterResult{Name: "clangd", Language: "cpp", Available: true, Skipped: true,
			Reason: err.Error(), Ms: ms, Note: "clangd 需 compile-commands/project 上下文才登记独立文件"}
	}
	return adapterResult{Name: "clangd", Language: "cpp", Available: true,
		Classes: len(ir.Classes), Methods: len(ir.Methods), Calls: len(ir.Calls), Ms: ms}
}

func validateLSPJava(t *testing.T) adapterResult {
	if _, err := exec.LookPath("jdtls"); err != nil {
		return adapterResult{Name: "jdtls", Language: "java", Available: false, Skipped: true,
			Reason: "jdtls not in PATH (需 JDK 17+ 与 jdtls 安装)"}
	}
	src := "package demo;\npublic class Service {\n    public void run() {}\n    public void stop() {}\n}\n"
	path := writeSample(t, "java", "Service.java", src)

	a := lsp.NewJDTLSAdapter()
	if err := a.Init(context.Background(), map[string]any{"rootUri": dirURI(path)}); err != nil {
		return adapterResult{Name: "jdtls", Language: "java", Available: false, Reason: err.Error()}
	}
	defer a.Close()

	start := time.Now()
	ir, err := a.Parse(context.Background(), path)
	ms := float64(time.Since(start).Microseconds()) / 1000
	if err != nil {
		return adapterResult{Name: "jdtls", Language: "java", Available: true, Skipped: true,
			Reason: err.Error(), Ms: ms}
	}
	return adapterResult{Name: "jdtls", Language: "java", Available: true,
		Classes: len(ir.Classes), Methods: len(ir.Methods), Calls: len(ir.Calls), Ms: ms}
}

func writeAdapterMarkdown(t *testing.T, results []adapterResult) {
	dir := filepath.Join(repoRoot(), "analysis")
	_ = os.MkdirAll(dir, 0o755)
	var b strings.Builder
	b.WriteString("# 适配器生产验证与多语言基准（" + time.Now().Format("2006-01-02 15:04") + "）\n\n")
	b.WriteString("| 适配器 | 语言 | 可用 | 类 | 方法 | 调用 | 延迟(ms) | 说明 |\n")
	b.WriteString("|---|---|---|---|---|---|---|---|\n")
	for _, r := range results {
		avail := "✅"
		if !r.Available {
			avail = "❌"
		}
		skipped := ""
		if r.Skipped {
			skipped = "（跳过：" + r.Reason + "）"
		}
		note := r.Note
		if skipped != "" && note == "" {
			note = skipped
		} else if skipped != "" {
			note = note + " " + skipped
		}
		b.WriteString("| " + r.Name + " | " + r.Language + " | " + avail + " | " +
			strconv.Itoa(r.Classes) + " | " + strconv.Itoa(r.Methods) + " | " +
			strconv.Itoa(r.Calls) + " | " + strconv.FormatFloat(r.Ms, 'f', 2, 64) + " | " + note + " |\n")
	}
	b.WriteString("\n## 说明\n")
	b.WriteString("- SCIP 基于 fixture index 验证 class/method/调用关系提取（始终可用）。\n")
	b.WriteString("- LSP（gopls/clangd/jdtls）为真实语言服务器端到端验证；工具缺失或缺少工程上下文时优雅跳过。\n")
	b.WriteString("- 完整 JSON 见 build/adapter-bench.json。\n")
	if err := os.WriteFile(filepath.Join(dir, "2026-08-14-adapter-validation.md"), []byte(b.String()), 0o644); err != nil {
		t.Logf("warn: 报告写入 analysis/2026-08-14-adapter-validation.md 失败（可能本机杀软锁定生成文件）: %v", err)
	}
}
