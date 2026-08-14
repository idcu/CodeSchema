package store

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/idcu/codeschema/internal/parser"
)

// FileStore 基于 JSON 文件的轻量存储实现。
// 数据以 JSON 文件形式存储在指定目录下，适用于 P0 MVP 原型阶段。
type FileStore struct {
	mu            sync.RWMutex
	rootDir       string
	files         map[string]*FileRecord   // absolute_path -> FileRecord
	classes       map[int64][]ClassRecord  // fileID -> classes
	methods       map[int64][]MethodRecord // classID -> methods
	calls         map[int64][]CallRecord   // fileID -> calls
	classTags     map[int64][]string       // classID -> tags
	methodTags    map[int64][]string       // methodID -> tags
	tagCategories map[string]string        // tag name -> category
	nextID        int64
}

// ClassRecord 对应解析后的类信息。
type ClassRecord struct {
	ID         int64    `json:"id"`
	FileID     int64    `json:"file_id"`
	Name       string   `json:"name"`
	FullName   string   `json:"full_name"`
	Type       string   `json:"type"`
	ParentFQNs []string `json:"parent_fqns,omitempty"`
	StartLine  int      `json:"start_line"`
	StartCol   int      `json:"start_col"`
	EndLine    int      `json:"end_line"`
	EndCol     int      `json:"end_col"`
	Modifier   string   `json:"modifier,omitempty"`
	Doc        string   `json:"doc,omitempty"`
	Source     string   `json:"source,omitempty"`
}

// MethodRecord 对应解析后的方法信息。
type MethodRecord struct {
	ID         int64  `json:"id"`
	ClassID    int64  `json:"class_id"`
	Name       string `json:"name"`
	FullName   string `json:"full_name"`
	Signature  string `json:"signature,omitempty"`
	ReturnType string `json:"return_type,omitempty"`
	StartLine  int    `json:"start_line"`
	StartCol   int    `json:"start_col"`
	EndLine    int    `json:"end_line"`
	EndCol     int    `json:"end_col"`
	Doc        string `json:"doc,omitempty"`
	Source     string `json:"source,omitempty"`
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
	fs.classTags = make(map[int64][]string)
	fs.methodTags = make(map[int64][]string)
	fs.tagCategories = make(map[string]string)
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
		ID:           fs.nextID,
		AbsolutePath: filePath,
		ContentHash:  contentHash,
		LineCount:    lineCount,
		ByteSize:     byteSize,
		ParseStatus:  "parse_ok",
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
			ID:         fs.nextID,
			FileID:     fileID,
			Name:       c.Name,
			FullName:   c.FullName,
			Type:       c.Type,
			ParentFQNs: c.ParentFQNs,
			StartLine:  c.StartLine,
			StartCol:   c.StartCol,
			EndLine:    c.EndLine,
			EndCol:     c.EndCol,
			Modifier:   c.Modifier,
			Doc:        c.Doc,
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
			ID:         fs.nextID,
			ClassID:    classID,
			Name:       m.Name,
			FullName:   m.ClassFQN + "." + m.Name,
			Signature:  m.Signature,
			ReturnType: m.ReturnType,
			StartLine:  m.StartLine,
			StartCol:   m.StartCol,
			EndLine:    m.EndLine,
			EndCol:     m.EndCol,
			Doc:        m.Doc,
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

	// 为每个类 Upsert 对应的方法：按 ClassFQN 匹配
	fs.mu.RLock()
	classRecords := fs.classes[fileID]
	fs.mu.RUnlock()

	if len(ir.Methods) > 0 && len(classRecords) > 0 {
		// 建立 FullName → ClassID 的映射
		classMap := make(map[string]int64, len(classRecords))
		for _, cr := range classRecords {
			classMap[cr.FullName] = cr.ID
		}

		// 按 ClassFQN 分组方法
		type methodGroup struct {
			classID int64
			methods []parser.MethodIR
		}
		groupMap := make(map[int64]*methodGroup)
		for _, m := range ir.Methods {
			cid, ok := classMap[m.ClassFQN]
			if !ok {
				// 方法没有匹配的类（如文件级函数），关联到文件级别
				continue
			}
			if _, exists := groupMap[cid]; !exists {
				groupMap[cid] = &methodGroup{classID: cid}
			}
			groupMap[cid].methods = append(groupMap[cid].methods, m)
		}

		for _, g := range groupMap {
			if err := fs.UpsertMethods(ctx, g.classID, g.methods); err != nil {
				return fmt.Errorf("upsert methods for class %d: %w", g.classID, err)
			}
		}
	}

	if err := fs.UpsertCalls(ctx, fileID, ir.Calls); err != nil {
		return fmt.Errorf("upsert calls: %w", err)
	}

	// 保存文件级 imports 元数据
	fs.mu.Lock()
	if f, ok := fs.files[ir.FilePath]; ok && len(ir.Imports) > 0 {
		f.Imports = ir.Imports
	}
	fs.mu.Unlock()

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

// UpsertTags 设置类标签（全量替换）。
func (fs *FileStore) UpsertTags(ctx context.Context, classID int64, tags []string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if len(tags) == 0 {
		delete(fs.classTags, classID)
		return nil
	}

	// 去重
	seen := make(map[string]bool)
	var unique []string
	for _, t := range tags {
		if !seen[t] {
			seen[t] = true
			unique = append(unique, t)
		}
	}
	fs.classTags[classID] = unique

	// 更新标签分类索引
	for _, t := range unique {
		if _, ok := fs.tagCategories[t]; !ok {
			fs.tagCategories[t] = deriveTagCategory(t)
		}
	}
	return nil
}

// UpsertMethodTags 设置方法标签（全量替换）。
func (fs *FileStore) UpsertMethodTags(ctx context.Context, methodID int64, tags []string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	if len(tags) == 0 {
		delete(fs.methodTags, methodID)
		return nil
	}

	seen := make(map[string]bool)
	var unique []string
	for _, t := range tags {
		if !seen[t] {
			seen[t] = true
			unique = append(unique, t)
		}
	}
	fs.methodTags[methodID] = unique

	for _, t := range unique {
		if _, ok := fs.tagCategories[t]; !ok {
			fs.tagCategories[t] = deriveTagCategory(t)
		}
	}
	return nil
}

// GetTagsByClassID 获取类的标签列表。
func (fs *FileStore) GetTagsByClassID(ctx context.Context, classID int64) ([]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	tags, ok := fs.classTags[classID]
	if !ok {
		return []string{}, nil
	}
	return tags, nil
}

// GetTagsByMethodID 获取方法的标签列表。
func (fs *FileStore) GetTagsByMethodID(ctx context.Context, methodID int64) ([]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	tags, ok := fs.methodTags[methodID]
	if !ok {
		return []string{}, nil
	}
	return tags, nil
}

// UpdateClassDoc 更新类文档注释（可选接口 docUpdater：供 AI 增强层写回补全文档）。
func (fs *FileStore) UpdateClassDoc(ctx context.Context, classID int64, doc string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	for fileID, classes := range fs.classes {
		for i := range classes {
			if classes[i].ID == classID {
				classes[i].Doc = doc
				fs.classes[fileID] = classes
				return nil
			}
		}
	}
	return nil
}

// UpdateMethodDoc 更新方法文档注释（可选接口 docUpdater：供 AI 增强层写回补全文档）。
func (fs *FileStore) UpdateMethodDoc(ctx context.Context, methodID int64, doc string) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()

	for classID, methods := range fs.methods {
		for i := range methods {
			if methods[i].ID == methodID {
				methods[i].Doc = doc
				fs.methods[classID] = methods
				return nil
			}
		}
	}
	return nil
}

// SearchByTag 按标签搜索类和方法的 ID 列表。
func (fs *FileStore) SearchByTag(ctx context.Context, tag string) ([]int64, []int64, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	var classIDs []int64
	for cid, tags := range fs.classTags {
		for _, t := range tags {
			if t == tag {
				classIDs = append(classIDs, cid)
				break
			}
		}
	}

	var methodIDs []int64
	for mid, tags := range fs.methodTags {
		for _, t := range tags {
			if t == tag {
				methodIDs = append(methodIDs, mid)
				break
			}
		}
	}

	return classIDs, methodIDs, nil
}

// GetAllTagsWithCategories 返回所有已知标签及其分类。
func (fs *FileStore) GetAllTagsWithCategories(ctx context.Context) (map[string]string, error) {
	fs.mu.RLock()
	defer fs.mu.RUnlock()

	result := make(map[string]string, len(fs.tagCategories))
	for k, v := range fs.tagCategories {
		result[k] = v
	}
	return result, nil
}

// deriveTagCategory 根据标签名推断其分类。
func deriveTagCategory(tag string) string {
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

// saveToDisk 将内存数据持久化到磁盘。
func (fs *FileStore) saveToDisk() error {
	data := struct {
		Files      map[string]*FileRecord   `json:"files"`
		Classes    map[int64][]ClassRecord  `json:"classes"`
		Methods    map[int64][]MethodRecord `json:"methods"`
		Calls      map[int64][]CallRecord   `json:"calls"`
		ClassTags  map[int64][]string       `json:"class_tags,omitempty"`
		MethodTags map[int64][]string       `json:"method_tags,omitempty"`
		TagCats    map[string]string        `json:"tag_categories,omitempty"`
		NextID     int64                    `json:"next_id"`
		Updated    string                   `json:"updated"`
	}{
		Files:      fs.files,
		Classes:    fs.classes,
		Methods:    fs.methods,
		Calls:      fs.calls,
		ClassTags:  fs.classTags,
		MethodTags: fs.methodTags,
		TagCats:    fs.tagCategories,
		NextID:     fs.nextID,
		Updated:    time.Now().UTC().Format(time.RFC3339),
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
		Files      map[string]*FileRecord   `json:"files"`
		Classes    map[int64][]ClassRecord  `json:"classes"`
		Methods    map[int64][]MethodRecord `json:"methods"`
		Calls      map[int64][]CallRecord   `json:"calls"`
		ClassTags  map[int64][]string       `json:"class_tags,omitempty"`
		MethodTags map[int64][]string       `json:"method_tags,omitempty"`
		TagCats    map[string]string        `json:"tag_categories,omitempty"`
		NextID     int64                    `json:"next_id"`
	}
	if err := json.Unmarshal(data, &loaded); err != nil {
		return fmt.Errorf("unmarshal store: %w", err)
	}

	fs.files = loaded.Files
	fs.classes = loaded.Classes
	fs.methods = loaded.Methods
	fs.calls = loaded.Calls
	fs.classTags = loaded.ClassTags
	fs.methodTags = loaded.MethodTags
	fs.tagCategories = loaded.TagCats
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
	if fs.classTags == nil {
		fs.classTags = make(map[int64][]string)
	}
	if fs.methodTags == nil {
		fs.methodTags = make(map[int64][]string)
	}
	if fs.tagCategories == nil {
		fs.tagCategories = make(map[string]string)
	}
	if fs.nextID == 0 {
		fs.nextID = 1
	}

	return nil
}

// BulkUpsert 批量入库（持有外层锁，避免逐文件重复加锁）。语义同逐文件 UpsertIR：
// 内存填充 file/class/method/call 映射，Close 时一次性全量落盘（O(n)）。
func (fs *FileStore) BulkUpsert(ctx context.Context, irs []*parser.IRDocument) error {
	fs.mu.Lock()
	defer fs.mu.Unlock()
	for _, ir := range irs {
		fileID, err := fs.upsertFileInMemory(ir.FilePath, ir.FileHash, ir.LineCount, ir.ByteSize)
		if err != nil {
			return fmt.Errorf("upsert file: %w", err)
		}
		var classRecords []ClassRecord
		classMap := make(map[string]int64, len(ir.Classes))
		for _, c := range ir.Classes {
			id := fs.nextID
			fs.nextID++
			classRecords = append(classRecords, ClassRecord{
				ID: id, FileID: fileID, Name: c.Name, FullName: c.FullName, Type: c.Type,
				ParentFQNs: c.ParentFQNs, StartLine: c.StartLine, StartCol: c.StartCol,
				EndLine: c.EndLine, EndCol: c.EndCol, Modifier: c.Modifier, Doc: c.Doc,
			})
			classMap[c.FullName] = id
		}
		fs.classes[fileID] = classRecords

		if len(ir.Methods) > 0 {
			var methodRecords []MethodRecord
			for _, m := range ir.Methods {
				cid, ok := classMap[m.ClassFQN]
				if !ok {
					continue
				}
				id := fs.nextID
				fs.nextID++
				methodRecords = append(methodRecords, MethodRecord{
					ID: id, ClassID: cid, Name: m.Name, FullName: m.ClassFQN + "." + m.Name,
					Signature: m.Signature, ReturnType: m.ReturnType,
					StartLine: m.StartLine, StartCol: m.StartCol, EndLine: m.EndLine, EndCol: m.EndCol, Doc: m.Doc,
				})
			}
			byClass := make(map[int64][]MethodRecord, len(methodRecords))
			for _, m := range methodRecords {
				byClass[m.ClassID] = append(byClass[m.ClassID], m)
			}
			for cid, recs := range byClass {
				fs.methods[cid] = recs
			}
		}

		if len(ir.Calls) > 0 {
			var callRecords []CallRecord
			for _, c := range ir.Calls {
				callRecords = append(callRecords, CallRecord{
					CallerFQN: c.CallerFQN, CalleeFQN: c.CalleeFQN, CallType: c.CallType, LineNumber: c.LineNumber,
				})
			}
			fs.calls[fileID] = callRecords
		}

		if f, ok := fs.files[ir.FilePath]; ok && len(ir.Imports) > 0 {
			f.Imports = ir.Imports
		}
	}
	return nil
}

// upsertFileInMemory 在已持锁前提下插入/更新文件记录（不重复加锁）。
func (fs *FileStore) upsertFileInMemory(filePath, contentHash string, lineCount int, byteSize int64) (int64, error) {
	if f, ok := fs.files[filePath]; ok {
		f.ContentHash = contentHash
		f.LineCount = lineCount
		f.ByteSize = byteSize
		f.ParseStatus = "parse_ok"
		return f.ID, nil
	}
	id := fs.nextID
	fs.nextID++
	fs.files[filePath] = &FileRecord{
		ID: id, AbsolutePath: filePath, ContentHash: contentHash,
		LineCount: lineCount, ByteSize: byteSize, ParseStatus: "parse_ok",
	}
	return id, nil
}
