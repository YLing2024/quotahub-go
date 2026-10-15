package auth

import (
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"log/slog"
	"time"

	"quotahub-go/internal/store"
	"quotahub-go/internal/timex"
)

// Session 是一次登录会话（token 为 64 位十六进制字符串）。
type Session struct {
	Token     string
	Username  string
	ExpiresAt string
}

// BootstrapResult 描述首启引导结果。
type BootstrapResult struct {
	// Created 为 true 表示本次新建了管理员。
	Created bool
	// Username 是管理员用户名。
	Username string
	// Password 仅在随机生成时非空（调用方只应打印一次，绝不落日志）。
	Password string
	// Generated 为 true 表示口令是随机生成的。
	Generated bool
}

// Accounts 是 builtin 认证的账号 / 会话服务。
type Accounts struct {
	db  *store.DB
	ttl time.Duration
}

// NewAccounts 构造账号服务。
func NewAccounts(db *store.DB, ttl time.Duration) *Accounts {
	return &Accounts{db: db, ttl: ttl}
}

// CleanupExpiredSessions 清理过期会话。
func (a *Accounts) CleanupExpiredSessions(ctx context.Context) (int64, error) {
	return a.db.DeleteExpiredSessions(ctx, timex.NowISO())
}

// BootstrapAdmin 首启引导：users 为空时创建管理员；已存在用户绝不覆盖口令。
func (a *Accounts) BootstrapAdmin(ctx context.Context, adminUser, adminPassword string) (BootstrapResult, error) {
	count, err := a.db.UserCount(ctx)
	if err != nil {
		return BootstrapResult{}, err
	}
	if count > 0 {
		return BootstrapResult{}, nil
	}
	username := adminUser
	password := adminPassword
	generated := false
	if password == "" {
		password, err = randomPassword()
		if err != nil {
			return BootstrapResult{}, err
		}
		generated = true
	}
	hash, err := HashPassword(password)
	if err != nil {
		return BootstrapResult{}, fmt.Errorf("生成口令哈希失败: %w", err)
	}
	if err := a.db.InsertUser(ctx, username, hash, timex.NowISO()); err != nil {
		return BootstrapResult{}, err
	}
	res := BootstrapResult{Created: true, Username: username, Generated: generated}
	if generated {
		res.Password = password
	}
	return res, nil
}

// CreateSession 新建会话并返回 token。
func (a *Accounts) CreateSession(ctx context.Context, username string) (Session, error) {
	token, err := randomHex(32)
	if err != nil {
		return Session{}, err
	}
	now := time.Now()
	expiresAt := timex.ISO(now.Add(a.ttl))
	if err := a.db.InsertSession(ctx, store.SessionRow{
		Token:     token,
		Username:  username,
		CreatedAt: timex.ISO(now),
		ExpiresAt: expiresAt,
	}); err != nil {
		return Session{}, err
	}
	return Session{Token: token, Username: username, ExpiresAt: expiresAt}, nil
}

// VerifySession 命中即通过并滑动续期；过期 / 不存在返回空串。
func (a *Accounts) VerifySession(ctx context.Context, token string) (string, error) {
	if token == "" {
		return "", nil
	}
	row, err := a.db.FindValidSession(ctx, token, timex.NowISO())
	if err != nil {
		return "", err
	}
	if row == nil {
		return "", nil
	}
	expiresAt := timex.ISO(time.Now().Add(a.ttl))
	if err := a.db.TouchSession(ctx, token, expiresAt); err != nil {
		return "", err
	}
	return row.Username, nil
}

// Login 校验账号口令并建立会话；失败返回 nil。
func (a *Accounts) Login(ctx context.Context, username, password string) (*Session, error) {
	user, err := a.db.FindUserByUsername(ctx, username)
	if err != nil {
		return nil, err
	}
	if user == nil {
		verifyDummy(password)
		return nil, nil
	}
	if !VerifyPassword(password, user.PasswordHash) {
		return nil, nil
	}
	session, err := a.CreateSession(ctx, user.Username)
	if err != nil {
		return nil, err
	}
	return &session, nil
}

// Logout 删除会话。
func (a *Accounts) Logout(ctx context.Context, token string) error {
	if token == "" {
		return nil
	}
	return a.db.DeleteSession(ctx, token)
}

// TTL 返回会话有效期。
func (a *Accounts) TTL() time.Duration { return a.ttl }

// LogCreatedAdmin 记录首启创建管理员的审计行（不含口令）。
func LogCreatedAdmin(logger *slog.Logger, username string) {
	logger.Info("[auth] 首次启动已创建管理员账号: " + username)
}

// randomHex 生成 n 字节随机数据的十六进制串。
func randomHex(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

// randomPassword 生成 12 字节随机口令（base64url，与旧实现一致）。
func randomPassword() (string, error) {
	b := make([]byte, 12)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}
