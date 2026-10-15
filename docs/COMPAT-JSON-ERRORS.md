# 请求体 JSON 报错文案兼容说明

本文件记录 quotahub-go 与旧实现 `/root/proj/quotahub` 在**请求体 JSON 语法错误报错文案**上的
对齐口径、采集方法与逐条对照。它属于「HTTP 契约逐条对齐」的一部分：路径、状态码、响应体形状
与这里的报错文案都必须与旧服务一致。

## 旧实现的行为（两层）

旧服务用 Express 5 + body-parser 2.3.0 的 `express.json()`（`server/dist/app.js` 里
`app.use(express.json())`）。它对请求体先做两层处理：

1. **strict 首字符校验**（`strict` 默认 true）
   - 体长度为 0：特判成空对象 `{}`，不报错。
   - 取首个非空白字符（空白只认 RFC 7159 的四个：空格、制表、换行、回车）。若不是 `{` 或
     `[`，直接抛语法错。
2. **strict 语法错的文案构造**（`body-parser/lib/types/json.js` 的 `createStrictSyntaxError`）
   - 把从该字符起（含该字符）到正文结尾的原文整段换成 `#`，得到 `partial`；
   - 调 `JSON.parse(partial)` 取 V8 原生报错；
   - 再把报错里每一段连续的 `#`，按「长度相同、从该字符位置起」的规则换回原文（`Unexpected
     token '#'` 里 token 位置的那一个 `#` 也一并换回）。
   - 结果形如 `Unexpected token 't', "tru" is not valid JSON`。
3. 若首字符是 `{` / `[`，则直接 `JSON.parse(body)`，报错即 V8 原生文案（含 `position` 与
   `line`/`column`，超长输入带 `...` 上下文截取）。

因此「裸 `JSON.parse`」的文案**不等于**旧服务的文案。例如 `tru`：裸 `JSON.parse('tru')`
给 `Unexpected end of JSON input`，而旧服务给 `Unexpected token 't', "tru" is not valid JSON`。

## 本项目实现

- `internal/v8json/v8json.go`：复刻 V8 `json-parser.cc` 的原生文案，导出 `ErrorMessage`。
- `internal/httpapi/jsonbody.go`：`strictBodyParserError` 复刻上面第 1、2 步（掩码 + 回填）。
- `internal/httpapi/server.go` 的 `decodeJSONBody`：空体 → `{}`；strict 命中 → `400`；否则
  用 `encoding/json` 解码，失败时用 `v8json.ErrorMessage` 产出 V8 文案。
- 单测：
  - `internal/v8json/v8json_test.go`：裸 V8 `JSON.parse` 文案表（`ErrorMessage` 的单元口径）。
  - `internal/httpapi/jsonbody_test.go`：**HTTP 可观测**文案表，与旧服务实测逐条对拍，是权威目标。

## 采集方法

1. 起旧服务：cwd **不在**旧仓库、`QUOTAHUB_DATA_DIR` 指向平台的只读副本、`HOST=127.0.0.1`、
   高位 `PORT`、`AUTH_MODE=sso`。
2. 对每个畸形体 `POST /api/platforms`（带 `X-Auth-User`），读 `400` 响应体里的 `error`。
3. 环境：Node v24.19.0（`NODE_MODULE_VERSION 137`，与旧仓库预编译的 `better-sqlite3` 匹配）。
4. 新 Go 服务同机起在另一高位端口，同样的数据副本，两端逐字节比较。

## 对照表（旧服务实测 = 目标；新 Go 逐字节一致）

| 输入 | 旧 Node 状态 | 新 Go 状态 | 旧 Node error 原文 | 新 Go error 原文 | 一致 |
| --- | --- | --- | --- | --- | --- |
| `"{not json"` | 400 | 400 | `Expected property name or '}' in JSON at position 1 (line 1 column 2)` | `Expected property name or '}' in JSON at position 1 (line 1 column 2)` | 是 |
| `"{\"a\":}"` | 400 | 400 | `Unexpected token '}', "{"a":}" is not valid JSON` | `Unexpected token '}', "{"a":}" is not valid JSON` | 是 |
| `"{\"a\" 1}"` | 400 | 400 | `Expected ':' after property name in JSON at position 5 (line 1 column 6)` | `Expected ':' after property name in JSON at position 5 (line 1 column 6)` | 是 |
| `"{\"a\":1,}"` | 400 | 400 | `Expected double-quoted property name in JSON at position 7 (line 1 column 8)` | `Expected double-quoted property name in JSON at position 7 (line 1 column 8)` | 是 |
| `"{\"a\":\"x}"` | 400 | 400 | `Unterminated string in JSON at position 8 (line 1 column 9)` | `Unterminated string in JSON at position 8 (line 1 column 9)` | 是 |
| `"{\"a\":01}"` | 400 | 400 | `Unexpected number in JSON at position 6 (line 1 column 7)` | `Unexpected number in JSON at position 6 (line 1 column 7)` | 是 |
| `"[1,]"` | 400 | 400 | `Unexpected token ']', "[1,]" is not valid JSON` | `Unexpected token ']', "[1,]" is not valid JSON` | 是 |
| `"{"` | 400 | 400 | `Expected property name or '}' in JSON at position 1 (line 1 column 2)` | `Expected property name or '}' in JSON at position 1 (line 1 column 2)` | 是 |
| `"tru"` | 400 | 400 | `Unexpected token 't', "tru" is not valid JSON` | `Unexpected token 't', "tru" is not valid JSON` | 是 |
| `"{\"a\":1} trailing"` | 400 | 400 | `Unexpected non-whitespace character after JSON at position 8 (line 1 column 9)` | `Unexpected non-whitespace character after JSON at position 8 (line 1 column 9)` | 是 |
| `"}"` | 400 | 400 | `Unexpected token '}', "}" is not valid JSON` | `Unexpected token '}', "}" is not valid JSON` | 是 |
| `"]"` | 400 | 400 | `Unexpected token ']', "]" is not valid JSON` | `Unexpected token ']', "]" is not valid JSON` | 是 |
| `","` | 400 | 400 | `Unexpected token ',', "," is not valid JSON` | `Unexpected token ',', "," is not valid JSON` | 是 |
| `":"` | 400 | 400 | `Unexpected token ':', ":" is not valid JSON` | `Unexpected token ':', ":" is not valid JSON` | 是 |
| `"{,}"` | 400 | 400 | `Expected property name or '}' in JSON at position 1 (line 1 column 2)` | `Expected property name or '}' in JSON at position 1 (line 1 column 2)` | 是 |
| `"{]"` | 400 | 400 | `Expected property name or '}' in JSON at position 1 (line 1 column 2)` | `Expected property name or '}' in JSON at position 1 (line 1 column 2)` | 是 |
| `"[}"` | 400 | 400 | `Unexpected token '}', "[}" is not valid JSON` | `Unexpected token '}', "[}" is not valid JSON` | 是 |
| `"[,]"` | 400 | 400 | `Unexpected token ',', "[,]" is not valid JSON` | `Unexpected token ',', "[,]" is not valid JSON` | 是 |
| `"[1,,2]"` | 400 | 400 | `Unexpected token ',', "[1,,2]" is not valid JSON` | `Unexpected token ',', "[1,,2]" is not valid JSON` | 是 |
| `"{\"a\""` | 400 | 400 | `Expected ':' after property name in JSON at position 4 (line 1 column 5)` | `Expected ':' after property name in JSON at position 4 (line 1 column 5)` | 是 |
| `"{\"a\":"` | 400 | 400 | `Unexpected end of JSON input` | `Unexpected end of JSON input` | 是 |
| `"{\"a\"}"` | 400 | 400 | `Expected ':' after property name in JSON at position 4 (line 1 column 5)` | `Expected ':' after property name in JSON at position 4 (line 1 column 5)` | 是 |
| `"{\"a\":1 \"b\":2}"` | 400 | 400 | `Expected ',' or '}' after property value in JSON at position 7 (line 1 column 8)` | `Expected ',' or '}' after property value in JSON at position 7 (line 1 column 8)` | 是 |
| `"{\"a\":1"` | 400 | 400 | `Expected ',' or '}' after property value in JSON at position 6 (line 1 column 7)` | `Expected ',' or '}' after property value in JSON at position 6 (line 1 column 7)` | 是 |
| `"{} x"` | 400 | 400 | `Unexpected non-whitespace character after JSON at position 3 (line 1 column 4)` | `Unexpected non-whitespace character after JSON at position 3 (line 1 column 4)` | 是 |
| `"[] x"` | 400 | 400 | `Unexpected non-whitespace character after JSON at position 3 (line 1 column 4)` | `Unexpected non-whitespace character after JSON at position 3 (line 1 column 4)` | 是 |
| `"true x"` | 400 | 400 | `Unexpected token 't', "true x" is not valid JSON` | `Unexpected token 't', "true x" is not valid JSON` | 是 |
| `"{\"a\":1}\n{\"b\":2}"` | 400 | 400 | `Unexpected non-whitespace character after JSON at position 8 (line 2 column 1)` | `Unexpected non-whitespace character after JSON at position 8 (line 2 column 1)` | 是 |
| `"[1 2]"` | 400 | 400 | `Expected ',' or ']' after array element in JSON at position 3 (line 1 column 4)` | `Expected ',' or ']' after array element in JSON at position 3 (line 1 column 4)` | 是 |
| `"[01]"` | 400 | 400 | `Unexpected number in JSON at position 2 (line 1 column 3)` | `Unexpected number in JSON at position 2 (line 1 column 3)` | 是 |
| `"01"` | 400 | 400 | `Unexpected token '0', "01" is not valid JSON` | `Unexpected token '0', "01" is not valid JSON` | 是 |
| `"-"` | 400 | 400 | `Unexpected token '-', "-" is not valid JSON` | `Unexpected token '-', "-" is not valid JSON` | 是 |
| `"00"` | 400 | 400 | `Unexpected token '0', "00" is not valid JSON` | `Unexpected token '0', "00" is not valid JSON` | 是 |
| `"1."` | 400 | 400 | `Unexpected token '1', "1." is not valid JSON` | `Unexpected token '1', "1." is not valid JSON` | 是 |
| `"1e"` | 400 | 400 | `Unexpected token '1', "1e" is not valid JSON` | `Unexpected token '1', "1e" is not valid JSON` | 是 |
| `"1e+"` | 400 | 400 | `Unexpected token '1', "1e+" is not valid JSON` | `Unexpected token '1', "1e+" is not valid JSON` | 是 |
| `"{\"a\":1.}"` | 400 | 400 | `Unterminated fractional number in JSON at position 7 (line 1 column 8)` | `Unterminated fractional number in JSON at position 7 (line 1 column 8)` | 是 |
| `"{\"a\":-}"` | 400 | 400 | `No number after minus sign in JSON at position 6 (line 1 column 7)` | `No number after minus sign in JSON at position 6 (line 1 column 7)` | 是 |
| `"\"abc"` | 400 | 400 | `Unexpected token '"', ""abc" is not valid JSON` | `Unexpected token '"', ""abc" is not valid JSON` | 是 |
| `"\"a\\"` | 400 | 400 | `Unexpected token '"', ""a\" is not valid JSON` | `Unexpected token '"', ""a\" is not valid JSON` | 是 |
| `"\"a\\x\""` | 400 | 400 | `Unexpected token '"', ""a\x"" is not valid JSON` | `Unexpected token '"', ""a\x"" is not valid JSON` | 是 |
| `"{\"a\":\"\\q\"}"` | 400 | 400 | `Bad escaped character in JSON at position 7 (line 1 column 8)` | `Bad escaped character in JSON at position 7 (line 1 column 8)` | 是 |
| `"{\"a\":\"\\uZZZZ\"}"` | 400 | 400 | `Bad Unicode escape in JSON at position 8 (line 1 column 9)` | `Bad Unicode escape in JSON at position 8 (line 1 column 9)` | 是 |
| `"{\"a\":\"\\u12\"}"` | 400 | 400 | `Bad Unicode escape in JSON at position 10 (line 1 column 11)` | `Bad Unicode escape in JSON at position 10 (line 1 column 11)` | 是 |
| `"{\"a\":\"\\u0\"}"` | 400 | 400 | `Bad Unicode escape in JSON at position 9 (line 1 column 10)` | `Bad Unicode escape in JSON at position 9 (line 1 column 10)` | 是 |
| `"undefined"` | 400 | 400 | `Unexpected token 'u', "undefined" is not valid JSON` | `Unexpected token 'u', "undefined" is not valid JSON` | 是 |
| `"NaN"` | 400 | 400 | `Unexpected token 'N', "NaN" is not valid JSON` | `Unexpected token 'N', "NaN" is not valid JSON` | 是 |
| `"Infinity"` | 400 | 400 | `Unexpected token 'I', "Infinity" is not valid JSON` | `Unexpected token 'I', "Infinity" is not valid JSON` | 是 |
| `"nul"` | 400 | 400 | `Unexpected token 'n', "nul" is not valid JSON` | `Unexpected token 'n', "nul" is not valid JSON` | 是 |
| `"true"` | 400 | 400 | `Unexpected token 't', "true" is not valid JSON` | `Unexpected token 't', "true" is not valid JSON` | 是 |
| `"false"` | 400 | 400 | `Unexpected token 'f', "false" is not valid JSON` | `Unexpected token 'f', "false" is not valid JSON` | 是 |
| `"null"` | 400 | 400 | `Unexpected token 'n', "null" is not valid JSON` | `Unexpected token 'n', "null" is not valid JSON` | 是 |
| `"tru}"` | 400 | 400 | `Unexpected token 't', "tru}" is not valid JSON` | `Unexpected token 't', "tru}" is not valid JSON` | 是 |
| `"[tru]"` | 400 | 400 | `Unexpected token ']', "[tru]" is not valid JSON` | `Unexpected token ']', "[tru]" is not valid JSON` | 是 |
| `"{\"a\":tru}"` | 400 | 400 | `Unexpected token '}', "{"a":tru}" is not valid JSON` | `Unexpected token '}', "{"a":tru}" is not valid JSON` | 是 |
| `"{\"a\":undefined}"` | 400 | 400 | `Unexpected token 'u', "{"a":undefined}" is not valid JSON` | `Unexpected token 'u', "{"a":undefined}" is not valid JSON` | 是 |
| `"TRUE"` | 400 | 400 | `Unexpected token 'T', "TRUE" is not valid JSON` | `Unexpected token 'T', "TRUE" is not valid JSON` | 是 |
| `"1x"` | 400 | 400 | `Unexpected token '1', "1x" is not valid JSON` | `Unexpected token '1', "1x" is not valid JSON` | 是 |
| `"0x1"` | 400 | 400 | `Unexpected token '0', "0x1" is not valid JSON` | `Unexpected token '0', "0x1" is not valid JSON` | 是 |
| `"{\"a\":1}extra"` | 400 | 400 | `Unexpected non-whitespace character after JSON at position 7 (line 1 column 8)` | `Unexpected non-whitespace character after JSON at position 7 (line 1 column 8)` | 是 |
| `"{\"a\":1} {\"b\":2}"` | 400 | 400 | `Unexpected non-whitespace character after JSON at position 8 (line 1 column 9)` | `Unexpected non-whitespace character after JSON at position 8 (line 1 column 9)` | 是 |
| `"0.1.2"` | 400 | 400 | `Unexpected token '0', "0.1.2" is not valid JSON` | `Unexpected token '0', "0.1.2" is not valid JSON` | 是 |
| `"1e1e1"` | 400 | 400 | `Unexpected token '1', "1e1e1" is not valid JSON` | `Unexpected token '1', "1e1e1" is not valid JSON` | 是 |
| `"--1"` | 400 | 400 | `Unexpected token '-', "--1" is not valid JSON` | `Unexpected token '-', "--1" is not valid JSON` | 是 |
| `"+1"` | 400 | 400 | `Unexpected token '+', "+1" is not valid JSON` | `Unexpected token '+', "+1" is not valid JSON` | 是 |
| `".5"` | 400 | 400 | `Unexpected token '.', ".5" is not valid JSON` | `Unexpected token '.', ".5" is not valid JSON` | 是 |
| `"\"\\u\""` | 400 | 400 | `Unexpected token '"', ""\u"" is not valid JSON` | `Unexpected token '"', ""\u"" is not valid JSON` | 是 |
| `"null x"` | 400 | 400 | `Unexpected token 'n', "null x" is not valid JSON` | `Unexpected token 'n', "null x" is not valid JSON` | 是 |
| `"false x"` | 400 | 400 | `Unexpected token 'f', "false x" is not valid JSON` | `Unexpected token 'f', "false x" is not valid JSON` | 是 |
| `"tru "` | 400 | 400 | `Unexpected token 't', "tru " is not valid JSON` | `Unexpected token 't', "tru " is not valid JSON` | 是 |
| `"x "` | 400 | 400 | `Unexpected token 'x', "x " is not valid JSON` | `Unexpected token 'x', "x " is not valid JSON` | 是 |
| `" "` | 400 | 400 | `Unexpected end of JSON input` | `Unexpected end of JSON input` | 是 |
| `"\t"` | 400 | 400 | `Unexpected end of JSON input` | `Unexpected end of JSON input` | 是 |
| `"\n"` | 400 | 400 | `Unexpected end of JSON input` | `Unexpected end of JSON input` | 是 |
| `"   \t\n  "` | 400 | 400 | `Unexpected end of JSON input` | `Unexpected end of JSON input` | 是 |
| `"  tru"` | 400 | 400 | `Unexpected token 't', "  tru" is not valid JSON` | `Unexpected token 't', "  tru" is not valid JSON` | 是 |
| `"  {"` | 400 | 400 | `Expected property name or '}' in JSON at position 3 (line 1 column 4)` | `Expected property name or '}' in JSON at position 3 (line 1 column 4)` | 是 |
| `"  [1,]"` | 400 | 400 | `Unexpected token ']', "  [1,]" is not valid JSON` | `Unexpected token ']', "  [1,]" is not valid JSON` | 是 |
| `" {\"a\":}"` | 400 | 400 | `Unexpected token '}', " {"a":}" is not valid JSON` | `Unexpected token '}', " {"a":}" is not valid JSON` | 是 |
| `" [1,]"` | 400 | 400 | `Unexpected token ']', " [1,]" is not valid JSON` | `Unexpected token ']', " [1,]" is not valid JSON` | 是 |
| `"[1,] "` | 400 | 400 | `Unexpected token ']', "[1,] " is not valid JSON` | `Unexpected token ']', "[1,] " is not valid JSON` | 是 |
| `"#"` | 400 | 400 | `Unexpected token '#', "#" is not valid JSON` | `Unexpected token '#', "#" is not valid JSON` | 是 |
| `"##"` | 400 | 400 | `Unexpected token '#', "##" is not valid JSON` | `Unexpected token '#', "##" is not valid JSON` | 是 |
| `"x#y"` | 400 | 400 | `Unexpected token 'x', "x#y" is not valid JSON` | `Unexpected token 'x', "x#y" is not valid JSON` | 是 |
| `"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"` | 400 | 400 | `Unexpected token 'a', "aaaaaaaaaa"... is not valid JSON` | `Unexpected token 'a', "aaaaaaaaaa"... is not valid JSON` | 是 |
| `"zzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzzz"` | 400 | 400 | `Unexpected token 'z', "zzzzzzzzzz"... is not valid JSON` | `Unexpected token 'z', "zzzzzzzzzz"... is not valid JSON` | 是 |
| `"  qqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqqq"` | 400 | 400 | `Unexpected token 'q', "  qqqqqqqqqq"... is not valid JSON` | `Unexpected token 'q', "  qqqqqqqqqq"... is not valid JSON` | 是 |
| `"[1,2,3,4,5,6,7,8,9,10,11,x]"` | 400 | 400 | `Unexpected token 'x', ..."8,9,10,11,x]" is not valid JSON` | `Unexpected token 'x', ..."8,9,10,11,x]" is not valid JSON` | 是 |
| `"{\"aaaaaaaaaaaaaa\":1,\"bbbbbbbbbbbbbb\":@}"` | 400 | 400 | `Unexpected token '@', ..."bbbbbbbb":@}" is not valid JSON` | `Unexpected token '@', ..."bbbbbbbb":@}" is not valid JSON` | 是 |
| `"[\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",!]"` | 400 | 400 | `Unexpected token '!', ..."aaaaaaaa",!]" is not valid JSON` | `Unexpected token '!', ..."aaaaaaaa",!]" is not valid JSON` | 是 |

总用例=90  一致=90  不一致=0  状态码不一致=0

上表共 90 条，旧新两端状态码与 `error` 文案全部逐字节一致（`一致=90 不一致=0`）。空体是特例：
旧服务把它当 `{}`，再走业务校验，返回 `{"error":"需要配置处理函数（或旧的提取/解析函数）"}`；
新 Go 在 `decodeJSONBody` 里对长度 0 的体同样特判为 `{}`，故文案一致。

## 已知边界（本次范围外，未修改）

| 情形 | 旧服务 | 本项目 | 说明 |
| --- | --- | --- | --- |
| 体首紧跟 UTF-8 BOM（`\ufeff{}`） | BOM 被剥离后按合法 `{}` 处理 | 首字符判为 BOM，报 strict 语法错 | 与「非法 JSON 文案」无关，未改；空白之后再跟 BOM 时两端一致 |
| 数值溢出（`{"a":1e1000}`） | `JSON.parse` 得 `Infinity` 并继续 | `encoding/json` 值域报错 | 非「非法 JSON 文案」，未改 |
| 非 `utf-` charset 的 `Content-Type` | body-parser 报不支持 charset | 不校验 charset | 未改 |
| 增补平面首字符（astral） | 掩码长度按 UTF-16 码元计 | 掩码长度按 rune 计 | BMP 完全一致，astral 可能相差码元数 |
