// Package service 提供 CodeSchema 系统的业务逻辑层。
//
// 封装 Store 操作，为 HTTP API 和 MCP Server 提供统一查询接口。
// P0 阶段实现基础查询骨架，P1 阶段接入真实数据。
package service

import (
	"context"
	"fmt"
	"strings"
	"time"

	"codeschema/internal/search"
	"codeschema/internal/store"
)

// Service 是业务逻辑层，封装所有查询操作。
type Service struct {
	store        store.Store
	startTime    time.Time
	searcher     *search.Searcher
	indexBuilder *search.IndexBuilder // P8.3 自动索引构建
}

// NewService 创建 Service 实例。
func NewService(st store.Store) *Service {
	return &Service{
		store:     st,
		startTime: time.Now(),
	}
}

// WithSearcher 设置双路检索器，启用语义搜索能力。
//
// 当 searcher 为 nil 时，Search 方法回退到 P0 占位行为（返回空结果）。
func (s *Service) WithSearcher(searcher *search.Searcher) *Service {
	s.searcher = searcher
	return s
}

// WithIndexBuilder 设置自动索引构建器，在扫描后自动更新搜索索引。
func (s *Service) WithIndexBuilder(b *search.IndexBuilder) *Service {
	s.indexBuilder = b
	return s
}

// BuildIndex 从 Store 全量构建搜索索引。
//
// 返回构建统计信息，包括文档数、索引数、耗时等。
func (s *Service) BuildIndex(ctx context.Context) (*search.BuildResult, error) {
	if s.indexBuilder == nil {
		return &search.BuildResult{}, nil
	}
	return s.indexBuilder.BuildFromStore(ctx, s.store)
}

// HealthStatus 健康检查响应。
type HealthStatus struct {
	Status    string `json:"status"`
	Uptime    string `json:"uptime"`
	StoreOK   bool   `json:"store_ok"`
	StoreType string `json:"store_type"`
}

// Health 返回系统健康状态。
func (s *Service) Health(ctx context.Context) *HealthStatus {
	status := &HealthStatus{
		Uptime:    time.Since(s.startTime).Round(time.Second).String(),
		StoreType: "file",
	}
	if err := s.store.HealthCheck(ctx); err != nil {
		status.Status = "degraded"
		status.StoreOK = false
	} else {
		status.Status = "ok"
		status.StoreOK = true
	}
	return status
}

// StoreHealthCheck 执行存储层健康检查。
func (s *Service) StoreHealthCheck(ctx context.Context) error {
	return s.store.HealthCheck(ctx)
}

// SymbolContext 符号上下文响应。
type SymbolContext struct {
	Symbol       string   `json:"symbol"`
	Source       string   `json:"source"`
	FilePath     string   `json:"file_path"`
	StartLine    int      `json:"start_line"`
	EndLine      int      `json:"end_line"`
	Doc          string   `json:"doc,omitempty"`
	RelatedTests []string `json:"related_tests,omitempty"`
}

// GetContext 获取指定符号的上下文（P0 骨架，返回占位数据）。
func (s *Service) GetContext(ctx context.Context, symbol string, contextLines int) (*SymbolContext, error) {
	if symbol == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "symbol is required"}
	}
	if contextLines <= 0 {
		contextLines = 5
	}

	// P0: 返回占位数据，P1 接入真实存储查询
	return &SymbolContext{
		Symbol:   symbol,
		Source:   fmt.Sprintf("// context for %s (P0 placeholder)", symbol),
		FilePath: "unknown",
	}, nil
}

// ImpactNode 影响面分析节点。
type ImpactNode struct {
	Method string `json:"method"`
	Depth  int    `json:"depth"`
}

// ImpactResult 影响面分析响应。
type ImpactResult struct {
	Method  string       `json:"method"`
	Callers []ImpactNode `json:"callers"`
	Callees []ImpactNode `json:"callees"`
}

// GetImpact 获取指定方法的影响面（P0 骨架，返回占位数据）。
func (s *Service) GetImpact(ctx context.Context, method string, depth int) (*ImpactResult, error) {
	if method == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "method is required"}
	}
	if depth <= 0 {
		depth = 1
	}
	return &ImpactResult{
		Method:  method,
		Callers: []ImpactNode{},
		Callees: []ImpactNode{},
	}, nil
}

// TestResult 关联单测结果。
type TestResult struct {
	TestMethod string `json:"test_method"`
	Strategy   string `json:"strategy"`
	Confidence int    `json:"confidence"`
}

// GetTests 获取关联单测，使用多种策略（naming/same_tag/dependency）。
func (s *Service) GetTests(ctx context.Context, method string, minConfidence int) ([]TestResult, error) {
	if method == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "method is required"}
	}
	if minConfidence <= 0 {
		minConfidence = 60
	}

	links, err := s.FindTestLinks(ctx, method, minConfidence)
	if err != nil {
		return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("find test links: %v", err)}
	}

	results := make([]TestResult, 0, len(links))
	for _, link := range links {
		results = append(results, TestResult{
			TestMethod: link.TestMethod,
			Strategy:   link.Strategy,
			Confidence: link.Confidence,
		})
	}
	return results, nil
}

// SearchResult 搜索结果项。
type SearchResult struct {
	Symbol  string  `json:"symbol"`
	Kind    string  `json:"kind"`
	File    string  `json:"file"`
	Score   float64 `json:"score"`
	Snippet string  `json:"snippet,omitempty"`
}

// Search 搜索符号，支持双路检索（FTS 精确匹配 + 向量语义搜索）。
//
// mode 参数：
//   - "exact": 仅 FTS 精确搜索
//   - "semantic": 仅向量语义搜索
//   - "both"（默认）: FTS + 向量融合检索
//
// 当 searcher 未设置时，回退到 P0 占位行为（返回空结果）。
func (s *Service) Search(ctx context.Context, query string, mode string, limit int) ([]SearchResult, error) {
	if query == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "query is required"}
	}
	if limit <= 0 {
		limit = 20
	}

	// 如果有搜索器，使用双路检索
	if s.searcher != nil {
		searchMode := search.SearchModeBoth
		switch mode {
		case "exact":
			searchMode = search.SearchModeExact
		case "semantic":
			searchMode = search.SearchModeSemantic
		}

		results, err := s.searcher.Search(ctx, query, searchMode, limit)
		if err != nil {
			return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("search: %v", err)}
		}

		// 富化搜索结果：从 Store 查询 Kind 和 File 信息
		s.enrichResults(ctx, results)

		// 映射为 service.SearchResult
		svcResults := make([]SearchResult, 0, len(results))
		for _, r := range results {
			svcResults = append(svcResults, SearchResult{
				Symbol:  r.Symbol,
				Kind:    r.Kind,
				File:    r.File,
				Score:   r.Score,
				Snippet: r.Snippet,
			})
		}
		return svcResults, nil
	}

	// 回退到 P0 占位行为
	return []SearchResult{}, nil
}

// enrichResults 从 Store 查询搜索结果中每个符号的 Kind 和 File 信息。
//
// 符号 ID 格式：
//   - "file:/path/to/file.go" → 直接提取文件路径
//   - "class:123" → 从 Store 查询类记录
//   - "method:456" → 从 Store 查询方法记录
//
// 对于找不到的符号，留空（不阻断搜索流程）。
func (s *Service) enrichResults(ctx context.Context, results []search.SearchResult) {
	for i, r := range results {
		if r.Kind != "" && r.File != "" {
			continue // 已经填充过
		}
		kind, file := s.resolveSymbol(ctx, r.Symbol)
		if kind != "" {
			results[i].Kind = kind
		}
		if file != "" {
			results[i].File = file
		}
	}
}

// resolveSymbol 解析符号 ID 为 Kind 和 File 路径。
func (s *Service) resolveSymbol(ctx context.Context, symbol string) (kind, file string) {
	const (
		filePrefix   = "file:"
		classPrefix  = "class:"
		methodPrefix = "method:"
	)

	switch {
	case strings.HasPrefix(symbol, filePrefix):
		return "file", symbol[len(filePrefix):]

	case strings.HasPrefix(symbol, classPrefix):
		id := parseInt64(symbol[len(classPrefix):])
		if id <= 0 {
			return "", ""
		}
		// 遍历所有文件查找类
		files, err := s.store.GetAllFiles(ctx)
		if err != nil {
			return "", ""
		}
		for _, f := range files {
			classes, err := s.store.GetClassesByFileID(ctx, f.ID)
			if err != nil {
				continue
			}
			for _, c := range classes {
				if c.ID == id {
					kind := "class"
					if c.Type != "" {
						kind = strings.ToLower(c.Type)
					}
					return kind, f.AbsolutePath
				}
			}
		}

	case strings.HasPrefix(symbol, methodPrefix):
		id := parseInt64(symbol[len(methodPrefix):])
		if id <= 0 {
			return "", ""
		}
		// 遍历所有文件查找方法
		files, err := s.store.GetAllFiles(ctx)
		if err != nil {
			return "", ""
		}
		for _, f := range files {
			classes, err := s.store.GetClassesByFileID(ctx, f.ID)
			if err != nil {
				continue
			}
			for _, c := range classes {
				methods, err := s.store.GetMethodsByClassID(ctx, c.ID)
				if err != nil {
					continue
				}
				for _, m := range methods {
					if m.ID == id {
						return "method", f.AbsolutePath
					}
				}
			}
		}
	}

	return "", ""
}

// parseInt64 简单解析 int64，解析失败返回 0。
func parseInt64(s string) int64 {
	if len(s) == 0 {
		return 0
	}
	var n int64
	for _, r := range s {
		if r < '0' || r > '9' {
			return 0
		}
		n = n*10 + int64(r-'0')
	}
	return n
}

// FindDependencies 查找依赖关系（P0 骨架）。
func (s *Service) FindDependencies(ctx context.Context, symbol string) ([]string, error) {
	if symbol == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "symbol is required"}
	}
	return []string{}, nil
}

// GetCallGraph 获取调用图（P0 骨架）。
func (s *Service) GetCallGraph(ctx context.Context, symbol string, depth int) (map[string]any, error) {
	if symbol == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "symbol is required"}
	}
	if depth <= 0 {
		depth = 1
	}
	return map[string]any{
		"symbol": symbol,
		"depth":  depth,
		"nodes":  []string{},
		"edges":  []string{},
	}, nil
}

// TagResult 标签查询结果。
type TagResult struct {
	Symbol     string            `json:"symbol"`
	Kind       string            `json:"kind"` // "class" or "method"
	Tags       []string          `json:"tags"`
	Categories map[string]string `json:"categories,omitempty"` // tag -> category
}

// GetTags 获取指定符号的标签列表。
//
// symbol 格式：类的 FullName 或方法的 FullName。
// 先尝试按类名查询，再按方法名查询。
func (s *Service) GetTags(ctx context.Context, symbol string) (*TagResult, error) {
	if symbol == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "symbol is required"}
	}

	// 先尝试按类名查询（通过所有文件）
	files, err := s.store.GetAllFiles(ctx)
	if err != nil {
		return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("get all files: %v", err)}
	}

	for _, f := range files {
		classes, err := s.store.GetClassesByFileID(ctx, f.ID)
		if err != nil {
			continue
		}
		for _, cls := range classes {
			if cls.FullName == symbol {
				tags, err := s.store.GetTagsByClassID(ctx, cls.ID)
				if err != nil {
					return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("get tags: %v", err)}
				}
				cats, _ := s.store.GetAllTagsWithCategories(ctx)
				result := &TagResult{
					Symbol: symbol,
					Kind:   "class",
					Tags:   tags,
				}
				if len(cats) > 0 {
					filtered := make(map[string]string)
					for _, t := range tags {
						if c, ok := cats[t]; ok {
							filtered[t] = c
						}
					}
					result.Categories = filtered
				}
				return result, nil
			}

			// 在方法中查询
			methods, err := s.store.GetMethodsByClassID(ctx, cls.ID)
			if err != nil {
				continue
			}
			for _, m := range methods {
				if m.FullName == symbol {
					tags, err := s.store.GetTagsByMethodID(ctx, m.ID)
					if err != nil {
						return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("get tags: %v", err)}
					}
					cats, _ := s.store.GetAllTagsWithCategories(ctx)
					result := &TagResult{
						Symbol: symbol,
						Kind:   "method",
						Tags:   tags,
					}
					if len(cats) > 0 {
						filtered := make(map[string]string)
						for _, t := range tags {
							if c, ok := cats[t]; ok {
								filtered[t] = c
							}
						}
						result.Categories = filtered
					}
					return result, nil
				}
			}
		}
	}

	return nil, &ServiceError{Code: "ERR_SYMBOL_NOT_FOUND", Message: fmt.Sprintf("symbol not found: %s", symbol)}
}

// TagSearchResult 标签搜索结果。
type TagSearchResult struct {
	Tag       string   `json:"tag"`
	ClassIDs  []int64  `json:"class_ids,omitempty"`
	MethodIDs []int64  `json:"method_ids,omitempty"`
	Classes   []string `json:"classes,omitempty"`   // 类名列表
	Methods   []string `json:"methods,omitempty"`   // 方法名列表
}

// SearchByTag 按标签搜索类和方法的 ID 和名称。
func (s *Service) SearchByTag(ctx context.Context, tag string) (*TagSearchResult, error) {
	if tag == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "tag is required"}
	}

	classIDs, methodIDs, err := s.store.SearchByTag(ctx, tag)
	if err != nil {
		return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("search by tag: %v", err)}
	}

	result := &TagSearchResult{
		Tag:       tag,
		ClassIDs:  classIDs,
		MethodIDs: methodIDs,
	}

	// 解析类名
	files, _ := s.store.GetAllFiles(ctx)
	for _, cid := range classIDs {
		for _, f := range files {
			classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
			for _, cls := range classes {
				if cls.ID == cid {
					result.Classes = append(result.Classes, cls.FullName)
				}
			}
		}
	}

	// 解析方法名
	for _, mid := range methodIDs {
		for _, f := range files {
			classes, _ := s.store.GetClassesByFileID(ctx, f.ID)
			for _, cls := range classes {
				methods, _ := s.store.GetMethodsByClassID(ctx, cls.ID)
				for _, m := range methods {
					if m.ID == mid {
						result.Methods = append(result.Methods, m.FullName)
					}
				}
			}
		}
	}

	return result, nil
}

// AllTagsResult 所有标签及其分类。
type AllTagsResult struct {
	Tags       map[string]string `json:"tags"`        // tag -> category
	Categories map[string]int    `json:"categories"`  // category -> count
}

// GetAllTags 返回所有已知标签及其分类统计。
func (s *Service) GetAllTags(ctx context.Context) (*AllTagsResult, error) {
	tags, err := s.store.GetAllTagsWithCategories(ctx)
	if err != nil {
		return nil, &ServiceError{Code: "ERR_INTERNAL", Message: fmt.Sprintf("get all tags: %v", err)}
	}

	cats := make(map[string]int)
	for _, cat := range tags {
		cats[cat]++
	}

	return &AllTagsResult{
		Tags:       tags,
		Categories: cats,
	}, nil
}

// SearchConfig 搜索配置项（P0 骨架）。
func (s *Service) SearchConfig(ctx context.Context, pattern string) ([]string, error) {
	if pattern == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "pattern is required"}
	}
	return []string{}, nil
}

// GetAffected 获取受影响内容（P0 骨架）。
func (s *Service) GetAffected(ctx context.Context, symbol string, recursive bool) ([]string, error) {
	if symbol == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "symbol is required"}
	}
	return []string{}, nil
}

// ServiceError 业务错误，包含错误码和消息。
type ServiceError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func (e *ServiceError) Error() string {
	return fmt.Sprintf("[%s] %s", e.Code, e.Message)
}

// HTTPStatus 返回对应的 HTTP 状态码。
func (e *ServiceError) HTTPStatus() int {
	switch e.Code {
	case "ERR_SYMBOL_NOT_FOUND":
		return 404
	case "ERR_INVALID_PARAMETER":
		return 400
	case "ERR_RATE_LIMITED":
		return 429
	case "ERR_UNAUTHORIZED":
		return 401
	default:
		return 500
	}
}