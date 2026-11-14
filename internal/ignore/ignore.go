// Package ignore 定义扫描与文件监听共用的忽略清单，避免 scanner 与 fswatch
// 各自维护一份导致口径漂移（R6-5）。
package ignore

// DefaultDirs 默认忽略的目录名（与主流工具链约定一致）。
var DefaultDirs = []string{
	".git", "node_modules", "target", "build", "vendor",
	".idea", ".vscode", "__pycache__",
}

// DefaultFiles 默认忽略的数据文件（store 目录位于仓库根时，避免把持久化文件
// 当作源码收录，污染文件清单与集成测试断言）。
var DefaultFiles = []string{
	"store.json", "store.lock", "store.db",
}

// DirSet 返回目录忽略集合（默认清单 + extra）。
func DirSet(extra ...string) map[string]bool {
	return setOf(DefaultDirs, extra)
}

// FileSet 返回文件忽略集合（默认清单 + extra）。
func FileSet(extra ...string) map[string]bool {
	return setOf(DefaultFiles, extra)
}

func setOf(base, extra []string) map[string]bool {
	m := make(map[string]bool, len(base)+len(extra))
	for _, v := range base {
		m[v] = true
	}
	for _, v := range extra {
		m[v] = true
	}
	return m
}
