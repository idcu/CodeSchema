# 数据模型与 DDL

> 开发顺序：第 1 步 · 先定义数据模型再写代码
> 前置依赖：[docs/dev/00-项目概述与架构概览.md](00-项目概述与架构概览.md)
> 对应原始章节：§4 数据模型

---

## 1. 设计原则

1. **类一张表、方法一张表**——Tag 独立表（多对多）；调用关系独立表。
2. **文件路径只存一次**（`file` 表）；行号区间索引支撑快速定位与增量匹配。
3. **`commit_hash` 作为版本锚点**，`imports` 以 JSONB 存储，`source` 追踪数据来源。
4. **文件表包含扩展字段**（`line_count`、`byte_size`、`referenced_by_files`），用于规模感知、大文件旁路和依赖闭环。

---

## 2. DDL

```sql
-- project：项目元信息
CREATE TABLE project (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  name TEXT NOT NULL,
  language TEXT,
  root_path TEXT,
  version TEXT,
  created_at TEXT DEFAULT (datetime('now'))
);

-- file：文件元信息（含扩展字段）
CREATE TABLE file (
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
  language TEXT,                         -- 文件主语言，高频查询免 join project
  last_indexed_at TEXT,                  -- 本次成功索引时间
  parse_status TEXT DEFAULT 'parse_ok',  -- parse_ok / parse_skipped / parse_error
  updated_at TEXT DEFAULT (datetime('now')),
  UNIQUE(absolute_path)
);
CREATE INDEX idx_file_category ON file(file_category);
CREATE INDEX idx_file_language ON file(language);

-- class：类/接口/枚举/抽象类
CREATE TABLE class (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  file_id INTEGER,
  name TEXT,
  full_name TEXT,
  type TEXT,                             -- CLASS / INTERFACE / ABSTRACT / ENUM
  parent_class_id INTEGER,
  start_line INTEGER, start_col INTEGER,
  end_line INTEGER, end_col INTEGER,
  modifier TEXT,
  doc_comment TEXT,
  annotations TEXT,                      -- JSONB
  source TEXT,                           -- 数据来源
  extra TEXT,                            -- JSONB，语言差异兜底
  created_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX idx_class_file_line ON class(file_id, start_line, end_line);
CREATE INDEX idx_class_full_name ON class(full_name);

-- class_parent：继承/实现关系
-- 父类不在当前索引中时，使用 parent_fqn 字段记录全限定名
CREATE TABLE class_parent (
  class_id INTEGER,
  parent_class_id INTEGER,               -- 可为 NULL（父类不在当前索引中）
  parent_fqn TEXT,                       -- 父类全限定名（兜底，parent_class_id 为 NULL 时使用）
  relation_type TEXT,                    -- EXTENDS / IMPLEMENTS
  PRIMARY KEY(class_id, parent_class_id, parent_fqn)
);

-- method：方法定义
CREATE TABLE method (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  class_id INTEGER,
  name TEXT,
  signature TEXT,
  return_type TEXT,
  start_line INTEGER, start_col INTEGER,
  end_line INTEGER, end_col INTEGER,
  modifier TEXT,
  doc_comment TEXT,
  annotations TEXT,                      -- JSONB
  is_static INTEGER DEFAULT 0,
  is_abstract INTEGER DEFAULT 0,
  is_constructor INTEGER DEFAULT 0,
  imports TEXT,                          -- JSONB：方法所在文件的 import 快照
  source TEXT,
  extra TEXT,                            -- JSONB
  created_at TEXT DEFAULT (datetime('now'))
);
CREATE INDEX idx_method_class_line ON method(class_id, start_line, end_line);
CREATE INDEX idx_method_class_id ON method(class_id);

-- parameter：方法参数
CREATE TABLE parameter (
  id INTEGER PRIMARY KEY,
  method_id INTEGER,
  name TEXT,
  type TEXT,
  idx INTEGER,
  annotation TEXT
);
CREATE INDEX idx_param_method ON parameter(method_id);

-- ret_type：返回类型
CREATE TABLE ret_type (
  method_id INTEGER,
  type TEXT,
  generic_type TEXT,
  description TEXT,
  PRIMARY KEY(method_id)
);

-- tag：分类标签（layer / biz / tech / risk / test / lang）
CREATE TABLE tag (
  id INTEGER PRIMARY KEY,
  name TEXT UNIQUE,
  category TEXT
);
CREATE TABLE class_tag (
  class_id INTEGER,
  tag_id INTEGER,
  PRIMARY KEY(class_id, tag_id)
);
CREATE TABLE method_tag (
  method_id INTEGER,
  tag_id INTEGER,
  PRIMARY KEY(method_id, tag_id)
);

-- call：调用关系（source 区分数据来源）
CREATE TABLE call (
  id INTEGER PRIMARY KEY,
  caller_method_id INTEGER,
  callee_method_id INTEGER,
  call_type TEXT,                        -- direct / interface / dynamic / unknown
  line_number INTEGER,
  source TEXT
);
CREATE INDEX idx_call_caller ON call(caller_method_id);
CREATE INDEX idx_call_callee ON call(callee_method_id);

-- method_test_link：方法-测试关联（五种策略）
CREATE TABLE method_test_link (
  id INTEGER PRIMARY KEY AUTOINCREMENT,
  method_id INTEGER,
  test_method_id INTEGER,
  strategy TEXT,                         -- explicit / naming / coverage / same_tag / dependency
  confidence INTEGER DEFAULT 70,
  UNIQUE(method_id, test_method_id)
);
CREATE INDEX idx_mtl_method ON method_test_link(method_id);
CREATE INDEX idx_mtl_test ON method_test_link(test_method_id);
```

---

## 3. 字段扩展评估

| 字段 | 类型 | 价值评估 | 推荐 |
|---|---|---|---|
| `line_count` | INTEGER | 文件总行数；支撑规模分层索引、裁剪限流、大文件旁路 | 强烈推荐 |
| `byte_size` | INTEGER | 文件字节大小；大文件旁路、分批、跳过生成式 AI 整文件读取 | 强烈推荐 |
| `referenced_by_files` | TEXT(JSONB) | 反向引用清单；C/C++ 头文件分析尤其关键，改 .h 经此精准定位受影响 .cpp 单测 | 强烈推荐 |
| `language` | TEXT | 高频查询免 join project，按语言分片扫描 | 推荐 |
| `last_indexed_at` | TEXT | 区分索引时间与内容变更时间，监控索引滞后 | 推荐 |
| `parse_status` | TEXT | 大文件跳过/解析失败留痕，运维可观测 | 推荐 |

**不推荐的字段**：`content`（绝不存原文）、`full_ast`（体积大且易过期）、冗余哈希（SHA-256 已足够）。

---

## 4. 索引策略

| 表 | 索引 | 查询场景 |
|---|---|---|
| `file` | `file_category`、`language` | 按分类/语言过滤 |
| `class` | `(file_id, start_line, end_line)`、`full_name` | 按行号定位、按全限定名查找 |
| `method` | `(class_id, start_line, end_line)`、`class_id` | 按类+行号定位、按类查找 |
| `call` | `caller_method_id`、`callee_method_id` | 调用图正向/反向遍历 |
| `method_test_link` | `method_id`、`test_method_id` | 按方法查关联单测 |
| `parameter` | `method_id` | 按方法查参数 |

---

## 5. KV Key 设计

| Key 模式 | Value | 用途 |
|---|---|---|
| `class:{full_name}` | class_id | 按全限定名查类 |
| `method:{class_id}:{name}:{sig}` | method_id | 查方法 |
| `file:{path}` | file_id | 路径 → 文件 |
| `file:{path}:meta` | `{line_count, byte_size, language, parse_status}` | 文件元信息热读 |
| `tag:class:{class_id}` | `[tag_id, ...]` | 类标签 |
| `tag:method:{method_id}` | `[tag_id, ...]` | 方法标签 |
| `caller:{method_id}` | `[callee_id, ...]` | 正向调用 |
| `callee:{method_id}` | `[caller_id, ...]` | 反向调用（影响面） |
| `refs:file:{file_id}` | `[referenced_by_file_id, ...]` | 反向引用 |

**设计原则**：SQLite/PG 为唯一真相源，Redis 由 DB 派生，可随时 `rebuild-kv` 重建。写入时机：IR 入库成功后同步写/更新对应 KV Key；删除实体时清理 Key。Redis 写入失败不应阻塞 DB 事务，采用"先写 DB，后写 Redis，失败记录日志 + 异步补偿"策略。

---

## 6. 开发指南

1. **创建 store 包**：在 `internal/store` 下创建 SQLite 初始化代码，加载所有 DDL。
2. **编写迁移脚本**：将上述 DDL 放入 `internal/store/migrations/001_init.sql`，使用 `database/sql` 执行。
3. **实现 DAO 层**：为每张表创建 CRUD 方法（`GetFileByPath`、`InsertClass`、`UpdateMethod` 等）。
4. **验证索引覆盖**：对照索引策略表，确保每个查询场景都有对应索引。
5. **实现 KV Key 生成**：在 `internal/kv` 包中定义 Key 常量与生成函数，为 P2 Redis 接入做准备。

---

## 7. 完成标准

- [ ] DDL 可在 SQLite 中完整执行，无报错
- [ ] 所有表及索引创建成功，通过 `sqlite3_master` 查询验证
- [ ] 每条 `CREATE INDEX` 的查询场景已确认覆盖
- [ ] KV Key 设计表已评审通过，覆盖所有高频查询场景
- [ ] `file` 表扩展字段（`line_count`、`byte_size`、`referenced_by_files`）已包含在 DDL 中
- [ ] 单元测试：内存 SQLite 建表 + 基础 CRUD 通过