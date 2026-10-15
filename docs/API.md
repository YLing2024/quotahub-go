# QuotaHub Go 后端 API

本文件是 quotahub-go 管理面 HTTP 契约的权威说明。契约与旧实现 `/root/proj/quotahub`
（Node/Express）逐条对齐：路径、方法、状态码、响应体形状均保持一致。

- 基址：`http://127.0.0.1:19250`（`HOST`/`PORT` 可覆盖）。
- 请求/响应均为 JSON（`Content-Type: application/json; charset=utf-8`）；请求体上限 100KB。
- 请求体是非法 JSON 时返回 `400 {"error":"<V8 JSON.parse 原文>"}`。文案按旧服务的
  `express.json()`（body-parser 2.3.0 strict + V8 `JSON.parse`）口径复刻，逐条对照见
  `docs/COMPAT-JSON-ERRORS.md`。
- 所有示例中的域名统一用 `example.com`、地址统一用 `127.0.0.1`，不含任何真实凭据。
- 机检：`node scripts/check-api-docs.mjs`（并入 `make check`）比对源码路由与本文件标题。

## 认证模式

`AUTH_MODE` 决定管理面鉴权，两种模式二选一：

| 模式 | 凭证来源 | 说明 |
| --- | --- | --- |
| `builtin`（默认） | `Authorization: Bearer <token>` 或 cookie `quotahub_session` | 自带账号 + 会话。首启 `users` 为空时用 `QUOTAHUB_ADMIN_USER`（默认 `admin`）与 `QUOTAHUB_ADMIN_PASSWORD` 建管理员；口令为空则随机生成并只打印一次。 |
| `sso` | 仅请求头 `X-Auth-User` | 身份完全交给前置认证层；忽略 cookie 与 Bearer，缺失或为空即未登录。 |

- `sso` 模式下自带认证端点 `POST /api/auth/login`、`POST /api/auth/logout`、
  `GET /api/auth/me` 一律返回 `404 {"error":"未找到"}`。
- 除 `GET /api/auth-mode` 与上述自带认证端点外，所有 `/api/*` 都先鉴权：未登录一律
  `401 {"error":"未登录"}`，鉴权通过后才判定资源是否存在（未知 `/api/*` → 404 HTML）。
- 会话有效期 `QUOTAHUB_SESSION_TTL_HOURS`（默认 12，上限 720）；命中即滑动续期。

## 路由与方法回退

分发顺序逐条对齐旧实现 Express app 的层序：`express.json()` 解析请求体 → 公开路由 →
内置认证端点 → `/api` 鉴权中间件 → 业务路由 → Express 默认 404。鉴权中间件排在业务路由
之前，因此本服务是**先鉴权、再做方法回退**。

- 公开路由只有 `GET /api/auth-mode`，且仅 `GET`/`HEAD` 免鉴权；`OPTIONS /api/auth-mode`
  会继续走到鉴权（未登录 `401`），已登录才落自动 OPTIONS。
- 内置认证端点 `/api/auth/{login,logout,me}` 的守卫与自动 OPTIONS 在鉴权之前：`builtin` 下
  `OPTIONS /api/auth/login` → `Allow: POST`、`OPTIONS /api/auth/me` → `Allow: GET, HEAD`；
  `sso` 下 `/api/auth/*` 任意方法、任意子路径（含未注册的 `/api/auth/nope`）一律
  `404 {"error":"未找到"}`。
- 其余 `/api/*` 先鉴权：**未登录时无论什么方法都先 `401 {"error":"未登录"}`**，鉴权通过后再
  做方法回退与 404 判定。

方法回退规则（与 Express 逐条一致）：

- **HEAD 走 GET 处理器**：状态码与响应头同 `GET`，响应体为空，但 `Content-Length` 仍等于
  GET 响应体长度（HTTP 对 HEAD 不返回体）。
- **自动 OPTIONS**：对该路径匹配的全部路由汇总允许的方法，去重后按字母序排序、以 `, `
  连接；响应 `200`、`Content-Type: text/plain`，`Allow` 头与响应体同为该串；存在 `GET` 的
  路由自动补 `HEAD`。同一路径上字面量段与 `:id` 段都会参与汇总，例如：
  - `OPTIONS /api/platforms` → `Allow: GET, HEAD, POST`
  - `OPTIONS /api/settings` → `Allow: GET, HEAD, PUT`
  - `OPTIONS /api/logs` → `Allow: DELETE, GET, HEAD`
  - `OPTIONS /api/import` → `Allow: POST`
  - `OPTIONS /api/platforms/reorder` → `Allow: DELETE, PUT`（`/reorder` 与 `/:id` 都匹配）
- **路径存在但方法不允许**（如 `PATCH /api/platforms`）**不是 `405`**，而是与未注册路径相同的
  Express 默认 404：`Content-Type: text/html; charset=utf-8`，体为
  `<!DOCTYPE html>\n<html lang="en">…<pre>Cannot PATCH /api/platforms</pre>…\n</html>\n`。
- **未注册的 `/api/*`**：已登录 → 同一个 Express HTML 404 页；未登录 → 先 `401` JSON。

## 数据模型约定

- **ID 类型**：`Platform.id`、`Preset.id` 均为字符串（UUID v4）；`LogEntry.id` 为字符串
  （`<毫秒36进制>-<6位36进制>`）。历史采样行为自增整数 `history_samples.id`，仅在库内使用。
  本项目不使用雪花 ID；如将来引入，ID 一律以字符串传输，禁止用数字承载。
- **时间**：所有时间戳为 UTC ISO 8601 字符串（如 `2026-01-02T03:04:05.678Z`），可直接比较。
  数据库列 `time`/`created_at`/`expires_at` 均为文本，字典序即时序。
- **纯数值语义**：余额为有限数值；平台展示文本由前端执行 `platform.format` 源码渲染。
- **松散对象**：`Platform.request` / `Platform.response` 原样透传用户配置（键集合不固定）。

### Platform（对外形态，不含已弃用的 `display`）

| 字段 | 类型 | 说明 |
| --- | --- | --- |
| `id` | string | UUID |
| `name` | string | 名称，默认 `未命名平台` |
| `request` | object | `{method,url,headers,body?}`，默认 `{method:"GET",url:"",headers:{}}` |
| `handler` | string | 单函数处理源码（`raw => 数值`） |
| `extractor` | string | 提取函数源码（`data => 数值`） |
| `parse` | string | 解析函数源码（`raw => data`） |
| `format` | string | 展示函数源码，前端渲染时执行；空串表示无 |
| `response` | object? | 旧版 `{path,divider}` 模型；`prefix`/`suffix` 导出时剔除 |
| `url` | string | 展示用链接 |
| `createdAt` | string | 创建时间 |

对外对象的**键集合忠实源记录**：源记录没有的键不下发（例如部分平台没有 `extractor` /
`parse`）；键存在但值为空串时原样下发 `""`；未知键（如 `preset`）原样透传。`null`、缺失、
`""` 三者互不等价，不互相顶替。已弃用的 `display` 一律剔除。新增/更新平台时请求里出现哪些键，
就按此规则落到对外对象上。

### Preset（对外形态，含 `builtin` 标记）

`id/name/fields/method/urlTemplate/headersTemplate/extractorTemplate`，内置预设额外带
`builtin:true`，用户覆盖内置时带 `edited:true`。内置预设集合固定为 `newapi`、`deepseek`。

### BalanceCard / Dashboard

`GET /api/platforms/balances` 返回 `{updatedAt,platforms,collecting,lastRunAt,lastRunReason}`；
每张卡片为 `{id,name,url,value,format,error,fetchedAt}`。`value` 取自历史采样最新点；
仅当最近一次失败晚于最新采样点时，`error`/`fetchedAt` 才表示失败态。

---

### GET /api/auth-mode

返回当前认证模式。免鉴权。

- 参数：无。
- 成功 `200`：`{"authMode":"builtin"}`
- curl：`curl -s http://127.0.0.1:19250/api/auth-mode`

### POST /api/auth/login

账号口令登录（仅 `builtin`）。成功下发会话 cookie。

- 参数（JSON body）：`username` string 必填；`password` string 必填。缺失或非字符串按 `401` 处理。
- 失败 `401`：`{"error":"账号或密码错误"}`
- 限速 `429`：连续失败 5 次锁 15 分钟，返回 `{"error":"尝试过于频繁","retryAfter":<秒>}`
- 成功 `200`：`{"ok":true,"user":{"name":"admin"}}`，并 `Set-Cookie: quotahub_session=…`（HttpOnly、SameSite=Lax、HTTPS 下加 Secure）。
- 副作用：写 cookies（会话表 `sessions`）、记/清登录失败计数。
- curl：
  ```bash
  curl -s -i -X POST http://127.0.0.1:19250/api/auth/login \
    -H 'Content-Type: application/json' \
    -d '{"username":"admin","password":"<口令>"}'
  ```

### POST /api/auth/logout

退出（仅 `builtin`）。幂等：未登录也 `200`。

- 参数：无。
- 成功 `200`：`{"ok":true}`，并清除 cookie。
- 副作用：删除当前会话行（若存在）。
- curl：`curl -s -X POST http://127.0.0.1:19250/api/auth/logout`

### GET /api/auth/me

返回当前登录用户名（仅 `builtin`）。

- 成功 `200`：`{"name":"admin"}`
- 未登录 `401`：`{"error":"未登录"}`
- curl：`curl -s --cookie 'quotahub_session=<token>' http://127.0.0.1:19250/api/auth/me`

### GET /api/platforms

列出全部平台（对外形态）。

- 成功 `200`：`[ {Platform}, … ]`（无平台为 `[]`）
- curl：`curl -s http://127.0.0.1:19250/api/platforms -H 'X-Auth-User: alice'`

### POST /api/platforms

新建平台。

- 参数（JSON body，必须为对象）：`name`、`request`、`handler`、`extractor`、`parse`、
  `format`、`url`、`display`（忽略，仅导入兼容）。字段级规范化见 Platform；`handler`/
  `extractor`/`parse` 至少一个非空，否则 `400`。
- 成功 `201`：`{Platform}`
- 失败 `400`：请求体非法 JSON 时为 `{"error":"<V8 JSON.parse 原文>"}`（见 `docs/COMPAT-JSON-ERRORS.md`）；
  否则为 `{"error":"请求体必须是 JSON 对象"}` / `{"error":"需要配置处理函数（或旧的提取/解析函数）"}`
- 副作用：写 `platforms.json`，记 `create` 日志。
- curl：
  ```bash
  curl -s -X POST http://127.0.0.1:19250/api/platforms -H 'X-Auth-User: alice' \
    -H 'Content-Type: application/json' \
    -d '{"name":"示例平台","request":{"method":"GET","url":"https://api.example.com/balance","headers":{}},"handler":"function(raw){return 1}"}'
  ```

### PUT /api/platforms/reorder

按 `ids` 重排平台（同时决定面板卡片顺序）。

- 参数（JSON body）：`ids` array&lt;string&gt; 必填，必须是现有平台 id 的完整排列。
- 成功 `200`：`{"ok":true}`
- 失败 `400`：`{"error":"需要 ids 数组"}` / `{"error":"ids 数量与现有平台不匹配"}` / `{"error":"未知平台 id: xxx"}` / `{"error":"ids 包含重复项"}`
- 副作用：写 `platforms.json`，记 `reorder` 日志。
- curl：
  ```bash
  curl -s -X PUT http://127.0.0.1:19250/api/platforms/reorder -H 'X-Auth-User: alice' \
    -H 'Content-Type: application/json' -d '{"ids":["<id1>","<id2>"]}'
  ```

### GET /api/platforms/balances

面板数据（最新值来自历史采样最新点，失败态仅在失败晚于最新采样时出现）。

- 成功 `200`：`{"updatedAt":…,"platforms":[BalanceCard…],"collecting":false,"lastRunAt":null,"lastRunReason":null}`
- 副作用：无（只读）。
- curl：`curl -s http://127.0.0.1:19250/api/platforms/balances -H 'X-Auth-User: alice'`

### POST /api/platforms/refresh

顺序抓取全部平台（手动刷新），记录采样点并记日志。

- 成功 `200`：`{"ok":true,"results":[{"id":"<id>","ok":true,"value":1},…]}`
  失败项为 `{"id":"<id>","ok":false,"error":"…"}`。
- 副作用：写 `history_samples`（每平台保留最新 2000 点）、更新运行时错误态、记 `refresh` 日志。
- curl：`curl -s -X POST http://127.0.0.1:19250/api/platforms/refresh -H 'X-Auth-User: alice'`

### POST /api/platforms/validate

校验任意抓取配置（不保存、不写历史、不记错误态）。

- 参数（松散，非对象按空处理）：`request`、`handler`、`extractor`、`parse`、`response`。
- 成功 `200`：`{"ok":true,"value":<number>}`
- 失败 `502`：`{"ok":false,"error":"…"}`
- curl：
  ```bash
  curl -s -X POST http://127.0.0.1:19250/api/platforms/validate -H 'X-Auth-User: alice' \
    -H 'Content-Type: application/json' \
    -d '{"request":{"url":"https://api.example.com/balance","headers":{}},"handler":"function(raw){return 1}"}'
  ```

### PUT /api/platforms/:id

部分更新平台（只覆盖请求里出现的字段；保存 `handler` 会清掉旧的 `parse`/`extractor`）。

- 路径参数：`id` string。
- 成功 `200`：`{Platform}`
- 失败 `400`：`{"error":"请求体必须是 JSON 对象"}`；`404`：`{"error":"平台不存在"}`
- 副作用：写 `platforms.json`，记 `update` 日志。
- curl：
  ```bash
  curl -s -X PUT http://127.0.0.1:19250/api/platforms/<id> -H 'X-Auth-User: alice' \
    -H 'Content-Type: application/json' -d '{"name":"改名"}'
  ```

### DELETE /api/platforms/:id

删除平台并清空其历史采样。

- 成功 `204`：无响应体。
- 失败 `404`：`{"error":"平台不存在"}`
- 副作用：写 `platforms.json`、删 `history_samples` 对应平台行、记 `delete` 日志。
- curl：`curl -s -o /dev/null -w '%{http_code}\n' -X DELETE http://127.0.0.1:19250/api/platforms/<id> -H 'X-Auth-User: alice'`

### POST /api/platforms/:id/test

用平台当前配置做一次抓取，不写历史。

- 成功 `200`：`{"ok":true,"value":<number>,"testedAt":"<ISO>"}`
- 失败 `502`：`{"ok":false,"error":"…"}`；平台不存在 `404`：`{"error":"平台不存在"}`
- 副作用：无持久化。
- curl：`curl -s -X POST http://127.0.0.1:19250/api/platforms/<id>/test -H 'X-Auth-User: alice'`

### POST /api/platforms/:id/fetch

抓取指定平台并记录采样点。

- 成功 `200`：`{"ok":true,"value":<number>,"fetchedAt":"<ISO>"}`
- 失败 `502`：`{"ok":false,"error":"…"}`；平台不存在 `404`：`{"error":"平台不存在"}`
- 副作用：写 `history_samples`、清/置运行时错误态、记 `fetch` 日志。
- curl：`curl -s -X POST http://127.0.0.1:19250/api/platforms/<id>/fetch -H 'X-Auth-User: alice'`

### GET /api/platforms/:id/history

折线图采样点（时间升序）。

- 成功 `200`：`{"id":"<id>","name":"…","points":[{"v":1,"t":"<ISO>"},…]}`
- 失败 `404`：`{"error":"平台不存在"}`
- curl：`curl -s http://127.0.0.1:19250/api/platforms/<id>/history -H 'X-Auth-User: alice'`

### GET /api/presets

列出预设：内置在前（含 `builtin`/`edited` 标记）+ 用户自定义。

- 成功 `200`：`[ {Preset…} ]`
- curl：`curl -s http://127.0.0.1:19250/api/presets -H 'X-Auth-User: alice'`

### POST /api/presets

新建用户预设。

- 参数（JSON body）：`name`、`fields` array（每项 `key`/`label`/`placeholder?`）、`method`（`GET`/`POST`）、
  `urlTemplate`、`headersTemplate`（对象或 JSON 字符串）、`extractorTemplate`。
- 成功 `201`：`{Preset,builtin:false}`
- 失败 `400`：`{"error":"请求体必须是 JSON 对象"}` / 各字段逐条中文校验信息（如 `预设名称不能为空`、
  `至少需要一个字段`、`字段 key 不能为空`、`字段 key 只能包含字母、数字、下划线、连字符`、
  `字段 label 不能为空`、`URL 模板不能为空`、`请求头模板必须是合法 JSON`、`请求头模板必须是 JSON 对象`、
  `提取函数模板不能为空`）。
- 副作用：写 `presets.json`。
- curl：
  ```bash
  curl -s -X POST http://127.0.0.1:19250/api/presets -H 'X-Auth-User: alice' \
    -H 'Content-Type: application/json' \
    -d '{"name":"自定义","fields":[{"key":"apiKey","label":"API Key"}],"urlTemplate":"https://api.example.com/x","headersTemplate":{},"extractorTemplate":"function(d){return 1}"}'
  ```

### PUT /api/presets/:id

upsert 预设（不存在则创建；内置 id 也可覆盖保存）。

- 成功 `200`：`{Preset,builtin:<bool>}`
- 失败 `400`：同 `POST /api/presets` 校验信息。
- 副作用：写 `presets.json`。
- curl：
  ```bash
  curl -s -X PUT http://127.0.0.1:19250/api/presets/newapi -H 'X-Auth-User: alice' \
    -H 'Content-Type: application/json' -d '{"name":"NEWAPI 覆盖","fields":[{"key":"baseUrl","label":"地址"}],"urlTemplate":"https://example.com/x","headersTemplate":{},"extractorTemplate":"function(d){return 1}"}'
  ```

### DELETE /api/presets/:id

删除用户预设；内置预设禁止删除（用重置恢复默认）。

- 成功 `204`：无响应体。
- 失败 `400`：`{"error":"内置预设使用重置恢复默认，不能删除"}`
- 副作用：写 `presets.json`。
- curl：`curl -s -o /dev/null -w '%{http_code}\n' -X DELETE http://127.0.0.1:19250/api/presets/<id> -H 'X-Auth-User: alice'`

### POST /api/presets/:id/reset

重置内置预设（删除用户覆盖，返回默认定义）。

- 成功 `200`：`{内置 Preset}`
- 失败 `400`：`{"error":"仅内置预设支持重置"}`
- 副作用：写 `presets.json`。
- curl：`curl -s -X POST http://127.0.0.1:19250/api/presets/newapi/reset -H 'X-Auth-User: alice'`

### GET /api/settings

读取运行时设置。

- 成功 `200`：`{"collectIntervalSeconds":0}`
- curl：`curl -s http://127.0.0.1:19250/api/settings -H 'X-Auth-User: alice'`

### PUT /api/settings

更新设置并重新排程定时采集。

- 参数（JSON body）：`collectIntervalSeconds` number 可选，`>=0` 取 floor 并钳制到 `86400`；非法/缺失回默认 `0`（关闭）。
- 成功 `200`：`{"collectIntervalSeconds":<n>}`
- 失败 `400`：`{"error":"请求体必须是 JSON 对象"}`
- 副作用：写 `settings.json`；变更时记 `settings` 日志；按新间隔重排采集调度。
- curl：
  ```bash
  curl -s -X PUT http://127.0.0.1:19250/api/settings -H 'X-Auth-User: alice' \
    -H 'Content-Type: application/json' -d '{"collectIntervalSeconds":600}'
  ```

### GET /api/logs

操作日志（最新在前）。

- 查询参数：`limit` number 可选，默认 200，钳制 `[1,2000]`。
- 成功 `200`：`{"logs":[LogEntry…],"total":<n>}`
- curl：`curl -s 'http://127.0.0.1:19250/api/logs?limit=50' -H 'X-Auth-User: alice'`

### DELETE /api/logs

清空操作日志，再记一条清空动作。

- 成功 `204`：无响应体。
- 副作用：写 `logs.json`。
- curl：`curl -s -o /dev/null -w '%{http_code}\n' -X DELETE http://127.0.0.1:19250/api/logs -H 'X-Auth-User: alice'`

### GET /api/export

导出完整配置（全部平台 + 用户预设，剔除 `display` 与 `response.prefix/suffix`）。

- 成功 `200`：`{"type":"quotahub-config","version":1,"exportedAt":"<ISO>","platforms":[…],"presets":[…]}`
- 副作用：记 `export` 日志。
- curl：`curl -s http://127.0.0.1:19250/api/export -H 'X-Auth-User: alice'`

### POST /api/import

导入配置，支持完整配置、单平台、单预设三种格式；按 `id` upsert，单项失败不影响其他项。

- 参数（JSON body，必须为对象）：完整配置 `{platforms?:[…],presets?:[…]}`，或单个平台/预设对象。
- 成功 `200`：`{"ok":true,"platforms":<n>,"presets":<n>,"errors":[…]}`（单项失败只进 `errors`）。
- 失败 `400`：`{"error":"无法识别的配置格式: 需要完整配置(含 platforms/presets 数组)、单个平台或单个预设的 JSON"}`
- 副作用：写 `platforms.json`/`presets.json`，记 `import` 日志。
- curl：
  ```bash
  curl -s -X POST http://127.0.0.1:19250/api/import -H 'X-Auth-User: alice' \
    -H 'Content-Type: application/json' -d '{"platforms":[],"presets":[]}'
  ```

---

## 错误码表

| 状态 | 场景 |
| --- | --- |
| `200` | 成功（含自动 `OPTIONS` 方法汇总响应） |
| `201` | 新建平台 / 预设成功 |
| `204` | 删除平台 / 预设 / 清空日志成功（无响应体） |
| `400` | 请求体非法 JSON（V8 文案）、请求体/字段校验失败、业务规则拒绝（如内置预设不可删） |
| `401` | 未登录（任何方法都先判）；`builtin` 登录账号或口令错误 |
| `404` | 资源不存在；`sso` 下本地认证端点不存在；已登录访问未知 `/api/*` 或「路径存在但方法不允许」（后两者为 Express HTML 404，非 405） |
| `413` | 请求体超过 100KB |
| `429` | 登录失败过于频繁（带 `retryAfter`） |
| `502` | 抓取失败（validate/test/fetch 的 `{ok:false,error}`） |
| `500` | 服务器内部错误 |

非业务异常统一落 `{"error":<message>}`；抓取类失败落 `{"ok":false,"error":<message>}`。

## 未实现的旧功能说明

需求书提到「blocked」能力，但旧实现（作为功能权威）中不存在任何 blocked 代码、字段或标签，
故本次重写为功能对等，不新增该能力。
