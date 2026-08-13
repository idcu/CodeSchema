// Package config 提供 YAML/JSON 配置文件的加载与管理。
//
// 使用 gopkg.in/yaml.v3 解析 YAML 配置文件，支持全部 YAML 语法。
// JSON 配置文件通过 encoding/json 解析。
package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Config 顶层配置结构。
type Config struct {
	Project ProjectConfig `yaml:"project" json:"project"`
	Storage StorageConfig `yaml:"storage" json:"storage"`
	Parser  ParserConfig  `yaml:"parser" json:"parser"`
	AI      AIConfig      `yaml:"ai" json:"ai"`
	Server  ServerConfig  `yaml:"server" json:"server"`
	Watcher WatcherConfig `yaml:"watcher" json:"watcher"`
	Scanner ScannerConfig `yaml:"scanner" json:"scanner"`
}

// ProjectConfig 项目配置。
type ProjectConfig struct {
	Name      string   `yaml:"name" json:"name"`
	Root      string   `yaml:"root" json:"root"`
	Languages []string `yaml:"languages" json:"languages"`
}

// StorageConfig 存储配置。
type StorageConfig struct {
	Driver string        `yaml:"driver" json:"driver"`
	DSN    string        `yaml:"dsn" json:"dsn"`
	KV     string        `yaml:"kv" json:"kv"`
	Vector VectorConfig  `yaml:"vector" json:"vector"`
	Search SearchConfig  `yaml:"search" json:"search"`
}

// VectorConfig 向量库配置。
type VectorConfig struct {
	Driver         string `yaml:"driver" json:"driver"`
	DSN            string `yaml:"dsn" json:"dsn"`
	EmbeddingModel string `yaml:"embedding_model" json:"embedding_model"`
}

// SearchConfig 搜索配置。
type SearchConfig struct {
	FTS      bool `yaml:"fts" json:"fts"`
	Semantic bool `yaml:"semantic" json:"semantic"`
}

// ParserConfig 解析器配置。
type ParserConfig struct {
	Adapters     []string           `yaml:"adapters" json:"adapters"`
	SCIP         SCIPConfig         `yaml:"scip" json:"scip"`
	CodeGraph    CodeGraphConfig    `yaml:"codegraph" json:"codegraph"`
	JCodeIndexer JCodeIndexerConfig `yaml:"jcodeindexer" json:"jcodeindexer"`
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
	Provider       string `yaml:"provider" json:"provider"`
	Model          string `yaml:"model" json:"model"`
	BudgetPerScan  int    `yaml:"budget_per_scan" json:"budget_per_scan"`
	BudgetPerQuery int    `yaml:"budget_per_query" json:"budget_per_query"`
}

// ServerConfig 服务器配置。
type ServerConfig struct {
	MCPAddr   string `yaml:"mcp_addr" json:"mcp_addr"`
	HTTPAddr  string `yaml:"http_addr" json:"http_addr"`
	AuthToken string `yaml:"auth_token" json:"auth_token"`
}

// WatcherConfig 文件监听配置。
type WatcherConfig struct {
	Enabled    bool     `yaml:"enabled" json:"enabled"`
	DebounceMs int      `yaml:"debounce_ms" json:"debounce_ms"`
	IgnoreDirs []string `yaml:"ignore_dirs" json:"ignore_dirs"`
	BatchSize  int      `yaml:"batch_size" json:"batch_size"`
}

// ScannerConfig 扫描器配置。
type ScannerConfig struct {
	Workers          int `yaml:"workers" json:"workers"`
	FileSizeLimitMB  int `yaml:"file_size_limit_mb" json:"file_size_limit_mb"`
	LineCountLimit   int `yaml:"line_count_limit" json:"line_count_limit"`
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