package main

import (
	"context"
	"log"
	"os/exec"

	"github.com/idcu/codeschema/internal/config"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/parser/adapter/codegraph"
	lspadapter "github.com/idcu/codeschema/internal/parser/adapter/lsp"
	"github.com/idcu/codeschema/internal/parser/adapter/scip"
	treesitter "github.com/idcu/codeschema/internal/parser/adapter/treesitter"
)

// newParserRegistry 构建统一的解析适配器注册中心（编排主路入口）。
//
// 注册策略（T1-3 落地：LSP 接入 Registry 编排主路）：
//  1. tree-sitter 适配器始终注册（30 语言正则，零依赖兜底）——修复此前
//     scan/watch 创建空 Registry 导致符号从未解析的隐藏缺陷；
//  2. 配置 parser.lsp.enabled=true 且系统存在对应语言服务器时，注册
//     gopls/jdtls/clangd 适配器（FallbackParser 包装：LSP 失败自动回退
//     tree-sitter，全链路可观测），并置为对应语言的最高优先级；
//  3. SCIP / CodeGraph 适配器按配置注册（可选），同样是「高精度优先、失败回退」。
//
// 语言优先级（高精度优先）：LSP(go/java/cpp) > SCIP > CodeGraph > tree-sitter。
func newParserRegistry(ctx context.Context, cfg *config.Config, rootDir string) *parser.Registry {
	reg := parser.NewRegistry()

	// ① 兜底：tree-sitter 始终注册（默认构建为 30 语言正则，零 CGO）
	ts := treesitter.NewTreeSitterAdapter()
	reg.Register(ts)

	// ② LSP 适配器（可选，配置启用 + 工具存在时注册）
	if cfg.Parser.LSP.Enabled {
		lspAdapters := []parser.ParserPlugin{
			lspadapter.NewGoplsAdapter(),
			lspadapter.NewJDTLSAdapter(),
			lspadapter.NewClangdAdapter(),
		}
		registered := 0
		for _, la := range lspAdapters {
			if !commandAvailable(la.Name()) {
				log.Printf("parser.lsp: %s not found in PATH, skipping (fallback to tree-sitter)", la.Name())
				continue
			}
			// rootUri 提供给 LSP 子进程作为工作区根（clangd 需用于探测 compile_commands.json）
			if err := la.Init(ctx, map[string]any{"rootUri": "file://" + rootDir}); err != nil {
				log.Printf("parser.lsp: %s init failed (%v), skipping (fallback to tree-sitter)", la.Name(), err)
				continue
			}
			// FallbackParser：LSP 解析失败自动回退 tree-sitter，不中断扫描
			reg.Register(parser.NewFallbackParser(la, ts))
			registered++
		}
		if registered == 0 {
			log.Printf("parser.lsp: enabled but no language server available, using tree-sitter only")
		}
	}

	// ③ SCIP 适配器（可选：配置了 index_dir 才注册）
	if dir := cfg.Parser.SCIP.IndexDir; dir != "" && dir != "./scipout" {
		sc := scip.NewSCIPAdapter(dir)
		if err := sc.Init(ctx, map[string]any{"index_dir": dir}); err != nil {
			log.Printf("parser.scip: init failed (%v), skipping", err)
		} else {
			reg.Register(parser.NewFallbackParser(sc, ts))
		}
	}

	// ④ CodeGraph 适配器（可选：配置了 db 才注册）
	if db := cfg.Parser.CodeGraph.DB; db != "" && db != "./codegraph.db" {
		cg := codegraph.NewCodeGraphAdapter(db)
		if err := cg.Init(ctx, map[string]any{"db_path": db}); err != nil {
			log.Printf("parser.codegraph: init failed (%v), skipping", err)
		} else {
			reg.Register(parser.NewFallbackParser(cg, ts))
		}
	}

	// 语言优先级（高精度优先；未列出的语言走注册顺序 = tree-sitter）
	reg.SetPriority("go", []string{"gopls", "codegraph", "scip", "treesitter"})
	reg.SetPriority("java", []string{"jdtls", "codegraph", "scip", "treesitter"})
	reg.SetPriority("cpp", []string{"clangd", "codegraph", "scip", "treesitter"})
	reg.SetPriority("ts", []string{"codegraph", "scip", "treesitter"})
	reg.SetPriority("py", []string{"codegraph", "scip", "treesitter"})
	reg.SetPriority("rust", []string{"codegraph", "scip", "treesitter"})

	return reg
}

// commandAvailable 检查 PATH 中是否存在指定命令。
func commandAvailable(name string) bool {
	_, err := exec.LookPath(name)
	return err == nil
}
