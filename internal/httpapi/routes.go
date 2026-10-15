package httpapi

// routes 是全部管理面路由的登记表，顺序即分发优先级。
//
// 顺序约定（对齐旧实现 Express 注册顺序）：
//   - 公开路由（GET /api/auth-mode）在最前；
//   - 内置认证端点仅 builtin 模式存在；
//   - /api/platforms 下字面量段（/reorder、/balances、/refresh、/validate）必须排在 /:id 之前；
//   - 其余业务路由与路径互不重叠。
//
// 机检脚本 scripts/check-api-docs.mjs 按 `pattern: "METHOD /path"` 字面量抽取路由，
// 与 docs/API.md 的 `### METHOD /path` 标题逐条比对；改了路由必须同步文档。
var routes = []route{
	{pattern: "GET /api/auth-mode", public: true, handler: (*Server).handleAuthMode},

	{pattern: "POST /api/auth/login", builtinOnly: true, handler: (*Server).handleLogin},
	{pattern: "POST /api/auth/logout", builtinOnly: true, handler: (*Server).handleLogout},
	{pattern: "GET /api/auth/me", builtinOnly: true, handler: (*Server).handleMe},

	{pattern: "GET /api/platforms", handler: (*Server).handleListPlatforms},
	{pattern: "POST /api/platforms", handler: (*Server).handleCreatePlatform},
	{pattern: "PUT /api/platforms/reorder", handler: (*Server).handleReorder},
	{pattern: "GET /api/platforms/balances", handler: (*Server).handleBalances},
	{pattern: "POST /api/platforms/refresh", handler: (*Server).handleRefresh},
	{pattern: "POST /api/platforms/validate", handler: (*Server).handleValidate},
	{pattern: "PUT /api/platforms/:id", handler: (*Server).handleUpdatePlatform},
	{pattern: "DELETE /api/platforms/:id", handler: (*Server).handleDeletePlatform},
	{pattern: "POST /api/platforms/:id/test", handler: (*Server).handleTestPlatform},
	{pattern: "POST /api/platforms/:id/fetch", handler: (*Server).handleFetchPlatform},
	{pattern: "GET /api/platforms/:id/history", handler: (*Server).handlePlatformHistory},

	{pattern: "GET /api/presets", handler: (*Server).handleListPresets},
	{pattern: "POST /api/presets", handler: (*Server).handleCreatePreset},
	{pattern: "PUT /api/presets/:id", handler: (*Server).handleUpsertPreset},
	{pattern: "DELETE /api/presets/:id", handler: (*Server).handleDeletePreset},
	{pattern: "POST /api/presets/:id/reset", handler: (*Server).handleResetPreset},

	{pattern: "GET /api/settings", handler: (*Server).handleGetSettings},
	{pattern: "PUT /api/settings", handler: (*Server).handleUpdateSettings},

	{pattern: "GET /api/logs", handler: (*Server).handleListLogs},
	{pattern: "DELETE /api/logs", handler: (*Server).handleClearLogs},

	{pattern: "GET /api/export", handler: (*Server).handleExport},
	{pattern: "POST /api/import", handler: (*Server).handleImport},
}
