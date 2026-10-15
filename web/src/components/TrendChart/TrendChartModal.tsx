/**
 * 余额趋势图弹窗：antd Modal + ECharts 折线图（懒加载）。
 *
 * 关键 props：
 * - `id`（string，必填）：平台 ID，用于拉取 `/api/platforms/:id/history`。
 * - `name`（string，必填）：平台名，用于标题。
 * - `onClose`（`() => void`）：关闭弹窗。
 *
 * 行为：工具栏时间范围档位（24 小时 / 7 天 / 30 天 / 一年 / 所有 / 自定义）切换即换区间；
 * 主图折线由 ECharts 渲染，底部 dataZoom 滑条选择 / 平移窗口（等价旧预览条）；空态 / 涨跌
 * 统计 / 自定义区间校验文案与旧实现逐字一致。
 * 注意：纯逻辑在 chartLogic.ts（可单测），本文件只做弹层与窗口编排。
 * 可访问性：antd Modal 焦点陷阱与 Esc 关闭；档位用 Segmented（可键盘选择）。
 */
import { Suspense, lazy, useEffect, useMemo, useState } from 'react'
import { Button, Flex, Input, Modal, Segmented, Skeleton, Space, Typography } from 'antd'
import { getHistory } from '@/api/endpoints'
import { fmtAuto } from '@/lib/format'
import type { SamplePoint } from '@/types'
import {
  clampWindow,
  filterPointsByWindow,
  parseLocalInputValue,
  presetWindow,
  toLocalInputValue,
  type RangePreset,
  type TimeWindow,
} from './chartLogic'

// ECharts 渲染组件动态载入: echarts 不进主入口 chunk, 加载中给 Skeleton。
const TrendChartECharts = lazy(() => import('./TrendChartECharts'))

// 工具栏时间范围档位(24小时/7天/30天/一年/所有/自定义)
const RANGE_PRESETS: Array<{ key: RangePreset; label: string }> = [
  { key: 'h24', label: '24小时' },
  { key: 'd7', label: '7天' },
  { key: 'd30', label: '30天' },
  { key: 'year', label: '一年' },
  { key: 'all', label: '所有' },
  { key: 'custom', label: '自定义' },
]

// 打开弹窗的默认档位
const DEFAULT_PRESET: RangePreset = 'd30'

interface Props {
  id: string
  name: string
  onClose(): void
}

export default function TrendChartModal({ id, name, onClose }: Props) {
  const [points, setPoints] = useState<SamplePoint[]>([])
  // 数据域(全量采样点的时间跨度), 供档位计算与自定义输入上下限
  const [domain, setDomain] = useState<TimeWindow | null>(null)
  const [preset, setPreset] = useState<RangePreset>(DEFAULT_PRESET)
  // 主图窗口边界(ms 时间戳) —— 即 dataZoom 选中的那一段
  const [win, setWin] = useState<TimeWindow>({ start: 0, end: 0 })
  const [loading, setLoading] = useState(true)
  const [loadError, setLoadError] = useState('')
  const [customStart, setCustomStart] = useState('')
  const [customEnd, setCustomEnd] = useState('')
  const [customError, setCustomError] = useState('')

  // ---- 数据加载: 排序后按默认档位初始化窗口与数据域 ----
  useEffect(() => {
    let cancelled = false
    setLoading(true)
    setLoadError('')
    getHistory(id)
      .then((data) => {
        if (cancelled) return
        const pts = ((data && data.points) || [])
          .slice()
          .sort((a, b) => new Date(a.t).getTime() - new Date(b.t).getTime())
        const times = pts.map((p) => new Date(p.t).getTime())
        const t0 = times.length ? Math.min(...times) : 0
        const t1 = times.length ? Math.max(...times) : 0
        // 打开弹窗默认落在默认档(数据不足该跨度时自动退化为全长)
        const w = times.length ? presetWindow(DEFAULT_PRESET, t0, t1, Date.now()) : { start: 0, end: 0 }
        setPoints(pts)
        setDomain(times.length ? { start: t0, end: t1 } : null)
        setWin(w)
        setPreset(DEFAULT_PRESET)
        setCustomError('')
        setCustomStart(times.length ? toLocalInputValue(w.start) : '')
        setCustomEnd(times.length ? toLocalInputValue(w.end) : '')
      })
      .catch((err: Error) => {
        if (cancelled) return
        setPoints([])
        setDomain(null)
        setWin({ start: 0, end: 0 })
        setPreset(DEFAULT_PRESET)
        setCustomError('')
        setCustomStart('')
        setCustomEnd('')
        setLoadError(err.message)
      })
      .finally(() => {
        if (!cancelled) setLoading(false)
      })
    return () => {
      cancelled = true
    }
  }, [id])

  // 当前窗口内的采样点(与旧实现同一筛选口径)
  const windowed = useMemo(() => filterPointsByWindow(points, win.start, win.end), [points, win])

  // 统计摘要(涨跌): 文案与旧实现逐字一致
  const stats = useMemo(() => {
    if (loading) return '加载中…'
    if (loadError) return `加载失败: ${loadError}`
    if (!windowed.length) return '暂无数据'
    const first = windowed[0].v
    const lastV = windowed[windowed.length - 1].v
    const diff = lastV - first
    const diffPct = first !== 0 ? (diff / Math.abs(first)) * 100 : 0
    const arrow = diff > 0 ? '↑' : diff < 0 ? '↓' : '→'
    return `${windowed.length} 个采样点 · ${arrow} ${diff >= 0 ? '+' : ''}${fmtAuto(diff)} (${diffPct >= 0 ? '+' : ''}${fmtAuto(diffPct)}%)`
  }, [loading, loadError, windowed])

  // ---- 时间范围档位: 预设 = 计算窗口并应用 ----
  const selectPreset = (key: RangePreset) => {
    if (!domain) return
    if (key === 'custom') {
      // 自定义档: 以当前窗口为准, 输入框回填供微调
      setCustomStart(toLocalInputValue(win.start))
      setCustomEnd(toLocalInputValue(win.end))
      setCustomError('')
      setPreset('custom')
      return
    }
    const w = presetWindow(key, domain.start, domain.end, Date.now())
    setWin(w)
    setCustomError('')
    setPreset(key)
  }

  const applyCustomRange = () => {
    if (!domain) return
    const start = parseLocalInputValue(customStart)
    const end = parseLocalInputValue(customEnd)
    if (start == null || end == null) {
      setCustomError('请填写完整的起止时间')
      return
    }
    if (start >= end) {
      setCustomError('起始时间需早于结束时间')
      return
    }
    // 夹紧到数据域; 完全落在域外会塌缩成空窗口 → 提示无数据
    const w = clampWindow({ start, end }, domain.start, domain.end)
    if (w.end <= w.start) {
      setCustomError('所选区间内没有采样数据')
      return
    }
    setWin(w)
    setCustomError('')
  }

  return (
    <Modal
      open
      title={`余额变化趋势 — ${name}`}
      onCancel={onClose}
      width={880}
      footer={<Button onClick={onClose}>关闭</Button>}
    >
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Flex align="center" gap={12} wrap>
          <Segmented
            value={preset}
            disabled={!domain}
            onChange={(v) => selectPreset(v as RangePreset)}
            options={RANGE_PRESETS.map((p) => ({ value: p.key, label: p.label }))}
          />
          <Typography.Text type="secondary" style={{ marginLeft: 'auto' }}>
            {stats}
          </Typography.Text>
        </Flex>

        {preset === 'custom' && domain && (
          <Flex align="center" gap={10} wrap>
            <Space size={6}>
              <Typography.Text type="secondary">起</Typography.Text>
              <Input
                type="datetime-local"
                value={customStart}
                min={toLocalInputValue(domain.start)}
                max={toLocalInputValue(domain.end)}
                onChange={(e) => {
                  setCustomStart(e.target.value)
                  setCustomError('')
                }}
              />
            </Space>
            <Space size={6}>
              <Typography.Text type="secondary">止</Typography.Text>
              <Input
                type="datetime-local"
                value={customEnd}
                min={toLocalInputValue(domain.start)}
                max={toLocalInputValue(domain.end)}
                onChange={(e) => {
                  setCustomEnd(e.target.value)
                  setCustomError('')
                }}
              />
            </Space>
            <Button onClick={applyCustomRange}>应用</Button>
            {customError ? <Typography.Text type="danger">{customError}</Typography.Text> : null}
          </Flex>
        )}

        <div title="拖动窗口两端或内部，选择主图时间范围">
          {loading ? (
            <Skeleton active paragraph={{ rows: 6 }} />
          ) : (
            <Suspense fallback={<Skeleton active paragraph={{ rows: 6 }} />}>
              <TrendChartECharts
                points={points}
                winStart={win.start}
                winEnd={win.end}
                onWindowChange={(start, end) => setWin({ start, end })}
              />
            </Suspense>
          )}
        </div>
      </Space>
    </Modal>
  )
}
