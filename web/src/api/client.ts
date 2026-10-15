// 统一 API client: 同源请求自动带会话 cookie (credentials: same-origin), 不携带 Authorization。
// 全局唯一 401 处理按认证模式分发:
//   sso     -> 整页跳网关登录页 /_auth/login?next=<当前地址> (已在 /_auth/ 页面上不重复跳)
//   builtin -> 通知应用切回本地登录页, 不跳 /_auth/*
// 不做弹窗/局部重试。

import { getAuthMode, notifyUnauthorized, peekAuthMode } from './authMode'

export class ApiError extends Error {
  status: number
  retryAfter: number | null
  constructor(status: number, message: string, retryAfter: number | null = null) {
    super(message)
    this.status = status
    this.retryAfter = retryAfter
  }
}

export interface ApiOptions {
  method?: string
  body?: unknown
  // 认证端点自行处理 401/429, 不触发全局跳转
  skipUnauthorizedHandler?: boolean
}

let redirecting = false

// 401 -> 整页跳网关登录页, next 带回当前地址 (pathname + search)
function redirectToGatewayLogin(): void {
  if (redirecting) return
  // 已在网关登录/回调页面上时不再跳, 防 next 嵌套
  if (window.location.pathname.startsWith('/_auth/')) return
  redirecting = true
  const next = encodeURIComponent(window.location.pathname + window.location.search)
  window.location.href = `/_auth/login?next=${next}`
}

async function handleUnauthorized(): Promise<void> {
  const mode = peekAuthMode() ?? (await getAuthMode())
  if (mode === 'sso') redirectToGatewayLogin()
  else notifyUnauthorized()
}

export async function api<T>(path: string, options: ApiOptions = {}): Promise<T> {
  const headers: Record<string, string> = {
    'Content-Type': 'application/json',
  }

  const res = await fetch(path, {
    method: options.method ?? 'GET',
    headers,
    credentials: 'same-origin',
    body: options.body !== undefined ? JSON.stringify(options.body) : undefined,
  })

  if (res.status === 401 && !options.skipUnauthorizedHandler) {
    await handleUnauthorized()
    throw new ApiError(401, '未登录或登录已过期')
  }

  if (res.status === 204) return undefined as T

  const data = (await res.json().catch(() => ({}))) as Record<string, unknown>
  if (!res.ok) {
    const retryAfter = Number(data.retryAfter)
    throw new ApiError(
      res.status,
      (data.error as string) || `HTTP ${res.status}`,
      Number.isFinite(retryAfter) && retryAfter > 0 ? retryAfter : null,
    )
  }
  return data as T
}
