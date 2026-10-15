// Package logs 提供操作日志（JSON 文件持久化 + 内存环形缓冲）。
//
// 语义对齐旧实现 services/logService.ts + repositories/logRepo.ts：最多保留 2000 条，
// 列表最新在前，写入失败只记 stderr 不打断业务；日志 id 复刻 `<毫秒36进制>-<6位36进制>`。
package logs

import (
	"log/slog"
	"path/filepath"
	"sync"
	"time"

	"quotahub-go/internal/ids"
	"quotahub-go/internal/jsonstore"
	"quotahub-go/internal/model"
	"quotahub-go/internal/timex"
)

// MaxEntries 是日志保留上限。
const MaxEntries = 2000

// Options 是日志条目的可选上下文。
type Options struct {
	PlatformID   string
	PlatformName string
	Meta         map[string]any
}

// Service 是操作日志的数据访问 + 业务逻辑。
type Service struct {
	file   string
	logger *slog.Logger
	mu     sync.Mutex
	loaded bool
	items  []model.LogEntry
}

// New 构造日志服务；dataDir 下使用 logs.json（与旧实现同名）。
func New(dataDir string, logger *slog.Logger) *Service {
	return &Service{file: filepath.Join(dataDir, "logs.json"), logger: logger}
}

// Add 追加一条日志并持久化；持久化失败只记录，不返回错误（对齐旧行为）。
func (s *Service) Add(action, detail string, opts Options) model.LogEntry {
	entry := model.LogEntry{
		ID:     ids.LogID(timeNowMillis()),
		Time:   timex.NowISO(),
		Action: action,
		Detail: detail,
	}
	if opts.PlatformID != "" {
		id := opts.PlatformID
		entry.PlatformID = &id
	}
	if opts.PlatformName != "" {
		name := opts.PlatformName
		entry.PlatformName = &name
	}
	if opts.Meta != nil {
		entry.Meta = opts.Meta
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	s.items = append(s.items, entry)
	if len(s.items) > MaxEntries {
		s.items = s.items[len(s.items)-MaxEntries:]
	}
	if err := jsonstore.WriteAtomic(s.file, s.items); err != nil && s.logger != nil {
		s.logger.Error("[logger] 写入日志失败: " + err.Error())
	}
	return entry
}

// List 返回最新在前的日志与总数；limit 钳制到 [1, MaxEntries]。
func (s *Service) List(limit int) ([]model.LogEntry, int) {
	if limit < 1 {
		limit = 1
	}
	if limit > MaxEntries {
		limit = MaxEntries
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	total := len(s.items)
	if limit > total {
		limit = total
	}
	out := make([]model.LogEntry, 0, limit)
	for i := total - 1; i >= total-limit; i-- {
		out = append(out, s.items[i])
	}
	return out, total
}

// Clear 清空日志并持久化。
func (s *Service) Clear() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.ensureLoadedLocked()
	s.items = []model.LogEntry{}
	if err := jsonstore.WriteAtomic(s.file, s.items); err != nil && s.logger != nil {
		s.logger.Error("[logger] 清空日志失败: " + err.Error())
	}
}

func (s *Service) ensureLoadedLocked() {
	if s.loaded {
		return
	}
	raw := jsonstore.ReadArray[model.LogEntry](s.file)
	if len(raw) > MaxEntries {
		raw = raw[len(raw)-MaxEntries:]
	}
	if raw == nil {
		raw = []model.LogEntry{}
	}
	s.items = raw
	s.loaded = true
}

// timeNowMillis 返回当前 epoch 毫秒（供日志 id 生成）。
func timeNowMillis() int64 { return time.Now().UnixMilli() }
