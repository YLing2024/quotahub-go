// Package store 是账本（MySQL）的访问层：业务 SQL、建表与增量迁移。
//
// 运行时只有 MySQL 一条路径（2026-10-16 口径，不再支持 SQLite）：数据库连接参数与 DSN
// 组装收在 internal/mysql，建表与增量迁移收在本包，所有查询使用 `?` 占位符、不做字符串拼接。
//
// 表语义与旧实现一致：history_samples（历史采样，每平台保留最新 2000 点）、users / sessions
// （builtin 认证）。旧库 `value_text` 列物理保留供旧数据读取，新代码不写入。
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"

	// 注册 database/sql 驱动名 "mysql"（本服务唯一数据库）。
	_ "github.com/go-sql-driver/mysql"
)

// Querier 是 *sql.DB 与 *sql.Tx 的公共子集，便于把读写放进事务。
type Querier interface {
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// DB 是数据库句柄。
type DB struct {
	SQL *sql.DB
}

// Open 打开 MySQL 连接池并跑增量迁移。
func Open(ctx context.Context, dsn string, maxOpenConns int) (*DB, error) {
	sqlDB, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开数据库失败: %w", err)
	}
	if maxOpenConns > 0 {
		sqlDB.SetMaxOpenConns(maxOpenConns)
	}
	sqlDB.SetMaxIdleConns(maxOpenConns)
	sqlDB.SetConnMaxLifetime(0)
	if err := sqlDB.PingContext(ctx); err != nil {
		_ = sqlDB.Close()
		return nil, fmt.Errorf("连接数据库失败: %w", err)
	}
	if err := migrate(ctx, sqlDB); err != nil {
		_ = sqlDB.Close()
		return nil, err
	}
	return &DB{SQL: sqlDB}, nil
}

// Close 关闭底层连接池。
func (db *DB) Close() error { return db.SQL.Close() }

// isNoRows 判断是否「查询无结果」。
func isNoRows(err error) bool { return errors.Is(err, sql.ErrNoRows) }

// Tx 执行 fn 于单个事务；fn 返回错误则回滚。
func (db *DB) Tx(ctx context.Context, fn func(q Querier) error) error {
	tx, err := db.SQL.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if err := fn(tx); err != nil {
		_ = tx.Rollback()
		return err
	}
	return tx.Commit()
}
