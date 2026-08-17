// Package sqlite 提供 CodeSchema 存储层的 SQLite 实现（权威存储）。
//
// 基于 modernc.org/sqlite（纯 Go，免 CGO），实现 store.Store 接口。
// 与 FileStore 语义保持一致（UpsertClasses/Methods/Calls 为「按归属全量替换」），
// 并额外提供跨会话一致性与并发查询能力，消除原 JSON 文件存储的规模化短板。
package sqlite

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"

	_ "modernc.org/sqlite"

	"github.com/idcu/codeschema/internal/fsperm"
	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// SQLiteStore 是基于 SQLite 的存储实现。
type SQLiteStore struct {
	mu sync.RWMutex
	db *sql.DB
}

// schema 初始化 SQL（幂等）。
const schema = `
CREATE TABLE IF NOT EXISTS file (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  absolute_path TEXT UNIQUE,
  content_hash TEXT,
  line_count INTEGER DEFAULT 0,
  byte_size INTEGER DEFAULT 0,
  referenced_by_files TEXT DEFAULT '[]',
  imports TEXT DEFAULT '[]',
  language TEXT DEFAULT '',
  parse_status TEXT DEFAULT 'parse_ok'
);
CREATE TABLE IF NOT EXISTS class (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  file_id INTEGER,
  name TEXT,
  full_name TEXT,
  type TEXT,
  parent_fqns TEXT DEFAULT '[]',
  start_line INTEGER, start_col INTEGER, end_line INTEGER, end_col INTEGER,
  modifier TEXT DEFAULT '',
  doc_comment TEXT DEFAULT '',
  source TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_class_file ON class(file_id);
CREATE INDEX IF NOT EXISTS idx_class_full ON class(full_name);
CREATE TABLE IF NOT EXISTS method (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  class_id INTEGER,
  name TEXT,
  full_name TEXT,
  signature TEXT,
  return_type TEXT,
  start_line INTEGER, start_col INTEGER, end_line INTEGER, end_col INTEGER,
  modifier TEXT DEFAULT '',
  doc_comment TEXT DEFAULT '',
  source TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_method_class ON method(class_id);
CREATE TABLE IF NOT EXISTS call (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  file_id INTEGER,
  caller_fqn TEXT,
  callee_fqn TEXT,
  call_type TEXT,
  line_number INTEGER,
  source TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_call_file ON call(file_id);
CREATE INDEX IF NOT EXISTS idx_call_caller ON call(caller_fqn);
CREATE INDEX IF NOT EXISTS idx_call_callee ON call(callee_fqn);
CREATE TABLE IF NOT EXISTS tag (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT UNIQUE,
  category TEXT
);
CREATE TABLE IF NOT EXISTS class_tag (
  class_id INTEGER,
  tag_id INTEGER,
  PRIMARY KEY(class_id, tag_id)
);
CREATE TABLE IF NOT EXISTS method_tag (
  method_id INTEGER,
  tag_id INTEGER,
  PRIMARY KEY(method_id, tag_id)
);
`

// NewSQLiteStore 创建 SQLite 存储实例。
func NewSQLiteStore() *SQLiteStore {
	return &SQLiteStore{}
}

// Open 打开（或创建）SQLite 数据库并初始化表结构。
// dsn 可为 .db 文件路径，或目录（将拼接 codeschema.db）。
func (s *SQLiteStore) Open(ctx context.Context, dsn string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	if dsn == "" {
		dsn = "./data"
	}
	path := dsn
	if filepath.Ext(path) != ".db" {
		if err := ensureDir(path); err != nil {
			return err
		}
		path = filepath.Join(path, "codeschema.db")
	}

	db, err := sql.Open("sqlite", path)
	if err != nil {
		return fmt.Errorf("open sqlite %s: %w", path, err)
	}
	// 提升并发与写入健壮性。WAL + synchronous=NORMAL：仅 WAL 检查点时 fsync，
	// 而非每事务 fsync，批量写入（索引大仓）吞吐显著提升；电源故障最多丢最近一次
	// 检查点内的提交，对“可重建的索引缓存”是可接受权衡。
	if _, err := db.Exec("PRAGMA journal_mode=WAL; PRAGMA synchronous=NORMAL; PRAGMA busy_timeout=5000;"); err != nil {
		_ = db.Close()
		return fmt.Errorf("set pragma: %w", err)
	}
	if _, err := db.Exec(schema); err != nil {
		_ = db.Close()
		return fmt.Errorf("init schema: %w", err)
	}
	s.db = db
	return nil
}

// Close 关闭数据库连接。
func (s *SQLiteStore) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.db == nil {
		return nil
	}
	return s.db.Close()
}

// HealthCheck 返回存储层健康状态。
func (s *SQLiteStore) HealthCheck(ctx context.Context) error {
	s.mu.RLock()
	defer s.mu.RUnlock()
	if s.db == nil {
		return fmt.Errorf("store not initialized")
	}
	var n int
	return s.db.QueryRow("SELECT 1").Scan(&n)
}

// UpsertFile 插入或更新文件记录，返回文件 ID。
func (s *SQLiteStore) UpsertFile(ctx context.Context, filePath, contentHash string, lineCount int, byteSize int64) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var id int64
	err := s.db.QueryRow(`
		INSERT INTO file (absolute_path, content_hash, line_count, byte_size, parse_status)
		VALUES (?, ?, ?, ?, 'parse_ok')
		ON CONFLICT(absolute_path) DO UPDATE SET
			content_hash=excluded.content_hash,
			line_count=excluded.line_count,
			byte_size=excluded.byte_size,
			parse_status='parse_ok'
		RETURNING id`,
		filePath, contentHash, lineCount, byteSize,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("upsert file: %w", err)
	}
	return id, nil
}

// MarkParseSkipped 记录一个被旁路的文件（超限未解析），parse_status 置为 parse_skipped。
func (s *SQLiteStore) MarkParseSkipped(ctx context.Context, filePath string, byteSize int64, lineCount int) (int64, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	var id int64
	err := s.db.QueryRow(`
		INSERT INTO file (absolute_path, content_hash, line_count, byte_size, parse_status)
		VALUES (?, '', ?, ?, 'parse_skipped')
		ON CONFLICT(absolute_path) DO UPDATE SET
			content_hash='',
			line_count=excluded.line_count,
			byte_size=excluded.byte_size,
			parse_status='parse_skipped'
		RETURNING id`,
		filePath, lineCount, byteSize,
	).Scan(&id)
	if err != nil {
		return 0, fmt.Errorf("mark file skipped: %w", err)
	}
	return id, nil
}

// GetFileByPath 按路径查询文件。
func (s *SQLiteStore) GetFileByPath(ctx context.Context, path string) (*store.FileRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.queryFile("WHERE absolute_path = ?", path)
}

// GetFileByID 按 ID 查询文件。
func (s *SQLiteStore) GetFileByID(ctx context.Context, id int64) (*store.FileRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.queryFile("WHERE id = ?", id)
}

func (s *SQLiteStore) queryFile(where string, arg any) (*store.FileRecord, error) {
	row := s.db.QueryRow(`
		SELECT id, absolute_path, content_hash, line_count, byte_size,
		       referenced_by_files, imports, language, parse_status
		FROM file `+where, arg)
	var f store.FileRecord
	var refBy, imps string
	if err := row.Scan(&f.ID, &f.AbsolutePath, &f.ContentHash, &f.LineCount, &f.ByteSize,
		&refBy, &imps, &f.Language, &f.ParseStatus); err != nil {
		if err == sql.ErrNoRows {
			return nil, nil
		}
		return nil, fmt.Errorf("scan file: %w", err)
	}
	f.ReferencedByFiles = jsonStringSlice(refBy)
	f.Imports = jsonStringSlice(imps)
	return &f, nil
}

// DeleteFile 删除文件及其级联数据。
func (s *SQLiteStore) DeleteFile(ctx context.Context, fileID int64) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM class_tag WHERE class_id IN (SELECT id FROM class WHERE file_id = ?)", fileID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM method_tag WHERE method_id IN (SELECT m.id FROM method m JOIN class c ON m.class_id = c.id WHERE c.file_id = ?)", fileID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM method WHERE class_id IN (SELECT id FROM class WHERE file_id = ?)", fileID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM call WHERE file_id = ?", fileID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM class WHERE file_id = ?", fileID); err != nil {
		return err
	}
	if _, err := tx.Exec("DELETE FROM file WHERE id = ?", fileID); err != nil {
		return err
	}
	return tx.Commit()
}

// UpsertClasses 按文件全量替换类记录。
func (s *SQLiteStore) UpsertClasses(ctx context.Context, fileID int64, classes []parser.ClassIR) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM class WHERE file_id = ?", fileID); err != nil {
		return err
	}
	for _, c := range classes {
		pf := toJSON(c.ParentFQNs)
		if _, err := tx.Exec(`
		INSERT INTO class (file_id, name, full_name, type, parent_fqns,
			start_line, start_col, end_line, end_col, modifier, doc_comment)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fileID, c.Name, c.FullName, c.Type, pf,
			c.StartLine, c.StartCol, c.EndLine, c.EndCol, c.Modifier, c.Doc); err != nil {
			return fmt.Errorf("insert class %s: %w", c.FullName, err)
		}
	}
	return tx.Commit()
}

// UpsertMethods 按类全量替换方法记录。
func (s *SQLiteStore) UpsertMethods(ctx context.Context, classID int64, methods []parser.MethodIR) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM method WHERE class_id = ?", classID); err != nil {
		return err
	}
	for _, m := range methods {
		if _, err := tx.Exec(`
		INSERT INTO method (class_id, name, full_name, signature, return_type,
			start_line, start_col, end_line, end_col, doc_comment)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			classID, m.Name, m.ClassFQN+"."+m.Name, m.Signature, m.ReturnType,
			m.StartLine, m.StartCol, m.EndLine, m.EndCol, m.Doc); err != nil {
			return fmt.Errorf("insert method %s: %w", m.Name, err)
		}
	}
	return tx.Commit()
}

// UpsertCalls 按文件全量替换调用关系。
func (s *SQLiteStore) UpsertCalls(ctx context.Context, fileID int64, calls []parser.CallIR) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec("DELETE FROM call WHERE file_id = ?", fileID); err != nil {
		return err
	}
	for _, c := range calls {
		if _, err := tx.Exec(`
		INSERT INTO call (file_id, caller_fqn, callee_fqn, call_type, line_number)
		VALUES (?, ?, ?, ?, ?)`,
			fileID, c.CallerFQN, c.CalleeFQN, c.CallType, c.LineNumber); err != nil {
			return fmt.Errorf("insert call: %w", err)
		}
	}
	return tx.Commit()
}

// UpsertIR 对一个文件的 IR 执行增量入库（语义对齐 FileStore）。
func (s *SQLiteStore) UpsertIR(ctx context.Context, ir *parser.IRDocument) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	fileID, err := s.upsertFileLocked(ir.FilePath, ir.FileHash, ir.LineCount, ir.ByteSize)
	if err != nil {
		return fmt.Errorf("upsert file: %w", err)
	}
	if err := s.upsertClassesLocked(fileID, ir.Classes); err != nil {
		return fmt.Errorf("upsert classes: %w", err)
	}

	// 建立 FullName -> classID 映射（从刚写入的数据查询）
	classMap, err := s.classIDMapLocked(fileID)
	if err != nil {
		return err
	}

	// 按 ClassFQN 分组方法
	type group struct {
		classID int64
		methods []parser.MethodIR
	}
	gm := make(map[int64]*group)
	for _, m := range ir.Methods {
		cid, ok := classMap[m.ClassFQN]
		if !ok {
			continue
		}
		if _, exists := gm[cid]; !exists {
			gm[cid] = &group{classID: cid}
		}
		gm[cid].methods = append(gm[cid].methods, m)
	}
	for _, g := range gm {
		if err := s.upsertMethodsLocked(g.classID, g.methods); err != nil {
			return fmt.Errorf("upsert methods for class %d: %w", g.classID, err)
		}
	}

	if err := s.upsertCallsLocked(fileID, ir.Calls); err != nil {
		return fmt.Errorf("upsert calls: %w", err)
	}

	// 保存文件级 imports 元数据
	if len(ir.Imports) > 0 {
		if _, err := s.db.Exec("UPDATE file SET imports = ? WHERE id = ?", toJSON(ir.Imports), fileID); err != nil {
			return fmt.Errorf("update imports: %w", err)
		}
	}
	if ir.Language != "" {
		if _, err := s.db.Exec("UPDATE file SET language = ? WHERE id = ?", ir.Language, fileID); err != nil {
			return fmt.Errorf("update language: %w", err)
		}
	}
	return nil
}

// GetAllFiles 返回所有文件记录。
func (s *SQLiteStore) GetAllFiles(ctx context.Context) ([]*store.FileRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, absolute_path, content_hash, line_count, byte_size,
		       referenced_by_files, imports, language, parse_status
		FROM file ORDER BY id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanFiles(rows)
}

// GetClassesByFileID 按文件 ID 查询类记录。
func (s *SQLiteStore) GetClassesByFileID(ctx context.Context, fileID int64) ([]store.ClassRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, file_id, name, full_name, type, parent_fqns,
		       start_line, start_col, end_line, end_col, modifier, doc_comment, source
		FROM class WHERE file_id = ? ORDER BY id`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanClasses(rows)
}

// GetMethodsByClassID 按类 ID 查询方法记录。
func (s *SQLiteStore) GetMethodsByClassID(ctx context.Context, classID int64) ([]store.MethodRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT id, class_id, name, full_name, signature, return_type,
		       start_line, start_col, end_line, end_col, doc_comment, source
		FROM method WHERE class_id = ? ORDER BY id`, classID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	return scanMethods(rows)
}

// GetCallsByFileID 按文件 ID 查询调用关系。
func (s *SQLiteStore) GetCallsByFileID(ctx context.Context, fileID int64) ([]store.CallRecord, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query(`
		SELECT caller_fqn, callee_fqn, call_type, line_number, source
		FROM call WHERE file_id = ? ORDER BY id`, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.CallRecord
	for rows.Next() {
		var c store.CallRecord
		if err := rows.Scan(&c.CallerFQN, &c.CalleeFQN, &c.CallType, &c.LineNumber, &c.Source); err != nil {
			return nil, err
		}
		out = append(out, c)
	}
	if out == nil {
		out = []store.CallRecord{}
	}
	return out, rows.Err()
}

// UpsertTags 设置类标签（全量替换）。
func (s *SQLiteStore) UpsertTags(ctx context.Context, classID int64, tags []string) error {
	return s.upsertTagsLocked(ctx, "class", classID, tags)
}

// UpsertMethodTags 设置方法标签（全量替换）。
func (s *SQLiteStore) UpsertMethodTags(ctx context.Context, methodID int64, tags []string) error {
	return s.upsertTagsLocked(ctx, "method", methodID, tags)
}

func (s *SQLiteStore) upsertTagsLocked(ctx context.Context, kind string, ownerID int64, tags []string) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	linkTable := "class_tag"
	idCol := "class_id"
	if kind == "method" {
		linkTable = "method_tag"
		idCol = "method_id"
	}

	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()

	if _, err := tx.Exec(fmt.Sprintf("DELETE FROM %s WHERE %s = ?", linkTable, idCol), ownerID); err != nil {
		return err
	}
	if len(tags) == 0 {
		return tx.Commit()
	}
	for _, t := range dedupe(tags) {
		var tagID int64
		if err := tx.QueryRow(`
			INSERT INTO tag (name, category) VALUES (?, ?)
			ON CONFLICT(name) DO UPDATE SET category=excluded.category
			RETURNING id`, t, deriveTagCategory(t)).Scan(&tagID); err != nil {
			return fmt.Errorf("upsert tag %s: %w", t, err)
		}
		if _, err := tx.Exec(fmt.Sprintf("INSERT OR IGNORE INTO %s (%s, tag_id) VALUES (?, ?)", linkTable, idCol), ownerID, tagID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// GetTagsByClassID 获取类的标签列表。
func (s *SQLiteStore) GetTagsByClassID(ctx context.Context, classID int64) ([]string, error) {
	return s.getTags(ctx, "class", classID)
}

// GetTagsByMethodID 获取方法的标签列表。
func (s *SQLiteStore) GetTagsByMethodID(ctx context.Context, methodID int64) ([]string, error) {
	return s.getTags(ctx, "method", methodID)
}

func (s *SQLiteStore) getTags(ctx context.Context, kind string, ownerID int64) ([]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	linkTable := "class_tag"
	idCol := "class_id"
	if kind == "method" {
		linkTable = "method_tag"
		idCol = "method_id"
	}
	rows, err := s.db.Query(fmt.Sprintf(`
		SELECT t.name FROM tag t JOIN %s lt ON t.id = lt.tag_id
		WHERE lt.%s = ? ORDER BY t.name`, linkTable, idCol), ownerID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var n string
		if err := rows.Scan(&n); err != nil {
			return nil, err
		}
		out = append(out, n)
	}
	if out == nil {
		out = []string{}
	}
	return out, rows.Err()
}

// SearchByTag 按单个标签搜索类和方法的 ID 列表（兼容入口，委托 SearchByTags）。
func (s *SQLiteStore) SearchByTag(ctx context.Context, tag string) ([]int64, []int64, error) {
	return s.SearchByTags(ctx, []string{tag})
}

// SearchByTags 按多个标签（AND）搜索类和方法的 ID 列表。
// 返回同时拥有所有指定标签的类和方法（交集）。
func (s *SQLiteStore) SearchByTags(ctx context.Context, tags []string) ([]int64, []int64, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	var classIDs, methodIDs []int64
	if len(tags) == 0 {
		return classIDs, methodIDs, nil
	}

	// 构造 IN (?,?,...) 占位符 + HAVING COUNT(DISTINCT) = len(tags) 实现 AND 语义。
	placeholders := strings.Repeat("?,", len(tags))
	placeholders = placeholders[:len(placeholders)-1]
	args := make([]interface{}, 0, len(tags)+1)
	for _, t := range tags {
		args = append(args, t)
	}
	args = append(args, len(tags))

	cRows, err := s.db.Query(`
		SELECT lt.class_id FROM class_tag lt JOIN tag t ON t.id = lt.tag_id
		WHERE t.name IN (`+placeholders+`)
		GROUP BY lt.class_id HAVING COUNT(DISTINCT t.id) = ?`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer cRows.Close()
	for cRows.Next() {
		var id int64
		if err := cRows.Scan(&id); err != nil {
			return nil, nil, err
		}
		classIDs = append(classIDs, id)
	}

	mRows, err := s.db.Query(`
		SELECT lt.method_id FROM method_tag lt JOIN tag t ON t.id = lt.tag_id
		WHERE t.name IN (`+placeholders+`)
		GROUP BY lt.method_id HAVING COUNT(DISTINCT t.id) = ?`, args...)
	if err != nil {
		return nil, nil, err
	}
	defer mRows.Close()
	for mRows.Next() {
		var id int64
		if err := mRows.Scan(&id); err != nil {
			return nil, nil, err
		}
		methodIDs = append(methodIDs, id)
	}
	if classIDs == nil {
		classIDs = []int64{}
	}
	if methodIDs == nil {
		methodIDs = []int64{}
	}
	return classIDs, methodIDs, nil
}

// GetAllTagsWithCategories 返回所有已知标签及其分类。
func (s *SQLiteStore) GetAllTagsWithCategories(ctx context.Context) (map[string]string, error) {
	s.mu.RLock()
	defer s.mu.RUnlock()

	rows, err := s.db.Query("SELECT name, category FROM tag")
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := make(map[string]string)
	for rows.Next() {
		var n, c string
		if err := rows.Scan(&n, &c); err != nil {
			return nil, err
		}
		out[n] = c
	}
	return out, rows.Err()
}

// ---- 内部辅助（带锁的原子操作） ----

func (s *SQLiteStore) upsertFileLocked(filePath, contentHash string, lineCount int, byteSize int64) (int64, error) {
	var id int64
	err := s.db.QueryRow(`
		INSERT INTO file (absolute_path, content_hash, line_count, byte_size, parse_status)
		VALUES (?, ?, ?, ?, 'parse_ok')
		ON CONFLICT(absolute_path) DO UPDATE SET
			content_hash=excluded.content_hash,
			line_count=excluded.line_count,
			byte_size=excluded.byte_size,
			parse_status='parse_ok'
		RETURNING id`,
		filePath, contentHash, lineCount, byteSize).Scan(&id)
	return id, err
}

func (s *SQLiteStore) upsertClassesLocked(fileID int64, classes []parser.ClassIR) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM class WHERE file_id = ?", fileID); err != nil {
		return err
	}
	for _, c := range classes {
		if _, err := tx.Exec(`
		INSERT INTO class (file_id, name, full_name, type, parent_fqns,
			start_line, start_col, end_line, end_col, modifier, doc_comment)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			fileID, c.Name, c.FullName, c.Type, toJSON(c.ParentFQNs),
			c.StartLine, c.StartCol, c.EndLine, c.EndCol, c.Modifier, c.Doc); err != nil {
			return fmt.Errorf("insert class %s: %w", c.FullName, err)
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) upsertMethodsLocked(classID int64, methods []parser.MethodIR) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM method WHERE class_id = ?", classID); err != nil {
		return err
	}
	for _, m := range methods {
		if _, err := tx.Exec(`
		INSERT INTO method (class_id, name, full_name, signature, return_type,
			start_line, start_col, end_line, end_col, doc_comment)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			classID, m.Name, m.ClassFQN+"."+m.Name, m.Signature, m.ReturnType,
			m.StartLine, m.StartCol, m.EndLine, m.EndCol, m.Doc); err != nil {
			return fmt.Errorf("insert method %s: %w", m.Name, err)
		}
	}
	return tx.Commit()
}

func (s *SQLiteStore) upsertCallsLocked(fileID int64, calls []parser.CallIR) error {
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.Exec("DELETE FROM call WHERE file_id = ?", fileID); err != nil {
		return err
	}
	for _, c := range calls {
		if _, err := tx.Exec(`
		INSERT INTO call (file_id, caller_fqn, callee_fqn, call_type, line_number)
		VALUES (?, ?, ?, ?, ?)`,
			fileID, c.CallerFQN, c.CalleeFQN, c.CallType, c.LineNumber); err != nil {
			return fmt.Errorf("insert call: %w", err)
		}
	}
	return tx.Commit()
}

// classIDMapLocked 查询文件下 FullName -> classID 映射。
func (s *SQLiteStore) classIDMapLocked(fileID int64) (map[string]int64, error) {
	rows, err := s.db.Query("SELECT id, full_name FROM class WHERE file_id = ?", fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := make(map[string]int64)
	for rows.Next() {
		var id int64
		var fn string
		if err := rows.Scan(&id, &fn); err != nil {
			return nil, err
		}
		m[fn] = id
	}
	return m, rows.Err()
}

// ---- 扫描辅助 ----

func scanFiles(rows *sql.Rows) ([]*store.FileRecord, error) {
	var out []*store.FileRecord
	for rows.Next() {
		var f store.FileRecord
		var refBy, imps string
		if err := rows.Scan(&f.ID, &f.AbsolutePath, &f.ContentHash, &f.LineCount, &f.ByteSize,
			&refBy, &imps, &f.Language, &f.ParseStatus); err != nil {
			return nil, err
		}
		f.ReferencedByFiles = jsonStringSlice(refBy)
		f.Imports = jsonStringSlice(imps)
		out = append(out, &f)
	}
	if out == nil {
		out = []*store.FileRecord{}
	}
	return out, rows.Err()
}

func scanClasses(rows *sql.Rows) ([]store.ClassRecord, error) {
	var out []store.ClassRecord
	for rows.Next() {
		var c store.ClassRecord
		var pf string
		if err := rows.Scan(&c.ID, &c.FileID, &c.Name, &c.FullName, &c.Type, &pf,
			&c.StartLine, &c.StartCol, &c.EndLine, &c.EndCol, &c.Modifier, &c.Doc, &c.Source); err != nil {
			return nil, err
		}
		c.ParentFQNs = jsonStringSlice(pf)
		out = append(out, c)
	}
	if out == nil {
		out = []store.ClassRecord{}
	}
	return out, rows.Err()
}

func scanMethods(rows *sql.Rows) ([]store.MethodRecord, error) {
	var out []store.MethodRecord
	for rows.Next() {
		var m store.MethodRecord
		if err := rows.Scan(&m.ID, &m.ClassID, &m.Name, &m.FullName, &m.Signature, &m.ReturnType,
			&m.StartLine, &m.StartCol, &m.EndLine, &m.EndCol, &m.Doc, &m.Source); err != nil {
			return nil, err
		}
		out = append(out, m)
	}
	if out == nil {
		out = []store.MethodRecord{}
	}
	return out, rows.Err()
}

// ---- 通用工具 ----

func toJSON(v any) string {
	b, err := json.Marshal(v)
	if err != nil {
		return "[]"
	}
	return string(b)
}

func jsonStringSlice(s string) []string {
	if s == "" {
		return []string{}
	}
	var out []string
	if err := json.Unmarshal([]byte(s), &out); err != nil || out == nil {
		return []string{}
	}
	return out
}

func dedupe(in []string) []string {
	seen := make(map[string]bool, len(in))
	var out []string
	for _, t := range in {
		if !seen[t] {
			seen[t] = true
			out = append(out, t)
		}
	}
	return out
}

// deriveTagCategory 根据标签名推断其分类（与 FileStore 保持一致）。
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

func ensureDir(dir string) error {
	if dir == "" {
		return nil
	}
	return fsperm.MkdirAll(dir)
}

// BulkUpsert 批量入库多个文件的 IR（语义同逐文件 UpsertIR，但置于单事务 +
// prepared statement，消除逐文件事务提交放大）。用于超大仓首次灌入 / 整仓重索引。
//
// 实测（N=100k，约 700k 行）：相比逐文件 UpsertIR（每文件拆 4~5 事务、100k 文件≈70万次
// 提交，180~237s），单事务批量 + 预编译语句可将落库压到秒级~十几秒，提速约 1~2 个数量级。
func (s *SQLiteStore) BulkUpsert(ctx context.Context, irs []*parser.IRDocument) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if len(irs) == 0 {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("bulk begin: %w", err)
	}
	defer tx.Rollback()

	// 预编译语句：循环外 prepare 一次，循环内复用，消除每条语句的 SQL 解析开销。
	stmtFile, err := tx.Prepare(`INSERT INTO file (absolute_path, content_hash, line_count, byte_size, parse_status)
		VALUES (?, ?, ?, ?, 'parse_ok')
		ON CONFLICT(absolute_path) DO UPDATE SET content_hash=excluded.content_hash, line_count=excluded.line_count, byte_size=excluded.byte_size, parse_status='parse_ok'
		RETURNING id`)
	if err != nil {
		return fmt.Errorf("prepare file: %w", err)
	}
	defer stmtFile.Close()
	stmtClass, err := tx.Prepare(`INSERT INTO class (file_id, name, full_name, type, parent_fqns, start_line, start_col, end_line, end_col, modifier, doc_comment)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?) RETURNING id`)
	if err != nil {
		return fmt.Errorf("prepare class: %w", err)
	}
	defer stmtClass.Close()
	stmtDelClass, err := tx.Prepare(`DELETE FROM class WHERE file_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare del class: %w", err)
	}
	defer stmtDelClass.Close()
	stmtDelMethod, err := tx.Prepare(`DELETE FROM method WHERE class_id IN (SELECT id FROM class WHERE file_id = ?)`)
	if err != nil {
		return fmt.Errorf("prepare del method: %w", err)
	}
	defer stmtDelMethod.Close()
	stmtMethod, err := tx.Prepare(`INSERT INTO method (class_id, name, full_name, signature, return_type, start_line, start_col, end_line, end_col, doc_comment)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare method: %w", err)
	}
	defer stmtMethod.Close()
	stmtDelCall, err := tx.Prepare(`DELETE FROM call WHERE file_id = ?`)
	if err != nil {
		return fmt.Errorf("prepare del call: %w", err)
	}
	defer stmtDelCall.Close()
	stmtCall, err := tx.Prepare(`INSERT INTO call (file_id, caller_fqn, callee_fqn, call_type, line_number)
		VALUES (?, ?, ?, ?, ?)`)
	if err != nil {
		return fmt.Errorf("prepare call: %w", err)
	}
	defer stmtCall.Close()

	for _, ir := range irs {
		var fileID int64
		if err := stmtFile.QueryRow(ir.FilePath, ir.FileHash, ir.LineCount, ir.ByteSize).Scan(&fileID); err != nil {
			return fmt.Errorf("bulk upsert file %s: %w", ir.FilePath, err)
		}

		// 类：插入并收集 full_name -> classID（同文件内），供方法归属。
		classIDByFQN := make(map[string]int64, len(ir.Classes))
		if len(ir.Classes) > 0 {
			if _, err := stmtDelClass.Exec(fileID); err != nil {
				return fmt.Errorf("bulk del class: %w", err)
			}
			for _, c := range ir.Classes {
				var cid int64
				if err := stmtClass.QueryRow(fileID, c.Name, c.FullName, c.Type, toJSON(c.ParentFQNs),
					c.StartLine, c.StartCol, c.EndLine, c.EndCol, c.Modifier, c.Doc).Scan(&cid); err != nil {
					return fmt.Errorf("bulk insert class %s: %w", c.FullName, err)
				}
				classIDByFQN[c.FullName] = cid
			}
		}

		// 方法：按 ClassFQN 归属到上面拿到的 classID。
		if len(ir.Methods) > 0 {
			if _, err := stmtDelMethod.Exec(fileID); err != nil {
				return fmt.Errorf("bulk del method: %w", err)
			}
			for _, m := range ir.Methods {
				cid, ok := classIDByFQN[m.ClassFQN]
				if !ok {
					continue
				}
				if _, err := stmtMethod.Exec(cid, m.Name, m.ClassFQN+"."+m.Name, m.Signature, m.ReturnType,
					m.StartLine, m.StartCol, m.EndLine, m.EndCol, m.Doc); err != nil {
					return fmt.Errorf("bulk insert method %s: %w", m.Name, err)
				}
			}
		}

		// 调用：全量替换归属。
		if len(ir.Calls) > 0 {
			if _, err := stmtDelCall.Exec(fileID); err != nil {
				return fmt.Errorf("bulk del call: %w", err)
			}
			for _, c := range ir.Calls {
				if _, err := stmtCall.Exec(fileID, c.CallerFQN, c.CalleeFQN, c.CallType, c.LineNumber); err != nil {
					return fmt.Errorf("bulk insert call: %w", err)
				}
			}
		}

		// 文件级 imports / language 元数据。
		if len(ir.Imports) > 0 {
			if _, err := tx.Exec(`UPDATE file SET imports = ? WHERE id = ?`, toJSON(ir.Imports), fileID); err != nil {
				return fmt.Errorf("bulk update imports: %w", err)
			}
		}
		if ir.Language != "" {
			if _, err := tx.Exec(`UPDATE file SET language = ? WHERE id = ?`, ir.Language, fileID); err != nil {
				return fmt.Errorf("bulk update language: %w", err)
			}
		}
	}
	return tx.Commit()
}
