// Package timex 提供与旧实现一致的 UTC ISO 时间字符串格式化。
//
// 旧实现全程使用 `new Date().toISOString()`：毫秒精度、固定 3 位小数、以 `Z` 结尾。
// Go 的 time.RFC3339Nano 会省掉末尾的 0，故这里自定义格式，保证与旧实现逐字一致
// （history_samples.time 与 sessions.expires_at 都依赖字典序比较，格式必须稳定）。
package timex

import "time"

// layout 是 ISO 8601 毫秒精度布局（UTC 下末尾为 Z）。
const layout = "2006-01-02T15:04:05.000Z"

// ISO 把时间格式化为 UTC ISO 字符串。
func ISO(t time.Time) string { return t.UTC().Format(layout) }

// NowISO 返回当前时刻的 UTC ISO 字符串。
func NowISO() string { return ISO(time.Now()) }
