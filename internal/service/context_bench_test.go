package service

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
)

// BenchmarkContextMode 对比 full 与 minimal 两种上下文注入模式的性能与 token 估算。
//
// 模拟多文件多符号场景（10 个文件 × 10 个符号 = 100 次查询），产出 token 节省与
// 耗时对比数据，作为"极简上下文模式评测基线"打点依据。
func BenchmarkContextMode(b *testing.B) {
	svc := newTestService(b)

	// 准备 10 个文件，每文件 10 个方法
	const numFiles = 10
	const methodsPerFile = 10
	dir := b.TempDir()

	for fi := 0; fi < numFiles; fi++ {
		var content string
		classes := make([]parser.ClassIR, 0, 1)
		methods := make([]parser.MethodIR, 0, methodsPerFile)

		clsName := fmt.Sprintf("Service%d", fi)
		clsFQN := fmt.Sprintf("com.example.Service%d", fi)
		content += fmt.Sprintf("package demo%d\n\n", fi)
		content += fmt.Sprintf("type %s struct{}\n\n", clsName)
		classes = append(classes, parser.ClassIR{
			Name:      clsName,
			FullName:  clsFQN,
			Type:      "CLASS",
			StartLine: 3,
			EndLine:   3,
		})

		baseLine := 5
		for mi := 0; mi < methodsPerFile; mi++ {
			mName := fmt.Sprintf("Method%d", mi)
			startLine := baseLine + mi*3
			endLine := startLine + 2
			content += fmt.Sprintf("func (s *%s) %s() string {\n\treturn \"result%d\"\n}\n\n", clsName, mName, mi)
			methods = append(methods, parser.MethodIR{
				Name:      mName,
				ClassFQN:  clsFQN,
				StartLine: startLine,
				EndLine:   endLine,
			})
		}

		path := filepath.Join(dir, fmt.Sprintf("svc%d.go", fi))
		if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
			b.Fatalf("write file %d: %v", fi, err)
		}

		ir := &parser.IRDocument{
			Source: "test", Language: "go", FilePath: path, FileHash: fmt.Sprintf("h%d", fi),
			LineCount: len(content), ByteSize: int64(len(content)),
			Classes: classes, Methods: methods,
		}
		if err := svc.store.UpsertIR(context.Background(), ir); err != nil {
			b.Fatalf("upsert ir %d: %v", fi, err)
		}
	}

	ctx := context.Background()

	// 提前构建符号列表
	var symbols []string
	for fi := 0; fi < numFiles; fi++ {
		clsFQN := fmt.Sprintf("com.example.Service%d", fi)
		for mi := 0; mi < methodsPerFile; mi++ {
			mFQN := clsFQN + "." + fmt.Sprintf("Method%d", mi)
			symbols = append(symbols, mFQN)
		}
	}

	b.ResetTimer()
	b.ReportAllocs()

	// full 模式：注入源码原文
	b.Run("full", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for _, sym := range symbols {
				_, err := svc.GetContextMode(ctx, sym, ContextOptions{
					Mode:         ModeFull,
					IncludeTrace: true,
				})
				if err != nil {
					b.Fatalf("GetContextMode full %s: %v", sym, err)
				}
			}
		}
	})

	// minimal 模式：仅符号元数据（快路径，零文件 IO）
	b.Run("minimal", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for _, sym := range symbols {
				_, err := svc.GetContextMode(ctx, sym, ContextOptions{
					Mode:         ModeMinimal,
					IncludeTrace: true,
				})
				if err != nil {
					b.Fatalf("GetContextMode minimal %s: %v", sym, err)
				}
			}
		}
	})

	// context_lines=5 的裁剪模式
	b.Run("context_lines_5", func(b *testing.B) {
		for i := 0; i < b.N; i++ {
			for _, sym := range symbols {
				_, err := svc.GetContextMode(ctx, sym, ContextOptions{
					ContextLines: 5,
					Mode:         ModeFull,
					IncludeTrace: true,
				})
				if err != nil {
					b.Fatalf("GetContextMode cl5 %s: %v", sym, err)
				}
			}
		}
	})
}
