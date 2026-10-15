package httpapi

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
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
	server   *httptest.Server
	client   *http.Client
	mode     config.AuthMode
	accounts *auth.Accounts
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
	accounts := auth.NewAccounts(db, time.Hour)

	srv := New(Deps{
		Config:    cfg,
		Auth:      auth.NewAuthenticator(mode, accounts),
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
	return &testEnv{server: ts, client: &http.Client{}, mode: mode, accounts: accounts}
}

// loginCookie 首启建管理员并登录，返回可直接放进 Cookie 头的会话串（仅 builtin）。
func (e *testEnv) loginCookie(t *testing.T, password string) string {
	t.Helper()
	if _, err := e.accounts.BootstrapAdmin(context.Background(), "admin", password); err != nil {
		t.Fatalf("引导管理员失败: %v", err)
	}
	raw, _ := json.Marshal(map[string]any{"username": "admin", "password": password})
	res, err := e.client.Post(e.server.URL+"/api/auth/login", "application/json", strings.NewReader(string(raw)))
	if err != nil {
		t.Fatalf("登录失败: %v", err)
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		t.Fatalf("登录应 200，得到 %d", res.StatusCode)
	}
	for _, c := range res.Header.Values("Set-Cookie") {
		if strings.HasPrefix(c, "quotahub_session=") {
			return strings.SplitN(c, ";", 2)[0]
		}
	}
	t.Fatal("响应缺少 quotahub_session cookie")
	return ""
}

// doFull 发一次请求并返回状态码、响应头与原始响应体（用于逐字节核对）。
func (e *testEnv) doFull(t *testing.T, method, path string, body []byte, headers map[string]string) (int, http.Header, []byte) {
	t.Helper()
	var reader io.Reader
	if body != nil {
		reader = strings.NewReader(string(body))
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
	return res.StatusCode, res.Header, data
}

// authHeaders 返回该模式下「已登录」的最小请求头（sso 用 X-Auth-User，builtin 用会话 cookie）。
func (e *testEnv) authHeaders(t *testing.T, cookie string) map[string]string {
	t.Helper()
	if e.mode == config.AuthSSO {
		return map[string]string{"X-Auth-User": "alice"}
	}
	return map[string]string{"Cookie": cookie}
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

// expressHTMLError 是 Express finalhandler 的 404 页模板（与旧实现逐字节一致）。
func expressHTMLError(method, path string) string {
	return "<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<title>Error</title>\n</head>\n<body>\n<pre>Cannot " + method + " " + path + "</pre>\n</body>\n</html>\n"
}

// TestAutomaticOptionsMatrix 覆盖 Express 自动 OPTIONS：Allow 与响应体同为排序去重后的方法串。
func TestAutomaticOptionsMatrix(t *testing.T) {
	for _, mode := range []config.AuthMode{config.AuthBuiltin, config.AuthSSO} {
		env := newTestEnv(t, mode)
		cookie := ""
		if mode == config.AuthBuiltin {
			cookie = env.loginCookie(t, "pw")
		}
		headers := env.authHeaders(t, cookie)
		cases := []struct {
			path string
			want string
		}{
			{"/api/platforms", "GET, HEAD, POST"},
			{"/api/presets", "GET, HEAD, POST"},
			{"/api/settings", "GET, HEAD, PUT"},
			{"/api/logs", "DELETE, GET, HEAD"},
			{"/api/export", "GET, HEAD"},
			{"/api/import", "POST"},
			{"/api/auth-mode", "GET, HEAD"},
			{"/api/platforms/reorder", "DELETE, PUT"},
			{"/api/platforms/balances", "DELETE, GET, HEAD, PUT"},
		}
		for _, tc := range cases {
			status, hdr, body := env.doFull(t, "OPTIONS", tc.path, nil, headers)
			if status != 200 {
				t.Fatalf("[%s] OPTIONS %s 应 200，得到 %d", mode, tc.path, status)
			}
			if ct := hdr.Get("Content-Type"); ct != "text/plain" {
				t.Fatalf("[%s] OPTIONS %s Content-Type 应为 text/plain，得到 %q", mode, tc.path, ct)
			}
			if got := hdr.Get("Allow"); got != tc.want {
				t.Fatalf("[%s] OPTIONS %s Allow 应为 %q，得到 %q", mode, tc.path, tc.want, got)
			}
			if string(body) != tc.want {
				t.Fatalf("[%s] OPTIONS %s 响应体应为 %q，得到 %q", mode, tc.path, tc.want, string(body))
			}
			if cl := hdr.Get("Content-Length"); cl != strconv.Itoa(len(tc.want)) {
				t.Fatalf("[%s] OPTIONS %s Content-Length 应为 %d，得到 %q", mode, tc.path, len(tc.want), cl)
			}
		}
	}
}

// TestMethodFallbackAuthOrdering 覆盖「先鉴权，再方法回退」：未登录任何 /api/* 先 401。
func TestMethodFallbackAuthOrdering(t *testing.T) {
	for _, mode := range []config.AuthMode{config.AuthBuiltin, config.AuthSSO} {
		env := newTestEnv(t, mode)
		for _, tc := range []struct{ method, path string }{
			{"OPTIONS", "/api/platforms"},
			{"HEAD", "/api/platforms"},
			{"PATCH", "/api/platforms"},
			{"OPTIONS", "/api/nope"},
			{"HEAD", "/api/nope"},
		} {
			status, hdr, body := env.doFull(t, tc.method, tc.path, nil, nil)
			if status != 401 || hdr.Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("[%s] 未登录 %s %s 应 401 JSON，得到 %d %q", mode, tc.method, tc.path, status, hdr.Get("Content-Type"))
			}
			// HEAD 无响应体，但 Content-Length 与 GET 一致，证明走的是同一条 JSON 出口。
			if tc.method == http.MethodHead {
				if len(body) != 0 || hdr.Get("Content-Length") != "21" {
					t.Fatalf("[%s] 未登录 HEAD %s 应为空体且 Content-Length 21，得到 %d %q", mode, tc.path, len(body), hdr.Get("Content-Length"))
				}
				continue
			}
			if string(body) != `{"error":"未登录"}` {
				t.Fatalf("[%s] 未登录 %s %s 响应体异常：%s", mode, tc.method, tc.path, body)
			}
		}
	}
}

// TestMethodFallbackNotAllowedIsHTML404 覆盖「路径在、方法不允许」落 Express HTML 404（不是 405）。
func TestMethodFallbackNotAllowedIsHTML404(t *testing.T) {
	env := newTestEnv(t, config.AuthSSO)
	headers := env.authHeaders(t, "")
	for _, tc := range []struct{ method, path string }{
		{"PATCH", "/api/platforms"},
		{"PUT", "/api/platforms"},
		{"DELETE", "/api/platforms"},
		{"GET", "/api/nope"},
		{"POST", "/api/foo/bar"},
		{"OPTIONS", "/api/nope"},
	} {
		status, hdr, body := env.doFull(t, tc.method, tc.path, nil, headers)
		if status != 404 {
			t.Fatalf("%s %s 应 404，得到 %d", tc.method, tc.path, status)
		}
		if ct := hdr.Get("Content-Type"); ct != "text/html; charset=utf-8" {
			t.Fatalf("%s %s Content-Type 应为 text/html; charset=utf-8，得到 %q", tc.method, tc.path, ct)
		}
		want := expressHTMLError(tc.method, tc.path)
		if string(body) != want {
			t.Fatalf("%s %s 响应体与 Express 404 页不一致：\n得到 %q\n期望 %q", tc.method, tc.path, body, want)
		}
		if cl := hdr.Get("Content-Length"); cl != strconv.Itoa(len(want)) {
			t.Fatalf("%s %s Content-Length 应为 %d，得到 %q", tc.method, tc.path, len(want), cl)
		}
	}
}

// TestHeadRunsGetHandler 覆盖 HEAD 走 GET 处理器（状态码/头同 GET，无响应体）。
func TestHeadRunsGetHandler(t *testing.T) {
	env := newTestEnv(t, config.AuthSSO)
	headers := env.authHeaders(t, "")

	status, hdr, body := env.doFull(t, "HEAD", "/api/platforms", nil, headers)
	if status != 200 {
		t.Fatalf("HEAD /api/platforms 应 200，得到 %d", status)
	}
	if ct := hdr.Get("Content-Type"); ct != "application/json; charset=utf-8" {
		t.Fatalf("HEAD /api/platforms Content-Type 异常：%q", ct)
	}
	if len(body) != 0 {
		t.Fatalf("HEAD /api/platforms 不应有响应体，得到 %q", body)
	}

	// HEAD 命中 GET 之外的路由不存在 -> Express HTML 404（无体）。
	status, hdr, body = env.doFull(t, "HEAD", "/api/import", nil, headers)
	if status != 404 || hdr.Get("Content-Type") != "text/html; charset=utf-8" || len(body) != 0 {
		t.Fatalf("HEAD /api/import 应 404 HTML 空体，得到 %d %q %q", status, hdr.Get("Content-Type"), body)
	}
}

// TestSSOBuiltinOnlyGuardCoversAllMethods 覆盖 sso 下 /api/auth 守卫对任意方法/子路径生效。
func TestSSOBuiltinOnlyGuardCoversAllMethods(t *testing.T) {
	env := newTestEnv(t, config.AuthSSO)
	headers := env.authHeaders(t, "")
	for _, path := range []string{"/api/auth/me", "/api/auth/login", "/api/auth/nope"} {
		for _, method := range []string{"OPTIONS", "HEAD", "GET", "POST", "PATCH", "PUT", "DELETE"} {
			status, hdr, body := env.doFull(t, method, path, nil, headers)
			if status != 404 || hdr.Get("Content-Type") != "application/json; charset=utf-8" {
				t.Fatalf("sso %s %s 应 404 JSON，得到 %d %q", method, path, status, hdr.Get("Content-Type"))
			}
			if method == http.MethodHead {
				if len(body) != 0 || hdr.Get("Content-Length") != "21" {
					t.Fatalf("sso HEAD %s 应为空体且 Content-Length 21，得到 %d %q", path, len(body), hdr.Get("Content-Length"))
				}
				continue
			}
			if string(body) != `{"error":"未找到"}` {
				t.Fatalf("sso %s %s 响应体异常：%s", method, path, body)
			}
		}
	}
}
