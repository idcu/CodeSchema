// Package config 提供 YAML 配置文件的加载与管理。
//
// 由于网络不可用无法下载 gopkg.in/yaml.v3，本包实现了一个最小 YAML 子集解析器，
// 覆盖 CodeSchema 配置文件的全部语法（键值对、嵌套映射、列表、注释、基本类型）。
// 后续网络恢复后可切换到 yaml.v3。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

// Config 顶层配置结构。
type Config struct {
	Project ProjectConfig `json:"project"`
	Storage StorageConfig `json:"storage"`
	Parser  ParserConfig  `json:"parser"`
	AI      AIConfig      `json:"ai"`
	Server  ServerConfig  `json:"server"`
	Watcher WatcherConfig `json:"watcher"`
	Scanner ScannerConfig `json:"scanner"`
}

// ProjectConfig 项目配置。
type ProjectConfig struct {
	Name      string   `json:"name"`
	Root      string   `json:"root"`
	Languages []string `json:"languages"`
}

// StorageConfig 存储配置。
type StorageConfig struct {
	Driver string        `json:"driver"`
	DSN    string        `json:"dsn"`
	KV     string        `json:"kv"`
	Vector VectorConfig  `json:"vector"`
	Search SearchConfig  `json:"search"`
}

// VectorConfig 向量库配置。
type VectorConfig struct {
	Driver         string `json:"driver"`
	DSN            string `json:"dsn"`
	EmbeddingModel string `json:"embedding_model"`
}

// SearchConfig 搜索配置。
type SearchConfig struct {
	FTS      bool `json:"fts"`
	Semantic bool `json:"semantic"`
}

// ParserConfig 解析器配置。
type ParserConfig struct {
	Adapters    []string            `json:"adapters"`
	SCIP        SCIPConfig          `json:"scip"`
	CodeGraph   CodeGraphConfig     `json:"codegraph"`
	JCodeIndexer JCodeIndexerConfig `json:"jcodeindexer"`
}

// SCIPConfig SCIP 适配器配置。
type SCIPConfig struct {
	IndexDir string `json:"index_dir"`
}

// CodeGraphConfig CodeGraph 适配器配置。
type CodeGraphConfig struct {
	DB string `json:"db"`
}

// JCodeIndexerConfig JCodeIndexer 适配器配置。
type JCodeIndexerConfig struct {
	DB         string            `json:"db"`
	ConfigFile string            `json:"config_file"`
	Env        map[string]string `json:"env"`
}

// AIConfig AI 增强配置。
type AIConfig struct {
	Provider       string `json:"provider"`
	Model          string `json:"model"`
	BudgetPerScan  int    `json:"budget_per_scan"`
	BudgetPerQuery int    `json:"budget_per_query"`
}

// ServerConfig 服务器配置。
type ServerConfig struct {
	MCPAddr   string `json:"mcp_addr"`
	HTTPAddr  string `json:"http_addr"`
	AuthToken string `json:"auth_token"`
}

// WatcherConfig 文件监听配置。
type WatcherConfig struct {
	Enabled    bool     `json:"enabled"`
	DebounceMs int      `json:"debounce_ms"`
	IgnoreDirs []string `json:"ignore_dirs"`
	BatchSize  int      `json:"batch_size"`
}

// ScannerConfig 扫描器配置。
type ScannerConfig struct {
	Workers          int `json:"workers"`
	FileSizeLimitMB  int `json:"file_size_limit_mb"`
	LineCountLimit   int `json:"line_count_limit"`
}

// DefaultConfig 返回带默认值的 Config。
func DefaultConfig() *Config {
	return &Config{
		Project: ProjectConfig{
			Name:      "",
			Root:      ".",
			Languages: []string{"go", "java", "python", "typescript", "rust", "cpp"},
		},
		Storage: StorageConfig{
			Driver: "file",
			DSN:    "./data",
			KV:     "",
			Vector: VectorConfig{
				Driver:         "chromem",
				DSN:            "./vector.db",
				EmbeddingModel: "bge-small-zh",
			},
			Search: SearchConfig{
				FTS:      true,
				Semantic: false,
			},
		},
		Parser: ParserConfig{
			Adapters: []string{"treesitter"},
			SCIP: SCIPConfig{
				IndexDir: "./scipout",
			},
			CodeGraph: CodeGraphConfig{
				DB: "./codegraph.db",
			},
			JCodeIndexer: JCodeIndexerConfig{
				DB:         "./jcodeindexer.db",
				ConfigFile: ".jindexer/config.yaml",
				Env:        map[string]string{},
			},
		},
		AI: AIConfig{
			Provider:       "openai",
			Model:          "gpt-4o-mini",
			BudgetPerScan:  100,
			BudgetPerQuery: 10,
		},
		Server: ServerConfig{
			MCPAddr:   ":8080",
			HTTPAddr:  ":8081",
			AuthToken: "",
		},
		Watcher: WatcherConfig{
			Enabled:    true,
			DebounceMs: 300,
			IgnoreDirs: []string{".git", "node_modules", "target", "build"},
			BatchSize:  50,
		},
		Scanner: ScannerConfig{
			Workers:          4,
			FileSizeLimitMB:  10,
			LineCountLimit:   50000,
		},
	}
}

// Load 从指定路径读取 YAML 配置文件，与默认值合并后返回。
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
		parsed, err := parseYAML(string(data))
		if err != nil {
			return nil, fmt.Errorf("config: parse yaml %s: %w", path, err)
		}
		if err := applyToConfig(cfg, parsed); err != nil {
			return nil, fmt.Errorf("config: apply %s: %w", path, err)
		}
	case ".json":
		parsed, err := parseJSON(data)
		if err != nil {
			return nil, fmt.Errorf("config: parse json %s: %w", path, err)
		}
		if err := applyToConfig(cfg, parsed); err != nil {
			return nil, fmt.Errorf("config: apply %s: %w", path, err)
		}
	default:
		return nil, fmt.Errorf("config: unsupported config format: %s (supported: yaml, yml, json)", ext)
	}

	return cfg, nil
}

// Validate 校验配置的合法性，返回所有错误。
func Validate(cfg *Config) []error {
	var errs []error

	if cfg.Project.Root == "" {
		errs = append(errs, fmt.Errorf("project.root must not be empty"))
	}

	if cfg.Storage.Driver == "" {
		errs = append(errs, fmt.Errorf("storage.driver must not be empty"))
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