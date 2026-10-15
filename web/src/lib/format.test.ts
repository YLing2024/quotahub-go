import { describe, expect, it } from 'vitest'
import {
  DEFAULT_HANDLER,
  buildHandler,
  fmt,
  fmtAuto,
  formatValue,
  renderTemplate,
  resolveHandler,
  runFormatOnClient,
} from './format'
import type { Platform } from '@/types'

const basePlatform = (over: Partial<Platform>): Platform => ({
  id: 'id1',
  name: 'P',
  request: { method: 'GET', url: 'https://api.example.com', headers: {} },
  handler: '',
  createdAt: '2026-01-01T00:00:00Z',
  ...over,
})

describe('fmt', () => {
  it('null/undefined → —', () => {
    expect(fmt(null)).toBe('—')
    expect(fmt(undefined)).toBe('—')
  })
  it('整数不带小数, 小数保留两位', () => {
    expect(fmt(8)).toBe('8')
    expect(fmt(8.5)).toBe('8.50')
    expect(fmt(18.000001)).toBe('18.00')
  })
  it('数字字符串走数值格式化', () => {
    expect(fmt('12')).toBe('12')
    expect(fmt('3.14159')).toBe('3.14')
  })
  it('非数字字符串原样返回', () => {
    expect(fmt('abc')).toBe('abc')
  })
  it('对象 JSON 序列化', () => {
    expect(fmt({ a: 1 })).toBe('{"a":1}')
  })
})

describe('fmtAuto (折线图数据精度自适应)', () => {
  it('最多保留 6 位小数并去尾零', () => {
    expect(fmtAuto(0.5849)).toBe('0.5849')
    expect(fmtAuto(28.12)).toBe('28.12')
    expect(fmtAuto(1.5)).toBe('1.5')
    expect(fmtAuto(92.0481)).toBe('92.0481')
  })
  it('整数原样不带小数位', () => {
    expect(fmtAuto(12)).toBe('12')
    expect(fmtAuto(0)).toBe('0')
    expect(fmtAuto(-7)).toBe('-7')
  })
  it('超出 6 位时四舍五入到 6 位', () => {
    expect(fmtAuto(12.3456789)).toBe('12.345679')
  })
  it('浮点误差经 6 位定点收敛', () => {
    expect(fmtAuto(0.1 + 0.2)).toBe('0.3')
    expect(fmtAuto(19.07)).toBe('19.07')
  })
})

describe('formatValue', () => {
  it('空值提示', () => {
    expect(formatValue(undefined)).toBe('（无返回值）')
    expect(formatValue(null)).toBe('（无返回值）')
  })
  it('NaN 提示检查提取函数', () => {
    expect(formatValue(Number.NaN)).toBe('（NaN，请检查提取函数）')
  })
  it('对象缩进序列化', () => {
    expect(formatValue({ a: 1 })).toBe('{\n  "a": 1\n}')
  })
  it('数值与字符串', () => {
    expect(formatValue(12.5)).toBe('12.5')
    expect(formatValue('ok')).toBe('ok')
  })
})

describe('runFormatOnClient (format 源码 -> 展示文本, 前端渲染)', () => {
  it('合法函数 + 拼接单位 -> 正确文本', () => {
    expect(runFormatOnClient('function (v) { return v.toFixed(2) + "元" }', 28.12)).toBe('28.12元')
    expect(runFormatOnClient('function (v) { return "¥" + v.toFixed(2) }', 19.07)).toBe('¥19.07')
  })
  it('箭头函数源码同样支持', () => {
    expect(runFormatOnClient('(v) => v.toFixed(4) + "G"', 0.5849)).toBe('0.5849G')
  })
  it('语法错误源码 -> null', () => {
    expect(runFormatOnClient('function (v) { return ))) }', 1)).toBeNull()
  })
  it('执行抛错 -> null', () => {
    expect(runFormatOnClient('function (v) { throw new Error("x") }', 1)).toBeNull()
  })
  it('返回非字符串(数字/对象)-> null', () => {
    expect(runFormatOnClient('function (v) { return v }', 5)).toBeNull()
    expect(runFormatOnClient('function (v) { return 123 }', 5)).toBeNull()
    expect(runFormatOnClient('function (v) { return ({ a: 1 }) }', 5)).toBeNull()
  })
  it('空串/空源码 -> null', () => {
    expect(runFormatOnClient('', 1)).toBeNull()
    expect(runFormatOnClient('   ', 1)).toBeNull()
  })
  it('缓存命中路径: 同一 src 二次调用复用编译结果', () => {
    const src = 'function (v) { return v.toFixed(1) + "%" }'
    expect(runFormatOnClient(src, 20.6)).toBe('20.6%')
    expect(runFormatOnClient(src, 33.3)).toBe('33.3%')
    expect(runFormatOnClient(src, 20.6)).toBe('20.6%')
  })
})

describe('renderTemplate', () => {
  it('替换非空变量', () => {
    expect(renderTemplate('{{baseUrl}}/api/user/self', { baseUrl: 'https://x.io' })).toBe(
      'https://x.io/api/user/self',
    )
  })
  it('空/缺失变量保留占位符', () => {
    expect(renderTemplate('{{a}}-{{b}}', { a: '' })).toBe('{{a}}-{{b}}')
    expect(renderTemplate('{{missing}}', {})).toBe('{{missing}}')
  })
  it('深度对象与数组递归渲染', () => {
    const out = renderTemplate(
      { url: '{{u}}', list: ['{{v}}'], keep: 1 },
      { u: 'U', v: 'V' },
    )
    expect(out).toEqual({ url: 'U', list: ['V'], keep: 1 })
  })
})

describe('buildHandler / resolveHandler', () => {
  it('优先使用 handler', () => {
    const p = basePlatform({ handler: 'function (raw) { return 1 }', parse: 'x', extractor: 'y' })
    expect(resolveHandler(p)).toBe('function (raw) { return 1 }')
  })

  it('parse+extractor 合成等效处理函数', () => {
    const p = basePlatform({ parse: '(s)=>JSON.parse(s)', extractor: '(d)=>d.balance' })
    expect(buildHandler(p)).toBe(
      'function (raw) {\n  var data = ((s)=>JSON.parse(s))(raw)\n  return ((d)=>d.balance)(data)\n}',
    )
  })

  it('仅 extractor 时默认 JSON.parse', () => {
    const p = basePlatform({ extractor: '(d)=>d.b' })
    expect(buildHandler(p)).toContain('var data = JSON.parse(raw)')
    expect(buildHandler(p)).toContain('return ((d)=>d.b)(data)')
  })

  it('更旧的 response.path 配置生成等效处理函数', () => {
    const p = basePlatform({ response: { path: 'data.balance', divider: 2 } as never })
    expect(resolveHandler(p)).toBe(
      'function (raw) {\n  var data = JSON.parse(raw)\n  return data.balance / 2\n}',
    )
  })

  it('response.path 自动补 data. 前缀', () => {
    const p = basePlatform({ response: { path: 'balance' } as never })
    expect(resolveHandler(p)).toContain('return data.balance\n')
  })

  it('path 已带 data. 或 data[ 前缀时不重复补', () => {
    const p1 = basePlatform({ response: { path: 'data.a.b' } as never })
    expect(resolveHandler(p1)).toContain('return data.a.b\n')
    const p2 = basePlatform({ response: { path: 'data["k"]' } as never })
    expect(resolveHandler(p2)).toContain('return data["k"]\n')
  })

  it('什么都没有时回退默认处理函数', () => {
    expect(resolveHandler(basePlatform({}))).toBe(DEFAULT_HANDLER)
    expect(DEFAULT_HANDLER).toBe('function (raw) {\n  return JSON.parse(raw).balance\n}')
  })
})
