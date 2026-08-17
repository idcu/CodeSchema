package adapterx

// AdapterInfo 描述一个内置适配器的对外元数据（发布清单用）。
type AdapterInfo struct {
	// Name 适配器唯一标识，与 ParserPlugin.Name 一致。
	Name string
	// Language 主攻语言（空 = 通用/多语言）。
	Language string
	// Kind 适配器类型：file（文件级）/ batch（批量 index/DB 级）。
	Kind string
	// Description 能力与来源说明。
	Description string
}

// BuiltinAdapters 返回 4 个内置解析适配器的发布元数据清单。
//
// 与当前仓库内部实现对应：internal/parser/adapter/
//
//	{treesitter → adapter/treesitter, scip → adapter/scip,
//	 codegraph → adapter/codegraph, lsp → adapter/lsp}
func BuiltinAdapters() []AdapterInfo {
	return []AdapterInfo{
		{Name: "treesitter", Kind: "file", Language: "go,java,py,ts,rust,cpp 等 30+",
			Description: "通用文件级解析适配器（tree-sitter 文法，CGO 可选，默认纯 Go 正则兜底）"},
		{Name: "scip", Kind: "batch", Language: "java,ts 等",
			Description: "消费 SCIP index 文件（scipout/），高精度结构索引"},
		{Name: "codegraph", Kind: "batch", Language: "java,go 等",
			Description: "直读竞品 CodeGraph SQLite 数据库，含 schema 漂移探测"},
		{Name: "lsp", Kind: "file", Language: "go,java,cpp",
			Description: "经语言服务器（gopls/jdtls/clangd）解析，失败自动回退 tree-sitter"},
	}
}
