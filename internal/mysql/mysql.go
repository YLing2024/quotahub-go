// Package mysql 收拢与 MySQL 驱动相关的连接细节。
//
// 本服务运行时只有 MySQL 一条路径（2026-10-16 口径，不再支持 SQLite）；建表与增量迁移
// 收在 internal/store，本包只负责把分段连接参数拼成 go-sql-driver/mysql 的 DSN。
package mysql

import (
	"fmt"
	"time"

	"github.com/go-sql-driver/mysql"
)

// BuildDSN 由分段参数拼出 go-sql-driver 的 DSN。口令只进返回值，绝不落日志。
//
// 参数取舍：utf8mb4 保中文/emoji；parseTime=false（时间列一律 ISO 文本，不交给驱动解析）；
// timeout 控制建连、read/writeTimeout 防止慢查询挂死调度器。
func BuildDSN(host string, port int, name, user, password string) string {
	cfg := mysql.NewConfig()
	cfg.User = user
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", host, port)
	cfg.DBName = name
	cfg.ParseTime = false
	cfg.Collation = "utf8mb4_unicode_ci"
	cfg.Loc = time.Local
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 15 * time.Second
	cfg.WriteTimeout = 15 * time.Second
	return cfg.FormatDSN()
}
