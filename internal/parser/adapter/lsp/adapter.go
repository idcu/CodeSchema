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
	"os/exec"
	"strconv"
	"sync"
	"time"

	"codeschema/internal/errors"
	"codeschema/internal/parser"
)

// LSPAdapter 通用 LSP 适配器，通过 JSON-RPC 2.0 与 LSP 服务器通信。
type LSPAdapter struct {
	name    string
	cmd     string
	args    []string
	lang    string
	timeout time.Duration

	mu       sync.Mutex
	cmdObj   *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	msgID    int
	pending  map[int]chan<- *jsonRPCResponse
	initOnce sync.Once
	cancel   context.CancelFunc
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
		name:    name,
		cmd:     cmd,
		args:    args,
		lang:    lang,
		timeout: timeout,
		pending: make(map[int]chan<- *jsonRPCResponse),
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
func (a *LSPAdapter) Init(ctx context.Context, config map[string]any) error {
	a.mu.Lock()
	defer a.mu.Unlock()

	ctx, a.cancel = context.WithCancel(ctx)

	// 启动 LSP 子进程
	cmd := exec.CommandContext(ctx, a.cmd, a.args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return fmt.Errorf("cannot create stdin pipe for %s: %w", a.name, err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return fmt.Errorf("cannot create stdout pipe for %s: %w", a.name, err)
	}

	if err := cmd.Start(); err != nil {
		return fmt.Errorf("cannot start %s: %w: %w", a.name, err, errors.ErrSourceUnavailable)
	}

	a.cmdObj = cmd
	a.stdin = stdin
	a.stdout = stdout

	// 启动异步响应读取协程
	go a.readResponses()

	// 发送 initialize 请求
	initParams := map[string]any{
		"processId":             nil,
		"clientInfo":            map[string]string{"name": "codeschema"},
		"capabilities":          map[string]any{},
		"workspaceFolders":      nil,
		"rootUri":               nil,
	}
	_, err = a.sendRequest(ctx, "initialize", initParams)
	if err != nil {
		cmd.Process.Kill()
		return fmt.Errorf("%s initialize failed: %w: %w", a.name, err, errors.ErrSourceUnavailable)
	}

	// 发送 initialized 通知
	a.sendNotification("initialized", map[string]any{})

	return nil
}

// Close 清理适配器资源，终止 LSP 子进程。
func (a *LSPAdapter) Close() error {
	a.mu.Lock()
	defer a.mu.Unlock()

	if a.cancel != nil {
		a.cancel()
	}

	if a.stdin != nil {
		a.stdin.Close()
	}

	if a.cmdObj != nil && a.cmdObj.Process != nil {
		// 发送 shutdown 请求
		a.sendNotification("shutdown", nil)
		a.sendNotification("exit", nil)
		_ = a.cmdObj.Process.Kill()
	}

	return nil
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
		return nil, ctx.Err()
	case <-time.After(a.timeout):
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
func (a *LSPAdapter) readResponses() {
	reader := bufio.NewReader(a.stdout)
	buf := bytes.NewBuffer(make([]byte, 0, 4096))

	for {
		// 1. 读取头直到 \r\n\r\n
		header, err := reader.ReadString('\n')
		if err != nil {
			return // 流关闭或出错，正常退出
		}

		// 跳过空行（上一个消息后的剩余 \r\n）
		if header == "\r\n" || header == "\n" {
			continue
		}

		// 解析 Content-Length
		if !bytes.HasPrefix([]byte(header), []byte("Content-Length: ")) {
			continue
		}
		contentLengthStr := header[len("Content-Length: "):]
		contentLengthStr = contentLengthStr[:len(contentLengthStr)-2] // 去掉 \r\n
		contentLength, err := strconv.Atoi(contentLengthStr)
		if err != nil || contentLength <= 0 {
			continue
		}

		// 2. 跳过 \r\n\r\n 后的空行（即 \r\n）
		_, _ = reader.Discard(2) // 跳过 \r\n

		// 3. 读取精确的 Content-Length 字节
		buf.Reset()
		buf.Grow(contentLength)
		_, err = io.CopyN(buf, reader, int64(contentLength))
		if err != nil {
			return
		}

		// 4. 解析 JSON
		body := buf.Bytes()
		var resp jsonRPCResponse
		if err := json.Unmarshal(body, &resp); err != nil {
			continue
		}

		// 5. 分发响应
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