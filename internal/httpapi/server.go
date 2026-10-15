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
	"sort"
	"strconv"
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
//
// 分发严格对齐旧实现 Express app 的层序：express.json() → 公开的 GET /api/auth-mode →
// 内置认证端点（含 sso 守卫）→ /api 鉴权中间件 → 业务路由 → Express 默认 404。
// 鉴权中间件排在业务路由之前，因此「方法回退」（HEAD 走 GET 处理器、OPTIONS 自动汇总
// Allow）发生在鉴权之后：未登录时任何 /api/* 都先落 401。
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

	// /api/auth 挂在 /api 鉴权中间件之前，其 builtinOnly 守卫对任意方法/子路径生效；
	// 命中路由或自动 OPTIONS 也在鉴权之前收尾。
	if isAuthAPIPath(r.URL.Path) {
		if s.cfg.AuthMode != config.AuthBuiltin {
			writeJSON(w, 404, map[string]any{"error": "未找到"})
			return
		}
		if s.dispatch(w, r, c, onlyBuiltin) {
			return
		}
		if r.Method == http.MethodOptions && s.dispatchOptions(w, r, onlyBuiltin) {
			return
		}
		// 未命中：继续走 /api 鉴权（对齐 Express 层序）。
	} else if r.URL.Path == publicAuthModePath && isReadMethod(r.Method) {
		// 公开路由只对 GET/HEAD 免鉴权；OPTIONS 及其余方法继续走鉴权与自动 OPTIONS。
		if s.dispatch(w, r, c, onlyPublic) {
			return
		}
	}

	// 其余 /api/*：先鉴权（未登录一律 401），再做方法回退与 404 判定。
	user := s.auth.User(r)
	if user == "" {
		writeJSON(w, 401, map[string]any{"error": "未登录"})
		return
	}
	c.User = user

	if s.dispatch(w, r, c, onlyAuthed) {
		return
	}
	if r.Method == http.MethodOptions && s.dispatchOptions(w, r, nil) {
		return
	}
	s.notFound(w, r)
}

// publicAuthModePath 是唯一免鉴权的公开路由路径。
const publicAuthModePath = "/api/auth-mode"

// isReadMethod 判断是否为读取方法（GET/HEAD）；公开路由与静态资源都只认这两种。
func isReadMethod(method string) bool {
	return method == http.MethodGet || method == http.MethodHead
}

// dispatch 在给定分组内做「方法 + 路径」匹配；命中即执行处理器并返回 true。
// HEAD 会回退到 GET 处理器（对齐 Express route 的 head→get 语义）。
func (s *Server) dispatch(w http.ResponseWriter, r *http.Request, c *reqCtx, accept func(route) bool) bool {
	rt, params, ok := matchRoute(routes, r.Method, r.URL.Path, accept)
	if !ok {
		return false
	}
	c.Params = params
	s.run(rt, c)
	return true
}

// dispatchOptions 处理 Express 的自动 OPTIONS：汇总该分组内所有「路径匹配」路由允许的方法，
// 非空则落 200 文本响应并返回 true，否则留给上层继续（返回 false）。
func (s *Server) dispatchOptions(w http.ResponseWriter, r *http.Request, accept func(route) bool) bool {
	methods := allowedMethods(routes, r.URL.Path, accept)
	if len(methods) == 0 {
		return false
	}
	writeOptions(w, methods)
	return true
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

// 路由分组谓词：对应 Express 里三类挂载顺序（公开 / 内置认证 / 其余鉴权）。
func onlyPublic(rt route) bool  { return rt.public }
func onlyBuiltin(rt route) bool { return rt.builtinOnly }
func onlyAuthed(rt route) bool  { return !rt.public && !rt.builtinOnly }

// matchRoute 按登记顺序找首个方法 + 路径都命中的路由，并抽取 `:param`。
func matchRoute(table []route, method, urlPath string, accept func(route) bool) (route, map[string]string, bool) {
	for _, rt := range table {
		if !accept(rt) {
			continue
		}
		patternMethod, patternPath := splitPattern(rt.pattern)
		if !methodMatches(patternMethod, method) {
			continue
		}
		if params, ok := matchPath(patternPath, urlPath); ok {
			return rt, params, true
		}
	}
	return route{}, nil, false
}

// methodMatches 判断请求方法是否命中某条路由登记的方法。
//
// 对齐 Express：HEAD 请求回退到 GET 处理器（route._handlesMethod 把 head 归一为 get）。
func methodMatches(patternMethod, requestMethod string) bool {
	if patternMethod == requestMethod {
		return true
	}
	return requestMethod == http.MethodHead && patternMethod == http.MethodGet
}

// allowedMethods 汇总路径匹配的全部路由允许的方法，去重后按字母序排序。
//
// 对齐 Express 自动 OPTIONS：逐个匹配到的 Route 取其注册方法，并为存在 GET 的路由补上 HEAD；
// 最终在 sendOptionsResponse 里去重、排序（所以 `Allow` 与响应体同串，形如 `GET, HEAD, POST`）。
// accept 为 nil 表示不限分组。
func allowedMethods(table []route, urlPath string, accept func(route) bool) []string {
	if accept == nil {
		accept = func(route) bool { return true }
	}
	set := map[string]struct{}{}
	for _, rt := range table {
		if !accept(rt) {
			continue
		}
		patternMethod, patternPath := splitPattern(rt.pattern)
		if _, ok := matchPath(patternPath, urlPath); !ok {
			continue
		}
		set[patternMethod] = struct{}{}
		if patternMethod == http.MethodGet {
			set[http.MethodHead] = struct{}{}
		}
	}
	out := make([]string, 0, len(set))
	for m := range set {
		out = append(out, m)
	}
	sort.Strings(out)
	return out
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

// isAuthAPIPath 判断是否属于内置认证挂载 /api/auth（按段边界，`/api/auth-mode` 不算）。
func isAuthAPIPath(p string) bool {
	return p == "/api/auth" || strings.HasPrefix(p, "/api/auth/")
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

// writeJSON 以 Express 风格落 JSON：不转义 HTML、无多余空白、带 charset 与 Content-Length。
func writeJSON(w http.ResponseWriter, status int, v any) {
	body, err := marshalJSON(v)
	if err != nil {
		w.Header().Set("Content-Type", "application/json; charset=utf-8")
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"服务器内部错误"}`))
		return
	}
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(status)
	_, _ = w.Write(body)
}

// writeOptions 落 Express 自动 OPTIONS 响应：`Content-Type: text/plain`、`Allow` 头与
// 响应体同为 `methods` 串（逗号 + 空格分隔、按字母序），并显式给出 `Content-Length`。
func writeOptions(w http.ResponseWriter, methods []string) {
	allow := strings.Join(methods, ", ")
	w.Header().Set("Allow", allow)
	w.Header().Set("Content-Type", "text/plain")
	w.Header().Set("Content-Length", strconv.Itoa(len(allow)))
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusOK)
	_, _ = io.WriteString(w, allow)
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

// notFound 输出 Express 风格的 404（finalhandler 的 `Cannot METHOD /path` HTML 页）。
func (s *Server) notFound(w http.ResponseWriter, r *http.Request) {
	body := fmt.Sprintf("<!DOCTYPE html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n<title>Error</title>\n</head>\n<body>\n<pre>Cannot %s %s</pre>\n</body>\n</html>\n", r.Method, escapeHTML(r.URL.Path))
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.Header().Set("Content-Length", strconv.Itoa(len(body)))
	w.Header().Set("Content-Security-Policy", "default-src 'none'")
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.WriteHeader(http.StatusNotFound)
	if r.Method != http.MethodHead {
		_, _ = io.WriteString(w, body)
	}
}

// escapeHTML 复刻 finalhandler 所用 escape-html 的实体转义（& < > " '）。
func escapeHTML(s string) string {
	return strings.NewReplacer(
		"&", "&amp;",
		"<", "&lt;",
		">", "&gt;",
		`"`, "&quot;",
		"'", "&#39;",
	).Replace(s)
}
