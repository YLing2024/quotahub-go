package httpapi

import (
	"quotahub-go/internal/collect"
	"quotahub-go/internal/httperr"
	"quotahub-go/internal/jsval"
	"quotahub-go/internal/logs"
	"quotahub-go/internal/timex"
)

// handleListPlatforms 处理 GET /api/platforms：返回全部平台对外形态。
func (s *Server) handleListPlatforms(c *reqCtx) error {
	writeJSON(c.W, 200, s.platforms.List())
	return nil
}

// handleCreatePlatform 处理 POST /api/platforms：新建平台并记日志，成功 201。
func (s *Server) handleCreatePlatform(c *reqCtx) error {
	body, ok := jsval.Obj(c.Body)
	if !ok {
		return httperr.New(400, "请求体必须是 JSON 对象")
	}
	created, err := s.platforms.Create(body)
	if err != nil {
		return err
	}
	createdID := jsval.Str(created["id"])
	createdName := jsval.Str(created["name"])
	s.logs.Add("create", "新增平台「"+createdName+"」", logs.Options{
		PlatformID:   createdID,
		PlatformName: createdName,
	})
	writeJSON(c.W, 201, created)
	return nil
}

// handleReorder 处理 PUT /api/platforms/reorder：ids 必须是现有平台的完整排列。
func (s *Server) handleReorder(c *reqCtx) error {
	body, ok := jsval.Obj(c.Body)
	if !ok {
		return httperr.New(400, "需要 ids 数组")
	}
	ids, ok := body["ids"].([]any)
	if !ok {
		return httperr.New(400, "需要 ids 数组")
	}
	if err := s.platforms.Reorder(ids); err != nil {
		return err
	}
	s.logs.Add("reorder", "调整平台排序", logs.Options{Meta: map[string]any{"count": len(ids)}})
	writeJSON(c.W, 200, okResponse{OK: true})
	return nil
}

// handleBalances 处理 GET /api/platforms/balances：面板数据。
func (s *Server) handleBalances(c *reqCtx) error {
	dashboard, err := s.monitor.BuildDashboard(c.R.Context())
	if err != nil {
		return err
	}
	writeJSON(c.W, 200, dashboard)
	return nil
}

// handleRefresh 处理 POST /api/platforms/refresh：顺序抓取全部平台。
func (s *Server) handleRefresh(c *reqCtx) error {
	writeJSON(c.W, 200, s.monitor.RefreshAll(c.R.Context()))
	return nil
}

// handleValidate 处理 POST /api/platforms/validate：校验抓取配置，不保存、不写历史。
func (s *Server) handleValidate(c *reqCtx) error {
	target := targetFromBody(jsval.ObjOrEmpty(c.Body))
	value, err := s.monitor.Validate(c.R.Context(), target)
	if err != nil {
		writeJSON(c.W, 502, failResponse{OK: false, Error: err.Error()})
		return nil
	}
	writeJSON(c.W, 200, valueResponse{OK: true, Value: value})
	return nil
}

// handleUpdatePlatform 处理 PUT /api/platforms/:id：部分更新并记日志。
func (s *Server) handleUpdatePlatform(c *reqCtx) error {
	body, ok := jsval.Obj(c.Body)
	if !ok {
		return httperr.New(400, "请求体必须是 JSON 对象")
	}
	updated, err := s.platforms.Update(c.Params["id"], body)
	if err != nil {
		return err
	}
	updatedID := jsval.Str(updated["id"])
	updatedName := jsval.Str(updated["name"])
	s.logs.Add("update", "更新平台「"+updatedName+"」", logs.Options{
		PlatformID:   updatedID,
		PlatformName: updatedName,
	})
	writeJSON(c.W, 200, updated)
	return nil
}

// handleDeletePlatform 处理 DELETE /api/platforms/:id：删除平台并清其历史采样，204。
func (s *Server) handleDeletePlatform(c *reqCtx) error {
	removed, err := s.platforms.Delete(c.Params["id"])
	if err != nil {
		return err
	}
	if err := s.history.RemoveSamples(c.R.Context(), removed.ID); err != nil {
		return err
	}
	s.logs.Add("delete", "删除平台「"+removed.Name+"」", logs.Options{
		PlatformID:   removed.ID,
		PlatformName: removed.Name,
	})
	c.W.WriteHeader(204)
	return nil
}

// handleTestPlatform 处理 POST /api/platforms/:id/test：不写历史，成功带 testedAt。
func (s *Server) handleTestPlatform(c *reqCtx) error {
	p, ok := s.platforms.Find(c.Params["id"])
	if !ok {
		return httperr.New(404, "平台不存在")
	}
	value, err := s.monitor.Test(c.R.Context(), p)
	if err != nil {
		writeJSON(c.W, 502, failResponse{OK: false, Error: err.Error()})
		return nil
	}
	writeJSON(c.W, 200, testResponse{OK: true, Value: value, TestedAt: timex.NowISO()})
	return nil
}

// handleFetchPlatform 处理 POST /api/platforms/:id/fetch：抓取并记录采样点。
func (s *Server) handleFetchPlatform(c *reqCtx) error {
	outcome, err := s.monitor.FetchOne(c.R.Context(), c.Params["id"])
	if err != nil {
		return err
	}
	if outcome.OK {
		writeJSON(c.W, 200, fetchResponse{OK: true, Value: outcome.Value, FetchedAt: outcome.FetchedAt})
		return nil
	}
	writeJSON(c.W, 502, failResponse{OK: false, Error: outcome.Error})
	return nil
}

// handlePlatformHistory 处理 GET /api/platforms/:id/history：折线图采样点（时间升序）。
func (s *Server) handlePlatformHistory(c *reqCtx) error {
	id := c.Params["id"]
	points, err := s.history.Samples(c.R.Context(), id)
	if err != nil {
		return err
	}
	view, err := s.platforms.HistoryView(id, points)
	if err != nil {
		return err
	}
	writeJSON(c.W, 200, view)
	return nil
}

// targetFromBody 从松散请求体抽取抓取目标（validate 接口用）。
func targetFromBody(m map[string]any) collect.FetchTarget {
	target := collect.FetchTarget{}
	if req, ok := jsval.Obj(m["request"]); ok {
		target.Request = req
	}
	if h, ok := m["handler"].(string); ok {
		target.Handler = h
	}
	if e, ok := m["extractor"].(string); ok {
		target.Extractor = e
	}
	if p, ok := m["parse"].(string); ok {
		target.Parse = p
	}
	if r, ok := jsval.Obj(m["response"]); ok {
		target.Response = r
	}
	return target
}
