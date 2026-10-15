// Package quotahub 承载嵌入式前端产物，供二进制自带前端形态使用。
package quotahub

import (
	"embed"
	"io/fs"
)

// distFS 嵌入前端构建产物；以 all: 前缀包含点文件与下划线开头的文件。
//
//go:embed all:web/dist
var distFS embed.FS

// DistFS 返回定位到 web/dist 根的文件系统；定位失败时退回原 FS（调用方按不存在处理）。
func DistFS() fs.FS {
	sub, err := fs.Sub(distFS, "web/dist")
	if err != nil {
		return distFS
	}
	return sub
}
