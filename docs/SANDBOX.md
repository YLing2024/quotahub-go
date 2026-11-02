# 采集脚本沙箱

QuotaHub 的平台 handler / parse / extract 脚本都是 JS，用户可编辑。这类脚本必须当作**不可信代码**处理：一个 `while(true){}`、一个内存炸弹、或者一段想外联内网的代码，都不该影响看板本身。

## 执行模型

```
quotahub-go (父进程, root, 有网)
   │  1. 自己拉 HTTP（带 cookie/token，脚本碰不到）
   │  2. fork 一次性子进程：quotahub-go --sandbox-runner
   ├───────────── stdin: {kind, src, raw, headers, data, timeout_ms}
   │              stdout: {ok, value} | {error} | {timeout}
   │  3. 子进程执行 goja，回执后立即退出
   └  4. 超时 = SIGKILL 整个进程组
```

子进程的隔离与限制（全部在 `internal/collect/sandbox_proc.go`）：

| 项 | 值 | 作用 |
|---|---|---|
| 网络命名空间 | `CLONE_NEWNET`（独立 netns） | 脚本侧完全无网络：连不上内网、连不上 `169.254.169.254`、连 DNS 都没有 |
| 凭据 | 降权到 `nobody`(65534)，`no_new_privs`，不可 dump | 引擎被攻破也只是个马上被杀的普通进程 |
| 环境变量 | 只给 `PATH/HOME/TMPDIR`，不继承父进程 | 数据库口令、会话密钥不会出现在子进程里 |
| 工作目录 | `/` | 不落在数据目录里，读不到 `platforms.json` |
| `RLIMIT_CPU` | 5s | 兜底，正常先被父进程 kill |
| `RLIMIT_AS` | 4GiB | 只做虚拟地址兜底（Go 运行时启动就要预留 >1GiB，设小了会起不来） |
| Go 软内存上限 | 256MB | 真正的内存闸门，内存炸弹直接 OOM 退出 |
| `RLIMIT_NOFILE` | 64 | |
| 超时 | 父进程 `SIGKILL` 进程组 | 死循环只能烧掉自己的 CPU 配额 |

脚本可见的宿主能力：**只有 `raw` 与 `meta.headers`**。没有 `require` / `process` / `fetch` / `XMLHttpRequest` / 文件系统。

## 为什么不用进程内沙箱

实测（2026-11）：

- **goja**：`runtime.Interrupt` 能中断死循环，但引擎与宿主同进程、共享内存，脚本与宿主之间只有"约定"没有边界；注入的宿主函数一旦有类型混淆就是任意代码执行。
- **QuickJS → WASM（wazero，qjs v0.0.6）**：隔离本身很好（独立线性内存，实测 `require/process/fetch` 全 `undefined`），但**无法中断死循环**——`MaxExecutionTime` 与 wazero 的 `CloseOnContextDone` 对 `while(true){}` 都不生效，一个恶意脚本会把一个核吃到进程重启。要防"恶意 JS"，必须有一个能被 kill 的边界。

因此选择进程级隔离：隔离性由内核提供，不依赖引擎实现质量。

## 配置

| 环境变量 | 默认 | 说明 |
|---|---|---|
| `QUOTAHUB_SANDBOX_MODE` | `process` | `inline` 仅供测试/排障，会用同进程 goja，**不要在生产开** |
| `QUOTAHUB_SANDBOX_UID` | `65534` | 子进程降权 uid/gid |
| `QUOTAHUB_SCRIPT_TIMEOUT_MS` | `2000` | 单个脚本的超时预算 |

前提：二进制必须**沿途每一级目录**都对 other 可执行（`/root` 是 700，所以服务要从 `/data/proj/...` 启动），否则降权后 `exec` 会 EACCES，此时会自动跳过降权（netns 仍然生效）。

## 自查

```bash
# 子进程是否在独立 netns、是否降权（跑一次采集时观察）
ps -eo pid,user,args | grep '[s]andbox-runner'
readlink /proc/<pid>/ns/net          # 应等于当前 shell 的独立 netns，而非宿主
grep -E 'Uid|NoNewPrivs' /proc/<pid>/status

# 恶意脚本行为
curl -s -X POST http://127.0.0.1:5300/api/platforms/refresh -H 'X-Auth-User: admin'
# 单元测试里有：死循环、内存炸弹、宿主能力探测、meta.headers
go test ./internal/collect/ -run TestSandboxProcessIsolation -v
```
