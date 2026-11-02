/**
 * 已配置平台表格：拖拽排序（第一列拖动手柄）+ 名称 / URL / 操作列。
 *
 * 关键 props：
 * - `platforms`（`Platform[]`，必填）：平台列表（顺序即展示顺序）。
 * - `rowMsg`（`Record<string, {text: string; error?: boolean}>`，必填）：每个平台的操作结果提示。
 * - `onReorder`（`(ids: string[]) => void`）：拖拽结束后回传新的完整顺序（平台 id 数组）。
 * - `onEdit` / `onTest` / `onFetch` / `onDelete`（各自接收 id 或平台）：行操作回调。
 * - `onExport`（`(p: Platform) => void`）：导出单个平台。
 * - `onImport`（`() => void`）：导入平台。
 *
 * 排序实现：@dnd-kit。
 * - 只有第一列的手柄能拖，避免误触行里的按钮/链接；
 * - PointerSensor 设 4px 激活距离，轻点手柄不会启动拖动；
 * - KeyboardSensor + sortableKeyboardCoordinates：手柄可聚焦，空格拿起、方向键移动、空格放下；
 * - 拖拽中的行加阴影与 z-index，落点由 closestCenter 判定。
 *
 * 注意：URL 列用 `Typography.Text` 的 `title` 承载完整地址，避免窄列截断丢失信息；操作列
 * 固定右侧。
 * 可访问性：手柄是 `<button>`，带 `aria-label="拖动排序"` 与 `title`，键盘可完整操作。
 */
import { createContext, useContext, useMemo, type CSSProperties, type HTMLAttributes } from 'react'
import { Button, Flex, Space, Table, Typography, theme, type TableProps } from 'antd'
import {
  DndContext,
  KeyboardSensor,
  PointerSensor,
  closestCenter,
  useSensor,
  useSensors,
  type DragEndEvent,
} from '@dnd-kit/core'
import {
  SortableContext,
  arrayMove,
  sortableKeyboardCoordinates,
  useSortable,
  verticalListSortingStrategy,
} from '@dnd-kit/sortable'
import { CSS } from '@dnd-kit/utilities'
import type { Platform } from '@/types'

interface RowMessage {
  text: string
  error?: boolean
}

interface Props {
  platforms: Platform[]
  rowMsg: Record<string, RowMessage>
  onReorder(ids: string[]): void
  onEdit(id: string): void
  onExport(p: Platform): void
  onTest(id: string): void
  onFetch(id: string): void
  onDelete(p: Platform): void
  onImport(): void
}

/** 把当前行的拖拽监听器传给手柄单元格（只有手柄可拖）。 */
const DragHandleContext = createContext<{
  attributes: Record<string, unknown>
  listeners: Record<string, unknown> | undefined
} | null>(null)

/** 六点手柄图标：不引入 @ant-design/icons，省一个依赖。 */
function GripIcon() {
  const dots: Array<[number, number]> = []
  for (const y of [3, 8, 13]) {
    for (const x of [3, 9]) dots.push([x, y])
  }
  return (
    <svg width="12" height="16" viewBox="0 0 12 16" aria-hidden="true" focusable="false">
      {dots.map(([x, y]) => (
        <circle key={`${x}-${y}`} cx={x} cy={y} r="1.4" fill="currentColor" />
      ))}
    </svg>
  )
}

function DragHandle() {
  const ctx = useContext(DragHandleContext)
  // 这个项目没开 antd 的 cssVar 模式，CSS 里写 var(--ant-color-*) 取不到值、只会落到
  // 写死的回退色（浅色下还行，深色下就是黑糊糊一片）。颜色一律从 token 走内联。
  const { token } = theme.useToken()
  return (
    <button
      type="button"
      className="qh-drag-handle"
      aria-label="拖动排序"
      title="拖动排序（键盘：空格拿起，方向键移动，空格放下）"
      style={{ color: token.colorTextSecondary }}
      {...(ctx?.attributes ?? {})}
      {...(ctx?.listeners ?? {})}
    >
      <GripIcon />
    </button>
  )
}

/** antd Table 的自定义行：接上 dnd-kit 的 transform / transition。 */
function SortableRow(props: HTMLAttributes<HTMLTableRowElement> & { 'data-row-key'?: string }) {
  const id = String(props['data-row-key'] ?? '')
  const { token } = theme.useToken()
  const { attributes, listeners, setNodeRef, transform, transition, isDragging } = useSortable({ id })
  const style: CSSProperties = {
    ...props.style,
    transform: CSS.Translate.toString(transform),
    transition,
    ...(isDragging
      ? { position: 'relative', zIndex: 9, boxShadow: token.boxShadowSecondary, background: token.colorBgElevated }
      : null),
  }
  return (
    <DragHandleContext.Provider
      value={{
        attributes: attributes as unknown as Record<string, unknown>,
        listeners: listeners as unknown as Record<string, unknown> | undefined,
      }}
    >
      <tr
        {...props}
        ref={setNodeRef}
        style={style}
        className={`${props.className ?? ''}${isDragging ? ' qh-drag-row-active' : ''}`}
      />
    </DragHandleContext.Provider>
  )
}

export default function PlatformTable({
  platforms,
  rowMsg,
  onReorder,
  onEdit,
  onExport,
  onTest,
  onFetch,
  onDelete,
  onImport,
}: Props) {
  const { token } = theme.useToken()
  const sensors = useSensors(
    useSensor(PointerSensor, { activationConstraint: { distance: 4 } }),
    useSensor(KeyboardSensor, { coordinateGetter: sortableKeyboardCoordinates }),
  )
  const ids = useMemo(() => platforms.map((p) => p.id), [platforms])

  const handleDragEnd = (event: DragEndEvent) => {
    const { active, over } = event
    if (!over || active.id === over.id) return
    const from = ids.indexOf(String(active.id))
    const to = ids.indexOf(String(over.id))
    if (from < 0 || to < 0) return
    onReorder(arrayMove(ids, from, to))
  }

  const columns: TableProps<Platform>['columns'] = [
    { title: '', key: 'drag', width: 44, align: 'center', render: () => <DragHandle /> },
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
      width: 340,
      render: (_, p) => {
        const rm = rowMsg[p.id]
        return (
          <Space size={0} wrap className="qh-actions">
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

      <DndContext sensors={sensors} collisionDetection={closestCenter} onDragEnd={handleDragEnd}>
        <SortableContext items={ids} strategy={verticalListSortingStrategy}>
          <Table<Platform>
            rowKey="id"
            columns={columns}
            dataSource={platforms}
            pagination={false}
            scroll={{ x: 'max-content' }}
            sticky={{ offsetHeader: 64 }}
            locale={{ emptyText: '尚未配置任何平台' }}
            components={{ body: { row: SortableRow } }}
            style={{ marginTop: 16 }}
          />
        </SortableContext>
      </DndContext>
    </section>
  )
}
