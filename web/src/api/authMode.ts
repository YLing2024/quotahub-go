// 认证模式探测: 启动时 GET /api/auth-mode 并缓存。
// 探测失败一律按 sso 处理 (绝不回退 builtin); 失败结果不缓存, 之后可再探测。

export type AuthMode = 'builtin' | 'sso'

let cached: AuthMode | null = null
let inflight: Promise<AuthMode> | null = null

type Listener = () => void
const unauthorizedListeners = new Set<Listener>()

export function peekAuthMode(): AuthMode | null {
  return cached
}

export function resetAuthMode(): void {
  cached = null
  inflight = null
}

// 订阅「收到 401」事件; builtin 模式下用于切回本地登录页
export function onUnauthorized(fn: Listener): () => void {
  unauthorizedListeners.add(fn)
  return () => {
    unauthorizedListeners.delete(fn)
  }
}

export function notifyUnauthorized(): void {
  for (const fn of unauthorizedListeners) fn()
}

async function probe(): Promise<AuthMode> {
  try {
    const res = await fetch('/api/auth-mode', { credentials: 'same-origin' })
    if (!res.ok) return 'sso'
    const data = (await res.json()) as { authMode?: unknown }
    const mode: AuthMode = data.authMode === 'builtin' ? 'builtin' : 'sso'
    cached = mode
    return mode
  } catch {
    return 'sso'
  } finally {
    inflight = null
  }
}

export async function getAuthMode(): Promise<AuthMode> {
  if (cached) return cached
  if (!inflight) inflight = probe()
  return inflight
}
