// 认证状态: 启动探测模式 + builtin 登录态。sso 模式不管理用户信息。
// 全局 401 (builtin) 会清空 user, App 据此切回登录页。

import { create } from 'zustand'
import { getAuthMode, onUnauthorized, type AuthMode } from '@/api/authMode'
import { getMe, login as apiLogin, logout as apiLogout, type AuthUser } from '@/api/auth'

interface AuthState {
  ready: boolean
  mode: AuthMode | null
  user: AuthUser | null
  init(): Promise<void>
  login(username: string, password: string): Promise<void>
  logout(): Promise<void>
}

let subscribed = false

export const useAuthStore = create<AuthState>((set, get) => ({
  ready: false,
  mode: null,
  user: null,

  init: async () => {
    if (!subscribed) {
      subscribed = true
      // 会话过期/失效时全局 401 -> 清空用户, 回到登录页 (仅 builtin 生效)
      onUnauthorized(() => {
        if (get().mode === 'builtin') set({ user: null })
      })
    }
    const mode = await getAuthMode()
    if (mode === 'sso') {
      set({ mode, user: null, ready: true })
      return
    }
    try {
      const user = await getMe()
      set({ mode, user, ready: true })
    } catch {
      set({ mode, user: null, ready: true })
    }
  },

  login: async (username, password) => {
    const result = await apiLogin(username, password)
    set({ user: result.user })
  },

  logout: async () => {
    try {
      await apiLogout()
    } finally {
      set({ user: null })
    }
  },
}))
