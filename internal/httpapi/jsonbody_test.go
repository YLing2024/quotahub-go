package httpapi

import (
	"net/http/httptest"
	"strings"
	"testing"

	"quotahub-go/internal/httperr"
)

// decodeBody 走真实的 decodeJSONBody 路径（含 Content-Type 判定与空体特例），便于对表测试。
func decodeBody(t *testing.T, contentType, body string) (any, error) {
	t.Helper()
	r := httptest.NewRequest("POST", "/api/platforms", strings.NewReader(body))
	if contentType != "" {
		r.Header.Set("Content-Type", contentType)
	}
	return decodeJSONBody(r)
}

// TestDecodeJSONBodyErrorsMatchOldServer 用旧实现（Express + body-parser 2.3.0 strict + V8
// JSON.parse）在 HTTP 上实测的报错原文逐字节对拍。文案来源表见 docs/COMPAT-JSON-ERRORS.md；
// 前 10 条是验收要求覆盖的常见形态，其余为补充采集，覆盖 strict 首字符分支与上下文截取。
func TestDecodeJSONBodyErrorsMatchOldServer(t *testing.T) {
	cases := []struct{ input, want string }{
		// 验收要求的常见形态（空体是特例，见 TestDecodeJSONBodyEmptyBodyIsEmptyObject）。
		{`{not json`, `Expected property name or '}' in JSON at position 1 (line 1 column 2)`},
		{`{"a":}`, `Unexpected token '}', "{"a":}" is not valid JSON`},
		{`{"a" 1}`, `Expected ':' after property name in JSON at position 5 (line 1 column 6)`},
		{`{"a":1,}`, `Expected double-quoted property name in JSON at position 7 (line 1 column 8)`},
		{`{"a":"x}`, `Unterminated string in JSON at position 8 (line 1 column 9)`},
		{`{"a":01}`, `Unexpected number in JSON at position 6 (line 1 column 7)`},
		{`[1,]`, `Unexpected token ']', "[1,]" is not valid JSON`},
		{`{`, `Expected property name or '}' in JSON at position 1 (line 1 column 2)`},
		{`tru`, `Unexpected token 't', "tru" is not valid JSON`},
		{`{"a":1} trailing`, `Unexpected non-whitespace character after JSON at position 8 (line 1 column 9)`},

		// 首字符不是 '{' / '['：body-parser strict 分支（与裸 JSON.parse 文案不同）。
		{`}`, `Unexpected token '}', "}" is not valid JSON`},
		{`]`, `Unexpected token ']', "]" is not valid JSON`},
		{`,`, `Unexpected token ',', "," is not valid JSON`},
		{`:`, `Unexpected token ':', ":" is not valid JSON`},
		{`true`, `Unexpected token 't', "true" is not valid JSON`},
		{`false`, `Unexpected token 'f', "false" is not valid JSON`},
		{`null`, `Unexpected token 'n', "null" is not valid JSON`},
		{`01`, `Unexpected token '0', "01" is not valid JSON`},
		{`00`, `Unexpected token '0', "00" is not valid JSON`},
		{`-`, `Unexpected token '-', "-" is not valid JSON`},
		{`1.`, `Unexpected token '1', "1." is not valid JSON`},
		{`1e`, `Unexpected token '1', "1e" is not valid JSON`},
		{`1e+`, `Unexpected token '1', "1e+" is not valid JSON`},
		{`undefined`, `Unexpected token 'u', "undefined" is not valid JSON`},
		{`NaN`, `Unexpected token 'N', "NaN" is not valid JSON`},
		{`Infinity`, `Unexpected token 'I', "Infinity" is not valid JSON`},
		{`nul`, `Unexpected token 'n', "nul" is not valid JSON`},
		{`tru}`, `Unexpected token 't', "tru}" is not valid JSON`},
		{`TRUE`, `Unexpected token 'T', "TRUE" is not valid JSON`},
		{`1x`, `Unexpected token '1', "1x" is not valid JSON`},
		{`0x1`, `Unexpected token '0', "0x1" is not valid JSON`},
		{`true x`, `Unexpected token 't', "true x" is not valid JSON`},
		{`"abc`, `Unexpected token '"', ""abc" is not valid JSON`},
		{`"a\`, `Unexpected token '"', ""a\" is not valid JSON`},
		{`"a\x"`, `Unexpected token '"', ""a\x"" is not valid JSON`},
		{`"\u"`, `Unexpected token '"', ""\u"" is not valid JSON`},
		{`0.1.2`, `Unexpected token '0', "0.1.2" is not valid JSON`},
		{`1e1e1`, `Unexpected token '1', "1e1e1" is not valid JSON`},
		{`--1`, `Unexpected token '-', "--1" is not valid JSON`},
		{`+1`, `Unexpected token '+', "+1" is not valid JSON`},
		{`.5`, `Unexpected token '.', ".5" is not valid JSON`},
		{"\x00", "Unexpected token '\x00', \"\x00\" is not valid JSON"},
		{`#`, `Unexpected token '#', "#" is not valid JSON`},
		{`##`, `Unexpected token '#', "##" is not valid JSON`},
		{`x#y`, `Unexpected token 'x', "x#y" is not valid JSON`},
		{`null x`, `Unexpected token 'n', "null x" is not valid JSON`},
		{`false x`, `Unexpected token 'f', "false x" is not valid JSON`},
		{`tru `, `Unexpected token 't', "tru " is not valid JSON`},
		{`x `, `Unexpected token 'x', "x " is not valid JSON`},

		// 前导空白：strict 与位置口径都要正确。
		{` `, `Unexpected end of JSON input`},
		{"\t", `Unexpected end of JSON input`},
		{"\n", `Unexpected end of JSON input`},
		{"   \t\n  ", `Unexpected end of JSON input`},
		{`  tru`, `Unexpected token 't', "  tru" is not valid JSON`},
		{`  {`, `Expected property name or '}' in JSON at position 3 (line 1 column 4)`},
		{`  [1,]`, `Unexpected token ']', "  [1,]" is not valid JSON`},
		{` {"a":}`, `Unexpected token '}', " {"a":}" is not valid JSON`},
		{` [1,]`, `Unexpected token ']', " [1,]" is not valid JSON`},
		{`[1,] `, `Unexpected token ']', "[1,] " is not valid JSON`},

		// 首字符是 '{' / '['：走 V8 JSON.parse 文案（含位置/行列）。
		{`{,}`, `Expected property name or '}' in JSON at position 1 (line 1 column 2)`},
		{`{]`, `Expected property name or '}' in JSON at position 1 (line 1 column 2)`},
		{`[}`, `Unexpected token '}', "[}" is not valid JSON`},
		{`[,]`, `Unexpected token ',', "[,]" is not valid JSON`},
		{`[1,,2]`, `Unexpected token ',', "[1,,2]" is not valid JSON`},
		{`{"a"`, `Expected ':' after property name in JSON at position 4 (line 1 column 5)`},
		{`{"a":`, `Unexpected end of JSON input`},
		{`{"a"}`, `Expected ':' after property name in JSON at position 4 (line 1 column 5)`},
		{`{"a":1 "b":2}`, `Expected ',' or '}' after property value in JSON at position 7 (line 1 column 8)`},
		{`{"a":1`, `Expected ',' or '}' after property value in JSON at position 6 (line 1 column 7)`},
		{`{} x`, `Unexpected non-whitespace character after JSON at position 3 (line 1 column 4)`},
		{`[] x`, `Unexpected non-whitespace character after JSON at position 3 (line 1 column 4)`},
		{"{\"a\":1}\n{\"b\":2}", `Unexpected non-whitespace character after JSON at position 8 (line 2 column 1)`},
		{`[1 2]`, `Expected ',' or ']' after array element in JSON at position 3 (line 1 column 4)`},
		{`[01]`, `Unexpected number in JSON at position 2 (line 1 column 3)`},
		{`{"a":1.}`, `Unterminated fractional number in JSON at position 7 (line 1 column 8)`},
		{`{"a":-}`, `No number after minus sign in JSON at position 6 (line 1 column 7)`},
		{`{"a":"\q"}`, `Bad escaped character in JSON at position 7 (line 1 column 8)`},
		{`{"a":"\uZZZZ"}`, `Bad Unicode escape in JSON at position 8 (line 1 column 9)`},
		{`{"a":"\u12"}`, `Bad Unicode escape in JSON at position 10 (line 1 column 11)`},
		{`{"a":"\u0"}`, `Bad Unicode escape in JSON at position 9 (line 1 column 10)`},
		{`[tru]`, `Unexpected token ']', "[tru]" is not valid JSON`},
		{`{"a":tru}`, `Unexpected token '}', "{"a":tru}" is not valid JSON`},
		{`{"a":undefined}`, `Unexpected token 'u', "{"a":undefined}" is not valid JSON`},
		{`{"a":1}extra`, `Unexpected non-whitespace character after JSON at position 7 (line 1 column 8)`},
		{`{"a":1} {"b":2}`, `Unexpected non-whitespace character after JSON at position 8 (line 1 column 9)`},
		{`[1,2,3,4,5,6,7,8,9,10,11,x]`, `Unexpected token 'x', ..."8,9,10,11,x]" is not valid JSON`},
		{`{"aaaaaaaaaaaaaa":1,"bbbbbbbbbbbbbb":@}`, `Unexpected token '@', ..."bbbbbbbb":@}" is not valid JSON`},
		{`["aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",!]`, `Unexpected token '!', ..."aaaaaaaa",!]" is not valid JSON`},
	}
	for _, c := range cases {
		v, err := decodeBody(t, "application/json", c.input)
		if err == nil {
			t.Errorf("decodeJSONBody(%q)：期望报错，实际返回 %#v", c.input, v)
			continue
		}
		if got := httperr.Message(err); got != c.want {
			t.Errorf("decodeJSONBody(%q)：\n got  %q\n want %q", c.input, got, c.want)
		}
		if got := httperr.Status(err); got != 400 {
			t.Errorf("decodeJSONBody(%q)：状态码 = %d，期望 400", c.input, got)
		}
	}

	// 长掩码输入触发上下文截取（strict 分支把整段换成 '#' 后再回填原文）。
	longCases := []struct{ input, want string }{
		{strings.Repeat("a", 50), `Unexpected token 'a', "aaaaaaaaaa"... is not valid JSON`},
		{strings.Repeat("z", 200), `Unexpected token 'z', "zzzzzzzzzz"... is not valid JSON`},
		{"  " + strings.Repeat("q", 60), `Unexpected token 'q', "  qqqqqqqqqq"... is not valid JSON`},
	}
	for _, c := range longCases {
		_, err := decodeBody(t, "application/json", c.input)
		if err == nil {
			t.Errorf("decodeJSONBody(长度 %d)：期望报错", len(c.input))
			continue
		}
		if got := httperr.Message(err); got != c.want {
			t.Errorf("decodeJSONBody(长度 %d)：\n got  %q\n want %q", len(c.input), got, c.want)
		}
	}
}

// TestDecodeJSONBodyEmptyBodyIsEmptyObject 对齐 body-parser：长度 0 的体特判为 {}，不报错。
func TestDecodeJSONBodyEmptyBodyIsEmptyObject(t *testing.T) {
	v, err := decodeBody(t, "application/json", "")
	if err != nil {
		t.Fatalf("空体不应报错，实际 %v", err)
	}
	m, ok := v.(map[string]any)
	if !ok || len(m) != 0 {
		t.Fatalf("空体应解析为 {}，实际 %#v", v)
	}
}

// TestDecodeJSONBodyValidAndNonJSON 校验合法体与「非 JSON Content-Type 不解析」两条正常路径。
func TestDecodeJSONBodyValidAndNonJSON(t *testing.T) {
	v, err := decodeBody(t, "application/json", ` {"a":1,"b":[true,null,2]} `)
	if err != nil {
		t.Fatalf("合法体不应报错：%v", err)
	}
	m, ok := v.(map[string]any)
	if !ok || m["a"] != float64(1) {
		t.Fatalf("合法体解析结果异常：%#v", v)
	}

	// Content-Type 非 application/json 时不解析，直接给空对象（对齐 express.json 的类型过滤）。
	v, err = decodeBody(t, "text/plain", "{not json")
	if err != nil {
		t.Fatalf("非 JSON Content-Type 不应解析报错：%v", err)
	}
	if m, ok := v.(map[string]any); !ok || len(m) != 0 {
		t.Fatalf("非 JSON Content-Type 应给 {}，实际 %#v", v)
	}

	// 无 Content-Type 同样不解析。
	if _, err := decodeBody(t, "", "{not json"); err != nil {
		t.Fatalf("无 Content-Type 不应解析报错：%v", err)
	}
}
