// Package adapterx 是 CodeSchema 解析适配器的对外发布契约层（A 级生态资产聚合包）。
//
// 定位：把 4 个解析适配器（tree-sitter / SCIP / CodeGraph / LSP）沉淀为可独立
// 对外发布的聚合资产。本包自包含（仅依赖标准库），不依赖 internal/*，因此可以
// 直接拷贝为独立仓库 `github.com/idcu/codeschema-adapterx` 对外发布；第三方可按
// 本契约自行实现 ParserPlugin，与 CodeSchema 的解析流水线无缝对接。
//
// 当前仓库内的内部适配器（internal/parser/adapter/*）与本节约定的对应关系：
//
//	adapterx.ParserPlugin      ↔  internal/parser.ParserPlugin
//	adapterx.IRDocument        ↔  internal/parser.IRDocument（桥接见 internal/parser/adapterx.go）
//	adapterx.BuiltinAdapters() ↔  internal/parser/adapter/{treesitter,scip,codegraph,lsp}
//
// 发布形态与路线图见 README.md 与 git 历史（2026-09-02 重构前文档）。
package adapterx

import "context"

// ParserPlugin 是所有解析适配器的统一对外契约。
//
// 文件级适配器（tree-sitter、LSP）实现 Parse；批量适配器（SCIP、CodeGraph）
// 实现 BatchParser 子接口。实现者只需满足本契约即可被任何消费方接入。
type ParserPlugin interface {
	// Name 返回适配器唯一标识（如 "treesitter" / "scip" / "codegraph" / "lsp"）。
	Name() string

	// Supports 判断适配器是否支持指定语言（"go" / "java" / "py" 等）。
	Supports(lang string) bool

	// Init 初始化适配器（启动 LSP 子进程、加载 index 文件等），注册后首次使用前调用一次。
	Init(ctx context.Context, config map[string]any) error

	// Close 清理适配器资源（关闭子进程、释放文件句柄）。
	Close() error

	// Parse 解析单个文件并返回归一化 IR；err == nil 且返回 nil 表示跳过该文件。
	Parse(ctx context.Context, path string) (*IRDocument, error)
}

// BatchParser 是批量适配器的扩展契约（消费 SCIP index 文件、竞品 SQLite 数据库等）。
type BatchParser interface {
	ParserPlugin

	// ParseAll 批量解析，返回所有文件 IR 的迭代器通道；调用方可通过 ctx 取消。
	ParseAll(ctx context.Context, paths []string) (<-chan *IRDocument, error)
}

// IRDocument 是一次解析的归一化产出（对外发布版，与 internal/parser.IRDocument 字段对齐）。
type IRDocument struct {
	Source       string   // 数据来源标识，如 "treesitter" / "scip-java" / "codegraph"
	Language     string   // "go" / "java" / "cpp" / "ts" / "py" / "rust"
	FilePath     string
	FileHash     string   // SHA-256，由编排层填充
	CommitHash   string   // git commit，由编排层填充
	LineCount    int      // 文件总行数（编排层统计）
	ByteSize     int64    // 文件字节大小（编排层 os.Stat）
	ReferencedBy []string // 引用本文件的文件清单（import/include 反向）
	Classes      []ClassIR
	Methods      []MethodIR
	Calls        []CallIR
	Imports      []string // 文件级 import，辅助跨模块/测试关联
}

// ClassIR 表示类/接口/枚举/抽象类解析结果。
type ClassIR struct {
	Name                         string
	FullName                     string
	Type                         string // CLASS / INTERFACE / ABSTRACT / ENUM
	ParentFQNs                   []string
	StartLine, StartCol, EndLine, EndCol int
	Modifier                     string
	Doc                          string
	Annotations                  []string
	Extra                        map[string]any // 语言差异兜底（JSONB）
}

// MethodIR 表示方法/函数解析结果。
type MethodIR struct {
	Name, Signature, ReturnType string
	ClassFQN                    string
	StartLine, StartCol, EndLine, EndCol int
	Modifier                    string
	Doc                         string
	Annotations                 []string
	IsStatic, IsAbstract, IsConstructor bool
	Params                      []ParamIR
	Extra                       map[string]any
}

// ParamIR 表示方法参数。
type ParamIR struct {
	Name, Type string
	Index      int
	Annotation string
}

// CallIR 表示调用关系。
type CallIR struct {
	CallerFQN, CalleeFQN string
	CallType             string // direct / interface / dynamic / unknown
	LineNumber           int
}
