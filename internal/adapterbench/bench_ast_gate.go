//go:build !treesitter

package adapterbench

// benchASTPath 报告当前构建是否启用 -tags treesitter（真语法树路径）。
// 默认构建（正则启发式）返回 false：AST 路径精度门槛仅在其启用时生效。
func benchASTPath() bool { return false }
