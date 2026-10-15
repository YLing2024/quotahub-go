/**
 * 导入 / 导出弹窗：复制粘贴 JSON + 自动识别格式（完整配置 / 单个平台 / 单个预设）。
 *
 * 关键 props：
 * - `request`（`TransferRequest`，必填）：`{mode, title, desc, text?, expectedKind?}`；`mode` 为
 *   `'export'` 时展示 `text` 供复制，为 `'import'` 时用 `text` 作初始粘贴内容。
 * - `onClose`（`() => void`）：关闭弹窗。
 * - `onImported`（`(summary: string) => void`）：导入成功后回传摘要，父级负责提示与刷新列表。
 *
 * 注意：导入按 id 合并、同名覆盖；`expectedKind` 只用于提示，其他格式仍可导入。语义对应旧
 * app.js 的 transfer-modal，外观换成 antd Modal/Input/Button/Alert。
 * 可访问性：antd Modal 焦点陷阱与 Esc 关闭；导入文本框有 label 关联。
 */
import { useRef, useState } from 'react'
import { Alert, Button, Input, Modal, Space, Typography } from 'antd'
import { importConfig } from '@/api/endpoints'
import { classifyConfig, KIND_NAME, type ConfigKind } from './classify'

export interface TransferRequest {
  mode: 'export' | 'import'
  title: string
  desc: string
  /** export 内容文本或 import 初始粘贴文本 */
  text?: string
  /** import 模式期望格式(仅提示用, 其他格式仍可导入) */
  expectedKind?: ConfigKind | null
}

interface Props {
  request: TransferRequest
  onClose(): void
  /** 导入成功后回调(父级负责提示与刷新列表) */
  onImported(summary: string): void
}

interface DetectState {
  text: string
  cls: '' | 'is-error' | 'is-warn'
}

function detectOf(text: string, expected: ConfigKind | null | undefined): DetectState {
  const trimmed = text.trim()
  if (!trimmed) return { text: '', cls: '' }
  let obj: unknown
  try {
    obj = JSON.parse(trimmed)
  } catch {
    return { text: '✗ 不是合法 JSON', cls: 'is-error' }
  }
  const hit = classifyConfig(obj)
  if (!hit) {
    return {
      text: '✗ 无法识别的配置格式（需要完整配置、单个平台或单个预设的 JSON）',
      cls: 'is-error',
    }
  }
  if (expected && expected !== hit.kind) {
    return {
      text: `⚠ 识别为${hit.label}，与期望的「${KIND_NAME[expected]}」不符，仍可导入`,
      cls: 'is-warn',
    }
  }
  return { text: `✓ 识别为${hit.label}`, cls: '' }
}

export default function TransferModal({ request, onClose, onImported }: Props) {
  const isExport = request.mode === 'export'
  const [text, setText] = useState(request.text ?? '')
  const [detect, setDetect] = useState<DetectState>(() =>
    detectOf(request.text ?? '', request.expectedKind),
  )
  const [result, setResult] = useState<{ text: string; error?: boolean }>({ text: '' })
  const [copied, setCopied] = useState(false)
  const textareaRef = useRef<React.ComponentRef<typeof Input.TextArea>>(null)

  const onTextChange = (value: string) => {
    setText(value)
    setDetect(detectOf(value, request.expectedKind))
  }

  const doCopy = async () => {
    try {
      if (navigator.clipboard && navigator.clipboard.writeText) {
        await navigator.clipboard.writeText(text)
      } else {
        textareaRef.current?.resizableTextArea?.textArea?.select()
        document.execCommand('copy')
      }
      setCopied(true)
      window.setTimeout(() => setCopied(false), 1500)
    } catch {
      setResult({ text: '复制失败，请手动全选复制', error: true })
    }
  }

  const doImport = async () => {
    let payload: unknown
    try {
      payload = JSON.parse(text)
    } catch {
      setResult({ text: '导入失败: 不是合法 JSON', error: true })
      return
    }
    if (!classifyConfig(payload)) {
      setResult({
        text: '导入失败: 无法识别的配置格式（需要完整配置、单个平台或单个预设的 JSON）',
        error: true,
      })
      return
    }
    try {
      const r = await importConfig(payload)
      const skipped =
        r.errors && r.errors.length ? `；跳过 ${r.errors.length} 项: ${r.errors.join('; ')}` : ''
      const msg = `导入完成: ${r.platforms} 个平台、${r.presets} 个预设${skipped}`
      onImported(msg)
    } catch (e) {
      setResult({ text: `导入失败: ${(e as Error).message}`, error: true })
    }
  }

  return (
    <Modal
      open
      width={760}
      title={request.title}
      onCancel={onClose}
      footer={
        <Space>
          {isExport ? (
            <Button type="primary" onClick={() => void doCopy()}>
              {copied ? '已复制 ✓' : '一键复制'}
            </Button>
          ) : (
            <Button type="primary" onClick={() => void doImport()}>
              确认导入
            </Button>
          )}
          <Button onClick={onClose}>关闭</Button>
        </Space>
      }
    >
      <Space direction="vertical" size={12} style={{ width: '100%' }}>
        <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
          {request.desc}
        </Typography.Paragraph>
        {detect.text && (
          <Alert
            type={detect.cls === 'is-error' ? 'error' : detect.cls === 'is-warn' ? 'warning' : 'success'}
            message={detect.text}
            showIcon={false}
          />
        )}
        <Input.TextArea
          ref={textareaRef}
          rows={14}
          spellCheck={false}
          placeholder="粘贴完整配置（含 platforms / presets）、单个平台或单个预设的 JSON…"
          readOnly={isExport}
          value={text}
          onChange={(e) => onTextChange(e.target.value)}
        />
        {result.text && (
          <Alert type={result.error ? 'error' : 'info'} message={result.text} showIcon={false} />
        )}
      </Space>
    </Modal>
  )
}
