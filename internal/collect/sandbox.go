// QuotaHub 采集脚本沙箱（goja 纯 Go 引擎）。
//
// 安全边界与旧实现 lib/fetcher.ts 的 vm 沙箱等价：
//   - 干净运行时，绝不注入任何宿主对象；data 以 JSON 字面量注入，其 constructor 链只能
//     到达引擎内部（goja 的 eval/Function 同样只能触达引擎内部），无法触达宿主 Go。
//   - 黑名单 + 顶层标识符白名单为纵深防御；解码 \uXXXX 后再做词法检查，堵 data.\u0063onstructor 绕过。
//   - 超时通过 runtime.Interrupt 中断执行（等价 node:vm 的 timeout）。
package collect

import (
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strings"
	"sync/atomic"
	"time"

	"github.com/dop251/goja"
)

// Sandbox 运行平台的处理 / 解析 / 提取函数与 JS 响应求值。
type Sandbox struct {
	timeout time.Duration
}

// NewSandbox 构造沙箱；timeoutMs 已在配置层钳制到 [100,30000]。
func NewSandbox(timeoutMs int) *Sandbox {
	return &Sandbox{timeout: time.Duration(timeoutMs) * time.Millisecond}
}

// errTimeout 是内部超时哨兵。
var errTimeout = errors.New("脚本执行超时")

// RunParse 解析函数（handler）：raw 为原始响应文本，字符串按原始值注入，代码在沙箱内自由 eval。
func (s *Sandbox) RunParse(raw, src string) (any, error) {
	code := strings.TrimSpace(src)
	if code == "" {
		return nil, errors.New("未配置解析函数")
	}
	v, err := s.run(map[string]any{"raw": raw}, "("+code+")(raw)")
	if err != nil {
		if errors.Is(err, errTimeout) {
			return nil, fmt.Errorf("解析函数执行超时 (%dms)", s.timeout.Milliseconds())
		}
		return nil, fmt.Errorf("解析函数执行失败: %s", jsMessage(err))
	}
	return exportValue(v), nil
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
	dataJSON := "null"
	if raw, err := json.Marshal(data); err == nil {
		dataJSON = string(raw)
	}
	v, err := s.runRaw("data = "+dataJSON, "("+code+")(data)")
	if err != nil {
		if errors.Is(err, errTimeout) {
			return nil, fmt.Errorf("提取函数执行超时 (%dms)", s.timeout.Milliseconds())
		}
		return nil, fmt.Errorf("提取函数执行失败: %s", jsMessage(err))
	}
	return exportValue(v), nil
}

// EvalJSResponse 求值非 JSON 的 JS 赋值式响应：去掉 `;0x..;` 长度前缀后在沙箱求值。
func (s *Sandbox) EvalJSResponse(text string) (any, error) {
	code := strings.TrimSpace(jsLengthPrefix.ReplaceAllString(strings.TrimSpace(text), ""))
	if code == "" {
		return nil, errors.New("响应不是有效 JSON")
	}
	v, err := s.runRaw("self = {}; $R = self.$R = {}", code)
	if err != nil {
		if errors.Is(err, errTimeout) {
			return nil, errors.New("响应求值超时")
		}
		return nil, fmt.Errorf("响应不是有效 JSON/JS: %s", jsMessage(err))
	}
	if v == nil || goja.IsUndefined(v) {
		return nil, errors.New("响应求值结果为空")
	}
	return exportValue(v), nil
}

// run 以 varName=value 注入单值后执行 expr。
func (s *Sandbox) run(globals map[string]any, expr string) (goja.Value, error) {
	return s.exec(func(rt *goja.Runtime) (goja.Value, error) {
		for k, v := range globals {
			if err := rt.Set(k, v); err != nil {
				return nil, err
			}
		}
		return rt.RunString(expr)
	})
}

// runRaw 先跑 setup（如注入 data），再跑 expr；两步共享同一超时预算。
func (s *Sandbox) runRaw(setup, expr string) (goja.Value, error) {
	return s.exec(func(rt *goja.Runtime) (goja.Value, error) {
		if _, err := rt.RunString(setup); err != nil {
			return nil, err
		}
		return rt.RunString(expr)
	})
}

// exec 新建干净运行时并执行 fn；超时用 Interrupt 中断（结果通过 errTimeout 归一）。
func (s *Sandbox) exec(fn func(rt *goja.Runtime) (goja.Value, error)) (goja.Value, error) {
	rt := goja.New()
	var timedOut int32
	timer := time.AfterFunc(s.timeout, func() {
		atomic.StoreInt32(&timedOut, 1)
		rt.Interrupt("脚本执行超时")
	})
	defer timer.Stop()
	v, err := fn(rt)
	if err != nil {
		if atomic.LoadInt32(&timedOut) == 1 {
			return nil, errTimeout
		}
		return nil, err
	}
	return v, nil
}

// exportValue 把 goja 值导出为宿主可用的 Go 值；undefined/null 导出为 nil。
func exportValue(v goja.Value) any {
	if v == nil {
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
		var code rune
		for _, c := range hex {
			code = code*16 + rune(hexVal(byte(c)))
		}
		return string(code)
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
