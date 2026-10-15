package httpapi

import (
	"quotahub-go/internal/httperr"
	"quotahub-go/internal/jsval"
)

// handleListPresets 处理 GET /api/presets：内置在前（含覆盖标记）+ 用户自定义。
func (s *Server) handleListPresets(c *reqCtx) error {
	writeJSON(c.W, 200, s.presets.List())
	return nil
}

// handleCreatePreset 处理 POST /api/presets：新建用户预设，成功 201。
func (s *Server) handleCreatePreset(c *reqCtx) error {
	body, ok := jsval.Obj(c.Body)
	if !ok {
		return httperr.New(400, "请求体必须是 JSON 对象")
	}
	item, err := s.presets.Create(body)
	if err != nil {
		return err
	}
	writeJSON(c.W, 201, item)
	return nil
}

// handleUpsertPreset 处理 PUT /api/presets/:id：不存在则创建（内置 id 也允许覆盖）。
func (s *Server) handleUpsertPreset(c *reqCtx) error {
	body, ok := jsval.Obj(c.Body)
	if !ok {
		return httperr.New(400, "请求体必须是 JSON 对象")
	}
	item, err := s.presets.Upsert(c.Params["id"], body)
	if err != nil {
		return err
	}
	writeJSON(c.W, 200, item)
	return nil
}

// handleDeletePreset 处理 DELETE /api/presets/:id：内置预设禁止删除，成功 204。
func (s *Server) handleDeletePreset(c *reqCtx) error {
	if err := s.presets.Delete(c.Params["id"]); err != nil {
		return err
	}
	c.W.WriteHeader(204)
	return nil
}

// handleResetPreset 处理 POST /api/presets/:id/reset：仅内置预设可重置。
func (s *Server) handleResetPreset(c *reqCtx) error {
	item, err := s.presets.Reset(c.Params["id"])
	if err != nil {
		return err
	}
	writeJSON(c.W, 200, item)
	return nil
}
