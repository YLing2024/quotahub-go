import { beforeEach, describe, expect, it, vi } from 'vitest'

// 轮询路径独立于首屏 loadDashboard: 失败必须保留上一次数据
const { getBalancesMock } = vi.hoisted(() => ({ getBalancesMock: vi.fn() }))

vi.mock('@/api/endpoints', () => ({
  getBalances: getBalancesMock,
  listPlatforms: vi.fn(),
  listPresets: vi.fn(),
  getSettings: vi.fn(),
}))

const { useStore, formatLastUpdate } = await import('./useStore')
import type { BalanceCard, DashboardData } from '@/types'

const card = (id: string): BalanceCard => ({
  id,
  name: id,
  value: 1,
  format: null,
  error: null,
  fetchedAt: null,
})

const dash = (over: Partial<DashboardData> = {}): DashboardData => ({
  updatedAt: '2026-09-28T00:00:00.000Z',
  platforms: [card('a')],
  collecting: false,
  lastRunAt: null,
  lastRunReason: null,
  ...over,
})

describe('refreshDashboard (轮询路径)', () => {
  beforeEach(() => {
    getBalancesMock.mockReset()
    useStore.setState({
      dashboard: { updatedAt: '', platforms: [], collecting: false, lastRunAt: null, lastRunReason: null },
      lastUpdateText: formatLastUpdate(null),
    })
  })

  it('成功 -> 覆盖面板并带入采集运行态', async () => {
    getBalancesMock.mockResolvedValueOnce(
      dash({ collecting: true, lastRunAt: '2026-09-28T00:00:30.000Z', lastRunReason: 'schedule' }),
    )
    await useStore.getState().refreshDashboard()
    const d = useStore.getState().dashboard
    expect(d.platforms).toHaveLength(1)
    expect(d.collecting).toBe(true)
    expect(d.lastRunReason).toBe('schedule')
  })

  it('失败 -> 保留上一次数据, 不清空面板', async () => {
    useStore.setState({ dashboard: dash() })
    getBalancesMock.mockRejectedValueOnce(new Error('network down'))
    await useStore.getState().refreshDashboard()
    const d = useStore.getState().dashboard
    expect(d.platforms).toHaveLength(1)
    expect(d.updatedAt).toBe('2026-09-28T00:00:00.000Z')
  })

  it('旧后端响应缺新字段 -> 归一化为未采集态', async () => {
    getBalancesMock.mockResolvedValueOnce({ updatedAt: 'x', platforms: [] })
    await useStore.getState().refreshDashboard()
    const d = useStore.getState().dashboard
    expect(d.collecting).toBe(false)
    expect(d.lastRunAt).toBeNull()
    expect(d.lastRunReason).toBeNull()
  })
})
