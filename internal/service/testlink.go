// Package service 提供测试关联策略实现。
//
// 五种策略（按置信度降序）：
//   - explicit (100): 显式注解匹配（预留，P1 实现）
//   - naming   (70):  命名约定（OrderServiceTest ↔ OrderService）
//   - coverage (90):  覆盖率数据反查（预留，P1 实现）
//   - same_tag (60):  同 Tag 聚类
//   - dependency (80): 导入依赖递归
package service

import (
	"context"
	"fmt"
	"strings"

	"codeschema/internal/store"
)

// TestLink 测试关联结果。
type TestLink struct {
	TestMethod string `json:"test_method"`
	MethodName string `json:"method_name"`
	Strategy   string `json:"strategy"`
	Confidence int    `json:"confidence"`
}

// FindTestLinks 查找指定方法的所有关联单测（按置信度降序）。
//
// 实现三种策略：
//   - naming: 类名约定匹配（如 OrderService ↔ OrderServiceTest）
//   - same_tag: 同 Tag 聚类
//   - dependency: 导入依赖递归
func (s *Service) FindTestLinks(ctx context.Context, methodFQN string, minConfidence int) ([]TestLink, error) {
	if methodFQN == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "methodFQN is required"}
	}
	if minConfidence <= 0 {
		minConfidence = 60
	}

	// 收集所有结果，去重
	type linkKey struct {
		testMethod string
		strategy   string
	}
	seen := make(map[linkKey]bool)
	var results []TestLink

	addResult := func(testMethod, methodName, strategy string, confidence int) {
		key := linkKey{testMethod: testMethod, strategy: strategy}
		if seen[key] {
			return
		}
		// 排除自身
		if testMethod == methodFQN {
			return
		}
		seen[key] = true
		results = append(results, TestLink{
			TestMethod: testMethod,
			MethodName: methodName,
			Strategy:   strategy,
			Confidence: confidence,
		})
	}

	// 获取所有文件数据
	files, err := s.store.GetAllFiles(ctx)
	if err != nil {
		return nil, fmt.Errorf("get all files: %w", err)
	}

	// 查找目标方法所在的类
	targetClass := extractClassFQN(methodFQN)
	allTestClasses := s.discoverTestClasses(ctx, files)

	// 1. naming 策略：类名匹配（OrderService ↔ OrderServiceTest）
	for classFQN, testClassFQN := range allTestClasses {
		if classFQN == targetClass {
			// 找到该类下的所有方法，关联到对应测试类下的方法
			s.linkByNaming(ctx, files, classFQN, testClassFQN, methodFQN, addResult)
		}
	}

	// 2. same_tag 策略：同 Tag 聚类
	if len(results) < 20 {
		s.linkBySameTag(ctx, files, methodFQN, addResult)
	}

	// 3. dependency 策略：依赖递归
	if len(results) < 10 {
		s.linkByDependency(ctx, files, methodFQN, addResult)
	}

	// 按置信度降序排序
	sortByConfidenceDesc(results)

	// 过滤置信度
	filtered := make([]TestLink, 0, len(results))
	for _, r := range results {
		if r.Confidence >= minConfidence {
			filtered = append(filtered, r)
		}
	}

	return filtered, nil
}

// discoverTestClasses 发现所有测试类及其对应的源类映射。
//
// 命名约定：OrderServiceTest ↔ OrderService（去掉 Test 后缀/前缀）
func (s *Service) discoverTestClasses(ctx context.Context, files []*store.FileRecord) map[string]string {
	result := make(map[string]string) // classFQN -> testClassFQN

	for _, f := range files {
		classes, err := s.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			continue
		}
		for _, cls := range classes {
			testClass := s.matchTestClass(cls)
			if testClass != "" {
				result[testClass] = cls.FullName
			}
		}
	}

	return result
}

// matchTestClass 判断是否为测试类，并返回对应的源类全限定名。
//
// 规则：
//   - OrderServiceTest → OrderService（去掉 Test 后缀）
//   - TestOrderService → OrderService（去掉 Test 前缀）
func (s *Service) matchTestClass(cls store.ClassRecord) string {
	name := cls.Name

	// 后缀匹配：OrderServiceTest
	if strings.HasSuffix(name, "Test") && len(name) > 4 {
		sourceName := strings.TrimSuffix(name, "Test")
		return strings.Replace(cls.FullName, name, sourceName, 1)
	}

	// 前缀匹配：TestOrderService
	if strings.HasPrefix(name, "Test") && len(name) > 4 {
		sourceName := strings.TrimPrefix(name, "Test")
		return strings.Replace(cls.FullName, name, sourceName, 1)
	}

	return ""
}

// linkByNaming 通过命名约定建立方法级关联。
func (s *Service) linkByNaming(ctx context.Context, files []*store.FileRecord, classFQN, testClassFQN string, methodFQN string, add func(string, string, string, int)) {
	// 找到目标类中的方法
	var targetMethod store.MethodRecord
	found := false
	for _, f := range files {
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		for _, cls := range classes {
			if cls.FullName != classFQN {
				continue
			}
			methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
			for _, m := range methods {
				if m.FullName == methodFQN {
					targetMethod = m
					found = true
					break
				}
			}
			if found {
				break
			}
		}
		if found {
			break
		}
	}

	if !found {
		return
	}

	// 在测试类中查找同名方法（或带 Test 前缀/后缀的方法）
	methodName := targetMethod.Name
	for _, f := range files {
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		for _, cls := range classes {
			if cls.FullName != testClassFQN {
				continue
			}
			methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
			for _, m := range methods {
				if matchesTestMethod(methodName, m.Name) {
					add(m.FullName, methodName, "naming", 70)
				}
			}
		}
	}
}

// matchesTestMethod 判断测试方法名是否匹配目标方法名。
func matchesTestMethod(targetName, testName string) bool {
	// 精确匹配
	if testName == targetName {
		return true
	}
	// Test 前缀：TestGetOrder ↔ GetOrder
	if strings.HasPrefix(testName, "Test") && strings.TrimPrefix(testName, "Test") == targetName {
		return true
	}
	// test 前缀：testGetOrder ↔ GetOrder
	if strings.HasPrefix(testName, "test") && strings.TrimPrefix(testName, "test") == targetName {
		return true
	}
	// Should 前缀：ShouldReturnOrder ↔ ReturnOrder（不精确，检查包含）
	if strings.HasPrefix(testName, "Should") && strings.Contains(strings.ToLower(testName), strings.ToLower(targetName)) {
		return true
	}
	// 目标方法名包含在测试方法名中
	if strings.Contains(strings.ToLower(testName), strings.ToLower(targetName)) {
		return true
	}
	return false
}

// linkBySameTag 通过同 Tag 聚类建立关联。
func (s *Service) linkBySameTag(ctx context.Context, files []*store.FileRecord, methodFQN string, add func(string, string, string, int)) {
	// 获取目标方法的标签
	var targetClassID int64
	var targetMethodID int64

	for _, f := range files {
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		for _, cls := range classes {
			methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
			for _, m := range methods {
				if m.FullName == methodFQN {
					targetMethodID = m.ID
					targetClassID = cls.ID
					break
				}
			}
		}
	}

	if targetMethodID == 0 && targetClassID == 0 {
		return
	}

	// 获取目标方法所在类的标签
	targetTags, _ := s.store.GetTagsByClassID(ctx, targetClassID)
	if len(targetTags) == 0 {
		return
	}

	// 查找同标签的测试类
	for _, f := range files {
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		for _, cls := range classes {
			// 跳过自身
			if cls.ID == targetClassID {
				continue
			}
			// 只考虑测试类
			if !isTestClass(cls) {
				continue
			}

			clsTags, _ := s.store.GetTagsByClassID(ctx, cls.ID)
			if hasCommonTag(targetTags, clsTags) {
				methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
				for _, m := range methods {
					add(m.FullName, m.Name, "same_tag", 60)
				}
			}
		}
	}
}

// isTestClass 判断是否为测试类。
func isTestClass(cls store.ClassRecord) bool {
	name := cls.Name
	return strings.HasSuffix(name, "Test") || strings.HasPrefix(name, "Test") ||
		strings.HasSuffix(name, "Tests") || strings.HasSuffix(name, "Spec")
}

// hasCommonTag 检查两个标签列表是否有共同标签。
func hasCommonTag(a, b []string) bool {
	set := make(map[string]bool)
	for _, t := range a {
		set[t] = true
	}
	for _, t := range b {
		if set[t] {
			return true
		}
	}
	return false
}

// linkByDependency 通过依赖递归建立关联。
//
// 策略：目标方法所在类 → 同一目录下的测试类 + 引用该类的测试类。
func (s *Service) linkByDependency(ctx context.Context, files []*store.FileRecord, methodFQN string, add func(string, string, string, int)) {
	// 找到目标方法所在的文件
	var targetFile *store.FileRecord
	for _, f := range files {
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		for _, cls := range classes {
			methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
			for _, m := range methods {
				if m.FullName == methodFQN {
					targetFile = f
					break
				}
			}
			if targetFile != nil {
				break
			}
		}
		if targetFile != nil {
			break
		}
	}

	if targetFile == nil {
		return
	}

	// 构建文件名到类名的映射
	fileToClasses := make(map[int64][]store.ClassRecord)
	for _, f := range files {
		classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
		fileToClasses[f.ID] = classes
	}

	// 策略1: 同一目录下的测试文件
	targetDir := dirFromPath(targetFile.AbsolutePath)
	for _, f := range files {
		if f.ID == targetFile.ID {
			continue
		}
		if dirFromPath(f.AbsolutePath) != targetDir {
			continue
		}
		classes := fileToClasses[f.ID]
		for _, cls := range classes {
			if isTestClass(cls) {
				methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
				for _, m := range methods {
					add(m.FullName, m.Name, "dependency", 80)
				}
			}
		}
	}

	// 策略2: 引用目标文件的测试类
	for _, f := range files {
		if f.ID == targetFile.ID {
			continue
		}
		if len(f.Imports) == 0 {
			continue
		}

		referencesTarget := false
		targetPath := strings.ReplaceAll(targetFile.AbsolutePath, "\\", "/")
		targetParts := strings.Split(targetPath, "/")

		for _, imp := range f.Imports {
			imp = strings.TrimSpace(imp)
			if imp == "" {
				continue
			}
			// 检查 import 路径是否包含在目标文件路径中
			if strings.Contains(targetPath, imp) || strings.Contains(imp, targetPath) {
				referencesTarget = true
				break
			}
			// 检查 import 的最后一个路径段是否匹配目标目录名
			if len(targetParts) > 1 {
				dirName := targetParts[len(targetParts)-2]
				if strings.Contains(imp, dirName) {
					referencesTarget = true
					break
				}
			}
		}

		if referencesTarget {
			classes := fileToClasses[f.ID]
			for _, cls := range classes {
				if isTestClass(cls) {
					methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
					for _, m := range methods {
						add(m.FullName, m.Name, "dependency", 80)
					}
				}
			}
		}
	}
}

// extractClassFQN 从方法 FQN 中提取类 FQN。
//
// 例如："com.example.OrderService.getOrder" → "com.example.OrderService"
func extractClassFQN(methodFQN string) string {
	lastDot := strings.LastIndex(methodFQN, ".")
	if lastDot < 0 {
		return methodFQN
	}
	return methodFQN[:lastDot]
}

// dirFromPath 从文件路径中提取目录部分。
//
// 例如："/project/internal/payment/service.go" → "/project/internal/payment"
func dirFromPath(path string) string {
	path = strings.ReplaceAll(path, "\\", "/")
	lastSlash := strings.LastIndex(path, "/")
	if lastSlash < 0 {
		return ""
	}
	return path[:lastSlash]
}

// sortByConfidenceDesc 按置信度降序排序。
func sortByConfidenceDesc(links []TestLink) {
	for i := 0; i < len(links); i++ {
		maxIdx := i
		for j := i + 1; j < len(links); j++ {
			if links[j].Confidence > links[maxIdx].Confidence {
				maxIdx = j
			}
		}
		links[i], links[maxIdx] = links[maxIdx], links[i]
	}
}