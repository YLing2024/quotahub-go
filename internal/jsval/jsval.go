// Package jsval 复刻旧实现里对松散 JSON 的 JS 取值语义。
//
// 旧实现全程在 JS 里处理请求体：`body.name ?? ”`、`String(x).trim()`、`x || 默认值`、
// `typeof v === 'object' && !Array.isArray(v)` 等。Go 的 encoding/json 把对象解成
// map[string]any、数组解成 []any、数字解成 float64，本包把这些判断收敛成一组小函数，
// 让业务层写出与旧实现逐条对应的分支。
package jsval

import (
	"math"
	"strconv"
)

// Obj 判断 v 是否为 JSON 对象（排除数组与 null），是则返回其 map。
func Obj(v any) (map[string]any, bool) {
	m, ok := v.(map[string]any)
	return m, ok
}

// ObjOrEmpty 返回 JSON 对象；非对象返回空 map（对齐 `rawObject(v)`）。
func ObjOrEmpty(v any) map[string]any {
	if m, ok := Obj(v); ok {
		return m
	}
	return map[string]any{}
}

// IsArray 判断 v 是否为 JSON 数组。
func IsArray(v any) bool {
	_, ok := v.([]any)
	return ok
}

// Str 按 JS `String(v)` 口径转字符串：undefined/null 交给调用方用 StrOr 兜底。
func Str(v any) string {
	switch t := v.(type) {
	case nil:
		return "undefined"
	case string:
		return t
	case bool:
		if t {
			return "true"
		}
		return "false"
	case float64:
		return formatNumber(t)
	case map[string]any, []any:
		// JS String(对象) 为 "[object Object]"/"[object Array]"；业务里不依赖该形态，
		// 这里给可读回退，避免出现误导性的空串。
		if _, ok := t.([]any); ok {
			return "[object Array]"
		}
		return "[object Object]"
	default:
		return "[object Object]"
	}
}

// StrOr 复刻 `String(v ?? ”)`：null/undefined 用 def，其余按 JS String() 转。
func StrOr(v any, def string) string {
	if v == nil {
		return def
	}
	return Str(v)
}

// Truthy 复刻 JS 真值判断（用于 `x || 默认值`、`body ? ... : ...`）。
func Truthy(v any) bool {
	switch t := v.(type) {
	case nil:
		return false
	case bool:
		return t
	case string:
		return t != ""
	case float64:
		return t != 0 && !math.IsNaN(t)
	case int64:
		return t != 0
	case int:
		return t != 0
	case int32:
		return t != 0
	case float32:
		return t != 0
	case map[string]any, []any:
		return true
	default:
		return true
	}
}

// Number 复刻 JS `Number(v)`：null→0、布尔→1/0、字符串按十进制解析（空串→0），
// 无法解析或对象→NaN（用 ok=false 表示）。
func Number(v any) (float64, bool) {
	switch t := v.(type) {
	case nil:
		return 0, true
	case bool:
		if t {
			return 1, true
		}
		return 0, true
	case float64:
		return t, true
	case int64:
		return float64(t), true
	case int:
		return float64(t), true
	case int32:
		return float64(t), true
	case float32:
		return float64(t), true
	case string:
		s := trimSpace(t)
		if s == "" {
			return 0, true
		}
		f, err := strconv.ParseFloat(s, 64)
		if err != nil {
			return 0, false
		}
		return f, true
	case map[string]any, []any:
		return 0, false
	default:
		return 0, false
	}
}

// Finite 判断浮点是否有限（复刻 Number.isFinite）。
func Finite(f float64) bool { return !math.IsInf(f, 0) && !math.IsNaN(f) }

// IsNumber 判断 v 是否为 JS number 类型。JSON 数字解成 float64，goja 导出的整数为 int64，
// 两者都属 number（复刻 `typeof v === 'number'`）。
func IsNumber(v any) bool {
	switch v.(type) {
	case float64, int64, int, int32:
		return true
	}
	return false
}

// Numeric 把数值型值取为 float64；非 number 返回 false。
func Numeric(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int64:
		return float64(t), true
	case int:
		return float64(t), true
	case int32:
		return float64(t), true
	}
	return 0, false
}

// formatNumber 按 JS Number.toString() 常见口径格式化（整数无小数点）。
func formatNumber(f float64) string {
	if math.IsNaN(f) {
		return "NaN"
	}
	if math.IsInf(f, 1) {
		return "Infinity"
	}
	if math.IsInf(f, -1) {
		return "-Infinity"
	}
	if f == math.Trunc(f) && math.Abs(f) < 1e21 {
		return strconv.FormatFloat(f, 'f', -1, 64)
	}
	return strconv.FormatFloat(f, 'g', -1, 64)
}

// trimSpace 复刻 JS String.prototype.trim() 覆盖的空白字符（含 BOM）。
func trimSpace(s string) string {
	start := 0
	for start < len(s) && isJSSpace(rune(s[start])) {
		start++
	}
	end := len(s)
	for end > start && isJSSpace(rune(s[end-1])) {
		end--
	}
	return s[start:end]
}

func isJSSpace(r rune) bool {
	switch r {
	case ' ', '\t', '\n', '\r', '\v', '\f', 0x00A0, 0xFEFF:
		return true
	}
	return false
}
