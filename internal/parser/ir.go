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
	Source       string // 数据来源标识，如 "treesitter" / "scip-java" / "codegraph"
	Language     string // "go" / "java" / "cpp" / "ts" / "py" / "rust"
	FilePath     string
	FileHash     string   // SHA-256，由编排层填充
	CommitHash   string   // git commit，由编排层填充
	LineCount    int      // 文件总行数（编排层统计）
	ByteSize     int64    // 文件字节大小（编排层 os.Stat）
	ReferencedBy []string // 引用本文件的文件清单（import/include 反向）
	Classes      []ClassIR
	Methods      []MethodIR
	Fields       []FieldIR    // 变量（成员变量/局部变量），2026-09-03 新增
	Constants    []ConstantIR // 常量（包级/类级），2026-09-03 新增
	Calls        []CallIR
	Imports      []string // 文件级 import，辅助跨模块/测试关联
}

// ClassIR 表示类/接口/枚举/抽象类解析结果。
type ClassIR struct {
	Name                                 string
	FullName                             string
	Type                                 string // CLASS / INTERFACE / ABSTRACT / ENUM
	ParentFQNs                           []string
	StartLine, StartCol, EndLine, EndCol int
	Modifier                             string
	Doc                                  string
	Annotations                          []string
	Extra                                map[string]any // 语言差异兜底（JSONB）
}

// MethodIR 表示方法/函数解析结果。
type MethodIR struct {
	Name, Signature, ReturnType          string
	ClassFQN                             string
	StartLine, StartCol, EndLine, EndCol int
	Modifier                             string
	Doc                                  string
	Annotations                          []string
	IsStatic, IsAbstract, IsConstructor  bool
	Params                               []ParamIR
	Extra                                map[string]any
}

// ParamIR 表示方法参数。
type ParamIR struct {
	Name, Type string
	Index      int
	Annotation string
}

// EnsureMethodOwners 为「归属类不在本文件声明中」的方法补齐最小类记录。
//
// Go 允许在同一包的不同文件中为同一类型定义方法：类型声明在 a.go，
// receiver 方法写在 b.go。IR 的 MethodIR.ClassFQN 指向接收者类型，
// 但该类不在 b.go 的 Classes 里；三个存储后端都按「本文件 ClassFQN → classID」
// 映射入库，未命中即静默丢弃（跨文件接收者方法对所有查询不可见）。
//
// 补一条最小类记录（FullName = ClassFQN，不改方法自身 FQN），入库后方法可被
// 索引/调用图/测试关联检索。非跨文件场景（类与方法同文件）不受影响。
func EnsureMethodOwners(doc *IRDocument) {
	if doc == nil || len(doc.Methods) == 0 {
		return
	}
	known := make(map[string]struct{}, len(doc.Classes))
	for _, c := range doc.Classes {
		known[c.FullName] = struct{}{}
	}
	for _, m := range doc.Methods {
		if m.ClassFQN == "" {
			continue
		}
		if _, ok := known[m.ClassFQN]; ok {
			continue
		}
		known[m.ClassFQN] = struct{}{}
		doc.Classes = append(doc.Classes, ClassIR{
			Name:      lastFQNSegment(m.ClassFQN),
			FullName:  m.ClassFQN,
			Type:      "CLASS",
			StartLine: m.StartLine,
			EndLine:   m.EndLine,
		})
	}
}

// lastFQNSegment 返回 FQN 的最后一段（Service / org.foo.Bar → Service / Bar）。
func lastFQNSegment(fqn string) string {
	for i := len(fqn) - 1; i >= 0; i-- {
		if fqn[i] == '.' || fqn[i] == '/' {
			return fqn[i+1:]
		}
	}
	return fqn
}

// FieldIR 表示变量解析结果（成员变量/局部变量）。
// 归属二选一：ClassFQN 非空为成员变量，MethodFQN 非空为局部变量（MethodFQN 形如 ClassFQN.MethodName）。
type FieldIR struct {
	Name                                 string
	Type                                 string
	ClassFQN                             string // 成员变量归属类（裸类名，与 ClassIR.FullName 对齐）
	MethodFQN                            string // 局部变量归属方法（ClassFQN.MethodName）；空表示非局部变量
	IsStatic, IsConst                    bool
	Modifier                             string
	StartLine, StartCol, EndLine, EndCol int
	Extra                                map[string]any // 语言差异兜底
}

// ConstantIR 表示常量解析结果。
// 归属二选一：FilePath 非空为包/文件级常量，ClassFQN 非空为类级常量。
type ConstantIR struct {
	Name, Type, Value string
	FilePath          string // 包/文件级常量归属文件
	ClassFQN          string // 类级常量归属类（裸类名，与 ClassIR.FullName 对齐）
	Modifier          string
	Extra             map[string]any
}

// 调用边来源标识（R5-3）。每条 CallIR 都必须携带来源，便于上层判断精度。
const (
	CallSourceLSP        = "lsp"
	CallSourceSCIP       = "scip"
	CallSourceTreeSitter = "treesitter"
	CallSourceRegex      = "regex"
)

// 调用边置信度（0-100）。数值越高表示来源越能证明「真实调用关系」。
const (
	ConfidenceLSP        = 100
	ConfidenceSCIP       = 100
	ConfidenceTreeSitter = 90
	ConfidenceRegex      = 70
	// ConfidenceExternal 外部索引适配器（CodeGraph/JCodeIndexer）的边精度：
	// 只有符号级边表，无法定位精确调用点，按 scip 来源但降级。
	ConfidenceExternal = 60
)

// NormalizeCallProvenance 归一化调用边来源/置信度。
//
// 历史数据 source 为空时无法追溯来源，按 treesitter/60 解释（最低可信值）；
// 有来源但置信度为 0 时回落到该来源的默认值。
func NormalizeCallProvenance(source string, confidence int) (string, int) {
	if confidence > 0 {
		return source, confidence
	}
	switch source {
	case "":
		return CallSourceTreeSitter, ConfidenceExternal
	case CallSourceLSP:
		return source, ConfidenceLSP
	case CallSourceSCIP:
		return source, ConfidenceSCIP
	case CallSourceTreeSitter:
		return source, ConfidenceTreeSitter
	case CallSourceRegex:
		return source, ConfidenceRegex
	default:
		return source, ConfidenceExternal
	}
}

// CallIR 表示调用关系。
type CallIR struct {
	CallerFQN, CalleeFQN string
	CallType             string // direct / interface / dynamic / unknown；非调用关系另标 imports / references / implements / extends
	LineNumber           int
	Source               string // 边来源：lsp / scip / treesitter / regex（R5-3）
	Confidence           int    // 边置信度 0-100，见 Confidence* 常量（R5-3）
}
