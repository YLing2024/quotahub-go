package collect

import (
	"strings"
	"testing"
)

func TestRunParse(t *testing.T) {
	s := NewSandbox(200)

	t.Run("JSON 响应解析", func(t *testing.T) {
		v, err := s.RunParse(`{"balance": 3.14}`, `function (raw) { return JSON.parse(raw).balance }`)
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		if n, _ := numericOf(v); n != 3.14 {
			t.Fatalf("期望 3.14，得到 %v", v)
		}
	})

	t.Run("函数内允许标准 eval", func(t *testing.T) {
		v, err := s.RunParse("whatever", `function (raw) { return eval("40+2") }`)
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		if n, _ := numericOf(v); n != 42 {
			t.Fatalf("期望 42，得到 %v", v)
		}
	})

	t.Run("语法错误中文提示", func(t *testing.T) {
		_, err := s.RunParse("x", `function (raw) { return ))) }`)
		if err == nil || !strings.Contains(err.Error(), "解析函数执行失败") {
			t.Fatalf("期望语法错误，得到: %v", err)
		}
	})

	t.Run("死循环超时", func(t *testing.T) {
		_, err := s.RunParse("x", `function (raw) { for(;;){} }`)
		if err == nil || err.Error() != "解析函数执行超时 (200ms)" {
			t.Fatalf("期望超时，得到: %v", err)
		}
	})

	t.Run("未配置解析函数", func(t *testing.T) {
		if _, err := s.RunParse("x", "   "); err == nil || err.Error() != "未配置解析函数" {
			t.Fatalf("期望未配置错误，得到: %v", err)
		}
	})
}

func TestRunExtractor(t *testing.T) {
	s := NewSandbox(200)

	t.Run("正常提取", func(t *testing.T) {
		v, err := s.RunExtractor(`function (data) { return data.quota / 2 }`, map[string]any{"quota": 10.0})
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		if n, _ := numericOf(v); n != 5 {
			t.Fatalf("期望 5，得到 %v", v)
		}
	})

	t.Run("禁止 require/process", func(t *testing.T) {
		_, err := s.RunExtractor(`function(d){ return require("fs") }`, map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "提取函数包含禁止的 API: ") {
			t.Fatalf("期望拦截，得到: %v", err)
		}
	})

	t.Run("禁止 Function 构造器", func(t *testing.T) {
		_, err := s.RunExtractor(`new Function("return process.env")()`, map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "提取函数包含禁止的 API") {
			t.Fatalf("期望拦截，得到: %v", err)
		}
	})

	t.Run("constructor 逃逸链被拦截", func(t *testing.T) {
		_, err := s.RunExtractor(`function(d){ return d.constructor.constructor("return process")() }`, map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "提取函数包含禁止的 API: constructor") {
			t.Fatalf("期望拦截 constructor，得到: %v", err)
		}
	})

	t.Run("unicode 转义绕过被解码后拦截", func(t *testing.T) {
		_, err := s.RunExtractor(`function(d){ return d.\u0063onstructor }`, map[string]any{})
		if err == nil || !strings.Contains(err.Error(), "提取函数包含禁止的 API: constructor") {
			t.Fatalf("期望拦截 constructor，得到: %v", err)
		}
	})

	t.Run("白名单外标识符被拦截", func(t *testing.T) {
		_, err := s.RunExtractor(`function(d){ return evilGlobal }`, map[string]any{})
		if err == nil || err.Error() != "使用了白名单外的标识符: evilGlobal" {
			t.Fatalf("期望白名单拦截，得到: %v", err)
		}
	})

	t.Run("预设风格属性链放行", func(t *testing.T) {
		v, err := s.RunExtractor(
			"function (data) {\n  var infos = data.balance_infos\n  if (!infos || !infos.length) return null\n  return infos[0].total_balance\n}",
			map[string]any{"balance_infos": []any{map[string]any{"total_balance": 5.0}}},
		)
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		if n, _ := numericOf(v); n != 5 {
			t.Fatalf("期望 5，得到 %v", v)
		}
	})
}

func TestEvalJSResponse(t *testing.T) {
	s := NewSandbox(200)

	t.Run("$R 协议求值", func(t *testing.T) {
		v, err := s.EvalJSResponse(`;0x1f;$R = { quota: 1234 };$R`)
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		m, ok := v.(map[string]any)
		if !ok {
			t.Fatalf("期望对象，得到 %T", v)
		}
		if n, _ := numericOf(m["quota"]); n != 1234 {
			t.Fatalf("期望 1234，得到 %v", m["quota"])
		}
	})

	t.Run("空文本报错", func(t *testing.T) {
		if _, err := s.EvalJSResponse("   "); err == nil || err.Error() != "响应不是有效 JSON" {
			t.Fatalf("期望空文本错误，得到: %v", err)
		}
	})
}

func TestResolvePath(t *testing.T) {
	data := map[string]any{"a": map[string]any{"b": []any{map[string]any{"c": 5.0}}}}

	if got := resolvePath(data, "a.b[0].c"); numeric(got) != 5 {
		t.Fatalf("期望 5，得到 %v", got)
	}
	if got := resolvePath(data, "a.missing"); got != nil {
		t.Fatalf("期望 nil，得到 %v", got)
	}
	if got := resolvePath(nil, "a.b"); got != nil {
		t.Fatalf("期望 nil，得到 %v", got)
	}
}

// numericOf 从沙箱导出的数值取 float64（goja 整数导出为 int64）。
func numericOf(v any) (float64, bool) {
	switch t := v.(type) {
	case float64:
		return t, true
	case int64:
		return float64(t), true
	case int:
		return float64(t), true
	}
	return 0, false
}

func numeric(v any) float64 { n, _ := numericOf(v); return n }
