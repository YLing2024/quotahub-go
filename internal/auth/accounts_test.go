package auth

import (
	"context"
	"testing"
	"time"

	"quotahub-go/internal/store"
)

// fakeAccounts 是 AccountStore 的内存实现，用于在不依赖数据库的情况下测试认证逻辑。
type fakeAccounts struct {
	users    map[string]store.UserRow
	sessions map[string]store.SessionRow
	nextID   int64
}

func newFakeAccounts() *fakeAccounts {
	return &fakeAccounts{users: map[string]store.UserRow{}, sessions: map[string]store.SessionRow{}}
}

func (f *fakeAccounts) DeleteExpiredSessions(_ context.Context, nowIso string) (int64, error) {
	var n int64
	for token, s := range f.sessions {
		if s.ExpiresAt <= nowIso {
			delete(f.sessions, token)
			n++
		}
	}
	return n, nil
}

func (f *fakeAccounts) UserCount(context.Context) (int64, error) { return int64(len(f.users)), nil }

func (f *fakeAccounts) InsertUser(_ context.Context, username, passwordHash, createdAt string) error {
	f.nextID++
	f.users[username] = store.UserRow{ID: f.nextID, Username: username, PasswordHash: passwordHash, CreatedAt: createdAt}
	return nil
}

func (f *fakeAccounts) InsertSession(_ context.Context, row store.SessionRow) error {
	f.sessions[row.Token] = row
	return nil
}

func (f *fakeAccounts) FindValidSession(_ context.Context, token, nowIso string) (*store.SessionRow, error) {
	s, ok := f.sessions[token]
	if !ok || s.ExpiresAt <= nowIso {
		return nil, nil
	}
	return &s, nil
}

func (f *fakeAccounts) TouchSession(_ context.Context, token, expiresAt string) error {
	if s, ok := f.sessions[token]; ok {
		s.ExpiresAt = expiresAt
		f.sessions[token] = s
	}
	return nil
}

func (f *fakeAccounts) FindUserByUsername(_ context.Context, username string) (*store.UserRow, error) {
	u, ok := f.users[username]
	if !ok {
		return nil, nil
	}
	return &u, nil
}

func (f *fakeAccounts) DeleteSession(_ context.Context, token string) error {
	delete(f.sessions, token)
	return nil
}

func TestBootstrapCreatesAdminOnce(t *testing.T) {
	ctx := context.Background()
	a := NewAccounts(newFakeAccounts(), time.Hour)

	first, err := a.BootstrapAdmin(ctx, "admin", "")
	if err != nil || !first.Created || !first.Generated || first.Password == "" {
		t.Fatalf("首启应创建管理员并生成口令: %+v err=%v", first, err)
	}
	second, err := a.BootstrapAdmin(ctx, "admin", "another")
	if err != nil {
		t.Fatalf("二次引导报错: %v", err)
	}
	if second.Created {
		t.Fatal("已有账号时不应重复创建")
	}
}

func TestLoginVerifyAndLogout(t *testing.T) {
	ctx := context.Background()
	a := NewAccounts(newFakeAccounts(), time.Hour)
	if _, err := a.BootstrapAdmin(ctx, "admin", "s3cret-pass"); err != nil {
		t.Fatalf("引导失败: %v", err)
	}

	if bad, err := a.Login(ctx, "admin", "wrong"); err != nil || bad != nil {
		t.Fatalf("错误口令应登录失败: %+v err=%v", bad, err)
	}
	if missing, err := a.Login(ctx, "nobody", "x"); err != nil || missing != nil {
		t.Fatalf("不存在账号应登录失败: %+v err=%v", missing, err)
	}

	sess, err := a.Login(ctx, "admin", "s3cret-pass")
	if err != nil || sess == nil {
		t.Fatalf("正确口令应登录成功: %v", err)
	}
	user, err := a.VerifySession(ctx, sess.Token)
	if err != nil || user != "admin" {
		t.Fatalf("会话应命中 admin: user=%q err=%v", user, err)
	}
	if err := a.Logout(ctx, sess.Token); err != nil {
		t.Fatalf("登出失败: %v", err)
	}
	if after, _ := a.VerifySession(ctx, sess.Token); after != "" {
		t.Fatalf("登出后不应命中，得到 %q", after)
	}
}

// TestVerifySessionSlidingRenewal 验证命中会话会滑动续期（过期时间被推后）。
func TestVerifySessionSlidingRenewal(t *testing.T) {
	ctx := context.Background()
	fake := newFakeAccounts()
	a := NewAccounts(fake, time.Hour)
	if _, err := a.BootstrapAdmin(ctx, "admin", "pw"); err != nil {
		t.Fatalf("引导失败: %v", err)
	}
	sess, err := a.Login(ctx, "admin", "pw")
	if err != nil || sess == nil {
		t.Fatalf("登录失败: %v", err)
	}
	before := fake.sessions[sess.Token].ExpiresAt
	// 让续期时间明显推后：直接把 TTL 视为 2 小时后再验证。
	a.ttl = 2 * time.Hour
	if _, err := a.VerifySession(ctx, sess.Token); err != nil {
		t.Fatalf("验证失败: %v", err)
	}
	after := fake.sessions[sess.Token].ExpiresAt
	if after <= before {
		t.Fatalf("启用续期后过期时间应推后: before=%s after=%s", before, after)
	}
}

func TestVerifyEmptyToken(t *testing.T) {
	a := NewAccounts(newFakeAccounts(), time.Hour)
	if user, err := a.VerifySession(context.Background(), ""); err != nil || user != "" {
		t.Fatalf("空 token 应直接返回空: user=%q err=%v", user, err)
	}
}
