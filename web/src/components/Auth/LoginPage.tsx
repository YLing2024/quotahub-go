/**
 * 本地登录页（builtin 模式）：账号 + 口令，失败提示，429 显示倒计时。
 *
 * 关键 props：无（登录态由 `useAuthStore` 提供）。
 * 注意：仅在 `AUTH_MODE=builtin` 且未登录时由 App 渲染；sso 模式不会进入本页。
 * 可访问性：使用 antd `Form` 的 label 关联与 `autoComplete`，提交按钮在请求中禁用并显示 loading。
 */
import { useEffect, useState } from 'react'
import { Alert, Avatar, Button, Card, Form, Input, Layout, Space, Typography } from 'antd'
import { ApiError } from '@/api/client'
import { useAuthStore } from '@/store/useAuthStore'

type LoginForm = { username: string; password: string }

export default function LoginPage() {
  const login = useAuthStore((s) => s.login)
  const [form] = Form.useForm<LoginForm>()
  const username = Form.useWatch('username', form)
  const password = Form.useWatch('password', form)

  const [loading, setLoading] = useState(false)
  const [error, setError] = useState('')
  const [lockLeft, setLockLeft] = useState(0)

  // 锁定期倒计时
  useEffect(() => {
    if (lockLeft <= 0) return
    const timer = window.setInterval(() => {
      setLockLeft((n) => (n > 0 ? n - 1 : 0))
    }, 1000)
    return () => window.clearInterval(timer)
  }, [lockLeft])

  const submit = async (values: LoginForm) => {
    if (loading || lockLeft > 0) return
    setLoading(true)
    setError('')
    try {
      await login(values.username, values.password)
      form.setFieldValue('password', '')
    } catch (err) {
      if (err instanceof ApiError && err.status === 429 && err.retryAfter) {
        setLockLeft(err.retryAfter)
        setError('尝试过于频繁')
      } else {
        setError(err instanceof Error ? err.message : '登录失败')
      }
    } finally {
      setLoading(false)
    }
  }

  const disabled = loading || lockLeft > 0 || !username || !password
  const message = lockLeft > 0 ? `${error}，请 ${lockLeft} 秒后重试` : error

  return (
    <Layout className="qh-layout">
      <Layout.Content className="qh-auth">
        <Card className="qh-auth-card">
          <Space size="middle" align="center">
            <Avatar shape="square" size="large">
              QH
            </Avatar>
            <Typography.Title level={4} className="qh-auth-title">
              QuotaHub
            </Typography.Title>
          </Space>
          <Typography.Paragraph type="secondary">登录后查看与管理平台余额。</Typography.Paragraph>

          <Form form={form} layout="vertical" requiredMark={false} onFinish={(v) => void submit(v)}>
            <Form.Item name="username" label="账号">
              <Input autoComplete="username" autoFocus />
            </Form.Item>
            <Form.Item name="password" label="口令">
              <Input.Password autoComplete="current-password" />
            </Form.Item>
            {message && (
              <Form.Item>
                <Alert type="error" showIcon message={message} />
              </Form.Item>
            )}
            <Form.Item>
              <Button type="primary" htmlType="submit" block disabled={disabled}>
                {loading ? '登录中…' : '登录'}
              </Button>
            </Form.Item>
          </Form>
        </Card>
      </Layout.Content>

      <Layout.Footer className="qh-footer">
        <Typography.Text type="secondary">QuotaHub © 2026</Typography.Text>
        <Typography.Text type="secondary">Open Source · MIT License</Typography.Text>
      </Layout.Footer>
    </Layout>
  )
}
