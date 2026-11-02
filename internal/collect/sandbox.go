// QuotaHub 采集脚本沙箱。
//
// 执行模型：**每个脚本跑在一次性的子进程里**（同一个二进制，隐藏参数 --sandbox-runner），
// 父进程只通过 stdin/stdout 传一次 JSON。默认加固：
//   - 独立网络命名空间（CLONE_NEWNET）：脚本看不到也连不上任何网络（内网、云元数据地址都不行）
//   - 降权到 nobody（65534）+ no_new_privs + 不可 dump：即使引擎被攻破也只是个无权限进程
//   - RLIMIT_CPU / RLIMIT_AS / RLIMIT_NOFILE / RLIMIT_CORE：CPU、内存、句柄、core 全有硬上限
//   - 超时由父进程直接 SIGKILL 整个进程组：死循环只能烧掉自己的 CPU 配额，宿主毫发无伤
//
// 为什么不是进程内沙箱：goja 与 QuickJS-WASM 都**无法可靠中断** `while(true){}`——实测
// qjs v0.0.6 的 MaxExecutionTime、wazero 的 CloseOnContextDone 对它都不生效，脚本会把一个核
// 吃到进程重启。要防"恶意 JS"，必须有一个能被 kill 的边界，所以选择进程级隔离。
//
// 进程内仍保留 goja 实现（inline 模式），只用于单元测试与排障，生产默认走子进程。
package collect

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

// 沙箱执行模式。
const (
	// SandboxModeProcess 每个脚本一个一次性子进程（默认，生产用）。
	SandboxModeProcess = "process"
	// SandboxModeInline 进程内 goja，仅用于测试与排障。
	SandboxModeInline = "inline"
)

// defaultSandboxUID 是子进程降权后的 uid/gid（nobody）。
const defaultSandboxUID = 65534

// 沙箱请求类型（父进程与子进程的协议字段）。
const (
	kindParse   = "parse"
	kindExtract = "extract"
	kindEvalJS  = "evaljs"
)

// Sandbox 运行平台的处理 / 解析 / 提取函数与 JS 响应求值。
type Sandbox struct {
	timeout time.Duration
	mode    string
	self    string
	uid     int
}

// NewSandbox 构造沙箱；timeoutMs 已在配置层钳制到 [100,30000]。
// 执行模式与降权 uid 可用 QUOTAHUB_SANDBOX_MODE / QUOTAHUB_SANDBOX_UID 覆盖。
func NewSandbox(timeoutMs int) *Sandbox {
	mode := strings.ToLower(strings.TrimSpace(os.Getenv("QUOTAHUB_SANDBOX_MODE")))
	if mode != SandboxModeInline {
		mode = SandboxModeProcess
	}
	uid := defaultSandboxUID
	if raw := strings.TrimSpace(os.Getenv("QUOTAHUB_SANDBOX_UID")); raw != "" {
		if n, err := strconv.Atoi(raw); err == nil && n >= 0 {
			uid = n
		}
	}
	self, err := os.Executable()
	if err != nil {
		self = os.Args[0]
	}
	// 二进制被就地替换（部署时 mv）后，/proc/self/exe 会带 " (deleted)" 后缀，
	// 拿去 exec 会 ENOENT；这里剥掉，保证沙箱子进程总能起来。
	self = strings.TrimSuffix(self, " (deleted)")
	return &Sandbox{
		timeout: time.Duration(timeoutMs) * time.Millisecond,
		mode:    mode,
		self:    self,
		uid:     uid,
	}
}

// NewSandboxInline 强制进程内 goja，供单元测试使用（不依赖外部二进制）。
func NewSandboxInline(timeoutMs int) *Sandbox {
	return &Sandbox{timeout: time.Duration(timeoutMs) * time.Millisecond, mode: SandboxModeInline}
}

// errTimeout 是内部超时哨兵。
var errTimeout = errors.New("脚本执行超时")

// RunParse 解析函数（handler）：raw 为原始响应文本，字符串按原始值注入。
func (s *Sandbox) RunParse(raw, src string) (any, error) {
	return s.RunParseWithHeaders(raw, nil, src)
}

// RunParseWithHeaders 与 RunParse 相同，但额外把响应头以第二个参数 meta 传给脚本：
//
//	function (raw, meta) { meta.headers['subscription-userinfo'] }
//
// 机场订阅把已用/总量/到期放在 `subscription-userinfo` 响应头里而不是正文，正文只有
// base64 节点列表，所以必须让脚本能读到头。headers 的键统一小写，值按 ", " 拼接。
func (s *Sandbox) RunParseWithHeaders(raw string, headers map[string]string, src string) (any, error) {
	code := strings.TrimSpace(src)
	if code == "" {
		return nil, errors.New("未配置解析函数")
	}
	v, err := s.exec(sandboxRequest{Kind: kindParse, Src: code, Raw: raw, Headers: headers})
	if err != nil {
		if errors.Is(err, errTimeout) {
			return nil, fmt.Errorf("解析函数执行超时 (%dms)", s.timeout.Milliseconds())
		}
		return nil, fmt.Errorf("解析函数执行失败: %s", err.Error())
	}
	return v, nil
}

// RunExtractor 提取函数：先做黑名单 / 白名单检查，再以 JSON 字面量注入 data。
func (s *Sandbox) RunExtractor(src string, data any) (any, error) {
	code := strings.TrimSpace(src)
	if code == "" {
		return nil, errors.New("未配置提取函数")
	}
	checked := decodeUnicodeEscapes(code)
	if bad := forbiddenHit(checked); bad != "" {
		return nil, fmt.Errorf("提取函数包含禁止的 API: %s", bad)
	}
	if err := checkTopLevelWhitelist(checked); err != nil {
		return nil, err
	}
	dataJSON, err := json.Marshal(data)
	if err != nil || len(dataJSON) == 0 {
		dataJSON = []byte("null")
	}
	v, err := s.exec(sandboxRequest{Kind: kindExtract, Src: code, Data: dataJSON})
	if err != nil {
		if errors.Is(err, errTimeout) {
			return nil, fmt.Errorf("提取函数执行超时 (%dms)", s.timeout.Milliseconds())
		}
		return nil, fmt.Errorf("提取函数执行失败: %s", err.Error())
	}
	return v, nil
}

// EvalJSResponse 求值非 JSON 的 JS 赋值式响应：去掉 `;0x..;` 长度前缀后在沙箱求值。
func (s *Sandbox) EvalJSResponse(text string) (any, error) {
	code := strings.TrimSpace(jsLengthPrefix.ReplaceAllString(strings.TrimSpace(text), ""))
	if code == "" {
		return nil, errors.New("响应不是有效 JSON")
	}
	v, err := s.exec(sandboxRequest{Kind: kindEvalJS, Src: code})
	if err != nil {
		if errors.Is(err, errTimeout) {
			return nil, errors.New("响应求值超时")
		}
		return nil, fmt.Errorf("响应不是有效 JSON/JS: %s", err.Error())
	}
	if v == nil {
		return nil, errors.New("响应求值结果为空")
	}
	return v, nil
}

// exec 按模式分发：生产走一次性子进程，测试走进程内 goja。
func (s *Sandbox) exec(req sandboxRequest) (any, error) {
	req.TimeoutMs = int(s.timeout.Milliseconds())
	if s.mode == SandboxModeInline {
		return s.runInline(req)
	}
	return s.execProcess(req)
}

// --- 进程内 goja 实现（inline 模式 + 沙箱子进程内部使用）---

// runInline 在进程内执行请求，返回导出值；超时返回 errTimeout，脚本异常返回 error。
func (s *Sandbox) runInline(req sandboxRequest) (any, error) {
	var setup, expr string
	globals := map[string]any{}

	switch req.Kind {
	case kindParse:
		headers := req.Headers
		if headers == nil {
			headers = map[string]string{}
		}
		globals["raw"] = req.Raw
		globals["meta"] = map[string]any{"headers": headers}
		expr = "(" + req.Src + ")(raw, meta)"
	case kindExtract:
		dataJSON := strings.TrimSpace(string(req.Data))
		if dataJSON == "" {
			dataJSON = "null"
		}
		setup = "data = " + dataJSON
		expr = "(" + req.Src + ")(data)"
	case kindEvalJS:
		setup = "self = {}; $R = self.$R = {}"
		expr = req.Src
	default:
		return nil, fmt.Errorf("未知的沙箱请求类型 %q", req.Kind)
	}

	v, err := s.runGoja(globals, setup, expr)
	if err != nil {
		return nil, err
	}
	if v == nil {
		return nil, nil
	}
	return exportValue(v), nil
}

// runGoja 新建干净运行时执行；超时用 Interrupt 中断（结果归一为 errTimeout）。
func (s *Sandbox) runGoja(globals map[string]any, setup, expr string) (goja.Value, error) {
	rt := goja.New()
	var timedOut int32
	timer := time.AfterFunc(s.timeout, func() {
		atomic.StoreInt32(&timedOut, 1)
		rt.Interrupt("脚本执行超时")
	})
	defer timer.Stop()

	for k, val := range globals {
		if err := rt.Set(k, val); err != nil {
			return nil, errors.New(err.Error())
		}
	}
	if setup != "" {
		if _, err := rt.RunString(setup); err != nil {
			if atomic.LoadInt32(&timedOut) == 1 {
				return nil, errTimeout
			}
			return nil, errors.New(jsMessage(err))
		}
	}
	v, err := rt.RunString(expr)
	if err != nil {
		if atomic.LoadInt32(&timedOut) == 1 {
			return nil, errTimeout
		}
		return nil, errors.New(jsMessage(err))
	}
	return v, nil
}

// exportValue 把 goja 值导出为宿主可用的 Go 值；undefined/null 导出为 nil。
func exportValue(v goja.Value) any {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return nil
	}
	return v.Export()
}

// jsMessage 取 JS 异常的 message（对齐 `(e as Error).message`，无则为 "undefined"）。
func jsMessage(err error) string {
	var ex *goja.Exception
	if errors.As(err, &ex) {
		if obj, ok := ex.Value().(*goja.Object); ok {
			if m := obj.Get("message"); m != nil && !goja.IsUndefined(m) {
				return m.String()
			}
		}
		return "undefined"
	}
	return err.Error()
}

// --- 值编码：NaN / ±Infinity 不是合法 JSON，用 {"$f":"NaN"} 这类标记往返 ---

// encodeForWire 把导出值转成可 JSON 化的结构。
func encodeForWire(v any) any {
	switch x := v.(type) {
	case float64:
		switch {
		case math.IsNaN(x):
			return map[string]any{"$f": "NaN"}
		case math.IsInf(x, 1):
			return map[string]any{"$f": "+Inf"}
		case math.IsInf(x, -1):
			return map[string]any{"$f": "-Inf"}
		}
		return x
	case []any:
		out := make([]any, len(x))
		for i := range x {
			out[i] = encodeForWire(x[i])
		}
		return out
	case map[string]any:
		out := make(map[string]any, len(x))
		for k, val := range x {
			out[k] = encodeForWire(val)
		}
		return out
	default:
		return v
	}
}

// decodeFromWire 还原 encodeForWire 的标记。
func decodeFromWire(v any) any {
	switch x := v.(type) {
	case map[string]any:
		if len(x) == 1 {
			if f, ok := x["$f"].(string); ok {
				switch f {
				case "NaN":
					return math.NaN()
				case "+Inf":
					return math.Inf(1)
				case "-Inf":
					return math.Inf(-1)
				}
			}
		}
		for k, val := range x {
			x[k] = decodeFromWire(val)
		}
		return x
	case []any:
		for i := range x {
			x[i] = decodeFromWire(x[i])
		}
		return x
	default:
		return v
	}
}

var jsLengthPrefix = regexp.MustCompile(`^;0x[0-9a-f]+;`)

// --- 黑名单 / 白名单（逐条复刻旧实现） ---

var (
	forbiddenFn = regexp.MustCompile(`\b(new\s+Function|Function\s*\()`)
	forbidden   = regexp.MustCompile(`(?i)\b(require|import\s*\(|import\b|process|module|exports|globalThis|\bglobal\b|window|document|fetch|XMLHttpRequest|WebSocket|\beval\b|constructor|__proto__|prototype|child_process|exec|spawn|node:|\bthis\b|setPrototypeOf|getPrototypeOf|defineProperty|defineProperties|__defineGetter__|__defineSetter__|__lookupGetter__|__lookupSetter__|Reflect|Proxy)`)
	unicodeEsc  = regexp.MustCompile(`\\u([0-9a-fA-F]{4})`)
)

// decodeUnicodeEscapes 解码 \uXXXX 转义后再做词法检查。
func decodeUnicodeEscapes(code string) string {
	return unicodeEsc.ReplaceAllStringFunc(code, func(m string) string {
		hex := m[2:]
		var r rune
		for _, c := range hex {
			r = r*16 + rune(hexVal(byte(c)))
		}
		return string(r)
	})
}

func hexVal(c byte) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	}
	return 0
}

// forbiddenHit 返回首个命中的禁用片段（空串表示无命中）。
func forbiddenHit(checked string) string {
	if m := forbiddenFn.FindString(checked); m != "" {
		return m
	}
	return forbidden.FindString(checked)
}

var allowedTopLevel = map[string]bool{
	"data": true, "Math": true, "Number": true, "JSON": true, "Object": true, "Array": true,
	"String": true, "Boolean": true, "Date": true, "RegExp": true, "parseInt": true,
	"parseFloat": true, "isNaN": true, "isFinite": true, "Infinity": true, "NaN": true,
	"undefined": true, "arguments": true, "function": true, "return": true, "var": true,
	"let": true, "const": true, "if": true, "else": true, "for": true, "while": true,
	"do": true, "switch": true, "case": true, "default": true, "break": true,
	"continue": true, "throw": true, "try": true, "catch": true, "finally": true,
	"typeof": true, "instanceof": true, "in": true, "of": true, "new": true,
	"delete": true, "void": true, "true": true, "false": true, "null": true, "yield": true,
	"await": true, "async": true, "class": true, "extends": true, "super": true,
	"static": true, "get": true, "set": true,
}

var (
	blockComment = regexp.MustCompile(`(?s)/\*.*?\*/`)
	lineComment  = regexp.MustCompile(`//[^\n\r]*`)
	strLiteral   = regexp.MustCompile(`'(?:\\.|[^'\\\n])*'|"(?:\\.|[^"\\\n])*"`)
	templateLit  = regexp.MustCompile("(?s)`(?:\\\\.|[^`\\\\])*`")
	templateExpr = regexp.MustCompile(`\$\{([\s\S]*?)\}`)
	declVar      = regexp.MustCompile(`\b(?:var|let|const)\s+([A-Za-z_$][\w$]*)`)
	funcParams   = regexp.MustCompile(`function\s*\(([^)]*)\)`)
	arrowParams  = regexp.MustCompile(`\(([^)]*)\)\s*=>`)
	arrowSingle  = regexp.MustCompile(`([A-Za-z_$][\w$]*)\s*=>`)
	identToken   = regexp.MustCompile(`[A-Za-z_$][\w$]*`)
	identInParam = regexp.MustCompile(`[A-Za-z_$][\w$]*`)
)

// checkTopLevelWhitelist 复刻旧实现的顶层标识符白名单检查。
func checkTopLevelWhitelist(code string) error {
	stripped := blockComment.ReplaceAllString(code, " ")
	stripped = lineComment.ReplaceAllString(stripped, " ")
	stripped = strLiteral.ReplaceAllString(stripped, `""`)
	stripped = templateLit.ReplaceAllStringFunc(stripped, func(m string) string {
		exprs := templateExpr.FindAllStringSubmatch(m, -1)
		if len(exprs) == 0 {
			return "``"
		}
		parts := make([]string, 0, len(exprs))
		for _, e := range exprs {
			parts = append(parts, e[1])
		}
		return strings.Join(parts, " ; ")
	})

	declared := map[string]bool{}
	for _, m := range declVar.FindAllStringSubmatch(stripped, -1) {
		declared[m[1]] = true
	}
	for _, m := range funcParams.FindAllStringSubmatch(stripped, -1) {
		addParams(declared, m[1])
	}
	for _, m := range arrowParams.FindAllStringSubmatch(stripped, -1) {
		addParams(declared, m[1])
	}
	for _, m := range arrowSingle.FindAllStringSubmatch(stripped, -1) {
		declared[m[1]] = true
	}

	for _, t := range identToken.FindAllString(stripped, -1) {
		if allowedTopLevel[t] || declared[t] {
			continue
		}
		if isPropertyOnly(stripped, t) {
			continue
		}
		return fmt.Errorf("使用了白名单外的标识符: %s", t)
	}
	return nil
}

func addParams(declared map[string]bool, params string) {
	for _, p := range strings.Split(params, ",") {
		if n := identInParam.FindString(p); n != "" {
			declared[n] = true
		}
	}
}

// isPropertyOnly 判断 token 是否只作为属性访问出现（前导字符为 . [ ] ,）。
func isPropertyOnly(stripped, token string) bool {
	idx := -1
	for {
		next := strings.Index(stripped[idx+1:], token)
		if next == -1 {
			break
		}
		idx += 1 + next
		before := strings.TrimRight(stripped[:idx], " \t\r\n")
		var prev byte
		if before != "" {
			prev = before[len(before)-1]
		}
		if prev != '.' && prev != '[' && prev != ']' && prev != ',' {
			return false
		}
	}
	return true
}
