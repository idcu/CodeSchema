// Package store 提供 CodeSchema 系统的存储层。
//
// 当前实现：基于 Go 标准库的 JSON 文件存储（P0 零依赖）。
// 后续可切换为 SQLite（internal/store/sqlite 子包）。
//
// Store 接口定义了所有存储操作，支持两种实现：
// - FileStore：基于 JSON 文件的轻量实现（P0 默认）
// - SQLiteStore：基于 SQLite 的关系型实现（P1 可选）
package store

import (
	"context"
	"github.com/idcu/codeschema/internal/parser"
)

// Store 是存储层统一接口。
type Store interface {
	// Open 打开存储，初始化数据目录。
	Open(ctx context.Context, dsn string) error

	// Close 关闭存储，释放资源。
	Close() error

	// UpsertFile 插入或更新文件记录。
	UpsertFile(ctx context.Context, filePath string, contentHash string, lineCount int, byteSize int64) (int64, error)

	// GetFileByPath 按路径查询文件。
	GetFileByPath(ctx context.Context, path string) (*FileRecord, error)

	// GetFileByID 按 ID 查询文件。
	GetFileByID(ctx context.Context, id int64) (*FileRecord, error)

	// DeleteFile 删除文件及其级联数据。
	DeleteFile(ctx context.Context, fileID int64) error

	// UpsertClasses 批量更新类记录（全量替换）。
	UpsertClasses(ctx context.Context, fileID int64, classes []parser.ClassIR) error

	// UpsertMethods 批量更新方法记录（全量替换）。
	UpsertMethods(ctx context.Context, classID int64, methods []parser.MethodIR) error

	// UpsertCalls 批量更新调用关系（全量替换）。
	UpsertCalls(ctx context.Context, fileID int64, calls []parser.CallIR) error

	// UpsertIR 对一个文件的 IR 执行增量入库。
	UpsertIR(ctx context.Context, ir *parser.IRDocument) error

	// BulkUpsert 批量入库多个文件的 IR（语义同逐文件 UpsertIR，但置于单事务/单批，
	// 消除逐文件事务提交放大，用于超大仓首次灌入或整仓重索引）。
	BulkUpsert(ctx context.Context, irs []*parser.IRDocument) error

	// GetAllFiles 返回所有文件记录。
	GetAllFiles(ctx context.Context) ([]*FileRecord, error)

	// GetClassesByFileID 按文件 ID 查询类记录。
	GetClassesByFileID(ctx context.Context, fileID int64) ([]ClassRecord, error)

	// GetMethodsByClassID 按类 ID 查询方法记录。
	GetMethodsByClassID(ctx context.Context, classID int64) ([]MethodRecord, error)

	// GetCallsByFileID 按文件 ID 查询调用关系。
	GetCallsByFileID(ctx context.Context, fileID int64) ([]CallRecord, error)

	// UpsertTags 设置类标签（全量替换）。
	UpsertTags(ctx context.Context, classID int64, tags []string) error

	// UpsertMethodTags 设置方法标签（全量替换）。
	UpsertMethodTags(ctx context.Context, methodID int64, tags []string) error

	// GetTagsByClassID 获取类的标签列表。
	GetTagsByClassID(ctx context.Context, classID int64) ([]string, error)

	// GetTagsByMethodID 获取方法的标签列表。
	GetTagsByMethodID(ctx context.Context, methodID int64) ([]string, error)

	// SearchByTags 按多个标签（AND）搜索类和方法的 ID 列表。
	// 返回同时拥有所有指定标签的类和方法（交集）。
	SearchByTags(ctx context.Context, tags []string) (classIDs []int64, methodIDs []int64, err error)

	// GetAllTagsWithCategories 返回所有已知标签及其分类。
	GetAllTagsWithCategories(ctx context.Context) (map[string]string, error)

	// HealthCheck 返回存储层健康状态。
	HealthCheck(ctx context.Context) error
}

// FileRecord 对应 file 表的一行记录。
type FileRecord struct {
	ID                int64    `json:"id"`
	AbsolutePath      string   `json:"absolute_path"`
	ContentHash       string   `json:"content_hash,omitempty"`
	LineCount         int      `json:"line_count"`
	ByteSize          int64    `json:"byte_size"`
	ReferencedByFiles []string `json:"referenced_by_files,omitempty"`
	Imports           []string `json:"imports,omitempty"`
	Language          string   `json:"language,omitempty"`
	ParseStatus       string   `json:"parse_status"`
}

// DriverNamer 可选接口：返回存储驱动名（file/sqlite/pg/...）。
// 供健康检查等按实例精确报告驱动类型；未实现时由调用方按默认推断。
type DriverNamer interface {
	DriverName() string
}

// SkippedWriter 可选接口：对因超限被旁路（大文件/超行数）的文件标记 parse_skipped 留痕。
// FileStore/SQLiteStore/PGStore 均实现；未实现该接口的 Store 保留原行为（UpsertFile）。
type SkippedWriter interface {
	// MarkParseSkipped 记录一个被旁路的文件，parse_status 置为 "parse_skipped"。
	MarkParseSkipped(ctx context.Context, filePath string, byteSize int64, lineCount int) (int64, error)
}

// CacheReader 可选接口：Redis L2 缓存读路径（按 FQN 热点类/调用反查）。
// 由 cmd 层的 redisCacheStore 实现；service 侧探测到该接口时可优先走缓存，
// 未命中或未实现时回退主存储，保证数据一致性与向后兼容。
type CacheReader interface {
	// GetClass 按全限定名读取缓存的类；未命中返回 (nil, nil)。
	GetClass(ctx context.Context, fqn string) (*parser.ClassIR, error)
	// ClassFilePath 返回类全限定名对应的源文件路径（Redis 反查索引；
	// 未命中返回 ("", false)）。
	ClassFilePath(ctx context.Context, fqn string) (string, bool)
	// CallersOf 返回某方法的调用者集合（反向索引）。
	CallersOf(ctx context.Context, fqn string) ([]string, error)
	// CalleesOf 返回某方法直接调用的被调者集合。
	CalleesOf(ctx context.Context, fqn string) ([]string, error)
	// ClassesOfFile 返回文件包含的类 FQN 集合。
	ClassesOfFile(ctx context.Context, path string) ([]string, error)
}

// NewStore 根据驱动类型创建对应的存储实现。
//
// 注意：因 sqlitestore/pg 子包反向依赖本包（实现 Store 接口），本函数按设计
// 仅分发 "file"；SQLite/PG 等后端经 cmd/codeschema 的 build-tagged 统一分发
// 接线（见 store_dispatch.go），避免在 store 包内形成循环依赖。
func NewStore(driver string) Store {
	switch driver {
	case "file":
		return &FileStore{}
	default:
		return &FileStore{}
	}
}

// DeriveTagCategory 根据标签名推断其分类（layer/test/lang/risk/tech/biz）。
// 三后端（FileStore/SQLiteStore/PGStore）共用同一分类口径，避免语义分叉。
func DeriveTagCategory(tag string) string {
	switch tag {
	case "controller", "service", "dao", "domain", "infra", "repository", "handler", "middleware", "config":
		return "layer"
	case "unit", "integration", "e2e", "mock":
		return "test"
	case "go", "java", "python", "typescript", "javascript", "cpp", "rust", "kotlin", "scala", "ruby", "php":
		return "lang"
	case "legacy", "todo", "deprecated", "performance", "security":
		return "risk"
	case "cache", "mq", "retry", "transactional", "async", "schedule", "batch":
		return "tech"
	default:
		return "biz"
	}
}

// UniqueStrings 去重并保持首次出现顺序。
func UniqueStrings(in []string) []string {
	seen := make(map[string]bool, len(in))
	out := make([]string, 0, len(in))
	for _, t := range in {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}
