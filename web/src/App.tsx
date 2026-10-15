/**
 * 应用根组件：认证探测、顶部导航、主题切换与三个页签的挂载。
 *
 * 关键 props：无。
 *
 * 行为：
 * - 启动探测 `AUTH_MODE`；`builtin` 未登录时渲染 `LoginPage`，`sso` 由前置认证层保证。
 * - 页签切换写入 `sessionStorage`（刷新后保持）；平台配置页与日志页保持挂载以免丢失编辑态。
 * - 窄屏（< 768px）不隐藏导航，改用“目录”按钮从右侧滑出 `Drawer`（Ant Design 默认样式）。
 *
 * 注意：主题三态（自动 / 亮色 / 暗色）宽屏为内联 `Segmented`、窄屏收进折叠菜单；退出在
 * `sso` 模式跳 `/_auth/logout`，`builtin` 调本地登出。
 * 可访问性：`Drawer` 由 antd 提供焦点陷阱与 Esc 关闭；“目录”为按钮元素，可键盘触发。
 */
import { useEffect, useState, useSyncExternalStore } from 'react'
import { Avatar, Badge, Button, Drawer, Dropdown, Layout, Menu, Segmented, Space, Typography, theme } from 'antd'
import DashboardTab from '@/components/Dashboard'
import LogsTab from '@/components/Logs'
import ConfigTab from '@/components/Config'
import LoginPage from '@/components/Auth/LoginPage'
import { useStore } from '@/store/useStore'
import { useAuthStore } from '@/store/useAuthStore'
import { setThemeMode, useThemeMode, type ThemeMode } from '@/theme'

export type TabName = 'dashboard' | 'config' | 'logs'

const TABS: Array<{ key: TabName; label: string }> = [
  { key: 'dashboard', label: '监控面板' },
  { key: 'config', label: '平台配置' },
  { key: 'logs', label: '操作日志' },
]

const TAB_KEY = 'quotahub-tab'

function initialTab(): TabName {
  const saved = sessionStorage.getItem(TAB_KEY)
  return saved === 'config' || saved === 'logs' ? saved : 'dashboard'
}

// 窄屏断点, 与 antd 的 md (768px) 对齐
const NARROW_QUERY = '(max-width: 767.98px)'

function subscribeNarrow(callback: () => void): () => void {
  const mq = window.matchMedia(NARROW_QUERY)
  mq.addEventListener('change', callback)
  return () => mq.removeEventListener('change', callback)
}

function getNarrow(): boolean {
  return window.matchMedia(NARROW_QUERY).matches
}

function useNarrow(): boolean {
  return useSyncExternalStore(subscribeNarrow, getNarrow, () => false)
}

const THEME_OPTIONS: Array<{ value: ThemeMode; label: string }> = [
  { value: 'auto', label: '自动' },
  { value: 'light', label: '亮色' },
  { value: 'dark', label: '暗色' },
]

// 主题三态切换: 宽屏内联 Segmented; 窄屏收进折叠菜单(菜单内仍是同一个 Segmented), 不挤坏顶栏。
function ThemeSwitcher({ narrow }: { narrow: boolean }) {
  const { token } = theme.useToken()
  const mode = useThemeMode()
  const onChange = (value: string | number) => setThemeMode(value as ThemeMode)

  if (!narrow) {
    return <Segmented value={mode} onChange={onChange} options={THEME_OPTIONS} />
  }
  return (
    <Dropdown
      trigger={['click']}
      placement="bottomRight"
      popupRender={() => (
        <div
          style={{
            padding: token.paddingXS,
            background: token.colorBgElevated,
            borderRadius: token.borderRadiusLG,
            boxShadow: token.boxShadowSecondary,
          }}
        >
          <Segmented vertical value={mode} onChange={onChange} options={THEME_OPTIONS} />
        </div>
      )}
    >
      <Button>主题</Button>
    </Dropdown>
  )
}

export default function App() {
  const { token } = theme.useToken()
  const [tab, setTab] = useState<TabName>(initialTab)
  const [navOpen, setNavOpen] = useState(false)
  const narrow = useNarrow()

  const ready = useAuthStore((s) => s.ready)
  const mode = useAuthStore((s) => s.mode)
  const user = useAuthStore((s) => s.user)
  const initAuth = useAuthStore((s) => s.init)
  const logout = useAuthStore((s) => s.logout)

  // 启动探测认证模式 + builtin 登录态
  useEffect(() => {
    void initAuth()
  }, [initAuth])

  // sso 模式由前置认证层保证; builtin 需已登录
  const authenticated = mode === 'sso' || !!user

  // 首屏数据预载 (已登录才加载, 避免未登录时无谓的 401)
  useEffect(() => {
    if (!authenticated) return
    const s = useStore.getState()
    void s.loadPresets()
    void s.loadPlatforms()
    void s.loadDashboard()
    void s.loadSettings()
  }, [authenticated])

  if (!ready) return null
  if (mode === 'builtin' && !user) return <LoginPage />

  const switchTab = (name: TabName) => {
    setTab(name)
    setNavOpen(false)
    sessionStorage.setItem(TAB_KEY, name)
  }

  // 退出按模式分发: builtin 清本地会话, sso 交给前置认证层
  const onLogout = () => {
    if (mode === 'sso') {
      window.location.href = '/_auth/logout'
      return
    }
    void logout()
  }

  return (
    <Layout className="qh-layout">
      <Layout.Header className="qh-header" style={{ background: token.colorBgContainer }}>
        <Space size="middle" align="center">
          <Avatar shape="square" size="large">
            QH
          </Avatar>
          <Typography.Text strong>QuotaHub</Typography.Text>
        </Space>

        {!narrow && (
          <Menu
            mode="horizontal"
            selectedKeys={[tab]}
            items={TABS}
            onClick={({ key }) => switchTab(key as TabName)}
            className="qh-nav"
          />
        )}

        <Space size="small" align="center">
          {narrow && <Button onClick={() => setNavOpen(true)}>目录</Button>}
          {!narrow && mode === 'builtin' && user && <Typography.Text>{user.name}</Typography.Text>}
          <ThemeSwitcher narrow={narrow} />
          <Button onClick={onLogout}>退出</Button>
          {!narrow && <Badge status="success" text="系统正常" />}
        </Space>
      </Layout.Header>

      <Drawer
        title="导航"
        placement="right"
        open={narrow && navOpen}
        onClose={() => setNavOpen(false)}
        width={240}
      >
        <Menu
          mode="inline"
          selectedKeys={[tab]}
          items={TABS}
          onClick={({ key }) => switchTab(key as TabName)}
        />
      </Drawer>

      <Layout.Content>
        <div className="main">
          <div className={`tab${tab === 'dashboard' ? ' is-active' : ''}`}>
            <DashboardTab />
          </div>
          {/* 平台配置页保持挂载(隐藏), CodeMirror 内容不丢 */}
          <div className={`tab${tab === 'config' ? ' is-active' : ''}`}>
            <ConfigTab />
          </div>
          {/* 日志页保持挂载, 激活时才加载(与旧版点击 tab 时加载一致) */}
          <div className={`tab${tab === 'logs' ? ' is-active' : ''}`}>
            <LogsTab active={tab === 'logs'} />
          </div>
        </div>
      </Layout.Content>

      <Layout.Footer className="qh-footer">
        <Typography.Text type="secondary">QuotaHub © 2026</Typography.Text>
        <Typography.Text type="secondary">Open Source · MIT License</Typography.Text>
      </Layout.Footer>
    </Layout>
  )
}
