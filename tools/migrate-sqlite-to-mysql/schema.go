package main

import (
	"context"
	"database/sql"
	"fmt"
)

// ddl 与 internal/store 的建表语句保持一致；独立模块无法 import 服务内部包，故此处复制。
// 表名 / 列名映射与默认值见 docs/DB.md。
var ddl = []string{
	`CREATE TABLE IF NOT EXISTS history_samples (
		id          BIGINT NOT NULL AUTO_INCREMENT,
		platform_id VARCHAR(64) NOT NULL,
		time        VARCHAR(32) NOT NULL,
		value       DOUBLE NOT NULL,
		value_text  TEXT NULL,
		PRIMARY KEY (id),
		KEY idx_history_platform_time (platform_id, time)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS users (
		id            BIGINT NOT NULL AUTO_INCREMENT,
		username      VARCHAR(190) NOT NULL,
		password_hash VARCHAR(255) NOT NULL,
		created_at    VARCHAR(32) NOT NULL,
		PRIMARY KEY (id),
		UNIQUE KEY uk_users_username (username)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS sessions (
		token      VARCHAR(64) NOT NULL,
		username   VARCHAR(190) NOT NULL,
		created_at VARCHAR(32) NOT NULL,
		expires_at VARCHAR(32) NOT NULL,
		PRIMARY KEY (token),
		KEY idx_sessions_expires (expires_at)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
	`CREATE TABLE IF NOT EXISTS schema_version (
		version    INT NOT NULL,
		applied_at BIGINT NOT NULL,
		PRIMARY KEY (version)
	) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4 COLLATE=utf8mb4_unicode_ci`,
}

// tableSpec 描述一张被搬运的表：列顺序、冲突键、需归一化的数值列。
type tableSpec struct {
	name       string
	columns    []string
	keyColumns []string
	floatCols  map[string]bool
}

// tables 是搬运顺序与映射（与旧 SQLite 表的列一一对应，不改名、不清洗）。
var tables = []tableSpec{
	{
		name:       "history_samples",
		columns:    []string{"id", "platform_id", "time", "value", "value_text"},
		keyColumns: []string{"id"},
		floatCols:  map[string]bool{"value": true},
	},
	{
		name:       "users",
		columns:    []string{"id", "username", "password_hash", "created_at"},
		keyColumns: []string{"id"},
	},
	{
		name:       "sessions",
		columns:    []string{"token", "username", "created_at", "expires_at"},
		keyColumns: []string{"token"},
	},
}

// ensureSchema 建表 + 补列（幂等、事务内，绝不 DROP / TRUNCATE / 重建）。
func ensureSchema(ctx context.Context, db *sql.DB) error {
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return fmt.Errorf("开启目标迁移事务失败: %w", err)
	}
	for _, stmt := range ddl {
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("执行目标建表语句失败: %w", err)
		}
	}
	// 旧库可能缺 value_text，目标已存在的老表也要能补齐。
	exists, err := columnExists(ctx, tx, "history_samples", "value_text")
	if err != nil {
		_ = tx.Rollback()
		return err
	}
	if !exists {
		if _, err := tx.ExecContext(ctx, "ALTER TABLE history_samples ADD COLUMN value_text TEXT NULL"); err != nil {
			_ = tx.Rollback()
			return fmt.Errorf("补齐 history_samples.value_text 失败: %w", err)
		}
	}
	return tx.Commit()
}

// columnExists 用 information_schema 探测列（限定当前库）。
func columnExists(ctx context.Context, q queryExecer, table, column string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.columns "+
			"WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?",
		table, column).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("读取目标表 %s 结构失败: %w", table, err)
	}
	return n > 0, nil
}

// queryExecer 是 *sql.DB 与 *sql.Tx 的公共子集。
type queryExecer interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}
