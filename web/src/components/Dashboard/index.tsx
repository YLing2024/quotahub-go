/**
 * 面板页：按平台展示余额卡片、汇总统计与自动采集间隔设置。
 *
 * 关键 props：无（数据与刷新动作由 `useStore` 与 `/api/*` 提供）。
 *
 * 组成：`BalanceCardView`（单平台卡片，含刷新 / 趋势入口，余额文本口径沿用旧实现，仅外观
 * 换成 antd）、汇总 `Statistic`、采集间隔输入（上限 86400 秒）、以及懒加载的趋势图弹窗。
 * 注意：卡片余额文本在前端执行 `platform.format` 源码渲染（`runFormatOnClient`），失败回退
 * 原始数值。
 * 可访问性：刷新 / 趋势按钮为按钮元素，可键盘聚焦；输入框有 label 关联。
 */
import { useEffect, useState } from 'react'
import {
  Badge,
  Button,
  Card,
  Col,
  Flex,
  Input,
  Row,
  Space,
  Statistic,
  Typography,
  theme,
} from 'antd'
import type { BalanceCard } from '@/types'
import { fmt, runFormatOnClient } from '@/lib/format'
import { useStore } from '@/store/useStore'
import { fetchPlatformBalance, refreshAllPlatforms, updateSettings } from '@/api/endpoints'
import TrendChartModal from '@/components/TrendChart/TrendChartModal'

const MAX_INTERVAL = 86400

// 单个平台余额卡片: 名称/刷新/趋势入口与余额文本口径沿用旧实现(仅外观换成 antd)。
function BalanceCardView({
  p,
  refreshing,
  onRefresh,
  onOpenChart,
}: {
  p: BalanceCard
  refreshing: boolean
  onRefresh(): void
  onOpenChart(id: string, name: string): void
}) {
  const { token } = theme.useToken()
  const title = p.url ? (
    <a href={p.url} target="_blank" rel="noopener noreferrer" title={p.name}>
      {p.name}
    </a>
  ) : (
    <span>{p.name}</span>
  )
  // 正常路径所有平台都应有 format: 以 format(value) 为唯一展示路径。
  // p.format 为空或执行失败(返回非字符串/抛错)时回退 fmt(p.value) —— 仅容错, 不再作为推荐形态
  const text = p.value != null && p.format ? runFormatOnClient(p.format, p.value) : null
  const balanceText = text ?? (p.value != null ? fmt(p.value) : '—')
  const foot = p.error || (p.fetchedAt ? `获取于 ${new Date(p.fetchedAt).toLocaleString('zh-CN')}` : '尚未获取')
  return (
    <Card
      size="small"
      title={title}
      style={p.error ? { borderColor: token.colorError } : undefined}
      extra={
        <Space size={4}>
          <Button
            size="small"
            disabled={refreshing}
            onClick={onRefresh}
            title="刷新此平台"
            aria-label="刷新此平台"
          >
            {refreshing ? '…' : '↻'}
          </Button>
          {p.value != null && (
            <Button
              size="small"
              onClick={() => onOpenChart(p.id, p.name)}
              title="查看余额变化趋势"
              aria-label="查看余额变化趋势"
            >
              📊
            </Button>
          )}
        </Space>
      }
    >
      <Space direction="vertical" size={4} style={{ width: '100%' }}>
        <Statistic value={balanceText} />
        <Typography.Text type={p.error ? 'danger' : 'secondary'}>{foot}</Typography.Text>
      </Space>
    </Card>
  )
}

export default function DashboardTab() {
  const dashboard = useStore((s) => s.dashboard)
  const lastUpdateText = useStore((s) => s.lastUpdateText)
  const settings = useStore((s) => s.settings)
  const loadDashboard = useStore((s) => s.loadDashboard)
  const refreshDashboard = useStore((s) => s.refreshDashboard)
  const setLastUpdateError = useStore((s) => s.setLastUpdateError)
  const loadSettings = useStore((s) => s.loadSettings)
  const applySettings = useStore((s) => s.applySettings)

  const [refreshing, setRefreshing] = useState(false)
  // 单卡片刷新中集合 (按钮显示 … 并禁用)
  const [cardRefreshing, setCardRefreshing] = useState<Set<string>>(new Set())
  const [chart, setChart] = useState<{ id: string; name: string } | null>(null)

  // 自动采集设置本地态
  const [intervalText, setIntervalText] = useState('0')
  const [collectMsg, setCollectMsg] = useState<{ text: string; error?: boolean }>({ text: '' })
  const [savingCollect, setSavingCollect] = useState(false)

  useEffect(() => {
    void loadDashboard()
    void loadSettings()
  }, [loadDashboard, loadSettings])

  // 页面可见时每 15s 轮询一次面板(感知自动采集结果); 不可见不发请求, 切回可见立刻补一次。
  // 失败由 refreshDashboard 静默处理并保留上一次数据, 不清空面板。
  useEffect(() => {
    const POLL_MS = 15000
    let timer: number | null = null
    const stopPolling = () => {
      if (timer != null) {
        window.clearInterval(timer)
        timer = null
      }
    }
    const startPolling = () => {
      if (timer != null) return
      timer = window.setInterval(() => {
        if (document.visibilityState === 'visible') void refreshDashboard()
      }, POLL_MS)
    }
    const onVisibilityChange = () => {
      if (document.visibilityState === 'visible') {
        void refreshDashboard()
        startPolling()
      } else {
        stopPolling()
      }
    }
    if (document.visibilityState === 'visible') startPolling()
    document.addEventListener('visibilitychange', onVisibilityChange)
    return () => {
      stopPolling()
      document.removeEventListener('visibilitychange', onVisibilityChange)
    }
  }, [refreshDashboard])

  // 已保存的采集间隔变化时回填输入框(渲染期调整, 替代 effect 同步)
  const [savedSec, setSavedSec] = useState(settings.collectIntervalSeconds)
  if (savedSec !== settings.collectIntervalSeconds) {
    setSavedSec(settings.collectIntervalSeconds)
    setIntervalText(String(settings.collectIntervalSeconds))
  }

  const sec = settings.collectIntervalSeconds

  const doRefreshAll = async () => {
    setRefreshing(true)
    try {
      await refreshAllPlatforms()
      await loadDashboard()
    } catch (e) {
      setLastUpdateError(`刷新失败: ${(e as Error).message}`)
    } finally {
      setRefreshing(false)
    }
  }

  const refreshCard = async (id: string) => {
    setCardRefreshing((prev) => new Set(prev).add(id))
    try {
      await fetchPlatformBalance(id)
      await loadDashboard()
    } catch (e) {
      setLastUpdateError(`刷新失败: ${(e as Error).message}`)
    } finally {
      setCardRefreshing((prev) => {
        const next = new Set(prev)
        next.delete(id)
        return next
      })
    }
  }

  const saveCollect = async () => {
    const raw = Number(intervalText)
    const value = Number.isFinite(raw) && raw >= 0 ? Math.floor(raw) : null
    if (value === null) {
      setCollectMsg({ text: '请输入大于等于 0 的整数', error: true })
      return
    }
    if (value > MAX_INTERVAL) {
      setCollectMsg({ text: '间隔不能超过 86400 秒（1 天）', error: true })
      return
    }
    setSavingCollect(true)
    setCollectMsg({ text: '保存中…' })
    try {
      const next = await updateSettings({ collectIntervalSeconds: value })
      applySettings(next)
      setCollectMsg({ text: value === 0 ? '已关闭自动采集' : `已设置每 ${value} 秒采集一次` })
    } catch (e) {
      setCollectMsg({ text: `保存失败: ${(e as Error).message}`, error: true })
    } finally {
      setSavingCollect(false)
    }
  }

  const items = dashboard.platforms || []

  return (
    <>
      <section className="section">
        <Space direction="vertical" size={4}>
          <Typography.Text type="secondary">LLM Platform · Balance Monitor</Typography.Text>
          <Typography.Title level={2} style={{ margin: 0 }}>
            LLM 平台余额监控
          </Typography.Title>
          <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
            统一查看各 LLM 平台的配额与余额。
            <br />
            在「平台配置」中接入平台后，可手动获取余额。
          </Typography.Paragraph>
        </Space>
      </section>

      <section className="section">
        <Flex justify="space-between" align="flex-end" gap={16} wrap style={{ marginBottom: 16 }}>
          <Typography.Title level={4} style={{ margin: 0 }}>
            平台余额总览
          </Typography.Title>
          <Space size="middle" wrap>
            <Typography.Text type="secondary">{lastUpdateText}</Typography.Text>
            <Badge
              status={dashboard.collecting ? 'processing' : 'default'}
              text={
                dashboard.collecting
                  ? '自动采集 · 进行中…'
                  : sec > 0
                    ? `自动采集 · 每 ${sec} 秒`
                    : '自动采集 · 关闭'
              }
            />
          </Space>
        </Flex>
        <Flex justify="flex-end" style={{ marginBottom: 16 }}>
          <Button type="primary" loading={refreshing} disabled={refreshing} onClick={() => void doRefreshAll()}>
            {refreshing ? '刷新中…' : '立即刷新'}
          </Button>
        </Flex>
        <Row gutter={[16, 16]}>
          {items.length ? (
            items.map((p) => (
              <Col key={p.id} xs={24} md={12} xl={8}>
                <BalanceCardView
                  p={p}
                  refreshing={cardRefreshing.has(p.id)}
                  onRefresh={() => void refreshCard(p.id)}
                  onOpenChart={(id, name) => setChart({ id, name })}
                />
              </Col>
            ))
          ) : (
            <Col xs={24}>
              <Card size="small">
                <Typography.Text type="secondary">尚未配置平台</Typography.Text>
              </Card>
            </Col>
          )}
        </Row>
      </section>

      <section className="section">
        <Space direction="vertical" size={8} style={{ width: '100%' }}>
          <Flex justify="space-between" align="flex-end" gap={16} wrap>
            <Typography.Title level={4} style={{ margin: 0 }}>
              自动采集
            </Typography.Title>
            <Typography.Text type="secondary">Auto Collection</Typography.Text>
          </Flex>
          <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
            配置每隔多久自动采集一次所有平台余额，采集结果会写入余额历史（折线图数据源）。0 秒表示关闭自动采集。
          </Typography.Paragraph>
          <Space wrap align="center">
            <Typography.Text>采集间隔（秒）</Typography.Text>
            <Input
              id="collect-interval"
              type="number"
              min={0}
              step={1}
              placeholder="0"
              value={intervalText}
              onChange={(e) => setIntervalText(e.target.value)}
              style={{ width: 140 }}
            />
            <Button loading={savingCollect} disabled={savingCollect} onClick={() => void saveCollect()}>
              保存
            </Button>
            <Typography.Text type={collectMsg.error ? 'danger' : undefined}>{collectMsg.text}</Typography.Text>
          </Space>
        </Space>
      </section>

      {chart && (
        <TrendChartModal id={chart.id} name={chart.name} onClose={() => setChart(null)} />
      )}
    </>
  )
}
