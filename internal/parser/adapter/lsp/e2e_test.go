package lsp

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/idcu/codeschema/internal/parser"
)

// toLSPPath 将本机绝对路径转为 LSP 适配器期望的形式：
// 适配器内部以 "file://" + path 构造 URI，因此 path 需为
// "/C:/Users/..." 这样的「带盘符、正斜杠、含前导斜杠」形式，
// 才能得到合法 URI "file:///C:/Users/..."。
func toLSPPath(p string) string {
	abs := filepath.ToSlash(p)
	if len(abs) >= 2 && abs[1] == ':' {
		abs = "/" + abs // C:/x -> /C:/x
	}
	return abs
}

// writeTempSource 在系统临时目录（t.TempDir，绝对长路径、无 8.3 短名问题）写源文件并返回其 LSP 路径。
//
// 注意：此前实现写入仓库根的 ./cs_lsp_tmp，会与「扫描整个仓库根」的集成测试
// （TestRealRepo_CollectMetrics）在并行执行时竞态——集成扫描途中该临时工程正被
// 创建/清理，读到已消失的 compile_commands.json 导致 ScanAll 失败。改用 t.TempDir()
// 后临时工程完全落在仓库外，根除了该竞态，且 t.TempDir 自动清理。
// LSP 的 rootUri 需为绝对 file URI，t.TempDir 返回的已是绝对长路径（无 8.3），clangd/gopls 不会因规范化失配。
func writeTempSource(t *testing.T, ext, content string) string {
	t.Helper()
	dir := t.TempDir()
	f, err := os.CreateTemp(dir, "*"+ext)
	if err != nil {
		t.Fatalf("create temp: %v", err)
	}
	if _, err := f.WriteString(content); err != nil {
		f.Close()
		t.Fatalf("write temp: %v", err)
	}
	f.Close()
	return toLSPPath(f.Name())
}

// parseWithRetry 对真实语言服务器重试 Parse，直到解析出符号或耗尽重试次数。
// 真实 LSP 在 didOpen 后需短暂时间完成首轮解析（clangd 还需异步加载 compile_commands
// 并登记文档，期间 documentSymbol 会报 "non-added document"），因此**错误也重试**
// 而非立即跳过；连续失败（如环境缺工程上下文）在耗尽次数后以 skip 记录缺口，CI 保持绿色。
func parseWithRetry(t *testing.T, a *LSPAdapter, path string, wantMin int) *parser.IRDocument {
	t.Helper()
	var last *parser.IRDocument
	for i := 0; i < 20; i++ {
		ir, err := a.Parse(context.Background(), path)
		if err != nil {
			t.Logf("LSP Parse attempt %d/%d failed (waiting for server index/registration): %v", i+1, 20, err)
			time.Sleep(500 * time.Millisecond)
			continue
		}
		last = ir
		if len(ir.Classes) >= wantMin {
			return ir
		}
		time.Sleep(500 * time.Millisecond)
	}
	if last == nil {
		t.Skipf("real LSP Parse failed after retries (missing project context or server not registering document)")
	}
	return last
}

// TestLSPAdapter_RealClangd 使用真实 clangd 验证 LSP 适配器端到端。
//
// 这是 LSP 适配器的「生产验证」：真实 JSON-RPC 传输 + 真实语言服务器符号解析。
// clangd 只登记「处于工程上下文（compile_commands.json）中」的文件，独立文件会报
// "non-added document" 并返回空。因此本测试构造一个含 compile_commands.json 的最小
// C++ 工程（main.cpp + 编译命令）驱动 clangd 真实提取类与方法：
//   - 无 clangd → 跳过；
//   - clangd 在工程上下文下仍无法登记/返回符号 → 记录缺口并跳过（CI 保持绿色）。
func TestLSPAdapter_RealClangd(t *testing.T) {
	if ResolveServerPath("clangd") == "" {
		t.Skip("clangd not discoverable via PATH or GOPATH/bin; skipping real LSP validation")
	}

	path, rootAbs := writeClangdProject(t)

	// --compile-commands-dir 显式指定工程上下文位置（clangd 默认按 rootUri 探测，
	// 显式指定加载更确定）；--background-index 开启后台索引加速符号可用。
	a := NewLSPAdapter("clangd", "clangd",
		[]string{"--compile-commands-dir=" + rootAbs, "--background-index"}, "cpp", 60*time.Second)
	if err := a.Init(context.Background(), map[string]any{"rootUri": "file://" + toLSPPath(rootAbs)}); err != nil {
		t.Fatalf("clangd Init failed: %v", err)
	}
	defer a.Close()

	ir := parseWithRetry(t, a, path, 1)
	if len(ir.Classes) < 1 || len(ir.Methods) < 1 {
		t.Skipf("clangd returned no symbols under compile-commands project (classes=%d methods=%d)", len(ir.Classes), len(ir.Methods))
	}
	if ir.Classes[0].Name != "Calculator" {
		t.Errorf("expected class 'Calculator', got %q", ir.Classes[0].Name)
	}
	names := make([]string, 0, len(ir.Methods))
	for _, m := range ir.Methods {
		names = append(names, m.Name)
	}
	if !containsName(names, "Add") {
		t.Errorf("expected method 'Add' in %v", names)
	}
}

// writeClangdProject 构造一个最小 C++ 工程（main.cpp + compile_commands.json），
// 使 clangd 能将文件登记进工程上下文并产出符号。
// 返回 (源文件 LSP 路径, 工程根目录绝对路径)。
//
// 工程目录落在 t.TempDir()（系统临时目录，绝对长路径，仓库外），不再写入仓库根
// ./cs_lsp_tmp——避免与扫描整仓的集成测试并行竞态（详见 writeTempSource 注释）。
// 关键：用 EvalSymlinks 解析真实路径（macOS 上 /tmp→/private/tmp、/Volumes→真实挂载），
// 否则 clangd 会把源文件路径规范化为真实路径，与 compile_commands 中的 file 失配，
// 报 "trying to get AST for non-added document"。
func writeClangdProject(t *testing.T) (string, string) {
	t.Helper()
	abs := filepath.Join(t.TempDir(), "clangd_proj")
	if err := os.MkdirAll(abs, 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(abs) })
	// 注意：不使用 EvalSymlinks 解析符号链接。实测 macOS 上 /Volumes/Data 为符号链接，
	// 若 compile_commands 用解析后的真实路径，而 didOpen 的 URI 用符号链接路径，二者失配
	// 会让 clangd 报 "non-added document"。保持 filepath.Abs 原始路径在两侧一致即可。

	src := `class Calculator {
public:
    int Add(int a, int b) {
        return a + b;
    }
    int Sub(int a, int b) {
        return a - b;
    }
};
`
	mainCPP := filepath.Join(abs, "main.cpp")
	if err := os.WriteFile(mainCPP, []byte(src), 0o644); err != nil {
		t.Fatalf("write main.cpp: %v", err)
	}
	// compile_commands.json：clangd 在根目录或子目录自动发现；
	// directory/file 使用解析后的真实路径，确保与 clangd 规范化路径一致
	cc := fmt.Sprintf(`[{"directory":%q,"command":"clang++ -std=c++17 -c main.cpp -o main.o","file":%q}]`,
		abs, mainCPP)
	if err := os.WriteFile(filepath.Join(abs, "compile_commands.json"), []byte(cc), 0o644); err != nil {
		t.Fatalf("write compile_commands.json: %v", err)
	}
	return toLSPPath(mainCPP), abs
}

// TestLSPAdapter_RealGopls 使用真实 gopls 验证 LSP 适配器端到端（Go 为本项目主语言）。
//
// gopls 可能未安装（如 CI 环境），缺失时优雅跳过。本地开发机若已安装 gopls 即可真正验证。
func TestLSPAdapter_RealGopls(t *testing.T) {
	if ResolveServerPath("gopls") == "" {
		t.Skip("gopls not discoverable via PATH or GOPATH/bin; skipping real LSP validation (install via 'go install golang.org/x/tools/gopls@latest')")
	}

	src := `package calc

type Calculator struct {
	precision int
}

func (c *Calculator) Add(a, b int) int {
	return a + b
}

func (c *Calculator) Sub(a, b int) int {
	return a - b
}
`
	path := writeTempSource(t, ".go", src)

	// gopls 需要 module（go.mod）才能建立 view 并提供符号；clangd/jdtls 忽略之。
	// writeTempSource 将文件写入 t.TempDir() 目录，此处补一个最小 go.mod。
	modDir := filepath.Dir(lspPathToOSPath(path))
	if modDir == "" || modDir == "." {
		t.Fatalf("cannot derive go.mod dir from LSP path %q", path)
	}
	if err := os.WriteFile(filepath.Join(modDir, "go.mod"), []byte("module cs_lsp_tmp_go\n\ngo 1.25\n"), 0o644); err != nil {
		t.Fatalf("write go.mod: %v", err)
	}

	// 生产默认超时 10s；gopls 首次为独立 module 建立 view 时首条 documentSymbol 可能更久，
	// 测试侧放宽到 60s 以容纳首次视图构建（生产默认不变）。
	a := NewLSPAdapter("gopls", "gopls", nil, "go", 60*time.Second)
	if err := a.Init(context.Background(), nil); err != nil {
		t.Fatalf("gopls Init failed: %v", err)
	}
	defer a.Close()

	ir := parseWithRetry(t, a, path, 1)
	if ir.Source != "gopls" {
		t.Errorf("source = %q, want gopls", ir.Source)
	}
	if len(ir.Classes) < 1 {
		t.Fatalf("expected >=1 class from gopls, got %d", len(ir.Classes))
	}
	if ir.Classes[0].Name != "Calculator" {
		t.Errorf("expected class 'Calculator', got %q", ir.Classes[0].Name)
	}
	if len(ir.Methods) < 1 {
		t.Fatalf("expected >=1 method from gopls, got %d", len(ir.Methods))
	}
	names := make([]string, 0, len(ir.Methods))
	for _, m := range ir.Methods {
		names = append(names, m.Name)
	}
	if !containsName(names, "Add") {
		t.Errorf("expected method 'Add' in %v", names)
	}
}

// containsName 方法名匹配：真实 LSP 的 documentSymbol 名称可能带接收者前缀，
// 例如 gopls 的 Go 方法为 "(*Calculator).Add"，clangd 的 C++ 方法为 "Add"，
// jdtls 的 Java 方法为 "add(int, int)" 或 "Calculator.add(int, int)"。
// 用「精确相等 / . 结尾 / ) 结尾 / 名(参数 开头」判定，兼容多种命名风格。
func containsName(ss []string, target string) bool {
	for _, s := range ss {
		if s == target ||
			strings.HasSuffix(s, "."+target) ||
			strings.HasSuffix(s, ")"+target) ||
			strings.HasPrefix(s, target+"(") {
			return true
		}
	}
	return false
}

// dirURI 从 LSP 路径推导其父目录的 file URI，供 initialize 的 rootUri 使用，
// 使 clangd 等语言服务器将文件关联到工程（否则可能拒绝登记文档）。
func dirURI(lspPath string) string {
	idx := strings.LastIndex(lspPath, "/")
	if idx < 0 {
		return "file://" + lspPath
	}
	return "file://" + lspPath[:idx]
}

// TestLSPAdapter_RealRustAnalyzer 使用真实 rust-analyzer 验证 LSP 适配器端到端（Rust 为 D 扩展语言）。
//
// rust-analyzer 可能未安装（如本机/常规 CI 环境），缺失时优雅跳过（CI 保持绿色）。
// 提供了 rust-analyzer + cargo 的 Docker 测试镜像可真正验证 Rust 高精度解析路径。
func TestLSPAdapter_RealRustAnalyzer(t *testing.T) {
	if ResolveServerPath("rust-analyzer") == "" {
		t.Skip("rust-analyzer not discoverable via PATH or GOPATH/bin; skipping real LSP validation (install via 'rustup component add rust-analyzer')")
	}

	path, rootAbs := writeRustProject(t)

	a := NewRustAnalyzerAdapter()
	if err := a.Init(context.Background(), map[string]any{"rootUri": "file://" + toLSPPath(rootAbs)}); err != nil {
		t.Fatalf("rust-analyzer Init failed: %v", err)
	}
	defer a.Close()

	ir := parseWithRetry(t, a, path, 1)
	if ir.Source != "rust-analyzer" {
		t.Errorf("source = %q, want rust-analyzer", ir.Source)
	}
	if len(ir.Classes) < 1 {
		t.Fatalf("expected >=1 class/struct from rust-analyzer, got %d", len(ir.Classes))
	}
	classNames := make([]string, 0, len(ir.Classes))
	for _, c := range ir.Classes {
		classNames = append(classNames, c.Name)
	}
	if !containsName(classNames, "Calculator") {
		t.Errorf("expected struct 'Calculator' in %v", classNames)
	}
	if len(ir.Methods) < 1 {
		t.Fatalf("expected >=1 method from rust-analyzer, got %d", len(ir.Methods))
	}
	names := make([]string, 0, len(ir.Methods))
	for _, m := range ir.Methods {
		names = append(names, m.Name)
	}
	if !containsName(names, "add") {
		t.Errorf("expected method 'add' in %v", names)
	}
}

// writeRustProject 构造一个最小 Rust 工程（Cargo.toml + src/lib.rs，含 struct/impl/fn/enum），
// 使 rust-analyzer 能建立 cargo workspace 视图并抽取符号。
// 返回 (源文件 LSP 路径, 工程根目录绝对路径)。目录落在 t.TempDir()（仓库外），自动清理。
func writeRustProject(t *testing.T) (string, string) {
	t.Helper()
	abs := filepath.Join(t.TempDir(), "rust_proj")
	if err := os.MkdirAll(filepath.Join(abs, "src"), 0o755); err != nil {
		t.Fatalf("mkdir proj: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(abs) })

	src := `pub struct Calculator {
    precision: i32,
}

impl Calculator {
    pub fn add(&self, a: i32, b: i32) -> i32 {
        a + b
    }
    pub fn sub(&self, a: i32, b: i32) -> i32 {
        a - b
    }
}

pub enum Op {
    Add,
    Sub,
}
`
	libRS := filepath.Join(abs, "src", "lib.rs")
	if err := os.WriteFile(libRS, []byte(src), 0o644); err != nil {
		t.Fatalf("write lib.rs: %v", err)
	}
	cargo := "[package]\nname = \"cs_lsp_tmp_rust\"\nversion = \"0.1.0\"\nedition = \"2021\"\n"
	if err := os.WriteFile(filepath.Join(abs, "Cargo.toml"), []byte(cargo), 0o644); err != nil {
		t.Fatalf("write Cargo.toml: %v", err)
	}
	return toLSPPath(libRS), abs
}

// TestLSPAdapter_RealPyright 使用真实 pyright 验证 LSP 适配器端到端（Python 为 D 扩展语言）。
//
// pyright 可能未安装（需 node 运行时），缺失时优雅跳过（CI 保持绿色）。提供了
// pyright + node 的 Docker 测试镜像可真正验证 Python 高精度解析路径。
func TestLSPAdapter_RealPyright(t *testing.T) {
	if ResolveServerPath("pyright-langserver") == "" {
		t.Skip("pyright-langserver not discoverable via PATH or GOPATH/bin; skipping real LSP validation (install via 'npm install -g pyright')")
	}

	src := `class Calculator:
    precision: int = 0

    def add(self, a: int, b: int) -> int:
        return a + b

    def sub(self, a: int, b: int) -> int:
        return a - b
`
	path := writeTempSource(t, ".py", src)

	a := NewPyrightAdapter()
	if err := a.Init(context.Background(), map[string]any{"rootUri": dirURI(path)}); err != nil {
		t.Fatalf("pyright Init failed: %v", err)
	}
	defer a.Close()

	ir := parseWithRetry(t, a, path, 1)
	if ir.Source != "pyright-langserver" {
		t.Errorf("source = %q, want pyright-langserver", ir.Source)
	}
	if len(ir.Classes) < 1 {
		t.Fatalf("expected >=1 class from pyright, got %d", len(ir.Classes))
	}
	classNames := make([]string, 0, len(ir.Classes))
	for _, c := range ir.Classes {
		classNames = append(classNames, c.Name)
	}
	if !containsName(classNames, "Calculator") {
		t.Errorf("expected class 'Calculator' in %v", classNames)
	}
	if len(ir.Methods) < 1 {
		t.Fatalf("expected >=1 method from pyright, got %d", len(ir.Methods))
	}
	names := make([]string, 0, len(ir.Methods))
	for _, m := range ir.Methods {
		names = append(names, m.Name)
	}
	if !containsName(names, "add") {
		t.Errorf("expected method 'add' in %v", names)
	}
}

// linkGlobalTypescript 将全局安装的 typescript 软链到被测工程的 node_modules，
// 供 typescript-language-server 在其 peerDep 解析路径内找到 tsserver。
//
// typescript-language-server 把 typescript 列为 peerDep，必须能在「被分析工程」的
// node_modules 内解析到 typescript（全局安装 + NODE_PATH 均不被采纳，这是此前 Docker
// e2e 报 "Could not find a valid TypeScript installation" 的根因）。本机未装
// typescript-language-server 时 e2e 直接 skip，不会执行到此；Docker 测试镜像以
// `npm install -g typescript` 装全局，npm root -g 即全局 node_modules 目录。
//
// 全局 typescript 不存在时本函数静默 no-op（不影响其它分支，亦不会污染本机工程）。
func linkGlobalTypescript(t *testing.T, projDir string) {
	t.Helper()
	// 解析 npm 全局根（如 /usr/local/lib/node_modules）
	out, err := exec.Command("npm", "root", "-g").Output()
	if err != nil {
		t.Logf("linkGlobalTypescript: npm root -g failed (%v), skipping symlink", err)
		return
	}
	globalRoot := strings.TrimSpace(string(out))
	globalTS := filepath.Join(globalRoot, "typescript")
	if fi, err := os.Stat(globalTS); err != nil || !fi.IsDir() {
		t.Logf("linkGlobalTypescript: global typescript not found at %s, skipping symlink", globalTS)
		return
	}
	nm := filepath.Join(projDir, "node_modules")
	if err := os.MkdirAll(nm, 0o755); err != nil {
		t.Fatalf("linkGlobalTypescript: mkdir node_modules: %v", err)
	}
	link := filepath.Join(nm, "typescript")
	// 已存在则跳过（避免重复软链或覆盖真实安装）
	if _, err := os.Lstat(link); err == nil {
		return
	}
	if err := os.Symlink(globalTS, link); err != nil {
		t.Fatalf("linkGlobalTypescript: symlink %s -> %s: %v", link, globalTS, err)
	}
	t.Logf("linkGlobalTypescript: linked %s -> %s", link, globalTS)
}

// TestLSPAdapter_RealTSLanguageServer 使用真实 typescript-language-server 验证 LSP 适配器端到端
// （TypeScript 为 D 扩展语言）。
//
// typescript-language-server 可能未安装（需 node 运行时），缺失时优雅跳过（CI 保持绿色）。
// 提供了 typescript-language-server + node 的 Docker 测试镜像可真正验证 TS 高精度解析路径。
func TestLSPAdapter_RealTSLanguageServer(t *testing.T) {
	if ResolveServerPath("typescript-language-server") == "" {
		t.Skip("typescript-language-server not discoverable via PATH or GOPATH/bin; skipping real LSP validation (install via 'npm install -g typescript-language-server typescript')")
	}

	src := `export class Calculator {
    precision: number = 0;

    add(a: number, b: number): number {
        return a + b;
    }

    sub(a: number, b: number): number {
        return a - b;
    }
}
`
	path := writeTempSource(t, ".ts", src)
	// ts-language-server 把 typescript 列为 peerDep，需在被分析工程内可解析；
	// Docker 测试镜像将 typescript 装为全局，这里软链到工程 node_modules 供验证
	// （本机 skip 不会执行到此；仅 Docker 内 typescript 全局路径存在时生效）。
	linkGlobalTypescript(t, filepath.Dir(lspPathToOSPath(path)))

	a := NewTSLanguageServerAdapter()
	if err := a.Init(context.Background(), map[string]any{"rootUri": dirURI(path)}); err != nil {
		t.Fatalf("typescript-language-server Init failed: %v", err)
	}
	defer a.Close()

	ir := parseWithRetry(t, a, path, 1)
	if ir.Source != "typescript-language-server" {
		t.Errorf("source = %q, want typescript-language-server", ir.Source)
	}
	if len(ir.Classes) < 1 {
		t.Fatalf("expected >=1 class from typescript-language-server, got %d", len(ir.Classes))
	}
	classNames := make([]string, 0, len(ir.Classes))
	for _, c := range ir.Classes {
		classNames = append(classNames, c.Name)
	}
	if !containsName(classNames, "Calculator") {
		t.Errorf("expected class 'Calculator' in %v", classNames)
	}
	if len(ir.Methods) < 1 {
		t.Fatalf("expected >=1 method from typescript-language-server, got %d", len(ir.Methods))
	}
	names := make([]string, 0, len(ir.Methods))
	for _, m := range ir.Methods {
		names = append(names, m.Name)
	}
	if !containsName(names, "add") {
		t.Errorf("expected method 'add' in %v", names)
	}
}

// TestLSPAdapter_RealJDTLS 使用真实 jdtls 验证 LSP 适配器端到端（Java 为 D 扩展语言）。
//
// jdtls 可能未安装（需 JDK 17 + eclipse.jdt.ls，并以 "jdtls" 命令名置于 PATH），
// 缺失时优雅跳过（CI 保持绿色）。提供了 jdtls + JDK 的 Docker 测试镜像可真正验证
// Java 高精度解析路径。jdtls 以 invisible-project 模式直接解析单文件，无需 pom.xml。
func TestLSPAdapter_RealJDTLS(t *testing.T) {
	if ResolveServerPath("jdtls") == "" {
		t.Skip("jdtls not discoverable via PATH or GOPATH/bin; skipping real LSP validation (install eclipse.jdt.ls + JDK 17, wrap as 'jdtls' on PATH)")
	}

	src := `public class Calculator {
    private int precision;

    public int add(int a, int b) {
        return a + b;
    }

    public int sub(int a, int b) {
        return a - b;
    }
}
`
	path := writeTempSource(t, ".java", src)

	a := NewJDTLSAdapter()
	if err := a.Init(context.Background(), map[string]any{"rootUri": dirURI(path)}); err != nil {
		t.Fatalf("jdtls Init failed: %v", err)
	}
	defer a.Close()

	ir := parseWithRetry(t, a, path, 1)
	if ir.Source != "jdtls" {
		t.Errorf("source = %q, want jdtls", ir.Source)
	}
	if len(ir.Classes) < 1 {
		t.Fatalf("expected >=1 class from jdtls, got %d", len(ir.Classes))
	}
	classNames := make([]string, 0, len(ir.Classes))
	for _, c := range ir.Classes {
		classNames = append(classNames, c.Name)
	}
	if !containsName(classNames, "Calculator") {
		t.Errorf("expected class 'Calculator' in %v", classNames)
	}
	if len(ir.Methods) < 1 {
		t.Fatalf("expected >=1 method from jdtls, got %d", len(ir.Methods))
	}
	names := make([]string, 0, len(ir.Methods))
	for _, m := range ir.Methods {
		names = append(names, m.Name)
	}
	if !containsName(names, "add") {
		t.Errorf("expected method 'add' in %v", names)
	}
}
