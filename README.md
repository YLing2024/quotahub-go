# QuotaHub (Go)

多平台 LLM 余额 / 配额监控面板的 Go 重写：按预设（各平台官方开放接口）定时采集额度与用量，
记录历史采样，出趋势图与汇总；带账号体系与前端面板。HTTP 契约与旧 Node 实现逐条对齐。

- 单二进制：前端产物经 `//go:embed` 打进可执行文件。
- 仅标准库 `net/http`，无 Web 框架；日志 `log/slog`。
- 存储：SQLite（默认，`modernc.org/sqlite` 纯 Go）或 MySQL（`go-sql-driver/mysql`），
  差异收在方言层，详见 `docs/DB.md`。
- 采集脚本在 JS 沙箱（`github.com/dop251/goja`）中执行，带超时；请求默认拒绝私网（SSRF 防护）。

## 快速开始

```bash
# 构建（会先装前端依赖并构建前端产物）
make build

# 启动：默认 127.0.0.1:19250，builtin 认证，SQLite
QUOTAHUB_DATA_DIR=./data ./quotahub-go
```

首启在 `users` 为空时按 `QUOTAHUB_ADMIN_USER`/`QUOTAHUB_ADMIN_PASSWORD` 创建管理员；
口令留空则随机生成并只在启动日志打印一次。

配置项见 `deploy/env.example`。

## 双库与跨库搬运

```bash
# 源库只读，目标库跑增量迁移；--verify 逐表比对条数与抽样行
./quotahub-go migrate-data --from sqlite:./data/monitor.db \
  --to "mysql:user:pass@tcp(127.0.0.1:3306)/quotahub" --verify
```

## 目录

- `cmd/quotahub-go/`：进程入口（装配 + 优雅退出 + `migrate-data`）。
- `internal/{config,store,sqlite,mysql,scrypt,jsonstore,model,platform,presets,settings,logs,collect,auth,httpapi}/`。
- `web/`：前端（React + Vite + TS + Ant Design），`//go:embed` 托管。
- `deploy/`：systemd / env / nginx 样例（只提交，不安装）。
- `docs/API.md`：HTTP 契约；`docs/DB.md`：数据库设计；`docs/COMPONENTS.md`：前端组件。

## 许可

MIT（见 `LICENSE`）。
