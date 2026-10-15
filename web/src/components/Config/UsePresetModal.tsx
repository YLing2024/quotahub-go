/**
 * 预设“使用（快速配置）”弹窗：填写字段 → 渲染模板 → 生成请求配置与处理函数回填表单。
 *
 * 关键 props：
 * - `preset`（`Preset`，必填）：要套用的预设（字段来自 `preset.fields`）。
 * - `onConfirm`（`(fill: PresetFill) => void`）：校验通过后回填 `{url, headersText, handlerText}`。
 * - `onCancel`（`() => void`）：取消关闭。
 * - `onError`（`(msg: string) => void`）：校验失败提示，与旧版一致走表单消息区。
 *
 * 注意：只回填 URL / 请求头 / 处理函数三项，不改动表单其他字段（与旧版一致）；语义对应旧
 * app.js 的 use-modal，外观换成 antd Modal/Form。
 * 可访问性：antd Modal 焦点陷阱与 Esc 关闭；字段有 label 关联。
 */
import { useState } from 'react'
import { Button, Form, Input, Modal, Space, Typography } from 'antd'
import type { Preset } from '@/types'
import { renderTemplate } from '@/lib/format'

export interface PresetFill {
  url: string
  headersText: string
  handlerText: string
}

interface Props {
  preset: Preset
  onConfirm(fill: PresetFill): void
  onCancel(): void
  /** 校验失败提示(与旧版一致走表单消息区) */
  onError(msg: string): void
}

export default function UsePresetModal({ preset, onConfirm, onCancel, onError }: Props) {
  const [vars, setVars] = useState<Record<string, string>>(() =>
    Object.fromEntries(preset.fields.map((f) => [f.key, ''])),
  )

  const setVar = (key: string, value: string) => {
    setVars((prev) => ({ ...prev, [key]: value }))
  }

  const confirm = () => {
    const filled: Record<string, string> = {}
    for (const f of preset.fields) filled[f.key] = (vars[f.key] ?? '').trim()
    const missingUrl = preset.fields.find((f) => /url/i.test(f.key) && !filled[f.key])
    if (missingUrl) {
      onError(`请填写 ${missingUrl.label}`)
      return
    }

    const url = renderTemplate(preset.urlTemplate, filled)
    const headers = renderTemplate(preset.headersTemplate, filled)
    for (const k of Object.keys(headers)) {
      if (String(headers[k]).trim() === '') delete headers[k]
    }
    // 预设的提取函数模板以 JSON 解析后的 data 为输入, 使用时包一层 JSON.parse 生成处理函数
    const extractor = renderTemplate(preset.extractorTemplate, filled)
    const handlerText = `function (raw) {\n  var data = JSON.parse(raw)\n  return (${extractor.trim()})(data)\n}`
    onConfirm({ url, headersText: JSON.stringify(headers, null, 2), handlerText })
  }

  return (
    <Modal
      open
      title={`${preset.name} 快速配置`}
      onCancel={onCancel}
      footer={
        <Space>
          <Button type="primary" onClick={confirm}>
            填入配置
          </Button>
          <Button onClick={onCancel}>取消</Button>
        </Space>
      }
    >
      <Typography.Paragraph type="secondary" style={{ marginTop: 0 }}>
        填写后自动生成请求配置与提取函数
      </Typography.Paragraph>
      <Form layout="vertical">
        {preset.fields.map((f) => (
          <Form.Item key={f.key} label={f.label}>
            <Input
              placeholder={f.placeholder || ''}
              value={vars[f.key] ?? ''}
              onChange={(e) => setVar(f.key, e.target.value)}
            />
          </Form.Item>
        ))}
      </Form>
    </Modal>
  )
}
