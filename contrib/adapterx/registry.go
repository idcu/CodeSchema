package adapterx

import "fmt"

// Registry 是按名称注册/枚举解析适配器的注册中心。
//
// 消费方（如编排层）按配置的适配器名解析实例；发布方把适配器实现注册进来。
// 对外发布时 Registry 与 ParserPlugin 契约一同提供，保证第三方实现可被
// 任何遵循本契约的消费方加载。
type Registry struct {
	plugins map[string]ParserPlugin
	order   []string
}

// NewRegistry 创建空注册中心。
func NewRegistry() *Registry {
	return &Registry{plugins: map[string]ParserPlugin{}}
}

// Register 按适配器名注册实现；同名重复注册返回错误（防静默覆盖）。
func (r *Registry) Register(p ParserPlugin) error {
	if p == nil {
		return fmt.Errorf("adapterx: register nil plugin")
	}
	name := p.Name()
	if name == "" {
		return fmt.Errorf("adapterx: plugin name is empty")
	}
	if _, ok := r.plugins[name]; ok {
		return fmt.Errorf("adapterx: plugin %q already registered", name)
	}
	r.plugins[name] = p
	r.order = append(r.order, name)
	return nil
}

// Get 按名解析适配器；未注册返回 nil。
func (r *Registry) Get(name string) ParserPlugin {
	return r.plugins[name]
}

// Names 返回注册顺序的适配器名列表。
func (r *Registry) Names() []string {
	out := make([]string, 0, len(r.order))
	out = append(out, r.order...)
	return out
}
