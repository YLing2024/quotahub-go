// Package auth 提供 builtin 认证（口令哈希、账号/会话、登录限速、cookie 读写）与
// builtin/sso 双模式鉴权中间件。
//
// 口令哈希与旧实现 services/authService.ts 完全一致：`scrypt$<saltHex>$<hashHex>`，
// salt 16 字节、keyLen 64、N=16384/r=8/p=1（Node crypto.scryptSync 默认档）。
package auth

import (
	"crypto/rand"
	"crypto/subtle"
	"encoding/hex"
	"strings"

	"quotahub-go/internal/scrypt"
)

// scryptKeyLen 是与旧实现一致的派生密钥长度。
const scryptKeyLen = 64

// dummyHash 用于账号不存在时也跑一次 scrypt，避免用响应时间枚举账号。
var dummyHash = "scrypt$" + strings.Repeat("0", 32) + "$" + strings.Repeat("0", scryptKeyLen*2)

// HashPassword 生成 `scrypt$saltHex$hashHex` 形态的口令哈希。
func HashPassword(password string) (string, error) {
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return "", err
	}
	hash, err := scrypt.Key([]byte(password), salt, scrypt.DefaultN, scrypt.DefaultR, scrypt.DefaultP, scryptKeyLen)
	if err != nil {
		return "", err
	}
	return "scrypt$" + hex.EncodeToString(salt) + "$" + hex.EncodeToString(hash), nil
}

// VerifyPassword 常量时间比对口令；格式非法一律返回 false。
func VerifyPassword(password, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 3 || parts[0] != "scrypt" {
		return false
	}
	salt, err := hex.DecodeString(parts[1])
	if err != nil || len(salt) == 0 {
		return false
	}
	expected, err := hex.DecodeString(parts[2])
	if err != nil || len(expected) == 0 {
		return false
	}
	actual, err := scrypt.Key([]byte(password), salt, scrypt.DefaultN, scrypt.DefaultR, scrypt.DefaultP, len(expected))
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare(actual, expected) == 1
}

// verifyDummy 对不存在账号跑一次等价开销的校验（忽略结果）。
func verifyDummy(password string) {
	_ = VerifyPassword(password, dummyHash)
}
