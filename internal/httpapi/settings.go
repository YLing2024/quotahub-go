package httpapi

import (
	"strconv"

	"quotahub-go/internal/httperr"
	"quotahub-go/internal/jsval"
	"quotahub-go/internal/logs"
)

// handleGetSettings 处理 GET /api/settings。
func (s *Server) handleGetSettings(c *reqCtx) error {
	writeJSON(c.W, 200, s.settings.Get())
	return nil
}

// handleUpdateSettings 处理 PUT /api/settings：更新设置并按新间隔重新排程定时采集。
func (s *Server) handleUpdateSettings(c *reqCtx) error {
	body, ok := jsval.Obj(c.Body)
	if !ok {
		return httperr.New(400, "请求体必须是 JSON 对象")
	}
	before := s.settings.Get()
	next := s.settings.Update(body)
	s.monitor.Reschedule()
	if before.CollectIntervalSeconds != next.CollectIntervalSeconds {
		detail := "自动采集间隔设置为 " + strconv.Itoa(next.CollectIntervalSeconds) + " 秒"
		if next.CollectIntervalSeconds == 0 {
			detail += "（已关闭）"
		}
		s.logs.Add("settings", detail, logs.Options{Meta: map[string]any{
			"from": before.CollectIntervalSeconds,
			"to":   next.CollectIntervalSeconds,
		}})
	}
	writeJSON(c.W, 200, next)
	return nil
}

// handleListLogs 处理 GET /api/logs：?limit= 控制条数（1~2000，默认 200）。
func (s *Server) handleListLogs(c *reqCtx) error {
	entries, total := s.logs.List(parseLogLimit(c.R.URL.Query()["limit"]))
	writeJSON(c.W, 200, map[string]any{"logs": entries, "total": total})
	return nil
}

// handleClearLogs 处理 DELETE /api/logs：清空日志后再记一条，成功 204。
func (s *Server) handleClearLogs(c *reqCtx) error {
	s.logs.Clear()
	s.logs.Add("clear-logs", "操作日志已清空", logs.Options{})
	c.W.WriteHeader(204)
	return nil
}

// parseLogLimit 复刻旧实现：`Number(raw) || 200` 后钳制到 [1,2000]。
func parseLogLimit(values []string) int {
	raw := ""
	if len(values) > 0 {
		raw = joinComma(values)
	}
	n, ok := jsval.Number(raw)
	if !ok || n == 0 {
		n = 200
	}
	if n < 1 {
		return 1
	}
	if n > logs.MaxEntries {
		return logs.MaxEntries
	}
	return int(n)
}

func joinComma(values []string) string {
	out := ""
	for i, v := range values {
		if i > 0 {
			out += ","
		}
		out += v
	}
	return out
}
