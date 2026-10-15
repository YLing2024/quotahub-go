package auth

import (
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"quotahub-go/internal/config"
)

// AuthUserHeader 是网关注入头：仅 sso 模式信任该头名。
const AuthUserHeader = "X-Auth-User"

// SessionCookie 是自带会话 cookie 名（与旧实现一致；不采用 __Host- 前缀以保持契约不变）。
const SessionCookie = "quotahub_session"

// Authenticator 是业务侧唯一鉴权入口。
type Authenticator struct {
	mode    config.AuthMode
	acct    *Accounts
	limiter *loginLimiter
}

// NewAuthenticator 构造鉴权器。limiter 与实例一一对应（登录限速按客户端 IP 计数）。
func NewAuthenticator(mode config.AuthMode, acct *Accounts) *Authenticator {
	return &Authenticator{mode: mode, acct: acct, limiter: newLoginLimiter()}
}

// Mode 返回当前模式。
func (a *Authenticator) Mode() config.AuthMode { return a.mode }

// Accounts 返回账号服务（auth 路由直接用）。
func (a *Authenticator) Accounts() *Accounts { return a.acct }

// User 返回当前请求身份；未认证返回空串。builtin 忽略 X-Auth-User，sso 忽略 cookie/Bearer。
func (a *Authenticator) User(r *http.Request) string {
	if a.mode == config.AuthSSO {
		return strings.TrimSpace(r.Header.Get(AuthUserHeader))
	}
	token := ReadSessionToken(r)
	if token == "" {
		return ""
	}
	name, err := a.acct.VerifySession(r.Context(), token)
	if err != nil {
		// 会话表读取失败按未登录处理：不因库抖动 500（鉴权失败即可，业务请求随后自会再报错）。
		return ""
	}
	return name
}

// ParseCookies 极简 cookie 解析（不引第三方）：只取一层，同名取首个，值做一次 URL 解码。
func ParseCookies(header string) map[string]string {
	out := map[string]string{}
	if header == "" {
		return out
	}
	for _, part := range strings.Split(header, ";") {
		eq := strings.IndexByte(part, '=')
		if eq <= 0 {
			continue
		}
		key := strings.TrimSpace(part[:eq])
		if key == "" {
			continue
		}
		if _, dup := out[key]; dup {
			continue
		}
		val := strings.TrimSpace(part[eq+1:])
		if decoded, err := url.QueryUnescape(val); err == nil {
			val = decoded
		}
		out[key] = val
	}
	return out
}

// ReadSessionToken 取请求凭证：优先 `Authorization: Bearer <token>`，其次 cookie `quotahub_session`。
func ReadSessionToken(r *http.Request) string {
	if header := r.Header.Get("Authorization"); header != "" {
		if token := bearerToken(header); token != "" {
			return token
		}
	}
	if v := strings.TrimSpace(ParseCookies(r.Header.Get("Cookie"))[SessionCookie]); v != "" {
		return v
	}
	return ""
}

// bearerToken 复刻 /^Bearer\s+(.+)$/i 的匹配。
func bearerToken(header string) string {
	h := strings.TrimSpace(header)
	if len(h) >= 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	if len(h) < 6 || !strings.EqualFold(h[:6], "Bearer") {
		return ""
	}
	return strings.TrimSpace(strings.TrimLeft(h[6:], " \t\r\n"))
}

// IsSecureRequest 判定请求是否走 HTTPS（直连 TLS 或反代 X-Forwarded-Proto）。
func IsSecureRequest(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	proto := r.Header.Get("X-Forwarded-Proto")
	if proto == "" {
		return false
	}
	return strings.TrimSpace(strings.Split(proto, ",")[0]) == "https"
}

// ClientIP 返回客户端 IP：对齐 Express 默认（未开 trust proxy）下的 req.ip —— 取 socket 对端地址。
func ClientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

// SessionSetCookie 生成登录会话的 Set-Cookie 头值。
//
// 属性与顺序对齐 Express `res.cookie(name, token, {path, httpOnly, sameSite:'lax', secure, maxAge})`
// （底层 cookie.serialize 输出顺序：Max-Age; Path; Expires; HttpOnly; [Secure]; SameSite）。
func SessionSetCookie(token string, ttl time.Duration, secure bool) string {
	seconds := int64(ttl / time.Second)
	expires := time.Now().Add(ttl).UTC().Format(http.TimeFormat)
	header := SessionCookie + "=" + token +
		"; Max-Age=" + strconv.FormatInt(seconds, 10) +
		"; Path=/; Expires=" + expires +
		"; HttpOnly"
	if secure {
		header += "; Secure"
	}
	return header + "; SameSite=Lax"
}

// SessionClearCookie 生成登出 / 失效时清除会话 cookie 的 Set-Cookie 头值。
//
// 对齐 Express `res.clearCookie(name, {path, httpOnly, sameSite, secure})`：expires 为
// new Date(1)，保留 HttpOnly/SameSite（clearCookie 会带上传入的选项）。
func SessionClearCookie(secure bool) string {
	header := SessionCookie + "=; Path=/; Expires=" + time.Unix(1, 0).UTC().Format(http.TimeFormat) + "; HttpOnly"
	if secure {
		header += "; Secure"
	}
	return header + "; SameSite=Lax"
}

// --- 登录失败限速（按客户端 IP，内存计数，进程重启清零）---

const (
	maxLoginFailures = 5
	lockDuration     = 15 * time.Minute
)

type attempt struct {
	failures    int
	lockedUntil int64
}

// loginLimiter 记录各 IP 的连续失败次数与锁定时刻。
type loginLimiter struct {
	entries map[string]attempt
}

func newLoginLimiter() *loginLimiter {
	return &loginLimiter{entries: map[string]attempt{}}
}

// RetryAfterSeconds 返回剩余锁定秒数；0 表示未锁定。
func (l *loginLimiter) RetryAfterSeconds(ip string, now int64) int {
	a, ok := l.entries[ip]
	if !ok || a.lockedUntil <= now {
		return 0
	}
	return int((a.lockedUntil - now + 999) / 1000)
}

// RecordFailure 记一次失败；达到阈值则锁 15 分钟并重置计数。
func (l *loginLimiter) RecordFailure(ip string, now int64) {
	a := l.entries[ip]
	a.failures++
	if a.failures >= maxLoginFailures {
		a.lockedUntil = now + lockDuration.Milliseconds()
		a.failures = 0
	}
	l.entries[ip] = a
}

// Clear 清除某 IP 的失败计数（登录成功）。
func (l *loginLimiter) Clear(ip string) { delete(l.entries, ip) }

// Limiter 返回登录限速器（auth 路由使用）。
func (a *Authenticator) Limiter() *loginLimiter { return a.limiter }
