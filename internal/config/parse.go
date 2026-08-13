package config

import (
	"encoding/json"
	"strconv"
	"strings"
)

type yamlNode struct {
	kind  nodeKind
	key   string
	value any
}

type nodeKind int

const (
	kindMap nodeKind = iota
	kindList
	kindString
	kindNumber
	kindBool
	kindNull
)

func parseYAML(input string) (map[string]any, error) {
	lines := strings.Split(input, "\n")
	result := make(map[string]any)

	var stack []map[string]any
	stack = append(stack, result)

	for _, line := range lines {
		line = strings.TrimRight(line, " \t\r\n")

		if line == "" || isComment(line) {
			continue
		}

		indent, key, content := splitLine(line)
		if key == "" {
			continue
		}

		depth := indent / 2
		for len(stack) > depth+1 {
			stack = stack[:len(stack)-1]
		}
		current := stack[len(stack)-1]

		switch {
		case content == "" || strings.HasSuffix(content, ":"):
			// Start of a new map
			newMap := make(map[string]any)
			current[key] = newMap
			stack = append(stack, newMap)
		case strings.HasPrefix(content, "-"):
			// List item
			content = strings.TrimSpace(strings.TrimPrefix(content, "-"))
			val := parseValue(content)
			if _, ok := current[key]; !ok {
				current[key] = []any{}
			}
			if list, ok := current[key].([]any); ok {
				current[key] = append(list, val)
			}
		default:
			// Key: value
			val := parseValue(content)
			current[key] = val
		}
	}

	return result, nil
}

func isComment(line string) bool {
	l := strings.TrimLeft(line, " \t")
	return strings.HasPrefix(l, "#")
}

// stripInlineComment removes trailing inline comments from a value string.
// It finds the first unquoted # and strips everything from there.
func stripInlineComment(s string) string {
	inSingle := false
	inDouble := false
	for i := 0; i < len(s); i++ {
		ch := s[i]
		switch {
		case ch == '"' && !inSingle:
			inDouble = !inDouble
		case ch == '\'' && !inDouble:
			inSingle = !inSingle
		case ch == '#' && !inSingle && !inDouble:
			// Strip from this # to end
			return strings.TrimRight(s[:i], " \t")
		}
	}
	return s
}

func splitLine(line string) (int, string, string) {
	indent := 0
	for _, r := range line {
		if r == ' ' || r == '\t' {
			indent++
		} else {
			break
		}
	}

	trimmed := line[indent:]
	colonIdx := strings.Index(trimmed, ":")
	if colonIdx == -1 {
		return indent, trimmed, ""
	}

	key := trimmed[:colonIdx]
	content := trimmed[colonIdx+1:]
	content = strings.TrimSpace(content)

	// Strip inline comments (content before first unquoted #)
	content = stripInlineComment(content)

	return indent, key, content
}

func parseValue(s string) any {
	s = strings.TrimSpace(s)
	if s == "" {
		return ""
	}

	// Check for boolean
	if lower := strings.ToLower(s); lower == "true" {
		return true
	} else if lower == "false" {
		return false
	}

	// Check for null/nil
	if lower := strings.ToLower(s); lower == "null" || lower == "nil" {
		return nil
	}

	// Check for number
	if n, err := strconv.Atoi(s); err == nil {
		return n
	}
	if f, err := strconv.ParseFloat(s, 64); err == nil {
		return f
	}

	// Unquote if quoted
	if len(s) >= 2 && (s[0] == '"' && s[len(s)-1] == '"' || s[0] == '\'' && s[len(s)-1] == '\'') {
		s = s[1 : len(s)-1]
		return s
	}

	// Check for inline list [a, b, c]
	if strings.HasPrefix(s, "[") && strings.HasSuffix(s, "]") {
		s = strings.Trim(s, "[]")
		if s == "" {
			return []any{}
		}
		parts := strings.Split(s, ",")
		list := make([]any, 0, len(parts))
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p != "" {
				list = append(list, parseValue(p))
			}
		}
		return list
	}

	// Default to string
	return s
}

func parseJSON(data []byte) (map[string]any, error) {
	var result map[string]any
	if err := json.Unmarshal(data, &result); err != nil {
		return nil, err
	}
	return result, nil
}

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