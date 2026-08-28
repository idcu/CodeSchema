// Package config 提供 YAML/JSON 配置文件的加载与管理。
//
// 使用 gopkg.in/yaml.v3 解析 YAML 配置文件，支持全部 YAML 语法。
// JSON 配置文件通过 encoding/json 解析。
//
// P9 新增特性：
//   - LoadFromEnv: 从环境变量加载配置覆盖（CODESCHEMA_<SECTION>_<KEY> 格式）
//   - Merge: 合并多个配置源（默认值 < 配置文件 < 环境变量 < CLI 参数）
//   - ConfigWatcher: 配置文件变更自动重载，无需重启进程
package config

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"gopkg.in/yaml.v3"
)

// 解析适配器默认路径常量（DefaultConfig 与 runtime 判断「是否显式配置」共用，
// 避免魔法串漂移）。
const (
	// DefaultSCIPIndexDir SCIP 适配器默认 index 目录。
	DefaultSCIPIndexDir = "./scipout"
	// DefaultCodeGraphDB CodeGraph 适配器默认 db 路径。
	DefaultCodeGraphDB = "./codegraph.db"
)

// Config 顶层配置结构。
type Config struct {
	// Preset 能力层预设（建议 3）：minimal / semantic / multitenant / ""（默认）。
	// 用单个字段组合整组能力配置，见 ApplyPreset。
	Preset  Preset        `yaml:"preset" json:"preset"`
	Project ProjectConfig `yaml:"project" json:"project"`
	Storage StorageConfig `yaml:"storage" json:"storage"`
	Parser  ParserConfig  `yaml:"parser" json:"parser"`
	AI      AIConfig      `yaml:"ai" json:"ai"`
	Server  ServerConfig  `yaml:"server" json:"server"`
	Watcher WatcherConfig `yaml:"watcher" json:"watcher"`
	Scanner ScannerConfig `yaml:"scanner" json:"scanner"`
	// Context 上下文供给（裁剪/预算/路径/查询缓存）配置。
	Context ContextConfig `yaml:"context" json:"context"`
	// Tenants 多租户注册表。非空时 serve/mcp 以单实例服务多个隔离仓库；
	// 为空则沿用顶层 project/storage 等配置，保持单项目模式（向后兼容）。
	Tenants []TenantConfig `yaml:"tenants" json:"tenants"`
}

// TenantConfig 多租户注册表中的单个项目（仓库）配置。
//
// 仅填写与全局默认不同的字段即可；未填写的字段由 TenantConfig.ToConfig 用全局
// Config 兜底。因此一个租户最小只需 id + root + storage.dsn。
type TenantConfig struct {
	ID        string        `yaml:"id" json:"id"`
	Name      string        `yaml:"name" json:"name"`
	Root      string        `yaml:"root" json:"root"`
	Languages []string      `yaml:"languages" json:"languages"`
	Storage   StorageConfig `yaml:"storage" json:"storage"`
	Parser    ParserConfig  `yaml:"parser" json:"parser"`
	AI        AIConfig      `yaml:"ai" json:"ai"`
	Watcher   WatcherConfig `yaml:"watcher" json:"watcher"`
	Scanner   ScannerConfig `yaml:"scanner" json:"scanner"`
	// AutoScan 启动时为该租户仓库执行一次全量扫描并入库（无需预先 scan）。
	AutoScan bool `yaml:"auto_scan" json:"auto_scan"`
	// Watch 启动后对该租户仓库后台增量监听（需 root 已配置）。
	Watch bool `yaml:"watch" json:"watch"`
}

// ToConfig 将租户配置叠加到全局 base 之上，生成该租户独立的完整 Config。
// 仅覆盖租户显式设置的字段，其余沿用 base 默认值。
func (t TenantConfig) ToConfig(base *Config) *Config {
	c := cloneConfig(base)
	if t.Name != "" {
		c.Project.Name = t.Name
	}
	if t.Root != "" {
		c.Project.Root = t.Root
	}
	if len(t.Languages) > 0 {
		c.Project.Languages = cloneStringSlice(t.Languages)
	}
	if t.Storage.Driver != "" {
		c.Storage.Driver = t.Storage.Driver
	}
	if t.Storage.DSN != "" {
		c.Storage.DSN = t.Storage.DSN
	}
	if t.Storage.KV != "" {
		c.Storage.KV = t.Storage.KV
	}
	if t.Storage.Vector.Driver != "" {
		c.Storage.Vector.Driver = t.Storage.Vector.Driver
	}
	if t.Storage.Vector.DSN != "" {
		c.Storage.Vector.DSN = t.Storage.Vector.DSN
	}
	if t.Storage.Vector.EmbeddingModel != "" {
		c.Storage.Vector.EmbeddingModel = t.Storage.Vector.EmbeddingModel
	}
	if t.Storage.Vector.ModelDir != "" {
		c.Storage.Vector.ModelDir = t.Storage.Vector.ModelDir
	}
	if t.Storage.Vector.ModelDownloadURL != "" {
		c.Storage.Vector.ModelDownloadURL = t.Storage.Vector.ModelDownloadURL
	}
	if t.Storage.Vector.ModelSHA256 != "" {
		c.Storage.Vector.ModelSHA256 = t.Storage.Vector.ModelSHA256
	}
	mergeSearch(&c.Storage.Search, &t.Storage.Search)
	if len(t.Parser.Adapters) > 0 {
		c.Parser.Adapters = cloneStringSlice(t.Parser.Adapters)
	}
	if t.Parser.SCIP.IndexDir != "" {
		c.Parser.SCIP.IndexDir = t.Parser.SCIP.IndexDir
	}
	if t.Parser.CodeGraph.DB != "" {
		c.Parser.CodeGraph.DB = t.Parser.CodeGraph.DB
	}
	if t.Parser.JCodeIndexer.DB != "" {
		c.Parser.JCodeIndexer.DB = t.Parser.JCodeIndexer.DB
	}
	if t.Parser.JCodeIndexer.ConfigFile != "" {
		c.Parser.JCodeIndexer.ConfigFile = t.Parser.JCodeIndexer.ConfigFile
	}
	if len(t.Parser.JCodeIndexer.Env) > 0 {
		c.Parser.JCodeIndexer.Env = cloneStringMap(t.Parser.JCodeIndexer.Env)
	}
	if t.Parser.LSP.Enabled {
		c.Parser.LSP.Enabled = true
	}
	if t.AI.Provider != "" {
		c.AI.Provider = t.AI.Provider
	}
	if t.AI.Model != "" {
		c.AI.Model = t.AI.Model
	}
	if t.AI.BaseURL != "" {
		c.AI.BaseURL = t.AI.BaseURL
	}
	if t.AI.APIKey != "" {
		c.AI.APIKey = t.AI.APIKey
	}
	if t.AI.BudgetPerScan > 0 {
		c.AI.BudgetPerScan = t.AI.BudgetPerScan
	}
	if t.AI.BudgetPerQuery > 0 {
		c.AI.BudgetPerQuery = t.AI.BudgetPerQuery
	}
	if t.Watcher.DebounceMs > 0 {
		c.Watcher.DebounceMs = t.Watcher.DebounceMs
	}
	if len(t.Watcher.IgnoreDirs) > 0 {
		c.Watcher.IgnoreDirs = cloneStringSlice(t.Watcher.IgnoreDirs)
	}
	if t.Watcher.BatchSize > 0 {
		c.Watcher.BatchSize = t.Watcher.BatchSize
	}
	if t.Watcher.UseFsnotify {
		c.Watcher.UseFsnotify = true
	}
	if t.Watcher.Enabled {
		c.Watcher.Enabled = true
	}
	if t.Scanner.Workers > 0 {
		c.Scanner.Workers = t.Scanner.Workers
	}
	if t.Scanner.FileSizeLimitMB > 0 {
		c.Scanner.FileSizeLimitMB = t.Scanner.FileSizeLimitMB
	}
	if t.Scanner.LineCountLimit > 0 {
		c.Scanner.LineCountLimit = t.Scanner.LineCountLimit
	}
	return c
}

// ProjectConfig 项目配置。
type ProjectConfig struct {
	Name      string   `yaml:"name" json:"name"`
	Root      string   `yaml:"root" json:"root"`
	Languages []string `yaml:"languages" json:"languages"`
}

// StorageConfig 存储配置。
type StorageConfig struct {
	Driver string       `yaml:"driver" json:"driver"`
	DSN    string       `yaml:"dsn" json:"dsn"`
	KV     string       `yaml:"kv" json:"kv"`
	Vector VectorConfig `yaml:"vector" json:"vector"`
	Search SearchConfig `yaml:"search" json:"search"`
}

// VectorConfig 向量库配置。
type VectorConfig struct {
	Driver         string `yaml:"driver" json:"driver"`
	DSN            string `yaml:"dsn" json:"dsn"`
	EmbeddingModel string `yaml:"embedding_model" json:"embedding_model"`

	// ModelDir 语义模型目录（含 onnx/ 子目录与 tokenizer.json）。
	// 默认 down/models/<embedding_model>；模型缺失时若配置了远程源则自动下载。
	ModelDir string `yaml:"model_dir" json:"model_dir"`

	// ModelDownloadURL ONNX 模型远程分发地址（可选）。
	// 模型缺失且配置了该地址时，启动时自动下载到 ModelDir（幂等：已存在跳过）。
	// 支持模板占位 {model}：如 https://example.com/models/{model}.tar.gz。
	ModelDownloadURL string `yaml:"model_download_url" json:"model_download_url"`
	// ModelSHA256 模型压缩包 SHA-256 校验和（可选，配置后下载完成即校验，不匹配报错）。
	ModelSHA256 string `yaml:"model_sha256" json:"model_sha256"`
}

// SearchConfig 搜索配置。
type SearchConfig struct {
	FTS       bool   `yaml:"fts" json:"fts"`
	Semantic  bool   `yaml:"semantic" json:"semantic"`
	FTSDir    string `yaml:"fts_dir" json:"fts_dir"`       // 全文搜索索引持久化目录
	VectorDir string `yaml:"vector_dir" json:"vector_dir"` // 向量索引持久化目录
	VectorDim int    `yaml:"vector_dim" json:"vector_dim"` // 向量维度（LocalEmbedder 用）
	IDFDir    string `yaml:"idf_dir" json:"idf_dir"`       // IDF 词典持久化目录
}

// ParserConfig 解析器配置。
type ParserConfig struct {
	Adapters     []string           `yaml:"adapters" json:"adapters"`
	SCIP         SCIPConfig         `yaml:"scip" json:"scip"`
	CodeGraph    CodeGraphConfig    `yaml:"codegraph" json:"codegraph"`
	JCodeIndexer JCodeIndexerConfig `yaml:"jcodeindexer" json:"jcodeindexer"`
	LSP          LSPConfig          `yaml:"lsp" json:"lsp"`
}

// LSPConfig LSP 适配器配置（接入 Registry 编排主路的开关）。
type LSPConfig struct {
	// Enabled 是否启用 LSP 适配器（按语言分发 gopls/jdtls/clangd）。
	// 默认 false：工具缺失时优雅跳过，失败自动回退 tree-sitter，不影响主路。
	Enabled bool `yaml:"enabled" json:"enabled"`
}

// SCIPConfig SCIP 适配器配置。
type SCIPConfig struct {
	IndexDir string `yaml:"index_dir" json:"index_dir"`
}

// CodeGraphConfig CodeGraph 适配器配置。
type CodeGraphConfig struct {
	DB string `yaml:"db" json:"db"`
}

// JCodeIndexerConfig JCodeIndexer 适配器配置。
type JCodeIndexerConfig struct {
	DB         string            `yaml:"db" json:"db"`
	ConfigFile string            `yaml:"config_file" json:"config_file"`
	Env        map[string]string `yaml:"env" json:"env"`
}

// AIConfig AI 增强配置。
type AIConfig struct {
	Provider string `yaml:"provider" json:"provider"`
	Model    string `yaml:"model" json:"model"`
	// BaseURL OpenAI 兼容 Chat Completions API 根地址。
	// 为空则使用官方默认（https://api.openai.com/v1）。
	BaseURL string `yaml:"base_url" json:"base_url"`
	// APIKey 鉴权密钥（也可通过环境变量 CODESCHEMA_AI_API_KEY 注入，
	// 优先读取环境变量；两者都为空时 AI 增强被禁用，主流程零影响）。
	APIKey         string `yaml:"api_key" json:"api_key"`
	BudgetPerScan  int    `yaml:"budget_per_scan" json:"budget_per_scan"`
	BudgetPerQuery int    `yaml:"budget_per_query" json:"budget_per_query"`
}

// ServerConfig 服务器配置。
// ContextConfig 上下文供给配置（裁剪/预算/路径/查询缓存）。
//
// 这些是「服务端默认值」：请求参数（context_lines / max_bytes / max_tokens /
// max_line_chars / path_style）显式传入时以请求为准，未传时取这里的默认值。
type ContextConfig struct {
	// ContextLines 默认上下文行数（0 = 仅符号体，不额外外扩）。
	ContextLines int `yaml:"context_lines" json:"context_lines"`
	// MaxBytes 默认输出字节预算（<=0 不限）。
	MaxBytes int `yaml:"max_bytes" json:"max_bytes"`
	// MaxTokens 默认输出 token 预算（<=0 不限），与 MaxBytes 取更严者。
	MaxTokens int `yaml:"max_tokens" json:"max_tokens"`
	// MaxLineChars 默认单行字符上限（<=0 不截断）。
	MaxLineChars int `yaml:"max_line_chars" json:"max_line_chars"`
	// CharsPerToken token 估算口径（每 token 折合字节数；<=0 用 4）。
	CharsPerToken float64 `yaml:"chars_per_token" json:"chars_per_token"`
	// DefaultPathStyle 默认路径输出形态：absolute（默认）/ virtual。
	DefaultPathStyle string `yaml:"default_path_style" json:"default_path_style"`
	// QueryCache 查询级缓存（B4）。
	QueryCache QueryCacheConfig `yaml:"query_cache" json:"query_cache"`
}

// QueryCacheConfig 查询级缓存配置（B4）。
type QueryCacheConfig struct {
	// Enabled 是否启用。默认关闭：查询缓存的正确性依赖索引变更时的主动失效，
	// 只有确实存在高频重复查询的场景才值得开启。
	Enabled bool `yaml:"enabled" json:"enabled"`
	// TTLMS 缓存存活毫秒（<=0 用 service.DefaultQueryCacheTTL=30s）。
	TTLMS int `yaml:"ttl_ms" json:"ttl_ms"`
	// MaxEntries 最多缓存条目数（<=0 用 service.DefaultQueryCacheEntries=512）。
	MaxEntries int `yaml:"max_entries" json:"max_entries"`
}

type ServerConfig struct {
	MCPAddr   string `yaml:"mcp_addr" json:"mcp_addr"`
	HTTPAddr  string `yaml:"http_addr" json:"http_addr"`
	AuthToken string `yaml:"auth_token" json:"auth_token"`
	// RateLimit 每分钟请求上限（令牌桶，突发=上限值）。0 表示不限流（默认）。
	RateLimit int `yaml:"rate_limit" json:"rate_limit"`
}

// WatcherConfig 文件监听配置。
type WatcherConfig struct {
	Enabled     bool     `yaml:"enabled" json:"enabled"`
	DebounceMs  int      `yaml:"debounce_ms" json:"debounce_ms"`
	IgnoreDirs  []string `yaml:"ignore_dirs" json:"ignore_dirs"`
	BatchSize   int      `yaml:"batch_size" json:"batch_size"`
	UseFsnotify bool     `yaml:"use_fsnotify" json:"use_fsnotify"` // 是否使用 fsnotify 原生监听（默认 false，使用 PollWatcher）
}

// ScannerConfig 扫描器配置。
type ScannerConfig struct {
	Workers         int `yaml:"workers" json:"workers"`
	FileSizeLimitMB int `yaml:"file_size_limit_mb" json:"file_size_limit_mb"`
	LineCountLimit  int `yaml:"line_count_limit" json:"line_count_limit"`
}

// DefaultConfig 返回带默认值的 Config。
func DefaultConfig() *Config {
	return &Config{
		Project: ProjectConfig{
			Name:      "",
			Root:      ".",
			Languages: []string{"go", "java", "python", "typescript", "rust", "cpp"},
		},
		Tenants: nil,
		Storage: StorageConfig{
			Driver: "file",
			DSN:    "./data",
			KV:     "",
			Vector: VectorConfig{
				// 默认文件持久化后端（PersistentStore）；仅显式配置 driver=chromem 时启用 chromem。
				// （历史：曾默认 chromem，但 runtime 未接线；现改为显式启用以保持既有行为不变）
				Driver:         "",
				DSN:            "",
				EmbeddingModel: "bge-small-zh-v1.5", // 与真实本地制品/注册表键一致；旧短名 bge-small-zh 仅作注册表远程别名
			},
			Search: SearchConfig{
				FTS:       true,
				Semantic:  true,
				FTSDir:    "./data/fts",
				VectorDir: "./data/vector",
				VectorDim: 1024,
				IDFDir:    "./data/idf",
			},
		},
		Parser: ParserConfig{
			Adapters: []string{"treesitter"},
			SCIP: SCIPConfig{
				IndexDir: DefaultSCIPIndexDir,
			},
			CodeGraph: CodeGraphConfig{
				DB: DefaultCodeGraphDB,
			},
			JCodeIndexer: JCodeIndexerConfig{
				DB:         "./jcodeindexer.db",
				ConfigFile: ".jindexer/config.yaml",
				Env:        map[string]string{},
			},
			LSP: LSPConfig{
				Enabled: false, // 默认关闭，配置 parser.lsp.enabled=true 启用（需系统安装对应语言服务器）
			},
		},
		AI: AIConfig{
			Provider:       "openai",
			Model:          "gpt-4o-mini",
			BaseURL:        "https://api.openai.com/v1",
			APIKey:         "",
			BudgetPerScan:  100,
			BudgetPerQuery: 10,
		},
		Server: ServerConfig{
			MCPAddr:   ":8080",
			HTTPAddr:  ":8081",
			AuthToken: "",
			RateLimit: 0, // 默认不限流；配置 >0 时按每分钟请求上限启用令牌桶
		},
		Watcher: WatcherConfig{
			Enabled:     true,
			DebounceMs:  300,
			IgnoreDirs:  []string{".git", "node_modules", "target", "build"},
			BatchSize:   50,
			UseFsnotify: false, // 默认使用 PollWatcher（零外部依赖）
		},
		Scanner: ScannerConfig{
			Workers:         4,
			FileSizeLimitMB: 10,
			LineCountLimit:  50000,
		},
		Context: ContextConfig{
			ContextLines:     0, // 默认只给符号体，不外扩上下文（调用方按需传）
			MaxBytes:         0, // 默认不限（向后兼容）
			MaxTokens:        0, // 默认不限
			MaxLineChars:     0, // 默认不截断
			CharsPerToken:    0, // <=0 用 trim 默认口径 4
			DefaultPathStyle: "absolute",
			QueryCache: QueryCacheConfig{
				Enabled: false, // 默认关闭：需靠索引变更主动失效，非高频重复查询场景不划算
			},
		},
	}
}

// Load 从指定路径读取 YAML/JSON 配置文件，与默认值合并后返回。
// 支持 .yaml、.yml、.json 三种格式。若文件不存在则不报错，返回默认值。
func Load(path string) (*Config, error) {
	cfg := DefaultConfig()

	if path == "" {
		return cfg, nil
	}

	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return cfg, nil
		}
		return nil, fmt.Errorf("config: read file %s: %w", path, err)
	}

	ext := strings.ToLower(filepath.Ext(path))
	switch ext {
	case ".yaml", ".yml":
		if err := yaml.Unmarshal(data, cfg); err != nil {
			return nil, fmt.Errorf("config: parse yaml %s: %w", path, err)
		}
	case ".json":
		// 先用 JSON 解析到 map，再通过 yaml.v3 的 Marshal/Unmarshal 合并到 Config
		// 这样可以复用 yaml.v3 的字段标签
		var parsed map[string]any
		if err := jsonUnmarshal(data, &parsed); err != nil {
			return nil, fmt.Errorf("config: parse json %s: %w", path, err)
		}
		if err := applyToConfig(cfg, parsed); err != nil {
			return nil, fmt.Errorf("config: apply %s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("config: unsupported config format: %s (supported: yaml, yml, json)", ext)
	}

	// 应用能力层预设（幂等；仅 YAML/JSON 显式配置 preset 时生效）。
	// 未知 preset 值在此归一为空，保持默认能力（可由 Validate 显式告警）。
	if cfg.Preset != "" && !ValidPreset(string(cfg.Preset)) {
		cfg.Preset = ""
	}
	ApplyPreset(cfg)

	return cfg, nil
}

// Validate 校验配置的合法性，返回所有错误。
func Validate(cfg *Config) []error {
	var errs []error

	if cfg.Preset != "" && !ValidPreset(string(cfg.Preset)) {
		errs = append(errs, fmt.Errorf("preset %q is unsupported (allowed: minimal, semantic, multitenant)", cfg.Preset))
	}

	if cfg.Project.Root == "" {
		errs = append(errs, fmt.Errorf("project.root must not be empty"))
	}

	// 多租户模式校验
	seen := map[string]bool{}
	for _, t := range cfg.Tenants {
		if t.ID == "" {
			errs = append(errs, fmt.Errorf("tenants: each tenant requires a non-empty id"))
			continue
		}
		if seen[t.ID] {
			errs = append(errs, fmt.Errorf("tenants: duplicate tenant id %q", t.ID))
		}
		seen[t.ID] = true
		if t.Storage.DSN == "" {
			errs = append(errs, fmt.Errorf("tenants: tenant %q requires storage.dsn", t.ID))
		}
	}

	if cfg.Storage.Driver == "" {
		errs = append(errs, fmt.Errorf("storage.driver must not be empty"))
	} else {
		switch cfg.Storage.Driver {
		case "file", "sqlite", "pg", "postgres":
			// 合法驱动；pg/postgres 需以 -tags pg 构建方可实际启用
		default:
			errs = append(errs, fmt.Errorf("storage.driver %q is unsupported (allowed: file, sqlite, pg, postgres)", cfg.Storage.Driver))
		}
	}

	if cfg.Storage.DSN == "" {
		errs = append(errs, fmt.Errorf("storage.dsn must not be empty"))
	}

	if cfg.Server.MCPAddr == "" && cfg.Server.HTTPAddr == "" {
		errs = append(errs, fmt.Errorf("at least one of server.mcp_addr or server.http_addr must be set"))
	}

	if cfg.Scanner.Workers <= 0 {
		errs = append(errs, fmt.Errorf("scanner.workers must be > 0"))
	}

	if cfg.Scanner.FileSizeLimitMB <= 0 {
		errs = append(errs, fmt.Errorf("scanner.file_size_limit_mb must be > 0"))
	}

	if cfg.Scanner.LineCountLimit <= 0 {
		errs = append(errs, fmt.Errorf("scanner.line_count_limit must be > 0"))
	}

	if cfg.Watcher.DebounceMs <= 0 {
		errs = append(errs, fmt.Errorf("watcher.debounce_ms must be > 0"))
	}

	if cfg.Watcher.BatchSize <= 0 {
		errs = append(errs, fmt.Errorf("watcher.batch_size must be > 0"))
	}

	if cfg.AI.BudgetPerScan < 0 {
		errs = append(errs, fmt.Errorf("ai.budget_per_scan must be >= 0"))
	}

	if cfg.AI.BudgetPerQuery < 0 {
		errs = append(errs, fmt.Errorf("ai.budget_per_query must be >= 0"))
	}

	return errs
}

// ---------------------------------------------------------------------------
// P9: 多配置源支持
// ---------------------------------------------------------------------------

// LoadFromEnv 从环境变量加载配置覆盖。
//
// 环境变量命名规则：CODESCHEMA_<SECTION>_<KEY>（全大写，下划线分隔）
// 例如：
//
//	CODESCHEMA_PROJECT_ROOT="/home/user/repo"
//	CODESCHEMA_STORAGE_DRIVER="sqlite"
//	CODESCHEMA_SERVER_MCP_ADDR=":9090"
//	CODESCHEMA_SCANNER_WORKERS="8"
//	CODESCHEMA_WATCHER_DEBOUNCE_MS="500"
//	CODESCHEMA_AI_BUDGET_PER_SCAN="200"
//
// LoadFromEnv 会直接修改传入的 cfg 实例，优先级高于配置文件但低于 CLI 参数。
func LoadFromEnv(cfg *Config) {
	// preset（能力层预设，设置后立即应用，幂等）
	if v := os.Getenv("CODESCHEMA_PRESET"); v != "" {
		if ValidPreset(v) {
			cfg.Preset = Preset(v)
			ApplyPreset(cfg)
		}
	}

	// project
	if v := os.Getenv("CODESCHEMA_PROJECT_ROOT"); v != "" {
		cfg.Project.Root = v
	}
	if v := os.Getenv("CODESCHEMA_PROJECT_NAME"); v != "" {
		cfg.Project.Name = v
	}

	// storage
	if v := os.Getenv("CODESCHEMA_STORAGE_DRIVER"); v != "" {
		cfg.Storage.Driver = v
	}
	if v := os.Getenv("CODESCHEMA_STORAGE_DSN"); v != "" {
		cfg.Storage.DSN = v
	}
	if v := os.Getenv("CODESCHEMA_STORAGE_KV"); v != "" {
		cfg.Storage.KV = v
	}
	if v := os.Getenv("CODESCHEMA_STORAGE_VECTOR_MODEL_DIR"); v != "" {
		cfg.Storage.Vector.ModelDir = v
	}
	if v := os.Getenv("CODESCHEMA_STORAGE_VECTOR_MODEL_DOWNLOAD_URL"); v != "" {
		cfg.Storage.Vector.ModelDownloadURL = v
	}
	if v := os.Getenv("CODESCHEMA_STORAGE_VECTOR_MODEL_SHA256"); v != "" {
		cfg.Storage.Vector.ModelSHA256 = v
	}

	// server
	if v := os.Getenv("CODESCHEMA_SERVER_MCP_ADDR"); v != "" {
		cfg.Server.MCPAddr = v
	}
	if v := os.Getenv("CODESCHEMA_SERVER_HTTP_ADDR"); v != "" {
		cfg.Server.HTTPAddr = v
	}
	if v := os.Getenv("CODESCHEMA_SERVER_AUTH_TOKEN"); v != "" {
		cfg.Server.AuthToken = v
	}
	if v := os.Getenv("CODESCHEMA_SERVER_RATE_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.Server.RateLimit = n
		}
	}

	// scanner
	if v := os.Getenv("CODESCHEMA_SCANNER_WORKERS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Scanner.Workers = n
		}
	}
	if v := os.Getenv("CODESCHEMA_SCANNER_FILE_SIZE_LIMIT_MB"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Scanner.FileSizeLimitMB = n
		}
	}
	if v := os.Getenv("CODESCHEMA_SCANNER_LINE_COUNT_LIMIT"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Scanner.LineCountLimit = n
		}
	}

	// watcher
	if v := os.Getenv("CODESCHEMA_WATCHER_ENABLED"); v != "" {
		cfg.Watcher.Enabled = v == "true" || v == "1" || v == "yes"
	}
	if v := os.Getenv("CODESCHEMA_WATCHER_DEBOUNCE_MS"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Watcher.DebounceMs = n
		}
	}
	if v := os.Getenv("CODESCHEMA_WATCHER_BATCH_SIZE"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n > 0 {
			cfg.Watcher.BatchSize = n
		}
	}
	if v := os.Getenv("CODESCHEMA_WATCHER_USE_FSNOTIFY"); v != "" {
		cfg.Watcher.UseFsnotify = v == "true" || v == "1" || v == "yes"
	}

	// ai
	if v := os.Getenv("CODESCHEMA_AI_PROVIDER"); v != "" {
		cfg.AI.Provider = v
	}
	if v := os.Getenv("CODESCHEMA_AI_MODEL"); v != "" {
		cfg.AI.Model = v
	}
	if v := os.Getenv("CODESCHEMA_AI_BASE_URL"); v != "" {
		cfg.AI.BaseURL = v
	}
	if v := os.Getenv("CODESCHEMA_AI_API_KEY"); v != "" {
		cfg.AI.APIKey = v
	}
	if v := os.Getenv("CODESCHEMA_AI_BUDGET_PER_SCAN"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.AI.BudgetPerScan = n
		}
	}
	if v := os.Getenv("CODESCHEMA_AI_BUDGET_PER_QUERY"); v != "" {
		if n, err := strconv.Atoi(v); err == nil && n >= 0 {
			cfg.AI.BudgetPerQuery = n
		}
	}

	// parser
	if v := os.Getenv("CODESCHEMA_PARSER_SCIP_INDEX_DIR"); v != "" {
		cfg.Parser.SCIP.IndexDir = v
	}
	if v := os.Getenv("CODESCHEMA_PARSER_CODEGRAPH_DB"); v != "" {
		cfg.Parser.CodeGraph.DB = v
	}
	if v := os.Getenv("CODESCHEMA_PARSER_LSP_ENABLED"); v != "" {
		if b, err := strconv.ParseBool(v); err == nil {
			cfg.Parser.LSP.Enabled = b
		}
	}
}

// Merge 合并两个配置实例，overlay 中的非零值字段会覆盖 base 的对应字段。
// 返回一个新的 Config 实例，不修改原始实例。
//
// 合并策略：
//   - 字符串：overlay 非空则覆盖
//   - 整型：overlay 值 > 0 则覆盖
//   - 布尔型：overlay 为 true 则覆盖（零值 false 不覆盖）
//   - 切片：overlay 非空则覆盖
//   - map：overlay 非空则覆盖
func Merge(base, overlay *Config) *Config {
	if base == nil {
		base = DefaultConfig()
	}
	if overlay == nil {
		return cloneConfig(base)
	}

	merged := cloneConfig(base)

	// Project
	if overlay.Project.Name != "" {
		merged.Project.Name = overlay.Project.Name
	}
	if overlay.Project.Root != "" {
		merged.Project.Root = overlay.Project.Root
	}
	if len(overlay.Project.Languages) > 0 {
		merged.Project.Languages = cloneStringSlice(overlay.Project.Languages)
	}

	// Storage
	if overlay.Storage.Driver != "" {
		merged.Storage.Driver = overlay.Storage.Driver
	}
	if overlay.Storage.DSN != "" {
		merged.Storage.DSN = overlay.Storage.DSN
	}
	if overlay.Storage.KV != "" {
		merged.Storage.KV = overlay.Storage.KV
	}
	// Vector sub
	if overlay.Storage.Vector.Driver != "" {
		merged.Storage.Vector.Driver = overlay.Storage.Vector.Driver
	}
	if overlay.Storage.Vector.DSN != "" {
		merged.Storage.Vector.DSN = overlay.Storage.Vector.DSN
	}
	if overlay.Storage.Vector.EmbeddingModel != "" {
		merged.Storage.Vector.EmbeddingModel = overlay.Storage.Vector.EmbeddingModel
	}
	if overlay.Storage.Vector.ModelDir != "" {
		merged.Storage.Vector.ModelDir = overlay.Storage.Vector.ModelDir
	}
	if overlay.Storage.Vector.ModelDownloadURL != "" {
		merged.Storage.Vector.ModelDownloadURL = overlay.Storage.Vector.ModelDownloadURL
	}
	if overlay.Storage.Vector.ModelSHA256 != "" {
		merged.Storage.Vector.ModelSHA256 = overlay.Storage.Vector.ModelSHA256
	}
	// Search sub
	mergeSearch(&merged.Storage.Search, &overlay.Storage.Search)

	// Parser
	if len(overlay.Parser.Adapters) > 0 {
		merged.Parser.Adapters = cloneStringSlice(overlay.Parser.Adapters)
	}
	if overlay.Parser.SCIP.IndexDir != "" {
		merged.Parser.SCIP.IndexDir = overlay.Parser.SCIP.IndexDir
	}
	if overlay.Parser.CodeGraph.DB != "" {
		merged.Parser.CodeGraph.DB = overlay.Parser.CodeGraph.DB
	}
	if overlay.Parser.JCodeIndexer.DB != "" {
		merged.Parser.JCodeIndexer.DB = overlay.Parser.JCodeIndexer.DB
	}
	if overlay.Parser.JCodeIndexer.ConfigFile != "" {
		merged.Parser.JCodeIndexer.ConfigFile = overlay.Parser.JCodeIndexer.ConfigFile
	}
	if len(overlay.Parser.JCodeIndexer.Env) > 0 {
		merged.Parser.JCodeIndexer.Env = cloneStringMap(overlay.Parser.JCodeIndexer.Env)
	}
	if overlay.Parser.LSP.Enabled {
		merged.Parser.LSP.Enabled = true
	}

	// AI
	if overlay.AI.Provider != "" {
		merged.AI.Provider = overlay.AI.Provider
	}
	if overlay.AI.Model != "" {
		merged.AI.Model = overlay.AI.Model
	}
	if overlay.AI.BaseURL != "" {
		merged.AI.BaseURL = overlay.AI.BaseURL
	}
	if overlay.AI.APIKey != "" {
		merged.AI.APIKey = overlay.AI.APIKey
	}
	if overlay.AI.BudgetPerScan > 0 {
		merged.AI.BudgetPerScan = overlay.AI.BudgetPerScan
	}
	if overlay.AI.BudgetPerQuery > 0 {
		merged.AI.BudgetPerQuery = overlay.AI.BudgetPerQuery
	}

	// Server
	if overlay.Server.MCPAddr != "" {
		merged.Server.MCPAddr = overlay.Server.MCPAddr
	}
	if overlay.Server.HTTPAddr != "" {
		merged.Server.HTTPAddr = overlay.Server.HTTPAddr
	}
	if overlay.Server.AuthToken != "" {
		merged.Server.AuthToken = overlay.Server.AuthToken
	}
	if overlay.Server.RateLimit > 0 {
		merged.Server.RateLimit = overlay.Server.RateLimit
	}

	// Watcher
	if overlay.Watcher.Enabled {
		merged.Watcher.Enabled = true
	}
	if overlay.Watcher.DebounceMs > 0 {
		merged.Watcher.DebounceMs = overlay.Watcher.DebounceMs
	}
	if len(overlay.Watcher.IgnoreDirs) > 0 {
		merged.Watcher.IgnoreDirs = cloneStringSlice(overlay.Watcher.IgnoreDirs)
	}
	if overlay.Watcher.BatchSize > 0 {
		merged.Watcher.BatchSize = overlay.Watcher.BatchSize
	}
	if overlay.Watcher.UseFsnotify {
		merged.Watcher.UseFsnotify = true
	}

	// Scanner
	if overlay.Scanner.Workers > 0 {
		merged.Scanner.Workers = overlay.Scanner.Workers
	}
	if overlay.Scanner.FileSizeLimitMB > 0 {
		merged.Scanner.FileSizeLimitMB = overlay.Scanner.FileSizeLimitMB
	}
	if overlay.Scanner.LineCountLimit > 0 {
		merged.Scanner.LineCountLimit = overlay.Scanner.LineCountLimit
	}

	// Preset：最后应用（覆盖同层字段，保证"只改 preset 即达预期组合"）
	if overlay.Preset != "" && ValidPreset(string(overlay.Preset)) {
		merged.Preset = overlay.Preset
		ApplyPreset(merged)
	}

	return merged
}

func mergeSearch(base, overlay *SearchConfig) {
	if overlay.FTS {
		base.FTS = true
	}
	if overlay.Semantic {
		base.Semantic = true
	}
	if overlay.FTSDir != "" {
		base.FTSDir = overlay.FTSDir
	}
	if overlay.VectorDir != "" {
		base.VectorDir = overlay.VectorDir
	}
	if overlay.VectorDim > 0 {
		base.VectorDim = overlay.VectorDim
	}
	if overlay.IDFDir != "" {
		base.IDFDir = overlay.IDFDir
	}
}

// cloneConfig 深拷贝一个 Config 实例。
func cloneConfig(cfg *Config) *Config {
	if cfg == nil {
		return nil
	}
	return &Config{
		Preset: cfg.Preset,
		Project: ProjectConfig{
			Name:      cfg.Project.Name,
			Root:      cfg.Project.Root,
			Languages: cloneStringSlice(cfg.Project.Languages),
		},
		Tenants: cloneTenantSlice(cfg.Tenants),
		Storage: StorageConfig{
			Driver: cfg.Storage.Driver,
			DSN:    cfg.Storage.DSN,
			KV:     cfg.Storage.KV,
			Vector: VectorConfig{
				Driver:         cfg.Storage.Vector.Driver,
				DSN:            cfg.Storage.Vector.DSN,
				EmbeddingModel: cfg.Storage.Vector.EmbeddingModel,
			},
			Search: SearchConfig{
				FTS:       cfg.Storage.Search.FTS,
				Semantic:  cfg.Storage.Search.Semantic,
				FTSDir:    cfg.Storage.Search.FTSDir,
				VectorDir: cfg.Storage.Search.VectorDir,
				VectorDim: cfg.Storage.Search.VectorDim,
				IDFDir:    cfg.Storage.Search.IDFDir,
			},
		},
		Parser: ParserConfig{
			Adapters: cloneStringSlice(cfg.Parser.Adapters),
			SCIP: SCIPConfig{
				IndexDir: cfg.Parser.SCIP.IndexDir,
			},
			CodeGraph: CodeGraphConfig{
				DB: cfg.Parser.CodeGraph.DB,
			},
			JCodeIndexer: JCodeIndexerConfig{
				DB:         cfg.Parser.JCodeIndexer.DB,
				ConfigFile: cfg.Parser.JCodeIndexer.ConfigFile,
				Env:        cloneStringMap(cfg.Parser.JCodeIndexer.Env),
			},
			LSP: LSPConfig{
				Enabled: cfg.Parser.LSP.Enabled,
			},
		},
		AI: AIConfig{
			Provider:       cfg.AI.Provider,
			Model:          cfg.AI.Model,
			BudgetPerScan:  cfg.AI.BudgetPerScan,
			BudgetPerQuery: cfg.AI.BudgetPerQuery,
		},
		Server: ServerConfig{
			MCPAddr:   cfg.Server.MCPAddr,
			HTTPAddr:  cfg.Server.HTTPAddr,
			AuthToken: cfg.Server.AuthToken,
			RateLimit: cfg.Server.RateLimit,
		},
		Watcher: WatcherConfig{
			Enabled:     cfg.Watcher.Enabled,
			DebounceMs:  cfg.Watcher.DebounceMs,
			IgnoreDirs:  cloneStringSlice(cfg.Watcher.IgnoreDirs),
			BatchSize:   cfg.Watcher.BatchSize,
			UseFsnotify: cfg.Watcher.UseFsnotify,
		},
		Scanner: ScannerConfig{
			Workers:         cfg.Scanner.Workers,
			FileSizeLimitMB: cfg.Scanner.FileSizeLimitMB,
			LineCountLimit:  cfg.Scanner.LineCountLimit,
		},
		Context: ContextConfig{
			ContextLines:     cfg.Context.ContextLines,
			MaxBytes:         cfg.Context.MaxBytes,
			MaxTokens:        cfg.Context.MaxTokens,
			MaxLineChars:     cfg.Context.MaxLineChars,
			CharsPerToken:    cfg.Context.CharsPerToken,
			DefaultPathStyle: cfg.Context.DefaultPathStyle,
			QueryCache: QueryCacheConfig{
				Enabled:    cfg.Context.QueryCache.Enabled,
				TTLMS:      cfg.Context.QueryCache.TTLMS,
				MaxEntries: cfg.Context.QueryCache.MaxEntries,
			},
		},
	}
}

func cloneStringSlice(s []string) []string {
	if s == nil {
		return nil
	}
	out := make([]string, len(s))
	copy(out, s)
	return out
}

func cloneStringMap(m map[string]string) map[string]string {
	if m == nil {
		return nil
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = v
	}
	return out
}

func cloneTenantSlice(s []TenantConfig) []TenantConfig {
	if s == nil {
		return nil
	}
	out := make([]TenantConfig, len(s))
	copy(out, s)
	return out
}

// ---------------------------------------------------------------------------
// P9: 配置热重载
// ---------------------------------------------------------------------------

// OnReload 是配置重载完成后的回调函数类型。
// 参数为旧的配置和新的配置，回调中可以安全地读取新配置。
type OnReload func(oldCfg, newCfg *Config)

// ConfigWatcher 监听配置文件变更并自动重载。
//
// 使用轮询方式检测文件变更（因为 fsnotify 需要外部依赖），
// 默认轮询间隔为 2 秒。当检测到文件内容变更时，自动重新加载配置
// 并通过 OnReload 回调通知应用层。
//
// 线程安全：cfgMu 保护配置的原子切换；reloadMu 保护 OnReload 回调
// 的注册与读取（SetOnReload 允许在 Start 之后调用）。
type ConfigWatcher struct {
	path         string
	cfg          *Config
	cfgMu        sync.RWMutex
	reloadMu     sync.Mutex
	onReload     OnReload
	pollInterval time.Duration
	lastModTime  time.Time
	lastSize     int64
	stopCh       chan struct{}
	stopped      bool
	stopMu       sync.Mutex
}

// NewConfigWatcher 创建一个新的配置监听器。
//
//   - path: 配置文件路径
//   - cfg: 初始配置（加载后的 Config 实例）
//   - onReload: 重载完成后的回调，可以为 nil
func NewConfigWatcher(path string, cfg *Config, onReload OnReload) *ConfigWatcher {
	fi, _ := os.Stat(path)
	var modTime time.Time
	var size int64
	if fi != nil {
		modTime = fi.ModTime()
		size = fi.Size()
	}
	return &ConfigWatcher{
		path:         path,
		cfg:          cfg,
		onReload:     onReload,
		pollInterval: 2 * time.Second,
		lastModTime:  modTime,
		lastSize:     size,
		stopCh:       make(chan struct{}),
	}
}

// SetPollInterval 设置轮询间隔。默认 2 秒。
func (cw *ConfigWatcher) SetPollInterval(d time.Duration) {
	if d > 0 {
		cw.pollInterval = d
	}
}

// SetOnReload 注册配置重载完成后的回调。可在 Start 前后任意时刻调用，
// 替换旧的回调；传 nil 可取消回调。回调在轮询 goroutine 中同步执行，
// 应快速返回，避免阻塞后续配置监听。
func (cw *ConfigWatcher) SetOnReload(fn OnReload) {
	cw.reloadMu.Lock()
	defer cw.reloadMu.Unlock()
	cw.onReload = fn
}

// GetConfig 返回当前配置（线程安全，原子切换）。
func (cw *ConfigWatcher) GetConfig() *Config {
	cw.cfgMu.RLock()
	defer cw.cfgMu.RUnlock()
	return cw.cfg
}

// Start 启动配置文件监听。阻塞直到 context 取消或 Stop 被调用。
func (cw *ConfigWatcher) Start(ctx context.Context) error {
	ticker := time.NewTicker(cw.pollInterval)
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-cw.stopCh:
			return nil
		case <-ticker.C:
			cw.checkAndReload(ctx)
		}
	}
}

// Stop 停止配置文件监听。
func (cw *ConfigWatcher) Stop() {
	cw.stopMu.Lock()
	defer cw.stopMu.Unlock()
	if !cw.stopped {
		close(cw.stopCh)
		cw.stopped = true
	}
}

// checkAndReload 检查文件是否变更，若变更则重新加载配置。
func (cw *ConfigWatcher) checkAndReload(ctx context.Context) {
	fi, err := os.Stat(cw.path)
	if err != nil {
		return // 文件不存在或无法访问，跳过
	}

	if fi.ModTime() == cw.lastModTime && fi.Size() == cw.lastSize {
		return // 文件未变更
	}

	// 文件已变更，重新加载
	newCfg, err := Load(cw.path)
	if err != nil {
		// 加载失败时保留旧配置，不中断
		return
	}

	// 应用环境变量覆盖
	LoadFromEnv(newCfg)

	oldCfg := cw.GetConfig()

	// 原子切换配置
	cw.cfgMu.Lock()
	cw.cfg = newCfg
	cw.lastModTime = fi.ModTime()
	cw.lastSize = fi.Size()
	cw.cfgMu.Unlock()

	// 通知回调
	cw.reloadMu.Lock()
	fn := cw.onReload
	cw.reloadMu.Unlock()
	if fn != nil {
		fn(oldCfg, newCfg)
	}
}
