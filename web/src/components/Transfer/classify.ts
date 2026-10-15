// 导入导出格式自动识别 (与旧 app.js classifyConfig 语义一致)

export type ConfigKind = 'full' | 'platform' | 'preset'

export interface Classified {
  kind: ConfigKind
  label: string
}

export const KIND_NAME: Record<ConfigKind, string> = {
  full: '完整配置',
  platform: '单个平台',
  preset: '单个预设',
}

export function classifyConfig(obj: unknown): Classified | null {
  if (!obj || typeof obj !== 'object' || Array.isArray(obj)) return null
  const o = obj as Record<string, unknown>
  if (Array.isArray(o.platforms) || Array.isArray(o.presets)) {
    // 与旧实现一致: 单侧缺省按空数组计数
    const np = Array.isArray(o.platforms) ? o.platforms.length : 0
    const ns = Array.isArray(o.presets) ? o.presets.length : 0
    return { kind: 'full', label: `完整配置：${np} 个平台、${ns} 个预设` }
  }
  if (
    typeof o.name === 'string' &&
    o.request &&
    typeof o.request === 'object' &&
    typeof (o.request as Record<string, unknown>).url === 'string'
  ) {
    return { kind: 'platform', label: `单个平台「${o.name}」` }
  }
  if (
    typeof o.name === 'string' &&
    Array.isArray(o.fields) &&
    typeof o.urlTemplate === 'string' &&
    typeof o.extractorTemplate === 'string'
  ) {
    return { kind: 'preset', label: `单个预设「${o.name}」` }
  }
  return null
}
