// Package adapter 提供适配器层的公共工具函数和常。
package adapter

import (
	"os"
	"strings"
)

// SupportedLanguages 返回适配器层支持的所有语言列表。
func SupportedLanguages() []string {
	return []string{"go", "java", "ts", "js", "py", "rust", "cpp", "c", "kotlin", "swift", "php", "csharp", "ruby"}
}

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
	default:
		return "unknown"
	}
}

// IsSourceFile 判断文件扩展名是否属于可识别的源码文件。
func IsSourceFile(path string) bool {
	ext := ""
	if idx := strings.LastIndex(path, "."); idx >= 0 {
		ext = path[idx:]
	}
	return ExtToLang(ext) != "unknown"
}

// LangToExtensions 返回指定语言的所有文件扩展名。
func LangToExtensions(lang string) []string {
	switch lang {
	case "go":
		return []string{".go"}
	case "java":
		return []string{".java"}
	case "py":
		return []string{".py"}
	case "ts":
		return []string{".ts", ".tsx"}
	case "js":
		return []string{".js", ".jsx"}
	case "rust":
		return []string{".rs"}
	case "cpp":
		return []string{".cpp", ".cc", ".cxx", ".h", ".hpp"}
	case "c":
		return []string{".c"}
	case "kotlin":
		return []string{".kt", ".kts"}
	case "swift":
		return []string{".swift"}
	case "php":
		return []string{".php"}
	case "csharp":
		return []string{".cs"}
	case "ruby":
		return []string{".rb"}
	default:
		return nil
	}
}
