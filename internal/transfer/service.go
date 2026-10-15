// Package transfer 提供配置导入 / 导出（JSON）。
//
// 语义逐条对齐旧实现 services/transferService.ts：导出剔除已弃用的展示字段；导入支持
// 完整配置 / 单个平台 / 单个预设三种格式，按 id upsert，单项失败不影响其他项。
package transfer

import (
	"strings"

	"quotahub-go/internal/httperr"
	"quotahub-go/internal/ids"
	"quotahub-go/internal/jsval"
	"quotahub-go/internal/logs"
	"quotahub-go/internal/model"
	"quotahub-go/internal/platform"
	"quotahub-go/internal/presets"
	"quotahub-go/internal/timex"
)

// Service 是导入导出的业务逻辑。
type Service struct {
	platforms *platform.Service
	presets   *presets.Service
	logs      *logs.Service
}

// New 构造导入导出服务。
func New(platforms *platform.Service, presetSvc *presets.Service, logSvc *logs.Service) *Service {
	return &Service{platforms: platforms, presets: presetSvc, logs: logSvc}
}

// ExportResult 是导入结果。
type ImportResult struct {
	OK        bool     `json:"ok"`
	Platforms int      `json:"platforms"`
	Presets   int      `json:"presets"`
	Errors    []string `json:"errors"`
}

// Export 导出完整配置（全部平台 + 用户预设，剔除已弃用展示字段）。
func (s *Service) Export() model.ExportPayload {
	all := s.platforms.All()
	platformsOut := make([]model.Platform, 0, len(all))
	for _, p := range all {
		platformsOut = append(platformsOut, stripDisplayFields(p))
	}
	presetsOut := s.presets.All()
	if presetsOut == nil {
		presetsOut = []model.Preset{}
	}
	payload := model.ExportPayload{
		Type:       "quotahub-config",
		Version:    1,
		ExportedAt: timex.NowISO(),
		Platforms:  platformsOut,
		Presets:    presetsOut,
	}
	s.logs.Add("export", "导出配置: "+itoa(len(platformsOut))+" 个平台、"+itoa(len(presetsOut))+" 个预设",
		logs.Options{Meta: map[string]any{
			"platforms": len(platformsOut),
			"presets":   len(presetsOut),
		}})
	return payload
}

// Import 识别格式并逐条导入；无法识别整体格式时返回 400。
func (s *Service) Import(body any) (ImportResult, error) {
	obj := jsval.ObjOrEmpty(body)

	var platformsRaw []any
	var presetsRaw []any
	hasPlatforms := false
	hasPresets := false
	if arr, ok := obj["platforms"].([]any); ok {
		platformsRaw = arr
		hasPlatforms = true
	}
	if arr, ok := obj["presets"].([]any); ok {
		presetsRaw = arr
		hasPresets = true
	}
	switch {
	case hasPlatforms || hasPresets:
		// 完整配置（单侧缺省按空处理）
	case isPlatformLike(obj):
		platformsRaw = []any{obj}
	case isPresetLike(obj):
		presetsRaw = []any{obj}
	default:
		return ImportResult{}, httperr.New(400,
			"无法识别的配置格式: 需要完整配置(含 platforms/presets 数组)、单个平台或单个预设的 JSON")
	}

	result := ImportResult{OK: true, Errors: []string{}}

	if hasPlatforms || len(platformsRaw) > 0 {
		list := s.platforms.All()
		for _, raw := range platformsRaw {
			if err := s.importPlatform(&list, raw); err != nil {
				result.Errors = append(result.Errors, "平台「"+rawName(raw)+"」: "+err.Error())
				continue
			}
			result.Platforms++
		}
		if err := s.platforms.SaveAll(list); err != nil {
			return ImportResult{}, err
		}
	}

	if hasPresets || len(presetsRaw) > 0 {
		user := s.presets.All()
		for _, raw := range presetsRaw {
			if err := s.importPreset(&user, raw); err != nil {
				result.Errors = append(result.Errors, "预设「"+rawName(raw)+"」: "+err.Error())
				continue
			}
			result.Presets++
		}
		if err := s.presets.SaveAll(user); err != nil {
			return ImportResult{}, err
		}
	}

	detail := "导入配置: " + itoa(result.Platforms) + " 个平台、" + itoa(result.Presets) + " 个预设"
	if len(result.Errors) > 0 {
		detail += " (跳过 " + itoa(len(result.Errors)) + " 项)"
	}
	s.logs.Add("import", detail, logs.Options{Meta: map[string]any{
		"platforms": result.Platforms,
		"presets":   result.Presets,
		"skipped":   len(result.Errors),
	}})
	return result, nil
}

func (s *Service) importPlatform(list *[]model.Platform, raw any) error {
	obj, ok := jsval.Obj(raw)
	if !ok {
		return httperr.New(400, "条目不是对象")
	}
	p := sanitizePlatform(obj, ids.UUID())
	if p.Name == "" {
		return httperr.New(400, "平台名称不能为空")
	}
	if jsval.StrOr(p.Request["url"], "") == "" {
		return httperr.New(400, "缺少请求 URL")
	}
	if err := assertImportablePlatform(p); err != nil {
		return err
	}
	for i := range *list {
		if (*list)[i].ID == p.ID {
			merged := (*list)[i]
			merged = mergePlatform(merged, p)
			(*list)[i] = merged
			return nil
		}
	}
	*list = append(*list, p)
	return nil
}

func (s *Service) importPreset(user *[]model.Preset, raw any) error {
	obj, ok := jsval.Obj(raw)
	if !ok {
		return httperr.New(400, "条目不是对象")
	}
	validated, err := presets.Validate(raw)
	if err != nil {
		return err
	}
	id := ""
	if v, ok := obj["id"].(string); ok && v != "" {
		id = v
	} else {
		id = ids.UUID()
	}
	validated.ID = id
	for i := range *user {
		if (*user)[i].ID == id {
			merged := (*user)[i]
			merged = mergePresetStored(merged, validated)
			(*user)[i] = merged
			return nil
		}
	}
	*user = append(*user, validated)
	return nil
}

// stripDisplayFields 导出前剔除 display 与 response.prefix/suffix。
func stripDisplayFields(p model.Platform) model.Platform {
	out := p
	out.Display = nil
	if out.Response != nil {
		res := map[string]any{}
		for k, v := range out.Response {
			if k == "prefix" || k == "suffix" {
				continue
			}
			res[k] = v
		}
		if len(res) > 0 {
			out.Response = res
		} else {
			out.Response = nil
		}
	}
	return out
}

// sanitizePlatform 复刻旧实现 sanitizePlatform：字段级规范化 + legacy display 保留。
func sanitizePlatform(obj map[string]any, fallbackID string) model.Platform {
	id := fallbackID
	if v, ok := obj["id"].(string); ok && v != "" {
		id = v
	}
	name := "未命名平台"
	if v, ok := obj["name"]; ok && jsval.Truthy(v) {
		name = strings.TrimSpace(jsval.Str(v))
	}
	req := jsval.ObjOrEmpty(obj["request"])
	method := "GET"
	if req["method"] == "POST" {
		method = "POST"
	}
	headers := map[string]any{}
	if m, ok := jsval.Obj(req["headers"]); ok {
		headers = m
	}
	request := map[string]any{
		"method":  method,
		"url":     jsval.StrOr(req["url"], ""),
		"headers": headers,
	}
	if body, ok := req["body"]; ok {
		request["body"] = body
	}
	p := model.Platform{
		ID:        id,
		Name:      name,
		Request:   request,
		Handler:   stringOr(obj["handler"]),
		Extractor: stringOr(obj["extractor"]),
		Parse:     stringOr(obj["parse"]),
		Format:    stringOr(obj["format"]),
	}
	if res, ok := jsval.Obj(obj["response"]); ok {
		p.Response = res
	}
	if display, ok := jsval.Obj(obj["display"]); ok {
		p.Display = display
	}
	if v, ok := obj["createdAt"].(string); ok && v != "" {
		p.CreatedAt = v
	} else {
		p.CreatedAt = timex.NowISO()
	}
	return p
}

// assertImportablePlatform 复刻导入校验：处理函数必须存在；凭据不能是脱敏占位符。
func assertImportablePlatform(p model.Platform) error {
	responsePath := ""
	if v, ok := p.Response["path"]; ok {
		responsePath = jsval.StrOr(v, "")
	}
	if strings.TrimSpace(p.Handler) == "" &&
		strings.TrimSpace(p.Extractor) == "" &&
		strings.TrimSpace(p.Parse) == "" &&
		responsePath == "" {
		return httperr.New(400, "缺少处理函数")
	}
	for k, v := range p.Request {
		if k != "headers" {
			continue
		}
		if m, ok := jsval.Obj(v); ok {
			for hk, hv := range m {
				if s, ok := hv.(string); ok && s == "********" {
					return httperr.Newf(400, "请求头 %s 是脱敏占位符，请使用完整导出（含真实凭据）的 JSON 导入", hk)
				}
			}
		}
	}
	return nil
}

func isPlatformLike(obj map[string]any) bool {
	if _, ok := obj["name"].(string); !ok {
		return false
	}
	req, ok := jsval.Obj(obj["request"])
	if !ok {
		return false
	}
	_, urlOK := req["url"].(string)
	return urlOK
}

func isPresetLike(obj map[string]any) bool {
	if _, ok := obj["name"].(string); !ok {
		return false
	}
	if !jsval.IsArray(obj["fields"]) {
		return false
	}
	if _, ok := obj["urlTemplate"].(string); !ok {
		return false
	}
	_, ok := obj["extractorTemplate"].(string)
	return ok
}

// mergePlatform 复刻 `{...list[idx], ...p}`：p 的已知字段覆盖旧值；p 缺 response/display 时
// 保留旧值；旧记录里的未知键（如 preset）保留。
func mergePlatform(old, p model.Platform) model.Platform {
	out := old.Clone()
	out.ID = p.ID
	out.Name = p.Name
	out.Request = p.Request
	out.Handler = p.Handler
	out.Extractor = p.Extractor
	out.Parse = p.Parse
	out.Format = p.Format
	out.CreatedAt = p.CreatedAt
	if p.Response != nil {
		out.Response = p.Response
	}
	if p.Display != nil {
		out.Display = p.Display
	}
	out.MarkPresent("id", "name", "request", "handler", "extractor", "parse", "format", "createdAt")
	if p.Response != nil {
		out.MarkPresent("response")
	}
	if p.Display != nil {
		out.MarkPresent("display")
	}
	return out
}

// mergePresetStored 复刻 `{...user[idx], ...item}`。
func mergePresetStored(old, item model.Preset) model.Preset {
	out := item
	if item.CreatedAt == "" {
		out.CreatedAt = old.CreatedAt
	}
	return out
}

func stringOr(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

func rawName(raw any) string {
	if obj, ok := jsval.Obj(raw); ok {
		if s, ok := obj["name"].(string); ok && s != "" {
			return s
		}
	}
	return "?"
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
