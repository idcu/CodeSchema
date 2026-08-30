// Package ai 提供标签自动推导和 AI 增强层。
//
// 当前实现：基于规则推导六类标签（layer/biz/tech/risk/test/lang）。
// 后续可扩展 AI 补全（Enhancer）和预算管控（Budget）。
package ai

import (
	"context"
	"path/filepath"
	"strings"

	log "gitee.com/idcu-go/log"
	"github.com/idcu/codeschema/internal/store"
)

// Tagger 负责基于规则自动推导实体标签。
//
// 推导来源：包名、目录结构、类名、方法名、文档注释、文件语言。
// 覆盖六类标签：layer / biz / tech / risk / test / lang
type Tagger struct {
	store  store.Store
	logger *log.Logger
}

// NewTagger 创建标签推导器。
func NewTagger(st store.Store) *Tagger {
	return &Tagger{store: st, logger: log.WithModule("tagger")}
}

// DeriveAllTags 对所有已索引的实体执行标签推导并存储。
//
// 遍历所有文件 -> 类 -> 方法，为每个实体推导标签并写入 Store。
// 错误不影响主流程（单个实体失败继续处理下一个）。
func (t *Tagger) DeriveAllTags(ctx context.Context) error {
	files, err := t.store.GetAllFiles(ctx)
	if err != nil {
		return err
	}

	t.logger.Info("deriving tags for all entities", "files", len(files))

	var classCount, methodCount int
	for _, f := range files {
		classes, err := t.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			continue
		}

		for _, cls := range classes {
			classTags := DeriveClassTags(cls, f)
			if len(classTags) > 0 {
				_ = t.store.UpsertTags(ctx, cls.ID, classTags)
				classCount++
			}

			methods, err := t.store.GetMethodsByClassID(ctx, cls.ID)
			if err != nil {
				continue
			}

			for _, m := range methods {
				methodTags := DeriveMethodTags(m, cls, f)
				if len(methodTags) > 0 {
					_ = t.store.UpsertMethodTags(ctx, m.ID, methodTags)
					methodCount++
				}
			}
		}
	}

	t.logger.Info("tag derivation completed", "classes_tagged", classCount, "methods_tagged", methodCount)
	return nil
}

// DeriveClassTags 为单个类推导标签，返回标签列表。
//
// 推导规则：
//   - layer: 从目录路径或类名后缀推断（controller/service/dao/domain/infra/middleware）
//   - biz:   从包路径中提取业务域名称
//   - tech:  从类名推断技术特征（cache/mq/retry/transactional/async/schedule/batch）
//   - risk:  从文档注释推断风险（todo/deprecated/legacy/performance/security）
//   - test:  从文件路径或类名推断测试类型（unit/integration/mock）
//   - lang:  从文件语言推断
func DeriveClassTags(cls store.ClassRecord, file *store.FileRecord) []string {
	tagSet := make(map[string]bool)

	// 1. layer 标签
	if tag := deriveLayerTag(cls, file); tag != "" {
		tagSet[tag] = true
	}

	// 2. biz 标签
	if tag := deriveBizTag(cls, file); tag != "" {
		tagSet[tag] = true
	}

	// 3. tech 标签
	if tag := deriveClassTechTag(cls); tag != "" {
		tagSet[tag] = true
	}

	// 4. risk 标签
	if tag := deriveClassRiskTag(cls); tag != "" {
		tagSet[tag] = true
	}

	// 5. test 标签
	if tag := deriveTestTag(cls, file); tag != "" {
		tagSet[tag] = true
	}

	// 6. lang 标签
	if file != nil && file.Language != "" {
		tagSet[file.Language] = true
	}

	tags := make([]string, 0, len(tagSet))
	for t := range tagSet {
		tags = append(tags, t)
	}
	return tags
}

// DeriveMethodTags 为单个方法推导标签，返回标签列表。
//
// 推导规则：
//   - tech:  从方法名推断（cache/mq/retry/transactional/async/schedule/batch）
//   - risk:  从文档注释推断（todo/deprecated）
//   - test:  从方法名推断（unit/mock）
func DeriveMethodTags(m store.MethodRecord, _ store.ClassRecord, _ *store.FileRecord) []string {
	tagSet := make(map[string]bool)

	// 1. tech 标签
	if tag := deriveMethodTechTag(m); tag != "" {
		tagSet[tag] = true
	}

	// 2. risk 标签
	if tag := deriveMethodRiskTag(m); tag != "" {
		tagSet[tag] = true
	}

	// 3. test 标签
	if tag := deriveMethodTestTag(m); tag != "" {
		tagSet[tag] = true
	}

	tags := make([]string, 0, len(tagSet))
	for t := range tagSet {
		tags = append(tags, t)
	}
	return tags
}

// ---------- 辅助推导函数 ----------

// layerKeywords 分层关键词及其对应标签名。
var layerKeywords = map[string]string{
	"controller":   "controller",
	"controllers":  "controller",
	"service":      "service",
	"services":     "service",
	"dao":          "dao",
	"daos":         "dao",
	"repository":   "dao",
	"repositories": "dao",
	"domain":       "domain",
	"domains":      "domain",
	"model":        "domain",
	"models":       "domain",
	"entity":       "domain",
	"entities":     "domain",
	"infra":        "infra",
	"infrastructure": "infra",
	"config":       "infra",
	"configs":      "infra",
	"middleware":   "middleware",
	"middlewares":  "middleware",
	"filter":       "middleware",
	"filters":      "middleware",
	"interceptor":  "middleware",
	"interceptors": "middleware",
	"handler":      "handler",
	"handlers":     "handler",
}

// layerClassSuffixes 类名后缀映射到分层标签。
var layerClassSuffixes = map[string]string{
	"Controller": "controller",
	"Service":    "service",
	"Dao":        "dao",
	"DAO":        "dao",
	"Repository": "dao",
	"Repo":       "dao",
	"Domain":     "domain",
	"Entity":     "domain",
	"Model":      "domain",
	"Config":     "infra",
	"Middleware": "middleware",
	"Handler":    "handler",
	"Filter":     "middleware",
	"Interceptor": "middleware",
}

// deriveLayerTag 从目录路径或类名后缀推断分层标签。
func deriveLayerTag(cls store.ClassRecord, file *store.FileRecord) string {
	if file == nil {
		return ""
	}

	path := filepath.ToSlash(file.AbsolutePath)
	parts := strings.Split(path, "/")

	// 策略1: 目录路径匹配
	for _, part := range parts {
		if tag, ok := layerKeywords[strings.ToLower(part)]; ok {
			return tag
		}
	}

	// 策略2: 类名后缀匹配
	for suffix, tag := range layerClassSuffixes {
		if strings.HasSuffix(cls.Name, suffix) {
			return tag
		}
	}

	return ""
}

// deriveBizTag 从目录路径中提取业务域名称。
//
// 策略：从右向左扫描路径段（最靠近文件名），取第一个非关键词的段。
// 例如：/project/internal/order/service.go → "order"
func deriveBizTag(cls store.ClassRecord, file *store.FileRecord) string {
	if file == nil {
		return ""
	}

	path := filepath.ToSlash(file.AbsolutePath)
	parts := strings.Split(path, "/")

	// 从右向左扫描：排除文件名和已知关键词
	for i := len(parts) - 1; i >= 0; i-- {
		part := parts[i]
		lower := strings.ToLower(part)

		// 排除文件扩展名（含文件名）
		if strings.Contains(part, ".") {
			continue
		}
		// 排除空段和单字符段
		if len(part) <= 1 {
			continue
		}
		// 排除已知的项目根目录关键词
		if isSkipWord(lower) {
			continue
		}
		// 排除 layer 关键词
		if _, ok := layerKeywords[lower]; ok {
			continue
		}
		// 排除技术关键词
		if isTechKeyword(lower) {
			continue
		}
		// 排除常见的非业务段
		if isCommonNonBiz(lower) {
			continue
		}
		return part
	}

	return ""
}

// isSkipWord 判断是否为已知的路径前缀关键词。
func isSkipWord(s string) bool {
	words := map[string]bool{
		"src": true, "main": true, "test": true, "java": true, "kotlin": true,
		"internal": true, "pkg": true, "cmd": true, "api": true, "proto": true,
		"resources": true, "webapp": true, "node_modules": true, "vendor": true,
		"gen": true, "generated": true, "third_party": true, "lib": true,
		"project": true, "app": true, "module": true, "packages": true,
		"com": true, "org": true, "net": true, "io": true, "cn": true,
	}
	return words[s]
}

// isTechKeyword 判断是否为技术关键词。
func isTechKeyword(s string) bool {
	techWords := map[string]bool{
		"cache": true, "mq": true, "queue": true, "retry": true,
		"transaction": true, "async": true, "schedule": true, "batch": true,
		"rpc": true, "grpc": true, "http": true, "rest": true,
		"event": true, "stream": true, "socket": true, "ws": true,
	}
	return techWords[s]
}

// isCommonNonBiz 判断是否为常见的非业务路径段。
func isCommonNonBiz(s string) bool {
	words := map[string]bool{
		"common": true, "util": true, "utils": true, "helper": true,
		"helpers": true, "base": true, "core": true, "support": true,
		"shared": true, "misc": true, "tool": true, "tools": true,
	}
	return words[s]
}

// deriveClassTechTag 从类名推断技术标签。
func deriveClassTechTag(cls store.ClassRecord) string {
	name := cls.Name

	// 按优先级检查
	if containsFold(name, "Cache") {
		return "cache"
	}
	if containsFold(name, "Queue") || containsFold(name, "Mq") {
		return "mq"
	}
	if containsFold(name, "Retry") {
		return "retry"
	}
	if containsFold(name, "Transaction") {
		return "transactional"
	}
	if containsFold(name, "Async") {
		return "async"
	}
	if containsFold(name, "Scheduler") || containsFold(name, "Schedule") || containsFold(name, "Cron") {
		return "schedule"
	}
	if containsFold(name, "Batch") {
		return "batch"
	}

	return ""
}

// deriveClassRiskTag 从类名或文档注释推断风险标签。
func deriveClassRiskTag(cls store.ClassRecord) string {
	// 检查类名
	name := cls.Name
	if containsFold(name, "Legacy") {
		return "legacy"
	}
	if containsFold(name, "Deprecated") {
		return "deprecated"
	}
	if containsFold(name, "Performance") {
		return "performance"
	}
	if containsFold(name, "Security") {
		return "security"
	}

	// 检查文档注释
	doc := cls.Doc
	if doc == "" {
		return ""
	}

	upper := strings.ToUpper(doc)
	if strings.Contains(upper, "TODO") || strings.Contains(upper, "FIXME") || strings.Contains(upper, "HACK") {
		return "todo"
	}
	if strings.Contains(upper, "DEPRECATED") {
		return "deprecated"
	}

	return ""
}

// deriveTestTag 从文件路径或类名推断测试标签。
func deriveTestTag(cls store.ClassRecord, file *store.FileRecord) string {
	if file == nil {
		return ""
	}

	path := filepath.ToSlash(file.AbsolutePath)
	lower := strings.ToLower(path)

	// 检查文件路径
	if strings.HasSuffix(lower, "_test.go") ||
		strings.HasSuffix(lower, "_test.py") ||
		strings.HasSuffix(lower, ".test.ts") ||
		strings.HasSuffix(lower, "_spec.rb") ||
		strings.HasSuffix(lower, "_spec.ex") {
		return "unit"
	}

	if strings.Contains(lower, "/test/") ||
		strings.Contains(lower, "/tests/") ||
		strings.Contains(lower, "/testdata/") ||
		strings.Contains(lower, "/__tests__/") ||
		strings.Contains(lower, "/spec/") {
		return "unit"
	}

	if strings.Contains(lower, "/integration/") {
		return "integration"
	}

	// 检查类名
	name := cls.Name
	if strings.HasPrefix(name, "Test") || strings.HasSuffix(name, "Test") ||
		strings.HasPrefix(name, "Mock") || strings.HasSuffix(name, "Mock") {
		return "mock"
	}

	return ""
}

// deriveMethodTechTag 从方法名推断技术标签。
func deriveMethodTechTag(m store.MethodRecord) string {
	name := m.Name

	if containsFold(name, "Cache") {
		return "cache"
	}
	if containsFold(name, "Queue") || containsFold(name, "Mq") || containsFold(name, "Send") {
		return "mq"
	}
	if containsFold(name, "Retry") {
		return "retry"
	}
	if containsFold(name, "Transaction") {
		return "transactional"
	}
	if containsFold(name, "Async") {
		return "async"
	}
	if containsFold(name, "Schedule") || containsFold(name, "Cron") {
		return "schedule"
	}
	if containsFold(name, "Batch") {
		return "batch"
	}

	return ""
}

// deriveMethodRiskTag 从方法文档注释推断风险标签。
func deriveMethodRiskTag(m store.MethodRecord) string {
	doc := m.Doc
	if doc == "" {
		return ""
	}

	upper := strings.ToUpper(doc)
	if strings.Contains(upper, "TODO") || strings.Contains(upper, "FIXME") || strings.Contains(upper, "HACK") {
		return "todo"
	}
	if strings.Contains(upper, "DEPRECATED") {
		return "deprecated"
	}

	return ""
}

// deriveMethodTestTag 从方法名推断测试标签。
func deriveMethodTestTag(m store.MethodRecord) string {
	name := m.Name

	if strings.HasPrefix(name, "Test") || strings.HasPrefix(name, "test") ||
		strings.HasPrefix(name, "Should") || strings.HasPrefix(name, "should") ||
		strings.HasPrefix(name, "Assert") || strings.HasPrefix(name, "assert") ||
		strings.HasPrefix(name, "Mock") || strings.HasPrefix(name, "mock") {
		return "mock"
	}

	return ""
}

// containsFold 检查字符串是否包含子串（大小写不敏感）。
func containsFold(s, substr string) bool {
	s, substr = strings.ToUpper(s), strings.ToUpper(substr)
	return strings.Contains(s, substr)
}