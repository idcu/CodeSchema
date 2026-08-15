package main

import (
	"fmt"
	"strings"
)

// printMCPClientConfigs 打印各主流 AI 客户端的 MCP 接入配置片段（T2-5）。
//
// 用法：codeschema mcp --print-config [--addr :8080] [--auth-token xxx]
// 输出 VS Code / JetBrains / Claude Code / Cursor / stdio 桥接五类配置，
// 用户可直接复制使用，无需查阅文档。
func printMCPClientConfigs(addr, authToken string) {
	// 规范化地址：无 scheme 补 http://；仅端口（:8080）补 localhost 主机名
	base := addr
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		if strings.HasPrefix(base, ":") {
			base = "localhost" + base
		}
		base = "http://" + base
	}
	sseURL := strings.TrimSuffix(base, "/") + "/sse"

	var authNote string
	if authToken != "" {
		authNote = fmt.Sprintf("（启用认证：Authorization: Bearer %s）", authToken)
	}

	fmt.Printf(`CodeSchema MCP Server 接入配置
===============================
MCP Server 地址: %s
SSE 端点: %s%s

【1. VS Code】项目根 .vscode/mcp.json:
{
  "servers": {
    "codeschema": {
      "type": "sse",
      "url": "%s"
    }
  }
}

【2. JetBrains IDEs（IntelliJ / GoLand / PyCharm）】
Settings → Tools → MCP → 添加:
{
  "mcpServers": {
    "codeschema": {
      "url": "%s"
    }
  }
}

【3. Claude Code】
claude mcp add codeschema --transport http %s

【4. Cursor】项目根 .cursor/mcp.json:
{
  "mcpServers": {
    "codeschema": {
      "url": "%s"
    }
  }
}

【5. 仅支持 stdio 的客户端（npx mcp-remote 桥接）】
{
  "mcpServers": {
    "codeschema": {
      "command": "npx",
      "args": ["mcp-remote", "%s"]
    }
  }
}

可用工具（11 个）：context / impact / tests / affected / get_call_graph /
search_config / find_dependencies / search_symbols / get_tags / search_by_tag / get_all_tags

启动服务：codeschema mcp --addr %s
`, sseURL, sseURL, authNote, sseURL, sseURL, sseURL, sseURL, sseURL, addr)
}
