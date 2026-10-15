// Package config 负责 quotahub-go 的环境变量解析与校验。
//
// 口径逐项对齐旧实现 server/src/config.ts：键名、默认值、取值域、非法 AUTH_MODE 回落
// builtin、沙箱超时钳制到 100~30000ms、会话 TTL 上限 720 小时。差异只有新增的数据库选择键
// （DB_DRIVER 等，见 docs/DB.md）。本包只读环境变量，不碰任何文件。
package config

import (
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

// AuthMode 是管理面认证模式。
type AuthMode string

const (
	// AuthBuiltin 自带账号 + 登录页 + cookie 会话（默认）。
	AuthBuiltin AuthMode = "builtin"
	// AuthSSO 关掉自带口令，身份只看前置网关注入的 X-Auth-User。
	AuthSSO AuthMode = "sso"
)

// DBDriver 是数据库方言。
type DBDriver string

const (
	// DBDriverSQLite 纯 Go SQLite（默认，零额外配置）。
	DBDriverSQLite DBDriver = "sqlite"
	// DBDriverMySQL 需 DB_HOST/DB_PORT/DB_NAME/DB_USER/DB_PASSWORD 或一条 DB_DSN。
	DBDriverMySQL DBDriver = "mysql"
)

// Config 是运行时配置快照；时间字段为秒/毫秒整数。
type Config struct {
	Host    string
	Port    int
	DataDir string
	// StaticDir 为静态前端目录覆盖；为空表示使用二进制内嵌产物。
	StaticDir string
	// HasStaticDir 表示 StaticDir 存在且可用。
	HasStaticDir bool

	ScriptTimeoutMs int
	AllowPrivate    bool
	AuthMode        AuthMode
	AdminUser       string
	// AdminPassword 为空表示首启随机生成并只打印一次。
	AdminPassword string
	// SessionTtlSeconds 为 builtin 会话有效期（秒）。
	SessionTtlSeconds int

	// 数据库选择与连接参数（本次新增；默认 sqlite）。
	DBDriver   DBDriver
	DBPath     string
	DBHost     string
	DBPort     int
	DBName     string
	DBUser     string
	DBPassword string
	DBDSN      string
}

// ConfigError 配置错误，Message 可直接展示给运维（不含敏感值）。
type ConfigError struct{ Message string }

// Error 实现 error 接口。
func (e *ConfigError) Error() string { return e.Message }

// LoadEnv 用进程环境变量解析配置。
func LoadEnv() (*Config, error) { return Load(os.Getenv) }

// Load 从环境变量解析配置。get 便于测试注入（返回空串视为未设置）。
func Load(get func(string) string) (*Config, error) {
	port, err := pickInt(get("PORT"), "PORT", 3000, 1, 65535)
	if err != nil {
		return nil, err
	}
	host := trimOr(get("HOST"), "127.0.0.1")

	dataDir := strings.TrimSpace(get("QUOTAHUB_DATA_DIR"))
	if dataDir == "" {
		dataDir = "./data"
	}

	staticDir := strings.TrimSpace(get("QUOTAHUB_STATIC_DIR"))
	hasStatic := false
	if staticDir != "" {
		if info, statErr := os.Stat(staticDir); statErr == nil && info.IsDir() {
			hasStatic = true
		}
	}

	authMode := AuthBuiltin
	if strings.TrimSpace(get("AUTH_MODE")) == "sso" {
		authMode = AuthSSO
	}

	adminUser := strings.TrimSpace(get("QUOTAHUB_ADMIN_USER"))
	if adminUser == "" {
		adminUser = "admin"
	}

	db, err := loadDB(get, dataDir)
	if err != nil {
		return nil, err
	}

	return &Config{
		Host:              host,
		Port:              port,
		DataDir:           dataDir,
		StaticDir:         staticDir,
		HasStaticDir:      hasStatic,
		ScriptTimeoutMs:   pickScriptTimeout(get("QUOTAHUB_SCRIPT_TIMEOUT_MS")),
		AllowPrivate:      get("QUOTAHUB_ALLOW_PRIVATE") == "1",
		AuthMode:          authMode,
		AdminUser:         adminUser,
		AdminPassword:     get("QUOTAHUB_ADMIN_PASSWORD"),
		SessionTtlSeconds: pickSessionTTL(get("QUOTAHUB_SESSION_TTL_HOURS")),
		DBDriver:          db.driver,
		DBPath:            db.path,
		DBHost:            db.host,
		DBPort:            db.port,
		DBName:            db.name,
		DBUser:            db.user,
		DBPassword:        db.password,
		DBDSN:             db.dsn,
	}, nil
}

// MonitorDBPath 返回 SQLite 库文件路径（默认 <数据目录>/monitor.db）。
func (c *Config) MonitorDBPath() string {
	if c.DBPath != "" {
		return c.DBPath
	}
	return filepath.Join(c.DataDir, "monitor.db")
}

type dbOptions struct {
	driver   DBDriver
	path     string
	host     string
	port     int
	name     string
	user     string
	password string
	dsn      string
}

// loadDB 解析数据库选择。默认 sqlite；只有 DB_DRIVER=mysql 时才读取 MySQL 键
// （sqlite 分支不加载驱动、不建连接、不受影响）。
func loadDB(get func(string) string, dataDir string) (dbOptions, error) {
	raw := strings.ToLower(strings.TrimSpace(get("DB_DRIVER")))
	switch raw {
	case "", "sqlite":
		path := strings.TrimSpace(get("DB_PATH"))
		if path == "" {
			path = filepath.Join(dataDir, "monitor.db")
		}
		return dbOptions{driver: DBDriverSQLite, path: path}, nil
	case "mysql":
		dsn := strings.TrimSpace(get("DB_DSN"))
		if dsn != "" {
			return dbOptions{driver: DBDriverMySQL, dsn: dsn}, nil
		}
		host := strings.TrimSpace(get("DB_HOST"))
		name := strings.TrimSpace(get("DB_NAME"))
		user := strings.TrimSpace(get("DB_USER"))
		if host == "" || name == "" || user == "" {
			return dbOptions{}, &ConfigError{Message: "DB_DRIVER=mysql 需要 DB_HOST/DB_NAME/DB_USER（或一条 DB_DSN）"}
		}
		port, err := pickInt(get("DB_PORT"), "DB_PORT", 3306, 1, 65535)
		if err != nil {
			return dbOptions{}, err
		}
		return dbOptions{
			driver:   DBDriverMySQL,
			host:     host,
			port:     port,
			name:     name,
			user:     user,
			password: get("DB_PASSWORD"),
		}, nil
	default:
		return dbOptions{}, &ConfigError{Message: fmt.Sprintf("DB_DRIVER 只能是 sqlite / mysql，当前为「%s」", raw)}
	}
}

// pickScriptTimeout 复刻旧 config.ts 的 QUOTAHUB_SCRIPT_TIMEOUT_MS 语义：
// 有限且 >0 时 floor 后钳制到 [100,30000]；否则回落 2000。
func pickScriptTimeout(raw string) int {
	n, ok := parseFloat(raw)
	if !ok || !(n > 0) {
		return 2000
	}
	n = math.Floor(n)
	if n < 100 {
		return 100
	}
	if n > 30000 {
		return 30000
	}
	return int(n)
}

// pickSessionTTL 复刻旧 config.ts 的 QUOTAHUB_SESSION_TTL_HOURS 语义：
// 有限且 >0 时取 min(小时, 720)，再换算为秒（四舍五入）；否则回落 12 小时。
func pickSessionTTL(raw string) int {
	n, ok := parseFloat(raw)
	if !ok || !(n > 0) {
		n = 12
	}
	if n > 720 {
		n = 720
	}
	return int(math.Round(n * 3600))
}

// parseFloat 解析十进制浮点；空串或非法返回 false（复刻 JS Number() 的严格口径）。
func parseFloat(raw string) (float64, bool) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return 0, false
	}
	f, err := strconv.ParseFloat(raw, 64)
	if err != nil {
		return 0, false
	}
	return f, true
}

// pickInt 读取十进制整数；未设置用默认值，取值域外或非整数直接报错。
func pickInt(raw, name string, fallback, min, max int) (int, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return fallback, nil
	}
	value, err := strconv.Atoi(raw)
	if err != nil || value < min || value > max {
		return 0, &ConfigError{Message: fmt.Sprintf("环境变量 %s 必须是 %d–%d 之间的整数，当前为「%s」", name, min, max, raw)}
	}
	return value, nil
}

func trimOr(raw, fallback string) string {
	if v := strings.TrimSpace(raw); v != "" {
		return v
	}
	return fallback
}

// Describe 生成启动摘要（不含任何敏感值）。
func (c *Config) Describe() string {
	dbDesc := string(c.DBDriver)
	if c.DBDriver == DBDriverSQLite {
		dbDesc += " " + c.MonitorDBPath()
	} else if c.DBName != "" {
		dbDesc += " " + c.DBName
	}
	return fmt.Sprintf("监听 http://%s:%d | 认证模式 %s | 数据库 %s", c.Host, c.Port, c.AuthMode, dbDesc)
}
