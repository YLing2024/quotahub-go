/**
 * 快速配置区：预设卡片网格（点击卡片套用预设；卡片内可编辑 / 导出 / 重置 / 删除）。
 *
 * 关键 props：
 * - `presets`（`Preset[]`，必填）：全部预设。
 * - `onUse` / `onEdit` / `onExport` / `onReset` / `onDelete`（各自接收 `Preset`）：卡片操作回调。
 * - `onImport` / `onAdd`（`() => void`）：导入预设 / 新增预设。
 *
 * 注意：内置预设显示“内置”标签与“重置默认”，用户预设显示“删除”；卡片内按钮的点击会
 * `stopPropagation`，避免误触发套用。语义与旧 app.js 平台配置 tab 的预设区一致，仅外观换成 antd。
 * 可访问性：卡片主体与内部按钮均为可聚焦元素；按钮有中文文本标签，可键盘操作。
 */
import { Button, Card, Col, Flex, Row, Space, Tag, Typography } from 'antd'
import type { Preset } from '@/types'

interface Props {
  presets: Preset[]
  onUse(preset: Preset): void
  onEdit(preset: Preset): void
  onExport(preset: Preset): void
  onReset(preset: Preset): void
  onDelete(preset: Preset): void
  onImport(): void
  onAdd(): void
}

export default function PresetGallery({
  presets,
  onUse,
  onEdit,
  onExport,
  onReset,
  onDelete,
  onImport,
  onAdd,
}: Props) {
  return (
    <section className="section">
      <Flex justify="space-between" align="flex-end" gap={16} wrap>
        <Typography.Title level={4} style={{ margin: 0 }}>
          快速配置
        </Typography.Title>
        <Space wrap>
          <Typography.Text type="secondary">Quick Setup</Typography.Text>
          <Button onClick={onImport}>导入预设</Button>
          <Button onClick={onAdd}>+ 添加预设</Button>
        </Space>
      </Flex>
      <Typography.Paragraph type="secondary" style={{ margin: '8px 0 16px' }}>
        点击预设卡片生成配置，可在下方表单中再修改；预设本身支持编辑，内置预设可重置为默认
      </Typography.Paragraph>
      <Row gutter={[16, 16]}>
        {presets.map((p) => (
          <Col key={p.id} xs={24} sm={12} lg={8}>
            <Card
              size="small"
              hoverable
              title={p.name}
              extra={p.builtin ? <Tag>内置</Tag> : null}
              onClick={() => onUse(p)}
            >
              <Typography.Text type="secondary">{p.fields.map((f) => f.label).join(' · ')}</Typography.Text>
              <div onClick={(e) => e.stopPropagation()} style={{ marginTop: 12 }}>
                <Space size={0} wrap className="qh-actions">
                  <Button type="link" size="small" onClick={() => onEdit(p)}>
                    编辑
                  </Button>
                  <Button type="link" size="small" onClick={() => onExport(p)}>
                    导出
                  </Button>
                  {p.builtin ? (
                    <Button type="link" size="small" onClick={() => onReset(p)}>
                      重置默认
                    </Button>
                  ) : (
                    <Button type="link" size="small" danger onClick={() => onDelete(p)}>
                      删除
                    </Button>
                  )}
                </Space>
              </div>
            </Card>
          </Col>
        ))}
      </Row>
    </section>
  )
}
