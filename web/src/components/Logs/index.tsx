/**
 * 日志页：按动作类型展示采集 / 配置变更日志，支持清空。
 *
 * 关键 props：
 * - `active`（boolean，必填）：该 tab 是否处于激活态；仅在激活时拉取日志（切走不请求）。
 *
 * 注意：动作标签（create/update/delete/fetch/...）的中文映射与旧实现一致；清空需二次确认。
 * 可访问性：动作类型用 antd Tag 呈现色块 + 文本，不只靠颜色区分；清空按钮有 danger 语义。
 */
import { useCallback, useEffect, useState } from 'react'
import { App, Button, Flex, List, Space, Tag, Typography } from 'antd'
import type { LogEntry } from '@/types'
import { clearLogs, getLogs } from '@/api/endpoints'

const ACTION_LABEL: Record<string, string> = {
  create: '新增平台',
  update: '更新平台',
  delete: '删除平台',
  fetch: '获取余额',
  refresh: '手动刷新',
  test: '测试连接',
  reorder: '平台排序',
  import: '导入配置',
  export: '导出配置',
  settings: '设置变更',
  'clear-logs': '清空日志',
  start: '系统启动',
  unknown: '其他',
}

// 与旧版一致: 删除/清空红色, 获取/刷新绿色, 其余默认
const DANGER_ACTIONS = new Set(['delete', 'clear-logs'])
const OK_ACTIONS = new Set(['fetch', 'refresh'])
const tagColor = (action: string): string | undefined =>
  DANGER_ACTIONS.has(action) ? 'red' : OK_ACTIONS.has(action) ? 'green' : undefined

function LogItem({ l }: { l: LogEntry }) {
  const meta =
    l.meta && typeof l.meta === 'object' && Object.keys(l.meta).length ? (
      <Typography.Text type="secondary">{JSON.stringify(l.meta)}</Typography.Text>
    ) : null
  return (
    <List.Item>
      <Space align="baseline" wrap size={12}>
        <Typography.Text type="secondary" style={{ whiteSpace: 'nowrap' }}>
          {new Date(l.time).toLocaleString('zh-CN')}
        </Typography.Text>
        <Tag color={tagColor(l.action)}>{ACTION_LABEL[l.action] || l.action}</Tag>
        <Typography.Text>
          {l.detail || ''}
          {l.platformName && <Typography.Text type="secondary">{` · ${l.platformName}`}</Typography.Text>}
        </Typography.Text>
        {meta}
      </Space>
    </List.Item>
  )
}

export default function LogsTab({ active }: { active: boolean }) {
  const { modal, message } = App.useApp()
  const [logs, setLogs] = useState<LogEntry[]>([])
  const [total, setTotal] = useState(0)
  const [error, setError] = useState('')

  const load = useCallback(async () => {
    try {
      const data = await getLogs(500)
      setLogs(data.logs || [])
      setTotal(data.total != null ? data.total : (data.logs || []).length)
      setError('')
    } catch (e) {
      setError(`加载失败: ${(e as Error).message}`)
    }
  }, [])

  // 与旧版一致: 每次切到日志 tab 时加载
  useEffect(() => {
    if (active) void load()
  }, [active, load])

  const clearAll = () => {
    modal.confirm({
      title: '确定清空所有操作日志？此操作不可撤销。',
      okText: '清空',
      cancelText: '取消',
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          await clearLogs()
          await load()
        } catch (e) {
          message.error(`清空失败: ${(e as Error).message}`)
        }
      },
    })
  }

  return (
    <>
      <section className="section">
        <Space direction="vertical" size={4} style={{ width: '100%' }}>
          <Typography.Text type="secondary">Operation · Logs</Typography.Text>
          <Typography.Title level={2} style={{ margin: 0 }}>
            操作日志
          </Typography.Title>
          <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
            记录平台增删改、余额获取、导入导出、设置变更等操作。
            <br />
            日志持久化保存，最多保留最近 2000 条。
          </Typography.Paragraph>
        </Space>
      </section>

      <section className="section">
        <Flex justify="space-between" align="flex-end" gap={16} wrap>
          <Typography.Title level={4} style={{ margin: 0 }}>
            日志列表
          </Typography.Title>
          <Space wrap>
            <Typography.Text type="secondary">Logs · {total}</Typography.Text>
            <Button onClick={() => void load()}>刷新</Button>
            <Button danger onClick={() => void clearAll()}>
              清空日志
            </Button>
          </Space>
        </Flex>
        <List<LogEntry>
          bordered
          style={{ marginTop: 16 }}
          dataSource={error ? [] : logs}
          locale={{ emptyText: error || '暂无操作日志' }}
          pagination={false}
          renderItem={(l) => <LogItem key={l.id} l={l} />}
        />
      </section>
    </>
  )
}
