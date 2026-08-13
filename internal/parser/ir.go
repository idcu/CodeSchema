// Package parser 定义解析适配中间层的核心接口与数据结构。
//
// 设计原则：
// 1. 核心不依赖任何具体解析器——编译期可剔除任意适配器。
// 2. 只定义 ParserPlugin 接口与统一 IRDocument。
// 3. 适配器可独立编译、测试、启停，数据来源可追溯。
// 4. 竞品是"被集成的上游数据源"，不内嵌其代码。
package parser

// IRDocument 是一次解析的归一化产出。
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
	ClassFQN                   string
	StartLine, StartCol, EndLine, EndCol int
	Modifier                   string
	Doc                        string
	Annotations                []string
	IsStatic, IsAbstract, IsConstructor bool
	Params                     []ParamIR
	Extra                      map[string]any
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