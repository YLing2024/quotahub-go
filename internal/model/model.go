// Package model 定义跨层共享的领域类型（平台 / 预设 / 设置 / 采样点 / 日志 / 面板）。
//
// 字段与 JSON 键逐条对齐旧实现 server/src/types.ts；平台 request/response/display 保持
// 松散对象形态（旧实现原样展开用户配置，键集合不固定）。展示文本由前端在渲染时执行
// platform.format 源码得到，后端只透传源码（纯数值语义）。
package model

import (
	"bytes"
	"encoding/json"
	"maps"
)

// Platform 是平台配置的存储形态（含 legacy 字段 display / response.prefix/suffix）。
//
// 旧实现把平台当作普通 JS 对象原样展开，因此「源记录里没有某个键」与「该键为空串」
// 是两种不同形状：序列化必须忠实还原源键集合（含 preset 等未知键）。Go 结构体无法表达
// 「键是否存在」，故这里额外记录 present（已知键出现情况）与 extra（未知键）：
//   - 由 json.UnmarshalJSON 读入时：present 记录文件里实际出现的已知键，extra 收留其余键；
//   - 由 normalize/sanitize 等程序构造时 present 为 nil，序列化按固定键集合输出（对齐旧实现）。
type Platform struct {
	ID        string         `json:"-"`
	Name      string         `json:"-"`
	Request   map[string]any `json:"-"`
	Handler   string         `json:"-"`
	Extractor string         `json:"-"`
	Parse     string         `json:"-"`
	Format    string         `json:"-"`
	Response  map[string]any `json:"-"`
	// Display 已弃用，仅为旧导出文件导入兼容保留，不渲染、不再被导出。
	Display   map[string]any `json:"-"`
	URL       string         `json:"-"`
	CreatedAt string         `json:"-"`

	// present 记录源对象里出现过的已知键；nil 表示「程序构造的规范对象」。
	present map[string]bool
	// extra 收留已知键以外的未知键（如 preset），原样透传。
	extra map[string]any
}

// platformKnownKeys 是平台对象的已知键；present 只对这些键有意义。
var platformKnownKeys = []string{
	"id", "name", "request", "handler", "extractor", "parse",
	"format", "response", "display", "url", "createdAt",
}

// PublicPlatform 是平台对外输出形态（不含 display，response 已剔除 prefix/suffix）。
//
// 用具名 map 而非结构体，是为了忠实保留源记录的键集合（缺失键不下发、未知键原样透传）。
type PublicPlatform map[string]any

// UnmarshalJSON 读入平台对象，并记录源键集合以便忠实回写/下发。
func (p *Platform) UnmarshalJSON(data []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return err
	}
	p.present = make(map[string]bool, len(raw))
	p.extra = map[string]any{}
	for key, value := range raw {
		switch key {
		case "id":
			p.ID, _ = value.(string)
		case "name":
			p.Name, _ = value.(string)
		case "request":
			p.Request, _ = value.(map[string]any)
		case "handler":
			p.Handler, _ = value.(string)
		case "extractor":
			p.Extractor, _ = value.(string)
		case "parse":
			p.Parse, _ = value.(string)
		case "format":
			p.Format, _ = value.(string)
		case "response":
			p.Response, _ = value.(map[string]any)
		case "display":
			p.Display, _ = value.(map[string]any)
		case "url":
			p.URL, _ = value.(string)
		case "createdAt":
			p.CreatedAt, _ = value.(string)
		default:
			p.extra[key] = value
		}
		p.present[key] = true
	}
	return nil
}

// MarshalJSON 输出忠实于源记录形状的对象（键序不保证，集合一致）。
func (p Platform) MarshalJSON() ([]byte, error) {
	return marshalNoHTMLEscape(p.Object())
}

// Object 返回平台对象的完整键集合（调用方可安全增删，不影响 Platform）。
func (p Platform) Object() map[string]any {
	obj := make(map[string]any, len(platformKnownKeys)+len(p.extra))
	if p.present == nil {
		obj["id"] = p.ID
		obj["name"] = p.Name
		obj["request"] = p.Request
		obj["handler"] = p.Handler
		obj["extractor"] = p.Extractor
		obj["parse"] = p.Parse
		obj["format"] = p.Format
		if p.Response != nil {
			obj["response"] = p.Response
		}
		if p.Display != nil {
			obj["display"] = p.Display
		}
		obj["url"] = p.URL
		obj["createdAt"] = p.CreatedAt
	} else {
		if p.present["id"] {
			obj["id"] = p.ID
		}
		if p.present["name"] {
			obj["name"] = p.Name
		}
		if p.present["request"] {
			obj["request"] = p.Request
		}
		if p.present["handler"] {
			obj["handler"] = p.Handler
		}
		if p.present["extractor"] {
			obj["extractor"] = p.Extractor
		}
		if p.present["parse"] {
			obj["parse"] = p.Parse
		}
		if p.present["format"] {
			obj["format"] = p.Format
		}
		if p.present["response"] && p.Response != nil {
			obj["response"] = p.Response
		}
		if p.present["display"] && p.Display != nil {
			obj["display"] = p.Display
		}
		if p.present["url"] {
			obj["url"] = p.URL
		}
		if p.present["createdAt"] {
			obj["createdAt"] = p.CreatedAt
		}
	}
	for key, value := range p.extra {
		obj[key] = value
	}
	return obj
}

// Clone 复制平台并把 present/extra 一并深拷，避免部分更新时污染原记录。
func (p Platform) Clone() Platform {
	out := p
	if p.present != nil {
		out.present = maps.Clone(p.present)
	}
	if p.extra != nil {
		out.extra = maps.Clone(p.extra)
	}
	return out
}

// MarkPresent 把给定已知键标记为「源对象里存在」（部分更新/导入合并后调用）。
func (p *Platform) MarkPresent(keys ...string) {
	if p.present == nil {
		// present 为 nil 表示规范对象，本来就输出全部已知键，无需记录。
		return
	}
	for _, key := range keys {
		p.present[key] = true
	}
}

// marshalNoHTMLEscape 序列化但不转义 `<` `>` `&`（对齐 JSON.stringify）。
func marshalNoHTMLEscape(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimRight(buf.Bytes(), "\n"), nil
}

// PresetField 是预设模板里的一个可填字段。
type PresetField struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Placeholder string `json:"placeholder"`
}

// Preset 是预设的存储形态（仅存用户自定义 / 对内置预设的覆盖）。
type Preset struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Fields            []PresetField     `json:"fields"`
	Method            string            `json:"method"`
	URLTemplate       string            `json:"urlTemplate"`
	HeadersTemplate   map[string]string `json:"headersTemplate"`
	ExtractorTemplate string            `json:"extractorTemplate"`
	CreatedAt         string            `json:"createdAt,omitempty"`
}

// PresetView 是预设对外输出形态（合并内置 + 用户，带 builtin/edited 标记）。
type PresetView struct {
	ID                string            `json:"id"`
	Name              string            `json:"name"`
	Fields            []PresetField     `json:"fields"`
	Method            string            `json:"method"`
	URLTemplate       string            `json:"urlTemplate"`
	HeadersTemplate   map[string]string `json:"headersTemplate"`
	ExtractorTemplate string            `json:"extractorTemplate"`
	CreatedAt         string            `json:"createdAt,omitempty"`
	Builtin           bool              `json:"builtin"`
	Edited            bool              `json:"edited,omitempty"`
}

// Settings 是运行时设置。
type Settings struct {
	// CollectIntervalSeconds 为自动采集间隔（秒），0 表示关闭。
	CollectIntervalSeconds int `json:"collectIntervalSeconds"`
}

// SamplePoint 是一个历史采样点（纯数值，展示文本由 format 实时渲染）。
type SamplePoint struct {
	V float64 `json:"v"`
	T string  `json:"t"`
}

// LogEntry 是一条操作日志。
type LogEntry struct {
	ID           string         `json:"id"`
	Time         string         `json:"time"`
	Action       string         `json:"action"`
	Detail       string         `json:"detail"`
	PlatformID   *string        `json:"platformId"`
	PlatformName *string        `json:"platformName"`
	Meta         map[string]any `json:"meta,omitempty"`
}

// BalanceCard 是面板上一张平台卡片。
type BalanceCard struct {
	ID        string   `json:"id"`
	Name      string   `json:"name"`
	URL       string   `json:"url"`
	Value     *float64 `json:"value"`
	Format    *string  `json:"format"`
	Error     *string  `json:"error"`
	FetchedAt *string  `json:"fetchedAt"`
}

// Dashboard 是面板响应（含采集运行态字段）。
type Dashboard struct {
	UpdatedAt     string        `json:"updatedAt"`
	Platforms     []BalanceCard `json:"platforms"`
	Collecting    bool          `json:"collecting"`
	LastRunAt     *string       `json:"lastRunAt"`
	LastRunReason *string       `json:"lastRunReason"`
}

// HistoryView 是折线图数据（时间升序，与旧实现一致）。
type HistoryView struct {
	ID     string        `json:"id"`
	Name   string        `json:"name"`
	Points []SamplePoint `json:"points"`
}

// ExportPayload 是配置导出文件。
type ExportPayload struct {
	Type       string     `json:"type"`
	Version    int        `json:"version"`
	ExportedAt string     `json:"exportedAt"`
	Platforms  []Platform `json:"platforms"`
	Presets    []Preset   `json:"presets"`
}
