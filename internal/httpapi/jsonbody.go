package httpapi

import (
	"strings"

	"quotahub-go/internal/v8json"
)

// strictBodyParserError 复刻 express.json()（body-parser 2.3.0）在 strict 模式下的请求体
// 语法校验：首个非空白字符不是 '{' / '[' 时返回对应报错文案，否则返回空串表示可继续解析。
//
// 为什么需要它：body-parser 在 JSON.parse 之前先看首个非空白字符，不是 '{' / '[' 就直接
// 抛错，因此 `tru`、`01`、`"abc`、`null` 这些「裸 V8 会给出位置类文案」的输入，在旧服务上
// 得到的是另一套文案。它把从该字符起的原文整段换成 '#' 后再交给 JSON.parse，再把报错里的
// '#' 片段按位置换回原文（见 node_modules/body-parser/lib/types/json.js 的
// createStrictSyntaxError）。这里照抄这套流程，用 v8json 复现底层的 V8 JSON.parse 文案。
//
// 空白只认 RFC 7159 的四个字符（空格、制表、换行、回车），与 body-parser 的 FIRST_CHAR 一致。
// 全空白输入按 idx = -1 处理：掩码串为空串，V8 报 `Unexpected end of JSON input`。
func strictBodyParserError(body string) string {
	runes := []rune(body)
	idx := -1
	for i, r := range runes {
		if r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		idx = i
		break
	}
	if idx >= 0 && (runes[idx] == '{' || runes[idx] == '[') {
		return ""
	}

	partial := ""
	if idx >= 0 {
		partial = string(runes[:idx]) + strings.Repeat("#", len(runes)-idx)
	}
	msg := v8json.ErrorMessage(partial)
	if msg == "" {
		// partial 必为非法 JSON，理论上到不了这里；保守交回空串由调用方兜底。
		return ""
	}
	return fillPlaceholders(msg, runes, idx)
}

// fillPlaceholders 复刻 body-parser 的 `message.replace(/#+/g, …)`：每一段连续的 '#' 换成
// body 中自 idx 起、长度等于该段的子串（token 字符本身也是一段 '#'，故一并被换回）。idx < 0
// 时消息里不含 '#'，原样返回。
func fillPlaceholders(msg string, runes []rune, idx int) string {
	if idx < 0 || !strings.Contains(msg, "#") {
		return msg
	}
	var b strings.Builder
	b.Grow(len(msg))
	for i := 0; i < len(msg); {
		if msg[i] != '#' {
			b.WriteByte(msg[i])
			i++
			continue
		}
		j := i
		for j < len(msg) && msg[j] == '#' {
			j++
		}
		end := idx + (j - i)
		if end > len(runes) {
			end = len(runes)
		}
		b.WriteString(string(runes[idx:end]))
		i = j
	}
	return b.String()
}
