package httpapi

import (
	"quotahub-go/internal/httperr"
	"quotahub-go/internal/jsval"
)

// handleExport 处理 GET /api/export：导出全部平台与用户预设（剔除已弃用展示字段）。
func (s *Server) handleExport(c *reqCtx) error {
	writeJSON(c.W, 200, s.transfer.Export())
	return nil
}

// handleImport 处理 POST /api/import：识别完整配置 / 单平台 / 单预设三种格式并逐条导入。
func (s *Server) handleImport(c *reqCtx) error {
	body, ok := jsval.Obj(c.Body)
	if !ok {
		return httperr.New(400,
			"无法识别的配置格式: 需要完整配置(含 platforms/presets 数组)、单个平台或单个预设的 JSON")
	}
	result, err := s.transfer.Import(body)
	if err != nil {
		return err
	}
	writeJSON(c.W, 200, result)
	return nil
}
