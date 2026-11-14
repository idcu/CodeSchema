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

	log "gitee.com/idcu-go/log"
	"gitee.com/idcu-go/metrics"
	"gitee.com/idcu-go/retry"
	"github.com/idcu/codeschema/internal/errors"
	"github.com/idcu/codeschema/internal/parser"
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

	// callsSupported 由 initialize 响应的 callHierarchyProvider 能力决定；
	// 不支持时只产出符号，不发起 callHierarchy 请求（避免无效往返）。
	callsSupported bool

	logger         *log.Logger   // 结构化日志，暴露降级原因，避免"静默丢信息"
	retryAttempts  int           // documentSymbol 等高频请求的可重试次数（含首次）
	retryBaseDelay time.Duration // 重试退避基础延迟
	retryMaxDelay  time.Duration // 重试退避最大延迟
}

// maxCallHierarchyMethods 单文件最多为多少个方法抽取调用边。
// callHierarchy 是「每方法 2 次请求」的协议，必须设上限，避免大文件放大解析延迟。
const maxCallHierarchyMethods = 64

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
		logger:     log.WithModule("lsp:" + name),
		// 重试以覆盖 LSP 子进程的瞬时抖动（超时、连接重置），不放大单文件解析延迟。
		retryAttempts:  2,
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
		"processId":  nil,
		"clientInfo": map[string]string{"name": "codeschema"},
		// 声明支持分层 DocumentSymbol：否则服务器按规范返回扁平 SymbolInformation[]，
		// 容器信息只能来自 containerName，嵌套类/方法的层级会丢失。
		"capabilities": map[string]any{
			"textDocument": map[string]any{
				"documentSymbol": map[string]any{
					"hierarchicalDocumentSymbolSupport": true,
				},
			},
		},
		"workspaceFolders": nil,
		"rootUri":          rootURI,
	}
	initResult, err := a.sendRequest(ctx, "initialize", initParams)
	if err != nil {
		a.logger.Warn("LSP initialize failed, degrading to fallback parser",
			"server", a.name, "error", err.Error())
		if cmd.Process != nil {
			_ = cmd.Process.Kill()
		}
		// 回收子进程并清理 pending：否则初始化失败的短暂进程会变僵尸，
		// 且挂起的请求永远等不到响应。
		_ = cmd.Wait()
		a.mu.Lock()
		a.alive = false
		a.cmdObj = nil
		a.stdin = nil
		a.pending = make(map[int]chan<- *jsonRPCResponse)
		a.mu.Unlock()
		if a.cancel != nil {
			a.cancel()
		}
		return fmt.Errorf("%s initialize failed: %w: %w", a.name, err, errors.ErrSourceUnavailable)
	}

	a.mu.Lock()
	a.callsSupported = supportsCallHierarchy(initResult)
	a.mu.Unlock()

	// 发送 initialized 通知（已释放锁，不会与 sendNotification 内部锁冲突）
	a.sendNotification("initialized", map[string]any{})

	return nil
}

// Close 清理适配器资源，优雅终止 LSP 子进程。
//
// 顺序很重要：先发 shutdown/exit 再关 stdin（旧实现先关 stdin，通知必然写失败），
// 然后 cmd.Wait() 回收进程（避免僵尸）；进程不配合时 2s 后强杀。
// 可重复调用。
func (a *LSPAdapter) Close() error {
	a.mu.Lock()
	a.alive = false
	stdin := a.stdin
	cmdObj := a.cmdObj
	cancel := a.cancel
	a.mu.Unlock()

	if cmdObj != nil && cmdObj.Process != nil && stdin != nil {
		// shutdown 是请求（有 id），exit 是通知；顺序符合 LSP 生命周期约定。
		sctx, scancel := context.WithTimeout(context.Background(), time.Second)
		_, _ = a.sendRequest(sctx, "shutdown", nil)
		scancel()
		writeNotification(stdin, "exit", nil)
	}
	if stdin != nil {
		_ = stdin.Close()
	}

	if cmdObj != nil && cmdObj.Process != nil {
		done := make(chan struct{})
		go func() {
			_ = cmdObj.Wait()
			close(done)
		}()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			_ = cmdObj.Process.Kill()
			<-done
		}
	}
	if cancel != nil {
		cancel()
	}

	a.mu.Lock()
	a.stdin = nil
	a.cmdObj = nil
	a.pending = make(map[int]chan<- *jsonRPCResponse)
	a.mu.Unlock()
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

	// 3. 解析符号结果：按负载形状区分 DocumentSymbol[]（分层）与
	// SymbolInformation[]（扁平）。注意不能先试 SymbolInformation——两者字段不重叠，
	// 前者反序列化到后者会"成功但字段全空"，导致容器/层级静默丢失（P0-2）。
	ir := &parser.IRDocument{
		Source:   a.name,
		Language: a.lang,
		FilePath: path,
	}
	sites := a.applySymbols(ir, uri, symbolResult)

	// 3.1 调用边：仅当服务器声明 callHierarchyProvider 时按方法逐个抽取（尽力而为，
	// 失败只记指标，不影响符号结果）。
	a.collectCalls(ctx, uri, sites, ir)

	// 4. 发送 didClose 通知
	a.sendNotification("textDocument/didClose", map[string]any{
		"textDocument": map[string]string{"uri": uri},
	})

	// 5. 可观测性 + 空结果回退：clangd 缺项目上下文 / 结构无法识别时会返回空符号，
	// 原实现静默返回空 IR 并写 hash，后续扫描永久跳过（P2-11）。这里显式返回
	// ErrSourceUnavailable，交由 FallbackParser 回退 tree-sitter。
	if a.maybeReportEmptySymbols(path, text, ir) {
		return nil, fmt.Errorf("%s returned no symbols for non-empty %s source: %w",
			a.name, a.lang, errors.ErrSourceUnavailable)
	}

	return ir, nil
}

// applySymbols 解析 documentSymbol 响应并写入 IR，返回方法位置（供调用边抽取）。
//
// 负载形状判定：任一条目含 range 且不含 location → DocumentSymbol[]；
// 否则按 SymbolInformation[] 处理。
func (a *LSPAdapter) applySymbols(ir *parser.IRDocument, uri string, symbolResult json.RawMessage) []methodSite {
	var raw []json.RawMessage
	if err := json.Unmarshal(symbolResult, &raw); err != nil || len(raw) == 0 {
		return nil
	}

	var sites []methodSite
	if isDocumentSymbolPayload(raw) {
		var docs []documentSymbol
		if err := json.Unmarshal(symbolResult, &docs); err != nil {
			return nil
		}
		for _, ds := range docs {
			a.addDocumentSymbol(ir, ds, "")
			collectDocumentSites(ds, "", &sites)
		}
		return sites
	}

	var syms []symbolInfo
	if err := json.Unmarshal(symbolResult, &syms); err != nil {
		return nil
	}
	for _, sym := range syms {
		a.addSymbolInfo(ir, sym)
		if sym.Location.URI == uri && isMethodKind(sym.Kind) {
			container, name := memberIdentity(sym.ContainerName, sym.Name)
			sites = append(sites, methodSite{
				fqn:  joinFQN(container, name),
				line: sym.Location.Range.Start.Line,
				char: sym.Location.Range.Start.Character,
			})
		}
	}
	return sites
}

// isDocumentSymbolPayload 判断响应是否为分层 DocumentSymbol[]。
func isDocumentSymbolPayload(raw []json.RawMessage) bool {
	for _, item := range raw {
		var probe struct {
			Range    json.RawMessage `json:"range"`
			Location json.RawMessage `json:"location"`
		}
		if json.Unmarshal(item, &probe) != nil {
			continue
		}
		if len(probe.Range) > 0 && len(probe.Location) == 0 {
			return true
		}
		if len(probe.Location) > 0 {
			return false
		}
	}
	return false
}

// methodSite 是一次调用边抽取的起点（方法 FQN + 0-based selectionRange 起点）。
type methodSite struct {
	fqn        string
	line, char int
}

// collectDocumentSites 递归收集方法位置；容器名同样逐层传递。
func collectDocumentSites(ds documentSymbol, container string, out *[]methodSite) {
	switch ds.Kind {
	case 5, 10, 11, 23: // Class / Enum / Interface / Struct
		for _, child := range ds.Children {
			collectDocumentSites(child, joinFQN(container, ds.Name), out)
		}
	case 6, 9, 12: // Method / Constructor / Function
		*out = append(*out, methodSite{
			fqn:  joinFQN(memberIdentity(container, ds.Name)),
			line: ds.SelectionRange.Start.Line,
			char: ds.SelectionRange.Start.Character,
		})
		for _, child := range ds.Children {
			collectDocumentSites(child, container, out)
		}
	}
}

// maybeReportEmptySymbols 在非空源文件却解析出 0 个符号时显式告警并记录指标。
// 返回 true 表示应回退到兜底解析器（空文件不算异常，返回 false）。
func (a *LSPAdapter) maybeReportEmptySymbols(path, text string, ir *parser.IRDocument) bool {
	if strings.TrimSpace(text) == "" || len(ir.Classes) != 0 || len(ir.Methods) != 0 {
		return false
	}
	hint := "LSP returned zero symbols; server may be missing project context"
	if a.lang == "cpp" {
		hint = "ensure compile_commands.json exists (cmake -DCMAKE_EXPORT_COMPILE_COMMANDS=ON or bear -- make)"
	}
	a.logger.Warn("LSP returned zero symbols for non-empty file; falling back to tree-sitter",
		"server", a.name, "lang", a.lang, "path", path, "hint", hint)
	metrics.IncCounter("lsp_parse_empty_symbols_total", a.lang)
	return true
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
	Name     string `json:"name"`
	Kind     int    `json:"kind"`
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

// joinFQN 拼接容器限定名；容器为空时返回裸名（避免产生 ".Foo" 这样的前导点）。
func joinFQN(container, name string) string {
	if container == "" {
		return name
	}
	return container + "." + name
}

// splitMemberName 拆分 LSP 常见的成员名形态：
//   - Go/gopls: "(*Calculator).Add"
//   - Java/C++: "(Calculator).Add"
//
// 无接收者前缀时返回 ("", name)。
func splitMemberName(name string) (container, member string) {
	if !strings.HasPrefix(name, "(") {
		return "", name
	}
	i := strings.Index(name, ")")
	if i <= 0 {
		return "", name
	}
	inner := strings.TrimSpace(name[1:i])
	member = strings.TrimPrefix(strings.TrimSpace(name[i+1:]), ".")
	// 兼容 "c *Calculator" 这种带变量名的接收者。
	if k := strings.LastIndex(inner, " "); k >= 0 {
		inner = inner[k+1:]
	}
	inner = strings.TrimPrefix(inner, "*")
	return inner, member
}

// memberIdentity 归一化容器与方法名：优先使用服务器给出的 containerName，
// 否则从 "(*Type).Method" 形态的名称中还原容器（gopls 常只给后者）。
func memberIdentity(containerName, name string) (string, string) {
	if c, m := splitMemberName(name); c != "" {
		return c, m
	}
	return containerName, name
}

// isMethodKind 判断 LSP SymbolKind 是否方法/函数/构造函数。
func isMethodKind(kind int) bool {
	switch kind {
	case 6, 9, 12:
		return true
	}
	return false
}

// addSymbolInfo 将 SymbolInformation 添加到 IRDocument。
func (a *LSPAdapter) addSymbolInfo(ir *parser.IRDocument, sym symbolInfo) *parser.IRDocument {
	// LSP SymbolKind（规范）：5=Class, 6=Method, 9=Constructor, 10=Enum,
	// 11=Interface, 12=Function, 23=Struct。
	// 旧实现把 Constructor(9) 当类、把 Interface(11) 漏掉、把 Event(24) 误当接口。
	container, name := memberIdentity(sym.ContainerName, sym.Name)
	fullName := joinFQN(container, name)
	switch sym.Kind {
	case 5:
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name: name, FullName: fullName, Type: "CLASS",
			StartLine: sym.Location.Range.Start.Line + 1,
			StartCol:  sym.Location.Range.Start.Character + 1,
			EndLine:   sym.Location.Range.End.Line + 1,
			EndCol:    sym.Location.Range.End.Character + 1,
		})
	case 10:
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name: name, FullName: fullName, Type: "ENUM",
			StartLine: sym.Location.Range.Start.Line + 1,
			StartCol:  sym.Location.Range.Start.Character + 1,
			EndLine:   sym.Location.Range.End.Line + 1,
			EndCol:    sym.Location.Range.End.Character + 1,
		})
	case 11:
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name: name, FullName: fullName, Type: "INTERFACE",
			StartLine: sym.Location.Range.Start.Line + 1,
			StartCol:  sym.Location.Range.Start.Character + 1,
			EndLine:   sym.Location.Range.End.Line + 1,
			EndCol:    sym.Location.Range.End.Character + 1,
		})
	case 23:
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name: name, FullName: fullName, Type: "STRUCT",
			StartLine: sym.Location.Range.Start.Line + 1,
			StartCol:  sym.Location.Range.Start.Character + 1,
			EndLine:   sym.Location.Range.End.Line + 1,
			EndCol:    sym.Location.Range.End.Character + 1,
		})
	case 6, 9, 12:
		ir.Methods = append(ir.Methods, parser.MethodIR{
			Name:          name,
			ClassFQN:      container,
			IsConstructor: sym.Kind == 9,
			StartLine:     sym.Location.Range.Start.Line + 1,
			StartCol:      sym.Location.Range.Start.Character + 1,
			EndLine:       sym.Location.Range.End.Line + 1,
			EndCol:        sym.Location.Range.End.Character + 1,
		})
	}
	return ir
}

// addDocumentSymbol 将 DocumentSymbol 递归添加到 IRDocument。
// container 为父容器限定名（顶层为空），逐层拼接后分别写入
// ClassIR.FullName 与 MethodIR.ClassFQN——这是方法能入库、调用边能对齐的前提。
func (a *LSPAdapter) addDocumentSymbol(ir *parser.IRDocument, ds documentSymbol, container string) *parser.IRDocument {
	// gopls 的分层 documentSymbol 方法名仍带接收者前缀（如 "(*Calculator).Add"），
	// 统一还原为容器 + 裸方法名，保证入库 FQN 与调用边对齐。
	container, name := memberIdentity(container, ds.Name)
	fullName := joinFQN(container, name)
	switch ds.Kind {
	case 5: // Class
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name: name, FullName: fullName, Type: "CLASS",
			StartLine: ds.Range.Start.Line + 1,
			StartCol:  ds.Range.Start.Character + 1,
			EndLine:   ds.Range.End.Line + 1,
			EndCol:    ds.Range.End.Character + 1,
		})
		container = fullName
	case 10: // Enum
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name: name, FullName: fullName, Type: "ENUM",
			StartLine: ds.Range.Start.Line + 1,
			StartCol:  ds.Range.Start.Character + 1,
			EndLine:   ds.Range.End.Line + 1,
			EndCol:    ds.Range.End.Character + 1,
		})
		container = fullName
	case 11: // Interface
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name: name, FullName: fullName, Type: "INTERFACE",
			StartLine: ds.Range.Start.Line + 1,
			StartCol:  ds.Range.Start.Character + 1,
			EndLine:   ds.Range.End.Line + 1,
			EndCol:    ds.Range.End.Character + 1,
		})
		container = fullName
	case 23: // Struct
		ir.Classes = append(ir.Classes, parser.ClassIR{
			Name: name, FullName: fullName, Type: "STRUCT",
			StartLine: ds.Range.Start.Line + 1,
			StartCol:  ds.Range.Start.Character + 1,
			EndLine:   ds.Range.End.Line + 1,
			EndCol:    ds.Range.End.Character + 1,
		})
		container = fullName
	case 6, 9, 12: // Method / Constructor / Function
		ir.Methods = append(ir.Methods, parser.MethodIR{
			Name:          name,
			ClassFQN:      container,
			IsConstructor: ds.Kind == 9,
			StartLine:     ds.Range.Start.Line + 1,
			StartCol:      ds.Range.Start.Character + 1,
			EndLine:       ds.Range.End.Line + 1,
			EndCol:        ds.Range.End.Character + 1,
		})
	}

	// 递归处理子符号：类用新容器，方法沿用当前容器（嵌套函数仍归属同一类）。
	for _, child := range ds.Children {
		ir = a.addDocumentSymbol(ir, child, container)
	}
	return ir
}

// callHierarchyItem 对应 LSP CallHierarchyItem。
type callHierarchyItem struct {
	Name           string        `json:"name"`
	Kind           int           `json:"kind"`
	Detail         string        `json:"detail,omitempty"`
	URI            string        `json:"uri"`
	Range          documentRange `json:"range"`
	SelectionRange documentRange `json:"selectionRange"`
}

// callHierarchyOutgoingCall 对应 LSP CallHierarchyOutgoingCall。
type callHierarchyOutgoingCall struct {
	To         callHierarchyItem `json:"to"`
	FromRanges []documentRange   `json:"fromRanges"`
}

// supportsCallHierarchy 判断 initialize 响应是否声明 callHierarchyProvider。
func supportsCallHierarchy(initResult json.RawMessage) bool {
	var res struct {
		Capabilities struct {
			CallHierarchyProvider json.RawMessage `json:"callHierarchyProvider"`
		} `json:"capabilities"`
	}
	if len(initResult) == 0 || json.Unmarshal(initResult, &res) != nil {
		return false
	}
	v := strings.TrimSpace(string(res.Capabilities.CallHierarchyProvider))
	return v != "" && v != "null" && v != "false"
}

// collectCalls 通过 callHierarchy 抽取方法调用边（尽力而为）。
func (a *LSPAdapter) collectCalls(ctx context.Context, uri string, sites []methodSite, ir *parser.IRDocument) {
	if len(sites) == 0 {
		return
	}
	a.mu.Lock()
	supported := a.callsSupported
	a.mu.Unlock()
	if !supported {
		return
	}
	if len(sites) > maxCallHierarchyMethods {
		a.logger.Debug("call hierarchy method cap reached; remaining methods skipped",
			"server", a.name, "total", len(sites), "cap", maxCallHierarchyMethods)
		sites = sites[:maxCallHierarchyMethods]
	}

	// 同文件方法名 → FQN：调用边优先落到本文件的容器限定名。
	local := make(map[string]string, len(ir.Methods))
	for _, m := range ir.Methods {
		if _, ok := local[m.Name]; !ok {
			local[m.Name] = joinFQN(m.ClassFQN, m.Name)
		}
	}

	seen := make(map[string]struct{})
	for _, site := range sites {
		items, err := a.prepareCallHierarchy(ctx, uri, site)
		if err != nil {
			metrics.IncCounter("lsp_parse_errors_total", "call_hierarchy")
			continue
		}
		for _, item := range items {
			calls, err := a.outgoingCalls(ctx, item)
			if err != nil {
				metrics.IncCounter("lsp_parse_errors_total", "call_hierarchy")
				continue
			}
			for _, c := range calls {
				cContainer, cName := memberIdentity("", c.To.Name)
				callee := local[cName]
				if callee == "" {
					if cContainer != "" {
						callee = joinFQN(cContainer, cName)
					} else {
						callee = qualifiedCallName(cName, c.To.Detail)
					}
				}
				if callee == "" {
					continue
				}
				key := site.fqn + "->" + callee
				if _, ok := seen[key]; ok {
					continue
				}
				seen[key] = struct{}{}
				line := 0
				if len(c.FromRanges) > 0 {
					line = c.FromRanges[0].Start.Line + 1
				}
				ir.Calls = append(ir.Calls, parser.CallIR{
					CallerFQN:  site.fqn,
					CalleeFQN:  callee,
					CallType:   "direct",
					LineNumber: line,
					Source:     parser.CallSourceLSP,
					Confidence: parser.ConfidenceLSP,
				})
			}
		}
	}
	if len(ir.Calls) > 0 {
		metrics.IncCounter("lsp_calls_total", a.name)
	}
}

func (a *LSPAdapter) prepareCallHierarchy(ctx context.Context, uri string, site methodSite) ([]callHierarchyItem, error) {
	raw, err := a.sendRequest(ctx, "textDocument/prepareCallHierarchy", map[string]any{
		"textDocument": map[string]string{"uri": uri},
		"position":     map[string]int{"line": site.line, "character": site.char},
	})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var items []callHierarchyItem
	if err := json.Unmarshal(raw, &items); err != nil {
		return nil, err
	}
	return items, nil
}

func (a *LSPAdapter) outgoingCalls(ctx context.Context, item callHierarchyItem) ([]callHierarchyOutgoingCall, error) {
	raw, err := a.sendRequest(ctx, "callHierarchy/outgoingCalls", map[string]any{"item": item})
	if err != nil {
		return nil, err
	}
	if len(raw) == 0 || string(raw) == "null" {
		return nil, nil
	}
	var calls []callHierarchyOutgoingCall
	if err := json.Unmarshal(raw, &calls); err != nil {
		return nil, err
	}
	return calls, nil
}

// qualifiedCallName 在无法从本文件方法表解析 callee 时，尝试从 detail 推导容器限定名。
// 支持 Go `func (r *Type) Name(...)` 与 C++ `Type::Name` 两种常见形态。
func qualifiedCallName(name, detail string) string {
	if name == "" {
		return ""
	}
	if i := strings.Index(detail, "func ("); i >= 0 {
		rest := detail[i+len("func ("):]
		if j := strings.Index(rest, ")"); j > 0 {
			fields := strings.Fields(strings.TrimSpace(rest[:j]))
			if len(fields) > 0 {
				typ := strings.TrimPrefix(fields[len(fields)-1], "*")
				if k := strings.LastIndex(typ, "."); k >= 0 {
					typ = typ[k+1:]
				}
				if typ != "" {
					return typ + "." + name
				}
			}
		}
	}
	if i := strings.LastIndex(detail, "::"); i > 0 {
		prefix := detail[:i]
		if k := strings.LastIndexAny(prefix, " \t("); k >= 0 {
			prefix = prefix[k+1:]
		}
		if prefix != "" {
			return prefix + "." + name
		}
	}
	return name
}

// writeNotification 向指定 writer 写一条 JSON-RPC 通知（无 id）。
// 与 sendNotification 的区别：不依赖适配器当前状态，供 Close 在清空字段后使用。
func writeNotification(w io.Writer, method string, params any) {
	req := jsonRPCRequest{JSONRPC: "2.0", Method: method, Params: params}
	data, err := json.Marshal(req)
	if err != nil {
		return
	}
	header := fmt.Sprintf("Content-Length: %d\r\n\r\n", len(data))
	_, _ = w.Write([]byte(header + string(data)))
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
//
//	Content-Length: <N>\r\n\r\n<JSON BODY (N bytes)>
//
// JSON 体可能包含换行符（如 pretty-printed JSON），
// 因此必须按字节读取 Content-Length 指定的长度，而非逐行读取。
//
// 稳定性质保：
//   - 内部 panic 会被 recover，不会导致整个适配器崩溃
//   - 支持多行头（Content-Type 等额外头被忽略但不破坏协议解析）
//   - 退出时自动关闭 aliveCheck 通道并置 alive=false
func (a *LSPAdapter) readResponses() {
	// 捕获本次运行的 aliveCheck：Init 重试会替换该字段，若退出时读字段可能误关新通道。
	a.mu.Lock()
	aliveCh := a.aliveCheck
	a.mu.Unlock()
	defer func() {
		_ = recover() // 记录 panic 但不崩溃，让编排层通过 IsAlive() 检测到异常
		a.mu.Lock()
		a.alive = false
		a.mu.Unlock()
		close(aliveCh)
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
