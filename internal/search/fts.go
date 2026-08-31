package search

import (
	"github.com/idcu/codeschema/internal/retrieval"
)

// 本文件为兼容层：将通用 FTS 引擎下沉到 internal/retrieval，
// 通过类型别名与薄封装函数保持既有调用方零改动。

// FTSMode 全文搜索模式（别名到 retrieval.FTSMode）。
type FTSMode = retrieval.FTSMode

const (
	FTSModeExact   = retrieval.FTSModeExact
	FTSModeFuzzy   = retrieval.FTSModeFuzzy
	FTSModeBoolean = retrieval.FTSModeBoolean
)

// FTSEngine 全文搜索引擎接口（别名到 retrieval.FTSEngine）。
type FTSEngine = retrieval.FTSEngine

// DocEntry 内存 FTS 文档条目（别名到 retrieval.DocEntry）。
type DocEntry = retrieval.DocEntry

// MemoryFTS 纯内存全文搜索引擎（别名到 retrieval.MemoryFTS）。
type MemoryFTS = retrieval.MemoryFTS

// PersistentFTS 持久化全文搜索引擎（别名到 retrieval.PersistentFTS）。
type PersistentFTS = retrieval.PersistentFTS

// FTSError FTS 错误类型（别名到 retrieval.FTSError）。
type FTSError = retrieval.FTSError

// ErrMismatchedLength IDs 和 Contents 长度不匹配（重新指向 retrieval.ErrMismatchedLength）。
var ErrMismatchedLength = retrieval.ErrMismatchedLength

// NewMemoryFTS 创建内存全文搜索引擎。
func NewMemoryFTS() *MemoryFTS { return retrieval.NewMemoryFTS() }

// NewPersistentFTS 创建持久化全文搜索引擎。
func NewPersistentFTS(filePath string) (*PersistentFTS, error) {
	return retrieval.NewPersistentFTS(filePath)
}
