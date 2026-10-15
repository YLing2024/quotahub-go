# QuotaHub (Go)

多平台 LLM 余额 / 配额监控面板的 Go 重写：按预设（各平台官方开放接口）定时采集额度与用量，
记录历史采样，出趋势图与汇总；带账号体系与前端面板。HTTP 契约与旧 Node 实现逐条对齐。

- 单二进制：前端产物经 `//go:embed` 打进可执行文件。
- 仅标准库 `net/http`，无 Web 框架；日志 `log/slog`。
- 存储：**仅 MySQL**（`go-sql-driver/mysql`），建表与增量迁移见 `docs/DB.md`；
  旧版 SQLite 数据由独立工具 `tools/migrate-sqlite-to-mysql` 一次性搬运。
- 采集脚本在 JS 沙箱（`github.com/dop251/goja`）中执行，带超时；请求默认拒绝私网（SSRF 防护）。

## 快速开始

```bash
# 构建（会先装前端依赖并构建前端产物）
make build

# 配置数据库（口令只从环境变量注入；不要提交真实 .env）
cp .env.example .env   # 填好 DB_* 后自行注入到进程环境

# 启动：默认 127.0.0.1:19250，builtin 认证
QUOTAHUB_DATA_DIR=./data ./quotahub-go
```

首启在 `users` 为空时按 `QUOTAHUB_ADMIN_USER`/`QUOTAHUB_ADMIN_PASSWORD` 创建管理员；
口令留空则随机生成并只在启动日志打印一次。配置项见 `.env.example`。

## 数据库

服务启动即连 MySQL 并跑增量迁移（幂等、事务内，绝不 DROP / TRUNCATE / 重建）。段参数与
`DB_DSN` 二选一，默认连 `127.0.0.1:3306` 的 `quotahub` 库。旧 SQLite 数据搬迁：

```bash
cd tools/migrate-sqlite-to-mysql
# 演练（只读源库报计数）
GOFLAGS=-mod=mod GOPROXY=off go run . --dry-run --sqlite /path/to/old-monitor.db
# 正式搬运 + 逐表条数与抽样比对（源库只读）
GOFLAGS=-mod=mod GOPROXY=off go run . --sqlite /path/to/old-monitor.db \
  --mysql-host 127.0.0.1 --mysql-name quotahub --mysql-user quotahub --mysql-password "$DB_PASSWORD"
```

## 目录

- `cmd/quotahub-go/`：进程入口（装配 + 优雅退出）。
- `internal/{config,store,mysql,scrypt,jsonstore,model,platform,presets,settings,logs,collect,auth,httpapi}/`。
- `tools/migrate-sqlite-to-mysql/`：一次性迁移工具（独立模块，不进服务运行时）。
- `web/`：前端（React + Vite + TS + Ant Design），`//go:embed` 托管。
- `deploy/`：systemd / nginx 样例（只提交，不安装）。
- `docs/API.md`：HTTP 契约；`docs/DB.md`：数据库设计；`docs/COMPONENTS.md`：前端组件。

## 许可

MIT（见 `LICENSE`）。
