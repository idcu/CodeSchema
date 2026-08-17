//go:build pg

// Package pg 提供基于 PostgreSQL 的权威存储实现，完整实现 store.Store 接口，
// 用于超大仓（10万+ 文件）场景下的关系型横向扩展。
//
// 启用步骤（需联网拉取驱动；go.mod 已可写）：
//
//	go get github.com/lib/pq
//	go build -tags pg ./internal/store/pg
//
// DDL 见 NewPGStore.InitSchema（由 001_init.sql 平移并适配 PG 语法：
// AUTOINCREMENT → SERIAL、datetime('now') → now()）。
package pg

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	_ "github.com/lib/pq"

	"github.com/idcu/codeschema/internal/parser"
	"github.com/idcu/codeschema/internal/store"
)

// PGStore 基于 PostgreSQL 的存储实现。
type PGStore struct {
	db  *sql.DB
	dsn string
}

// NewPGStore 创建 PG 存储（未打开，需 Open）。
func NewPGStore() *PGStore { return &PGStore{} }

func (s *PGStore) Open(ctx context.Context, dsn string) error {
	db, err := sql.Open("postgres", dsn)
	if err != nil {
		return fmt.Errorf("pg open: %w", err)
	}
	if err := db.PingContext(ctx); err != nil {
		return fmt.Errorf("pg ping: %w", err)
	}
	s.db = db
	s.dsn = dsn
	return nil
}

func (s *PGStore) Close() error {
	if s.db != nil {
		return s.db.Close()
	}
	return nil
}

func (s *PGStore) HealthCheck(ctx context.Context) error {
	if s.db == nil {
		return fmt.Errorf("pg not opened")
	}
	return s.db.PingContext(ctx)
}

// InitSchema 在 PG 中创建与 001_init.sql 等价的表结构（PG 语法）。
func (s *PGStore) InitSchema(ctx context.Context) error {
	for _, stmt := range strings.Split(pgSchemaDDL(), ";\n") {
		stmt = strings.TrimSpace(stmt)
		if stmt == "" {
			continue
		}
		if _, err := s.db.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("pg init schema: %w", err)
		}
	}
	return nil
}

// UpsertIR 对一个文件的 IR 执行增量入库（单事务）。
func (s *PGStore) UpsertIR(ctx context.Context, ir *parser.IRDocument) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	fileID, err := upsertFileTx(ctx, tx, ir)
	if err != nil {
		return err
	}
	if len(ir.Classes) > 0 {
		if err := upsertClassesTx(ctx, tx, fileID, ir.Classes, ir.Source); err != nil {
			return err
		}
		// 方法随类写入（骨架历史缺陷：method 从未落库，GetMethodsByClassID 恒空）
		if err := upsertMethodsTx(ctx, tx, ir.Classes, ir.Methods, ir.Source); err != nil {
			return err
		}
	}
	if len(ir.Calls) > 0 {
		if err := upsertCallsTx(ctx, tx, fileID, ir.Calls, ir.Source); err != nil {
			return err
		}
	}
	return tx.Commit()
}

// upsertMethodsTx 写入类下的方法（按 ClassFQN 关联类 ID，ON CONFLICT 幂等）。
func upsertMethodsTx(ctx context.Context, tx *sql.Tx, classes []parser.ClassIR, methods []parser.MethodIR, src string) error {
	// 构建类 FQN → 类 ID 映射
	classIDByFQN := make(map[string]int64)
	for _, c := range classes {
		const q = `SELECT id FROM class WHERE full_name=$1 LIMIT 1`
		var id int64
		if err := tx.QueryRowContext(ctx, q, c.FullName).Scan(&id); err == nil {
			classIDByFQN[c.FullName] = id
		}
	}
	for _, m := range methods {
		classID, ok := classIDByFQN[m.ClassFQN]
		if !ok {
			continue
		}
		const q = `INSERT INTO method (class_id, name, signature, return_type, start_line, start_col, end_line, end_col, modifier, doc_comment, source)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11)
			ON CONFLICT DO NOTHING`
		if _, err := tx.ExecContext(ctx, q, classID, m.Name, m.Signature, m.ReturnType,
			m.StartLine, m.StartCol, m.EndLine, m.EndCol, m.Modifier, m.Doc, src); err != nil {
			return err
		}
	}
	return nil
}

func upsertFileTx(ctx context.Context, tx *sql.Tx, ir *parser.IRDocument) (int64, error) {
	ref, _ := json.Marshal(ir.ReferencedBy)
	const q = `INSERT INTO file (absolute_path, relative_path, content_hash, commit_hash, line_count, byte_size, referenced_by_files, language, parse_status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9)
		ON CONFLICT (absolute_path) DO UPDATE SET content_hash=EXCLUDED.content_hash, line_count=EXCLUDED.line_count,
			byte_size=EXCLUDED.byte_size, referenced_by_files=EXCLUDED.referenced_by_files, language=EXCLUDED.language, updated_at=now()
		RETURNING id`
	var id int64
	err := tx.QueryRowContext(ctx, q, ir.FilePath, ir.FilePath, ir.FileHash, ir.CommitHash,
		ir.LineCount, ir.ByteSize, string(ref), ir.Language, "parse_ok").Scan(&id)
	return id, err
}

func upsertClassesTx(ctx context.Context, tx *sql.Tx, fileID int64, classes []parser.ClassIR, src string) error {
	for _, c := range classes {
		ann, _ := json.Marshal(c.Annotations)
		extra, _ := json.Marshal(c.Extra)
		const q = `INSERT INTO class (file_id, name, full_name, type, start_line, start_col, end_line, end_col, modifier, doc_comment, annotations, source, extra)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13)
			ON CONFLICT DO NOTHING`
		if _, err := tx.ExecContext(ctx, q, fileID, c.Name, c.FullName, c.Type,
			c.StartLine, c.StartCol, c.EndLine, c.EndCol, c.Modifier, c.Doc, string(ann), src, string(extra)); err != nil {
			return err
		}
	}
	return nil
}

func upsertCallsTx(ctx context.Context, tx *sql.Tx, fileID int64, calls []parser.CallIR, src string) error {
	for _, c := range calls {
		const q = `INSERT INTO call (file_id, caller_fqn, callee_fqn, call_type, line_number, source)
			VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT DO NOTHING`
		if _, err := tx.ExecContext(ctx, q, fileID, c.CallerFQN, c.CalleeFQN, c.CallType, c.LineNumber, src); err != nil {
			return err
		}
	}
	return nil
}

// UpsertFile 公开方法（接口要求）。
func (s *PGStore) UpsertFile(ctx context.Context, filePath, contentHash string, lineCount int, byteSize int64) (int64, error) {
	const q = `INSERT INTO file (absolute_path, relative_path, content_hash, line_count, byte_size, parse_status)
		VALUES ($1,$2,$3,$4,$5,$6) ON CONFLICT (absolute_path) DO UPDATE SET content_hash=EXCLUDED.content_hash, line_count=EXCLUDED.line_count, byte_size=EXCLUDED.byte_size, updated_at=now() RETURNING id`
	var id int64
	err := s.db.QueryRowContext(ctx, q, filePath, filePath, contentHash, lineCount, byteSize, "parse_ok").Scan(&id)
	return id, err
}

func (s *PGStore) GetFileByPath(ctx context.Context, path string) (*store.FileRecord, error) {
	const q = `SELECT id, absolute_path, content_hash, line_count, byte_size, referenced_by_files, imports, language, parse_status FROM file WHERE absolute_path=$1`
	row := s.db.QueryRowContext(ctx, q, path)
	return scanFile(row)
}

// MarkParseSkipped 记录一个被旁路的文件（超限未解析），parse_status 置为 parse_skipped。
func (s *PGStore) MarkParseSkipped(ctx context.Context, filePath string, byteSize int64, lineCount int) (int64, error) {
	const q = `INSERT INTO file (absolute_path, relative_path, content_hash, line_count, byte_size, parse_status)
		VALUES ($1,$2,'', $3,$4,'parse_skipped')
		ON CONFLICT (absolute_path) DO UPDATE SET content_hash='', line_count=EXCLUDED.line_count,
		byte_size=EXCLUDED.byte_size, parse_status='parse_skipped', updated_at=now() RETURNING id`
	var id int64
	err := s.db.QueryRowContext(ctx, q, filePath, filePath, lineCount, byteSize).Scan(&id)
	return id, err
}

func (s *PGStore) GetFileByID(ctx context.Context, id int64) (*store.FileRecord, error) {
	const q = `SELECT id, absolute_path, content_hash, line_count, byte_size, referenced_by_files, imports, language, parse_status FROM file WHERE id=$1`
	row := s.db.QueryRowContext(ctx, q, id)
	return scanFile(row)
}

func (s *PGStore) GetAllFiles(ctx context.Context) ([]*store.FileRecord, error) {
	const q = `SELECT id, absolute_path, content_hash, line_count, byte_size, referenced_by_files, imports, language, parse_status FROM file ORDER BY id`
	rows, err := s.db.QueryContext(ctx, q)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []*store.FileRecord
	for rows.Next() {
		fr, err := scanFile(rows)
		if err != nil {
			return nil, err
		}
		out = append(out, fr)
	}
	return out, rows.Err()
}

func (s *PGStore) DeleteFile(ctx context.Context, fileID int64) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM call WHERE file_id=$1`, fileID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM class WHERE file_id=$1`, fileID); err != nil {
		return err
	}
	if _, err := tx.ExecContext(ctx, `DELETE FROM file WHERE id=$1`, fileID); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PGStore) GetClassesByFileID(ctx context.Context, fileID int64) ([]store.ClassRecord, error) {
	const q = `SELECT id, file_id, name, full_name, type, start_line, start_col, end_line, end_col, modifier, doc_comment, source FROM class WHERE file_id=$1`
	return queryClasses(ctx, s.db, q, fileID)
}

func (s *PGStore) GetMethodsByClassID(ctx context.Context, classID int64) ([]store.MethodRecord, error) {
	const q = `SELECT id, class_id, name, signature, return_type, start_line, start_col, end_line, end_col, doc_comment, source FROM method WHERE class_id=$1`
	return queryMethods(ctx, s.db, q, classID)
}

func (s *PGStore) GetCallsByFileID(ctx context.Context, fileID int64) ([]store.CallRecord, error) {
	const q = `SELECT caller_fqn, callee_fqn, call_type, line_number, source FROM call WHERE file_id=$1`
	rows, err := s.db.QueryContext(ctx, q, fileID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.CallRecord
	for rows.Next() {
		var r store.CallRecord
		if err := rows.Scan(&r.CallerFQN, &r.CalleeFQN, &r.CallType, &r.LineNumber, &r.Source); err != nil {
			return nil, err
		}
		out = append(out, r)
	}
	return out, rows.Err()
}

// —— 以下为标签与批量接口（生产实现可直接复用上面的事务模式） ——

func (s *PGStore) UpsertClasses(ctx context.Context, fileID int64, classes []parser.ClassIR) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertClassesTx(ctx, tx, fileID, classes, ""); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PGStore) UpsertMethods(ctx context.Context, classID int64, methods []parser.MethodIR) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	for _, m := range methods {
		ann, _ := json.Marshal(m.Annotations)
		extra, _ := json.Marshal(m.Extra)
		const q = `INSERT INTO method (class_id, name, signature, return_type, start_line, start_col, end_line, end_col, modifier, doc_comment, annotations, is_static, is_abstract, is_constructor, source, extra)
			VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16) ON CONFLICT DO NOTHING`
		if _, err := tx.ExecContext(ctx, q, classID, m.Name, m.Signature, m.ReturnType,
			m.StartLine, m.StartCol, m.EndLine, m.EndCol, m.Modifier, m.Doc, string(ann),
			boolToInt(m.IsStatic), boolToInt(m.IsAbstract), boolToInt(m.IsConstructor), "", string(extra)); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func (s *PGStore) UpsertCalls(ctx context.Context, fileID int64, calls []parser.CallIR) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if err := upsertCallsTx(ctx, tx, fileID, calls, ""); err != nil {
		return err
	}
	return tx.Commit()
}

func (s *PGStore) UpsertTags(ctx context.Context, classID int64, tags []string) error {
	return upsertTagsTx(ctx, s.db, "class_tag", "class_id", classID, tags)
}
func (s *PGStore) UpsertMethodTags(ctx context.Context, methodID int64, tags []string) error {
	return upsertTagsTx(ctx, s.db, "method_tag", "method_id", methodID, tags)
}
func (s *PGStore) GetTagsByClassID(ctx context.Context, classID int64) ([]string, error) {
	return getTagsTx(ctx, s.db, "class_tag", "class_id", classID)
}
func (s *PGStore) GetTagsByMethodID(ctx context.Context, methodID int64) ([]string, error) {
	return getTagsTx(ctx, s.db, "method_tag", "method_id", methodID)
}
func (s *PGStore) SearchByTag(ctx context.Context, tag string) (classIDs, methodIDs []int64, err error) {
	cids, err := idCol(ctx, s.db, `SELECT class_id FROM class_tag ct JOIN tag t ON t.id=ct.tag_id WHERE t.name=$1`, tag)
	if err != nil {
		return nil, nil, err
	}
	mids, err := idCol(ctx, s.db, `SELECT method_id FROM method_tag mt JOIN tag t ON t.id=mt.tag_id WHERE t.name=$1`, tag)
	if err != nil {
		return nil, nil, err
	}
	return cids, mids, nil
}
func (s *PGStore) GetAllTagsWithCategories(ctx context.Context) (map[string]string, error) {
	rows, err := s.db.QueryContext(ctx, `SELECT name, category FROM tag`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	m := map[string]string{}
	for rows.Next() {
		var n, c string
		if err := rows.Scan(&n, &c); err != nil {
			return nil, err
		}
		m[n] = c
	}
	return m, rows.Err()
}

// —— helpers ——

func boolToInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

func scanFile(scanner interface {
	Scan(...interface{}) error
}) (*store.FileRecord, error) {
	var (
		fr                           store.FileRecord
		ref, imp, hash, lang, status sql.NullString
		lc, bs                       sql.NullInt64
	)
	if err := scanner.Scan(&fr.ID, &fr.AbsolutePath, &hash, &lc, &bs, &ref, &imp, &lang, &status); err != nil {
		return nil, err
	}
	fr.ContentHash = hash.String
	fr.Language = lang.String
	fr.ParseStatus = status.String
	fr.LineCount = int(lc.Int64)
	fr.ByteSize = bs.Int64
	_ = json.Unmarshal([]byte(ref.String), &fr.ReferencedByFiles)
	_ = json.Unmarshal([]byte(imp.String), &fr.Imports)
	return &fr, nil
}

func queryClasses(ctx context.Context, db *sql.DB, q string, arg int64) ([]store.ClassRecord, error) {
	rows, err := db.QueryContext(ctx, q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.ClassRecord
	for rows.Next() {
		var r store.ClassRecord
		var typ sql.NullString
		if err := rows.Scan(&r.ID, &r.FileID, &r.Name, &r.FullName, &typ, &r.StartLine, &r.StartCol, &r.EndLine, &r.EndCol, &r.Modifier, &r.Doc, &r.Source); err != nil {
			return nil, err
		}
		r.Type = typ.String
		out = append(out, r)
	}
	return out, rows.Err()
}

func queryMethods(ctx context.Context, db *sql.DB, q string, arg int64) ([]store.MethodRecord, error) {
	rows, err := db.QueryContext(ctx, q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []store.MethodRecord
	for rows.Next() {
		var r store.MethodRecord
		var sig, ret, doc, src sql.NullString
		if err := rows.Scan(&r.ID, &r.ClassID, &r.Name, &sig, &ret, &r.StartLine, &r.StartCol, &r.EndLine, &r.EndCol, &doc, &src); err != nil {
			return nil, err
		}
		r.Signature = sig.String
		r.ReturnType = ret.String
		r.Doc = doc.String
		r.Source = src.String
		out = append(out, r)
	}
	return out, rows.Err()
}

func upsertTagsTx(ctx context.Context, db *sql.DB, linkTable, idCol string, id int64, tags []string) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if _, err := tx.ExecContext(ctx, `DELETE FROM `+linkTable+` WHERE `+idCol+`=$1`, id); err != nil {
		return err
	}
	for _, tag := range tags {
		if _, err := tx.ExecContext(ctx, `INSERT INTO tag (name, category) VALUES ($1,'auto') ON CONFLICT (name) DO NOTHING`, tag); err != nil {
			return err
		}
		var tagID int64
		if err := tx.QueryRowContext(ctx, `SELECT id FROM tag WHERE name=$1`, tag).Scan(&tagID); err != nil {
			return err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO `+linkTable+` (`+idCol+`, tag_id) VALUES ($1,$2) ON CONFLICT DO NOTHING`, id, tagID); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func getTagsTx(ctx context.Context, db *sql.DB, linkTable, idCol string, id int64) ([]string, error) {
	rows, err := db.QueryContext(ctx, `SELECT t.name FROM tag t JOIN `+linkTable+` l ON l.tag_id=t.id WHERE l.`+idCol+`=$1`, id)
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
	return out, rows.Err()
}

func idCol(ctx context.Context, db *sql.DB, q string, arg string) ([]int64, error) {
	rows, err := db.QueryContext(ctx, q, arg)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []int64
	for rows.Next() {
		var id int64
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		out = append(out, id)
	}
	return out, rows.Err()
}

// pgSchemaDDL 返回 PG 适配版 DDL（由 001_init.sql 平移：SERIAL 替代 AUTOINCREMENT、now() 替代 datetime('now')）。
func pgSchemaDDL() string {
	return `
CREATE TABLE IF NOT EXISTS project (
  id SERIAL PRIMARY KEY, name TEXT NOT NULL, language TEXT, root_path TEXT, version TEXT, created_at TEXT DEFAULT now()
);
CREATE TABLE IF NOT EXISTS file (
  id SERIAL PRIMARY KEY, project_id INTEGER, absolute_path TEXT, relative_path TEXT, file_category TEXT DEFAULT 'source',
  content_hash TEXT, commit_hash TEXT, line_count INTEGER DEFAULT 0, byte_size INTEGER DEFAULT 0,
  referenced_by_files TEXT DEFAULT '[]', imports TEXT DEFAULT '[]', language TEXT DEFAULT '', last_indexed_at TEXT, parse_status TEXT DEFAULT 'parse_ok',
  updated_at TEXT DEFAULT now(), UNIQUE(absolute_path)
);
-- 兼容旧库：file.imports 列在早期 DDL 缺失（被误放 method 表），
-- 查询 SQL 依赖该列，显式补列避免真实实例报 "column imports does not exist"。
ALTER TABLE file ADD COLUMN IF NOT EXISTS imports TEXT DEFAULT '[]';
CREATE INDEX IF NOT EXISTS idx_file_category ON file(file_category);
CREATE INDEX IF NOT EXISTS idx_file_language ON file(language);
CREATE TABLE IF NOT EXISTS class (
  id SERIAL PRIMARY KEY, file_id INTEGER, name TEXT, full_name TEXT, type TEXT, parent_class_id INTEGER,
  start_line INTEGER, start_col INTEGER, end_line INTEGER, end_col INTEGER, modifier TEXT DEFAULT '',
  doc_comment TEXT DEFAULT '', annotations TEXT DEFAULT '[]', source TEXT DEFAULT '', extra TEXT, created_at TEXT DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_class_file_line ON class(file_id, start_line, end_line);
CREATE INDEX IF NOT EXISTS idx_class_full_name ON class(full_name);
CREATE TABLE IF NOT EXISTS class_parent (
  class_id INTEGER, parent_class_id INTEGER, parent_fqn TEXT, relation_type TEXT,
  PRIMARY KEY(class_id, parent_class_id, parent_fqn)
);
CREATE TABLE IF NOT EXISTS method (
  id SERIAL PRIMARY KEY, class_id INTEGER, name TEXT, signature TEXT, return_type TEXT,
  start_line INTEGER, start_col INTEGER, end_line INTEGER, end_col INTEGER, modifier TEXT DEFAULT '',
  doc_comment TEXT DEFAULT '', annotations TEXT DEFAULT '[]', is_static INTEGER DEFAULT 0, is_abstract INTEGER DEFAULT 0,
  is_constructor INTEGER DEFAULT 0, imports TEXT DEFAULT '[]', source TEXT DEFAULT '', extra TEXT, created_at TEXT DEFAULT now()
);
CREATE INDEX IF NOT EXISTS idx_method_class_line ON method(class_id, start_line, end_line);
CREATE INDEX IF NOT EXISTS idx_method_class_id ON method(class_id);
CREATE TABLE IF NOT EXISTS parameter (
  id SERIAL PRIMARY KEY, method_id INTEGER, name TEXT, type TEXT, idx INTEGER, annotation TEXT
);
CREATE INDEX IF NOT EXISTS idx_param_method ON parameter(method_id);
CREATE TABLE IF NOT EXISTS ret_type (
  method_id INTEGER PRIMARY KEY, type TEXT, generic_type TEXT, description TEXT
);
CREATE TABLE IF NOT EXISTS tag (
  id SERIAL PRIMARY KEY, name TEXT UNIQUE, category TEXT
);
CREATE TABLE IF NOT EXISTS class_tag ( class_id INTEGER, tag_id INTEGER, PRIMARY KEY(class_id, tag_id) );
CREATE TABLE IF NOT EXISTS method_tag ( method_id INTEGER, tag_id INTEGER, PRIMARY KEY(method_id, tag_id) );
CREATE TABLE IF NOT EXISTS call (
  id SERIAL PRIMARY KEY, file_id INTEGER, caller_fqn TEXT, callee_fqn TEXT, call_type TEXT DEFAULT '', line_number INTEGER, source TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_call_caller ON call(caller_fqn);
CREATE INDEX IF NOT EXISTS idx_call_callee ON call(callee_fqn);
CREATE TABLE IF NOT EXISTS method_test_link (
  id SERIAL PRIMARY KEY, method_id INTEGER, test_method_id INTEGER, strategy TEXT, confidence INTEGER DEFAULT 70,
  UNIQUE(method_id, test_method_id)
);
CREATE INDEX IF NOT EXISTS idx_mtl_method ON method_test_link(method_id);
CREATE INDEX IF NOT EXISTS idx_mtl_test ON method_test_link(test_method_id);
`
}

var _ store.Store = (*PGStore)(nil)

// BulkUpsert 批量入库多个文件的 IR（语义同逐文件 UpsertIR，但置于单事务 +
// prepared statement + RETURNING id 维护 classID 映射，消除逐文件事务提交放大）。
// 注意：相对 UpsertIR 额外补全了 methods 入库（UpsertIR 骨架当时未覆盖 methods）。
func (s *PGStore) BulkUpsert(ctx context.Context, irs []*parser.IRDocument) error {
	if len(irs) == 0 {
		return nil
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()

	const qFile = `INSERT INTO file (absolute_path, relative_path, content_hash, commit_hash, line_count, byte_size, referenced_by_files, language, parse_status)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9) ON CONFLICT (absolute_path) DO UPDATE SET content_hash=EXCLUDED.content_hash, line_count=EXCLUDED.line_count, byte_size=EXCLUDED.byte_size, referenced_by_files=EXCLUDED.referenced_by_files, language=EXCLUDED.language, updated_at=now() RETURNING id`
	const qClass = `INSERT INTO class (file_id, name, full_name, type, start_line, start_col, end_line, end_col, modifier, doc_comment, annotations, source, extra)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13) RETURNING id`
	const qMethod = `INSERT INTO method (class_id, name, signature, return_type, start_line, start_col, end_line, end_col, modifier, doc_comment, annotations, is_static, is_abstract, is_constructor, source, extra)
		VALUES ($1,$2,$3,$4,$5,$6,$7,$8,$9,$10,$11,$12,$13,$14,$15,$16)`

	for _, ir := range irs {
		ref, _ := json.Marshal(ir.ReferencedBy)
		var fileID int64
		if err := tx.QueryRowContext(ctx, qFile, ir.FilePath, ir.FilePath, ir.FileHash, ir.CommitHash,
			ir.LineCount, ir.ByteSize, string(ref), ir.Language, "parse_ok").Scan(&fileID); err != nil {
			return fmt.Errorf("bulk upsert file %s: %w", ir.FilePath, err)
		}
		classIDByFQN := make(map[string]int64, len(ir.Classes))
		if len(ir.Classes) > 0 {
			for _, c := range ir.Classes {
				ann, _ := json.Marshal(c.Annotations)
				extra, _ := json.Marshal(c.Extra)
				var cid int64
				if err := tx.QueryRowContext(ctx, qClass, fileID, c.Name, c.FullName, c.Type,
					c.StartLine, c.StartCol, c.EndLine, c.EndCol, c.Modifier, c.Doc, string(ann), ir.Source, string(extra)).Scan(&cid); err != nil {
					return fmt.Errorf("bulk insert class %s: %w", c.FullName, err)
				}
				classIDByFQN[c.FullName] = cid
			}
		}
		if len(ir.Methods) > 0 {
			for _, m := range ir.Methods {
				cid, ok := classIDByFQN[m.ClassFQN]
				if !ok {
					continue
				}
				ann, _ := json.Marshal(m.Annotations)
				extra, _ := json.Marshal(m.Extra)
				if _, err := tx.ExecContext(ctx, qMethod, cid, m.Name, m.Signature, m.ReturnType,
					m.StartLine, m.StartCol, m.EndLine, m.EndCol, m.Modifier, m.Doc, string(ann),
					boolToInt(m.IsStatic), boolToInt(m.IsAbstract), boolToInt(m.IsConstructor), "", string(extra)); err != nil {
					return fmt.Errorf("bulk insert method %s: %w", m.Name, err)
				}
			}
		}
		if len(ir.Calls) > 0 {
			if err := upsertCallsTx(ctx, tx, fileID, ir.Calls, ir.Source); err != nil {
				return err
			}
		}
	}
	return tx.Commit()
}
