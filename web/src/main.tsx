import { StrictMode } from 'react'
import ReactDOM from 'react-dom/client'
import { App as AntdApp, ConfigProvider, theme } from 'antd'
import zhCN from 'antd/locale/zh_CN'
import App from './App'
import { useResolvedTheme } from './theme'
import 'antd/dist/reset.css'
import './styles/style.css'

// antd 应用级上下文: 供 message / modal 等静态方法使用。
// algorithm 跟随主题模块解析结果 (auto 跟随系统, light/dark 强制) —— 只用 antd 默认算法, 不做自定义 token 映射。
function Root() {
  const resolved = useResolvedTheme()
  return (
    <ConfigProvider
      locale={zhCN}
      theme={{ algorithm: resolved === 'dark' ? theme.darkAlgorithm : theme.defaultAlgorithm }}
    >
      <AntdApp>
        <App />
      </AntdApp>
    </ConfigProvider>
  )
}

ReactDOM.createRoot(document.getElementById('root')!).render(
  <StrictMode>
    <Root />
  </StrictMode>,
)
