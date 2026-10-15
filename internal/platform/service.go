// Package platform 提供平台配置的业务逻辑（JSON 文件持久化）。
//
// 语义逐条对齐旧实现 services/platformService.ts：字段规范化、部分更新（pickConfig）、
// 处理函数必填校验、排序、对外输出剔除已弃用的 display 与 response.prefix/suffix。
package platform

import (
	"path/filepath"
	"strings"
	"sync"

	"quotahub-go/internal/httperr"
	"quotahub-go/internal/ids"
	"quotahub-go/internal/jsonstore"
	"quotahub-go/internal/jsval"
	"quotahub-go/internal/model"
	"quotahub-go/internal/timex"
)

// Service 是平台配置的数据访问 + 业务逻辑。
type Service struct {
	file string
	mu   sync.Mutex
}

// New 构造平台服务；dataDir 下使用 platforms.json（与旧实现同名）。
func New(dataDir string) *Service {
	return &Service{file: filepath.Join(dataDir, "platforms.json")}
}

// All 返回全部平台的存储形态（含 legacy 字段）；供采集、导入导出与列表复用。
func (s *Service) All() []model.Platform {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.readLocked()
}

// List 返回全部平台的对外形态。
func (s *Service) List() []model.PublicPlatform {
	all := s.All()
	out := make([]model.PublicPlatform, 0, len(all))
	for _, p := range all {
		out = append(out, toPublic(p))
	}
	return out
}

// Create 新建平台：规范化 + 处理函数校验后追加并落盘。
func (s *Service) Create(body map[string]any) (model.PublicPlatform, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	p := normalizePlatform(body)
	if err := assertHasHandler(p); err != nil {
		return model.PublicPlatform{}, err
	}
	list := s.readLocked()
	list = append(list, p)
	if err := s.writeLocked(list); err != nil {
		return model.PublicPlatform{}, err
	}
	return toPublic(p), nil
}

// Update 部分更新平台：只覆盖请求里出现的字段。
func (s *Service) Update(id string, body map[string]any) (model.PublicPlatform, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.readLocked()
	idx := indexOf(list, id)
	if idx == -1 {
		return model.PublicPlatform{}, notFound()
	}
	next := applyPatch(list[idx], body)
	if err := assertHasHandler(next); err != nil {
		return model.PublicPlatform{}, err
	}
	list[idx] = next
	if err := s.writeLocked(list); err != nil {
		return model.PublicPlatform{}, err
	}
	return toPublic(next), nil
}

// Delete 删除平台并返回被删项（调用方据此清历史采样、记日志）。
func (s *Service) Delete(id string) (model.Platform, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.readLocked()
	idx := indexOf(list, id)
	if idx == -1 {
		return model.Platform{}, notFound()
	}
	removed := list[idx]
	next := append(append([]model.Platform{}, list[:idx]...), list[idx+1:]...)
	if err := s.writeLocked(next); err != nil {
		return model.Platform{}, err
	}
	return removed, nil
}

// Reorder 按 ids 重排平台；ids 必须是现有平台 id 的完整排列。
func (s *Service) Reorder(rawIDs any) error {
	if !jsval.IsArray(rawIDs) {
		return httperr.New(400, "需要 ids 数组")
	}
	idsList := rawIDs.([]any)
	s.mu.Lock()
	defer s.mu.Unlock()
	list := s.readLocked()
	if len(idsList) != len(list) {
		return httperr.New(400, "ids 数量与现有平台不匹配")
	}
	byID := make(map[string]model.Platform, len(list))
	for _, p := range list {
		byID[p.ID] = p
	}
	seen := make(map[string]bool, len(idsList))
	next := make([]model.Platform, 0, len(idsList))
	for _, raw := range idsList {
		id, ok := raw.(string)
		if !ok {
			return httperr.Newf(400, "未知平台 id: %s", jsval.Str(raw))
		}
		p, exists := byID[id]
		if !exists {
			return httperr.Newf(400, "未知平台 id: %s", id)
		}
		if seen[id] {
			return httperr.New(400, "ids 包含重复项")
		}
		seen[id] = true
		next = append(next, p)
	}
	return s.writeLocked(next)
}

// HistoryView 组装折线图数据；平台不存在返回 404。
func (s *Service) HistoryView(id string, points []model.SamplePoint) (model.HistoryView, error) {
	if points == nil {
		points = []model.SamplePoint{}
	}
	all := s.All()
	idx := indexOf(all, id)
	if idx == -1 {
		return model.HistoryView{}, notFound()
	}
	return model.HistoryView{ID: id, Name: all[idx].Name, Points: points}, nil
}

// Find 按 id 取平台存储形态；不存在返回 false。
func (s *Service) Find(id string) (model.Platform, bool) {
	for _, p := range s.All() {
		if p.ID == id {
			return p, true
		}
	}
	return model.Platform{}, false
}

// SaveAll 覆盖写入全部平台（供导入按 id 合并后落盘）。
func (s *Service) SaveAll(list []model.Platform) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.writeLocked(list)
}

// --- 内部 ---

func (s *Service) readLocked() []model.Platform {
	list := jsonstore.ReadArray[model.Platform](s.file)
	if len(list) == 0 {
		return []model.Platform{}
	}
	return list
}

func (s *Service) writeLocked(list []model.Platform) error {
	if list == nil {
		list = []model.Platform{}
	}
	return jsonstore.WriteAtomic(s.file, list)
}

func indexOf(list []model.Platform, id string) int {
	for i := range list {
		if list[i].ID == id {
			return i
		}
	}
	return -1
}

func notFound() error { return httperr.New(404, "平台不存在") }

// normalizePlatform 复刻旧实现：默认值 + 原样展开用户请求配置。
func normalizePlatform(body map[string]any) model.Platform {
	request := map[string]any{"method": "GET", "url": "", "headers": map[string]any{}}
	for k, v := range jsval.ObjOrEmpty(body["request"]) {
		request[k] = v
	}
	return model.Platform{
		ID:        ids.UUID(),
		Name:      nameOrDefault(body["name"]),
		Request:   request,
		Handler:   textField(body["handler"]),
		Extractor: textField(body["extractor"]),
		Parse:     textField(body["parse"]),
		Format:    strings.TrimSpace(jsval.StrOr(body["format"], "")),
		URL:       truncate(strings.TrimSpace(jsval.StrOr(body["url"], "")), 500),
		CreatedAt: timex.NowISO(),
	}
}

// nameOrDefault 复刻 `(body.name) || '未命名平台'`：只接受非空字符串。
func nameOrDefault(v any) string {
	if s, ok := v.(string); ok && s != "" {
		return s
	}
	return "未命名平台"
}

// textField 复刻旧实现里对 body.x 取逻辑或空串的取值：非字符串一律视为空串。
func textField(v any) string {
	if s, ok := v.(string); ok {
		return s
	}
	return ""
}

// applyPatch 复刻旧 pickConfig：只覆盖请求里出现的字段；保存 handler 时清掉旧 parse/extractor。
func applyPatch(old model.Platform, body map[string]any) model.Platform {
	next := old
	if v, ok := body["name"]; ok {
		next.Name = strings.TrimSpace(jsval.Str(v))
	}
	if v, ok := body["request"]; ok {
		if m, isObj := jsval.Obj(v); isObj {
			next.Request = m
		} else {
			next.Request = map[string]any{}
		}
	}
	if v, ok := body["handler"]; ok {
		next.Handler = jsval.Str(v)
		next.Extractor = ""
		next.Parse = ""
	} else {
		if v, ok := body["extractor"]; ok {
			next.Extractor = jsval.Str(v)
		}
		if v, ok := body["parse"]; ok {
			next.Parse = jsval.Str(v)
		}
	}
	if v, ok := body["format"]; ok {
		next.Format = strings.TrimSpace(jsval.StrOr(v, ""))
	}
	if v, ok := body["url"]; ok {
		next.URL = truncate(strings.TrimSpace(jsval.StrOr(v, "")), 500)
	}
	return next
}

// assertHasHandler 复刻：至少需要一个处理 / 提取 / 解析函数。
func assertHasHandler(p model.Platform) error {
	if strings.TrimSpace(p.Handler) == "" &&
		strings.TrimSpace(p.Extractor) == "" &&
		strings.TrimSpace(p.Parse) == "" {
		return httperr.New(400, "需要配置处理函数（或旧的提取/解析函数）")
	}
	return nil
}

// toPublic 复刻旧 toPublic：剔除 display，response 内剔除 prefix/suffix，全空则省略 response。
func toPublic(p model.Platform) model.PublicPlatform {
	var response map[string]any
	if p.Response != nil {
		res := map[string]any{}
		for k, v := range p.Response {
			if k == "prefix" || k == "suffix" {
				continue
			}
			res[k] = v
		}
		if len(res) > 0 {
			response = res
		}
	}
	return model.PublicPlatform{
		ID:        p.ID,
		Name:      p.Name,
		Request:   p.Request,
		Handler:   p.Handler,
		Extractor: p.Extractor,
		Parse:     p.Parse,
		Format:    p.Format,
		Response:  response,
		URL:       p.URL,
		CreatedAt: p.CreatedAt,
	}
}

func truncate(s string, max int) string {
	if len(s) <= max {
		return s
	}
	return s[:max]
}
