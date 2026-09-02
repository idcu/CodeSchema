-- 001_init.sql
-- CodeSchema P0 基础 DDL
-- 适用数据库：SQLite 3.x / PostgreSQL
--
-- ⚠️ 本文件是「12 表 ID 型」目标设计稿 / PG 参考 DDL，运行时不加载执行。
-- 实际后端 schema 以内联 DDL 为准：
--   · 默认 SQLite 运行时 → internal/store/sqlite/sqlite.go（FQN 型轻量 schema）
--   · PG 后端 → internal/store/pg/pg.go（本文件等价平移：SERIAL 替代 AUTOINCREMENT）
-- 两处行为级 schema（call 用 caller_fqn/callee_fqn、file 含 imports 等）以代码为准。

-- project：项目元信息
CREATE TABLE IF NOT EXISTS project (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  language TEXT,
  root_path TEXT,
  version TEXT,
  created_at TEXT DEFAULT (datetime('now'))
);

-- file：文件元信息（含扩展字段）
CREATE TABLE IF NOT EXISTS file (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  project_id INTEGER,
  absolute_path TEXT,
  relative_path TEXT,
  file_category TEXT DEFAULT 'source',   -- source / header / test / generated
  content_hash TEXT,                     -- SHA-256
  commit_hash TEXT,
  line_count INTEGER DEFAULT 0,          -- 文件总行数，规模感知/裁剪限流
  byte_size INTEGER DEFAULT 0,           -- 字节大小，大文件旁路/分批
  referenced_by_files TEXT DEFAULT '[]', -- JSONB，反向引用本文件的文件清单
  imports TEXT DEFAULT '[]',             -- JSONB，文件 import 快照
  language TEXT DEFAULT '',              -- 文件主语言，高频查询免 join project
  last_indexed_at TEXT,                  -- 本次成功索引时间
  parse_status TEXT DEFAULT 'parse_ok',  -- parse_ok / parse_skipped / parse_error
  updated_at TEXT DEFAULT (datetime('now')),
  UNIQUE(absolute_path)
);
CREATE INDEX IF NOT EXISTS idx_file_category ON file(file_category);
CREATE INDEX IF NOT EXISTS idx_file_language ON file(language);
CREATE INDEX IF NOT EXISTS idx_file_hash ON file(content_hash);

-- class：类/接口/枚举/抽象类
CREATE TABLE IF NOT EXISTS class (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  file_id INTEGER,
  name TEXT,
  full_name TEXT,
  type TEXT,                             -- CLASS / INTERFACE / ABSTRACT / ENUM
  parent_class_id INTEGER,
  start_line INTEGER, start_col INTEGER,
  end_line INTEGER, end_col INTEGER,
  modifier TEXT DEFAULT '',
  doc_comment TEXT DEFAULT '',
  annotations TEXT DEFAULT '[]',         -- JSONB
  source TEXT DEFAULT '',                -- 数据来源
  extra TEXT,                            -- JSONB，语言差异兜底
  created_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_class_file_line ON class(file_id, start_line, end_line);
CREATE INDEX IF NOT EXISTS idx_class_full_name ON class(full_name);
CREATE INDEX IF NOT EXISTS idx_class_parent ON class(parent_class_id);   -- 按父类枚举子类/实现类

-- class_parent：继承/实现关系
CREATE TABLE IF NOT EXISTS class_parent (
  class_id INTEGER,
  parent_class_id INTEGER,               -- 可为 NULL（父类不在当前索引中）
  parent_fqn TEXT,                       -- 父类全限定名（兜底）
  relation_type TEXT,                    -- EXTENDS / IMPLEMENTS
  PRIMARY KEY(class_id, parent_class_id, parent_fqn)
);

-- method：方法定义
CREATE TABLE IF NOT EXISTS method (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  class_id INTEGER,
  name TEXT,
  signature TEXT,
  return_type TEXT,
  start_line INTEGER, start_col INTEGER,
  end_line INTEGER, end_col INTEGER,
  modifier TEXT DEFAULT '',
  doc_comment TEXT DEFAULT '',
  annotations TEXT DEFAULT '[]',         -- JSONB
  is_static INTEGER DEFAULT 0,
  is_abstract INTEGER DEFAULT 0,
  is_constructor INTEGER DEFAULT 0,
  imports TEXT DEFAULT '[]',             -- JSONB：方法所在文件的 import 快照
  source TEXT DEFAULT '',
  extra TEXT,                            -- JSONB
  created_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX IF NOT EXISTS idx_method_class_line ON method(class_id, start_line, end_line);
CREATE INDEX IF NOT EXISTS idx_method_class_id ON method(class_id);
CREATE INDEX IF NOT EXISTS idx_method_name ON method(name);

-- parameter：方法参数
CREATE TABLE IF NOT EXISTS parameter (
  id INTEGER PRIMARY KEY,
  method_id INTEGER,
  name TEXT,
  type TEXT,
  idx INTEGER,
  annotation TEXT
);
CREATE INDEX IF NOT EXISTS idx_param_method ON parameter(method_id);

-- ret_type：返回类型
CREATE TABLE IF NOT EXISTS ret_type (
  method_id INTEGER,
  type TEXT,
  generic_type TEXT,
  description TEXT,
  PRIMARY KEY(method_id)
);

-- tag：分类标签（layer / biz / tech / risk / test / lang）
CREATE TABLE IF NOT EXISTS tag (
  id INTEGER PRIMARY KEY,
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

-- call：调用关系（FQN 口径，与运行时 sqlite/pg 后端一致；caller/callee 以全限定名串接 method）
CREATE TABLE IF NOT EXISTS call (
  id INTEGER PRIMARY KEY,
  file_id INTEGER,
  caller_fqn TEXT,
  callee_fqn TEXT,
  call_type TEXT DEFAULT '',            -- direct / interface / dynamic / unknown
  line_number INTEGER,
  source TEXT DEFAULT ''
);
CREATE INDEX IF NOT EXISTS idx_call_caller ON call(caller_fqn);
CREATE INDEX IF NOT EXISTS idx_call_callee ON call(callee_fqn);

-- method_test_link：方法-测试关联（五种策略）
CREATE TABLE IF NOT EXISTS method_test_link (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  method_id INTEGER,
  test_method_id INTEGER,
  strategy TEXT,                         -- explicit / naming / coverage / same_tag / dependency
  confidence INTEGER DEFAULT 70,
  UNIQUE(method_id, test_method_id)
);
CREATE INDEX IF NOT EXISTS idx_mtl_method ON method_test_link(method_id);
CREATE INDEX IF NOT EXISTS idx_mtl_test ON method_test_link(test_method_id);