package parser

import "fmt"

// Registry 是适配器注册中心，管理所有 ParserPlugin 的注册与选择。
type Registry struct {
	plugins     []ParserPlugin
	priorityMap map[string][]string // language -> ordered adapter names
}

// NewRegistry 创建空的适配器注册中心。
func NewRegistry() *Registry {
	return &Registry{
		priorityMap: make(map[string][]string),
	}
}

// Register 注册一个适配器到注册中心。
func (r *Registry) Register(p ParserPlugin) {
	r.plugins = append(r.plugins, p)
}

// SetPriority 为指定语言设置适配器优先级顺序。
// 如未设置，默认按注册顺序。
func (r *Registry) SetPriority(lang string, names []string) {
	r.priorityMap[lang] = names
}

// Select 按语言选择优先级最高的可用适配器。
//
// 优先级策略：SCIP > LSP > 竞品直读 > tree-sitter（默认），配置可覆盖。
// 如果未通过 SetPriority 设置优先级，则按注册顺序取第一个匹配的适配器。
func (r *Registry) Select(lang string) (ParserPlugin, error) {
	priorities, hasPriority := r.priorityMap[lang]
	if hasPriority {
		for _, name := range priorities {
			for _, p := range r.plugins {
				if p.Name() == name && p.Supports(lang) {
					return p, nil
				}
			}
		}
	}
	// 默认：按注册顺序取第一个匹配的
	for _, p := range r.plugins {
		if p.Supports(lang) {
			return p, nil
		}
	}
	return nil, fmt.Errorf("no adapter found for language: %s", lang)
}