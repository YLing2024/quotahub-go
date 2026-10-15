// Package ids 生成平台 / 预设的 UUID 与日志的短随机标识。
//
// 平台 id 与预设 id 在旧实现里是 crypto.randomUUID()，这里生成等价的 UUID v4 字符串；
// 日志 id 复刻 `${Date.now().toString(36)}-${random.toString(36).slice(2,8)}` 的形态。
package ids

import (
	"crypto/rand"
	"encoding/hex"
)

const base36digits = "0123456789abcdefghijklmnopqrstuvwxyz"

// UUID 生成一个随机的 UUID v4 字符串。
func UUID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40 // version 4
	b[8] = (b[8] & 0x3f) | 0x80 // variant RFC 4122
	buf := make([]byte, 36)
	hex.Encode(buf[0:8], b[0:4])
	buf[8] = '-'
	hex.Encode(buf[9:13], b[4:6])
	buf[13] = '-'
	hex.Encode(buf[14:18], b[6:8])
	buf[18] = '-'
	hex.Encode(buf[19:23], b[8:10])
	buf[23] = '-'
	hex.Encode(buf[24:36], b[10:16])
	return string(buf)
}

// Base36 把非负整数转成小写 36 进制（对齐 Number.prototype.toString(36)）。
func Base36(n int64) string {
	if n == 0 {
		return "0"
	}
	if n < 0 {
		n = -n
	}
	var buf [16]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = base36digits[n%36]
		n /= 36
	}
	return string(buf[i:])
}

// RandomBase36 生成 n 个小写 36 进制随机字符。
func RandomBase36(n int) string {
	if n <= 0 {
		return ""
	}
	b := make([]byte, n)
	_, _ = rand.Read(b)
	out := make([]byte, n)
	for i := range b {
		out[i] = base36digits[int(b[i])%36]
	}
	return string(out)
}

// LogID 复刻旧实现的日志 id 形态：`<毫秒的 36 进制>-<6 位随机 36 进制>`。
func LogID(nowMillis int64) string {
	return Base36(nowMillis) + "-" + RandomBase36(6)
}
