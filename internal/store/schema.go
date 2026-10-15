package store

import "context"

// ddl 是建表语句（幂等）。`time` 为 UTC ISO 字符串 VARCHAR(32)，value 为 DOUBLE，
// value_text 为旧版本遗留列（保留，不写入）。字符集与排序规则固定 utf8mb4。
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

// migrateVersion 是记录进 schema_version 的版本号（DDL 幂等，重复执行无副作用）。
const migrateVersion = 1

// alterSpecs 是各表用于「增量补列」的列定义。只列需要防老库缺列的列；
// 建表语句见 ddl（CREATE TABLE IF NOT EXISTS）。旧库可能缺 history_samples.value_text，
// 补齐后新代码仍不写入。
var alterSpecs = map[string][]string{
	"history_samples": {"value_text TEXT NULL"},
}

// migrateTables 是执行「增量补列」的表顺序（确定性，便于复现）。
var migrateTables = []string{"history_samples"}

// ensureAlterColumns 逐表补齐缺失列（幂等）。
func ensureAlterColumns(ctx context.Context, q Querier, table string) error {
	for _, spec := range alterSpecs[table] {
		name, typ := splitColumnDef(spec)
		exists, err := columnExists(ctx, q, table, name)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		if _, err := q.ExecContext(ctx, "ALTER TABLE "+table+" ADD COLUMN "+name+" "+typ); err != nil {
			return err
		}
	}
	return nil
}

// splitColumnDef 把 "name TYPE ..." 定义拆成列名与类型（列定义均为受控常量，无注入面）。
func splitColumnDef(def string) (name, typ string) {
	for i := 0; i < len(def); i++ {
		if def[i] == ' ' {
			return def[:i], def[i+1:]
		}
	}
	return def, ""
}
