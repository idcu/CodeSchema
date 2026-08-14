//go:build treesitter

package adapterbench

// benchASTPath 报告当前构建是否启用 -tags treesitter（真语法树路径）。
func benchASTPath() bool { return true }
