// Command migrate-sqlite-to-mysql 把旧版 QuotaHub 的 SQLite 库一次性搬到 MySQL。
//
// 独立交付物，不进服务运行时、不被 cmd/quotahub-go 引用：它自带 go.mod，是唯一允许持有
// SQLite 驱动的地方。行为：
//
//	读旧 SQLite 文件（只读） → 建目标表（仅增量） → 逐表「源计数 → 拷贝 → 目标计数 → 抽样比对」
//
// 源库全程只被 SELECT / PRAGMA（连接串带 mode=ro 且开启 query_only），绝不建表、改结构或删行。
// 表名 / 列名映射与默认值见仓库 docs/DB.md。跑一次即可，之后不再需要 SQLite。
package main

import (
	"context"
	"database/sql"
	"errors"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"

	_ "github.com/go-sql-driver/mysql"
	mysqlcfg "github.com/go-sql-driver/mysql"

	_ "modernc.org/sqlite"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "迁移失败：%v\n", err)
		os.Exit(1)
	}
}

func run(args []string) error {
	fs := flag.NewFlagSet("migrate-sqlite-to-mysql", flag.ContinueOnError)
	sqlitePath := fs.String("sqlite", "", "旧 SQLite 库文件路径（只读打开）")
	mysqlDSN := fs.String("mysql", "", "目标 MySQL DSN（给全了就不用下面的分段参数）")
	host := fs.String("mysql-host", "127.0.0.1", "目标 MySQL 主机")
	port := fs.Int("mysql-port", 3306, "目标 MySQL 端口")
	name := fs.String("mysql-name", "quotahub", "目标库名")
	user := fs.String("mysql-user", "quotahub", "目标用户名")
	password := fs.String("mysql-password", "", "目标口令（也可用环境变量 DB_PASSWORD）")
	dryRun := fs.Bool("dry-run", false, "只报计划与源计数，不写目标库")
	sampleSize := fs.Int("sample-size", 5, "每表抽样比对行数")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if strings.TrimSpace(*sqlitePath) == "" {
		return errors.New("需要 --sqlite 指定旧库文件")
	}
	if *sampleSize < 0 {
		return errors.New("--sample-size 不能为负")
	}
	if *password == "" {
		*password = os.Getenv("DB_PASSWORD")
	}
	dsn := *mysqlDSN
	if dsn == "" {
		dsn = buildMySQLDSN(*host, *port, *name, *user, *password)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()

	src, err := openSQLiteReadOnly(ctx, *sqlitePath)
	if err != nil {
		return err
	}
	defer func() { _ = src.Close() }()

	if *dryRun {
		return reportDryRun(ctx, src, *sqlitePath)
	}

	dst, err := openMySQL(ctx, dsn)
	if err != nil {
		return err
	}
	defer func() { _ = dst.Close() }()

	return migrateAll(ctx, src, dst, *sampleSize)
}

// buildMySQLDSN 由分段参数拼 DSN；parseTime=false 让时间列保持 ISO 文本原样搬运。
func buildMySQLDSN(host string, port int, name, user, password string) string {
	cfg := mysqlcfg.NewConfig()
	cfg.User = user
	cfg.Passwd = password
	cfg.Net = "tcp"
	cfg.Addr = fmt.Sprintf("%s:%d", host, port)
	cfg.DBName = name
	cfg.ParseTime = false
	cfg.Collation = "utf8mb4_unicode_ci"
	cfg.Timeout = 5 * time.Second
	cfg.ReadTimeout = 60 * time.Second
	cfg.WriteTimeout = 60 * time.Second
	return cfg.FormatDSN()
}

// openSQLiteReadOnly 以只读方式打开旧库：连接串 mode=ro，再开 query_only 双保险。
func openSQLiteReadOnly(ctx context.Context, path string) (*sql.DB, error) {
	dsn := "file:" + path + "?mode=ro&_pragma=query_only(true)"
	db, err := sql.Open("sqlite", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开旧 SQLite 库失败: %w", err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接旧 SQLite 库失败: %w", err)
	}
	return db, nil
}

// openMySQL 打开目标库并补齐表结构（仅增量，绝不 DROP / TRUNCATE / 重建）。
func openMySQL(ctx context.Context, dsn string) (*sql.DB, error) {
	db, err := sql.Open("mysql", dsn)
	if err != nil {
		return nil, fmt.Errorf("打开目标 MySQL 失败: %w", err)
	}
	db.SetMaxOpenConns(4)
	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()
		return nil, fmt.Errorf("连接目标 MySQL 失败: %w", err)
	}
	if err := ensureSchema(ctx, db); err != nil {
		_ = db.Close()
		return nil, err
	}
	return db, nil
}
