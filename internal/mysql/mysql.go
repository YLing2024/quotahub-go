// Package mysql 是账本层的 MySQL 方言（github.com/go-sql-driver/mysql）。
//
// 与 SQLite 方言的差异全部收在这里：DDL 类型（VARCHAR/BIGINT/DOUBLE/AUTO_INCREMENT）、
// 列存在性探测（information_schema.columns）、upsert 形态（ON DUPLICATE KEY UPDATE）、
// 字符集与排序规则（utf8mb4/utf8mb4_unicode_ci）。迁移只做增量，绝不 DROP / TRUNCATE / 重建表。
package mysql

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/go-sql-driver/mysql"

	"quotahub-go/internal/store"
)

// Dialect 实现 store.Dialect。
type Dialect struct{}

// New 返回 MySQL 方言单例。
func New() *Dialect { return &Dialect{} }

// Name 返回方言名。
func (d *Dialect) Name() string { return "mysql" }

// DriverName 返回 go-sql-driver/mysql 注册的驱动名。
func (d *Dialect) DriverName() string { return "mysql" }

// BuildDSN 由分段参数拼出 go-sql-driver 的 DSN。口令只进返回值，绝不落日志。
//
// 参数取舍：utf8mb4 保中文/emoji；parseTime=false（时间列一律 ISO 文本，不交给驱动解析）；
// timeout 控制建连、read/writeTimeout 防止慢查询挂死调度器；loc=Local 对文本时间列无影响。
func BuildDSN(host string, port int, name, user, password string) string {
	cfg := mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", host, port)
	cfg.DBName = name
	cfg.ParseTime = false
	cfg.Collation = "utf8mb4_unicode_ci"
	cfg.Loc = time.Local
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 15 * time.Second
	cfg.WriteTimeout = 15 * time.Second
	return cfg.FormatDSN()
}

// ddl 是建表语句（幂等）。time 为 UTC ISO 字符串 VARCHAR(32)，value 为 DOUBLE，
// value_text 为 legacy 列（保留，不写入）。
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
		"INSERT IGNORE INTO schema_version (version, applied_at) VALUES (?, ?)",
		migrateVersion, time.Now().UnixMilli()); err != nil {
		_ = tx.Rollback()
		return fmt.Errorf("记录 schema_version 失败: %w", err)
	}
	return tx.Commit()
}

// ColumnExists 用 information_schema.columns 探测列（限定当前库）。
func (d *Dialect) ColumnExists(ctx context.Context, q store.Querier, table, column string) (bool, error) {
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

// UpsertSQL 生成 ON DUPLICATE KEY UPDATE 形态（跨库搬运幂等写入）。
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
		assignments = append(assignments, c+" = VALUES("+c+")")
	}
	base := "INSERT INTO " + table + " (" + strings.Join(columns, ", ") + ") VALUES (" + strings.Join(placeholders, ", ") + ")"
	if len(assignments) == 0 {
		return base
	}
	return base + " ON DUPLICATE KEY UPDATE " + strings.Join(assignments, ", ")
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
