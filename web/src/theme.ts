// 主题三态模块 (auto / light / dark)。
// - localStorage 键 theme_mode, 取值 auto|light|dark, 默认 auto
// - auto: 实时跟随系统 prefers-color-scheme 的 change 事件
// - light / dark: 强制, 不跟随系统
// - 在 <html> 上维护 data-theme=light|dark 与 <meta name="color-scheme">, 供 CSS / 首屏使用
// - 只输出「解析后的明暗」这一事实, 不做任何 antd token 映射 (antd 默认设计语言不变)

import { useSyncExternalStore } from 'react'

export type ThemeMode = 'auto' | 'light' | 'dark'
export type ResolvedTheme = 'light' | 'dark'

export const THEME_STORAGE_KEY = 'theme_mode'
export const THEME_MEDIA_QUERY = '(prefers-color-scheme: dark)'

const MODES: readonly ThemeMode[] = ['auto', 'light', 'dark']

function isThemeMode(value: unknown): value is ThemeMode {
  return typeof value === 'string' && (MODES as readonly string[]).includes(value)
}

// 读取持久化模式; 非法/缺失/读取失败一律回退 auto
function readStoredMode(): ThemeMode {
  try {
    const raw = localStorage.getItem(THEME_STORAGE_KEY)
    return isThemeMode(raw) ? raw : 'auto'
  } catch {
    return 'auto'
  }
}

function systemPrefersDark(): boolean {
  if (typeof window === 'undefined' || typeof window.matchMedia !== 'function') return false
  return window.matchMedia(THEME_MEDIA_QUERY).matches
}

function resolveTheme(mode: ThemeMode): ResolvedTheme {
  if (mode === 'auto') return systemPrefersDark() ? 'dark' : 'light'
  return mode
}

let mode: ThemeMode = readStoredMode()

const listeners = new Set<() => void>()

function emit(): void {
  for (const listener of listeners) listener()
}

// 把解析结果同步到文档: data-theme (供 CSS) + meta color-scheme (UA 画布, 防刷新闪白) + 原生 color-scheme
function applyToDocument(): void {
  if (typeof document === 'undefined') return
  const theme = resolveTheme(mode)
  const root = document.documentElement
  root.setAttribute('data-theme', theme)
  root.style.colorScheme = theme
  let meta = document.querySelector('meta[name="color-scheme"]')
  if (!meta) {
    meta = document.createElement('meta')
    meta.setAttribute('name', 'color-scheme')
    document.head.appendChild(meta)
  }
  meta.setAttribute('content', theme)
}

// 系统外观变化: 非 auto 时不重渲染 (切回 auto 时会重新查询系统, 故无需缓存)
function onSystemChange(): void {
  if (mode === 'auto') {
    applyToDocument()
    emit()
  }
}

function subscribe(listener: () => void): () => void {
  listeners.add(listener)
  return () => {
    listeners.delete(listener)
  }
}

export function getThemeMode(): ThemeMode {
  return mode
}

export function getResolvedTheme(): ResolvedTheme {
  return resolveTheme(mode)
}

// 当前选择 (auto/light/dark): 供 Segmented 高亮
export function useThemeMode(): ThemeMode {
  return useSyncExternalStore(subscribe, getThemeMode, () => 'auto')
}

// 解析后的明暗: 供 ConfigProvider algorithm / CodeMirror 主题
export function useResolvedTheme(): ResolvedTheme {
  return useSyncExternalStore(subscribe, getResolvedTheme, () => 'light')
}

// 切换模式: 立刻写 localStorage + 刷新文档标记 + 通知订阅者
export function setThemeMode(next: ThemeMode): void {
  if (!isThemeMode(next)) return
  if (next === mode) {
    // 同一模式: 仍同步一次文档标记, 保证首次调用也能落定
    applyToDocument()
    return
  }
  mode = next
  applyToDocument()
  emit()
  try {
    localStorage.setItem(THEME_STORAGE_KEY, next)
  } catch {
    // 隐私模式等不允许写入: 内存态仍然生效, 只是刷新后回到 auto
  }
}

// 初始化: 监听系统外观并同步当前文档标记 (模块加载即执行一次)
if (typeof window !== 'undefined' && typeof window.matchMedia === 'function') {
  window.matchMedia(THEME_MEDIA_QUERY).addEventListener('change', onSystemChange)
}
applyToDocument()
