/**
 * 平台配置页：编排快速配置（预设）、添加平台表单、导入 / 导出与已配置平台表格。
 *
 * 关键 props：无（数据来自 `useStore` 与 `/api/*`）。
 *
 * 组成：`PresetGallery`（预设卡片）、内联的“添加平台”表单（CodeMirror 编辑请求头 / 处理函数 /
 * 显示格式函数）、`ImportExportSection`（导入导出）、`PlatformTable`（已配置平台）。
 * 语义与旧 app.js 平台配置 tab 一致，仅外观换成 antd。表单与各子区的交互逻辑（保存 / 验证 /
 * 编辑回填 / 排序 / 测试 / 获取 / 删除 / 预设操作）仍集中在本文件，子组件只做展示。
 *
 * 可访问性：表单字段均有 label；验证输出为只读编辑器；操作结果以文本 + 颜色双重表达。
 */
import { useEffect, useRef, useState } from 'react'
import { Alert, App, Button, Col, Flex, Form, Input, Row, Select, Space, Typography, type InputRef } from 'antd'
import type { HttpMethod, Platform, Preset } from '@/types'
import {
  createPlatform,
  deletePlatform,
  deletePreset,
  exportConfig,
  fetchPlatformBalance,
  reorderPlatforms,
  resetPreset,
  testPlatform,
  updatePlatform,
  validatePlatformConfig,
} from '@/api/endpoints'
import { useStore } from '@/store/useStore'
import { DEFAULT_HANDLER, fmt, formatValue, resolveHandler } from '@/lib/format'
import CodeEditor from './CodeEditor'
import PresetGallery from './PresetGallery'
import PlatformTable from './PlatformTable'
import ImportExportSection from './ImportExportSection'
import UsePresetModal, { type PresetFill } from './UsePresetModal'
import EditPresetModal from './EditPresetModal'
import TransferModal, { type TransferRequest } from '@/components/Transfer/TransferModal'

const DEFAULT_HEADERS = '{\n  "Authorization": "Bearer 你的密钥"\n}'

interface FormState {
  name: string
  homepage: string
  method: HttpMethod
  bodyText: string
  url: string
  headersText: string
  handlerText: string
  formatText: string
}

const EMPTY_FORM: FormState = {
  name: '',
  homepage: '',
  method: 'GET',
  bodyText: '',
  url: '',
  headersText: DEFAULT_HEADERS,
  handlerText: DEFAULT_HANDLER,
  formatText: '',
}

function fillFormState(p: Platform): FormState {
  return {
    name: p.name || '',
    homepage: p.url || '',
    method: p.request?.method || 'GET',
    url: p.request?.url || '',
    headersText: JSON.stringify(p.request?.headers || {}, null, 2),
    bodyText:
      p.request?.body != null && (typeof p.request.body !== 'object' || Object.keys(p.request.body).length > 0)
        ? JSON.stringify(p.request.body)
        : '',
    handlerText: resolveHandler(p),
    formatText: p.format || '',
  }
}

export default function ConfigTab() {
  const { modal } = App.useApp()
  const platforms = useStore((s) => s.platforms)
  const presets = useStore((s) => s.presets)
  const loadPlatforms = useStore((s) => s.loadPlatforms)
  const setPlatforms = useStore((s) => s.setPlatforms)
  const loadPresets = useStore((s) => s.loadPresets)
  const loadDashboard = useStore((s) => s.loadDashboard)

  // ---- 表单状态 ----
  const [form, setForm] = useState<FormState>(EMPTY_FORM)
  const [editingId, setEditingId] = useState<string | null>(null)
  const [formMsg, setFormMsg] = useState<{ text: string; error?: boolean }>({ text: '' })
  const notify = (text: string, isError?: boolean) => setFormMsg({ text, error: isError })
  const [validating, setValidating] = useState(false)
  const [verifyText, setVerifyText] = useState('')

  const urlInputRef = useRef<InputRef | null>(null)
  const formRef2 = useRef<HTMLDivElement>(null)

  const patchForm = (patch: Partial<FormState>) => setForm((prev) => ({ ...prev, ...patch }))

  const clearForm = () => {
    setForm({ ...EMPTY_FORM })
    setEditingId(null)
  }

  const readPayload = (f: FormState) => {
    let headers: Record<string, string>
    try {
      headers = JSON.parse(f.headersText || '{}')
    } catch {
      throw new Error('请求头必须是合法 JSON')
    }
    const bodyText = f.bodyText.trim()
    let body: unknown
    if (bodyText) {
      try {
        body = JSON.parse(bodyText)
      } catch {
        throw new Error('请求体必须是合法 JSON')
      }
    }
    return {
      name: f.name,
      url: f.homepage.trim(),
      request: { method: f.method, url: f.url, headers, body },
      handler: f.handlerText,
      format: f.formatText.trim(),
    }
  }

  // ---- 保存 / 取消编辑 / 验证 ----

  const save = async () => {
    notify('')
    if (!form.formatText.trim()) {
      notify('请填写显示格式函数', true)
      return
    }
    let payload: ReturnType<typeof readPayload>
    try {
      payload = readPayload(form)
    } catch (err) {
      notify((err as Error).message, true)
      return
    }
    try {
      const saved = editingId ? await updatePlatform(editingId, payload) : await createPlatform(payload)
      notify(`${editingId ? '已更新' : '已保存'}: ${saved.name}`)
      clearForm()
      await loadPlatforms()
      await loadDashboard()
    } catch (err) {
      notify(`保存失败: ${(err as Error).message}`, true)
    }
  }

  const cancelEdit = () => {
    clearForm()
    notify('')
  }

  const validate = async () => {
    notify('验证中…')
    setVerifyText('验证中…')
    setValidating(true)
    let payload: ReturnType<typeof readPayload>
    try {
      payload = readPayload(form)
    } catch (err) {
      notify((err as Error).message, true)
      setVerifyText(`配置错误：${(err as Error).message}`)
      setValidating(false)
      return
    }
    try {
      const r = await validatePlatformConfig(payload)
      if (r.ok) {
        setVerifyText(formatValue(r.value))
        notify('验证成功')
      } else {
        setVerifyText(`请求失败：${r.error}`)
        notify('验证失败', true)
      }
    } catch (err) {
      setVerifyText(`验证失败：${(err as Error).message}`)
      notify(`验证失败: ${(err as Error).message}`, true)
    } finally {
      setValidating(false)
    }
  }

  // ---- 编辑回填(表格「编辑」入口) ----

  const startEdit = (id: string) => {
    const p = platforms.find((x) => x.id === id)
    if (!p) return
    setForm(fillFormState(p))
    setEditingId(id)
    notify(`正在编辑：${p.name}`)
    formRef2.current?.scrollIntoView({ behavior: 'smooth', block: 'start' })
  }

  // ---- 预设使用弹窗填入(url/headers/handler 三项, 与旧版一致不动其他字段) ----

  const [usePreset, setUsePreset] = useState<Preset | null>(null)

  const applyPresetFill = (fill: PresetFill) => {
    const name = usePreset?.name ?? ''
    patchForm({ url: fill.url, headersText: fill.headersText, handlerText: fill.handlerText })
    setUsePreset(null)
    notify(`已填入 ${name} 配置，可修改后保存`)
    requestAnimationFrame(() => {
      urlInputRef.current?.nativeElement?.scrollIntoView({ behavior: 'smooth', block: 'center' })
    })
  }

  // ---- 预设卡片操作 ----

  const resetBuiltinPreset = async (preset: Preset) => {
    try {
      const def = await resetPreset(preset.id)
      await loadPresets()
      notify(`已重置 ${def.name} 为默认配置`)
    } catch (e) {
      notify(`重置失败: ${(e as Error).message}`, true)
    }
  }

  const deleteCustomPreset = (preset: Preset) => {
    modal.confirm({
      title: `确定删除预设「${preset.name}」？`,
      okText: '删除',
      cancelText: '取消',
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          await deletePreset(preset.id)
          await loadPresets()
        } catch (e) {
          notify(`删除失败: ${(e as Error).message}`, true)
        }
      },
    })
  }

  // ---- 预设编辑弹窗 (null=关闭; {preset:null}=新增) ----

  const [presetModal, setPresetModal] = useState<Preset | null | undefined>(undefined)

  // ---- 平台表格操作 ----

  const [rowMsg, setRowMsg] = useState<Record<string, { text: string; error?: boolean }>>({})

  const setRowMessage = (id: string, text: string, isError?: boolean) =>
    setRowMsg((prev) => ({ ...prev, [id]: { text, error: isError } }))

  const movePlatform = async (id: string, dir: -1 | 1) => {
    const idx = platforms.findIndex((x) => x.id === id)
    const swap = idx + dir
    if (idx === -1 || swap < 0 || swap >= platforms.length) return
    const next = [...platforms]
    ;[next[idx], next[swap]] = [next[swap], next[idx]]
    setPlatforms(next)
    try {
      await reorderPlatforms(next.map((p) => p.id))
      await loadDashboard()
    } catch (err) {
      notify(`排序失败: ${(err as Error).message}`, true)
      await loadPlatforms()
    }
  }

  const testRow = async (id: string) => {
    setRowMessage(id, '测试中…')
    try {
      const r = await testPlatform(id)
      if (r.ok) setRowMessage(id, `成功: ${fmt(r.value)}`)
      else setRowMessage(id, `失败: ${r.error}`, true)
    } catch (err) {
      setRowMessage(id, (err as Error).message, true)
    }
  }

  const fetchRow = async (id: string) => {
    setRowMessage(id, '获取中…')
    try {
      const r = await fetchPlatformBalance(id)
      if (r.ok) setRowMessage(id, `已获取: ${fmt(r.value)}`)
      else setRowMessage(id, `失败: ${r.error}`, true)
      await loadDashboard()
    } catch (err) {
      setRowMessage(id, (err as Error).message, true)
    }
  }

  const removeRowPlatform = (id: string, name: string) => {
    modal.confirm({
      title: `确定删除平台「${name}」？`,
      okText: '删除',
      cancelText: '取消',
      okButtonProps: { danger: true },
      onOk: async () => {
        try {
          await deletePlatform(id)
          if (editingId === id) clearForm()
          await loadPlatforms()
          await loadDashboard()
        } catch (err) {
          setRowMessage(id, (err as Error).message, true)
        }
      },
    })
  }

  // ---- 导入导出 ----

  const [transferReq, setTransferReq] = useState<TransferRequest | null>(null)
  const [transferMsg, setTransferMsg] = useState<{ text: string; error?: boolean }>({ text: '' })

  const exportAll = async () => {
    try {
      const data = await exportConfig()
      setTransferReq({
        mode: 'export',
        title: '导出完整配置',
        desc: `共 ${data.platforms.length} 个平台、${data.presets.length} 个预设，复制以下 JSON 即可备份或分享`,
        text: JSON.stringify(data, null, 2),
      })
    } catch (e) {
      setTransferMsg({ text: `导出失败: ${(e as Error).message}`, error: true })
    }
  }

  const exportPreset = (p: Preset) =>
    setTransferReq({
      mode: 'export',
      title: `导出预设：${p.name}`,
      desc: '复制以下 JSON 即可分享或备份',
      text: JSON.stringify(p, null, 2),
    })

  const exportPlatform = (p: Platform) =>
    setTransferReq({
      mode: 'export',
      title: `导出平台：${p.name}`,
      desc: '复制以下 JSON 即可分享或备份',
      text: JSON.stringify(p, null, 2),
    })

  const onImported = async (summary: string) => {
    setTransferReq(null)
    setTransferMsg({ text: summary })
    await loadPlatforms()
    await loadPresets()
    await loadDashboard()
  }

  // 数据兜底加载(App 已预载, 此处幂等)
  useEffect(() => {
    void loadPresets()
    void loadPlatforms()
  }, [loadPresets, loadPlatforms])

  return (
    <>
      <section className="section">
        <Space direction="vertical" size={4} style={{ width: '100%' }}>
          <Typography.Text type="secondary">Platform · Configuration</Typography.Text>
          <Typography.Title level={2} style={{ margin: 0 }}>
            平台配置
          </Typography.Title>
          <Typography.Paragraph type="secondary" style={{ margin: 0 }}>
            添加需要监控的 LLM 平台，填写凭据与余额接口信息。
            <br />
            配置保存功能建设中。
          </Typography.Paragraph>
        </Space>
      </section>

      <PresetGallery
        presets={presets}
        onUse={setUsePreset}
        onEdit={setPresetModal}
        onExport={exportPreset}
        onReset={(p) => void resetBuiltinPreset(p)}
        onDelete={deleteCustomPreset}
        onImport={() =>
          setTransferReq({
            mode: 'import',
            title: '导入预设',
            desc: '粘贴单个预设的 JSON（可由预设卡片的「导出」得到），点击「确认导入」',
            expectedKind: 'preset',
          })
        }
        onAdd={() => setPresetModal(null)}
      />

      <section className="section">
        <Flex justify="space-between" align="flex-end" gap={16} wrap>
          <Typography.Title level={4} style={{ margin: 0 }}>
            添加平台
          </Typography.Title>
          <Typography.Text type="secondary">New Platform</Typography.Text>
        </Flex>
        <Typography.Paragraph type="secondary" style={{ margin: '8px 0 16px' }}>
          懒得手动配置？把本仓库和调用方式丢给 AI，让它按这里的配置格式（请求方法 / URL /
          请求头 / 处理函数）帮你生成平台配置，然后通过下方「导入 / 导出」导入即可。
        </Typography.Paragraph>

        <div ref={formRef2}>
          <Form layout="vertical" onFinish={() => void save()}>
            <Form.Item label="平台名称">
              <Input
                placeholder="DeepSeek"
                value={form.name}
                onChange={(e) => patchForm({ name: e.target.value })}
              />
            </Form.Item>
            <Form.Item label="平台主页 URL">
              <Input
                placeholder="https://platform.example.com"
                value={form.homepage}
                onChange={(e) => patchForm({ homepage: e.target.value })}
              />
            </Form.Item>
            <Row gutter={16}>
              <Col xs={24} md={12}>
                <Form.Item label="请求方法">
                  <Select
                    value={form.method}
                    onChange={(v) => patchForm({ method: v })}
                    options={[
                      { value: 'GET', label: 'GET' },
                      { value: 'POST', label: 'POST' },
                    ]}
                  />
                </Form.Item>
              </Col>
              <Col xs={24} md={12}>
                <Form.Item label="请求体（JSON，仅 POST）">
                  <Input
                    placeholder='{"api_key": "sk-xxx"}'
                    disabled={form.method !== 'POST'}
                    value={form.bodyText}
                    onChange={(e) => patchForm({ bodyText: e.target.value })}
                  />
                </Form.Item>
              </Col>
            </Row>
            <Form.Item label="请求 URL">
              <Input
                ref={urlInputRef}
                placeholder="https://api.deepseek.com/user/balance"
                value={form.url}
                onChange={(e) => patchForm({ url: e.target.value })}
              />
            </Form.Item>
            <Form.Item label="请求头（JSON）">
              <CodeEditor
                value={form.headersText}
                mode="json"
                onChange={(v) => patchForm({ headersText: v })}
                onModEnter={() => void validate()}
              />
            </Form.Item>
            <Form.Item
              label="处理函数（raw 为原始响应文本，在函数内自由解析并返回余额）"
              extra="返回余额数值（number），不要拼单位；单位 / 前后缀 / 小数位写在右侧「显示格式函数」里"
            >
              <CodeEditor
                value={form.handlerText}
                mode="js"
                onChange={(v) => patchForm({ handlerText: v })}
                onModEnter={() => void validate()}
              />
            </Form.Item>
            <Form.Item
              label="显示格式函数（必填，入参 v 为处理函数返回的数值，返回最终展示字符串）"
              extra="示例：v.toFixed(4) + 'G'（流量）、'¥' + v.toFixed(2) + '元'、v.toLocaleString('zh-CN')（千分位）；函数返回非字符串或抛错时卡片回退显示原始数值"
            >
              <CodeEditor
                value={form.formatText}
                mode="js"
                placeholder="function (v) { return v.toLocaleString('zh-CN') }"
                onChange={(v) => patchForm({ formatText: v })}
                onModEnter={() => void validate()}
              />
            </Form.Item>
            <Space wrap>
              <Button type="primary" htmlType="submit">
                {editingId ? '更新配置' : '保存配置'}
              </Button>
              {editingId && <Button onClick={cancelEdit}>取消编辑</Button>}
              <Button disabled={validating} loading={validating} onClick={() => void validate()}>
                验证
              </Button>
            </Space>
            {formMsg.text && (
              <div style={{ marginTop: 16 }}>
                <Alert type={formMsg.error ? 'error' : 'info'} message={formMsg.text} showIcon={false} />
              </div>
            )}
            <Form.Item label="验证输出" style={{ marginTop: 16 }}>
              <CodeEditor value={verifyText} mode="json" readOnly />
            </Form.Item>
          </Form>
        </div>
      </section>

      <ImportExportSection
        message={transferMsg}
        onExportAll={() => void exportAll()}
        onImportConfig={() =>
          setTransferReq({
            mode: 'import',
            title: '导入配置',
            desc: '支持完整配置、单个平台、单个预设的 JSON（可由任何「导出」按钮得到），粘贴后自动识别，点击「确认导入」',
          })
        }
      />

      <PlatformTable
        platforms={platforms}
        rowMsg={rowMsg}
        onMove={(id, dir) => void movePlatform(id, dir)}
        onEdit={startEdit}
        onExport={exportPlatform}
        onTest={(id) => void testRow(id)}
        onFetch={(id) => void fetchRow(id)}
        onDelete={(p) => removeRowPlatform(p.id, p.name)}
        onImport={() =>
          setTransferReq({
            mode: 'import',
            title: '导入平台',
            desc: '粘贴单个平台的 JSON（可由「已配置平台」表格的「导出」得到），点击「确认导入」',
            expectedKind: 'platform',
          })
        }
      />

      {/* ========== 弹窗 ========== */}
      {usePreset && (
        <UsePresetModal
          preset={usePreset}
          onError={notify}
          onCancel={() => setUsePreset(null)}
          onConfirm={applyPresetFill}
        />
      )}
      {presetModal !== undefined && (
        <EditPresetModal
          preset={presetModal}
          onClose={() => setPresetModal(undefined)}
          onNotify={notify}
          onSaved={() => void loadPresets()}
        />
      )}
      {transferReq && (
        <TransferModal
          request={transferReq}
          onClose={() => setTransferReq(null)}
          onImported={(summary) => void onImported(summary)}
        />
      )}
    </>
  )
}
