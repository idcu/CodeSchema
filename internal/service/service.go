// Package service 提供 CodeSchema 系统的业务逻辑层。
//
// 封装 Store 操作，为 HTTP API 和 MCP Server 提供统一查询接口。
// P0 阶段实现基础查询骨架，P1 阶段接入真实数据。
package service

import (
	"context"
	"fmt"
	"time"

	"codeschema/internal/store"
)

// Service 是业务逻辑层，封装所有查询操作。
type Service struct {
	store  store.Store
	startTime time.Time
}

// NewService 创建 Service 实例。
func NewService(st store.Store) *Service {
	return &Service{
		store:     st,
		startTime: time.Now(),
	}
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

// GetTests 获取关联单测（P0 骨架，返回占位数据）。
func (s *Service) GetTests(ctx context.Context, method string, minConfidence int) ([]TestResult, error) {
	if method == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "method is required"}
	}
	if minConfidence <= 0 {
		minConfidence = 60
	}
	return []TestResult{}, nil
}

// SearchResult 搜索结果项。
type SearchResult struct {
	Symbol  string `json:"symbol"`
	Kind    string `json:"kind"`
	File    string `json:"file"`
	Score   float64 `json:"score"`
	Snippet string `json:"snippet,omitempty"`
}

// Search 搜索符号（P0 骨架，返回占位数据）。
func (s *Service) Search(ctx context.Context, query string, mode string, limit int) ([]SearchResult, error) {
	if query == "" {
		return nil, &ServiceError{Code: "ERR_INVALID_PARAMETER", Message: "query is required"}
	}
	if limit <= 0 {
		limit = 20
	}
	return []SearchResult{}, nil
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