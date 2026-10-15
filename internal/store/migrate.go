package store

import (
	"context"
	"database/sql"
	"fmt"
	"time"
)

// migrate 建表 / 建索引 / 补列（幂等、事务内），并记录 schema_version。
// 只做增量：CREATE TABLE IF NOT EXISTS 与 ADD COLUMN，绝不 DROP / TRUNCATE / 重建表。
func migrate(ctx context.Context, db *sql.DB) error {
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
	for _, table := range migrateTables {
		if err := ensureAlterColumns(ctx, tx, table); err != nil {
			_ = tx.Rollback()
			return err
		}
	}
	if _, err := tx.ExecContext(ctx,
		"INSERT IGNORE INTO schema_version (version, applied_at) VALUES (?, ?)",
		migrateVersion, time.Now().UnixMilli()); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("记录 schema_version 失败: %w", err)
	}
	return tx.Commit()
}

// columnExists 用 information_schema.columns 探测列（限定当前库）。
func columnExists(ctx context.Context, q Querier, table, column string) (bool, error) {
	var n int
	err := q.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM information_schema.columns "+
			"WHERE table_schema = DATABASE() AND table_name = ? AND column_name = ?",
		table, column).Scan(&n)
	if err != nil {
		return false, fmt.Errorf("读取表 %s 结构失败: %w", table, err)
	}
	return n > 0, nil
}
