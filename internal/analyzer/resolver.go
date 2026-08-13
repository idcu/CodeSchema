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
// 例如：module="codeschema", imp="codeschema/internal/store" → "internal/store"
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
//   - 自动跳过 Java 标准库（java.*, javax.* 等）
//   - 源根目录剥离（src/main/java/ 等）
type JavaResolver struct {
	sourceRoots []string // 源根目录列表，如 ["src/main/java", "src/main/kotlin", "src/test/java"]
}

// defaultJavaSourceRoots 默认 Java 源根目录。
var defaultJavaSourceRoots = []string{
	"src/main/java",
	"src/main/kotlin",
	"src/test/java",
	"src/test/kotlin",
}

// NewJavaResolver 创建 Java 解析器。
func NewJavaResolver(sourceRoots []string) *JavaResolver {
	if len(sourceRoots) == 0 {
		sourceRoots = defaultJavaSourceRoots
	}
	return &JavaResolver{sourceRoots: sourceRoots}
}

// Resolve 实现 ImportResolver 接口。
func (r *JavaResolver) Resolve(imp string, importIndex map[string][]string) []string {
	// 1. 跳过 Java 标准库
	if isJavaStdlib(imp) {
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
func isJavaStdlib(imp string) bool {
	for _, prefix := range javaStdlibPrefixes {
		if strings.HasPrefix(imp, prefix) {
			return true
		}
	}
	return false
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