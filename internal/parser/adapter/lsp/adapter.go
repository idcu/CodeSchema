// Package lsp 提供通用 LSP 适配器框架。
//
// LSP（Language Server Protocol）适配器通过子进程启动 LSP 服务器，
// 使用 JSON-RPC 2.0 协议通信，获取精确的符号定义和引用关系。
//
// 支持以下 LSP 实现：
//   - gopls: Go 官方 LSP（需 Go 1.22+）
//   - jdtls: Eclipse JDT LS（需 JDK 17+）
//   - clangd: LLVM Clangd（需 LLVM 工具链）
//
// 降级策略：当 LSP 子进程启动失败或通信异常时，
// 返回 ErrSourceUnavailable 触发编排层降级到 tree-sitter。
package lsp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"go/build"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/idcu/codeschema/internal/errors"
	log "gitee.com/idcu-go/log"
	"gitee.com/idcu-go/metrics"
	"github.com/idcu/codeschema/internal/parser"
	"gitee.com/idcu-go/retry"
)

// LSPAdapter 通用 LSP 适配器，通过 JSON-RPC 2.0 与 LSP 服务器通信。
type LSPAdapter struct {
	name    string
	cmd     string
	args    []string
	lang    string
	timeout time.Duration
	env     []string // 子进程额外环境变量（可选）

	mu       sync.Mutex
	cmdObj   *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	msgID    int
	pending  map[int]chan<- *jsonRPCResponse
	initOnce sync.Once
	cancel   context.CancelFunc

	alive      bool          // 连接是否存活，readResponses 退出时自动置 false
	aliveCheck chan struct{} // readResponses 退出时关闭，用于等待优雅退出

	logger         *log.Logger      // 结构化日志，暴露降级原因，避免"静默丢信息"
	retryAttempts  int             // documentSymbol 等高频请求的可重试次数（含首次）
	retryBaseDelay time.Duration   // 重试退避基础延迟
	retryMaxDelay  time.Duration   // 重试退避最大延迟
}

// jsonRPCRequest JSON-RPC 2.0 请求结构。
// 注意：ID 必须 omitempty——notification（didOpen/didClose/initialized）不允许携带 id，
// 否则 clangd 等严格实现会把通知当请求处理（-32601 method not found）并拒绝登记文档。
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params"`
}

// jsonRPCResponse JSON-RPC 2.0 响应结构。
type jsonRPCResponse struct {
	JSONRPC string          `json:"jsonrpc"`
	ID      int             `json:"id"`
	Result  json.RawMessage `json:"result,omitempty"`
	Error   *jsonRPCError   `json:"error,omitempty"`
	Method  string          `json:"method,omitempty"`
}

// jsonRPCError JSON-RPC 错误结构。
type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

// NewLSPAdapter 创建通用 LSP 适配器。
//   - name: 适配器标识（如 "gopls" / "jdtls" / "clangd"）
//   - cmd: LSP 服务器命令
//   - args: 启动参数
//   - lang: 支持的语言标识
//   - timeout: 请求超时时间（默认 10s）
func NewLSPAdapter(name, cmd string, args []string, lang string, timeout time.Duration) *LSPAdapter {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}
	resolved := ResolveServerPath(cmd)
	if resolved == "" {
		resolved = cmd
	}
	return &LSPAdapter{
		name:       name,
		cmd:        resolved,
		args:       args,
		lang:       lang,
		timeout:    timeout,
		pending:    make(map[int]chan<- *jsonRPCResponse),
		aliveCheck: make(chan struct{}),
		logger:      log.WithModule("lsp:" + name),
		// 重试以覆盖 LSP 子进程的瞬时抖动（超时、连接重置），不放大单文件解析延迟。
		retryAttempts: 2,
		retryBaseDelay: 150 * time.Millisecond,
		retryMaxDelay:  time.Second,
	}
}

// ResolveServerPath 解析语言服务器可执行文件路径。
//
// 先查 PATH（exec.LookPath），再回退到各 $GOPATH/bin 与 $HOME/go/bin，
// 避免 GOPATH/bin 未加入 PATH（macOS 常见）时 gopls 等无法被发现而静默降级到 tree-sitter。
// 找不到时返回空串：commandAvailable 据此返回 false（不注册该适配器）；
// 运行期拉起命令（a.cmd）在解析失败时会回退为原命令名，由 spawn 失败触发优雅降级。
func ResolveServerPath(name string) string {
	if p, err := exec.LookPath(name); err == nil {
		return p
	}
	for _, gp := range filepath.SplitList(build.Default.GOPATH) {
		if gp == "" {
			continue
		}
		cand := filepath.Join(gp, "bin", name)
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	if home := os.Getenv("HOME"); home != "" {
		cand := filepath.Join(home, "go", "bin", name)
		if _, err := os.Stat(cand); err == nil {
			return cand
		}
	}
	return ""
}

// init 注册 LSP 适配器的可观测性指标，暴露降级与失败原因。
func init() {
	metrics.RegisterCounter("lsp_missing_compile_commands_total",
		"clangd 初始化时未发现 compile_commands.json（C/C++ 可能静默返回空符号）")
	metrics.RegisterCounter("lsp_parse_empty_symbols_total",
		"解析非空 C/C++ 文件但 LSP 返回 0 个符号（疑似缺项目上下文）", "lang")
	metrics.RegisterCounter("lsp_parse_errors_total",
		"LSP 解析请求失败次数", "reason")
	metrics.RegisterCounter("lsp_retries_total",
		"LSP 请求重试次数（瞬时失败）", "method")
	metrics.RegisterCounter("lsp_malformed_frames_total",
		"被静默恢复的异常 LSP 协议帧（已暴露为 WARN）", "kind")
}

// NewGoplsAdapter 创建 gopls 适配器。
func NewGoplsAdapter() *LSPAdapter {
	return NewLSPAdapter("gopls", "gopls", nil, "go", 10*time.Second)
}

// NewJDTLSAdapter 创建 jdtls 适配器。
//
// jdtls 基于 Eclipse JDT LS，需 JDK 21 + java -jar 启动（非单二进制）；JVM 冷启动
// 与 bundle 解析在容器/CI 内首启偏重，默认超时放宽到 30s（其余 LSP 多为 10–20s）。
func NewJDTLSAdapter() *LSPAdapter {
	return NewLSPAdapter("jdtls", "jdtls", nil, "java", 30*time.Second)
}

// NewClangdAdapter 创建 clangd 适配器。
func NewClangdAdapter() *LSPAdapter {
	return NewLSPAdapter("clangd", "clangd", nil, "cpp", 10*time.Second)
}

// NewRustAnalyzerAdapter 创建 rust-analyzer 适配器。
//
// rust-analyzer 启动比 gopls/clangd 略重（需 cargo metadata 建立 workspace 视图），
// 默认超时放宽到 20s；其余走通用 JSON-RPC + documentSymbol 映射（struct→STRUCT、
// fn→Method、enum→ENUM），无需工程上下文即可抽取单文件符号。
func NewRustAnalyzerAdapter() *LSPAdapter {
	return NewLSPAdapter("rust-analyzer", "rust-analyzer", nil, "rust", 20*time.Second)
}

// NewPyrightAdapter 创建 pyright 适配器（Python）。
//
// pyright 是基于 Node 的 Python 语言服务器（类型检查 + LSP），需 node 运行时。
// 其 LSP 入口二进制为 `pyright-langserver`（而非 `pyright` CLI），以 --stdio 进入 LSP 模式；
// `pyright` 直接跑会卡在 initialize。通用 documentSymbol 映射覆盖 Python class(5)/method(6)/function(12)，
// 无需虚拟环境即可抽取单文件符号（类型错误仅影响诊断，不影响符号提取）。
func NewPyrightAdapter() *LSPAdapter {
	return NewLSPAdapter("pyright-langserver", "pyright-langserver", []string{"--stdio"}, "py", 20*time.Second)
}

// NewTSLanguageServerAdapter 创建 typescript-language-server 适配器（TypeScript）。
//
// typescript-language-server 基于 Node，以 --stdio 进入 LSP 模式；需 typescript 同伴安装。
// 通用 documentSymbol 映射覆盖 TS class(5)/method(6)/function(12)/interface(24)，
// 单文件即可抽取符号（tsconfig 缺失时退化为单文件语义，不影响符号提取）。
func NewTSLanguageServerAdapter() *LSPAdapter {
	return NewLSPAdapter("typescript-language-server", "typescript-language-server", []string{"--stdio"}, "ts", 20*time.Second)
}

// Name 返回适配器唯一标识。
func (a *LSPAdapter) Name() string { return a.name }

// Supports 判断是否支持指定语言。
func (a *LSPAdapter) Supports(lang string) bool {
	return lang == a.lang
}

// Init 初始化适配器，启动 LSP 子进程并发送 initialize 请求。
// config 支持以下键：
//   - rootUri: string 工作区根 URI（可选）
func (a *LSPAdapter) Init(ctx context.Context, config map[string]any) error {
	ctx, a.cancel = context.WithCancel(ctx)

	// 启动 LSP 子进程（受锁保护）
	a.mu.Lock()
	cmd := exec.CommandContext(ctx, a.cmd, a.args...)
	if len(a.env) > 0 {
		cmd.Env = append(os.Environ(), a.env...)
	}
	stdin, err := cmd.StdinPipe()
	if err != nil {
		a.mu.Unlock()
		return fmt.Errorf("cannot create stdin pipe for %s: %w", a.name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		a.mu.Unlock()
		stdin.Close()
		return fmt.Errorf("cannot create stdout pipe for %s: %w", a.name, err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		a.mu.Unlock()
		stdin.Close()
		stdout.Close()
		return fmt.Errorf("cannot create stderr pipe for %s: %w", a.name, err)
	}
	if err := cmd.Start(); err != nil {
		a.mu.Unlock()
		stdin.Close()
		stdout.Close()
		stderr.Close()
		return fmt.Errorf("cannot start %s: %w: %w", a.name, err, errors.ErrSourceUnavailable)
	}
	a.cmdObj = cmd
	a.stdin = stdin
	a.stdout = stdout
	a.alive = true
	a.aliveCheck = make(chan struct{})
	a.mu.Unlock()

	// 捕获 stderr：clangd 等常把降级/错误原因（如缺 compile_commands.json）写到 stderr，
	// 原实现直接丢弃（io.Discard），导致"静默丢信息"。这里按行暴露到日志。
	go func() {
		sc := bufio.NewScanner(stderr)
		sc.Buffer(make([]byte, 0, 64*1024), 1024*1024)
		for sc.Scan() {
			line := strings.TrimSpace(sc.Text())
			if line == "" {
				continue
			}
			if isLSPServerErrorLine(line) {
				a.logger.Warn("LSP server stderr", "server", a.name, "line", line)
			} else {
				a.logger.Debug("LSP server stderr", "server", a.name, "line", line)
			}
		}
	}()

	// 启动异步响应读取协程
	go a.readResponses()

	// 从 config 提取 rootUri
	rootURI := interface{}(nil)
	if config != nil {
		if ru, ok := config["rootUri"]; ok {
			rootURI = ru
		}
	}

	// 探测项目上下文：clangd 缺 compile_commands.json 时会静默返回空符号，
	// 这里显式探测并暴露原因，避免"静默丢信息"。
	if a.lang == "cpp" {
		if rootDir, ok := rootURIToDir(rootURI); ok {
			a.probeCompileCommands(rootDir)
		}
	}

	// 发送 initialize 请求（此时已释放锁，不会与 sendRequest 内部锁冲突）
	initParams := map[string]any{
		"processId":             nil,
		"clientInfo":            map[string]string{"name": "codeschema"},
		"capabilities":          map[string]any{},
		"workspaceFolders":      nil,
		"rootUri":               rootURI,
	}
	_, err = a.sendRequest(ctx, "initialize", initParams)
	if err != nil {
		a.logger.Warn("LSP initialize failed, degrading to fallback parser",
			"server", a.name, "error", err.Error())
		cmd.Process.Kill()
		a.mu.Lock()
		a.alive = false
		a.mu.Unlock()
		return fmt.Errorf("%s initialize failed: %w: %w", a.name, err, errors.ErrSourceUnavailable)
	}

	// 发送 initialized 通知（已释放锁，不会与 sendNotification 内部锁冲突）
	a.sendNotification("initialized", map[string]any{})

	return nil
}

// Close 清理适配器资源，终止 LSP 子进程。
func (a *LSPAdapter) Close() error {
	a.mu.Lock()
	a.alive = false
	if a.cancel != nil {
		a.cancel()
	}
	stdin := a.stdin
	cmdObj := a.cmdObj
	a.mu.Unlock()

	if stdin != nil {
		stdin.Close()
	}

	if cmdObj != nil && cmdObj.Process != nil {
		// 发送 shutdown/exit 通知（已释放锁，不会与 sendNotification 内部锁冲突）
		a.sendNotification("shutdown", nil)
		a.sendNotification("exit", nil)
		_ = cmdObj.Process.Kill()
	}

	return nil
}

// IsAlive 检查 LSP 连接是否存活。
// 当 readResponses 因进程退出或管道关闭而终止时，返回 false。
func (a *LSPAdapter) IsAlive() bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.alive
}

// WaitAlive 等待 readResponses 协程退出（用于测试）。
func (a *LSPAdapter) WaitAlive() {
	<-a.aliveCheck
}

// Parse 解析单个文件，通过 LSP 获取符号和引用信息。
func (a *LSPAdapter) Parse(ctx context.Context, path string) (*parser.IRDocument, error) {
	a.mu.Lock()
	if a.cmdObj == nil || a.cmdObj.Process == nil {
		a.mu.Unlock()
		return nil, fmt.Errorf("%s not initialized: %w", a.name, errors.ErrSourceUnavailable)
	}
	a.mu.Unlock()

	// 1. 发送 textDocument/didOpen 通知
	uri := "file://" + path
	// 携带真实文件内容：部分语言服务器（如 clangd）要求 didOpen 提供文本，
	// 否则不会登记文档，后续 documentSymbol 会报 "non-added document"。
	// 读取失败则回退为空文本（gopls 等会自行从磁盘读取，行为不变）。
	text := readLSPFileContent(lspPathToOSPath(path))
	didOpenParams := map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": a.lang,
			"version":    1,
			"text":       text,
		},
	}
	a.sendNotification("textDocument/didOpen", didOpenParams)

	// 2. 发送 textDocument/documentSymbol 请求（带重试，覆盖瞬时失败）
	symbolParams := map[string]any{
		"textDocument": map[string]string{"uri": uri},
	}
	symbolResult, err := a.requestWithRetry(ctx, "textDocument/documentSymbol", symbolParams)
	if err != nil {
		metrics.IncCounter("lsp_parse_errors_total", "document_symbol")
		a.logger.Warn("LSP documentSymbol failed after retries, file skipped",
			"server", a.name, "path", path, "error", err.Error())
		// 发送 didClose 通知
		a.sendNotification("textDocument/didClose", map[string]any{
			"textDocument": map[string]string{"uri": uri},
		})
		return nil, fmt.Errorf("%s documentSymbol failed: %w", a.name, err)
	}

	// 3. 解析符号结果
	ir := &parser.IRDocument{
		Source:   a.name,
		Language: a.lang,
		FilePath: path,
	}

	// 尝试解析为 SymbolInformation[] 或 DocumentSymbol[]
	var symbols []symbolInfo
	if err := json.Unmarshal(symbolResult, &symbols); err == nil {
		for _, sym := range symbols {
			ir = a.addSymbolInfo(ir, sym)
		}
	} else {
		var docSymbols []documentSymbol
		if err := json.Unmarshal(symbolResult, &docSymbols); err == nil {
			for _, ds := range docSymbols {
				ir = a.addDocumentSymbol(ir, ds)
			}
		}
	}

	// 4. 发送 didClose 通知
	a.sendNotification("textDocument/didClose", map[string]any{
		"textDocument": map[string]string{"uri": uri},
	})

	// 5. 可观测性：clangd 缺项目上下文（如 compile_commands.json）时会静默返回空符号，
	// 这里对非空 C/C++ 文件显式暴露该降级，避免"静默丢信息"。
	a.maybeReportEmptySymbols(path, text, ir)

	return ir, nil
}

// maybeReportEmptySymbols 在 C/C++ 非空文件却解析出 0 个符号时显式告警，
// 通常意味着 clangd 缺少项目上下文（compile_commands.json），原实现会静默返回空 IR。
func (a *LSPAdapter) maybeReportEmptySymbols(path, text string, ir *parser.IRDocument) {
	if a.lang != "cpp" || len(text) == 0 || len(ir.Classes) != 0 || len(ir.Methods) != 0 {
		return
	}
	a.logger.Warn("LSP returned zero symbols for non-empty C/C++ file; likely missing project context",
		"server", a.name, "path", path,
		"hint", "ensure compile_commands.json exists (cmake -DCMAKE_EXPORT_COMPILE_COMMANDS=ON or bear -- make)")
	metrics.IncCounter("lsp_parse_empty_symbols_total", "cpp")
}

// requestWithRetry 发送 JSON-RPC 请求，并在瞬时失败时按指数退避重试（复用 robust）。
// 仅对可重试错误（超时、连接重置等）重试；参数/权限/未实现等错误直接返回。
func (a *LSPAdapter) requestWithRetry(ctx context.Context, method string, params any) (json.RawMessage, error) {
	var result json.RawMessage
	var calls int
	err := retry.Do(ctx, func() error {
		calls++
		if calls > 1 {
			metrics.IncCounter("lsp_retries_total", method)
			a.logger.Warn("retrying LSP request after transient failure",
				"server", a.name, "method", method, "attempt", calls)
		}
		r, e := a.sendRequest(ctx, method, params)
		if e != nil {
			return e
		}
		result = r
		return nil
	},
		retry.WithMaxAttempts(a.retryAttempts),
		retry.WithExponentialBackoff(a.retryBaseDelay, a.retryMaxDelay),
		retry.WithRetryIf(retry.RetryableError),
	)
	if err != nil {
		return nil, err
	}
	return result, nil
}

// rootURIToDir 将 LSP rootUri（"file:///path" 或 "/path"）还原为目录路径。
func rootURIToDir(rootURI any) (string, bool) {
	s, ok := rootURI.(string)
	if !ok || s == "" {
		return "", false
	}
	s = strings.TrimPrefix(s, "file://")
	return s, true
}

// probeCompileCommands 探测 clangd 所需的 compile_commands.json，
// 缺失时显式告警暴露降级原因（否则 clangd 会静默返回空符号）。返回是否找到。
func (a *LSPAdapter) probeCompileCommands(rootDir string) bool {
	candidates := []string{
		filepath.Join(rootDir, "compile_commands.json"),
		filepath.Join(rootDir, "build", "compile_commands.json"),
		filepath.Join(rootDir, "cmake-build-debug", "compile_commands.json"),
		filepath.Join(rootDir, "out", "compile_commands.json"),
		filepath.Join(rootDir, ".cache", "clangd", "compile_commands.json"),
	}
	for _, c := range candidates {
		if _, err := os.Stat(c); err == nil {
			a.logger.Info("found compile_commands.json for clangd", "path", c)
			return true
		}
	}
	a.logger.Warn("no compile_commands.json found; clangd may return empty symbols (missing project context)",
		"root", rootDir,
		"hint", "generate via 'cmake -DCMAKE_EXPORT_COMPILE_COMMANDS=ON' or 'bear -- make'")
	metrics.IncCounter("lsp_missing_compile_commands_total")
	return false
}

// lspPathToOSPath 将 LSP 路径（适配器内 "file://" + path 形式）还原为操作系统路径，
// 用于读取真实文件内容。反向 toLSPPath："/C:/Users/x" → "C:/Users/x"，
// Unix 路径 "/home/x" 保持不变。
func lspPathToOSPath(p string) string {
	if len(p) > 2 && p[0] == '/' && p[2] == ':' {
		p = p[1:] // 去掉前导斜杠，得到 "C:/Users/x"
	}
	return filepath.FromSlash(p)
}

// readLSPFileContent 读取文件内容用于 didOpen 文本；失败返回空字符串（降级）。
func readLSPFileContent(p string) string {
	data, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	return string(data)
}

// symbolInfo 对应 LSP SymbolInformation 结构。
type symbolInfo struct {
	Name     string     `json:"name"`
	Kind     int        `json:"kind"`
	Location struct {
		URI   string `json:"uri"`
		Range struct {
			Start struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"start"`
			End struct {
				Line      int `json:"line"`
				Character int `json:"character"`
			} `json:"end"`
		} `json:"range"`
	} `json:"location"`
	ContainerName string `json:"containerName,omitempty"`
}

// documentSymbol 对应 LSP DocumentSymbol 结构。
type documentSymbol struct {
	Name           string           `json:"name"`
	Kind           int              `json:"kind"`
	Range          documentRange    `json:"range"`
	SelectionRange documentRange    `json:"selectionRange"`
	Children       []documentSymbol `json:"children,omitempty"`
}

type documentRange struct {
	Start struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"start"`
	End struct {
		Line      int `json:"line"`
		Character int `json:"character"`
	} `json:"end"`
}

// addSymbolInfo 将 SymbolInformation 添加到 IRDocument。
func (a *LSPAdapter) addSymbolInfo(ir *parser.IRDocument, sym symbolInfo) *parser.IRDocument {
	// LSP SymbolKind（部分）：5=Class, 6=Method, 9=Constructor, 11=Field,
	// 12=Function, 23=Struct, 24=Interface。
	// 注意：Go 的 struct/interface/func 分别为 23/24/12，原实现仅映射 5/6 会漏掉 Go 符号。
	switch sym.Kind {
	case 5, 9: // Class, Constructor
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name:      sym.Name,
			FullName:  sym.ContainerName + "." + sym.Name,
			Type:      "CLASS",
			StartLine: sym.Location.Range.Start.Line + 1,
			StartCol:  sym.Location.Range.Start.Character + 1,
			EndLine:   sym.Location.Range.End.Line + 1,
			EndCol:    sym.Location.Range.End.Character + 1,
		})
	case 10: // Enum
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name:      sym.Name,
			FullName:  sym.ContainerName + "." + sym.Name,
			Type:      "ENUM",
			StartLine: sym.Location.Range.Start.Line + 1,
			StartCol:  sym.Location.Range.Start.Character + 1,
			EndLine:   sym.Location.Range.End.Line + 1,
			EndCol:    sym.Location.Range.End.Character + 1,
		})
	case 23: // Struct
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name:      sym.Name,
			FullName:  sym.ContainerName + "." + sym.Name,
			Type:      "STRUCT",
			StartLine: sym.Location.Range.Start.Line + 1,
			StartCol:  sym.Location.Range.Start.Character + 1,
			EndLine:   sym.Location.Range.End.Line + 1,
			EndCol:    sym.Location.Range.End.Character + 1,
		})
	case 24: // Interface
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name:      sym.Name,
			FullName:  sym.ContainerName + "." + sym.Name,
			Type:      "INTERFACE",
			StartLine: sym.Location.Range.Start.Line + 1,
			StartCol:  sym.Location.Range.Start.Character + 1,
			EndLine:   sym.Location.Range.End.Line + 1,
			EndCol:    sym.Location.Range.End.Character + 1,
		})
	case 6, 12: // Method, Function
		ir.Methods = append(ir.Methods, parser.MethodIR{
			Name:      sym.Name,
			ClassFQN:  sym.ContainerName,
			StartLine: sym.Location.Range.Start.Line + 1,
			StartCol:  sym.Location.Range.Start.Character + 1,
			EndLine:   sym.Location.Range.End.Line + 1,
			EndCol:    sym.Location.Range.End.Character + 1,
		})
	}
	return ir
}

// addDocumentSymbol 将 DocumentSymbol 递归添加到 IRDocument。
func (a *LSPAdapter) addDocumentSymbol(ir *parser.IRDocument, ds documentSymbol) *parser.IRDocument {
	switch ds.Kind {
	case 5, 9: // Class, Constructor
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name:      ds.Name,
			FullName:  ds.Name,
			Type:      "CLASS",
			StartLine: ds.Range.Start.Line + 1,
			StartCol:  ds.Range.Start.Character + 1,
			EndLine:   ds.Range.End.Line + 1,
			EndCol:    ds.Range.End.Character + 1,
		})
	case 10: // Enum
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name:      ds.Name,
			FullName:  ds.Name,
			Type:      "ENUM",
			StartLine: ds.Range.Start.Line + 1,
			StartCol:  ds.Range.Start.Character + 1,
			EndLine:   ds.Range.End.Line + 1,
			EndCol:    ds.Range.End.Character + 1,
		})
	case 23: // Struct
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name:      ds.Name,
			FullName:  ds.Name,
			Type:      "STRUCT",
			StartLine: ds.Range.Start.Line + 1,
			StartCol:  ds.Range.Start.Character + 1,
			EndLine:   ds.Range.End.Line + 1,
			EndCol:    ds.Range.End.Character + 1,
		})
	case 24: // Interface
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name:      ds.Name,
			FullName:  ds.Name,
			Type:      "INTERFACE",
			StartLine: ds.Range.Start.Line + 1,
			StartCol:  ds.Range.Start.Character + 1,
			EndLine:   ds.Range.End.Line + 1,
			EndCol:    ds.Range.End.Character + 1,
		})
	case 6, 12: // Method, Function
		ir.Methods = append(ir.Methods, parser.MethodIR{
			Name:      ds.Name,
			StartLine: ds.Range.Start.Line + 1,
			StartCol:  ds.Range.Start.Character + 1,
			EndLine:   ds.Range.End.Line + 1,
			EndCol:    ds.Range.End.Character + 1,
		})
	}

	// 递归处理子符号
	for _, child := range ds.Children {
		ir = a.addDocumentSymbol(ir, child)
	}
	return ir
}

// sendRequest 发送 JSON-RPC 请求并等待响应。
func (a *LSPAdapter) sendRequest(ctx context.Context, method string, params any) (json.RawMessage, error) {
	a.mu.Lock()
	a.msgID++
	id := a.msgID
	respCh := make(chan *jsonRPCResponse, 1)
	a.pending[id] = respCh

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		ID:      id,
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	a.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("cannot marshal request: %w", err)
	}

	// 写入 LSP 标准输入（Content-Length 头格式）
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	a.mu.Lock()
	_, err = a.stdin.Write([]byte(header + string(data)))
	a.mu.Unlock()
	if err != nil {
		return nil, fmt.Errorf("cannot write to LSP stdin: %w", err)
	}

	// 等待响应或超时
	select {
	case resp := <-respCh:
		if resp.Error != nil {
			return nil, fmt.Errorf("LSP error %d: %s", resp.Error.Code, resp.Error.Message)
		}
		return resp.Result, nil
	case <-ctx.Done():
		a.mu.Lock()
		delete(a.pending, id)
		a.mu.Unlock()
		return nil, ctx.Err()
	case <-time.After(a.timeout):
		a.mu.Lock()
		delete(a.pending, id)
		a.mu.Unlock()
		return nil, fmt.Errorf("LSP request %s timed out after %v", method, a.timeout)
	}
}

// sendNotification 发送 JSON-RPC 通知（无 ID，无需响应）。
func (a *LSPAdapter) sendNotification(method string, params any) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.stdin == nil {
		return
	}

	req := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
	}
	data, err := json.Marshal(req)
	if err != nil {
		return
	}

	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	_, _ = a.stdin.Write([]byte(header + string(data)))
}

// readResponses 异步读取 LSP 标准输出并分发响应。
//
// LSP 协议使用 HTTP 风格的头格式：
//   Content-Length: <N>\r\n\r\n<JSON BODY (N bytes)>
//
// JSON 体可能包含换行符（如 pretty-printed JSON），
// 因此必须按字节读取 Content-Length 指定的长度，而非逐行读取。
//
// 稳定性质保：
//   - 内部 panic 会被 recover，不会导致整个适配器崩溃
//   - 支持多行头（Content-Type 等额外头被忽略但不破坏协议解析）
//   - 退出时自动关闭 aliveCheck 通道并置 alive=false
func (a *LSPAdapter) readResponses() {
	defer func() {
		if r := recover(); r != nil {
			// 记录 panic 但不崩溃，让编排层通过 IsAlive() 检测到异常
		}
		a.mu.Lock()
		a.alive = false
		a.mu.Unlock()
		close(a.aliveCheck)
	}()

	reader := bufio.NewReader(a.stdout)
	buf := bytes.NewBuffer(make([]byte, 0, 4096))

	for {
		// 读取多行头直到空行（\r\n）
		contentLength := 0
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return // 流关闭或出错，正常退出
			}
			line = strings.TrimRight(line, "\r\n")
			if line == "" {
				break // 空行，头结束
			}
			if strings.HasPrefix(line, "Content-Length: ") {
				cl := strings.TrimPrefix(line, "Content-Length: ")
				n, perr := strconv.Atoi(strings.TrimSpace(cl))
				if perr != nil || n <= 0 {
					if perr != nil {
						// 异常响应头：Content-Length 无法解析，原实现静默丢弃，此处暴露为 WARN。
						a.logger.Warn("malformed LSP Content-Length header, frame skipped",
							"server", a.name, "value", cl)
						metrics.IncCounter("lsp_malformed_frames_total", "content_length")
					}
					contentLength = 0
				} else {
					contentLength = n
				}
			}
			// 其他头（Content-Type 等）被忽略
		}

		if contentLength <= 0 {
			continue
		}

		// 读取精确的 Content-Length 字节
		buf.Reset()
		buf.Grow(contentLength)
		_, err := io.CopyN(buf, reader, int64(contentLength))
		if err != nil {
			return
		}

		// 解析 JSON
		body := buf.Bytes()
		var resp jsonRPCResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			// 异常帧：JSON 解析失败原实现静默跳过，此处暴露为 WARN 便于定位。
			a.logger.Warn("malformed LSP body (invalid JSON), frame skipped",
				"server", a.name, "body", truncate(string(body), 200))
			metrics.IncCounter("lsp_malformed_frames_total", "json")
			continue
		}

		// 分发响应
		if resp.ID > 0 {
			a.mu.Lock()
			ch, ok := a.pending[resp.ID]
			delete(a.pending, resp.ID)
			a.mu.Unlock()
			if ok {
				ch <- &resp
				close(ch)
			} else {
				// 孤儿响应：没有对应的待处理请求，原实现静默丢弃，此处暴露为 WARN。
				a.logger.Warn("orphan LSP response (no matching pending request), dropped",
					"server", a.name, "id", resp.ID)
				metrics.IncCounter("lsp_malformed_frames_total", "orphan")
			}
		}
	}
}

// truncate 截断字符串用于日志，避免超长 body 污染日志。
func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max] + "..."
}

// isLSPServerErrorLine 判断 LSP 服务器 stderr 行是否值得以 WARN 暴露
// （错误/失败/降级原因），其余行以 DEBUG 记录，避免日志风暴。
func isLSPServerErrorLine(line string) bool {
	lower := strings.ToLower(line)
	for _, kw := range []string{
		"error", "fail", "compile", "missing", "not found",
		"unable", "cannot", "warning", "exception", "panic",
	} {
		if strings.Contains(lower, kw) {
			return true
		}
	}
	return false
}