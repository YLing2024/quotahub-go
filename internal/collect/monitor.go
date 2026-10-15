// Package collect 是采集链路：SSRF 防护 + HTTP 抓取 + goja 沙箱解析，以及采集监控状态机。
//
// 语义逐条对齐旧实现 lib/fetcher.ts 与 services/monitorService.ts：
//   - 抓取超时 15s、响应上限 1MB、失败只记运行时错误态（面板呈现），绝不丢历史；
//   - 单平台历史保留最新 2000 点；「最新值」取自历史采样最新点，仅当失败晚于最新采样才显示错误；
//   - 定时采集由 internal/scheduler 统一承载，runOnce 跳过重入。
package collect

import (
	"context"
	"strconv"
	"sync"
	"time"

	"quotahub-go/internal/httperr"
	"quotahub-go/internal/jsval"
	"quotahub-go/internal/logs"
	"quotahub-go/internal/model"
	"quotahub-go/internal/platform"
	"quotahub-go/internal/scheduler"
	"quotahub-go/internal/settings"
	"quotahub-go/internal/store"
	"quotahub-go/internal/timex"
)

// Outcome 是单平台一次抓取的结果（成功带 value/fetchedAt，失败带 error）。
type Outcome struct {
	OK        bool
	Value     float64
	Error     string
	FetchedAt string
}

// RefreshResult 是 POST /api/platforms/refresh 的响应体。
type RefreshResult struct {
	OK      bool             `json:"ok"`
	Results []map[string]any `json:"results"`
}

// Monitor 承载采集业务逻辑与运行时状态（最近失败、是否在跑、最近一轮完成信息）。
type Monitor struct {
	platforms *platform.Service
	history   *store.DB
	settings  *settings.Service
	logs      *logs.Service
	fetcher   *Fetcher
	sched     *scheduler.Scheduler

	mu            sync.Mutex
	lastErrors    map[string]lastError
	running       bool
	lastRunAt     *string
	lastRunReason *string
}

type lastError struct {
	Error     string
	FetchedAt string
}

// NewMonitor 构造采集监控。fetchTimeout/脚本超时已由上层配置层钳制。
func NewMonitor(
	platforms *platform.Service,
	history *store.DB,
	settingsSvc *settings.Service,
	logsSvc *logs.Service,
	allowPrivate bool,
	scriptTimeoutMs int,
) *Monitor {
	return &Monitor{
		platforms:  platforms,
		history:    history,
		settings:   settingsSvc,
		logs:       logsSvc,
		fetcher:    NewFetcher(allowPrivate, scriptTimeoutMs),
		sched:      scheduler.New(),
		lastErrors: map[string]lastError{},
	}
}

// Validate 校验任意松散抓取配置（POST /api/platforms/validate），不写历史、不记错误态。
func (m *Monitor) Validate(ctx context.Context, target FetchTarget) (float64, error) {
	return m.fetcher.FetchBalance(ctx, target)
}

// Test 用平台存储配置做一次抓取（POST /api/platforms/:id/test），不写历史、不记错误态。
func (m *Monitor) Test(ctx context.Context, p model.Platform) (float64, error) {
	return m.fetcher.FetchBalance(ctx, TargetOf(p))
}

// Collect 抓取单个平台：成功写采样点并清除错误态；失败记录运行时错误态。
func (m *Monitor) Collect(ctx context.Context, p model.Platform) Outcome {
	value, err := m.fetcher.FetchBalance(ctx, TargetOf(p))
	if err == nil {
		fetchedAt := timex.NowISO()
		if _, recErr := m.history.RecordSample(ctx, p.ID, value, fetchedAt); recErr != nil {
			err = recErr
		} else {
			m.clearError(p.ID)
			return Outcome{OK: true, Value: value, FetchedAt: fetchedAt}
		}
	}
	message := err.Error()
	m.setError(p.ID, message)
	return Outcome{Error: message}
}

// FetchOne 抓取指定平台并记日志（POST /api/platforms/:id/fetch）；平台不存在返回 404。
func (m *Monitor) FetchOne(ctx context.Context, id string) (Outcome, error) {
	p, ok := m.platforms.Find(id)
	if !ok {
		return Outcome{}, httperr.New(404, "平台不存在")
	}
	outcome := m.Collect(ctx, p)
	if outcome.OK {
		m.logs.Add("fetch", "获取「"+p.Name+"」余额成功: "+formatValue(outcome.Value), logs.Options{
			PlatformID:   p.ID,
			PlatformName: p.Name,
			Meta:         map[string]any{"source": "manual", "value": outcome.Value},
		})
	} else {
		m.logs.Add("fetch", "获取「"+p.Name+"」余额失败: "+outcome.Error, logs.Options{
			PlatformID:   p.ID,
			PlatformName: p.Name,
			Meta:         map[string]any{"source": "manual", "error": outcome.Error},
		})
	}
	return outcome, nil
}

// RefreshAll 顺序抓取全部平台并汇总（POST /api/platforms/refresh）。
func (m *Monitor) RefreshAll(ctx context.Context) RefreshResult {
	list := m.platforms.All()
	results := make([]map[string]any, 0, len(list))
	ok, fail := 0, 0
	for _, p := range list {
		outcome := m.Collect(ctx, p)
		if outcome.OK {
			results = append(results, map[string]any{"id": p.ID, "ok": true, "value": outcome.Value})
			ok++
		} else {
			results = append(results, map[string]any{"id": p.ID, "ok": false, "error": outcome.Error})
			fail++
		}
	}
	m.logs.Add("refresh", "手动刷新完成: 成功 "+strconv.Itoa(ok)+" 个, 失败 "+strconv.Itoa(fail)+" 个", logs.Options{
		Meta: map[string]any{"source": "manual", "ok": ok, "fail": fail},
	})
	m.markRunFinished("manual")
	return RefreshResult{OK: true, Results: results}
}

// RunOnce 执行一轮定时采集；已有一轮在跑则跳过（等价旧实现 monitor.runOnce 的防重入）。
func (m *Monitor) RunOnce(ctx context.Context, reason string) {
	m.mu.Lock()
	if m.running {
		m.mu.Unlock()
		return
	}
	m.running = true
	m.mu.Unlock()

	defer func() {
		m.mu.Lock()
		m.running = false
		m.markRunFinishedLocked(reason)
		m.mu.Unlock()
	}()

	list := m.platforms.All()
	if len(list) == 0 {
		return
	}
	ok, fail := 0, 0
	for _, p := range list {
		if outcome := m.Collect(ctx, p); outcome.OK {
			ok++
		} else {
			fail++
		}
	}
	if reason == "" {
		reason = "schedule"
	}
	m.logs.Add("fetch", "自动采集完成: 成功 "+strconv.Itoa(ok)+" 个, 失败 "+strconv.Itoa(fail)+" 个", logs.Options{
		Meta: map[string]any{"source": "auto", "reason": reason, "ok": ok, "fail": fail},
	})
}

// Start 按当前设置启动定时采集（间隔 <= 0 视为关闭）。
func (m *Monitor) Start() { m.Reschedule() }

// Reschedule 设置变更后重新排程。
func (m *Monitor) Reschedule() {
	sec := m.settings.Get().CollectIntervalSeconds
	if sec <= 0 {
		m.sched.Schedule(0, nil)
		return
	}
	m.sched.Schedule(time.Duration(sec)*time.Second, func() {
		m.RunOnce(context.Background(), "schedule")
	})
}

// Stop 停止调度器（进程退出时调用）。
func (m *Monitor) Stop() { m.sched.Stop() }

// BuildDashboard 组装面板数据（GET /api/platforms/balances）。
func (m *Monitor) BuildDashboard(ctx context.Context) (model.Dashboard, error) {
	list := m.platforms.All()
	cards := make([]model.BalanceCard, 0, len(list))
	for _, p := range list {
		latest, err := m.history.LatestSample(ctx, p.ID)
		if err != nil {
			return model.Dashboard{}, err
		}
		card := model.BalanceCard{ID: p.ID, Name: p.Name, URL: p.URL}
		if failing, ok := m.getError(p.ID); ok && (latest == nil || failing.FetchedAt > latest.T) {
			msg, at := failing.Error, failing.FetchedAt
			card.Error = &msg
			card.FetchedAt = &at
		} else if latest != nil {
			value, at := latest.V, latest.T
			card.Value = &value
			card.FetchedAt = &at
		}
		if p.Format != "" {
			format := p.Format
			card.Format = &format
		}
		cards = append(cards, card)
	}
	m.mu.Lock()
	collecting := m.running
	lastRunAt := m.lastRunAt
	lastRunReason := m.lastRunReason
	m.mu.Unlock()
	return model.Dashboard{
		UpdatedAt:     timex.NowISO(),
		Platforms:     cards,
		Collecting:    collecting,
		LastRunAt:     lastRunAt,
		LastRunReason: lastRunReason,
	}, nil
}

// --- 内部 ---

func (m *Monitor) setError(id, message string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.lastErrors[id] = lastError{Error: message, FetchedAt: timex.NowISO()}
}

func (m *Monitor) clearError(id string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	delete(m.lastErrors, id)
}

func (m *Monitor) getError(id string) (lastError, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	e, ok := m.lastErrors[id]
	return e, ok
}

// markRunFinished 记录一轮采集完成（供手动刷新调用）。
func (m *Monitor) markRunFinished(reason string) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.markRunFinishedLocked(reason)
}

func (m *Monitor) markRunFinishedLocked(reason string) {
	if reason == "" {
		reason = "schedule"
	}
	at := timex.NowISO()
	m.lastRunAt = &at
	m.lastRunReason = &reason
}

// TargetOf 把平台存储形态映射为抓取目标。
func TargetOf(p model.Platform) FetchTarget {
	return FetchTarget{
		Request:   p.Request,
		Handler:   p.Handler,
		Extractor: p.Extractor,
		Parse:     p.Parse,
		Response:  p.Response,
	}
}

// formatValue 复刻 JS 模板串里数值的字符串形态（整数无小数点）。
func formatValue(v float64) string { return jsval.Str(v) }
