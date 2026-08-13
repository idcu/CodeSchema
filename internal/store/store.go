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
	"codeschema/internal/parser"
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

	// HealthCheck 返回存储层健康状态。
	HealthCheck(ctx context.Context) error
}

// FileRecord 对应 file 表的一行记录。
type FileRecord struct {
	ID               int64    `json:"id"`
	AbsolutePath     string   `json:"absolute_path"`
	ContentHash      string   `json:"content_hash,omitempty"`
	LineCount        int      `json:"line_count"`
	ByteSize         int64    `json:"byte_size"`
	ReferencedByFiles []string `json:"referenced_by_files,omitempty"`
	Language         string   `json:"language,omitempty"`
	ParseStatus      string   `json:"parse_status"`
}

// NewStore 根据驱动类型创建对应的存储实现。
// 当前仅支持 "file" 驱动。
func NewStore(driver string) Store {
	switch driver {
	case "file":
		return &FileStore{}
	default:
		return &FileStore{}
	}
}