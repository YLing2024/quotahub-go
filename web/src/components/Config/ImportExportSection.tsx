/**
 * 导入 / 导出区：导出完整配置、导入配置，并展示最近一次导入/导出的结果消息。
 *
 * 关键 props：
 * - `message`（`{text: string; error?: boolean}`，必填）：结果提示（空文本则不显示）。
 * - `onExportAll`（`() => void`）：导出完整配置（全部平台 + 预设）。
 * - `onImportConfig`（`() => void`）：打开导入弹窗（支持完整配置 / 单平台 / 单预设）。
 *
 * 注意：导入按 id 合并、同名覆盖；语义与旧 app.js 的导入导出区一致，仅外观换成 antd。
 * 可访问性：两个按钮均为可聚焦元素；结果消息用文本 + 颜色双重表达（`danger` / `secondary`）。
 */
import { Button, Flex, Space, Typography } from 'antd'

interface Props {
  message: { text: string; error?: boolean }
  onExportAll(): void
  onImportConfig(): void
}

export default function ImportExportSection({ message, onExportAll, onImportConfig }: Props) {
  return (
    <section className="section">
      <Flex justify="space-between" align="flex-end" gap={16} wrap>
        <Typography.Title level={4} style={{ margin: 0 }}>
          导入 / 导出
        </Typography.Title>
        <Typography.Text type="secondary">JSON</Typography.Text>
      </Flex>
      <Typography.Paragraph type="secondary" style={{ margin: '8px 0 16px' }}>
        导出完整配置（全部平台 +
        预设）用于备份或迁移；也支持单个平台 / 单个预设的导出与导入。导入时按 id 合并，同名配置会被覆盖。
      </Typography.Paragraph>
      <Space wrap align="center">
        <Button type="primary" onClick={onExportAll}>
          导出完整配置
        </Button>
        <Button onClick={onImportConfig}>导入配置</Button>
        {message.text && (
          <Typography.Text type={message.error ? 'danger' : 'secondary'}>{message.text}</Typography.Text>
        )}
      </Space>
    </section>
  )
}
