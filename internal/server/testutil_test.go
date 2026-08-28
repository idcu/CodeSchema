package server

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// seedSymbol 在 store 中写入 com.example.MyClass 类与其 myMethod 方法，并落盘真实源码。
//
// context 等工具已改为真实符号解析（GetContextMode），空库命中不到符号会返回
// ERR_SYMBOL_NOT_FOUND；本 helper 让 HTTP / MCP 的成功路径用例可稳定命中。
// 文件内容（行号与 ClassIR/MethodIR 行区间对齐）：
//
//	1  package demo
//	2
//	3  type MyClass struct{}
//	4
//	5  func (c *MyClass) myMethod() string {
//	6      return "ok"
//	7  }
//
// seedSymbol 写入种子文件并入库，返回文件绝对路径。
//
// 返回值供需要真实路径的用例使用（如路径虚拟化要拿文件所在目录建虚拟根）；
// 多数调用点直接忽略返回值即可（Go 允许丢弃返回值）。
func seedSymbol(t testing.TB, st store.Store) string {
	t.Helper()
	content := "package demo\n\ntype MyClass struct{}\n\nfunc (c *MyClass) myMethod() string {\n\treturn \"ok\"\n}\n"
	path := filepath.Join(t.TempDir(), "myclass.go")
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write seed file: %v", err)
	}

	ir := &parser.IRDocument{
		Source:    "test",
		Language:  "go",
		FilePath:  path,
		FileHash:  "h-myclass",
		LineCount: 7,
		ByteSize:  int64(len(content)),
		Classes: []parser.ClassIR{{
			Name:      "MyClass",
			FullName:  "com.example.MyClass",
			Type:      "CLASS",
			StartLine: 3,
			EndLine:   3,
		}},
		Methods: []parser.MethodIR{{
			Name:      "myMethod",
			ClassFQN:  "com.example.MyClass",
			Signature: "myMethod() string",
			StartLine: 5,
			EndLine:   7,
		}},
	}
	if err := st.UpsertIR(context.Background(), ir); err != nil {
		t.Fatalf("upsert seed ir: %v", err)
	}
	return path
}
