package config

// Preset 能力层预设（建议 3：借鉴 dsh「一切皆插件 + preset 配置」）。
//
// 用单个 YAML 字段组合整组能力配置，无需逐项手写，让用户"不改源码、只改 YAML
// 组合能力"。预设仅在未显式配置的字段上兜底，显式配置优先，避免覆盖用户意图。
//
// 可选值：
//   - ""（默认）：不调整，完全使用显式配置（向后兼容）；
//   - "minimal"：最小资源档——仅 FTS 检索，关语义/向量/ONNX，关 AI 增强，
//     适合低内存/快速索引场景；
//   - "semantic"：语义档——开启 FTS + 语义检索（向量/ONNX），保持 AI 增强；
//   - "multitenant"：多租户档——保持全能力，监听默认开启，租户在 tenants 声明。
type Preset string

const (
	// PresetDefault 默认（空）：不调整能力组合。
	PresetDefault Preset = ""
	// PresetMinimal 最小资源档。
	PresetMinimal Preset = "minimal"
	// PresetSemantic 语义检索档。
	PresetSemantic Preset = "semantic"
	// PresetMultitenant 多租户档。
	PresetMultitenant Preset = "multitenant"
)

// ValidPreset 判断 preset 取值是否合法。
func ValidPreset(p string) bool {
	switch Preset(p) {
	case PresetDefault, PresetMinimal, PresetSemantic, PresetMultitenant:
		return true
	}
	return false
}

// ApplyPreset 应用能力层预设，就地修改 cfg。
//
// 幂等：可安全重复调用。预设是能力组合的权威来源：配置了 preset 后，
// 其管理的字段以 preset 为准（覆盖默认值与其他同层字段），保证"只改 preset
// 即达预期组合"；未管理的字段保持显式配置。
func ApplyPreset(cfg *Config) {
	if cfg == nil {
		return
	}
	switch cfg.Preset {
	case PresetMinimal:
		cfg.Storage.Search.Semantic = false // 只留 FTS 精确检索
		cfg.Storage.Search.FTS = true
		cfg.Storage.Vector.Driver = "" // 关向量后端（不加载 ONNX/本地嵌入器）
		cfg.AI.BudgetPerScan = 0       // 关 AI 增强（tagger/enhancer 预算归零）
		cfg.AI.BudgetPerQuery = 0
	case PresetSemantic:
		cfg.Storage.Search.FTS = true
		cfg.Storage.Search.Semantic = true // 语义检索 + 向量/ONNX
	case PresetMultitenant:
		cfg.Storage.Search.FTS = true
		cfg.Storage.Search.Semantic = true // 保持全能力
		cfg.Watcher.Enabled = true         // 多租户增量监听默认开启
	}
}
