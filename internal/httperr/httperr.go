// Package httperr 提供带 HTTP 状态码的业务错误。
//
// 旧实现里业务错误用 `Object.assign(new Error(msg), { status })` 表达，统一错误处理按
// `err.status ?? 400` 落状态码、`{ error: msg }` 落响应体。Go 侧用本类型等价承载：
// Handler 只需返回 error，分发层据 Status 落状态码。
package httperr

import (
	"errors"
	"fmt"
)

// Error 是带状态码的业务错误。
type Error struct {
	Status  int
	Message string
}

// Error 实现 error 接口。
func (e *Error) Error() string { return e.Message }

// New 构造带状态码的业务错误。
func New(status int, message string) *Error {
	return &Error{Status: status, Message: message}
}

// Newf 构造带状态码的业务错误（格式化消息）。
func Newf(status int, format string, args ...any) *Error {
	return &Error{Status: status, Message: fmt.Sprintf(format, args...)}
}

// Status 取错误的状态码；非本类型错误视为 400（对齐旧实现 `err.status ?? 400`）。
func Status(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Status
	}
	return 400
}

// Message 取错误的展示消息。
func Message(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
