# AGENTS.md — quotahub-go 维护约定

本仓库是 QuotaHub（LLM 平台余额 / 配额看板）的 Go 重写。旧实现 `/root/proj/quotahub` 是
**功能权威**（只读，不得修改）。权威工程规范见 `/root/proj/ENGINEERING_BASELINE.md`；
本文件只做本仓库补充，不降低其要求。

## 技术栈（固定）

- HTTP：仅标准库 `net/http`；不引 Web 框架与路由库（分发顺序手写在 `internal/httpapi`）。
- 数据库：`modernc.org/sqlite`（纯 Go、无 cgo）+ `github.com/go-sql-driver/mysql`；
  业务 SQL 全收在 `internal/store`，方言差异在 `internal/sqlite`、`internal/mysql`。
- 采集脚本沙箱：`github.com/dop251/goja`（纯 Go JS 引擎，无 cgo），保持旧实现
  `handler/parse/extractor` 的 JS 函数契约。
- 口令哈希：`internal/scrypt` 纯标准库实现（N=16384,r=8,p=1，与旧 `node:crypto` 参数一致）。
- 配置：只读环境变量（不引 YAML / dotenv）。
- 日志：标准库 `log/slog`。
- 前端：`web/`，React + Vite + TS + **Ant Design 默认设计语言**；产物经根包
  `//go:embed all:web/dist` 打进二进制。

**允许的第三方依赖只有 `modernc.org/sqlite`、`github.com/go-sql-driver/mysql` 与
`github.com/dop251/goja`**；新增依赖前先改本文件与 `/root/proj/go-rewrite/docs/00-CONVENTIONS.md`。

## 目录

- `cmd/quotahub-go/`：入口（装配 + 优雅退出 + `migrate-data`）。
- `internal/store/`：账本层（SQL 与方言接口）、历史采样、账号/会话。
- `internal/sqlite/`、`internal/mysql/`：两套薄方言（DDL / upsert / 自增回填 / 列探测 / DSN）。
- `internal/{model,jsonstore,platform,presets,settings,logs,collect,auth,httpapi,scrypt,httperr}/`。
- `web/`：前端；`deploy/`：systemd / env / nginx 样例（**只提交，不安装到 /etc**）。

## 三条硬约束（改动前必读）

1. **HTTP 契约逐条对齐**：路径、方法、状态码、响应体形状不得改；未知 `/api/*` 未登录先 401、
   已登录才 404。改了路由必须同步 `docs/API.md`（`make check` 会机检）。
2. **采集语义对齐**：周期来自 `settings.collectIntervalSeconds`；抓取超时 15s；响应上限 1MB；
   失败只记运行时错误态（面板呈现），**不丢历史**；每平台历史保留最新 2000 点。
3. **数据库迁移只做增量**：禁止 DROP / TRUNCATE / 重建表；两种方言下都要能通过同一套测试。

## 红线

- 源码 / 配置 / 文档 / 提交信息**绝不**出现真实域名、公网 / 内网 IP、邮箱、密钥、真实口令、
  真实平台 key；示例统一 `example.com` / `127.0.0.1`。
- 不写调试残留；不留注释掉的死代码；新增 TODO/FIXME 必须为 0。
- 登录限速、会话 token、口令哈希、数据库口令一律不进日志。
- **不得修改 `/root/proj/quotahub`、不得重启 `quotahub.service`、不得写它的现网库**。
- 只监听 `127.0.0.1`；服务端口 `19250`。

## 命令

- `make build` / `make test` / `make race` / `make vet` / `make check`（路由文档机检 + vet + 单测）。
- 依赖安装与前端命令一律带 `timeout`。

## 提交

小步提交、中文信息、不带署名与 TASK 号；**禁止** `git push`、`checkout -b`、`rebase`、
`reset --hard`、`merge`；不要 `git add` 运行产物（二进制、`web/dist`、`web/node_modules`、数据目录）。
