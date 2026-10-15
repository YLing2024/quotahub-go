// 各 API 类型化封装 (全部走统一 client, 401 由 client 统一跳网关登录页)

import { api } from './client'
import type {
  DashboardData,
  ExportPayload,
  HistoryView,
  ImportResult,
  LogEntry,
  Platform,
  Preset,
  RefreshResult,
  Settings,
} from '@/types'

// ---- 平台 ----

export const getBalances = () => api<DashboardData>('/api/platforms/balances')

export const refreshAllPlatforms = () => api<RefreshResult>('/api/platforms/refresh', { method: 'POST' })

export const listPlatforms = () => api<Platform[]>('/api/platforms')

export type PlatformPayload = Pick<Platform, 'name' | 'url' | 'request' | 'handler' | 'format'>

export const createPlatform = (payload: PlatformPayload) =>
  api<Platform>('/api/platforms', { method: 'POST', body: payload })

export const updatePlatform = (id: string, payload: PlatformPayload) =>
  api<Platform>(`/api/platforms/${encodeURIComponent(id)}`, { method: 'PUT', body: payload })

export const deletePlatform = (id: string) =>
  api<void>(`/api/platforms/${encodeURIComponent(id)}`, { method: 'DELETE' })

export const reorderPlatforms = (ids: string[]) =>
  api<{ ok: true }>('/api/platforms/reorder', { method: 'PUT', body: { ids } })

export const testPlatform = (id: string) =>
  api<{ ok: true; value: number; testedAt: string } | { ok: false; error: string }>(
    `/api/platforms/${encodeURIComponent(id)}/test`,
    { method: 'POST' },
  )

export const fetchPlatformBalance = (id: string) =>
  api<{ ok: true; value: number; fetchedAt: string } | { ok: false; error: string }>(
    `/api/platforms/${encodeURIComponent(id)}/fetch`,
    { method: 'POST' },
  )

export const validatePlatformConfig = (
  payload: Omit<PlatformPayload, 'name' | 'url'> & { name?: string; url?: string },
) =>
  api<{ ok: true; value: number } | { ok: false; error: string }>('/api/platforms/validate', {
    method: 'POST',
    body: payload,
  })

export const getHistory = (id: string) =>
  api<HistoryView>(`/api/platforms/${encodeURIComponent(id)}/history`)

// ---- 预设 ----

export type PresetPayload = Omit<Preset, 'id' | 'createdAt' | 'builtin' | 'edited'>

export const listPresets = () => api<Preset[]>('/api/presets')

export const createPreset = (payload: PresetPayload) =>
  api<Preset>('/api/presets', { method: 'POST', body: payload })

export const updatePreset = (id: string, payload: PresetPayload) =>
  api<Preset>(`/api/presets/${encodeURIComponent(id)}`, { method: 'PUT', body: payload })

export const deletePreset = (id: string) =>
  api<void>(`/api/presets/${encodeURIComponent(id)}`, { method: 'DELETE' })

export const resetPreset = (id: string) =>
  api<Preset>(`/api/presets/${encodeURIComponent(id)}/reset`, { method: 'POST' })

// ---- 设置 ----

export const getSettings = () => api<Settings>('/api/settings')

export const updateSettings = (patch: Partial<Settings>) =>
  api<Settings>('/api/settings', { method: 'PUT', body: patch })

// ---- 日志 ----

export const getLogs = (limit = 500) => {
  const qs = new URLSearchParams({ limit: String(limit) })
  return api<{ logs: LogEntry[]; total: number }>(`/api/logs?${qs}`)
}

export const clearLogs = () => api<void>('/api/logs', { method: 'DELETE' })

// ---- 导入导出 ----

export const exportConfig = () => api<ExportPayload>('/api/export')

export const importConfig = (payload: unknown) =>
  api<ImportResult>('/api/import', { method: 'POST', body: payload })
