// Package presets 提供预设模板的业务逻辑（JSON 文件持久化）。
//
// 语义逐条对齐旧实现 services/presetService.ts：内置预设集合与参数语义不得增删改；
// 仅内置预设支持「重置」，内置预设不可删除。用户文件只存自定义预设与对内置预设的覆盖。
package presets

import (
	"encoding/json"
	"path/filepath"
	"regexp"
	"strings"
	"sync"

	"quotahub-go/internal/httperr"
	"quotahub-go/internal/ids"
	"quotahub-go/internal/jsonstore"
	"quotahub-go/internal/jsval"
	"quotahub-go/internal/model"
	"quotahub-go/internal/timex"
)

// builtin 是内置预设集合。id / 名称 / 字段 / 模板逐字对齐旧实现，不得增删改。
var builtin = []model.Preset{
	{
		ID:   "newapi",
		Name: "NEWAPI",
		Fields: []model.PresetField{
			{Key: "baseUrl", Label: "请求地址（Base URL）", Placeholder: "https://your-newapi.example.com"},
			{Key: "token", Label: "访问令牌", Placeholder: "sk-你的访问令牌"},
			{Key: "userId", Label: "用户 ID（可留空）", Placeholder: "留空则不发送 New-Api-User 头"},
		},
		Method:      "GET",
		URLTemplate: "{{baseUrl}}/api/user/self",
		HeadersTemplate: map[string]string{
			"Content-Type":  "application/json",
			"Authorization": "Bearer {{token}}",
			"User-Agent":    "cc-switch/1.0",
			"New-Api-User":  "{{userId}}",
		},
		ExtractorTemplate: "function (data) {\n  return data.data.quota / 500000\n}",
	},
	{
		ID:   "deepseek",
		Name: "DeepSeek",
		Fields: []model.PresetField{
			{Key: "apiKey", Label: "API Key", Placeholder: "sk-你的 API Key"},
		},
		Method:      "GET",
		URLTemplate: "https://api.deepseek.com/user/balance",
		HeadersTemplate: map[string]string{
			"Accept":        "application/json",
			"Authorization": "Bearer {{apiKey}}",
		},
		ExtractorTemplate: "function (data) {\n  var infos = data.balance_infos\n  if (!infos || !infos.length) return null\n  return infos[0].total_balance\n}",
	},
}

var keyPattern = regexp.MustCompile(`^[\w-]+$`)

// Service 是预设配置的数据访问 + 业务逻辑。
type Service struct {
	file string
	mu   sync.Mutex
}

// New 构造预设服务；dataDir 下使用 presets.json（与旧实现同名）。
func New(dataDir string) *Service {
	return &Service{file: filepath.Join(dataDir, "presets.json")}
}

// IsBuiltin 判断 id 是否为内置预设。
func IsBuiltin(id string) bool {
	for _, b := range builtin {
		if b.ID == id {
			return true
		}
	}
	return false
}

// All 返回用户预设（仅自定义 / 覆盖项）；供导出与合并列表使用。
func (s *Service) All() []model.Preset {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked()
}

// SaveAll 覆盖写入全部用户预设（供导入按 id 合并后落盘）。
func (s *Service) SaveAll(list []model.Preset) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(list)
}

// Validate 暴露预设校验（供配置导入复用），返回规范化后的预设。
func Validate(body any) (model.Preset, error) { return validatePreset(body) }

// List 合并输出：内置在前（含覆盖标记），用户自定义在后。
func (s *Service) List() []model.PresetView {
	user := s.All()
	merged := make([]model.PresetView, 0, len(builtin)+len(user))
	for _, b := range builtin {
		var override *model.Preset
		for i := range user {
			if user[i].ID == b.ID {
				override = &user[i]
				break
			}
		}
		merged = append(merged, mergeBuiltin(b, override))
	}
	for _, p := range user {
		if IsBuiltin(p.ID) {
			continue
		}
		merged = append(merged, userView(p))
	}
	return merged
}

// Create 新建用户预设。
func (s *Service) Create(body any) (model.PresetView, error) {
	validated, err := validatePreset(body)
	if err != nil {
		return model.PresetView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.readLocked()
	item := validated
	item.ID = ids.UUID()
	item.CreatedAt = timex.NowISO()
	user = append(user, item)
	if err := s.writeLocked(user); err != nil {
		return model.PresetView{}, err
	}
	return userView(item), nil
}

// Upsert 不存在则创建（内置 id 也允许覆盖保存）。
func (s *Service) Upsert(id string, body any) (model.PresetView, error) {
	validated, err := validatePreset(body)
	if err != nil {
		return model.PresetView{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	validated.ID = id
	user := s.readLocked()
	idx := -1
	for i := range user {
		if user[i].ID == id {
			idx = i
			break
		}
	}
	if idx == -1 {
		user = append(user, validated)
	} else {
		merged := user[idx]
		merged = mergePreset(merged, validated)
		merged.ID = id
		user[idx] = merged
	}
	if err := s.writeLocked(user); err != nil {
		return model.PresetView{}, err
	}
	out := model.PresetView{
		ID:                id,
		Name:              validated.Name,
		Fields:            validated.Fields,
		Method:            validated.Method,
		URLTemplate:       validated.URLTemplate,
		HeadersTemplate:   validated.HeadersTemplate,
		ExtractorTemplate: validated.ExtractorTemplate,
		Builtin:           IsBuiltin(id),
	}
	return out, nil
}

// Delete 删除用户预设；内置预设禁止删除。
func (s *Service) Delete(id string) error {
	if IsBuiltin(id) {
		return httperr.New(400, "内置预设使用重置恢复默认，不能删除")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.readLocked()
	next := make([]model.Preset, 0, len(user))
	for _, p := range user {
		if p.ID != id {
			next = append(next, p)
		}
	}
	return s.writeLocked(next)
}

// Reset 重置内置预设（删掉覆盖项），返回默认定义；非内置预设报错。
func (s *Service) Reset(id string) (model.Preset, error) {
	if !IsBuiltin(id) {
		return model.Preset{}, httperr.New(400, "仅内置预设支持重置")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	user := s.readLocked()
	next := make([]model.Preset, 0, len(user))
	for _, p := range user {
		if p.ID != id {
			next = append(next, p)
		}
	}
	if err := s.writeLocked(next); err != nil {
		return model.Preset{}, err
	}
	for _, b := range builtin {
		if b.ID == id {
			return b, nil
		}
	}
	return model.Preset{}, httperr.New(400, "仅内置预设支持重置")
}

func (s *Service) readLocked() []model.Preset {
	list := jsonstore.ReadArray[model.Preset](s.file)
	if len(list) == 0 {
		return []model.Preset{}
	}
	return list
}

func (s *Service) writeLocked(list []model.Preset) error {
	if list == nil {
		list = []model.Preset{}
	}
	return jsonstore.WriteAtomic(s.file, list)
}

// userView 把用户预设转成对外视图（builtin=false）。
func userView(p model.Preset) model.PresetView {
	return model.PresetView{
		ID:                p.ID,
		Name:              p.Name,
		Fields:            orEmptyFields(p.Fields),
		Method:            p.Method,
		URLTemplate:       p.URLTemplate,
		HeadersTemplate:   p.HeadersTemplate,
		ExtractorTemplate: p.ExtractorTemplate,
		CreatedAt:         p.CreatedAt,
		Builtin:           false,
	}
}

// mergeBuiltin 复刻 `{...b, ...edited, builtin:true, edited:true}`：用户覆盖只带去非零字段。
func mergeBuiltin(b model.Preset, override *model.Preset) model.PresetView {
	out := model.PresetView{
		ID:                b.ID,
		Name:              b.Name,
		Fields:            orEmptyFields(b.Fields),
		Method:            b.Method,
		URLTemplate:       b.URLTemplate,
		HeadersTemplate:   b.HeadersTemplate,
		ExtractorTemplate: b.ExtractorTemplate,
		Builtin:           true,
	}
	if override == nil {
		return out
	}
	out.Edited = true
	if override.Name != "" {
		out.Name = override.Name
	}
	if override.Fields != nil {
		out.Fields = override.Fields
	}
	if override.Method != "" {
		out.Method = override.Method
	}
	if override.URLTemplate != "" {
		out.URLTemplate = override.URLTemplate
	}
	if override.HeadersTemplate != nil {
		out.HeadersTemplate = override.HeadersTemplate
	}
	if override.ExtractorTemplate != "" {
		out.ExtractorTemplate = override.ExtractorTemplate
	}
	if override.CreatedAt != "" {
		out.CreatedAt = override.CreatedAt
	}
	return out
}

// mergePreset 复刻 `{...existing, ...preset}`：preset 里非零字段覆盖 existing。
func mergePreset(existing, preset model.Preset) model.Preset {
	out := existing
	if preset.Name != "" {
		out.Name = preset.Name
	}
	if preset.Fields != nil {
		out.Fields = preset.Fields
	}
	if preset.Method != "" {
		out.Method = preset.Method
	}
	if preset.URLTemplate != "" {
		out.URLTemplate = preset.URLTemplate
	}
	if preset.HeadersTemplate != nil {
		out.HeadersTemplate = preset.HeadersTemplate
	}
	if preset.ExtractorTemplate != "" {
		out.ExtractorTemplate = preset.ExtractorTemplate
	}
	return out
}

func orEmptyFields(fields []model.PresetField) []model.PresetField {
	if fields == nil {
		return []model.PresetField{}
	}
	return fields
}

// validatePreset 复刻旧实现逐条中文校验，返回规范化后的预设（无 id/createdAt）。
func validatePreset(body any) (model.Preset, error) {
	b := jsval.ObjOrEmpty(body)
	name := jsval.StrOr(b["name"], "")
	if strings.TrimSpace(name) == "" {
		return model.Preset{}, httperr.New(400, "预设名称不能为空")
	}
	rawFields, ok := b["fields"].([]any)
	if !ok || len(rawFields) == 0 {
		return model.Preset{}, httperr.New(400, "至少需要一个字段")
	}
	fields := make([]model.PresetField, 0, len(rawFields))
	for _, raw := range rawFields {
		field := jsval.ObjOrEmpty(raw)
		key := strings.TrimSpace(jsval.StrOr(field["key"], ""))
		if key == "" {
			return model.Preset{}, httperr.New(400, "字段 key 不能为空")
		}
		if !keyPattern.MatchString(key) {
			return model.Preset{}, httperr.New(400, "字段 key 只能包含字母、数字、下划线、连字符")
		}
		label := strings.TrimSpace(jsval.StrOr(field["label"], ""))
		if label == "" {
			return model.Preset{}, httperr.New(400, "字段 label 不能为空")
		}
		fields = append(fields, model.PresetField{
			Key:         key,
			Label:       label,
			Placeholder: strings.TrimSpace(jsval.StrOr(field["placeholder"], "")),
		})
	}
	urlTemplate := jsval.StrOr(b["urlTemplate"], "")
	if strings.TrimSpace(urlTemplate) == "" {
		return model.Preset{}, httperr.New(400, "URL 模板不能为空")
	}
	headers, err := parseHeaders(b["headersTemplate"])
	if err != nil {
		return model.Preset{}, err
	}
	extractor := jsval.StrOr(b["extractorTemplate"], "")
	if strings.TrimSpace(extractor) == "" {
		return model.Preset{}, httperr.New(400, "提取函数模板不能为空")
	}
	method := "GET"
	if b["method"] == "POST" {
		method = "POST"
	}
	return model.Preset{
		Name:              strings.TrimSpace(name),
		Fields:            fields,
		Method:            method,
		URLTemplate:       strings.TrimSpace(urlTemplate),
		HeadersTemplate:   headers,
		ExtractorTemplate: extractor,
	}, nil
}

// parseHeaders 复刻旧实现的 headersTemplate 解析：字符串按 JSON 解析，其余必须是对象。
func parseHeaders(raw any) (map[string]string, error) {
	if s, ok := raw.(string); ok {
		var parsed any
		if err := json.Unmarshal([]byte(s), &parsed); err != nil {
			return nil, httperr.New(400, "请求头模板必须是合法 JSON")
		}
		raw = parsed
	}
	m, ok := jsval.Obj(raw)
	if !ok {
		return nil, httperr.New(400, "请求头模板必须是 JSON 对象")
	}
	out := make(map[string]string, len(m))
	for k, v := range m {
		out[k] = jsval.StrOr(v, "")
	}
	return out, nil
}
