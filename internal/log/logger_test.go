package log

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestInit(t *testing.T) {
	var buf bytes.Buffer
	InitWriter(&buf, LevelDebug, true)

	if defaultLogger == nil {
		t.Fatal("defaultLogger should not be nil after Init")
	}
}

func TestLogLevels(t *testing.T) {
	var buf bytes.Buffer
	InitWriter(&buf, LevelDebug, true)

	// 记录所有级别日志
	Debug("debug message", "key", "debug_val")
	Info("info message", "key", "info_val")
	Warn("warn message", "key", "warn_val")
	Error("error message", "key", "error_val")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 4 {
		t.Fatalf("expected 4 log lines, got %d", len(lines))
	}

	// 验证级别顺序
	levels := []string{"DEBUG", "INFO", "WARN", "ERROR"}
	for i, line := range lines {
		var entry map[string]any
		if err := json.Unmarshal([]byte(line), &entry); err != nil {
			t.Fatalf("line %d: unmarshal error: %v", i, err)
		}
		if entry["level"] != levels[i] {
			t.Errorf("line %d: expected level %s, got %v", i, levels[i], entry["level"])
		}
	}
}

func TestLogLevelFilter(t *testing.T) {
	var buf bytes.Buffer
	InitWriter(&buf, LevelWarn, true) // 只记录 WARN 及以上

	Info("should be filtered")
	Warn("should appear")
	Error("should appear")

	lines := strings.Split(strings.TrimSpace(buf.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 log lines, got %d", len(lines))
	}
}

func TestWithModule(t *testing.T) {
	var buf bytes.Buffer
	InitWriter(&buf, LevelInfo, true)

	logger := WithModule("test_module")
	logger.Info("module test", "key", "val")

	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &entry); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if entry["module"] != "test_module" {
		t.Errorf("expected module 'test_module', got %v", entry["module"])
	}
}

func TestWithExtraFields(t *testing.T) {
	var buf bytes.Buffer
	InitWriter(&buf, LevelInfo, true)

	logger := L().With("request_id", "abc-123")
	logger.Info("with fields", "key", "val")

	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &entry); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	if entry["request_id"] != "abc-123" {
		t.Errorf("expected request_id 'abc-123', got %v", entry["request_id"])
	}
}

func TestJSONFormat(t *testing.T) {
	var buf bytes.Buffer
	InitWriter(&buf, LevelInfo, true)

	Info("json test", "latency_ms", 42)

	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &entry); err != nil {
		t.Fatalf("unmarshal error: %v, json: %s", err, buf.String())
	}

	if entry["msg"] != "json test" {
		t.Errorf("expected msg 'json test', got %v", entry["msg"])
	}

	if entry["latency_ms"] != float64(42) {
		t.Errorf("expected latency_ms 42, got %v", entry["latency_ms"])
	}

	// 验证 timestamp 字段存在
	if _, ok := entry["timestamp"]; !ok {
		t.Error("expected timestamp field")
	}

	// 验证 caller 字段存在
	if _, ok := entry["caller"]; !ok {
		t.Error("expected caller field")
	}
}

func TestTextFormat(t *testing.T) {
	var buf bytes.Buffer
	InitWriter(&buf, LevelInfo, false)

	Info("text test")

	output := buf.String()
	if !strings.Contains(output, "text test") {
		t.Errorf("expected 'text test' in output, got: %s", output)
	}
}

func TestLazyInit(t *testing.T) {
	// 重置 defaultLogger
	defaultLogger = nil

	// L() 应该自动初始化
	logger := L()
	if logger == nil {
		t.Fatal("L() should return non-nil logger")
	}
	if logger.inner == nil {
		t.Fatal("logger.inner should not be nil")
	}
}

func TestWithModuleChain(t *testing.T) {
	var buf bytes.Buffer
	InitWriter(&buf, LevelInfo, true)

	l1 := WithModule("module_a")
	l2 := l1.WithModule("module_b")

	l2.Info("chained")

	var entry map[string]any
	if err := json.Unmarshal([]byte(strings.TrimSpace(buf.String())), &entry); err != nil {
		t.Fatalf("unmarshal error: %v", err)
	}

	// module 会被覆盖
	if entry["module"] != "module_b" {
		t.Errorf("expected module 'module_b', got %v", entry["module"])
	}
}

func TestArgsToAttrs(t *testing.T) {
	attrs := argsToAttrs([]any{"key1", "val1", "key2", 42})
	if len(attrs) != 2 {
		t.Fatalf("expected 2 attrs, got %d", len(attrs))
	}
	if attrs[0].Key != "key1" {
		t.Errorf("expected key 'key1', got %s", attrs[0].Key)
	}
}

func TestArgsToAttrs_OddCount(t *testing.T) {
	attrs := argsToAttrs([]any{"key1", "val1", "orphan"})
	if len(attrs) != 1 {
		t.Fatalf("expected 1 attr, got %d", len(attrs))
	}
}

func TestArgsToAttrs_Empty(t *testing.T) {
	attrs := argsToAttrs(nil)
	if attrs != nil {
		t.Fatalf("expected nil, got %v", attrs)
	}
}

func TestArgsToAttrs_NonStringKey(t *testing.T) {
	attrs := argsToAttrs([]any{42, "val1", "key2", "val2"})
	if len(attrs) != 1 {
		t.Fatalf("expected 1 attr, got %d", len(attrs))
	}
	if attrs[0].Key != "key2" {
		t.Errorf("expected key 'key2', got %s", attrs[0].Key)
	}
}