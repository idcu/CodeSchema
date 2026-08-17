// Package adapter 提供适配器层的公共工具函数和常量。
package adapter

import (
	"os"
	"strings"
)

// FileExists 检查文件是否存在。
func FileExists(path string) bool {
	info, err := os.Stat(path)
	if err != nil {
		return false
	}
	return !info.IsDir()
}

// ExtToLang 将文件扩展名映射到语言标识。
// 与 scanner.detectLang 保持同步。
func ExtToLang(ext string) string {
	switch strings.ToLower(ext) {
	case ".go":
		return "go"
	case ".java":
		return "java"
	case ".py":
		return "py"
	case ".ts", ".tsx":
		return "ts"
	case ".js", ".jsx":
		return "js"
	case ".rs":
		return "rust"
	case ".cpp", ".cc", ".cxx":
		return "cpp"
	case ".c":
		return "c"
	case ".h", ".hpp":
		return "cpp"
	case ".kt", ".kts":
		return "kotlin"
	case ".swift":
		return "swift"
	case ".php":
		return "php"
	case ".cs":
		return "csharp"
	case ".rb":
		return "ruby"
	case ".sh", ".bash":
		return "bash"
	case ".scala", ".sc":
		return "scala"
	case ".sql":
		return "sql"
	case ".ex", ".exs":
		return "elixir"
	case ".ml", ".mli":
		return "ocaml"
	case ".lua":
		return "lua"
	case ".groovy":
		return "groovy"
	case ".css":
		return "css"
	case ".toml":
		return "toml"
	case ".yml", ".yaml":
		return "yaml"
	case ".proto":
		return "protobuf"
	case ".html", ".htm":
		return "html"
	case ".tf", ".hcl":
		return "hcl"
	case ".svelte":
		return "svelte"
	case ".md", ".markdown":
		return "markdown"
	case "Dockerfile":
		return "dockerfile"
	case ".elm":
		return "elm"
	case ".cue":
		return "cue"
	default:
		return "unknown"
	}
}
