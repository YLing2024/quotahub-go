// Package model 定义跨层共享的领域类型（平台 / 预设 / 设置 / 采样点 / 日志 / 面板）。
//
// 字段与 JSON 键逐条对齐旧实现 server/src/types.ts；平台 request/response/display 保持
// 松散对象形态（旧实现原样展开用户配置，键集合不固定）。展示文本由前端在渲染时执行
// platform.format 源码得到，后端只透传源码（纯数值语义）。
package model

// Platform 是平台配置的存储形态（含 legacy 字段 display / response.prefix/suffix）。
type Platform struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Request   map[string]any `json:"request"`
	Handler   string         `json:"handler"`
	Extractor string         `json:"extractor"`
	Parse     string         `json:"parse"`
	Format    string         `json:"format"`
	Response  map[string]any `json:"response,omitempty"`
	// Display 已弃用，仅为旧导出文件导入兼容保留，不渲染、不再被导出。
	Display   map[string]any `json:"display,omitempty"`
	URL       string         `json:"url"`
	CreatedAt string         `json:"createdAt"`
}

// PublicPlatform 是平台对外输出形态（不含 display，response 已剔除 prefix/suffix）。
type PublicPlatform struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Request   map[string]any `json:"request"`
	Handler   string         `json:"handler"`
	Extractor string         `json:"extractor"`
	Parse     string         `json:"parse"`
	Format    string         `json:"format"`
	Response  map[string]any `json:"response,omitempty"`
	URL       string         `json:"url"`
	CreatedAt string         `json:"createdAt"`
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
