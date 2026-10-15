// Package sqlite 是账本层的 SQLite 方言（modernc.org/sqlite，纯 Go、无 cgo）。
//
// 与 MySQL 方言的差异全部收在这里：DDL 类型、列存在性探测（pragma_table_info）、
// upsert 形态（ON CONFLICT ... DO UPDATE）。迁移只做增量（CREATE TABLE IF NOT EXISTS /
// ADD COLUMN），绝不 DROP、TRUNCATE 或重建表 —— 旧库（含历史 value_text 列）必须原地继续可用。
package sqlite

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"quotahub-go/internal/store"

	// 注册 database/sql 驱动名 "sqlite"（modernc 纯 Go 实现，无 cgo）。
	_ "modernc.org/sqlite"
)

// Dialect 实现 store.Dialect。
type Dialect struct{}

// New 返回 SQLite 方言单例。
func New() *Dialect { return &Dialect{} }

// Name 返回方言名。
func (d *Dialect) Name() string { return "sqlite" }

// DriverName 返回 modernc.org/sqlite 注册的驱动名。
func (d *Dialect) DriverName() string { return "sqlite" }

// ddl 是建表语句（幂等）。列定义与旧实现 db/init.ts 逐字对齐：time 为 UTC ISO 字符串，
// value_text 为 legacy 列（保留，不写入）。
var ddl = []string{
	`CREATE TABLE IF NOT EXISTS history_samples (
		id          INTEGER PRIMARY KEY AUTOINCREMENT,
		platform_id TEXT NOT NULL,
		time        TEXT NOT NULL,
		value       REAL NOT NULL,
		value_text  TEXT
	)`,
	`CREATE INDEX IF NOT EXISTS idx_history_platform_time ON history_samples(platform_id, time)`,
	`CREATE TABLE IF NOT EXISTS users (
		id            INTEGER PRIMARY KEY AUTOINCREMENT,
		username      TEXT NOT NULL UNIQUE,
		password_hash TEXT NOT NULL,
		created_at    TEXT NOT NULL
	)`,
	`CREATE TABLE IF NOT EXISTS sessions (
		token      TEXT PRIMARY KEY,
		username   TEXT NOT NULL,
		created_at TEXT NOT NULL,
		expires_at TEXT NOT NULL
	)`,
	`CREATE INDEX IF NOT EXISTS idx_sessions_expires ON sessions(expires_at)`,
	`CREATE TABLE IF NOT EXISTS schema_version (
		version    INTEGER PRIMARY KEY,
		applied_at INTEGER NOT NULL
	)`,
}

// migrateVersion 是记录进 schema_version 的版本号（DDL 幂等，重复执行无副作用）。
const migrateVersion = 1

// Migrate 建表 / 建索引 / 补列（幂等、事务内），并记录 schema_version。
func (d *Dialect) Migrate(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启迁移事务失败: %w", err)
	}
	for _, stmt := range ddl {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("执行建表语句失败: %w", err)
		}
	}
	for _, table := range store.MigrateTables() {
		if err := store.EnsureAlterColumns(ctx, d, tx, table); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT OR IGNORE INTO schema_version (version, applied_at) VALUES (?, ?)",
		migrateVersion, time.Now().UnixMilli()); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("记录 schema_version 失败: %w", err)
	}
	return tx.Commit()
}

// ColumnExists 用 pragma_table_info 探测列；表名为受控常量，无注入面。
func (d *Dialect) ColumnExists(ctx context.Context, q store.Querier, table, column string) (bool, error) {
	rows, err := q.QueryContext(ctx, "SELECT name FROM pragma_table_info(?)", table)
	if err != nil {
		return false, fmt.Errorf("读取表 %s 结构失败: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return false, err
		}
		if name == column {
			return true, nil
		}
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	return false, nil
}

// UpsertSQL 生成 ON CONFLICT(key) DO UPDATE 形态（跨库搬运幂等写入）。
func (d *Dialect) UpsertSQL(table string, columns, keyColumns []string) string {
	placeholders := make([]string, len(columns))
	for i := range columns {
		placeholders[i] = "?"
	}
	assignments := make([]string, 0, len(columns))
	for _, c := range columns {
		if contains(keyColumns, c) {
			continue
		}
		assignments = append(assignments, c+" = excluded."+c)
	}
	base := "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	if len(assignments) == 0 {
		return base
	}
	return base + " ON CONFLICT(" + strings.Join(keyColumns, ", ") + ") DO UPDATE SET " + strings.Join(assignments, ", ")
}

// PrepareCopy 无操作：本服务表间无外键约束，拷贝天然不受引用完整性影响。
func (d *Dialect) PrepareCopy(_ context.Context, _ store.Querier) error { return nil }

func contains(list []string, v string) bool {
	for _, item := range list {
		if item == v {
			return true
		}
	}
	return false
}
