package store

import (
	"context"
	"fmt"
)

// UserRow 是 users 表一行（builtin 认证）。
type UserRow struct {
	ID           int64
	Username     string
	PasswordHash string
	CreatedAt    string
}

// SessionRow 是 sessions 表一行；时间列均为 UTC ISO 字符串（可直接字典序比较）。
type SessionRow struct {
	Token     string
	Username  string
	CreatedAt string
	ExpiresAt string
}

// UserCount 返回账号总数（首启引导用：为 0 才创建管理员）。
func (db *DB) UserCount(ctx context.Context) (int64, error) {
	var n int64
	if err := db.SQL.QueryRowContext(ctx, "SELECT COUNT(*) FROM users").Scan(&n); err != nil {
		return 0, fmt.Errorf("统计账号失败: %w", err)
	}
	return n, nil
}

// FindUserByUsername 按用户名查账号；不存在返回 nil。
func (db *DB) FindUserByUsername(ctx context.Context, username string) (*UserRow, error) {
	var u UserRow
	err := db.SQL.QueryRowContext(ctx,
		"SELECT id, username, password_hash, created_at FROM users WHERE username = ?",
		username).Scan(&u.ID, &u.Username, &u.PasswordHash, &u.CreatedAt)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取账号失败: %w", err)
	}
	return &u, nil
}

// InsertUser 新建账号。
func (db *DB) InsertUser(ctx context.Context, username, passwordHash, createdAt string) error {
	_, err := db.SQL.ExecContext(ctx,
		"INSERT INTO users (username, password_hash, created_at) VALUES (?, ?, ?)",
		username, passwordHash, createdAt)
	if err != nil {
		return fmt.Errorf("创建账号失败: %w", err)
	}
	return nil
}

// InsertSession 写入一条会话。
func (db *DB) InsertSession(ctx context.Context, row SessionRow) error {
	_, err := db.SQL.ExecContext(ctx,
		"INSERT INTO sessions (token, username, created_at, expires_at) VALUES (?, ?, ?, ?)",
		row.Token, row.Username, row.CreatedAt, row.ExpiresAt)
	if err != nil {
		return fmt.Errorf("写入会话失败: %w", err)
	}
	return nil
}

// FindValidSession 命中且未过期才返回（nowIso 与 expires_at 同为 UTC ISO，字典序即时间序）。
func (db *DB) FindValidSession(ctx context.Context, token, nowIso string) (*SessionRow, error) {
	var s SessionRow
	err := db.SQL.QueryRowContext(ctx,
		"SELECT token, username, created_at, expires_at FROM sessions WHERE token = ? AND expires_at > ?",
		token, nowIso).Scan(&s.Token, &s.Username, &s.CreatedAt, &s.ExpiresAt)
	if err != nil {
		if isNoRows(err) {
			return nil, nil
		}
		return nil, fmt.Errorf("读取会话失败: %w", err)
	}
	return &s, nil
}

// TouchSession 滑动续期。
func (db *DB) TouchSession(ctx context.Context, token, expiresAt string) error {
	_, err := db.SQL.ExecContext(ctx,
		"UPDATE sessions SET expires_at = ? WHERE token = ?", expiresAt, token)
	return err
}

// DeleteSession 删除指定会话。
func (db *DB) DeleteSession(ctx context.Context, token string) error {
	_, err := db.SQL.ExecContext(ctx, "DELETE FROM sessions WHERE token = ?", token)
	return err
}

// DeleteExpiredSessions 清理过期会话，返回删除行数。
func (db *DB) DeleteExpiredSessions(ctx context.Context, nowIso string) (int64, error) {
	res, err := db.SQL.ExecContext(ctx, "DELETE FROM sessions WHERE expires_at <= ?", nowIso)
	if err != nil {
		return 0, fmt.Errorf("清理过期会话失败: %w", err)
	}
	return res.RowsAffected()
}
