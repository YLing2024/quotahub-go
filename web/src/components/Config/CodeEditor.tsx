/**
 * CodeMirror 6 编辑器封装，替代旧版 CodeMirror 5 vendor 包。
 *
 * 关键 props：
 * - `value`（string，必填）：受控文本。
 * - `onChange?`（`(value: string) => void`）：内容变更回调；只读模式不触发。
 * - `mode?`（`'json' | 'js'`，默认 `'json'`）：语法高亮语言。
 * - `readOnly?`（boolean，默认 `false`）：只读展示（如验证输出）。
 * - `placeholder?`（string）：空内容占位。
 * - `onModEnter?`（`() => void`）：按 `Mod-Enter`（Cmd/Ctrl+Enter）触发，常用于“验证”。
 *
 * 注意：编辑器主题跟随全局明暗（深色 oneDark / 浅色内置 light），外层容器外观取自 antd
 * token；不要在调用处写死颜色。`onModEnter` 仅在非只读时生效。
 * 可访问性：底层 CodeMirror 自带键盘导航与 aria 语义；只读态不可编辑但仍可选中复制。
 */
import { useMemo } from 'react'
import { theme } from 'antd'
import CodeMirror from '@uiw/react-codemirror'
import { oneDark } from '@codemirror/theme-one-dark'
import { javascript } from '@codemirror/lang-javascript'
import { json } from '@codemirror/lang-json'
import { EditorState, Prec, type Extension } from '@codemirror/state'
import { keymap } from '@codemirror/view'
import { useResolvedTheme } from '@/theme'

interface Props {
  value: string
  onChange?(value: string): void
  mode?: 'js' | 'json'
  readOnly?: boolean
  placeholder?: string
  onModEnter?: () => void
}

export default function CodeEditor({ value, onChange, mode = 'json', readOnly, placeholder, onModEnter }: Props) {
  const { token } = theme.useToken()
  const resolved = useResolvedTheme()
  const extensions = useMemo(() => {
    const list: Extension[] = [mode === 'json' ? json() : javascript()]
    if (onModEnter && !readOnly) {
      list.push(
        Prec.highest(
          keymap.of([
            {
              key: 'Mod-Enter',
              run: () => {
                onModEnter()
                return true
              },
            },
          ]),
        ),
      )
    }
    if (readOnly) list.push(EditorState.readOnly.of(true))
    return list
  }, [mode, readOnly, onModEnter])

  return (
    <div
      style={{
        background: token.colorBgContainer,
        border: `1px solid ${readOnly ? token.colorBorderSecondary : token.colorBorder}`,
        borderRadius: token.borderRadius,
        overflow: 'hidden',
      }}
    >
      <CodeMirror
        value={value}
        height="auto"
        placeholder={placeholder}
        extensions={extensions}
        editable={!readOnly}
        theme={resolved === 'dark' ? oneDark : 'light'}
        basicSetup={{
          lineNumbers: true,
          foldGutter: false,
          autocompletion: false,
          highlightActiveLine: !readOnly,
          searchKeymap: false,
        }}
        onChange={(v) => onChange?.(v)}
      />
    </div>
  )
}
