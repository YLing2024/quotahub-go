package collect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"quotahub-go/internal/jsval"
)

// RequestTimeoutMs 是单次平台抓取的超时（与旧实现 TIMEOUT_MS 一致）。
const RequestTimeoutMs = 15000

// maxResponseBytes 是响应体上限：防止恶意服务器返回超大 JS/JSON 造成内存 DoS。
const maxResponseBytes = 1024 * 1024

// FetchTarget 是抓取目标：平台或 validate 接口的任意松散配置。
//
// request/response 保持松散对象形态（键集合不固定，与旧实现原样展开用户配置一致）。
type FetchTarget struct {
	Request   map[string]any
	Handler   string
	Extractor string
	Parse     string
	Response  map[string]any
}

// Fetcher 执行一次平台余额抓取：SSRF 校验 → HTTP 请求 → 沙箱解析/提取。
type Fetcher struct {
	client       *http.Client
	sandbox      *Sandbox
	allowPrivate bool
}

// NewFetcher 构造抓取器。allowPrivate 对应 QUOTAHUB_ALLOW_PRIVATE=1。
func NewFetcher(allowPrivate bool, scriptTimeoutMs int) *Fetcher {
	transport := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   10 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   10,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
	return &Fetcher{
		client: &http.Client{
			Transport: transport,
			Timeout:   time.Duration(RequestTimeoutMs) * time.Millisecond,
			// node:fetch 默认最多跟随 20 次重定向，超出后报错。
			CheckRedirect: func(_ *http.Request, via []*http.Request) error {
				if len(via) >= 20 {
					return errors.New("stopped after 20 redirects")
				}
				return nil
			},
		},
		sandbox:      NewSandbox(scriptTimeoutMs),
		allowPrivate: allowPrivate,
	}
}

// FetchBalance 抓取目标平台余额，成功返回有限数值（纯数值语义）。
func (f *Fetcher) FetchBalance(ctx context.Context, target FetchTarget) (float64, error) {
	rawURL := target.Request["url"]
	if target.Request == nil || !jsval.Truthy(rawURL) {
		return 0, errors.New("未配置请求 URL")
	}
	reqURL := jsval.Str(rawURL)
	if err := f.assertPublicURL(ctx, reqURL); err != nil {
		return 0, err
	}

	method := "GET"
	if m, ok := target.Request["method"]; ok && m != nil {
		method = jsval.Str(m)
	}
	var bodyReader io.Reader
	if body, ok := target.Request["body"]; ok && jsval.Truthy(body) {
		encoded, err := json.Marshal(body)
		if err != nil {
			return 0, fmt.Errorf("请求失败: %s", err.Error())
		}
		bodyReader = bytes.NewReader(encoded)
	}

	req, err := http.NewRequestWithContext(ctx, method, reqURL, bodyReader)
	if err != nil {
		return 0, fmt.Errorf("请求失败: %s", err.Error())
	}
	for k, v := range jsval.ObjOrEmpty(target.Request["headers"]) {
		req.Header.Set(k, jsval.Str(v))
	}

	res, err := f.client.Do(req)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || isTimeout(err) {
			return 0, fmt.Errorf("请求超时 (%dms)", RequestTimeoutMs)
		}
		return 0, fmt.Errorf("请求失败: %s", err.Error())
	}
	defer res.Body.Close()

	if declared := parseContentLength(res.Header.Get("Content-Length")); declared > maxResponseBytes {
		return 0, fmt.Errorf("响应过大 (%d 字节, 上限 1MB)", declared)
	}
	text, err := readLimited(res.Body)
	if err != nil {
		return 0, err
	}
	if len(text) > maxResponseBytes {
		return 0, errors.New("响应过大 (超过 1MB)")
	}
	if res.StatusCode < 200 || res.StatusCode > 299 {
		head := truncateRunes(text, 200)
		if head == "" {
			head = statusText(res)
		}
		return 0, fmt.Errorf("HTTP %d: %s", res.StatusCode, head)
	}

	value, err := f.extract(target, text, flattenHeaders(res.Header))
	if err != nil {
		return 0, err
	}
	return assertNumeric(value)
}

// flattenHeaders 把响应头摊平成「小写键 → 值」，供 handler 的第二个参数读取。
func flattenHeaders(h http.Header) map[string]string {
	out := make(map[string]string, len(h))
	for k, v := range h {
		out[strings.ToLower(k)] = strings.Join(v, ", ")
	}
	return out
}

// extract 复刻旧实现的三种取值模型：单函数 handler / parse+extractor / response.path。
func (f *Fetcher) extract(target FetchTarget, text string, headers map[string]string) (any, error) {
	if target.Handler != "" {
		return f.sandbox.RunParseWithHeaders(text, headers, target.Handler)
	}
	if target.Extractor != "" || target.Parse != "" {
		var data any
		if target.Parse != "" {
			var err error
			data, err = f.sandbox.RunParse(text, target.Parse)
			if err != nil {
				return nil, err
			}
		} else {
			data = f.parseJSONOrJS(text)
		}
		if target.Extractor != "" {
			return f.sandbox.RunExtractor(target.Extractor, data)
		}
		return data, nil
	}
	if path, ok := target.Response["path"]; ok && jsval.Truthy(path) {
		value := resolvePath(f.parseJSONOrJS(text), path)
		if value != nil && jsval.IsNumber(value) {
			if div, valid := jsval.Number(target.Response["divider"]); valid && div != 0 {
				if n, ok := jsval.Numeric(value); ok {
					value = n / div
				}
			}
		}
		return value, nil
	}
	return nil, errors.New("未配置处理函数")
}

// parseJSONOrJS 先按 JSON 解析，失败再按 JS 赋值式响应求值（旧实现一致）。
func (f *Fetcher) parseJSONOrJS(text string) any {
	var v any
	if err := json.Unmarshal([]byte(text), &v); err == nil {
		return v
	}
	evaluated, err := f.sandbox.EvalJSResponse(text)
	if err != nil {
		return nil
	}
	return evaluated
}

// assertNumeric 复刻旧实现的结尾数值语义：undefined/null → 未返回余额；
// 有限数值 → 通过；空白字符串 → 未返回余额；其余 → 「必须返回数字」。
func assertNumeric(value any) (float64, error) {
	if value == nil {
		return 0, errors.New("处理函数未返回余额")
	}
	if n, ok := jsval.Numeric(value); ok {
		if jsval.Finite(n) {
			return n, nil
		}
		return 0, fmt.Errorf("处理函数必须返回数字(当前返回 number)")
	}
	if s, ok := value.(string); ok {
		if strings.TrimSpace(s) == "" {
			return 0, errors.New("处理函数未返回余额")
		}
	}
	return 0, fmt.Errorf("处理函数必须返回数字(当前返回 %s)", jsTypeOf(value))
}

// jsTypeOf 复刻 JS `typeof`（旧实现错误信息里使用）。
func jsTypeOf(v any) string {
	switch v.(type) {
	case nil:
		return "undefined"
	case string:
		return "string"
	case bool:
		return "boolean"
	case float64, int64, int:
		return "number"
	case map[string]any, []any:
		return "object"
	default:
		return "object"
	}
}

// --- SSRF 防护 ---

// assertPublicURL 只允许 http/https；默认拒绝环回 / 内网 / 云元数据地址。
func (f *Fetcher) assertPublicURL(ctx context.Context, rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Scheme == "" {
		return errors.New("请求 URL 无效")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return errors.New("仅支持 http/https 请求")
	}
	if f.allowPrivate {
		return nil
	}
	const deny = "目标地址为环回/内网地址，已阻止 (设置 QUOTAHUB_ALLOW_PRIVATE=1 可放开)"
	host := strings.ToLower(u.Hostname())
	if host == "localhost" || host == "localhost.localdomain" || strings.HasSuffix(host, ".localhost") {
		return errors.New(deny)
	}
	if ip := net.ParseIP(host); ip != nil {
		if isPrivateIP(ip) {
			return errors.New(deny)
		}
		return nil
	}
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", host)
	if err != nil {
		// 域名解析失败交给后续请求报错（与旧实现一致）。
		return nil
	}
	for _, ip := range ips {
		if isPrivateIP(ip) {
			return errors.New(deny)
		}
	}
	return nil
}

// isPrivateIP 复刻旧实现 isPrivateIp 的判定口径（环回 / 私网 / CGNAT / 链路本地 / 组播 / ULA）。
func isPrivateIP(ip net.IP) bool {
	if v4 := ip.To4(); v4 != nil {
		a, b := int(v4[0]), int(v4[1])
		if a == 0 || a == 10 || a == 127 {
			return true
		}
		if a == 100 && b >= 64 && b <= 127 { // CGNAT
			return true
		}
		if a == 169 && b == 254 { // 云元数据 169.254.169.254
			return true
		}
		if a == 172 && b >= 16 && b <= 31 {
			return true
		}
		if a == 192 && b == 168 {
			return true
		}
		return a >= 224 // 组播 / 保留
	}
	if ip.IsUnspecified() || ip.IsLoopback() {
		return true
	}
	if ip[0]&0xfe == 0xfc { // ULA fc00::/7
		return true
	}
	if ip[0] == 0xfe && ip[1]&0xc0 == 0x80 { // 链路本地 fe80::/10
		return true
	}
	return ip.IsMulticast() // ff00::/8 及 IPv4 组播
}

// --- 工具 ---

// parseContentLength 解析 Content-Length；缺失或非法按 0（复刻 `Number(header || 0)`）。
func parseContentLength(raw string) int64 {
	if raw == "" {
		return 0
	}
	n, err := strconv.ParseInt(strings.TrimSpace(raw), 10, 64)
	if err != nil {
		return 0
	}
	return n
}

// readLimited 读取响应体（多读 1 字节以判定是否超限）。
func readLimited(r io.Reader) (string, error) {
	buf, err := io.ReadAll(io.LimitReader(r, maxResponseBytes+1))
	if err != nil {
		return "", fmt.Errorf("请求失败: %s", err.Error())
	}
	return string(buf), nil
}

// statusText 取状态码的原因短语（对齐 res.statusText）。
func statusText(res *http.Response) string {
	prefix := strconv.Itoa(res.StatusCode) + " "
	return strings.TrimPrefix(res.Status, prefix)
}

// truncateRunes 按字符截断（对齐 JS slice 对中文的口径）。
func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}

// isTimeout 判断错误是否为超时（含 net.Error Timeout）。
func isTimeout(err error) bool {
	var ne net.Error
	return errors.As(err, &ne) && ne.Timeout()
}

// --- response.path 取值（复刻旧实现 resolvePath）---

var bracketIndex = regexp.MustCompile(`\[(\d+)\]`)

// resolvePath 复刻旧实现 resolvePath：`[n]` 归一为 `.n` 后按段下钻，遇 null/undefined 短路。
func resolvePath(obj any, pathExpr any) any {
	s := bracketIndex.ReplaceAllString(jsval.Str(pathExpr), ".$1")
	cur := obj
	for _, seg := range strings.Split(s, ".") {
		if seg == "" {
			continue
		}
		if cur == nil {
			return nil
		}
		cur = indexKey(cur, seg)
	}
	return cur
}

// indexKey 复刻 JS `cur[seg]`：对象按下标取键，数组按十进制下标取元素，其余返回 undefined。
func indexKey(cur any, seg string) any {
	switch t := cur.(type) {
	case map[string]any:
		return t[seg]
	case []any:
		idx, err := strconv.Atoi(seg)
		if err != nil || idx < 0 || idx >= len(t) {
			return nil
		}
		return t[idx]
	default:
		return nil
	}
}
