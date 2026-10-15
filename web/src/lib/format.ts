// 展示格式化工具 (语义与旧 app.js 一致)

import type { Platform } from '@/types'

export const fmt = (v: unknown): string => {
  if (v == null) return '—'
  if (typeof v === 'object') return JSON.stringify(v)
  if (typeof v === 'number' && Number.isNaN(v)) return String(v)
  const n = Number(v)
  if (Number.isNaN(n)) return String(v)
  return n % 1 === 0 ? n.toString() : n.toFixed(2)
}

// format 源码 -> 函数, 带编译缓存(同一平台源码复用编译结果; 上限 50 防极端内存)
const formatFnCache = new Map<string, (v: number) => unknown>()
const FORMAT_FN_CACHE_LIMIT = 50

export function runFormatOnClient(src: string, v: number): string | null {
  let fn = formatFnCache.get(src)
  if (!fn) {
    try {
      fn = new Function(`return (${src})`)() as (v: number) => unknown
      if (formatFnCache.size >= FORMAT_FN_CACHE_LIMIT) {
        formatFnCache.delete(formatFnCache.keys().next().value as string)
      }
      formatFnCache.set(src, fn)
    } catch {
      return null
    }
  }
  try {
    const t = fn(v)
    return typeof t === 'string' ? t : null
  } catch {
    return null
  }
}

// 数据精度自适应格式化(折线图 hover/涨跌统计用): 整数原样; 非整数最多保留 6 位小数并去尾零。
// 0.5849 -> '0.5849'; 28.12 -> '28.12'; 1.5 -> '1.5'; 12 -> '12'; 12.3456789 -> '12.345679'; 0.1+0.2 -> '0.3'
export const fmtAuto = (v: number): string => {
  if (typeof v !== 'number' || !Number.isFinite(v)) return typeof v === 'number' ? String(v) : '—'
  const n = Number(v.toFixed(6))
  if (Number.isInteger(n)) return String(n)
  let s = n.toFixed(6)
  s = s.replace(/0+$/, '')
  if (s.endsWith('.')) s = s.slice(0, -1)
  return s
}

export const formatValue = (v: unknown): string => {
  if (v === undefined || v === null) return '（无返回值）'
  if (typeof v === 'object') return JSON.stringify(v, null, 2)
  if (typeof v === 'number' && Number.isNaN(v)) return '（NaN，请检查提取函数）'
  return String(v)
}

// 模板渲染: {{key}} 仅在对应变量非空时替换, 否则保留占位符
export function renderTemplate<T>(tpl: T, vars: Record<string, string>): T {
  if (typeof tpl === 'string') {
    return tpl.replace(/\{\{(\w+)\}\}/g, (m, k: string) =>
      vars[k] !== undefined && vars[k] !== '' ? vars[k] : m,
    ) as T
  }
  if (Array.isArray(tpl)) return tpl.map((x) => renderTemplate(x, vars)) as T
  if (tpl && typeof tpl === 'object') {
    const out: Record<string, unknown> = {}
    for (const [k, v] of Object.entries(tpl as Record<string, unknown>)) {
      out[k] = renderTemplate(v, vars)
    }
    return out as T
  }
  return tpl
}

export const DEFAULT_HANDLER = 'function (raw) {\n  return JSON.parse(raw).balance\n}'

// 处理函数展示: 新模型直接取 handler; 旧配置(parse/extractor)自动生成等效合并版
export function buildHandler(p: Platform): string {
  if (p.handler) return p.handler
  const lines: string[] = []
  if (p.parse) {
    lines.push(`var data = (${String(p.parse).trim()})(raw)`)
  } else {
    lines.push('var data = JSON.parse(raw)')
  }
  if (p.extractor) {
    lines.push(`return (${String(p.extractor).trim()})(data)`)
  } else {
    lines.push('return data')
  }
  return `function (raw) {\n  ${lines.join('\n  ')}\n}`
}

// 更旧的 response.path 配置 → 等效处理函数
export function handlerFromLegacyResponse(
  response: { path?: string; divider?: number } | undefined,
): string | null {
  if (!response || !response.path) return null
  const path = response.path
  const expr = /^(data\.|data\[)/.test(path) ? path : `data.${path}`
  return `function (raw) {\n  var data = JSON.parse(raw)\n  return ${expr}${response.divider ? ' / ' + response.divider : ''}\n}`
}

// 平台展示用处理函数: handler > parse/extractor 合成 > response.path > 默认
export function resolveHandler(p: Platform): string {
  if (p.handler) return p.handler
  if (p.parse || p.extractor) return buildHandler(p)
  return handlerFromLegacyResponse(p.response) ?? DEFAULT_HANDLER
}
