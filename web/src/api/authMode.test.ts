import { afterEach, beforeEach, describe, expect, it, vi } from 'vitest'
import {
  getAuthMode,
  notifyUnauthorized,
  onUnauthorized,
  peekAuthMode,
  resetAuthMode,
} from './authMode'

// 认证模式探测: 成功才缓存; 探测失败/非 2xx 一律按 sso (绝不回退 builtin)

function jsonResponse(body: unknown, status = 200): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { 'Content-Type': 'application/json' },
  })
}

describe('getAuthMode', () => {
  beforeEach(() => {
    resetAuthMode()
  })

  afterEach(() => {
    vi.unstubAllGlobals()
  })

  it('成功探测 builtin 并缓存 (只请求一次)', async () => {
    const fetchMock = vi.fn(async () => jsonResponse({ authMode: 'builtin' }))
    vi.stubGlobal('fetch', fetchMock)

    expect(peekAuthMode()).toBeNull()
    expect(await getAuthMode()).toBe('builtin')
    expect(peekAuthMode()).toBe('builtin')
    expect(await getAuthMode()).toBe('builtin')
    expect(fetchMock).toHaveBeenCalledTimes(1)
  })

  it('成功探测 sso', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ authMode: 'sso' })))
    expect(await getAuthMode()).toBe('sso')
  })

  it('未知取值按 sso 处理', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ authMode: 'whatever' })))
    expect(await getAuthMode()).toBe('sso')
  })

  it('请求失败按 sso 处理且不缓存, 之后可重新探测', async () => {
    const fetchMock = vi
      .fn()
      .mockRejectedValueOnce(new Error('network down'))
      .mockResolvedValueOnce(jsonResponse({ authMode: 'builtin' }))
    vi.stubGlobal('fetch', fetchMock)

    expect(await getAuthMode()).toBe('sso')
    expect(peekAuthMode()).toBeNull()
    expect(await getAuthMode()).toBe('builtin')
    expect(fetchMock).toHaveBeenCalledTimes(2)
  })

  it('非 2xx 响应按 sso 处理', async () => {
    vi.stubGlobal('fetch', vi.fn(async () => jsonResponse({ error: 'boom' }, 500)))
    expect(await getAuthMode()).toBe('sso')
  })
})

describe('onUnauthorized', () => {
  it('订阅者会收到 401 通知, 取消订阅后不再收到', () => {
    const fn = vi.fn()
    const off = onUnauthorized(fn)
    notifyUnauthorized()
    expect(fn).toHaveBeenCalledTimes(1)
    off()
    notifyUnauthorized()
    expect(fn).toHaveBeenCalledTimes(1)
  })
})
