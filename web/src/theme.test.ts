// 主题三态模块单测 (node 环境, jsdom 直接构造 DOM)。
// 注意: jsdom 未提升到仓库根, vitest 的 environment:'jsdom' 从根解析不到, 因此在这里直接 import 使用。
import { JSDOM } from 'jsdom'
import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'

type MediaListener = (event: MediaQueryListEvent) => void

interface FakeMedia {
  setDark(next: boolean): void
}

// jsdom 不实现 matchMedia: 用可切换的替身驱动系统外观变化
function installMatchMedia(win: Window, initialDark: boolean): FakeMedia {
  const listeners = new Set<MediaListener>()
  const state = { matches: initialDark }
  const mql = {
    media: '(prefers-color-scheme: dark)',
    get matches() {
      return state.matches
    },
    onchange: null,
    addEventListener: (_type: string, listener: MediaListener) => {
      listeners.add(listener)
    },
    removeEventListener: (_type: string, listener: MediaListener) => {
      listeners.delete(listener)
    },
    addListener: (listener: MediaListener) => {
      listeners.add(listener)
    },
    removeListener: (listener: MediaListener) => {
      listeners.delete(listener)
    },
    dispatchEvent: () => false,
  } as unknown as MediaQueryList

  win.matchMedia = vi.fn(() => mql) as unknown as typeof win.matchMedia

  return {
    setDark(next: boolean) {
      state.matches = next
      const event = { matches: next, media: mql.media } as MediaQueryListEvent
      for (const listener of [...listeners]) listener(event)
    },
  }
}

// 每次重置模块 + 新建 DOM: theme.ts 的模块级初始化按当前替身重跑, 互不污染
async function loadTheme(initialDark: boolean, stored?: string) {
  vi.resetModules()
  const dom = new JSDOM('<!doctype html><html><head></head><body></body></html>', {
    url: 'http://localhost/',
  })
  const win = dom.window
  vi.stubGlobal('window', win)
  vi.stubGlobal('document', win.document)
  vi.stubGlobal('localStorage', win.localStorage)
  if (stored !== undefined) win.localStorage.setItem('theme_mode', stored)
  const mq = installMatchMedia(win as unknown as Window, initialDark)
  const theme = await import('./theme')
  return { theme, mq, win }
}

beforeEach(() => {
  ;(globalThis as { IS_REACT_ACT_ENVIRONMENT?: boolean }).IS_REACT_ACT_ENVIRONMENT = true
})

afterEach(() => {
  vi.unstubAllGlobals()
  vi.restoreAllMocks()
})

describe('theme 三态', () => {
  it('默认 auto: 系统浅色解析为 light 并写 data-theme', async () => {
    const { theme } = await loadTheme(false)
    expect(theme.getThemeMode()).toBe('auto')
    expect(theme.getResolvedTheme()).toBe('light')
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')
  })

  it('auto: 系统深色解析为 dark', async () => {
    const { theme } = await loadTheme(true)
    expect(theme.getResolvedTheme()).toBe('dark')
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
  })

  it('auto: 系统外观切换时实时跟随', async () => {
    const { theme, mq } = await loadTheme(false)
    expect(theme.getResolvedTheme()).toBe('light')
    mq.setDark(true)
    expect(theme.getResolvedTheme()).toBe('dark')
    expect(document.documentElement.getAttribute('data-theme')).toBe('dark')
    mq.setDark(false)
    expect(theme.getResolvedTheme()).toBe('light')
  })

  it('setThemeMode 强制 light/dark 并写入 localStorage, 强制后不跟随系统', async () => {
    const { theme, mq } = await loadTheme(true)
    theme.setThemeMode('light')
    expect(theme.getThemeMode()).toBe('light')
    expect(theme.getResolvedTheme()).toBe('light')
    expect(localStorage.getItem('theme_mode')).toBe('light')
    expect(document.documentElement.getAttribute('data-theme')).toBe('light')

    mq.setDark(false)
    expect(theme.getResolvedTheme()).toBe('light')

    theme.setThemeMode('dark')
    expect(theme.getResolvedTheme()).toBe('dark')
    expect(localStorage.getItem('theme_mode')).toBe('dark')
  })

  it('读取已持久化模式; 非法值回退 auto', async () => {
    const first = await loadTheme(false, 'dark')
    expect(first.theme.getThemeMode()).toBe('dark')

    const second = await loadTheme(false, 'nonsense')
    expect(second.theme.getThemeMode()).toBe('auto')
  })

  it('meta color-scheme 与 data-theme 同步', async () => {
    const { theme } = await loadTheme(true)
    theme.setThemeMode('light')
    const meta = document.querySelector('meta[name="color-scheme"]')
    expect(meta?.getAttribute('content')).toBe('light')
  })
})

// React hooks 的订阅行为 (真实渲染一个探针组件)
describe('theme hooks', () => {
  it('useThemeMode / useResolvedTheme 随切换更新', async () => {
    const { theme } = await loadTheme(false)
    const React = await import('react')
    const { createRoot } = await import('react-dom/client')
    const { useThemeMode, useResolvedTheme } = theme

    const container = document.createElement('div')
    document.body.appendChild(container)
    const root = createRoot(container)
    let mode = ''
    let resolved = ''
    function Probe() {
      mode = useThemeMode()
      resolved = useResolvedTheme()
      return null
    }

    await React.act(async () => {
      root.render(React.createElement(Probe))
    })
    expect(mode).toBe('auto')
    expect(resolved).toBe('light')

    await React.act(async () => {
      theme.setThemeMode('dark')
    })
    expect(mode).toBe('dark')
    expect(resolved).toBe('dark')

    await React.act(async () => {
      theme.setThemeMode('auto')
    })
    expect(mode).toBe('auto')
    expect(resolved).toBe('light')

    await React.act(async () => {
      root.unmount()
    })
  })
})
