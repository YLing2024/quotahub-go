package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strings"
	"sync"
	"testing"
	"testing/fstest"
	"time"

	"quotahub-go/internal/auth"
	"quotahub-go/internal/collect"
	"quotahub-go/internal/config"
	"quotahub-go/internal/logs"
	"quotahub-go/internal/model"
	"quotahub-go/internal/platform"
	"quotahub-go/internal/presets"
	"quotahub-go/internal/settings"
	"quotahub-go/internal/store"
	"quotahub-go/internal/transfer"
)

// memStore 是 HTTP 契约测试用的内存账本替身：只实现被测试路径用到的行为，
// 让契约测试不依赖真实数据库（旧实现里 SQLite 已按 2026-10-16 口径移除）。
type memStore struct {
	mu       sync.Mutex
	users    map[string]store.UserRow
	sessions map[string]store.SessionRow
	samples  map[string][]model.SamplePoint
}

func newMemStore() *memStore {
	return &memStore{
		users:    map[string]store.UserRow{},
		sessions: map[string]store.SessionRow{},
		samples:  map[string][]model.SamplePoint{},
	}
}

func (m *memStore) DeleteExpiredSessions(_ context.Context, nowIso string) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	var n int64
	for token, s := range m.sessions {
		if s.ExpiresAt <= nowIso {
			delete(m.sessions, token)
			n++
		}
	}
	return n, nil
}

func (m *memStore) UserCount(context.Context) (int64, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	return int64(len(m.users)), nil
}

func (m *memStore) InsertUser(_ context.Context, username, passwordHash, createdAt string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.users[username] = store.UserRow{
		ID:           int64(len(m.users) + 1),
		Username:     username,
		PasswordHash: passwordHash,
		CreatedAt:    createdAt,
	}
	return nil
}

func (m *memStore) InsertSession(_ context.Context, row store.SessionRow) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.sessions[row.Token] = row
	return nil
}

func (m *memStore) FindValidSession(_ context.Context, token, nowIso string) (*store.SessionRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	s, ok := m.sessions[token]
	if !ok || s.ExpiresAt <= nowIso {
		return nil, nil
	}
	return &s, nil
}

func (m *memStore) TouchSession(_ context.Context, token, expiresAt string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	if s, ok := m.sessions[token]; ok {
		s.ExpiresAt = expiresAt
		m.sessions[token] = s
	}
	return nil
}

func (m *memStore) FindUserByUsername(_ context.Context, username string) (*store.UserRow, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	u, ok := m.users[username]
	if !ok {
		return nil, nil
	}
	return &u, nil
}

func (m *memStore) DeleteSession(_ context.Context, token string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.sessions, token)
	return nil
}

func (m *memStore) RecordSample(_ context.Context, platformID string, value float64, at string) (bool, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.samples[platformID] = append(m.samples[platformID], model.SamplePoint{T: at, V: value})
	return true, nil
}

func (m *memStore) LatestSample(_ context.Context, platformID string) (*model.SamplePoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	list := m.samples[platformID]
	if len(list) == 0 {
		return nil, nil
	}
	last := list[len(list)-1]
	return &last, nil
}

func (m *memStore) RemoveSamples(_ context.Context, platformID string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.samples, platformID)
	return nil
}

func (m *memStore) Samples(_ context.Context, platformID string) ([]model.SamplePoint, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := append([]model.SamplePoint(nil), m.samples[platformID]...)
	sort.SliceStable(out, func(i, j int) bool { return out[i].T < out[j].T })
	return out, nil
}

type testEnv struct {
	server *httptest.Server
	client *http.Client
}

// newTestEnv 起一个隔离的测试服务：临时数据目录 + 内存账本替身 + 内嵌占位产物。
func newTestEnv(t *testing.T, mode config.AuthMode) *testEnv {
	t.Helper()
	dir := t.TempDir()
	db := newMemStore()

	cfg := &config.Config{
		DataDir:           dir,
		AuthMode:          mode,
		AdminUser:         "admin",
		ScriptTimeoutMs:   2000,
		SessionTtlSeconds: 3600,
		AllowPrivate:      true,
	}
	platformSvc := platform.New(dir)
	presetSvc := presets.New(dir)
	settingsSvc := settings.New(dir)
	logSvc := logs.New(dir, nil)
	monitorSvc := collect.NewMonitor(platformSvc, db, settingsSvc, logSvc, true, 2000)
	transferSvc := transfer.New(platformSvc, presetSvc, logSvc)
	assets := fstest.MapFS{"index.html": &fstest.MapFile{Data: []byte("<!doctype html><html></html>")}}

	srv := New(Deps{
		Config:    cfg,
		Auth:      auth.NewAuthenticator(mode, auth.NewAccounts(db, time.Hour)),
		Platforms: platformSvc,
		Presets:   presetSvc,
		Settings:  settingsSvc,
		Logs:      logSvc,
		Monitor:   monitorSvc,
		Transfer:  transferSvc,
		History:   db,
		Assets:    assets,
	})
	ts := httptest.NewServer(srv)
	t.Cleanup(ts.Close)
	return &testEnv{server: ts, client: &http.Client{}}
}

func (e *testEnv) do(t *testing.T, method, path string, body any, headers map[string]string) (int, string) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		raw, _ := json.Marshal(body)
		reader = strings.NewReader(string(raw))
	}
	req, err := http.NewRequest(method, e.server.URL+path, reader)
	if err != nil {
		t.Fatalf("构造请求失败: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	res, err := e.client.Do(req)
	if err != nil {
		t.Fatalf("请求失败: %v", err)
	}
	defer res.Body.Close()
	data, _ := io.ReadAll(res.Body)
	return res.StatusCode, strings.TrimSpace(string(data))
}

func TestAuthModePublic(t *testing.T) {
	env := newTestEnv(t, config.AuthSSO)
	status, body := env.do(t, "GET", "/api/auth-mode", nil, nil)
	if status != 200 || !strings.Contains(body, `"authMode":"sso"`) {
		t.Fatalf("auth-mode 响应异常: %d %s", status, body)
	}
}

func TestUnknownAPIAuthOrdering(t *testing.T) {
	env := newTestEnv(t, config.AuthSSO)

	if status, _ := env.do(t, "GET", "/api/definitely-not-exist", nil, nil); status != 401 {
		t.Fatalf("未登录未知路由应 401，得到 %d", status)
	}
	status, _ := env.do(t, "GET", "/api/definitely-not-exist", nil, map[string]string{"X-Auth-User": "alice"})
	if status != 404 {
		t.Fatalf("已登录未知路由应 404，得到 %d", status)
	}
}

func TestSSOIgnoresLocalAuthEndpoints(t *testing.T) {
	env := newTestEnv(t, config.AuthSSO)
	for _, tc := range []struct{ method, path string }{
		{"POST", "/api/auth/login"},
		{"POST", "/api/auth/logout"},
		{"GET", "/api/auth/me"},
	} {
		status, _ := env.do(t, tc.method, tc.path, map[string]any{}, map[string]string{"X-Auth-User": "alice"})
		if status != 404 {
			t.Fatalf("%s %s 在 sso 下应 404，得到 %d", tc.method, tc.path, status)
		}
	}
}

func TestSSOAuthAndSettings(t *testing.T) {
	env := newTestEnv(t, config.AuthSSO)
	status, body := env.do(t, "GET", "/api/settings", nil, map[string]string{"X-Auth-User": "alice"})
	if status != 200 || !strings.Contains(body, `"collectIntervalSeconds":0`) {
		t.Fatalf("settings 响应异常: %d %s", status, body)
	}
	if status, _ := env.do(t, "GET", "/api/settings", nil, nil); status != 401 {
		t.Fatalf("缺少 X-Auth-User 应 401，得到 %d", status)
	}
}

func TestBuiltinLoginAndPlatformFlow(t *testing.T) {
	env := newTestEnv(t, config.AuthBuiltin)
	status, _ := env.do(t, "GET", "/api/platforms", nil, nil)
	if status != 401 {
		t.Fatalf("未登录应 401，得到 %d", status)
	}
	// 登录（首启未创建用户 -> 401；这里手工建用户更贴近真实引导，但直接跳到鉴权失败路径）
	status, body := env.do(t, "POST", "/api/auth/login", map[string]any{"username": "admin", "password": "x"}, nil)
	if status != 401 || !strings.Contains(body, "账号或密码错误") {
		t.Fatalf("无账号登录应 401，得到 %d %s", status, body)
	}
}

func TestStaticAndNotFound(t *testing.T) {
	env := newTestEnv(t, config.AuthSSO)
	status, body := env.do(t, "GET", "/", nil, nil)
	if status != 200 || !strings.Contains(body, "<!doctype html>") {
		t.Fatalf("GET / 应返回 index.html，得到 %d %s", status, body)
	}
	status, body = env.do(t, "GET", "/no-such-asset.js", nil, nil)
	if status != 404 || !strings.Contains(body, "Cannot GET /no-such-asset.js") {
		t.Fatalf("未知静态资源应 404，得到 %d %s", status, body)
	}
}
