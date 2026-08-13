package metrics

import (
	"strings"
	"testing"
)

func TestRegisterCounter(t *testing.T) {
	Reset()
	RegisterCounter("test_counter", "A test counter")
	IncCounter("test_counter")
	IncCounter("test_counter")

	snap := Collect()
	if snap.Counters["test_counter"] != 2 {
		t.Errorf("expected 2, got %v", snap.Counters["test_counter"])
	}
}

func TestRegisterGauge(t *testing.T) {
	Reset()
	RegisterGauge("test_gauge", "A test gauge")
	SetGauge("test_gauge", 42)

	snap := Collect()
	if snap.Gauges["test_gauge"] != 42 {
		t.Errorf("expected 42, got %v", snap.Gauges["test_gauge"])
	}

	SetGauge("test_gauge", 100)
	snap = Collect()
	if snap.Gauges["test_gauge"] != 100 {
		t.Errorf("expected 100, got %v", snap.Gauges["test_gauge"])
	}
}

func TestIncGauge(t *testing.T) {
	Reset()
	RegisterGauge("test_gauge", "A test gauge")
	SetGauge("test_gauge", 10)
	IncGauge("test_gauge")
	DecGauge("test_gauge")
	IncGauge("test_gauge")

	snap := Collect()
	if snap.Gauges["test_gauge"] != 11 {
		t.Errorf("expected 11, got %v", snap.Gauges["test_gauge"])
	}
}

func TestCounterWithLabels(t *testing.T) {
	Reset()
	RegisterCounter("http_requests", "HTTP requests", "method", "path")
	IncCounter("http_requests", "GET", "/api/health")
	IncCounter("http_requests", "GET", "/api/health")
	IncCounter("http_requests", "POST", "/api/data")

	output := Render()
	if !strings.Contains(output, "http_requests") {
		t.Error("expected http_requests in output")
	}
	if !strings.Contains(output, `method="GET"`) {
		t.Error("expected method=\"GET\" in output")
	}
	if !strings.Contains(output, `path="/api/health"`) {
		t.Error(`expected path="/api/health" in output`)
	}
}

func TestGaugeWithLabels(t *testing.T) {
	Reset()
	RegisterGauge("index_total", "Index total", "language")
	SetGauge("index_total", 100, "go")
	SetGauge("index_total", 50, "java")

	output := Render()
	if !strings.Contains(output, `language="go"`) {
		t.Error("expected language=\"go\" in output")
	}
}

func TestRender(t *testing.T) {
	Reset()
	RegisterCounter("requests_total", "Total requests")
	RegisterGauge("workers_idle", "Idle workers")

	IncCounter("requests_total")
	IncCounter("requests_total")
	IncCounter("requests_total")
	IncCounter("requests_total")
	IncCounter("requests_total")
	SetGauge("workers_idle", 3)

	output := Render()

	// 检查 HELP 和 TYPE
	if !strings.Contains(output, "# HELP requests_total Total requests") {
		t.Error("expected HELP line")
	}
	if !strings.Contains(output, "# TYPE requests_total counter") {
		t.Error("expected TYPE line")
	}
	if !strings.Contains(output, "requests_total 5") {
		t.Error("expected counter value")
	}
	if !strings.Contains(output, "workers_idle 3") {
		t.Error("expected gauge value")
	}
}

func TestRender_Empty(t *testing.T) {
	Reset()
	output := Render()
	if output != "" {
		t.Errorf("expected empty output, got: %s", output)
	}
}

func TestCounter_Unregistered(t *testing.T) {
	Reset()
	IncCounter("nonexistent") // should not panic
	AddCounter("nonexistent", 5)
}

func TestGauge_Unregistered(t *testing.T) {
	Reset()
	SetGauge("nonexistent", 42) // should not panic
	IncGauge("nonexistent")
	DecGauge("nonexistent")
}

func TestReset(t *testing.T) {
	Reset()
	RegisterCounter("test", "test")
	IncCounter("test")

	Reset()
	snap := Collect()
	if len(snap.Counters) != 0 {
		t.Error("expected empty after reset")
	}
}

func TestAddCounter(t *testing.T) {
	Reset()
	RegisterCounter("test", "test")
	AddCounter("test", 10)
	AddCounter("test", 5.5)

	snap := Collect()
	if snap.Counters["test"] != 15.5 {
		t.Errorf("expected 15.5, got %v", snap.Counters["test"])
	}
}

func TestMultipleLabelValues(t *testing.T) {
	Reset()
	RegisterCounter("db_queries", "DB queries", "db", "table", "operation")
	IncCounter("db_queries", "main", "users", "select")
	IncCounter("db_queries", "main", "users", "select")
	IncCounter("db_queries", "cache", "orders", "insert")

	output := Render()
	lines := strings.Split(strings.TrimSpace(output), "\n")

	// 过滤指标数据行（不含 # 前缀）
	dataLines := 0
	for _, line := range lines {
		if !strings.HasPrefix(line, "#") && strings.TrimSpace(line) != "" {
			dataLines++
		}
	}
	if dataLines != 2 {
		t.Errorf("expected 2 data lines, got %d", dataLines)
	}
}

func TestSnapshot_WithLabels(t *testing.T) {
	Reset()
	RegisterCounter("labeled", "Labeled", "tag")
	IncCounter("labeled", "a")
	IncCounter("labeled", "b")

	// 带标签的指标不纳入 Snapshot
	snap := Collect()
	if _, ok := snap.Counters["labeled"]; ok {
		t.Error("labeled counter should not appear in Snapshot")
	}
}