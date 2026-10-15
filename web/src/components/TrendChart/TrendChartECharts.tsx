/**
 * ECharts 余额趋势折线图（按需注册 + 懒加载）。
 *
 * 关键 props：
 * - `points`（`SamplePoint[]`，必填）：时间升序采样点 `{t, v}`。
 * - `winStart` / `winEnd`（number，必填）：当前主图窗口边界（ms 时间戳）。
 * - `onWindowChange`（`(start: number, end: number) => void`）：用户拖动底部 dataZoom 滑条时回传新窗口。
 *
 * 注意：本文件被 TrendChartModal 动态 import，使 echarts 成为独立 chunk、不进主入口；只负责
 * 把采样点画成折线并联动时间窗口，主题 / 尺寸 / 实例生命周期都在这里管理。颜色取自 antd
 * token（不写死色值）。
 * 可访问性：图表为辅助可视化，数值同时以 tooltip 文本呈现；图表本身不承担键盘交互。
 */
import { useEffect, useRef } from 'react'
import * as echarts from 'echarts/core'
import { LineChart } from 'echarts/charts'
import {
  DataZoomComponent,
  GridComponent,
  LegendComponent,
  TitleComponent,
  TooltipComponent,
} from 'echarts/components'
import { CanvasRenderer } from 'echarts/renderers'
import { theme } from 'antd'
import { fmtAuto } from '@/lib/format'
import { filterPointsByWindow } from './chartLogic'
import type { SamplePoint } from '@/types'

// 按需注册: 只引入折线图 + 网格/提示/图例/缩放/标题 + Canvas 渲染器, 不 import 整包。
echarts.use([
  LineChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  DataZoomComponent,
  TitleComponent,
  CanvasRenderer,
])

// 图表高度(px): 在容器 inline 尺寸里给出, 避免依赖旧 CSS。
const CHART_HEIGHT = 360

interface Props {
  points: SamplePoint[]
  winStart: number
  winEnd: number
  onWindowChange(start: number, end: number): void
}

// 主题 token 取色: 追加透明度生成 8 位 hex 渐变色, 源码中不写死任何具体色值。
function withAlpha(color: string, alpha: number): string {
  const hex = color.replace('#', '')
  const a = Math.round(Math.max(0, Math.min(1, alpha)) * 255)
    .toString(16)
    .padStart(2, '0')
  return `#${hex}${a}`
}

interface TooltipParam {
  value: [number, number]
}

export default function TrendChartECharts({ points, winStart, winEnd, onWindowChange }: Props) {
  const { token } = theme.useToken()
  const containerRef = useRef<HTMLDivElement>(null)
  const chartRef = useRef<echarts.ECharts | null>(null)
  // 闭包最新值: dataZoom 事件回调里读取, 避免重复绑定监听
  const domainRef = useRef({ t0: 0, t1: 0 })
  const winRef = useRef({ start: winStart, end: winEnd })
  const onWinChangeRef = useRef(onWindowChange)
  useEffect(() => {
    winRef.current = { start: winStart, end: winEnd }
  }, [winStart, winEnd])
  useEffect(() => {
    onWinChangeRef.current = onWindowChange
  }, [onWindowChange])

  // 实例生命周期: 挂载初始化 + 尺寸自适应; 卸载必须 dispose, 不泄漏。
  useEffect(() => {
    const el = containerRef.current
    if (!el) return
    const chart = echarts.init(el)
    chartRef.current = chart

    const onDataZoom = () => {
      const opt = chart.getOption() as {
        dataZoom?: Array<{ start?: number; end?: number; startValue?: number; endValue?: number }>
      }
      const dz = opt.dataZoom?.[0]
      if (!dz) return
      const { t0, t1 } = domainRef.current
      let start: number
      let end: number
      if (typeof dz.startValue === 'number' && typeof dz.endValue === 'number') {
        start = dz.startValue
        end = dz.endValue
      } else {
        const span = t1 - t0
        start = t0 + (span * (dz.start ?? 0)) / 100
        end = t0 + (span * (dz.end ?? 100)) / 100
      }
      if (!Number.isFinite(start) || !Number.isFinite(end)) return
      const cur = winRef.current
      if (Math.abs(start - cur.start) < 1 && Math.abs(end - cur.end) < 1) return
      onWinChangeRef.current(start, end)
    }
    chart.on('dataZoom', onDataZoom)

    // 容器尺寸变化(窗口缩放/布局变化) → resize, 保证画布不拉伸。
    const ro = new ResizeObserver(() => chart.resize())
    ro.observe(el)
    const raf = requestAnimationFrame(() => chart.resize())

    return () => {
      cancelAnimationFrame(raf)
      ro.disconnect()
      chart.off('dataZoom', onDataZoom)
      chart.dispose()
      chartRef.current = null
    }
  }, [])

  // 数据/窗口/主题变化时重建 option(深色浅色切换靠 token 变化触发重设)。
  useEffect(() => {
    const chart = chartRef.current
    if (!chart) return

    const times = points.map((p) => new Date(p.t).getTime())
    const t0 = times.length ? Math.min(...times) : 0
    const t1 = times.length ? Math.max(...times) : 0
    domainRef.current = { t0, t1 }

    const axisLabel = { color: token.colorTextSecondary }
    const axisLine = { lineStyle: { color: token.colorBorderSecondary } }

    // 空态: 无历史数据 / 当前窗口内无采样点, 文案与旧实现一致。
    const windowed = filterPointsByWindow(points, winStart, winEnd)
    if (!points.length || !windowed.length) {
      chart.setOption(
        {
          title: {
            text: points.length ? '当前窗口内无采样点' : '暂无余额历史数据（获取余额后自动记录采样点）',
            left: 'center',
            top: 'middle',
            textStyle: { color: token.colorTextSecondary, fontSize: token.fontSize },
          },
          xAxis: { show: false },
          yAxis: { show: false },
          series: [],
          dataZoom: [],
        },
        true,
      )
      return
    }

    const data = points.map((p) => [new Date(p.t).getTime(), p.v] as [number, number])
    const xMin = t0 === t1 ? t0 - 3_600_000 : t0
    const xMax = t0 === t1 ? t1 + 3_600_000 : t1

    chart.setOption(
      {
        animation: false,
        grid: { left: 64, right: 24, top: 30, bottom: 68 },
        tooltip: {
          trigger: 'axis',
          axisPointer: { type: 'cross' },
          formatter: (params: unknown) => {
            const first = (Array.isArray(params) ? params[0] : params) as TooltipParam | undefined
            if (!first || !Array.isArray(first.value)) return ''
            const ts = first.value[0]
            const timeText = new Date(ts).toLocaleString('zh-CN', {
              month: '2-digit',
              day: '2-digit',
              hour: '2-digit',
              minute: '2-digit',
            })
            return `${fmtAuto(first.value[1])}<br/>${timeText}`
          },
        },
        xAxis: {
          type: 'time',
          min: xMin,
          max: xMax,
          axisLabel,
          axisLine,
          splitLine: { show: false },
        },
        yAxis: {
          type: 'value',
          scale: true,
          axisLabel,
          axisLine: { show: false },
          splitLine: { lineStyle: { color: token.colorSplit } },
        },
        dataZoom: [
          { type: 'inside', xAxisIndex: 0, startValue: winStart, endValue: winEnd },
          {
            type: 'slider',
            xAxisIndex: 0,
            startValue: winStart,
            endValue: winEnd,
            height: 32,
            bottom: 12,
          },
        ],
        series: [
          {
            type: 'line',
            name: '余额',
            data,
            showSymbol: points.length <= 120,
            symbolSize: 4,
            lineStyle: { color: token.colorPrimary, width: 2 },
            itemStyle: { color: token.colorPrimary },
            areaStyle: {
              color: {
                type: 'linear',
                x: 0,
                y: 0,
                x2: 0,
                y2: 1,
                colorStops: [
                  { offset: 0, color: withAlpha(token.colorPrimary, 0.14) },
                  { offset: 1, color: withAlpha(token.colorPrimary, 0) },
                ],
              },
            },
          },
        ],
      },
      true,
    )
  }, [
    points,
    winStart,
    winEnd,
    token.colorPrimary,
    token.colorTextSecondary,
    token.colorBorderSecondary,
    token.colorSplit,
    token.fontSize,
  ])

  return <div ref={containerRef} style={{ width: '100%', height: CHART_HEIGHT }} />
}
