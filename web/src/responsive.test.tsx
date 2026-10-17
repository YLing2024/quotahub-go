/// <reference types="node" />
// @vitest-environment jsdom
/**
 * 触摸/窄屏响应式断言测试。
 *
 * jsdom 不做真实排版、量不到命中区尺寸，因此这里按需求书允许的口径，改为对**样式与 class**
 * 做断言：解析 `styles/responsive-mobile.css` 的触摸断点规则并逐条校验（主要按钮 ≥44px、
 * 紧凑按钮 ≥32×32、表单控件 ≥44px 且字号 ≥16px、无 <12px 字号、行内操作间距 ≥8px），
 * 再真实渲染带 `qh-actions` 标记的组件，确认标记 class 落在 antd `Space` 上（间距规则才生效）。
 * 真浏览器 390×844 的量测结果见交付报告（含原始数值）。
 */
import { readFileSync } from 'node:fs'
import { join } from 'node:path'
import { cleanup, render } from '@testing-library/react'
import { afterEach, beforeAll, describe, expect, it } from 'vitest'
import PlatformTable from './components/Config/PlatformTable'
import PresetGallery from './components/Config/PresetGallery'
import type { Platform, Preset } from './types'
// jsdom 拿不到真实排版，按需求书口径改为断言样式源码规则（工作目录为 web/）。
const RAW_CSS = readFileSync(join(process.cwd(), 'src', 'styles', 'responsive-mobile.css'), 'utf8')

// jsdom 未实现 matchMedia / ResizeObserver：为 antd 的响应式栅格与 rc-table 补最小替身。
beforeAll(() => {
  if (!window.matchMedia) {
    window.matchMedia = ((query: string) => ({
      matches: false,
      media: query,
      onchange: null,
      addEventListener: () => {},
      removeEventListener: () => {},
      addListener: () => {},
      removeListener: () => {},
      dispatchEvent: () => false,
    })) as unknown as typeof window.matchMedia
  }
  const g = globalThis as unknown as { ResizeObserver?: unknown }
  if (!g.ResizeObserver) {
    g.ResizeObserver = class {
      observe() {}
      unobserve() {}
      disconnect() {}
    }
  }
})

// 去掉注释，便于按规则切分。
const CSS = RAW_CSS.replace(/\/\*[\s\S]*?\*\//g, '')

interface Rule {
  selector: string
  decls: string
}

// 只取触摸断点 media block 内的规则（本文件唯一的顶层块）。
function touchRules(): Rule[] {
  const at = CSS.indexOf('@media')
  const open = CSS.indexOf('{', at)
  const close = CSS.lastIndexOf('}')
  const body = CSS.slice(open + 1, close)
  const rules: Rule[] = []
  const re = /([^{}]+)\{([^{}]*)\}/g
  let m: RegExpExecArray | null
  while ((m = re.exec(body)) !== null) {
    rules.push({ selector: m[1].replace(/\s+/g, ' ').trim(), decls: m[2].replace(/\s+/g, ' ').trim() })
  }
  return rules
}

const RULES = touchRules()

function rule(selectorPart: string): Rule | undefined {
  return RULES.find((r) => r.selector.split(',').some((s) => s.trim() === selectorPart))
}

function findRuleWith(selectorPart: string): Rule | undefined {
  return RULES.find((r) => r.selector.split(',').some((s) => s.includes(selectorPart)))
}

afterEach(cleanup)

describe('responsive-mobile.css 触摸断点', () => {
  it('断点只在窄屏（≤991.98px）或粗指针触发，桌面不命中', () => {
    expect(CSS).toMatch(/@media\s*\(max-width:\s*991\.98px\)\s*,\s*\(pointer:\s*coarse\)/)
  })

  it('主要按钮 ≥44px，紧凑按钮 ≥32×32', () => {
    const btn = rule('body .ant-btn')
    expect(btn, 'body .ant-btn 规则缺失').toBeTruthy()
    expect(btn!.decls).toContain('min-height: 44px')

    const small = rule('body .ant-btn.ant-btn-sm')
    expect(small, 'body .ant-btn.ant-btn-sm 规则缺失').toBeTruthy()
    expect(small!.decls).toContain('min-height: 32px')
    expect(small!.decls).toContain('min-width: 32px')
  })

  it('表单控件 ≥44px 高且字号 ≥16px（含 antd Select 的 .ant-select-selector 命中区）', () => {
    const input = findRuleWith('body .ant-input')
    expect(input!.decls).toContain('min-height: 44px')
    expect(input!.decls).toContain('font-size: 16px')

    // .ant-select-single 外层有固定 height，必须显式撑高
    const selectRoot = rule('body .ant-select-single')
    expect(selectRoot, 'body .ant-select-single 规则缺失').toBeTruthy()
    expect(selectRoot!.decls).toContain('height: 44px')

    // 实际命中区 .ant-select-selector 必须撑满并保证字号
    const selectorRule = findRuleWith('.ant-select-selector')
    expect(selectorRule, '.ant-select-selector 规则缺失').toBeTruthy()
    expect(selectorRule!.decls).toMatch(/font-size:\s*16px/)
  })

  it('抽屉导航项与时间范围分段控件 ≥44px', () => {
    const menu = findRuleWith('.ant-menu-inline .ant-menu-item')
    expect(menu!.decls).toContain('height: 44px')

    const seg = findRuleWith('.ant-segmented-item')
    expect(seg!.decls).toContain('min-height: 44px')
  })

  it('行内相邻操作按钮间距 ≥8px', () => {
    const actions = rule('.qh-actions')
    expect(actions, '.qh-actions 规则缺失').toBeTruthy()
    expect(actions!.decls).toContain('column-gap: 8px !important')
    expect(actions!.decls).toContain('row-gap: 8px !important')
  })

  it('不自造 <12px 字号', () => {
    for (const r of RULES) {
      for (const m of r.decls.matchAll(/font-size:\s*([\d.]+)px/g)) {
        expect(Number(m[1]), `${r.selector} 字号 <12px`).toBeGreaterThanOrEqual(12)
      }
    }
  })
})

const platform: Platform = {
  id: '1',
  name: 'Example',
  request: { method: 'GET', url: 'https://api.example.com/balance', headers: {} },
  handler: '',
  createdAt: '2026-01-01T00:00:00.000Z',
}

const preset: Preset = {
  id: 'p1',
  name: 'Example',
  fields: [{ key: 'baseUrl', label: '请求地址' }],
  method: 'GET',
  urlTemplate: '{{baseUrl}}',
  headersTemplate: {},
  extractorTemplate: 'function (d) { return d.balance }',
  builtin: true,
}

describe('操作组带 qh-actions 标记（间距规则的作用对象）', () => {
  it('PlatformTable 行操作组带标记，且由紧凑按钮组成', () => {
    const { container } = render(
      <PlatformTable
        platforms={[platform]}
        rowMsg={{}}
        onMove={() => {}}
        onEdit={() => {}}
        onExport={() => {}}
        onTest={() => {}}
        onFetch={() => {}}
        onDelete={() => {}}
        onImport={() => {}}
      />,
    )
    const group = container.querySelector('.qh-actions')
    expect(group, 'PlatformTable 操作组缺少 qh-actions').toBeTruthy()
    const btns = group!.querySelectorAll('.ant-btn-sm')
    expect(btns.length).toBeGreaterThan(0)
  })

  it('PresetGallery 卡片内操作组带标记', () => {
    const { container } = render(
      <PresetGallery
        presets={[preset]}
        onUse={() => {}}
        onEdit={() => {}}
        onExport={() => {}}
        onReset={() => {}}
        onDelete={() => {}}
        onImport={() => {}}
        onAdd={() => {}}
      />,
    )
    expect(container.querySelector('.qh-actions'), 'PresetGallery 操作组缺少 qh-actions').toBeTruthy()
  })
})
