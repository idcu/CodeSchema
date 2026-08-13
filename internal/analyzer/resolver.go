package analyzer

import "strings"

// ImportResolver 语言无关的 import 解析器接口。
//
// 每种语言实现一个 Resolver，负责将该语言的 import 路径
// 解析为文件 store 中已知的文件路径。
type ImportResolver interface {
	// Resolve 解析 import 路径，返回匹配的文件路径列表。
	// 如果无法解析，返回 nil。
	Resolve(imp string, importIndex map[string][]string) []string
}

// javaStdlibPrefixes Java 标准库和常用框架的包前缀，这些不应该匹配文件 store。
var javaStdlibPrefixes = []string{
	"java.", "javax.", "jdk.", "sun.", "oracle.",
	"org.junit.", "org.mockito.", "org.hamcrest.",
	"org.slf4j.", "org.apache.logging.", "org.apache.log4j.",
	"org.springframework.", "org.springframework.boot.",
	"com.fasterxml.", "com.google.common.", "com.google.gson.",
	"io.netty.", "org.apache.commons.",
	"lombok.",
}

// GoResolver 解析 Go 模块 import 路径。
//
// 策略：匹配 modulePath 前缀后，去掉前缀得到包目录路径，在索引中查找。
// 例如：module="codeschema", imp="github.com/idcu/codeschema/internal/store" → "internal/store"
type GoResolver struct {
	modulePath string
}

// NewGoResolver 创建 Go 解析器。
func NewGoResolver(modulePath string) *GoResolver {
	return &GoResolver{modulePath: modulePath}
}

// Resolve 实现 ImportResolver 接口。
func (r *GoResolver) Resolve(imp string, importIndex map[string][]string) []string {
	if r.modulePath == "" || !strings.HasPrefix(imp, r.modulePath+"/") {
		return nil
	}
	pkgDir := strings.TrimPrefix(imp, r.modulePath+"/")
	if targets, ok := importIndex[pkgDir]; ok {
		return targets
	}
	// 尝试用包目录的最后一段匹配
	parts := strings.Split(pkgDir, "/")
	last := parts[len(parts)-1]
	if targets, ok := importIndex[last]; ok {
		return targets
	}
	return nil
}

// JavaResolver 解析 Java Maven/Gradle 包路径。
//
// 支持：
//   - FQCN 导入：com.example.service.UserService → com/example/service/UserService
//   - 通配符导入：com.example.* → com/example/ 目录下所有文件
//   - 可配置的 Java 标准库过滤（java.*, javax.*, lombok. 等）
//   - 源根目录剥离（src/main/java/ 等）
type JavaResolver struct {
	sourceRoots    []string // 源根目录列表，如 ["src/main/java", "src/main/kotlin", "src/test/java"]
	stdlibPrefixes []string // 标准库/框架前缀，匹配这些前缀的 import 将被跳过
}

// defaultJavaSourceRoots 默认 Java 源根目录。
var defaultJavaSourceRoots = []string{
	"src/main/java",
	"src/main/kotlin",
	"src/test/java",
	"src/test/kotlin",
}

// NewJavaResolver 创建 Java 解析器。
//
// sourceRoots 为源根目录列表，传 nil 或空切片使用默认值。
// 标准库前缀使用默认列表（含 java.*/javax.*/org.springframework.* 等 23 个）。
func NewJavaResolver(sourceRoots []string) *JavaResolver {
	if len(sourceRoots) == 0 {
		sourceRoots = defaultJavaSourceRoots
	}
	// 深拷贝默认前缀，避免外部修改影响默认值
	prefixes := make([]string, len(javaStdlibPrefixes))
	copy(prefixes, javaStdlibPrefixes)
	return &JavaResolver{
		sourceRoots:    sourceRoots,
		stdlibPrefixes: prefixes,
	}
}

// SetStdlibPrefixes 设置自定义标准库/框架前缀列表。
//
// 覆盖默认的 23 个前缀。传入空切片表示不过滤任何 import。
// 示例：r.SetStdlibPrefixes([]string{"java.", "javax."})
func (r *JavaResolver) SetStdlibPrefixes(prefixes []string) {
	r.stdlibPrefixes = prefixes
}

// AddStdlibPrefix 追加一个标准库/框架前缀。
func (r *JavaResolver) AddStdlibPrefix(prefix string) {
	r.stdlibPrefixes = append(r.stdlibPrefixes, prefix)
}

// Resolve 实现 ImportResolver 接口。
func (r *JavaResolver) Resolve(imp string, importIndex map[string][]string) []string {
	// 1. 跳过 Java 标准库
	if r.isJavaStdlib(imp) {
		return nil
	}

	// 2. 处理通配符导入：com.example.*
	if strings.HasSuffix(imp, ".*") {
		pkgPath := strings.TrimSuffix(imp, ".*")
		slashPath := strings.ReplaceAll(pkgPath, ".", "/")
		return r.matchWildcard(slashPath, importIndex)
	}

	// 3. 处理 FQCN 导入：com.example.service.UserService
	slashPath := strings.ReplaceAll(imp, ".", "/")
	if targets, ok := importIndex[slashPath]; ok {
		return targets
	}

	// 4. 尝试带源根目录前缀匹配
	// 例如：imp="com.example.service.UserService" → slashPath="com/example/service/UserService"
	// 文件路径可能包含 "src/main/java/com/example/service/UserService"
	for _, root := range r.sourceRoots {
		key := root + "/" + slashPath
		if targets, ok := importIndex[key]; ok {
			return targets
		}
		// 也尝试只匹配最后一段
		parts := strings.Split(slashPath, "/")
		last := parts[len(parts)-1]
		key = root + "/" + last
		if targets, ok := importIndex[key]; ok {
			return targets
		}
		// 尝试逐级匹配源根目录
		// 例如 imp="com.example" → 可能匹配 "src/main/java/com/example"
		if len(parts) <= 3 {
			key = root + "/" + slashPath
			if targets, ok := importIndex[key]; ok {
				return targets
			}
		}
	}

	return nil
}

// matchWildcard 处理通配符导入，匹配目录下所有文件。
func (r *JavaResolver) matchWildcard(pkgDir string, importIndex map[string][]string) []string {
	seen := make(map[string]bool)
	var result []string

	// 方法 1: 直接匹配索引键前缀
	for key, targets := range importIndex {
		if strings.HasPrefix(key, pkgDir+"/") {
			for _, t := range targets {
				if !seen[t] {
					seen[t] = true
					result = append(result, t)
				}
			}
		}
	}
	if len(result) > 0 {
		return result
	}

	// 方法 2: 带源根目录前缀匹配
	for _, root := range r.sourceRoots {
		prefix := root + "/" + pkgDir + "/"
		for key, targets := range importIndex {
			if strings.HasPrefix(key, prefix) {
				for _, t := range targets {
					if !seen[t] {
						seen[t] = true
						result = append(result, t)
					}
				}
			}
		}
		if len(result) > 0 {
			return result
		}
	}

	return nil
}

// isJavaStdlib 判断是否为 Java 标准库或常见框架的 import。
//
// 使用实例的 stdlibPrefixes 列表，而非全局变量。
// 可通过 SetStdlibPrefixes 自定义。
func (r *JavaResolver) isJavaStdlib(imp string) bool {
	for _, prefix := range r.stdlibPrefixes {
		if strings.HasPrefix(imp, prefix) {
			return true
		}
	}
	return false
}

// GradleResolver 解析 Gradle 多模块项目路径。
//
// Gradle 多模块使用 : 作为模块路径分隔符，例如：
//   - :app:service:UserService → app/service/UserService（含类名）
//   - :core:domain:* → core/domain/ 下所有文件（通配符）
//   - :lib:common → lib/common 包（不含类名，仅包路径）
//
// 支持：
//   - 前导 : 自动剥离
//   - : 到 / 的转换
//   - 与 JavaResolver 相同的源根目录匹配
//   - 可选的模块名白名单（如 ["app", "core", "lib"]）
//   - 通配符导入（:module:*）
type GradleResolver struct {
	sourceRoots    []string // 源根目录列表
	moduleNames    []string // 模块名白名单，空表示不限制
	stdlibPrefixes []string // 标准库前缀（复用 JavaResolver 的过滤规则）
}

// NewGradleResolver 创建 Gradle 多模块解析器。
//
// sourceRoots 为源根目录列表，传 nil 或空切片使用默认值。
// moduleNames 为模块名白名单，传 nil 表示不限制。
func NewGradleResolver(sourceRoots []string, moduleNames []string) *GradleResolver {
	if len(sourceRoots) == 0 {
		sourceRoots = defaultJavaSourceRoots
	}
	prefixes := make([]string, len(javaStdlibPrefixes))
	copy(prefixes, javaStdlibPrefixes)
	return &GradleResolver{
		sourceRoots:    sourceRoots,
		moduleNames:    moduleNames,
		stdlibPrefixes: prefixes,
	}
}

// Resolve 实现 ImportResolver 接口。
//
// 解析策略：
//   - 仅处理以 : 开头的 Gradle 多模块路径
//   - 第一个 : 段为模块名（如 :app → module "app"）
//   - 后续段为模块内的路径（如 :app:controller:UserController → 模块内 controller/UserController）
//   - 模块名 + 源根目录 + 内部路径 组合匹配（如 app/src/main/java/controller/UserController）
func (r *GradleResolver) Resolve(imp string, importIndex map[string][]string) []string {
	// 仅处理 Gradle 格式的 import（以 : 开头）
	if !strings.HasPrefix(imp, ":") {
		return nil
	}

	// 标准库过滤
	if r.isStdlib(imp) {
		return nil
	}

	// 剥离前导 :，按 : 分割
	path := strings.TrimPrefix(imp, ":")
	parts := strings.Split(path, ":")

	// 模块名白名单检查
	moduleName := parts[0]
	if len(r.moduleNames) > 0 && !r.isModuleAllowed(moduleName) {
		return nil
	}

	// 模块内路径：后续段以 / 连接
	internalPath := ""
	if len(parts) > 1 {
		internalPath = strings.Join(parts[1:], "/")
	}

	// 构建完整的模块路径：moduleName/internalPath
	modulePath := moduleName
	if internalPath != "" {
		modulePath = moduleName + "/" + internalPath
	}

	// 处理通配符：:module:submodule:*
	if strings.HasSuffix(modulePath, "/*") {
		pkgDir := strings.TrimSuffix(modulePath, "/*")
		return r.matchWildcard(pkgDir, importIndex)
	}

	// 策略 1: 直接匹配模块路径
	// 例如:modulePath="app/controller/UserController" → 匹配索引键 "app/controller/UserController"
	if targets, ok := importIndex[modulePath]; ok {
		return targets
	}

	// 策略 2: 模块名 + 源根目录 + 内部路径
	// 例如: moduleName="app", root="src/main/java", internalPath="controller/UserController"
	// → 键="app/src/main/java/controller/UserController"
	for _, root := range r.sourceRoots {
		key := moduleName + "/" + root + "/" + internalPath
		if targets, ok := importIndex[key]; ok {
			return targets
		}
	}

	// 策略 3: 源根目录 + 完整模块路径
	// 例如: root="src/main/java", modulePath="app/controller/UserController"
	// → 键="src/main/java/app/controller/UserController"
	for _, root := range r.sourceRoots {
		key := root + "/" + modulePath
		if targets, ok := importIndex[key]; ok {
			return targets
		}
	}

	// 策略 4: 仅匹配最后一段（类名/包名）
	lastParts := strings.Split(modulePath, "/")
	last := lastParts[len(lastParts)-1]
	if targets, ok := importIndex[last]; ok {
		return targets
	}
	for _, root := range r.sourceRoots {
		key := root + "/" + last
		if targets, ok := importIndex[key]; ok {
			return targets
		}
		key = moduleName + "/" + root + "/" + last
		if targets, ok := importIndex[key]; ok {
			return targets
		}
	}

	return nil
}

// matchWildcard 处理 Gradle 通配符导入，匹配模块目录下所有文件。
func (r *GradleResolver) matchWildcard(pkgDir string, importIndex map[string][]string) []string {
	seen := make(map[string]bool)
	var result []string

	// 从 pkgDir 中提取模块名和内部路径
	// pkgDir 格式为 "moduleName/internalPath" 或 "moduleName"
	slashIdx := strings.Index(pkgDir, "/")
	moduleName := pkgDir
	internalPath := ""
	if slashIdx > 0 {
		moduleName = pkgDir[:slashIdx]
		internalPath = pkgDir[slashIdx+1:]
	}

	// 策略 1: 直接前缀匹配 pkgDir/
	for key, targets := range importIndex {
		if strings.HasPrefix(key, pkgDir+"/") {
			for _, t := range targets {
				if !seen[t] {
					seen[t] = true
					result = append(result, t)
				}
			}
		}
	}
	if len(result) > 0 {
		return result
	}

	// 策略 2: 模块名 + 源根目录 + 内部路径前缀
	if internalPath != "" {
		for _, root := range r.sourceRoots {
			prefix := moduleName + "/" + root + "/" + internalPath + "/"
			for key, targets := range importIndex {
				if strings.HasPrefix(key, prefix) {
					for _, t := range targets {
						if !seen[t] {
							seen[t] = true
							result = append(result, t)
						}
					}
				}
			}
			if len(result) > 0 {
				return result
			}
		}
	}

	// 策略 3: 源根目录 + 完整路径前缀
	for _, root := range r.sourceRoots {
		prefix := root + "/" + pkgDir + "/"
		for key, targets := range importIndex {
			if strings.HasPrefix(key, prefix) {
				for _, t := range targets {
					if !seen[t] {
						seen[t] = true
						result = append(result, t)
					}
				}
			}
		}
		if len(result) > 0 {
			return result
		}
	}

	return nil
}

// isModuleAllowed 检查模块名是否在白名单中。
func (r *GradleResolver) isModuleAllowed(moduleName string) bool {
	for _, name := range r.moduleNames {
		if name == moduleName {
			return true
		}
	}
	return false
}

// isStdlib 判断是否为标准库 import。
//
// 同时检查 . 和 : 两种分隔符格式：
//   - java.lang.String（标准 FQCN）
//   - java:lang:String（Gradle : 分隔符）
func (r *GradleResolver) isStdlib(imp string) bool {
	// 去掉前导 :
	path := strings.TrimPrefix(imp, ":")
	// 将 : 替换为 . 统一检查
	normalized := strings.ReplaceAll(path, ":", ".")
	for _, prefix := range r.stdlibPrefixes {
		if strings.HasPrefix(normalized, prefix) {
			return true
		}
	}
	return false
}

// SetStdlibPrefixes 设置自定义标准库前缀列表。
func (r *GradleResolver) SetStdlibPrefixes(prefixes []string) {
	r.stdlibPrefixes = prefixes
}

// CompositeResolver 组合多个解析器，按优先级依次尝试。
//
// 每个解析器按注册顺序尝试，一旦某个解析器返回非空结果，立即返回。
// 如果所有解析器都返回空，回退到启发式匹配。
type CompositeResolver struct {
	resolvers []ImportResolver // 按优先级从高到低排列
}

// NewCompositeResolver 创建组合解析器。
func NewCompositeResolver(resolvers ...ImportResolver) *CompositeResolver {
	return &CompositeResolver{resolvers: resolvers}
}

// AddResolver 添加解析器到末尾。
func (c *CompositeResolver) AddResolver(r ImportResolver) {
	c.resolvers = append(c.resolvers, r)
}

// Resolve 实现 ImportResolver 接口。
//
// 依次尝试每个注册的解析器，若都返回空，回退到启发式匹配。
func (c *CompositeResolver) Resolve(imp string, importIndex map[string][]string) []string {
	for _, resolver := range c.resolvers {
		if targets := resolver.Resolve(imp, importIndex); len(targets) > 0 {
			return targets
		}
	}
	return nil
}

// heuristicResolver 启发式解析器，作为最终的 fallback。
type heuristicResolver struct{}

// Resolve 实现 ImportResolver 接口。
//
// 策略：
//  1. 直接匹配 import 路径
//  2. 提取最后一段（包名/文件名）匹配
//  3. 将 "." 替换为 "/" 后匹配
func (h *heuristicResolver) Resolve(imp string, importIndex map[string][]string) []string {
	// 策略 1: 直接匹配
	if targets, ok := importIndex[imp]; ok {
		return targets
	}

	// 策略 2: 提取最后一段
	parts := strings.Split(imp, "/")
	last := parts[len(parts)-1]
	if targets, ok := importIndex[last]; ok {
		return targets
	}

	// 策略 3: 将 "." 替换为 "/" 后匹配
	dotPath := strings.ReplaceAll(imp, ".", "/")
	if dotPath != imp {
		if targets, ok := importIndex[dotPath]; ok {
			return targets
		}
		dotParts := strings.Split(dotPath, "/")
		if len(dotParts) > 0 {
			dotLast := dotParts[len(dotParts)-1]
			if targets, ok := importIndex[dotLast]; ok {
				return targets
			}
		}
	}

	return nil
}