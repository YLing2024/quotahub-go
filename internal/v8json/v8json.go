// Package v8json 复刻 V8（Node 的 JSON.parse）在语法错误时的报错文案。
//
// 旧实现用 Express 的 body-parser 解析请求体，底层是 V8 的 JSON.parse；本项目改用
// encoding/json，其错误文案与 V8 不一致。为了保持 HTTP 契约的报错逐字节对齐，本包按
// V8 的 json-parser.cc 语义重新扫描输入，产出完全相同的 message（含 position 与
// line/column），供请求体解析失败时使用。
//
// 合法输入返回空串；只有确实非法时才需要调用，以避免对合法 JSON 的重复扫描。
// 说明：位置按 UTF-16 码元口径计算；对纯 ASCII 输入与 V8 完全一致，含增补平面字符的
// 输入可能与 V8 相差码元数，属已知边界。
package v8json

import "fmt"

// endOfString 是 V8 中 CurrentCharacter() 越界时返回的 kEndOfString 哨兵。
const endOfString rune = -1

// token 对应 V8 JsonToken，仅保留报错路径需要的取值。
type token int

const (
	tokenIllegal token = iota
	tokenEOS
	tokenWhitespace
	tokenLBrace
	tokenRBrace
	tokenLBrack
	tokenRBrack
	tokenColon
	tokenComma
	tokenString
	tokenNumber
	tokenTrue
	tokenFalse
	tokenNull
)

// V8 message-template.h 中的 JSON 报错模板（占位符依次为 position/line/column）。
const (
	msgUnexpectedEOS                  = "Unexpected end of JSON input"
	msgNumberFmt                      = "Unexpected number in JSON at position %d (line %d column %d)"
	msgStringFmt                      = "Unexpected string in JSON at position %d (line %d column %d)"
	msgUnterminatedStringFmt          = "Unterminated string in JSON at position %d (line %d column %d)"
	msgExpectedPropNameOrRBraceFmt    = "Expected property name or '}' in JSON at position %d (line %d column %d)"
	msgExpectedCommaOrRBrackFmt       = "Expected ',' or ']' after array element in JSON at position %d (line %d column %d)"
	msgExpectedCommaOrRBraceFmt       = "Expected ',' or '}' after property value in JSON at position %d (line %d column %d)"
	msgExpectedColonFmt               = "Expected ':' after property name in JSON at position %d (line %d column %d)"
	msgExpectedDoubleQuotedNameFmt    = "Expected double-quoted property name in JSON at position %d (line %d column %d)"
	msgUnterminatedFractionalFmt      = "Unterminated fractional number in JSON at position %d (line %d column %d)"
	msgExponentPartMissingNumberFmt   = "Exponent part is missing a number in JSON at position %d (line %d column %d)"
	msgNonWhitespaceAfterJSONFmt      = "Unexpected non-whitespace character after JSON at position %d (line %d column %d)"
	msgBadEscapedCharacterFmt         = "Bad escaped character in JSON at position %d (line %d column %d)"
	msgBadControlCharacterFmt         = "Bad control character in string literal in JSON at position %d (line %d column %d)"
	msgBadUnicodeEscapeFmt            = "Bad Unicode escape in JSON at position %d (line %d column %d)"
	msgNoNumberAfterMinusSignFmt      = "No number after minus sign in JSON at position %d (line %d column %d)"
	msgShortString                    = `"%s" is not valid JSON`
	msgUnexpectedTokenShortString     = `Unexpected token '%c', "%s" is not valid JSON`
	msgUnexpectedTokenStartContext    = `Unexpected token '%c', "%s"... is not valid JSON`
	msgUnexpectedTokenSurroundContext = `Unexpected token '%c', ..."%s"... is not valid JSON`
	msgUnexpectedTokenEndContext      = `Unexpected token '%c', ..."%s" is not valid JSON`
)

// V8 上下文截取参数（json-parser.h）。
const (
	maxContextCharacters      = 10
	minSourceLengthForContext = maxContextCharacters*2 + 1
)

// ErrorMessage 在 s 不是合法 JSON 时返回 V8 JSON.parse 的报错原文，合法时返回空串。
func ErrorMessage(s string) string {
	p := &parser{src: []rune(s), raw: s}
	if !p.parse() {
		return p.msg
	}
	return ""
}

// parser 承载一次扫描的可变状态。
type parser struct {
	src    []rune
	raw    string
	cursor int
	next   token
	msg    string
}

func (p *parser) position() int { return p.cursor }

func (p *parser) atEnd() bool { return p.cursor >= len(p.src) }

func (p *parser) current() rune {
	if p.atEnd() {
		return endOfString
	}
	return p.src[p.cursor]
}

// nextChar 对应 V8 的 NextCharacter()：先前进再读取。
func (p *parser) nextChar() rune {
	p.cursor++
	return p.current()
}

func (p *parser) advance() { p.cursor++ }

// parse 对应 V8 ParseJson：解析顶层值，再校验尾部只剩空白。
func (p *parser) parse() bool {
	if !p.parseValue() {
		return false
	}
	return p.expectNext(tokenEOS, msgNonWhitespaceAfterJSONFmt)
}

func (p *parser) parseValue() bool {
	p.scanToken()
	switch p.next {
	case tokenNumber:
		return p.parseNumber()
	case tokenString:
		p.advance() // 进入字符串（跳过开引号）
		return p.scanString()
	case tokenTrue:
		return p.scanLiteral("true")
	case tokenFalse:
		return p.scanLiteral("false")
	case tokenNull:
		return p.scanLiteral("null")
	case tokenLBrace:
		return p.parseObject()
	case tokenLBrack:
		return p.parseArray()
	default:
		p.reportUnexpectedCharacter(p.current())
		return false
	}
}

// --- 对象 / 数组 ---

func (p *parser) parseObject() bool {
	p.advance() // 跳过 '{'
	if p.check(tokenRBrace) {
		return true
	}
	firstMsg := msgExpectedPropNameOrRBraceFmt
	for {
		if !p.expectNext(tokenString, firstMsg) {
			return false
		}
		firstMsg = msgExpectedDoubleQuotedNameFmt
		if !p.scanString() {
			return false
		}
		if !p.expectNext(tokenColon, msgExpectedColonFmt) {
			return false
		}
		if !p.parseValue() {
			return false
		}
		if !p.check(tokenComma) {
			break
		}
	}
	return p.expect(tokenRBrace, msgExpectedCommaOrRBraceFmt)
}

func (p *parser) parseArray() bool {
	p.advance() // 跳过 '['
	if p.check(tokenRBrack) {
		return true
	}
	// 复刻 V8 对纯数字数组的快路径：它决定了 `[1,]`、`[1 2]` 等形态的报错点。
	for p.next == tokenNumber {
		if !p.parseNumber() {
			return false
		}
		if p.check(tokenComma) {
			p.scanToken()
			continue
		}
		return p.expect(tokenRBrack, msgExpectedCommaOrRBrackFmt)
	}
	if !p.parseValue() {
		return false
	}
	for p.check(tokenComma) {
		if !p.parseValue() {
			return false
		}
	}
	return p.expect(tokenRBrack, msgExpectedCommaOrRBrackFmt)
}

// --- 字符串 ---

func (p *parser) scanString() bool {
	for {
		for p.cursor < len(p.src) && !mayTerminateString(p.src[p.cursor]) {
			p.cursor++
		}
		if p.atEnd() {
			p.report(tokenIllegal, msgUnterminatedStringFmt)
			return false
		}
		switch p.src[p.cursor] {
		case '"':
			p.advance()
			return true
		case '\\':
			c := p.nextChar()
			if c == endOfString || c > 0xff {
				p.reportUnexpectedCharacter(c)
				return false
			}
			switch escapeKind(c) {
			case escapeSelf, escapeBackspace, escapeTab, escapeNewLine, escapeFormFeed, escapeCarriageReturn:
				// 合法转义，继续。
			case escapeUnicode:
				if !p.scanUnicode() {
					return false
				}
			default:
				p.report(tokenIllegal, msgBadEscapedCharacterFmt)
				return false
			}
			p.advance()
		default:
			p.report(tokenIllegal, msgBadControlCharacterFmt)
			return false
		}
	}
}

// scanUnicode 对应 V8 ScanUnicodeCharacter：连续读 4 个十六进制位。
func (p *parser) scanUnicode() bool {
	for i := 0; i < 4; i++ {
		if hexValue(p.nextChar()) < 0 {
			p.report(tokenIllegal, msgBadUnicodeEscapeFmt)
			return false
		}
	}
	return true
}

type escape int

const (
	escapeIllegal escape = iota
	escapeSelf
	escapeBackspace
	escapeTab
	escapeNewLine
	escapeFormFeed
	escapeCarriageReturn
	escapeUnicode
)

func escapeKind(c rune) escape {
	switch c {
	case 'b':
		return escapeBackspace
	case 't':
		return escapeTab
	case 'n':
		return escapeNewLine
	case 'f':
		return escapeFormFeed
	case 'r':
		return escapeCarriageReturn
	case 'u':
		return escapeUnicode
	case '"', '\\', '/':
		return escapeSelf
	default:
		return escapeIllegal
	}
}

func mayTerminateString(c rune) bool { return c < 0x20 || c == '"' || c == '\\' }

// --- 数字 ---

func (p *parser) parseNumber() bool {
	sign := 1
	c := p.current()
	if c == '-' {
		sign = -1
		c = p.nextChar()
	}

	if c == '0' {
		c = p.nextChar()
		if isLatin1(c) && isNumberPart(c) {
			if isDecimalDigit(c) {
				p.report(tokenNumber, "")
				return false
			}
		} else if sign > 0 {
			// 单个 0，数字到此结束。
			return true
		}
	} else {
		start := p.cursor
		stop := p.cursor + 9
		if stop > len(p.src) {
			stop = len(p.src)
		}
		for p.cursor < stop && isDecimalDigit(p.current()) {
			p.cursor++
		}
		if start == p.cursor {
			p.report(tokenIllegal, msgNoNumberAfterMinusSignFmt)
			return false
		}
		c = p.current()
		if !isLatin1(c) || !isNumberPart(c) {
			return true // 普通整数
		}
		p.advanceToNonDecimal()
	}

	if p.current() == '.' {
		c = p.nextChar()
		if !isDecimalDigit(c) {
			p.report(tokenIllegal, msgUnterminatedFractionalFmt)
			return false
		}
		p.advanceToNonDecimal()
	}

	if asciiLower(p.current()) == 'e' {
		c = p.nextChar()
		if c == '-' || c == '+' {
			c = p.nextChar()
		}
		if !isDecimalDigit(c) {
			p.report(tokenIllegal, msgExponentPartMissingNumberFmt)
			return false
		}
		p.advanceToNonDecimal()
	}
	return true
}

func (p *parser) advanceToNonDecimal() {
	for p.cursor < len(p.src) && isDecimalDigit(p.src[p.cursor]) {
		p.cursor++
	}
}

// --- 字面量 ---

// scanLiteral 复刻 V8 ScanLiteral：首字符已由 token 匹配，这里只逐位校验其余字符。
func (p *parser) scanLiteral(lit string) bool {
	remaining := len(p.src) - p.cursor
	rest := lit[1:]
	if remaining >= len(lit) && equalASCII(p.src[p.cursor+1:p.cursor+1+len(rest)], rest) {
		p.cursor += len(lit)
		return true
	}
	p.advance()
	limit := len(rest)
	if remaining-1 < limit {
		limit = remaining - 1
	}
	for i := 0; i < limit; i++ {
		if p.current() != rune(rest[i]) {
			p.reportUnexpectedCharacter(p.current())
			return false
		}
		p.advance()
	}
	p.report(tokenEOS, "")
	return false
}

// --- token 状态机（对应 V8 GetNextNonWhitespaceToken / Check / Expect / ExpectNext）---

func (p *parser) scanToken() {
	next := tokenEOS
	for p.cursor < len(p.src) {
		cur := tokenFor(p.src[p.cursor])
		if cur != tokenWhitespace {
			next = cur
			break
		}
		p.cursor++
	}
	p.next = next
}

func (p *parser) isNextToken(t token) bool {
	switch t {
	case tokenEOS:
		return p.atEnd()
	case tokenLBrace, tokenRBrace, tokenLBrack, tokenRBrack, tokenColon, tokenComma:
		return charOf(t) == p.current()
	default:
		// STRING / NUMBER / 字面量不是单字符 token，必须先跳过空白再看 peek()。
		return false
	}
}

func (p *parser) check(t token) bool {
	if p.isNextToken(t) {
		p.advance()
		return true
	}
	p.scanToken()
	if p.next == t {
		p.advance()
		return true
	}
	return false
}

func (p *parser) expect(t token, msg string) bool {
	if p.next == t {
		p.advance()
		return true
	}
	p.report(p.next, msg)
	return false
}

func (p *parser) expectNext(t token, msg string) bool {
	if p.check(t) {
		return true
	}
	p.report(p.next, msg)
	return false
}

// --- 报错 ---

// report 对应 V8 ReportUnexpectedToken：msgTpl 非空表示按指定模板报错，否则按 token 选文案。
func (p *parser) report(t token, msgTpl string) {
	if p.msg != "" {
		return
	}
	pos := p.position()
	if msgTpl != "" {
		p.msg = withLocation(msgTpl, pos, p.src)
		return
	}
	switch t {
	case tokenEOS:
		p.msg = msgUnexpectedEOS
	case tokenNumber:
		p.msg = withLocation(msgNumberFmt, pos, p.src)
	case tokenString:
		p.msg = withLocation(msgStringFmt, pos, p.src)
	default:
		if isSpecialString(p.raw) {
			p.msg = fmt.Sprintf(msgShortString, p.raw)
			return
		}
		p.msg = unexpectedTokenMessage(p.raw, pos)
	}
}

func (p *parser) reportUnexpectedCharacter(c rune) {
	if c == endOfString {
		p.report(tokenEOS, "")
		return
	}
	p.report(tokenFor(c), "")
}

// withLocation 计算模板位置参数（position 与 line/column）。
func withLocation(msgTpl string, pos int, src []rune) string {
	line, column := fileLocation(src, pos)
	return fmt.Sprintf(msgTpl, pos, line, column)
}

// fileLocation 复刻 V8 CalculateFileLocation：\r\n 记一个换行。
func fileLocation(src []rune, pos int) (int, int) {
	line := 1
	lastLineBreak := 0
	for i := 0; i < pos && i < len(src); i++ {
		c := src[i]
		if c == '\r' && i < pos-1 && src[i+1] == '\n' {
			i++
		}
		if src[i] == '\r' || src[i] == '\n' {
			line++
			lastLineBreak = i + 1
		}
	}
	return line, 1 + (pos - lastLineBreak)
}

// unexpectedTokenMessage 复刻 V8 GetErrorMessageWithEllipses：按输入长度与位置决定上下文截取。
func unexpectedTokenMessage(raw string, pos int) string {
	runes := []rune(raw)
	bad := rune(0)
	if pos < len(runes) {
		bad = runes[pos]
	}
	if len(runes) < minSourceLengthForContext {
		return fmt.Sprintf(msgUnexpectedTokenShortString, bad, raw)
	}
	switch {
	case pos < maxContextCharacters:
		end := pos + maxContextCharacters
		if end > len(runes) {
			end = len(runes)
		}
		return fmt.Sprintf(msgUnexpectedTokenStartContext, bad, string(runes[:end]))
	case pos < len(runes)-maxContextCharacters:
		return fmt.Sprintf(msgUnexpectedTokenSurroundContext, bad,
			string(runes[pos-maxContextCharacters:pos+maxContextCharacters]))
	default:
		start := pos - maxContextCharacters
		if start < 0 {
			start = 0
		}
		return fmt.Sprintf(msgUnexpectedTokenEndContext, bad, string(runes[start:]))
	}
}

// isSpecialString 复刻 V8 IsSpecialString：整串恰为这些值时不带 token 报错。
func isSpecialString(raw string) bool {
	switch raw {
	case "undefined", "NaN", "Infinity", "[object Object]":
		return true
	default:
		return false
	}
}

// --- 字符判定 ---

func tokenFor(c rune) token {
	if c < 0 || c > 0xff {
		return tokenIllegal
	}
	switch {
	case c == '"':
		return tokenString
	case isDecimalDigit(c):
		return tokenNumber
	case c == '-':
		return tokenNumber
	case c == '[':
		return tokenLBrack
	case c == '{':
		return tokenLBrace
	case c == ']':
		return tokenRBrack
	case c == '}':
		return tokenRBrace
	case c == 't':
		return tokenTrue
	case c == 'f':
		return tokenFalse
	case c == 'n':
		return tokenNull
	case c == ' ' || c == '\t' || c == '\r' || c == '\n':
		return tokenWhitespace
	case c == ':':
		return tokenColon
	case c == ',':
		return tokenComma
	default:
		return tokenIllegal
	}
}

// charOf 返回单字符 token 对应的字符，仅用于 isNextToken。
func charOf(t token) rune {
	switch t {
	case tokenLBrace:
		return '{'
	case tokenRBrace:
		return '}'
	case tokenLBrack:
		return '['
	case tokenRBrack:
		return ']'
	case tokenColon:
		return ':'
	case tokenComma:
		return ','
	default:
		return endOfString
	}
}

func isDecimalDigit(c rune) bool { return c >= '0' && c <= '9' }

func isLatin1(c rune) bool { return c >= 0 && c <= 0xff }

func isNumberPart(c rune) bool {
	return c == '.' || c == 'e' || c == 'E' || isDecimalDigit(c) || c == '-' || c == '+'
}

func hexValue(c rune) int {
	switch {
	case c >= '0' && c <= '9':
		return int(c - '0')
	case c >= 'a' && c <= 'f':
		return int(c-'a') + 10
	case c >= 'A' && c <= 'F':
		return int(c-'A') + 10
	default:
		return -1
	}
}

func asciiLower(c rune) rune {
	if c >= 'A' && c <= 'Z' {
		return c + ('a' - 'A')
	}
	return c
}

func equalASCII(got []rune, want string) bool {
	if len(got) != len(want) {
		return false
	}
	for i := range got {
		if got[i] != rune(want[i]) {
			return false
		}
	}
	return true
}
