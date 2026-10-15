import { describe, expect, it } from 'vitest'
import { classifyConfig, KIND_NAME } from './classify'

describe('classifyConfig', () => {
  it('识别完整配置(platforms/presets 数组)并统计数量', () => {
    const hit = classifyConfig({
      platforms: [{ name: 'a' }, { name: 'b' }],
      presets: [{ name: 'p' }],
    })
    expect(hit?.kind).toBe('full')
    expect(hit?.label).toBe('完整配置：2 个平台、1 个预设')
  })

  it('仅有 presets 数组也识别为完整配置', () => {
    const hit = classifyConfig({ presets: [] })
    expect(hit?.kind).toBe('full')
    expect(hit?.label).toBe('完整配置：0 个平台、0 个预设')
  })

  it('识别单个平台(name + request.url)', () => {
    const hit = classifyConfig({
      name: 'DeepSeek',
      request: { method: 'GET', url: 'https://api.example.com/user/balance', headers: {} },
    })
    expect(hit?.kind).toBe('platform')
    expect(hit?.label).toBe('单个平台「DeepSeek」')
  })

  it('request.url 非字符串不算平台', () => {
    expect(
      classifyConfig({ name: 'x', request: { url: 123 } }),
    ).toBeNull()
  })

  it('识别单个预设(name/fields/urlTemplate/extractorTemplate)', () => {
    const hit = classifyConfig({
      name: 'NEWAPI',
      fields: [{ key: 'token', label: '令牌' }],
      urlTemplate: '{{baseUrl}}/api/user/self',
      headersTemplate: {},
      extractorTemplate: 'function (data) { return data.balance }',
    })
    expect(hit?.kind).toBe('preset')
    expect(hit?.label).toBe('单个预设「NEWAPI」')
  })

  it('缺 extractorTemplate 不算预设', () => {
    expect(classifyConfig({ name: 'x', fields: [], urlTemplate: 'y' })).toBeNull()
  })

  it('非法输入返回 null(数组/null/标量)', () => {
    expect(classifyConfig(null)).toBeNull()
    expect(classifyConfig([1, 2])).toBeNull()
    expect(classifyConfig('str')).toBeNull()
    expect(classifyConfig(42)).toBeNull()
    expect(classifyConfig({})).toBeNull()
  })

  it('KIND_NAME 覆盖三种格式', () => {
    expect(KIND_NAME.full).toBe('完整配置')
    expect(KIND_NAME.platform).toBe('单个平台')
    expect(KIND_NAME.preset).toBe('单个预设')
  })
})
