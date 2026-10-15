package v8json

import "testing"

// TestErrorMessageMatchesV8 用真实 V8（Node）采集的报错原文做逐字节对拍。
//
// 这里测的是**裸 `JSON.parse`** 的原生文案（`ErrorMessage` 的单元口径）；旧服务的 HTTP 文案
// 还会先过 body-parser 的 strict 首字符校验，那层由 internal/httpapi/jsonbody_test.go 对拍，
// 两层的整体说明与完整对照表见 docs/COMPAT-JSON-ERRORS.md。
func TestErrorMessageMatchesV8(t *testing.T) {
	cases := []struct {
		input string
		want  string
	}{
		// 验收要求的常见形态。
		{input: "{not json", want: "Expected property name or '}' in JSON at position 1 (line 1 column 2)"},
		{input: "{\"a\":}", want: "Unexpected token '}', \"{\"a\":}\" is not valid JSON"},
		{input: "{\"a\" 1}", want: "Expected ':' after property name in JSON at position 5 (line 1 column 6)"},
		{input: "{\"a\":1,}", want: "Expected double-quoted property name in JSON at position 7 (line 1 column 8)"},
		{input: "{\"a\":\"x}", want: "Unterminated string in JSON at position 8 (line 1 column 9)"},
		{input: "{\"a\":01}", want: "Unexpected number in JSON at position 6 (line 1 column 7)"},
		{input: "[1,]", want: "Unexpected token ']', \"[1,]\" is not valid JSON"},
		{input: "", want: "Unexpected end of JSON input"},
		{input: "{", want: "Expected property name or '}' in JSON at position 1 (line 1 column 2)"},
		{input: "tru", want: "Unexpected end of JSON input"},
		{input: "{\"a\":1} trailing", want: "Unexpected non-whitespace character after JSON at position 8 (line 1 column 9)"},

		// 更多实测形态。
		{input: "}", want: "Unexpected token '}', \"}\" is not valid JSON"},
		{input: "]", want: "Unexpected token ']', \"]\" is not valid JSON"},
		{input: ",", want: "Unexpected token ',', \",\" is not valid JSON"},
		{input: ":", want: "Unexpected token ':', \":\" is not valid JSON"},
		{input: "{,}", want: "Expected property name or '}' in JSON at position 1 (line 1 column 2)"},
		{input: "{]", want: "Expected property name or '}' in JSON at position 1 (line 1 column 2)"},
		{input: "[}", want: "Unexpected token '}', \"[}\" is not valid JSON"},
		{input: "[,]", want: "Unexpected token ',', \"[,]\" is not valid JSON"},
		{input: "[1,,2]", want: "Unexpected token ',', \"[1,,2]\" is not valid JSON"},
		{input: "{\"a\"", want: "Expected ':' after property name in JSON at position 4 (line 1 column 5)"},
		{input: "{\"a\":", want: "Unexpected end of JSON input"},
		{input: "{\"a\"}", want: "Expected ':' after property name in JSON at position 4 (line 1 column 5)"},
		{input: "{\"a\":1 \"b\":2}", want: "Expected ',' or '}' after property value in JSON at position 7 (line 1 column 8)"},
		{input: "{\"a\":1", want: "Expected ',' or '}' after property value in JSON at position 6 (line 1 column 7)"},
		{input: "{} x", want: "Unexpected non-whitespace character after JSON at position 3 (line 1 column 4)"},
		{input: "[] x", want: "Unexpected non-whitespace character after JSON at position 3 (line 1 column 4)"},
		{input: "true x", want: "Unexpected non-whitespace character after JSON at position 5 (line 1 column 6)"},
		{input: "{\"a\":1}\n{\"b\":2}", want: "Unexpected non-whitespace character after JSON at position 8 (line 2 column 1)"},
		{input: "[1 2]", want: "Expected ',' or ']' after array element in JSON at position 3 (line 1 column 4)"},
		{input: "[01]", want: "Unexpected number in JSON at position 2 (line 1 column 3)"},
		{input: "01", want: "Unexpected number in JSON at position 1 (line 1 column 2)"},
		{input: "-", want: "No number after minus sign in JSON at position 1 (line 1 column 2)"},
		{input: "00", want: "Unexpected number in JSON at position 1 (line 1 column 2)"},
		{input: "1.", want: "Unterminated fractional number in JSON at position 2 (line 1 column 3)"},
		{input: "1e", want: "Exponent part is missing a number in JSON at position 2 (line 1 column 3)"},
		{input: "1e+", want: "Exponent part is missing a number in JSON at position 3 (line 1 column 4)"},
		{input: "{\"a\":1.}", want: "Unterminated fractional number in JSON at position 7 (line 1 column 8)"},
		{input: "{\"a\":-}", want: "No number after minus sign in JSON at position 6 (line 1 column 7)"},
		{input: "\"abc", want: "Unterminated string in JSON at position 4 (line 1 column 5)"},
		{input: "\"a\\", want: "Unexpected end of JSON input"},
		{input: "\"a\\x\"", want: "Bad escaped character in JSON at position 3 (line 1 column 4)"},
		{input: "{\"a\":\"\\q\"}", want: "Bad escaped character in JSON at position 7 (line 1 column 8)"},
		{input: "{\"a\":\"\\uZZZZ\"}", want: "Bad Unicode escape in JSON at position 8 (line 1 column 9)"},
		{input: "{\"a\":\"\\u12\"}", want: "Bad Unicode escape in JSON at position 10 (line 1 column 11)"},
		{input: "undefined", want: "\"undefined\" is not valid JSON"},
		{input: "NaN", want: "\"NaN\" is not valid JSON"},
		{input: "Infinity", want: "\"Infinity\" is not valid JSON"},
		{input: "nul", want: "Unexpected end of JSON input"},
		{input: "tru}", want: "Unexpected token '}', \"tru}\" is not valid JSON"},
		{input: "[tru]", want: "Unexpected token ']', \"[tru]\" is not valid JSON"},
		{input: "{\"a\":tru}", want: "Unexpected token '}', \"{\"a\":tru}\" is not valid JSON"},
		{input: "{\"a\":undefined}", want: "Unexpected token 'u', \"{\"a\":undefined}\" is not valid JSON"},
		{input: "TRUE", want: "Unexpected token 'T', \"TRUE\" is not valid JSON"},
		{input: "1x", want: "Unexpected non-whitespace character after JSON at position 1 (line 1 column 2)"},
		{input: "0x1", want: "Unexpected non-whitespace character after JSON at position 1 (line 1 column 2)"},
		{input: "{\"a\":1}extra", want: "Unexpected non-whitespace character after JSON at position 7 (line 1 column 8)"},

		// 长输入触发上下文截取（起点/环绕/结尾三种）。
		{input: "[1,2,3,4,5,6,7,8,9,10,11,x]", want: "Unexpected token 'x', ...\"8,9,10,11,x]\" is not valid JSON"},
		{input: "{\"aaaaaaaaaaaaaa\":1,\"bbbbbbbbbbbbbb\":@}", want: "Unexpected token '@', ...\"bbbbbbbb\":@}\" is not valid JSON"},
		{input: "[\"aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa\",!]", want: "Unexpected token '!', ...\"aaaaaaaa\",!]\" is not valid JSON"},
	}
	for _, c := range cases {
		if got := ErrorMessage(c.input); got != c.want {
			t.Errorf("ErrorMessage(%q):\n got  %q\n want %q", c.input, got, c.want)
		}
	}
}

// TestValidInputsHaveNoError 确认合法 JSON 不会被误判（返回空串交回 encoding/json 解码）。
func TestValidInputsHaveNoError(t *testing.T) {
	valid := []string{
		"null", "true", "false", "0", "-0", "1", "1.5", "1e3", "-1.5e-3", "1E+2",
		`""`, `"a"`, `"\u00e9"`, `"a\"b\\c\/d"`, `"line\nbreak"`,
		"[]", "{}", "[1,2,3]", `{"a":1,"b":[true,null,"x"]}`, `{"a":{"b":{"c":1}}}`,
		`{"nested":{"arr":[1,2,{"k":"v"}]}}`,
	}
	for _, s := range valid {
		if got := ErrorMessage(s); got != "" {
			t.Errorf("ErrorMessage(%q) = %q，期望为空（合法）", s, got)
		}
	}
}
