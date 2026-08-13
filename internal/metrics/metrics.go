// Package metrics 提供基础监控指标收集功能。
//
// 纯 Go 实现，无外部依赖。输出 Prometheus 文本格式，可通过 /metrics 端点暴露。
// 支持 Counter 和 Gauge 两种指标类型，线程安全。
//
// 使用方式：
//
//	 metrics.RegisterCounter("http_requests_total", "Total HTTP requests", "method", "path")
//	 metrics.IncCounter("http_requests_total", "GET", "/api/health")
//	 metrics.RegisterGauge("index_total_files", "Total indexed files")
//	 metrics.SetGauge("index_total_files", 42)
//	 fmt.Println(metrics.Render())  // Prometheus 文本格式
package metrics

import (
	"fmt"
	"sort"
	"strings"
	"sync"
)

// MetricType 指标类型。
type MetricType string

const (
	TypeCounter MetricType = "counter"
	TypeGauge   MetricType = "gauge"
)

// MetricDef 指标定义。
type MetricDef struct {
	Name        string
	Type        MetricType
	Help        string
	LabelNames  []string
}

// metricValue 指标值，含标签。
type metricValue struct {
	labels map[string]string
	value  float64
}

// registry 全局指标注册表。
type registry struct {
	mu      sync.RWMutex
	defs    map[string]*MetricDef
	values  map[string][]*metricValue // name -> values
}

var globalReg = &registry{
	defs:   make(map[string]*MetricDef),
	values: make(map[string][]*metricValue),
}

// RegisterCounter 注册一个 Counter 指标。
// labelNames 为可选的标签名列表。
func RegisterCounter(name, help string, labelNames ...string) {
	globalReg.mu.Lock()
	defer globalReg.mu.Unlock()
	globalReg.defs[name] = &MetricDef{
		Name:       name,
		Type:       TypeCounter,
		Help:       help,
		LabelNames: labelNames,
	}
}

// RegisterGauge 注册一个 Gauge 指标。
// labelNames 为可选的标签名列表。
func RegisterGauge(name, help string, labelNames ...string) {
	globalReg.mu.Lock()
	defer globalReg.mu.Unlock()
	globalReg.defs[name] = &MetricDef{
		Name:       name,
		Type:       TypeGauge,
		Help:       help,
		LabelNames: labelNames,
	}
}

// IncCounter 递增 Counter 指标的值。
// labelValues 为标签值，顺序必须与 RegisterCounter 的 labelNames 一致。
func IncCounter(name string, labelValues ...string) {
	AddCounter(name, 1, labelValues...)
}

// AddCounter 为 Counter 指标增加指定值。
func AddCounter(name string, delta float64, labelValues ...string) {
	globalReg.mu.Lock()
	defer globalReg.mu.Unlock()

	def, ok := globalReg.defs[name]
	if !ok {
		return
	}
	if def.Type != TypeCounter {
		return
	}

	labels := buildLabels(def.LabelNames, labelValues)
	globalReg.addValue(name, labels, delta)
}

// SetGauge 设置 Gauge 指标的值。
func SetGauge(name string, value float64, labelValues ...string) {
	globalReg.mu.Lock()
	defer globalReg.mu.Unlock()

	def, ok := globalReg.defs[name]
	if !ok {
		return
	}
	if def.Type != TypeGauge {
		return
	}

	labels := buildLabels(def.LabelNames, labelValues)
	// Gauge 直接覆盖
	globalReg.removeValues(name, labels)
	globalReg.values[name] = append(globalReg.values[name], &metricValue{
		labels: labels,
		value:  value,
	})
}

// IncGauge 递增 Gauge 指标的值。
func IncGauge(name string, labelValues ...string) {
	globalReg.mu.Lock()
	defer globalReg.mu.Unlock()

	def, ok := globalReg.defs[name]
	if !ok {
		return
	}
	if def.Type != TypeGauge {
		return
	}

	labels := buildLabels(def.LabelNames, labelValues)
	globalReg.addValue(name, labels, 1)
}

// DecGauge 递减 Gauge 指标的值。
func DecGauge(name string, labelValues ...string) {
	globalReg.mu.Lock()
	defer globalReg.mu.Unlock()

	def, ok := globalReg.defs[name]
	if !ok {
		return
	}
	if def.Type != TypeGauge {
		return
	}

	labels := buildLabels(def.LabelNames, labelValues)
	globalReg.addValue(name, labels, -1)
}

// Render 返回所有指标的 Prometheus 文本格式。
func Render() string {
	globalReg.mu.RLock()
	defer globalReg.mu.RUnlock()

	// 按名称排序，确保输出稳定
	names := make([]string, 0, len(globalReg.defs))
	for name := range globalReg.defs {
		names = append(names, name)
	}
	sort.Strings(names)

	var b strings.Builder
	for _, name := range names {
		def := globalReg.defs[name]
		values := globalReg.values[name]

		// HELP 和 TYPE
		fmt.Fprintf(&b, "# HELP %s %s\n", name, def.Help)
		fmt.Fprintf(&b, "# TYPE %s %s\n", name, def.Type)

		if len(values) == 0 {
			// 未设置值的指标输出 0
			if len(def.LabelNames) == 0 {
				fmt.Fprintf(&b, "%s 0\n", name)
			}
			continue
		}

		for _, v := range values {
			if len(def.LabelNames) > 0 {
				// 有标签
				labelPairs := make([]string, 0, len(def.LabelNames))
				for _, labelName := range def.LabelNames {
					labelPairs = append(labelPairs, fmt.Sprintf("%s=%q", labelName, v.labels[labelName]))
				}
				fmt.Fprintf(&b, "%s{%s} %v\n", name, strings.Join(labelPairs, ","), formatFloat(v.value))
			} else {
				fmt.Fprintf(&b, "%s %v\n", name, formatFloat(v.value))
			}
		}
	}

	return b.String()
}

// Snapshot 返回当前所有指标的快照（用于测试）。
type Snapshot struct {
	Counters map[string]float64
	Gauges   map[string]float64
}

// Collect 返回当前指标快照（仅无标签指标）。
func Collect() *Snapshot {
	globalReg.mu.RLock()
	defer globalReg.mu.RUnlock()

	s := &Snapshot{
		Counters: make(map[string]float64),
		Gauges:   make(map[string]float64),
	}

	for name, def := range globalReg.defs {
		if len(def.LabelNames) > 0 {
			continue
		}
		values := globalReg.values[name]
		var total float64
		for _, v := range values {
			total += v.value
		}
		switch def.Type {
		case TypeCounter:
			s.Counters[name] = total
		case TypeGauge:
			s.Gauges[name] = total
		}
	}

	return s
}

// Reset 清空所有指标（用于测试）。
func Reset() {
	globalReg.mu.Lock()
	defer globalReg.mu.Unlock()
	globalReg.defs = make(map[string]*MetricDef)
	globalReg.values = make(map[string][]*metricValue)
}

// buildLabels 构建标签 map。
func buildLabels(labelNames, labelValues []string) map[string]string {
	if len(labelNames) == 0 {
		return nil
	}
	labels := make(map[string]string, len(labelNames))
	for i, name := range labelNames {
		if i < len(labelValues) {
			labels[name] = labelValues[i]
		} else {
			labels[name] = ""
		}
	}
	return labels
}

// addValue 添加或更新指标值。
func (r *registry) addValue(name string, labels map[string]string, delta float64) {
	for _, v := range r.values[name] {
		if labelsEqual(v.labels, labels) {
			v.value += delta
			return
		}
	}
	r.values[name] = append(r.values[name], &metricValue{
		labels: labels,
		value:  delta,
	})
}

// removeValues 删除匹配标签的指标值。
func (r *registry) removeValues(name string, labels map[string]string) {
	values := r.values[name]
	filtered := make([]*metricValue, 0, len(values))
	for _, v := range values {
		if !labelsEqual(v.labels, labels) {
			filtered = append(filtered, v)
		}
	}
	r.values[name] = filtered
}

// labelsEqual 比较两个标签 map 是否相等。
func labelsEqual(a, b map[string]string) bool {
	if len(a) != len(b) {
		return false
	}
	for k, v := range a {
		if b[k] != v {
			return false
		}
	}
	return true
}

// formatFloat 格式化浮点数输出。
func formatFloat(v float64) string {
	if v == float64(int64(v)) {
		return fmt.Sprintf("%d", int64(v))
	}
	return fmt.Sprintf("%g", v)
}