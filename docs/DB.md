# QuotaHub Go 数据库说明

本文件是 quotahub-go 存储层的权威说明：运行时只支持 **MySQL**，建表与增量迁移的规则、
连接参数、与旧版 SQLite 的对照，以及一次性数据搬迁工具。

> 口径变更（2026-10-16，用户定）：新版一律 MySQL，不再支持 SQLite。服务运行时**不引入**
> SQLite 驱动、不保留 `DB_DRIVER` 开关、不留 SQLite 方言分支；旧 SQLite 数据由独立迁移
> 工具一次性搬到 MySQL，之后不再需要 SQLite。

## 1. 连接配置（环境变量）

服务只读环境变量；口令只从环境变量注入，仓库只提交 `.env.example`。

| 变量 | 默认值 | 说明 |
| --- | --- | --- |
| `DB_DSN` | 空 | 整条 go-sql-driver DSN；非空时覆盖下面全部 `DB_*` 分段键 |
| `DB_HOST` | `127.0.0.1` | 主机 |
| `DB_PORT` | `3306` | 端口 |
| `DB_NAME` | `quotahub` | 库名 |
| `DB_USER` | `quotahub` | 用户名 |
| `DB_PASSWORD` | 空 | 口令（只从环境变量注入） |

- 字符集 `utf8mb4`，排序 `utf8mb4_unicode_ci`，表引擎 `InnoDB`。
- DSN 组装见 `internal/mysql.BuildDSN`：`parseTime=false`（时间列一律 ISO 文本）、建连超时 5s、
  读写超时 15s。
- 服务启动会先连库并跑迁移；连不上直接启动失败（不静默降级）。

## 2. 表结构

运行时表共四张，语义与旧实现一致。

### history_samples（历史采样）

| 列 | 类型 | 说明 |
| --- | --- | --- |
| `id` | `BIGINT AUTO_INCREMENT` | 自增主键，仅库内使用 |
| `platform_id` | `VARCHAR(64)` | 平台 ID |
| `time` | `VARCHAR(32)` | 采样时间，UTC ISO 8601 文本 |
| `value` | `DOUBLE` | 余额数值 |
| `value_text` | `TEXT NULL` | 旧版本遗留列，物理保留供旧数据读取，新代码不写入 |

索引 `idx_history_platform_time(platform_id, time)`。每平台保留最新 2000 点（按 `time DESC, id DESC`）。

### users / sessions（builtin 认证）

| users 列 | 类型 | | sessions 列 | 类型 |
| --- | --- | --- | --- | --- |
| `id` | `BIGINT AUTO_INCREMENT` | | `token` | `VARCHAR(64)` PK |
| `username` | `VARCHAR(190)` UNIQUE | | `username` | `VARCHAR(190)` |
| `password_hash` | `VARCHAR(255)` | | `created_at` | `VARCHAR(32)` |
| `created_at` | `VARCHAR(32)` | | `expires_at` | `VARCHAR(32)` |

`sessions` 另有索引 `idx_sessions_expires(expires_at)`。

### schema_version

记录已应用的迁移版本（`version` 主键、`applied_at` 毫秒）。当前版本 `1`。

> 时间列一律 UTC ISO 文本（如 `2026-01-02T03:04:05.678Z`），字典序即时序，不交给驱动解析。

## 3. 建表与增量迁移

- 建表与迁移收在 `internal/store`（`schema.go` 定义 DDL 与需补的列，`migrate.go` 执行）。
- 启动时自动补齐缺表缺列；整个过程在**单个事务**内、**幂等**。
- 只做增量：`CREATE TABLE IF NOT EXISTS`、`CREATE INDEX`（随建表）、`ALTER TABLE ... ADD COLUMN`。
  **禁止** `DROP` / `TRUNCATE` / 重建表。
- `ensureAlterColumns` 用 `information_schema.columns` 探测列是否存在，缺了才补
  （需要防的老库缺列只有 `history_samples.value_text`）。
- 版本号写入 `schema_version`（`INSERT IGNORE`，重复执行无副作用）。

## 4. 一次性迁移工具 migrate-sqlite-to-mysql

位置：`tools/migrate-sqlite-to-mysql/`。它是**独立 Go 模块**（自带 `go.mod`），唯一的
SQLite 依赖持有者，**不进服务运行时、不被 `cmd/quotahub-go` import**。跑一次即可。

```
cd tools/migrate-sqlite-to-mysql
# 演练：只读源库报源计数，不连目标库
GOFLAGS=-mod=mod GOPROXY=off go run . --dry-run --sqlite /path/to/old-monitor.db

# 正式搬运（目标 MySQL 用分段参数或一条 --mysql DSN）
GOFLAGS=-mod=mod GOPROXY=off go run . \
  --sqlite /path/to/old-monitor.db \
  --mysql-host 127.0.0.1 --mysql-port 3306 \
  --mysql-name quotahub --mysql-user quotahub --mysql-password "$DB_PASSWORD"
```

参数：`--sqlite`（必填）、`--mysql` 或 `--mysql-host/--mysql-port/--mysql-name/--mysql-user/
--mysql-password`、`--dry-run`、`--sample-size`（默认 5）。口令也可用环境变量 `DB_PASSWORD`。

行为与保证：

1. **源库只读**：以 `file:<path>?mode=ro&_pragma=query_only(true)` 打开，只执行 `SELECT` / `PRAGMA`，
   绝不建表、改结构或删行。
2. 目标库按第 3 节的增量规则补齐表结构。
3. 逐表「**源计数 → 拷贝 → 目标计数 → 抽样比对**」：每表单事务、走 `INSERT ... ON DUPLICATE KEY UPDATE`
   幂等写入；抽样按主键读回比对数值/文本。任一条数或抽样不一致即非 0 退出并打印明细。
4. 数值列（`history_samples.value`）在比对前归一为 `float64`，消除驱动返回差异。
5. 目标库建议先用空库；若已有数据，条数比对可能因目标库既有行而报“条数不一致”。

### 表 / 列映射（旧 SQLite → MySQL）

表名与列名**原样对应**，不改名、不清洗；仅类型按 MySQL 落地。

| 旧 SQLite | MySQL | 备注 |
| --- | --- | --- |
| `history_samples` | `history_samples` | 逐列同名 |
| `users` | `users` | 逐列同名 |
| `sessions` | `sessions` | 逐列同名 |
| `INTEGER PRIMARY KEY AUTOINCREMENT` | `BIGINT AUTO_INCREMENT` | `history_samples.id`、`users.id` |
| `TEXT` | `VARCHAR(32/64/190/255)` 或 `TEXT` | 按列长宽 |
| `REAL` | `DOUBLE` | `history_samples.value` |

搬运完成后，旧 SQLite 文件不再被服务使用；工具可删除，SQLite 依赖随之消失。

## 5. 待办与已知差异

- `blocked` 屏蔽名单：需求书提到，但旧实现无对应代码/字段，本版功能对齐即“无”，详见 `docs/API.md` 末节。
- 应用层 JSON 存档（`platforms.json` / `presets.json` / `settings.json` / `logs.json`）仍在
  `QUOTAHUB_DATA_DIR` 下，不进数据库；数据库只承载上表四张。
