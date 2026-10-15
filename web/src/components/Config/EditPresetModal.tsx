/**
 * 预设编辑弹窗：编辑名称 / 交互式字段行 / 方法 / URL 模板 / 请求头模板 / 提取函数模板。
 *
 * 关键 props：
 * - `preset`（`Preset | null`，必填）：`null` 表示新增模式，否则为待编辑预设。
 * - `onClose`（`() => void`）：关闭弹窗。
 * - `onNotify`（`(msg: string, isError?: boolean) => void`）：消息提示（校验失败 / 保存结果 /
 *   重置提示），与旧版一致走表单消息区。
 * - `onSaved`（`() => void`）：保存成功后回调，父级据此刷新预设列表。
 *
 * 注意：内置预设提供“重置为默认”；语义与旧 app.js 的 preset-modal 一致，仅外观换成
 * antd Modal/Form。表单校验失败时不关闭弹窗。
 * 可访问性：antd Modal 自带焦点陷阱与 Esc 关闭；字段有 label 关联。
 */
import { useMemo, useState } from 'react'
import { Button, Col, Form, Input, Modal, Row, Select, Space } from 'antd'
import type { HttpMethod, Preset } from '@/types'
import { createPreset, resetPreset, updatePreset } from '@/api/endpoints'
import CodeEditor from './CodeEditor'

const DEFAULT_HEADERS_TEMPLATE = '{\n  "Authorization": "Bearer {{token}}"\n}'
const DEFAULT_EXTRACTOR_TEMPLATE = 'function (data) {\n  return data.balance\n}'

interface FieldRow {
  key: string
  label: string
  placeholder: string
}

interface Props {
  preset: Preset | null // null = 新增模式
  onClose(): void
  /** 消息提示(与旧版一致走表单消息区): 校验失败/保存结果/重置提示 */
  onNotify(msg: string, isError?: boolean): void
  /** 预设成功保存后回调(父级刷新预设列表) */
  onSaved(): void
}

function rowsFromFields(fields: Preset['fields'] | undefined): FieldRow[] {
  const list: Preset['fields'] =
    fields && fields.length ? fields : ([{ key: '', label: '', placeholder: '' }] as Preset['fields'])
  return list.map((f) => ({
    key: f.key || '',
    label: f.label || '',
    placeholder: f.placeholder || '',
  }))
}

export default function EditPresetModal({ preset, onClose, onNotify, onSaved }: Props) {
  const initial = useMemo(
    () => ({
      name: preset ? preset.name : '',
      rows: rowsFromFields(preset?.fields),
      method: (preset ? preset.method : 'GET') as HttpMethod,
      urlTemplate: preset ? preset.urlTemplate : '',
      headersText: preset
        ? JSON.stringify(preset.headersTemplate, null, 2)
        : DEFAULT_HEADERS_TEMPLATE,
      extractorText: preset ? preset.extractorTemplate : DEFAULT_EXTRACTOR_TEMPLATE,
    }),
    [preset],
  )

  const [name, setName] = useState(initial.name)
  const [rows, setRows] = useState<FieldRow[]>(initial.rows)
  const [method, setMethod] = useState<HttpMethod>(initial.method)
  const [urlTemplate, setUrlTemplate] = useState(initial.urlTemplate)
  const [headersText, setHeadersText] = useState(initial.headersText)
  const [extractorText, setExtractorText] = useState(initial.extractorText)

  const editingId = preset?.id ?? null
  const builtin = Boolean(preset?.builtin)

  const setRow = (idx: number, patch: Partial<FieldRow>) => {
    setRows((prev) => prev.map((r, i) => (i === idx ? { ...r, ...patch } : r)))
  }
  const removeRow = (idx: number) => setRows((prev) => prev.filter((_, i) => i !== idx))
  const addRow = () =>
    setRows((prev) => [...prev, { key: '', label: '', placeholder: '' }])

  const doReset = async () => {
    if (!editingId) return
    try {
      const def = await resetPreset(editingId)
      // 与旧版一致: 用默认定义重开弹窗内容(可继续修改后保存)
      setName(def.name)
      setRows(rowsFromFields(def.fields))
      setMethod(def.method)
      setUrlTemplate(def.urlTemplate)
      setHeadersText(JSON.stringify(def.headersTemplate, null, 2))
      setExtractorText(def.extractorTemplate)
      onNotify('已重置为默认配置，可修改后保存')
    } catch (e) {
      onNotify(`重置失败: ${(e as Error).message}`, true)
    }
  }

  const save = async () => {
    // 全空行自动忽略(与旧 collectFieldRows 一致)
    const fields = rows.filter((r) => r.key.trim() || r.label.trim() || r.placeholder.trim())
    const missing = fields.find((f) => !f.key.trim() || !f.label.trim())
    if (missing) return onNotify(`字段缺少${!missing.key.trim() ? ' key' : '显示名称'}`, true)
    if (!fields.length) return onNotify('至少需要一个字段', true)
    if (new Set(fields.map((f) => f.key.trim())).size !== fields.length) {
      return onNotify('字段 key 不能重复', true)
    }
    let headers: Record<string, string>
    try {
      headers = JSON.parse(headersText || '{}')
    } catch {
      return onNotify('请求头模板必须是合法 JSON', true)
    }

    const payload = {
      name,
      fields: fields.map((f) => ({
        key: f.key.trim(),
        label: f.label.trim(),
        ...(f.placeholder.trim() ? { placeholder: f.placeholder.trim() } : {}),
      })),
      method,
      urlTemplate,
      headersTemplate: headers,
      extractorTemplate: extractorText,
    }

    try {
      if (editingId) {
        await updatePreset(editingId, payload)
        onNotify('预设已更新')
      } else {
        await createPreset(payload)
        onNotify('预设已添加')
      }
      onSaved()
      onClose()
    } catch (e) {
      onNotify(`保存失败: ${(e as Error).message}`, true)
    }
  }

  return (
    <Modal
      open
      width={760}
      title={preset ? `编辑预设：${preset.name}` : '添加预设'}
      onCancel={onClose}
      footer={
        <Space>
          <Button type="primary" onClick={() => void save()}>
            保存预设
          </Button>
          {builtin && (
            <Button onClick={() => void doReset()}>
              重置为默认
            </Button>
          )}
          <Button onClick={onClose}>取消</Button>
        </Space>
      }
    >
      <Form layout="vertical">
        <Form.Item label="预设名称">
          <Input placeholder="NEWAPI" value={name} onChange={(e) => setName(e.target.value)} />
        </Form.Item>

        <Form.Item
          label={`字段（key 与显示名称必填；key 用于 URL / 请求头 / 提取函数模板中的 {{key}} 替换）`}
        >
          <div
            style={{
              display: 'flex',
              flexDirection: 'column',
              gap: 8,
              maxHeight: 260,
              overflowY: 'auto',
              paddingRight: 4,
            }}
          >
            {rows.map((row, idx) => (
              <Space key={idx} align="center" style={{ display: 'flex' }}>
                <Input
                  style={{ width: 160 }}
                  placeholder="字段 key，如 baseUrl"
                  value={row.key}
                  onChange={(e) => setRow(idx, { key: e.target.value })}
                />
                <Input
                  style={{ width: 160 }}
                  placeholder="显示名称，如 请求地址"
                  value={row.label}
                  onChange={(e) => setRow(idx, { label: e.target.value })}
                />
                <Input
                  style={{ flex: 1, minWidth: 160 }}
                  placeholder="输入提示（可留空）"
                  value={row.placeholder}
                  onChange={(e) => setRow(idx, { placeholder: e.target.value })}
                />
                <Button type="link" danger onClick={() => removeRow(idx)}>
                  删除
                </Button>
              </Space>
            ))}
          </div>
          <Button style={{ marginTop: 8 }} onClick={addRow}>
            + 添加字段
          </Button>
        </Form.Item>

        <Row gutter={16}>
          <Col xs={24} md={8}>
            <Form.Item label="请求方法">
              <Select
                value={method}
                onChange={(v) => setMethod(v)}
                options={[
                  { value: 'GET', label: 'GET' },
                  { value: 'POST', label: 'POST' },
                ]}
              />
            </Form.Item>
          </Col>
          <Col xs={24} md={16}>
            <Form.Item label={`URL 模板（支持 {{字段key}}）`}>
              <Input
                placeholder="{{baseUrl}}/api/user/self"
                value={urlTemplate}
                onChange={(e) => setUrlTemplate(e.target.value)}
              />
            </Form.Item>
          </Col>
        </Row>

        <Form.Item label={`请求头模板（JSON，支持 {{字段key}}，留空值自动移除）`}>
          <CodeEditor value={headersText} mode="json" onChange={setHeadersText} />
        </Form.Item>

        <Form.Item
          label={`提取函数模板（JS，data 为 JSON 解析后的响应；使用时自动包一层 JSON.parse 生成处理函数，支持 {{字段key}}）`}
        >
          <CodeEditor value={extractorText} mode="js" onChange={setExtractorText} />
        </Form.Item>
      </Form>
    </Modal>
  )
}
