// Package httpapi 是管理面 HTTP 契约层：路由分发、请求/响应编解码与静态资源。
//
// 分发顺序逐条对齐旧实现 Express app：先 express.json()，再公开的 GET /api/auth-mode、
// 内置认证端点（sso 下 404），其余 /api/* 一律先鉴权（未登录 401、未知路由 404 在这之后）；
// 非 /api 路径走静态资源，命中不到回 Express 风格 404。路由顺序按 routeTable 在 routes.go 中登记。
package httpapi

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"mime"
	"net/http"
	"os"
	"path"
	"strings"

	"quotahub-go/internal/auth"
	"quotahub-go/internal/collect"
	"quotahub-go/internal/config"
	"quotahub-go/internal/httperr"
	"quotahub-go/internal/logs"
	"quotahub-go/internal/model"
	"quotahub-go/internal/platform"
	"quotahub-go/internal/presets"
	"quotahub-go/internal/settings"
	"quotahub-go/internal/transfer"
	"quotahub-go/internal/v8json"
)

// maxBodyBytes 是请求体上限（对齐 express.json 默认 100kb）。
const maxBodyBytes = 100 * 1024

// HistoryStore 是管理面历史查询所需的能力子集。
//
// 抽成接口是为了让 HTTP 契约测试可脱离真实数据库；生产实现是 *store.DB。
type HistoryStore interface {
	RemoveSamples(ctx context.Context, platformID string) error
	Samples(ctx context.Context, platformID string) ([]model.SamplePoint, error)
}

// Server 聚合管理面所需的全部依赖并承载分发。
type Server struct {
	cfg       *config.Config
	auth      *auth.Authenticator
	platforms *platform.Service
	presets   *presets.Service
	settings  *settings.Service
	logs      *logs.Service
	monitor   *collect.Monitor
	transfer  *transfer.Service
	history   HistoryStore
	assets    fs.FS
}

// Deps 是构造 Server 的依赖集合。
type Deps struct {
	Config    *config.Config
	Auth      *auth.Authenticator
	Platforms *platform.Service
	Presets   *presets.Service
	Settings  *settings.Service
	Logs      *logs.Service
	Monitor   *collect.Monitor
	Transfer  *transfer.Service
	History   HistoryStore
	// Assets 是内嵌前端产物根（web/dist）。
	Assets fs.FS
}

// New 构造管理面服务。
func New(d Deps) *Server {
	return &Server{
		cfg:       d.Config,
		auth:      d.Auth,
		platforms: d.Platforms,
		presets:   d.Presets,
		settings:  d.Settings,
		logs:      d.Logs,
		monitor:   d.Monitor,
		transfer:  d.Transfer,
		history:   d.History,
		assets:    d.Assets,
	}
}

// reqCtx 承载一次请求的解析结果与匹配参数。
type reqCtx struct {
	W      http.ResponseWriter
	R      *http.Request
	Body   any
	Params map[string]string
	User   string
}

// handlerFunc 是路由处理器：返回错误由分发层按状态码落成 `{error: ...}`。
type handlerFunc func(s *Server, c *reqCtx) error

// route 是一条路由登记；pattern 形如 "METHOD /path"，`:id` 为单段参数。
type route struct {
	pattern string
	// public 为真表示无需鉴权（仅 GET /api/auth-mode）。
	public bool
	// builtinOnly 为真表示仅 builtin 模式存在（sso 下 404）。
	builtinOnly bool
	handler     handlerFunc
}

// ServeHTTP 实现 http.Handler。
func (s *Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	body, err := decodeJSONBody(r)
	if err != nil {
		s.writeErr(w, err)
		return
	}
	c := &reqCtx{W: w, R: r, Body: body, Params: map[string]string{}}

	if !isAPIPath(r.URL.Path) {
		s.serveStatic(w, r)
		return
	}

	if rt, params, ok := matchRoute(routes, r.Method, r.URL.Path, func(rt route) bool { return rt.public }); ok {
		c.Params = params
		s.run(rt, c)
		return
	}

	if rt, params, ok := matchRoute(routes, r.Method, r.URL.Path, func(rt route) bool { return rt.builtinOnly }); ok {
		if s.cfg.AuthMode != config.AuthBuiltin {
			writeJSON(w, 404, map[string]any{"error": "未找到"})
			return
		}
		c.Params = params
		s.run(rt, c)
		return
	}

	user := s.auth.User(r)
	if user == "" {
		writeJSON(w, 401, map[string]any{"error": "未登录"})
		return
	}
	c.User = user

	if rt, params, ok := matchRoute(routes, r.Method, r.URL.Path, func(rt route) bool { return !rt.public && !rt.builtinOnly }); ok {
		c.Params = params
		s.run(rt, c)
		return
	}
	s.notFound(w, r)
}

// run 执行处理器并统一落错误响应。
func (s *Server) run(rt route, c *reqCtx) {
	if err := rt.handler(s, c); err != nil {
		s.writeErr(c.W, err)
	}
}

// writeErr 按 httperr 状态码落 `{error: msg}`；非业务错误一律 500（对齐 Express 统一错误处理）。
func (s *Server) writeErr(w http.ResponseWriter, err error) {
	status := http.StatusInternalServerError
	message := err.Error()
	var he *httperr.Error
	if errors.As(err, &he) {
		status = he.Status
		message = he.Message
	}
	if message == "" {
		message = "服务器内部错误"
	}
	writeJSON(w, status, map[string]any{"error": message})
}

// --- 路由匹配 ---

// matchRoute 按登记顺序找首个方法 + 路径都命中的路由，并抽取 `:param`。
func matchRoute(table []route, method, urlPath string, accept func(route) bool) (route, map[string]string, bool) {
	for _, rt := range table {
		if !accept(rt) {
			continue
		}
		patternMethod, patternPath := splitPattern(rt.pattern)
		if patternMethod != method {
			continue
		}
		if params, ok := matchPath(patternPath, urlPath); ok {
			return rt, params, true
		}
	}
	return route{}, nil, false
}

func splitPattern(pattern string) (string, string) {
	parts := strings.SplitN(pattern, " ", 2)
	return parts[0], parts[1]
}

// matchPath 逐段比较：`:name` 匹配任意单段，其余要求字面相等，段数必须一致。
func matchPath(patternPath, urlPath string) (map[string]string, bool) {
	want := splitSegments(patternPath)
	got := splitSegments(urlPath)
	if len(want) != len(got) {
		return nil, false
	}
	params := map[string]string{}
	for i := range want {
		if strings.HasPrefix(want[i], ":") {
			params[want[i][1:]] = got[i]
			continue
		}
		if want[i] != got[i] {
			return nil, false
		}
	}
	return params, true
}

// splitSegments 把路径切成非空段（忽略尾部斜杠差异，与 Express 类似）。
func splitSegments(p string) []string {
	out := make([]string, 0, 8)
	for _, seg := range strings.Split(p, "/") {
		if seg != "" {
			out = append(out, seg)
		}
	}
	return out
}

// isAPIPath 判断是否属于管理面 API（按段边界，避免 /apitest 被误判）。
func isAPIPath(p string) bool {
	return p == "/api" || strings.HasPrefix(p, "/api/")
}

// --- 请求体 / 响应体编解码 ---

// decodeJSONBody 预解析请求体（对齐 express.json）：仅 application/json 会解析；
// 空体按 `{}`、非法 JSON 按 400、超过 100kb 按 413。
func decodeJSONBody(r *http.Request) (any, error) {
	if !isJSONContentType(r.Header.Get("Content-Type")) {
		return map[string]any{}, nil
	}
	data, err := io.ReadAll(io.LimitReader(r.Body, maxBodyBytes+1))
	if err != nil {
		return nil, httperr.New(400, "读取请求体失败")
	}
	if len(data) > maxBodyBytes {
		return nil, httperr.New(413, "请求体过大")
	}
	if len(data) == 0 {
		return map[string]any{}, nil
	}
	text := string(data)
	// 先过 body-parser 的 strict 首字符校验：非 '{' / '[' 的输入（含全空白、`null` 等）
	// 在旧服务上就报语法错，不能落到 encoding/json 或各路由的字段校验上。
	if message := strictBodyParserError(text); message != "" {
		return nil, httperr.New(400, message)
	}
	var v any
	if err := json.Unmarshal(data, &v); err != nil {
		message := v8json.ErrorMessage(text)
		if message == "" {
			// 理论上不该发生（Go 报错而 V8 判合法）；退化为 Go 原文，避免空消息。
			message = err.Error()
		}
		return nil, httperr.New(400, message)
	}
	if v == nil {
		return map[string]any{}, nil
	}
	return v, nil
}

// isJSONContentType 判断 Content-Type 是否为 application/json（允许带 charset 等参数）。
func isJSONContentType(ct string) bool {
	if ct == "" {
		return false
	}
	mediaType, _, err := mime.ParseMediaType(ct)
	if err != nil {
		return false
	}
	return mediaType == "application/json"
}

// writeJSON 以 Express 风格落 JSON：不转义 HTML、无多余空白、带 charset。
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := marshalJSON(v)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"服务器内部错误"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// marshalJSON 序列化但保留 `<`/`>`/`&` 原样（JSON.stringify 口径）。
func marshalJSON(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// --- 静态资源 ---

// staticFS 返回静态资源根：配置了外部目录用目录，否则用内嵌产物。
func (s *Server) staticFS() fs.FS {
	if s.cfg.HasStaticDir {
		return os.DirFS(s.cfg.StaticDir)
	}
	return s.assets
}

// serveStatic 服务前端产物（对齐 express.static：仅 GET/HEAD，命中不到回 404）。
func (s *Server) serveStatic(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		s.notFound(w, r)
		return
	}
	root := s.staticFS()
	if root == nil {
		s.notFound(w, r)
		return
	}
	name := strings.TrimPrefix(path.Clean("/"+r.URL.Path), "/")
	if name == "" {
		name = "index.html"
	}
	file, err := root.Open(name)
	if err != nil {
		s.notFound(w, r)
		return
	}
	defer file.Close()
	info, err := file.Stat()
	if err != nil || info.IsDir() {
		s.notFound(w, r)
		return
	}
	contentType := mime.TypeByExtension(path.Ext(name))
	if contentType == "" {
		contentType = "application/octet-stream"
	}
	w.Header().Set("Content-Type", contentType)
	w.Header().Set("Cache-Control", "public, max-age=0")
	if seeker, ok := file.(io.ReadSeeker); ok {
		http.ServeContent(w, r, name, info.ModTime(), seeker)
		return
	}
	_, _ = io.Copy(w, file)
}

// notFound 输出 Express 风格的 404（`Cannot GET /path` HTML）。
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	body := fmt.Sprintf("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<title>Error</title>\n</head>\n<body>\n<pre>Cannot %s %s</pre>\n</body>\n</html>\n", r.Method, r.URL.Path)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusNotFound)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body)
	}
}
