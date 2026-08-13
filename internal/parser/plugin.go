package parser

import "context"

// ParserPlugin 是所有适配器的统一契约。
//
// 文件级适配器（tree-sitter、LSP）实现 Parse 方法。
// 批量适配器（SCIP、CodeGraph、JCodeIndexer）应实现 BatchParser 子接口。
type ParserPlugin interface {
	// Name 返回适配器唯一标识。
	Name() string

	// Supports 判断适配器是否支持指定语言。
	Supports(lang string) bool

	// Init 初始化适配器（如启动 LSP 子进程、加载 index 文件）。
	// 在注册后、首次使用前调用一次；返回 nil 表示初始化成功。
	Init(ctx context.Context, config map[string]any) error

	// Close 清理适配器资源（如关闭子进程、释放文件句柄）。
	Close() error

	// Parse 解析单个文件，返回归一化 IR。
	// err == nil 且 IR 为空表示跳过该文件。
	Parse(ctx context.Context, path string) (*IRDocument, error)
}

// BatchParser 是批量适配器的扩展接口。
// 消费 SCIP index 文件、竞品 SQLite 数据库的适配器应实现此接口。
type BatchParser interface {
	ParserPlugin

	// ParseAll 批量解析，返回所有文件 IR 的迭代器通道。
	// 调用方可通过 context 取消，适配器应在取消时关闭 channel。
	ParseAll(ctx context.Context, paths []string) (<-chan *IRDocument, error)
}