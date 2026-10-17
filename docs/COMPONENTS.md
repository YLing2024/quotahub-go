# QuotaHub 前端组件登记

本文件登记 `web/src` 下的可复用组件（Ant Design 默认设计语言；颜色 / 圆角 / 字号一律走 antd
token，不新造色值）。新增可复用组件必须同一笔在此登记，删除组件同步删条目。每个组件的文件头
都有 TSDoc（用途 / props / 使用要点 / 注意事项 / 可访问性）。

| 组件 | 用途 | 关键 props / 事件 | 最小示例 | 注意事项 |
| --- | --- | --- | --- | --- |
| `App`（`App.tsx`） | 应用根：认证探测、顶栏导航、主题切换、三页签挂载 | 无 props | `<App />` | 窄屏（<768px）不隐藏导航，改用“目录”按钮从右侧滑出 `Drawer`；页签写入 `sessionStorage` |
| `LoginPage`（`components/Auth/LoginPage.tsx`） | builtin 模式本地登录页 | 无 props（登录态来自 `useAuthStore`） | `<LoginPage />` | 仅 builtin 未登录时渲染；429 显示倒计时；sso 模式不会进入 |
| `ConfigTab`（`components/Config/index.tsx`） | 平台配置页编排：预设 / 表单 / 导入导出 / 表格 | 无 props | `<ConfigTab />` | 保持挂载（隐藏）以免 CodeMirror 内容丢失；表单校验失败提示走消息区 |
| `PresetGallery`（`components/Config/PresetGallery.tsx`） | 预设卡片网格 | `presets`；`onUse/onEdit/onExport/onReset/onDelete(preset)`、`onImport/onAdd()` | `<PresetGallery presets={presets} onUse={setUsePreset} … />` | 卡片内按钮 `stopPropagation`，避免误触发套用；内置预设给“重置默认” |
| `PlatformTable`（`components/Config/PlatformTable.tsx`） | 已配置平台表格 | `platforms`、`rowMsg`；`onMove/onEdit/onTest/onFetch/onDelete/onExport/onImport` | `<PlatformTable platforms={platforms} rowMsg={rowMsg} … />` | URL 列用 `title` 承载完整地址；操作列固定右侧 |
| `ImportExportSection`（`components/Config/ImportExportSection.tsx`） | 导入 / 导出区 | `message`；`onExportAll()`、`onImportConfig()` | `<ImportExportSection message={msg} onExportAll={fn} onImportConfig={fn} />` | 导入按 id 合并、同名覆盖；结果消息文本 + 颜色双重表达 |
| `CodeEditor`（`components/Config/CodeEditor.tsx`） | CodeMirror 6 编辑器封装 | `value`；`onChange?`、`mode?='json'`、`readOnly?=false`、`placeholder?`、`onModEnter?` | `<CodeEditor value={text} mode="js" onChange={fn} />` | 主题跟随全局明暗；`onModEnter` 仅非只读生效；不用在调用处写死颜色 |
| `EditPresetModal`（`components/Config/EditPresetModal.tsx`） | 预设编辑弹窗 | `preset`（`null`=新增）；`onClose()`、`onNotify(msg,isError?)`、`onSaved()` | `<EditPresetModal preset={p} onClose={fn} onNotify={fn} onSaved={fn} />` | 内置预设可重置默认；校验失败不关闭弹窗 |
| `UsePresetModal`（`components/Config/UsePresetModal.tsx`） | 预设“使用（快速配置）”弹窗 | `preset`；`onConfirm(fill)`、`onCancel()`、`onError(msg)` | `<UsePresetModal preset={p} onConfirm={fn} onCancel={fn} onError={fn} />` | 只回填 url/headers/handler 三项，不动表单其他字段 |
| `TransferModal`（`components/Transfer/TransferModal.tsx`） | 导入 / 导出弹窗，自动识别格式 | `request`（`TransferRequest`）；`onClose()`、`onImported(summary)` | `<TransferModal request={req} onClose={fn} onImported={fn} />` | `expectedKind` 仅提示；其他格式仍可导入 |
| `DashboardTab`（`components/Dashboard/index.tsx`） | 面板页：余额卡片 + 汇总 + 采集间隔 | 无 props | `<DashboardTab />` | 余额文本在前端执行 `platform.format`，失败回退原始数值；间隔上限 86400 秒 |
| `LogsTab`（`components/Logs/index.tsx`） | 操作日志页 | `active`（boolean） | `<LogsTab active={tab === 'logs'} />` | 仅激活时拉取日志；清空需二次确认 |
| `TrendChartModal`（`components/TrendChart/TrendChartModal.tsx`） | 余额趋势图弹窗 | `id`、`name`；`onClose()` | `<TrendChartModal id={p.id} name={p.name} onClose={fn} />` | 纯逻辑在 `chartLogic.ts`；档位与自定义区间校验文案与旧实现一致 |
| `TrendChartECharts`（`components/TrendChart/TrendChartECharts.tsx`） | ECharts 折线图（懒加载） | `points`、`winStart`、`winEnd`；`onWindowChange(start,end)` | 仅由 `TrendChartModal` 动态 import | 使 echarts 独立成 chunk、不进主入口；颜色取 antd token |

## 响应式（移动端）约定

- **唯一样式文件**：`web/src/styles/responsive-mobile.css`，只在
  `@media (max-width: 991.98px), (pointer: coarse)` 内生效；≥992px 且鼠标（`pointer: fine`）
  时不命中任何规则，桌面观感逐像素不变。所有覆盖用 `body` 前缀提高权重，稳定压过 antd 运行时
  注入的同名规则。
- **触摸目标**：主要按钮（default/middle）≥44px 高；`size="small"` 紧凑按钮（含次要图标与行内
  操作）≥32×32；抽屉导航项、时间范围与主题分段控件 ≥44px。
- **表单控件**：`Input` / `Select` / `DatePicker` 等 ≥44px 高、字号 ≥16px（防 iOS 聚焦放大）。
  antd `Select` 命中区是内层 `.ant-select-selector`，且外层 `.ant-select-single` 固定
  `height`，必须同时撑高外层并校正内层行高，仅设 `min-height` 无效。
- **行内操作间距**：`Space` 间距写在行内 style 上，用标记类 `qh-actions` + 媒体查询内
  `!important` 覆盖为 ≥8px；新增「多个相邻按钮」的操作组时给该 `Space` 加 `className="qh-actions"`。
- **断言测试**：`web/src/responsive.test.tsx` 解析上述 CSS 规则并渲染带 `qh-actions` 的组件做
  断言（jsdom 无真实排版，按需求书口径改为断言样式 / class；真浏览器量测见交付报告）。

## 说明

- `App.tsx` 内的 `ThemeSwitcher` 为模块内私有辅助组件（宽屏内联 `Segmented` / 窄屏折叠菜单），
  不对外复用，故不单独登记。
- 扁平模块（非组件）不在此登记：`api/*`、`store/*`、`lib/format.ts`、`theme.ts`、
  `components/TrendChart/chartLogic.ts`、`components/Transfer/classify.ts` 等以纯函数测试保障。
