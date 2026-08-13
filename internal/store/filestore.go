package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"codeschema/internal/parser"
)

// FileStore 基于 JSON 文件的轻量存储实现。
// 数据以 JSON 文件形式存储在指定目录下，适用于 P0 MVP 原型阶段。
type FileStore struct {
	mu       sync.RWMutex
	rootDir  string
	files    map[string]*FileRecord   // absolute_path -> FileRecord
	classes  map[int64][]ClassRecord  // fileID -> classes
	methods  map[int64][]MethodRecord // classID -> methods
	calls    map[int64][]CallRecord   // fileID -> calls
	nextID   int64
}

// ClassRecord 对应解析后的类信息。
type ClassRecord struct {
	ID       int64  `json:"id"`
	FileID   int64  `json:"file_id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Type     string `json:"type"`
	StartLine int   `json:"start_line"`
	StartCol  int   `json:"start_col"`
	EndLine   int   `json:"end_line"`
	EndCol    int   `json:"end_col"`
	Modifier  string `json:"modifier,omitempty"`
	Doc      string `json:"doc,omitempty"`
	Source   string `json:"source,omitempty"`
}

// MethodRecord 对应解析后的方法信息。
type MethodRecord struct {
	ID       int64  `json:"id"`
	ClassID  int64  `json:"class_id"`
	Name     string `json:"name"`
	FullName string `json:"full_name"`
	Signature string `json:"signature,omitempty"`
	ReturnType string `json:"return_type,omitempty"`
	StartLine int   `json:"start_line"`
	StartCol  int   `json:"start_col"`
	EndLine   int   `json:"end_line"`
	EndCol    int   `json:"end_col"`
	Doc      string `json:"doc,omitempty"`
	Source   string `json:"source,omitempty"`
}

// CallRecord 对应解析后的调用关系。
type CallRecord struct {
	CallerFQN  string `json:"caller_fqn"`
	CalleeFQN  string `json:"callee_fqn"`
	CallType   string `json:"call_type"`
	LineNumber int    `json:"line_number"`
	Source     string `json:"source,omitempty"`
}

// Open 初始化文件存储。
func (fs *FileStore) Open(ctx context.Context, dsn string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	fs.rootDir = dsn
	if fs.rootDir == "" {
		fs.rootDir = "./data"
	}

	if err := os.MkdirAll(fs.rootDir, 0755); err != nil {
		return fmt.Errorf("create data dir: %w", err)
	}

	fs.files = make(map[string]*FileRecord)
	fs.classes = make(map[int64][]ClassRecord)
	fs.methods = make(map[int64][]MethodRecord)
	fs.calls = make(map[int64][]CallRecord)
	fs.nextID = 1

	// 尝试从磁盘恢复数据
	fs.loadFromDisk()

	return nil
}

// Close 保存数据到磁盘。
func (fs *FileStore) Close() error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	return fs.saveToDisk()
}

// UpsertFile 插入或更新文件记录。
func (fs *FileStore) UpsertFile(ctx context.Context, filePath string, contentHash string, lineCount int, byteSize int64) (int64, error) {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	existing, ok := fs.files[filePath]
	if ok {
		existing.ContentHash = contentHash
		existing.LineCount = lineCount
		existing.ByteSize = byteSize
		existing.ParseStatus = "parse_ok"
		return existing.ID, nil
	}

	rec := &FileRecord{
		ID:          fs.nextID,
		AbsolutePath: filePath,
		ContentHash: contentHash,
		LineCount:   lineCount,
		ByteSize:    byteSize,
		ParseStatus: "parse_ok",
	}
	fs.nextID++
	fs.files[filePath] = rec
	return rec.ID, nil
}

// GetFileByPath 按路径查询文件。
func (fs *FileStore) GetFileByPath(ctx context.Context, path string) (*FileRecord, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	f, ok := fs.files[path]
	if !ok {
		return nil, nil
	}
	return f, nil
}

// GetFileByID 按 ID 查询文件。
func (fs *FileStore) GetFileByID(ctx context.Context, id int64) (*FileRecord, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	for _, f := range fs.files {
		if f.ID == id {
			return f, nil
		}
	}
	return nil, nil
}

// DeleteFile 删除文件及其级联数据。
func (fs *FileStore) DeleteFile(ctx context.Context, fileID int64) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	for path, f := range fs.files {
		if f.ID == fileID {
			delete(fs.files, path)
			delete(fs.classes, fileID)
			delete(fs.calls, fileID)
			// 清理关联的 methods
			for classID := range fs.methods {
				for _, c := range fs.classes[fileID] {
					if c.ID == classID {
						delete(fs.methods, classID)
					}
				}
			}
			return nil
		}
	}
	return nil
}

// UpsertClasses 批量更新类记录。
func (fs *FileStore) UpsertClasses(ctx context.Context, fileID int64, classes []parser.ClassIR) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	var records []ClassRecord
	for _, c := range classes {
		records = append(records, ClassRecord{
			ID:       fs.nextID,
			FileID:   fileID,
			Name:     c.Name,
			FullName: c.FullName,
			Type:     c.Type,
			StartLine: c.StartLine,
			StartCol:  c.StartCol,
			EndLine:   c.EndLine,
			EndCol:    c.EndCol,
			Modifier:  c.Modifier,
			Doc:      c.Doc,
		})
		fs.nextID++
	}
	fs.classes[fileID] = records
	return nil
}

// UpsertMethods 批量更新方法记录。
func (fs *FileStore) UpsertMethods(ctx context.Context, classID int64, methods []parser.MethodIR) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	var records []MethodRecord
	for _, m := range methods {
		records = append(records, MethodRecord{
			ID:       fs.nextID,
			ClassID:  classID,
			Name:     m.Name,
			FullName: m.ClassFQN + "." + m.Name,
			Signature: m.Signature,
			ReturnType: m.ReturnType,
			StartLine: m.StartLine,
			StartCol:  m.StartCol,
			EndLine:   m.EndLine,
			EndCol:    m.EndCol,
			Doc:      m.Doc,
		})
		fs.nextID++
	}
	fs.methods[classID] = records
	return nil
}

// UpsertCalls 批量更新调用关系。
func (fs *FileStore) UpsertCalls(ctx context.Context, fileID int64, calls []parser.CallIR) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	var records []CallRecord
	for _, c := range calls {
		records = append(records, CallRecord{
			CallerFQN:  c.CallerFQN,
			CalleeFQN:  c.CalleeFQN,
			CallType:   c.CallType,
			LineNumber: c.LineNumber,
		})
	}
	fs.calls[fileID] = records
	return nil
}

// UpsertIR 对一个文件的 IR 执行增量入库。
func (fs *FileStore) UpsertIR(ctx context.Context, ir *parser.IRDocument) error {
	fileID, err := fs.UpsertFile(ctx, ir.FilePath, ir.FileHash, ir.LineCount, ir.ByteSize)
	if err != nil {
		return fmt.Errorf("upsert file: %w", err)
	}
	if err := fs.UpsertClasses(ctx, fileID, ir.Classes); err != nil {
		return fmt.Errorf("upsert classes: %w", err)
	}
	if err := fs.UpsertCalls(ctx, fileID, ir.Calls); err != nil {
		return fmt.Errorf("upsert calls: %w", err)
	}
	return nil
}

// HealthCheck 返回存储层健康状态。
func (fs *FileStore) HealthCheck(ctx context.Context) error {
	fs.mu.RLock()
	defer fs.mu.RUnlock()
	if fs.rootDir == "" {
		return fmt.Errorf("store not initialized")
	}
	return nil
}

// GetAllFiles 返回所有文件记录。
func (fs *FileStore) GetAllFiles(ctx context.Context) ([]*FileRecord, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	result := make([]*FileRecord, 0, len(fs.files))
	for _, f := range fs.files {
		result = append(result, f)
	}
	return result, nil
}

// GetClassesByFileID 按文件 ID 查询类记录。
func (fs *FileStore) GetClassesByFileID(ctx context.Context, fileID int64) ([]ClassRecord, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	classes, ok := fs.classes[fileID]
	if !ok {
		return []ClassRecord{}, nil
	}
	return classes, nil
}

// GetMethodsByClassID 按类 ID 查询方法记录。
func (fs *FileStore) GetMethodsByClassID(ctx context.Context, classID int64) ([]MethodRecord, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	methods, ok := fs.methods[classID]
	if !ok {
		return []MethodRecord{}, nil
	}
	return methods, nil
}

// GetCallsByFileID 按文件 ID 查询调用关系。
func (fs *FileStore) GetCallsByFileID(ctx context.Context, fileID int64) ([]CallRecord, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	calls, ok := fs.calls[fileID]
	if !ok {
		return []CallRecord{}, nil
	}
	return calls, nil
}

// saveToDisk 将内存数据持久化到磁盘。
func (fs *FileStore) saveToDisk() error {
	data := struct {
		Files   map[string]*FileRecord   `json:"files"`
		Classes map[int64][]ClassRecord  `json:"classes"`
		Methods map[int64][]MethodRecord `json:"methods"`
		Calls   map[int64][]CallRecord   `json:"calls"`
		NextID  int64                    `json:"next_id"`
		Updated string                   `json:"updated"`
	}{
		Files:   fs.files,
		Classes: fs.classes,
		Methods: fs.methods,
		Calls:   fs.calls,
		NextID:  fs.nextID,
		Updated: time.Now().UTC().Format(time.RFC3339),
	}

	b, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return fmt.Errorf("marshal store: %w", err)
	}

	tmpPath := filepath.Join(fs.rootDir, "store.json.tmp")
	finalPath := filepath.Join(fs.rootDir, "store.json")

	if err := os.WriteFile(tmpPath, b, 0644); err != nil {
		return fmt.Errorf("write temp store: %w", err)
	}
	return os.Rename(tmpPath, finalPath)
}

// loadFromDisk 从磁盘加载数据到内存。
func (fs *FileStore) loadFromDisk() error {
	path := filepath.Join(fs.rootDir, "store.json")
	data, err := os.ReadFile(path)
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("read store: %w", err)
	}

	var loaded struct {
		Files   map[string]*FileRecord   `json:"files"`
		Classes map[int64][]ClassRecord  `json:"classes"`
		Methods map[int64][]MethodRecord `json:"methods"`
		Calls   map[int64][]CallRecord   `json:"calls"`
		NextID  int64                    `json:"next_id"`
	}
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("unmarshal store: %w", err)
	}

	fs.files = loaded.Files
	fs.classes = loaded.Classes
	fs.methods = loaded.Methods
	fs.calls = loaded.Calls
	fs.nextID = loaded.NextID

	if fs.files == nil {
		fs.files = make(map[string]*FileRecord)
	}
	if fs.classes == nil {
		fs.classes = make(map[int64][]ClassRecord)
	}
	if fs.methods == nil {
		fs.methods = make(map[int64][]MethodRecord)
	}
	if fs.calls == nil {
		fs.calls = make(map[int64][]CallRecord)
	}
	if fs.nextID == 0 {
		fs.nextID = 1
	}

	return nil
}