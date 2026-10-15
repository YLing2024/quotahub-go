package httpapi

import (
	"time"

	"quotahub-go/internal/auth"
	"quotahub-go/internal/httperr"
	"quotahub-go/internal/jsval"
)

// handleAuthMode 处理 GET /api/auth-mode（免鉴权）：返回当前认证模式。
func (s *Server) handleAuthMode(c *reqCtx) error {
	writeJSON(c.W, 200, map[string]any{"authMode": string(s.cfg.AuthMode)})
	return nil
}

// handleLogin 处理 POST /api/auth/login（仅 builtin）：账号口令校验 + 会话 cookie。
func (s *Server) handleLogin(c *reqCtx) error {
	body, ok := jsval.Obj(c.Body)
	username, uOK := body["username"].(string)
	password, pOK := body["password"].(string)
	if !ok || !uOK || !pOK {
		writeJSON(c.W, 401, map[string]any{"error": "账号或密码错误"})
		return nil
	}

	ip := auth.ClientIP(c.R)
	now := time.Now().UnixMilli()
	if retry := s.auth.Limiter().RetryAfterSeconds(ip, now); retry > 0 {
		writeJSON(c.W, 429, map[string]any{"error": "尝试过于频繁", "retryAfter": retry})
		return nil
	}

	session, err := s.auth.Accounts().Login(c.R.Context(), username, password)
	if err != nil {
		return err
	}
	if session == nil {
		s.auth.Limiter().RecordFailure(ip, now)
		writeJSON(c.W, 401, map[string]any{"error": "账号或密码错误"})
		return nil
	}
	s.auth.Limiter().Clear(ip)
	secure := auth.IsSecureRequest(c.R)
	ttl := s.auth.Accounts().TTL()
	c.W.Header().Add("Set-Cookie", auth.SessionSetCookie(session.Token, ttl, secure))
	writeJSON(c.W, 200, map[string]any{"ok": true, "user": map[string]any{"name": session.Username}})
	return nil
}

// handleLogout 处理 POST /api/auth/logout（仅 builtin）：幂等，未登录也 200。
func (s *Server) handleLogout(c *reqCtx) error {
	token := auth.ReadSessionToken(c.R)
	if err := s.auth.Accounts().Logout(c.R.Context(), token); err != nil {
		return err
	}
	c.W.Header().Add("Set-Cookie", auth.SessionClearCookie(auth.IsSecureRequest(c.R)))
	writeJSON(c.W, 200, map[string]any{"ok": true})
	return nil
}

// handleMe 处理 GET /api/auth/me（仅 builtin）：返回当前登录用户名，未登录 401。
func (s *Server) handleMe(c *reqCtx) error {
	token := auth.ReadSessionToken(c.R)
	name, err := s.auth.Accounts().VerifySession(c.R.Context(), token)
	if err != nil {
		return err
	}
	if name == "" {
		return httperr.New(401, "未登录")
	}
	writeJSON(c.W, 200, map[string]any{"name": name})
	return nil
}
