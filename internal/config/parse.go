package config

import (
	"encoding/json"
	"strconv"
)

// applyToConfig 将解析后的 map 数据合并到默认配置中。
// 仅 JSON 配置使用此路径；YAML 配置直接通过 yaml.v3 的 Unmarshal 合并。
func applyToConfig(cfg *Config, parsed map[string]any) error {
	if v, ok := parsed["project"]; ok {
		if m, ok := v.(map[string]any); ok {
			applyProject(&cfg.Project, m)
		}
	}
	if v, ok := parsed["storage"]; ok {
		if m, ok := v.(map[string]any); ok {
			applyStorage(&cfg.Storage, m)
		}
	}
	if v, ok := parsed["parser"]; ok {
		if m, ok := v.(map[string]any); ok {
			applyParser(&cfg.Parser, m)
		}
	}
	if v, ok := parsed["ai"]; ok {
		if m, ok := v.(map[string]any); ok {
			applyAI(&cfg.AI, m)
		}
	}
	if v, ok := parsed["server"]; ok {
		if m, ok := v.(map[string]any); ok {
			applyServer(&cfg.Server, m)
		}
	}
	if v, ok := parsed["watcher"]; ok {
		if m, ok := v.(map[string]any); ok {
			applyWatcher(&cfg.Watcher, m)
		}
	}
	if v, ok := parsed["scanner"]; ok {
		if m, ok := v.(map[string]any); ok {
			applyScanner(&cfg.Scanner, m)
		}
	}
	return nil
}

func applyProject(cfg *ProjectConfig, m map[string]any) {
	if v, ok := m["name"].(string); ok && v != "" {
		cfg.Name = v
	}
	if v, ok := m["root"].(string); ok && v != "" {
		cfg.Root = v
	}
	if v, ok := m["languages"].([]any); ok {
		langs := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				langs = append(langs, s)
			}
		}
		if len(langs) > 0 {
			cfg.Languages = langs
		}
	}
}

func applyStorage(cfg *StorageConfig, m map[string]any) {
	if v, ok := m["driver"].(string); ok && v != "" {
		cfg.Driver = v
	}
	if v, ok := m["dsn"].(string); ok && v != "" {
		cfg.DSN = v
	}
	if v, ok := m["kv"].(string); ok {
		cfg.KV = v
	}
	if v, ok := m["vector"]; ok {
		if vm, ok := v.(map[string]any); ok {
			applyVector(&cfg.Vector, vm)
		}
	}
	if v, ok := m["search"]; ok {
		if sm, ok := v.(map[string]any); ok {
			applySearch(&cfg.Search, sm)
		}
	}
}

func applyVector(cfg *VectorConfig, m map[string]any) {
	if v, ok := m["driver"].(string); ok && v != "" {
		cfg.Driver = v
	}
	if v, ok := m["dsn"].(string); ok && v != "" {
		cfg.DSN = v
	}
	if v, ok := m["embedding_model"].(string); ok && v != "" {
		cfg.EmbeddingModel = v
	}
}

func applySearch(cfg *SearchConfig, m map[string]any) {
	if v, ok := m["fts"].(bool); ok {
		cfg.FTS = v
	}
	if v, ok := m["semantic"].(bool); ok {
		cfg.Semantic = v
	}
}

func applyParser(cfg *ParserConfig, m map[string]any) {
	if v, ok := m["adapters"].([]any); ok {
		adapters := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				adapters = append(adapters, s)
			}
		}
		if len(adapters) > 0 {
			cfg.Adapters = adapters
		}
	}
	if v, ok := m["scip"]; ok {
		if sm, ok := v.(map[string]any); ok {
			applySCIP(&cfg.SCIP, sm)
		}
	}
	if v, ok := m["codegraph"]; ok {
		if cm, ok := v.(map[string]any); ok {
			applyCodeGraph(&cfg.CodeGraph, cm)
		}
	}
	if v, ok := m["jcodeindexer"]; ok {
		if jm, ok := v.(map[string]any); ok {
			applyJCodeIndexer(&cfg.JCodeIndexer, jm)
		}
	}
}

func applySCIP(cfg *SCIPConfig, m map[string]any) {
	if v, ok := m["index_dir"].(string); ok && v != "" {
		cfg.IndexDir = v
	}
}

func applyCodeGraph(cfg *CodeGraphConfig, m map[string]any) {
	if v, ok := m["db"].(string); ok && v != "" {
		cfg.DB = v
	}
}

func applyJCodeIndexer(cfg *JCodeIndexerConfig, m map[string]any) {
	if v, ok := m["db"].(string); ok && v != "" {
		cfg.DB = v
	}
	if v, ok := m["config_file"].(string); ok && v != "" {
		cfg.ConfigFile = v
	}
	if v, ok := m["env"]; ok {
		if em, ok := v.(map[string]any); ok {
			env := make(map[string]string)
			for ek, ev := range em {
				if es, ok := ev.(string); ok {
					env[ek] = es
				}
			}
			cfg.Env = env
		}
	}
}

func applyAI(cfg *AIConfig, m map[string]any) {
	if v, ok := m["provider"].(string); ok && v != "" {
		cfg.Provider = v
	}
	if v, ok := m["model"].(string); ok && v != "" {
		cfg.Model = v
	}
	if v, ok := m["budget_per_scan"]; ok {
		if n, ok := toInt(v); ok {
			cfg.BudgetPerScan = n
		}
	}
	if v, ok := m["budget_per_query"]; ok {
		if n, ok := toInt(v); ok {
			cfg.BudgetPerQuery = n
		}
	}
}

func applyServer(cfg *ServerConfig, m map[string]any) {
	if v, ok := m["mcp_addr"].(string); ok && v != "" {
		cfg.MCPAddr = v
	}
	if v, ok := m["http_addr"].(string); ok && v != "" {
		cfg.HTTPAddr = v
	}
	if v, ok := m["auth_token"].(string); ok {
		cfg.AuthToken = v
	}
}

func applyWatcher(cfg *WatcherConfig, m map[string]any) {
	if v, ok := m["enabled"]; ok {
		if b, ok := v.(bool); ok {
			cfg.Enabled = b
		}
	}
	if v, ok := m["debounce_ms"]; ok {
		if n, ok := toInt(v); ok {
			cfg.DebounceMs = n
		}
	}
	if v, ok := m["ignore_dirs"].([]any); ok {
		dirs := make([]string, 0, len(v))
		for _, item := range v {
			if s, ok := item.(string); ok && s != "" {
				dirs = append(dirs, s)
			}
		}
		if len(dirs) > 0 {
			cfg.IgnoreDirs = dirs
		}
	}
	if v, ok := m["batch_size"]; ok {
		if n, ok := toInt(v); ok {
			cfg.BatchSize = n
		}
	}
}

func applyScanner(cfg *ScannerConfig, m map[string]any) {
	if v, ok := m["workers"]; ok {
		if n, ok := toInt(v); ok {
			cfg.Workers = n
		}
	}
	if v, ok := m["file_size_limit_mb"]; ok {
		if n, ok := toInt(v); ok {
			cfg.FileSizeLimitMB = n
		}
	}
	if v, ok := m["line_count_limit"]; ok {
		if n, ok := toInt(v); ok {
			cfg.LineCountLimit = n
		}
	}
}

func toInt(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case float64:
		return int(n), true
	case string:
		if i, err := strconv.Atoi(n); err == nil {
			return i, true
		}
	}
	return 0, false
}

// jsonUnmarshal 是 json.Unmarshal 的封装，用于 JSON 配置解析。
func jsonUnmarshal(data []byte, v any) error {
	return json.Unmarshal(data, v)
}