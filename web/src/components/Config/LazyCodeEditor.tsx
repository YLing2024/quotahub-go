/**
 * CodeEditor 的懒加载包装。
 *
 * 真正的编辑器（CodeMirror 6，打包后约 520 KB）只有「配置里改 headers / 提取函数」时才用得到，
 * 也就是设置页里点开输入区那一刻。原来它被 Config/index.tsx 静态 import，Vite 会把它当成
 * 首屏依赖（modulepreload）一起下载——首屏体积直接翻倍。
 *
 * 这里用 React.lazy 把它切成独立 chunk：首屏只加载主包，真正渲染编辑器时再拉这 520 KB。
 * 加载期间给骨架占位，避免布局跳动。对外 props 与 CodeEditor 完全一致，调用方不用改。
 */
import { Suspense, lazy } from 'react'
import { Skeleton } from 'antd'

export interface CodeEditorProps {
  value: string
  onChange?(value: string): void
  mode?: 'js' | 'json'
  readOnly?: boolean
  placeholder?: string
  onModEnter?: () => void
}

const Inner = lazy(() => import('./CodeEditor'))

export default function LazyCodeEditor(props: CodeEditorProps) {
  return (
    <Suspense fallback={<Skeleton active paragraph={{ rows: 4 }} title={false} />}>
      <Inner {...props} />
    </Suspense>
  )
}
