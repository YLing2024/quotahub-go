// builtin 模式本地认证端点 (401 由调用方处理, 不触发全局跳转)

import { api } from './client'

export interface AuthUser {
  name: string
}

export const getMe = () =>
  api<AuthUser>('/api/auth/me', { skipUnauthorizedHandler: true })

export const login = (username: string, password: string) =>
  api<{ ok: true; user: AuthUser }>('/api/auth/login', {
    method: 'POST',
    body: { username, password },
    skipUnauthorizedHandler: true,
  })

export const logout = () =>
  api<{ ok: true }>('/api/auth/logout', { method: 'POST', skipUnauthorizedHandler: true })
