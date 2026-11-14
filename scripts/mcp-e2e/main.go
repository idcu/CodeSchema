// Command mcp-e2e 使用官方 MCP Go SDK 对 CodeSchema 做 stdio + SSE 端到端验证。
//
// 该工具刻意不复用仓库内的自写客户端，而是引入
// github.com/modelcontextprotocol/go-sdk（规范实现）完成 initialize / tools/list /
// tools/call，并把完整 transcript 落盘，作为「规范客户端可接入」的证据。
//
// 用法（在仓库根目录）：
//
//	make mcp-e2e
//	# 或
//	cd scripts/mcp-e2e && go run . -repo ../.. -out ../../docs/bench/2026-11-14
package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

const clientName = "codeschema-mcp-e2e"

type callRecord struct {
	Tool      string         `json:"tool"`
	Arguments map[string]any `json:"arguments"`
	IsError   bool           `json:"is_error"`
	Text      string         `json:"text"`
}

type transcript struct {
	Transport       string       `json:"transport"`
	GeneratedAt     string       `json:"generated_at"`
	Client          string       `json:"client"`
	ProtocolVersion string       `json:"protocol_version"`
	ServerName      string       `json:"server_name"`
	ServerVersion   string       `json:"server_version"`
	Tools           []string     `json:"tools"`
	Calls           []callRecord `json:"calls"`
	ServerStderr    string       `json:"server_stderr,omitempty"`
	Error           string       `json:"error,omitempty"`
	Pass            bool         `json:"pass"`
}

func main() {
	repo := flag.String("repo", ".", "codeschema 仓库根目录")
	outDir := flag.String("out", "docs/bench/2026-11-14", "transcript 输出目录")
	flag.Parse()

	if err := run(*repo, *outDir); err != nil {
		log.Fatalf("mcp-e2e FAILED: %v", err)
	}
}

func run(repo, outDir string) error {
	repo, err := filepath.Abs(repo)
	if err != nil {
		return err
	}
	if _, err := os.Stat(filepath.Join(repo, "cmd", "codeschema")); err != nil {
		return fmt.Errorf("repo 参数不是 codeschema 根目录: %w", err)
	}
	if err := os.MkdirAll(outDir, 0o755); err != nil {
		return err
	}

	tmp, err := os.MkdirTemp("", "codeschema-mcp-e2e-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(tmp)

	bin := filepath.Join(tmp, "codeschema")
	fmt.Printf("==> build %s\n", bin)
	if err := runIn(repo, "go", "build", "-o", bin, "./cmd/codeschema"); err != nil {
		return err
	}

	proj := filepath.Join(tmp, "proj")
	if err := writeFixture(proj); err != nil {
		return err
	}
	cfg := filepath.Join(tmp, "config.yaml")
	if err := writeConfig(cfg, proj, tmp); err != nil {
		return err
	}

	fmt.Printf("==> scan fixture %s\n", proj)
	if err := runIn(proj, bin, "--config", cfg, "scan", proj); err != nil {
		return err
	}

	var failures []string

	stdioTr := runStdio(bin, cfg, proj)
	if !stdioTr.Pass {
		failures = append(failures, "stdio: "+stdioTr.Error)
	}
	if err := writeTranscript(filepath.Join(outDir, "mcp-transcript-stdio.json"), stdioTr); err != nil {
		return err
	}
	fmt.Printf("stdio: pass=%v tools=%d calls=%d\n", stdioTr.Pass, len(stdioTr.Tools), len(stdioTr.Calls))

	sseTr := runSSE(bin, cfg, proj)
	if !sseTr.Pass {
		failures = append(failures, "sse: "+sseTr.Error)
	}
	if err := writeTranscript(filepath.Join(outDir, "mcp-transcript-sse.json"), sseTr); err != nil {
		return err
	}
	fmt.Printf("sse:   pass=%v tools=%d calls=%d\n", sseTr.Pass, len(sseTr.Tools), len(sseTr.Calls))

	if len(failures) > 0 {
		return errors.New(strings.Join(failures, "; "))
	}
	fmt.Println("==> OK: 官方 MCP Go SDK 在 stdio 与 SSE 两种传输上均完成 initialize/list/call")
	return nil
}

// runStdio 通过官方 SDK 的 CommandTransport 拉起 `codeschema mcp --stdio`。
func runStdio(bin, cfg, proj string) *transcript {
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.Command(bin, "--config", cfg, "mcp", "--stdio")
	cmd.Dir = proj
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	tr := newTranscript("stdio")
	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.CommandTransport{Command: cmd}, nil)
	if err != nil {
		tr.Error = fmt.Sprintf("connect: %v", err)
		tr.ServerStderr = tail(stderr.String(), 4096)
		return tr
	}
	tr.ServerStderr = tail(stderr.String(), 4096)
	collect(ctx, session, tr)
	if err := session.Close(); err != nil {
		tr.Error = fmt.Sprintf("close: %v", err)
		tr.Pass = false
		return tr
	}
	tr.ServerStderr = tail(stderr.String(), 4096)
	return tr
}

// runSSE 启动 `codeschema mcp --addr <host:port>`，用官方 SDK 的 SSEClientTransport 连接。
func runSSE(bin, cfg, proj string) *transcript {
	tr := newTranscript("sse")

	port, err := freePort()
	if err != nil {
		tr.Error = fmt.Sprintf("free port: %v", err)
		return tr
	}
	addr := fmt.Sprintf("127.0.0.1:%d", port)

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	cmd := exec.Command(bin, "--config", cfg, "mcp", "--addr", addr)
	cmd.Dir = proj
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		tr.Error = fmt.Sprintf("start server: %v", err)
		return tr
	}
	defer stopProcess(cmd)

	if err := waitTCP(ctx, addr, 20*time.Second); err != nil {
		tr.Error = fmt.Sprintf("server not ready: %v", err)
		tr.ServerStderr = tail(stderr.String(), 4096)
		return tr
	}

	client := mcp.NewClient(&mcp.Implementation{Name: clientName, Version: "1.0.0"}, nil)
	session, err := client.Connect(ctx, &mcp.SSEClientTransport{Endpoint: "http://" + addr + "/sse"}, nil)
	if err != nil {
		tr.Error = fmt.Sprintf("connect: %v", err)
		tr.ServerStderr = tail(stderr.String(), 4096)
		return tr
	}
	collect(ctx, session, tr)
	if err := session.Close(); err != nil {
		tr.Error = fmt.Sprintf("close: %v", err)
		tr.Pass = false
	}
	tr.ServerStderr = tail(stderr.String(), 4096)
	return tr
}

func newTranscript(transport string) *transcript {
	return &transcript{
		Transport:   transport,
		GeneratedAt: time.Now().Format(time.RFC3339),
		Client:      clientName,
	}
}

// collect 复用同一组断言：initialize 结果、工具清单、三次工具调用。
func collect(ctx context.Context, session *mcp.ClientSession, tr *transcript) {
	init := session.InitializeResult()
	if init == nil {
		tr.Error = "initialize 返回空结果"
		return
	}
	tr.ProtocolVersion = init.ProtocolVersion
	if init.ServerInfo != nil {
		tr.ServerName = init.ServerInfo.Name
		tr.ServerVersion = init.ServerInfo.Version
	}

	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		tr.Error = fmt.Sprintf("tools/list: %v", err)
		return
	}
	for _, t := range tools.Tools {
		tr.Tools = append(tr.Tools, t.Name)
	}

	calls := []callRecord{
		{Tool: "search_config", Arguments: map[string]any{"pattern": "Hello"}},
		{Tool: "context", Arguments: map[string]any{"symbol": "Greeter.Hello", "context_lines": 2}},
		{Tool: "get_call_graph", Arguments: map[string]any{"symbol": "Greeter.Hello", "depth": 1}},
	}
	var problems []string
	for i := range calls {
		res, err := session.CallTool(ctx, &mcp.CallToolParams{Name: calls[i].Tool, Arguments: calls[i].Arguments})
		if err != nil {
			calls[i].IsError = true
			calls[i].Text = err.Error()
			problems = append(problems, fmt.Sprintf("%s: %v", calls[i].Tool, err))
			continue
		}
		calls[i].IsError = res.IsError
		calls[i].Text = contentText(res.Content)
		if res.IsError {
			problems = append(problems, fmt.Sprintf("%s: tool error: %s", calls[i].Tool, tail(calls[i].Text, 200)))
		}
	}
	tr.Calls = calls

	if len(tr.Tools) != 12 {
		problems = append(problems, fmt.Sprintf("tools/list 返回 %d 个工具，期望 12", len(tr.Tools)))
	}
	for _, want := range []string{"context", "search_symbols", "get_call_graph", "list_projects"} {
		if !contains(tr.Tools, want) {
			problems = append(problems, "缺少工具 "+want)
		}
	}
	if len(problems) > 0 {
		tr.Error = strings.Join(problems, "; ")
		return
	}
	tr.Pass = true
}

func contentText(content []mcp.Content) string {
	var b strings.Builder
	for _, c := range content {
		if tc, ok := c.(*mcp.TextContent); ok {
			b.WriteString(tc.Text)
		}
	}
	return tail(b.String(), 8192)
}

func writeTranscript(path string, tr *transcript) error {
	raw, err := json.MarshalIndent(tr, "", "  ")
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		return err
	}
	fmt.Printf("==> transcript %s\n", path)
	return nil
}

func writeFixture(dir string) error {
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	files := map[string]string{
		"go.mod": "module example.com/demo\n\ngo 1.21\n",
		"greeter.go": `package demo

// Greeter 提供问候能力。
type Greeter struct {
	Prefix string
}

// Hello 返回带前缀的问候语。
func (g *Greeter) Hello(name string) string {
	return g.Prefix + " " + name
}

// CallHello 调用 Greeter.Hello。
func CallHello(g *Greeter, name string) string {
	return g.Hello(name)
}
`,
		"greeter_test.go": `package demo

import "testing"

func TestGreeterHello(t *testing.T) {
	g := &Greeter{Prefix: "hi"}
	if got := g.Hello("x"); got != "hi x" {
		t.Fatalf("got %q", got)
	}
}
`,
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
			return err
		}
	}
	return nil
}

func writeConfig(path, proj, tmp string) error {
	cfg := fmt.Sprintf(`project:
  name: "mcp-e2e"
  root: %q
  languages: [go]
storage:
  driver: file
  dsn: %q
  vector:
    # 指向不存在的本地分发源：跳过启动期 5 分钟远程模型下载（本 e2e 只验证 MCP 协议，
    # 语义检索关闭），下载失败后运行时按既有逻辑降级 LocalEmbedder。
    model_download_url: %q
  search:
    fts: true
    semantic: false
    fts_dir: %q
    vector_dir: %q
server:
  mcp_addr: "127.0.0.1:18099"
  http_addr: ""
`, proj, filepath.Join(tmp, "data"), "file://"+filepath.Join(tmp, "missing-model.tar.gz"),
		filepath.Join(tmp, "data", "fts"), filepath.Join(tmp, "data", "vector"))
	return os.WriteFile(path, []byte(cfg), 0o644)
}

func runIn(dir, name string, args ...string) error {
	cmd := exec.Command(name, args...)
	cmd.Dir = dir
	var out bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("%s %v: %w\n%s", name, args, err, tail(out.String(), 4096))
	}
	return nil
}

func freePort() (int, error) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return 0, err
	}
	defer l.Close()
	return l.Addr().(*net.TCPAddr).Port, nil
}

func waitTCP(ctx context.Context, addr string, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		conn, err := net.DialTimeout("tcp", addr, 300*time.Millisecond)
		if err == nil {
			_ = conn.Close()
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(100 * time.Millisecond):
		}
	}
	return fmt.Errorf("timeout waiting for %s", addr)
}

func stopProcess(cmd *exec.Cmd) {
	if cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(os.Interrupt)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
}

func tail(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[len(s)-n:]
}

func contains(list []string, want string) bool {
	for _, v := range list {
		if v == want {
			return true
		}
	}
	return false
}
