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
	"io"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

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
}

// jsonRPCRequest JSON-RPC 2.0 请求结构。
type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      int    `json:"id"`
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
	return &LSPAdapter{
		name:       name,
		cmd:        cmd,
		args:       args,
		lang:       lang,
		timeout:    timeout,
		pending:    make(map[int]chan<- *jsonRPCResponse),
		aliveCheck: make(chan struct{}),
	}
}

// NewGoplsAdapter 创建 gopls 适配器。
func NewGoplsAdapter() *LSPAdapter {
	return NewLSPAdapter("gopls", "gopls", nil, "go", 10*time.Second)
}

// NewJDTLSAdapter 创建 jdtls 适配器。
func NewJDTLSAdapter() *LSPAdapter {
	return NewLSPAdapter("jdtls", "jdtls", nil, "java", 15*time.Second)
}

// NewClangdAdapter 创建 clangd 适配器。
func NewClangdAdapter() *LSPAdapter {
	return NewLSPAdapter("clangd", "clangd", nil, "cpp", 10*time.Second)
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

	// 捕获 stderr，防止缓冲区满导致进程阻塞
	go func() {
		io.Copy(io.Discard, stderr)
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
	didOpenParams := map[string]any{
		"textDocument": map[string]any{
			"uri":        uri,
			"languageId": a.lang,
			"version":    1,
			"text":       "",
		},
	}
	a.sendNotification("textDocument/didOpen", didOpenParams)

	// 2. 发送 textDocument/documentSymbol 请求
	symbolParams := map[string]any{
		"textDocument": map[string]string{"uri": uri},
	}
	symbolResult, err := a.sendRequest(ctx, "textDocument/documentSymbol", symbolParams)
	if err != nil {
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

	return ir, nil
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
	// LSP SymbolKind: 1=File, 2=Module, 3=Namespace, 4=Package,
	// 5=Class, 6=Method, 7=Property, 8=Field, 9=Constructor, ...
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
	case 6: // Method
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
	case 6: // Method
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
				contentLength, err = strconv.Atoi(strings.TrimSpace(cl))
				if err != nil || contentLength <= 0 {
					contentLength = 0
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
			}
		}
	}
}