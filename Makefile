# CodeSchema Makefile
# 目标：构建、测试、交叉编译、容器化
# 默认使用 Go 原生交叉编译，CGO 目标平台需自行安装交叉编译器

BINARY   ?= codeschema
OUTPUT   ?= build
GO       ?= go
GOOS     ?= $(shell go env GOOS)
GOARCH   ?= $(shell go env GOARCH)
CGO_ENABLED ?= 0
LDFLAGS  ?= -s -w
VERSION  ?= $(shell git describe --tags --always --dirty 2>/dev/null || echo "dev")

# 当前平台构建
.PHONY: build
build:
	@echo "==> Building $(BINARY) $(VERSION) for $(GOOS)/$(GOARCH) ..."
	@mkdir -p $(OUTPUT)
	CGO_ENABLED=$(CGO_ENABLED) GOOS=$(GOOS) GOARCH=$(GOARCH) $(GO) build \
		-ldflags "$(LDFLAGS) -X main.version=$(VERSION)" \
		-o $(OUTPUT)/$(BINARY)$(if $(filter windows,$(GOOS)),.exe,) \
		./cmd/codeschema
	@echo "==> Binary: $(OUTPUT)/$(BINARY)$(if $(filter windows,$(GOOS)),.exe,)"

# 本地调试构建（含 CGO 支持）
.PHONY: build-cgo
build-cgo:
	@echo "==> Building $(BINARY) $(VERSION) with CGO_ENABLED=1 ..."
	@mkdir -p $(OUTPUT)
	CGO_ENABLED=1 $(GO) build \
		-ldflags "$(LDFLAGS) -X main.version=$(VERSION)" \
		-o $(OUTPUT)/$(BINARY)$(if $(filter windows,$(GOOS)),.exe,) \
		./cmd/codeschema
	@echo "==> Copying onnxruntime library for $(GOOS) ..."
	@case "$(GOOS)" in \
		windows) \
			lib="onnxruntime.dll";; \
		linux) \
			lib="libonnxruntime.so";; \
		darwin) \
			lib="libonnxruntime.dylib";; \
		*) \
			lib="";; \
	esac; \
	if [ -n "$$lib" ] && [ -f "down/onnxruntime/$$lib" ]; then \
		cp "down/onnxruntime/$$lib" $(OUTPUT)/; \
		echo "  -> copied $$lib"; \
	else \
		echo "  -> skipped (not found: down/onnxruntime/$$lib)"; \
	fi
	@echo "==> Binary: $(OUTPUT)/$(BINARY)$(if $(filter windows,$(GOOS)),.exe,)"
# ONNX 变体构建（含语义嵌入，-tags onnx）：输出到 build/codeschema-onnx
.PHONY: build-onnx
build-onnx:
	@echo "==> Building $(BINARY)-onnx $(VERSION) (CGO_ENABLED=1, -tags onnx) ..."
	@mkdir -p $(OUTPUT)
	CGO_ENABLED=1 $(GO) build -tags onnx \
		-ldflags "$(LDFLAGS) -X main.version=$(VERSION)" \
		-o $(OUTPUT)/$(BINARY)-onnx$(if $(filter windows,$(GOOS)),.exe,) \
		./cmd/codeschema
	@echo "==> Copying onnxruntime library for $(GOOS) ..."
	@case "$(GOOS)" in \
		windows) lib="onnxruntime.dll";; \
		linux)   lib="libonnxruntime.so";; \
		darwin)  lib="libonnxruntime.dylib";; \
		*) lib="";; \
	esac; \
	if [ -n "$$lib" ] && [ -f "down/onnxruntime/$$lib" ]; then \
		cp "down/onnxruntime/$$lib" $(OUTPUT)/; \
		echo "  -> copied $$lib"; \
	else \
		echo "  -> skipped (not found: down/onnxruntime/$$lib)"; \
	fi
	@echo "==> Binary: $(OUTPUT)/$(BINARY)-onnx$(if $(filter windows,$(GOOS)),.exe,)"


# 测试
.PHONY: test
test:
	@echo "==> Running tests ..."
	$(GO) test -count=1 -timeout 120s ./...

# 测试 + 竞态检测
.PHONY: test-race
test-race:
	@echo "==> Running tests with race detector ..."
	$(GO) test -race -count=1 -timeout 180s ./...

# 测试 + 覆盖率
.PHONY: test-cover
test-cover:
	@echo "==> Running tests with coverage ..."
	@mkdir -p $(OUTPUT)
	$(GO) test -count=1 -timeout 120s -coverprofile=$(OUTPUT)/coverage.out ./...
	$(GO) tool cover -func=$(OUTPUT)/coverage.out

# 性能基准测试
.PHONY: bench
bench:
	@echo "==> Running integration benchmarks ..."
	$(GO) test -bench=. -benchmem -count=1 -timeout 300s ./internal/integration/...
	@echo "==> Running scalebench (BulkUpsert 回归看护) ..."
	$(GO) test -run '^$$' -bench=BenchmarkScaleBulk -benchmem -count=1 -timeout 300s ./internal/scalebench/...

# 调用图精度基准（正则 + 真语法树两路径；产出 build/treesitter-callgraph-bench.json 与历史 JSONL）
.PHONY: bench-callgraph
bench-callgraph:
	@echo "==> Running treesitter callgraph precision bench (default/regex) ..."
	$(GO) test -run TestTreeSitterCallGraphBench -count=1 -timeout 120s ./internal/adapterbench/
	@echo "==> Running treesitter callgraph precision bench (-tags treesitter) ..."
	$(GO) test -tags treesitter -run TestTreeSitterCallGraphBench -count=1 -timeout 120s ./internal/adapterbench/
	@echo "==> Bench outputs: build/treesitter-callgraph-bench.json + build/treesitter-bench-history.jsonl"

# Agent 任务端到端评测（对外可信基准）：真实仓库评测 + 刷新快照
# （build/agent-task-bench/；快照归一化 repo_path=仓库名，可跨机器 diff）
# 用法：make bench-agent [AGENT_BENCH_REPO=/path/to/repo]
.PHONY: bench-agent
bench-agent:
	@echo "==> Running agent task bench (real repo, snapshot refresh) ..."
	$(GO) build -o $(OUTPUT)/codeschema ./cmd/codeschema
	$(OUTPUT)/codeschema agent-bench --repo $(if $(AGENT_BENCH_REPO),$(AGENT_BENCH_REPO),.) --out build/agent-task-bench
	@echo "==> Agent bench outputs: build/agent-task-bench/agent-task-bench.md + .json"

# 基准历史趋势可视化：读取 build/treesitter-bench-history.jsonl 生成 HTML 趋势图
.PHONY: bench-trend
bench-trend:
	@echo "==> Generating bench trend report (build/treesitter-bench-trend.html) ..."
	$(GO) run ./scripts/benchtrend
	@echo "==> Trend report: build/treesitter-bench-trend.html"

# ONNX 模型打包发布：将本地 down/models/<name> 打包为 tar.gz 并输出 SHA-256，
# 供发布到制品托管后回填 internal/vector/model_registry.go 的 DownloadURL/SHA256
# 用法：make models-pack MODEL=bge-small-zh-v1.5
.PHONY: models-pack
MODEL ?= bge-small-zh-v1.5
models-pack:
	@if [ ! -d "down/models/$(MODEL)" ]; then echo "model dir not found: down/models/$(MODEL)"; exit 1; fi
	@mkdir -p $(OUTPUT)
	@echo "==> Packing down/models/$(MODEL) -> $(OUTPUT)/models-$(MODEL).tar.gz ..."
	@tar -C down/models -czf $(OUTPUT)/models-$(MODEL).tar.gz $(MODEL)
	@echo "==> SHA-256:"
	@shasum -a 256 $(OUTPUT)/models-$(MODEL).tar.gz | tee $(OUTPUT)/models-$(MODEL).sha256
	@echo "==> 发布到制品托管后，将 URL 与 SHA-256 回填 internal/vector/model_registry.go"

# ONNX 模型本地 HTTP 分发（模拟公网制品源 / 局域网分发）：
# 把 build/ 目录（含 make models-pack 产物）起一个静态 HTTP 服务，
# 供「无本地模型」的干净环境通过 model_download_url 自动拉取。
# 用法：make models-serve PORT=8090   （默认 8090）
# 客户端配置示例：storage.vector.model_download_url = "http://<host>:8090/models-{model}.tar.gz"
.PHONY: models-serve
PORT ?= 8090
models-serve:
	@echo "==> Serving $(OUTPUT)/ on http://localhost:$(PORT)/ ..."
	@echo "    模型 URL 示例: http://localhost:$(PORT)/models-$(MODEL).tar.gz"
	@cd $(OUTPUT) && python3 -m http.server $(PORT)

# 代码检查
.PHONY: lint
lint:
	@echo "==> Running go vet ..."
	$(GO) vet ./...

# 标签隔离代码看护：解析并类型检查 onnx/pg/redis 标签隔离包（不链接运行时，轻量）
# 防止默认构建外的 ONNX 嵌入 / PG / Redis 存储代码长期无人编译而腐化。
# 等价于「该变体至少能编译」；CI 见 .github/workflows/ci.yml 的 tag-guard job。
.PHONY: verify-tags
verify-tags:
	@echo "==> Verifying tag-isolated packages (onnx/pg/redis) ..."
	$(GO) list -tags 'onnx pg redis' ./...
	@echo "==> Tag-isolated packages resolve OK."

# 项目计数审计（P3#10）：脚本化输出包数 / LoC / MCP 工具数 / HTTP 路由数，
# 取代手工数字，防止文档口径长期漂移（此前出现过 27/31/32/36 四处包数不一）。
# 用法：make counts ｜ make counts JSON=1（等价 python3 scripts/project_counts.py --json）
.PHONY: counts
counts:
	@python3 ./scripts/project_counts.py $(if $(JSON),--json,)

# 计数漂移断言（CI 闭环）：实时计数与 scripts/counts_baseline.json 比对，
# 任一结构性计数不一致即失败（退出码 1）。make counts-update 可刷新基线。
.PHONY: counts-check
counts-check:
	@python3 ./scripts/counts_check.py

# 刷新计数基线（确认本次变更属预期后使用）
.PHONY: counts-update
counts-update:
	@python3 ./scripts/project_counts.py --json > scripts/counts_baseline.json
	@echo "==> baseline refreshed: scripts/counts_baseline.json"

# 交叉编译（无 CGO，纯 Go 二进制）
CROSS_TARGETS = \
	linux/amd64 \
	linux/arm64 \
	darwin/amd64 \
	darwin/arm64 \
	windows/amd64

.PHONY: cross
cross:
	@echo "==> Cross-compiling $(BINARY) $(VERSION) ..."
	@mkdir -p $(OUTPUT)
	@for target in $(CROSS_TARGETS); do \
		os=$$(echo $$target | cut -d/ -f1); \
		arch=$$(echo $$target | cut -d/ -f2); \
		ext=$$( [ "$$os" = "windows" ] && echo ".exe" || echo "" ); \
		echo "  -> $$os/$$arch ..."; \
		CGO_ENABLED=0 GOOS=$$os GOARCH=$$arch $(GO) build \
			-ldflags "$(LDFLAGS) -X main.version=$(VERSION)" \
			-o $(OUTPUT)/$(BINARY)-$$os-$$arch$$ext \
			./cmd/codeschema; \
	done
	@echo "==> Cross-compilation complete."
	@ls -lh $(OUTPUT)/$(BINARY)-*

# 清理
.PHONY: clean
clean:
	@echo "==> Cleaning ..."
	rm -rf $(OUTPUT)
	rm -f coverage.out
	@echo "==> Done."

# 运行
.PHONY: run
run: build
	$(OUTPUT)/$(BINARY)$(if $(filter windows,$(GOOS)),.exe,) $(ARGS)

# 帮助
.PHONY: help
help:
	@echo "Usage: make <target>"
	@echo ""
	@echo "Targets:"
	@echo "  build        Build binary for current platform (CGO_ENABLED=0)"
	@echo "  build-cgo    Build binary with CGO_ENABLED=1"
	@echo "  test         Run all tests"
	@echo "  test-race    Run tests with race detector"
	@echo "  test-cover   Run tests with coverage report"
	@echo "  bench        Run benchmarks"
	@echo "  lint         Run go vet"
	@echo "  verify-tags  Verify onnx/pg/redis tag-isolated packages compile"
	@echo "  counts       Print project counts (packages/LoC/MCP tools/HTTP routes)"
	@echo "  counts-check Compare live counts against scripts/counts_baseline.json (CI drift guard)"
	@echo "  counts-update Refresh scripts/counts_baseline.json with current counts"
	@echo "  cross        Cross-compile for all platforms (CGO_ENABLED=0)"
	@echo "  clean        Clean build artifacts"
	@echo "  run          Build and run (pass ARGS=... for arguments)"
	@echo "  help         Show this help"
	@echo ""
	@echo "Variables:"
	@echo "  BINARY=codeschema    Binary name"
	@echo "  OUTPUT=build         Output directory"
	@echo "  GOOS=$$(go env GOOS)  Target OS"
	@echo "  GOARCH=$$(go env GOARCH) Target architecture"
	@echo "  VERSION=auto         Version string"