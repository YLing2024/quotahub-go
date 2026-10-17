/**
 * 已配置平台表格：名称 / URL / 操作列（上移、下移、编辑、导出、测试连接、获取余额、删除）。
 *
 * 关键 props：
 * - `platforms`（`Platform[]`，必填）：平台列表（顺序即展示顺序）。
 * - `rowMsg`（`Record<string, {text: string; error?: boolean}>`，必填）：每个平台的操作结果提示。
 * - `onMove`（`(id, dir: -1 | 1) => void`）：上/下移排序。
 * - `onEdit` / `onTest` / `onFetch` / `onDelete`（各自接收 id 或平台）：行操作回调。
 * - `onExport`（`(p: Platform) => void`）：导出单个平台。
 * - `onImport`（`() => void`）：导入平台。
 *
 * 注意：URL 列用 `Typography.Text` 的 `title` 承载完整地址，避免窄列截断丢失信息；操作列
 * 固定右侧。语义与旧 app.js 平台配置 tab 的表格一致，仅外观换成 antd。
 * 可访问性：上/下移按钮带 `title`（“上移 / 下移”），行操作均为可聚焦按钮。
 */
import { Button, Flex, Space, Table, Typography, theme, type TableProps } from 'antd'
import type { Platform } from '@/types'

interface RowMessage {
  text: string
  error?: boolean
}

interface Props {
  platforms: Platform[]
  rowMsg: Record<string, RowMessage>
  onMove(id: string, dir: -1 | 1): void
  onEdit(id: string): void
  onExport(p: Platform): void
  onTest(id: string): void
  onFetch(id: string): void
  onDelete(p: Platform): void
  onImport(): void
}

export default function PlatformTable({
  platforms,
  rowMsg,
  onMove,
  onEdit,
  onExport,
  onTest,
  onFetch,
  onDelete,
  onImport,
}: Props) {
  const { token } = theme.useToken()

  const columns: TableProps<Platform>['columns'] = [
    { title: '平台', dataIndex: 'name', key: 'name' },
    {
      title: 'URL',
      key: 'url',
      render: (_, p) => (
        <Typography.Text style={{ whiteSpace: 'nowrap' }} title={p.request?.url || ''}>
          {p.request?.url || '—'}
        </Typography.Text>
      ),
    },
    {
      title: '操作',
      key: 'ops',
      fixed: 'right',
      width: 320,
      render: (_, p) => {
        const rm = rowMsg[p.id]
        return (
          <Space size={0} wrap className="qh-actions">
            <Button type="link" size="small" title="上移" onClick={() => onMove(p.id, -1)}>
              ↑
            </Button>
            <Button type="link" size="small" title="下移" onClick={() => onMove(p.id, 1)}>
              ↓
            </Button>
            <Button type="link" size="small" onClick={() => onEdit(p.id)}>
              编辑
            </Button>
            <Button type="link" size="small" onClick={() => onExport(p)}>
              导出
            </Button>
            <Button type="link" size="small" onClick={() => onTest(p.id)}>
              测试连接
            </Button>
            <Button type="link" size="small" onClick={() => onFetch(p.id)}>
              获取余额
            </Button>
            <Button type="link" size="small" danger onClick={() => onDelete(p)}>
              删除
            </Button>
            {rm?.text && (
              <Typography.Text type={rm.error ? 'danger' : 'secondary'} style={{ fontSize: token.fontSizeSM }}>
                {rm.text}
              </Typography.Text>
            )}
          </Space>
        )
      },
    },
  ]

  return (
    <section className="section">
      <Flex justify="space-between" align="flex-end" gap={16} wrap>
        <Typography.Title level={4} style={{ margin: 0 }}>
          已配置平台
        </Typography.Title>
        <Space wrap>
          <Typography.Text type="secondary">Configured · {platforms.length}</Typography.Text>
          <Button onClick={onImport}>导入平台</Button>
        </Space>
      </Flex>

      <Table<Platform>
        rowKey="id"
        columns={columns}
        dataSource={platforms}
        pagination={false}
        scroll={{ x: 'max-content' }}
        sticky={{ offsetHeader: 64 }}
        locale={{ emptyText: '尚未配置任何平台' }}
        style={{ marginTop: 16 }}
      />
    </section>
  )
}
