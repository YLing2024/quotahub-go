// Command quotahub-go 是 QuotaHub（LLM 平台余额 / 配额看板）的 Go 重写版入口。
//
// 运行形态：
//
//	quotahub-go                 启动管理面（HTTP + 内置认证 + 定时采集）
//	quotahub-go migrate-data …  跨库搬运（SQLite ⇄ MySQL），源库只读
//
// 入口只做装配与生命周期：初始化数据目录 → 开库 → 首启引导 → 组装服务 → 监听 → 优雅退出。
// 定时任务统一交给 collect.Monitor 内的 scheduler，不在入口裸起定时 goroutine。
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"time"

	quotahub "quotahub-go"
	"quotahub-go/internal/auth"
	"quotahub-go/internal/collect"
	"quotahub-go/internal/config"
	"quotahub-go/internal/httpapi"
	"quotahub-go/internal/logs"
	"quotahub-go/internal/mysql"
	"quotahub-go/internal/platform"
	"quotahub-go/internal/presets"
	"quotahub-go/internal/settings"
	"quotahub-go/internal/sqlite"
	"quotahub-go/internal/store"
	"quotahub-go/internal/transfer"
)

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "启动失败：%v\n", err)
		os.Exit(1)
	}
}

// run 解析子命令并分派。
func run(args []string) error {
	logger := newLogger()
	if len(args) > 0 && args[0] == "migrate-data" {
		return runMigrateData(args[1:], logger)
	}
	if len(args) > 0 && args[0] != "serve" {
		return fmt.Errorf("未知参数 %q（可用：serve、migrate-data）", args[0])
	}
	cfg, err := config.LoadEnv()
	if err != nil {
		return err
	}
	return serve(cfg, logger)
}

// serve 起完整管理面：开库 → 引导 → 装配 → listen → 起定时采集 → 等退出信号。
func serve(cfg *config.Config, logger *slog.Logger) error {
	ctx := context.Background()
	if err := ensureDataDir(cfg); err != nil {
		return err
	}
	dialect, dsn, err := buildDialect(cfg)
	if err != nil {
		return err
	}
	db, err := store.Open(ctx, dialect, dsn, maxOpenConns(cfg.DBDriver))
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	// 认证：启动时打印模式；builtin 下 users 为空时创建管理员（口令仅打印一次）。
	fmt.Println("认证模式: " + string(cfg.AuthMode))
	accounts := auth.NewAccounts(db, time.Duration(cfg.SessionTtlSeconds)*time.Second)
	if cfg.AuthMode == config.AuthBuiltin {
		boot, err := accounts.BootstrapAdmin(ctx, cfg.AdminUser, cfg.AdminPassword)
		if err != nil {
			return err
		}
		if boot.Created {
			fmt.Printf("已创建管理员账号「%s」\n", boot.Username)
			if boot.Generated {
				printGeneratedPassword(boot.Password)
			} else {
				fmt.Println("初始口令取自 QUOTAHUB_ADMIN_PASSWORD。")
			}
		}
	}
	authenticator := auth.NewAuthenticator(cfg.AuthMode, accounts)

	platformSvc := platform.New(cfg.DataDir)
	presetSvc := presets.New(cfg.DataDir)
	settingsSvc := settings.New(cfg.DataDir)
	logSvc := logs.New(cfg.DataDir, logger)
	monitorSvc := collect.NewMonitor(platformSvc, db, settingsSvc, logSvc, cfg.AllowPrivate, cfg.ScriptTimeoutMs)
	transferSvc := transfer.New(platformSvc, presetSvc, logSvc)

	handler := httpapi.New(httpapi.Deps{
		Config:    cfg,
		Auth:      authenticator,
		Platforms: platformSvc,
		Presets:   presetSvc,
		Settings:  settingsSvc,
		Logs:      logSvc,
		Monitor:   monitorSvc,
		Transfer:  transferSvc,
		History:   db,
		Assets:    quotahub.DistFS(),
	})
	server := &http.Server{
		Addr:              net.JoinHostPort(cfg.Host, strconv.Itoa(cfg.Port)),
		Handler:           handler,
		ReadHeaderTimeout: 10 * time.Second,
	}
	listener, err := net.Listen("tcp", server.Addr)
	if err != nil {
		return err
	}

	fmt.Printf("QuotaHub 已启动: http://%s:%d\n", cfg.Host, cfg.Port)
	fmt.Printf("沙箱脚本超时: %dms (QUOTAHUB_SCRIPT_TIMEOUT_MS 可调)\n", cfg.ScriptTimeoutMs)
	interval := settingsSvc.Get().CollectIntervalSeconds
	if interval > 0 {
		fmt.Printf("自动采集: 每 %d 秒采集一次\n", interval)
		monitorSvc.Start()
	} else {
		fmt.Println("自动采集: 已关闭 (在设置中配置间隔开启)")
	}
	logSvc.Add("start", "QuotaHub 已启动", logs.Options{})

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			logger.Error("服务器错误：" + err.Error())
		}
	}()

	return waitForShutdown(server, monitorSvc, db)
}

// waitForShutdown 按旧实现顺序优雅退出：停调度 → 关 HTTP → 关库。
func waitForShutdown(server *http.Server, monitorSvc *collect.Monitor, db *store.DB) error {
	signals := make(chan os.Signal, 1)
	signal.Notify(signals, syscall.SIGINT, syscall.SIGTERM)
	sig := <-signals
	fmt.Printf("收到 %s, 正在退出...\n", signalName(sig))
	if monitorSvc != nil {
		monitorSvc.Stop()
	}
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	if err := server.Shutdown(ctx); err != nil {
		fmt.Fprintf(os.Stderr, "关闭服务器超时：%v\n", err)
	}
	if db != nil {
		_ = db.Close()
	}
	return nil
}

// ensureDataDir 建数据目录与 SQLite 库父目录（MySQL 无本地文件）。
func ensureDataDir(cfg *config.Config) error {
	dirs := []string{cfg.DataDir}
	if cfg.DBDriver == config.DBDriverSQLite {
		if parent := filepath.Dir(cfg.MonitorDBPath()); parent != "" && parent != "." {
			dirs = append(dirs, parent)
		}
	}
	for _, dir := range dirs {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return fmt.Errorf("创建数据目录失败: %w", err)
		}
	}
	return nil
}

// buildDialect 依据配置选方言与 DSN：MySQL 未配置时根本不加载驱动、不建连接。
func buildDialect(cfg *config.Config) (store.Dialect, string, error) {
	switch cfg.DBDriver {
	case config.DBDriverMySQL:
		if cfg.DBDSN != "" {
			return mysql.New(), cfg.DBDSN, nil
		}
		return mysql.New(), mysql.BuildDSN(cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBUser, cfg.DBPassword), nil
	case config.DBDriverSQLite:
		return sqlite.New(), sqlite.DSN(cfg.MonitorDBPath()), nil
	default:
		return nil, "", fmt.Errorf("不支持的 DB_DRIVER %q", cfg.DBDriver)
	}
}

// maxOpenConns 给两种方言各设连接池上限（SQLite 写锁敏感，池小些）。
func maxOpenConns(driver config.DBDriver) int {
	if driver == config.DBDriverMySQL {
		return 16
	}
	return 4
}

// signalName 返回信号可读名。
func signalName(sig os.Signal) string {
	switch sig {
	case syscall.SIGINT:
		return "SIGINT"
	case syscall.SIGTERM:
		return "SIGTERM"
	}
	return "信号"
}

// newLogger 用标准库 slog 输出到 stderr。
func newLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelInfo}))
}

// printGeneratedPassword 只打印一次随机初始口令，提示立即保存。
func printGeneratedPassword(password string) {
	line := strings.Repeat("-", 60)
	fmt.Println(line)
	fmt.Printf("初始口令: %s\n", password)
	fmt.Println("此口令只在本次启动打印一次，请立即保存。")
	fmt.Println(line)
}

// runMigrateData 跨库搬运：--from / --to 用 `sqlite:` / `mysql:` 前缀选驱动。
func runMigrateData(args []string, logger *slog.Logger) error {
	fs := flag.NewFlagSet("migrate-data", flag.ContinueOnError)
	from := fs.String("from", "", "源库 DSN，前缀 sqlite: 或 mysql:")
	to := fs.String("to", "", "目标库 DSN，前缀 sqlite: 或 mysql:")
	dryRun := fs.Bool("dry-run", false, "只统计与校验，不写目标库")
	verify := fs.Bool("verify", false, "搬运后逐表比对条数与抽样行")
	if err := fs.Parse(args); err != nil {
		return err
	}
	if *from == "" || *to == "" {
		return errors.New("migrate-data 需要 --from 与 --to")
	}
	srcDialect, srcDSN, err := parseMigrateDSN(*from)
	if err != nil {
		return fmt.Errorf("--from 无效: %w", err)
	}
	dstDialect, dstDSN, err := parseMigrateDSN(*to)
	if err != nil {
		return fmt.Errorf("--to 无效: %w", err)
	}

	ctx := context.Background()
	// 源库用 OpenRaw：只连不迁移，保证源库只被 SELECT。
	src, err := store.OpenRaw(ctx, srcDialect, srcDSN, 4)
	if err != nil {
		return fmt.Errorf("打开源库失败: %w", err)
	}
	defer func() { _ = src.Close() }()
	dst, err := store.Open(ctx, dstDialect, dstDSN, 4)
	if err != nil {
		return fmt.Errorf("打开目标库失败: %w", err)
	}
	defer func() { _ = dst.Close() }()

	report, err := store.MigrateData(ctx, src, dst, store.MigrateOptions{DryRun: *dryRun, Verify: *verify})
	if err != nil {
		return err
	}
	printMigrateReport(report, *from, *to)
	if *verify && !report.OK() {
		return errors.New("抽样/条数比对未通过")
	}
	logger.Info("migrate-data 完成")
	return nil
}

// parseMigrateDSN 解析 `sqlite:<path>` / `mysql:<go-sql-driver DSN>`，前缀决定方言。
func parseMigrateDSN(raw string) (store.Dialect, string, error) {
	switch {
	case strings.HasPrefix(raw, "sqlite:"):
		path := strings.TrimPrefix(raw, "sqlite:")
		if strings.TrimSpace(path) == "" {
			return nil, "", errors.New("sqlite DSN 需要文件路径，例如 sqlite:./data/monitor.db")
		}
		return sqlite.New(), sqlite.DSN(path), nil
	case strings.HasPrefix(raw, "mysql:"):
		dsn := strings.TrimPrefix(raw, "mysql:")
		if strings.TrimSpace(dsn) == "" {
			return nil, "", errors.New("mysql DSN 不能为空")
		}
		return mysql.New(), dsn, nil
	default:
		return nil, "", fmt.Errorf("无法识别的 DSN %q（需以 sqlite: 或 mysql: 开头）", raw)
	}
}

// printMigrateReport 打印逐表搬运与校验结果（不含任何连接串 / 口令）。
func printMigrateReport(report *store.MigrateReport, from, to string) {
	mode := "搬运"
	if report.DryRun {
		mode = "演练（dry-run，不写目标库）"
	}
	fmt.Printf("migrate-data %s: %s → %s\n", mode, from, to)
	var totalSrc, totalWritten int64
	for _, t := range report.Tables {
		totalSrc += t.Source
		totalWritten += t.Written
		line := fmt.Sprintf("  %-16s 源 %5d  写入 %5d", t.Table, t.Source, t.Written)
		if report.Verify {
			status := "OK"
			if len(t.Mismatches) > 0 {
				status = "不一致"
			}
			line += fmt.Sprintf("  抽样 %d  %s", t.SampleRows, status)
		}
		fmt.Println(line)
		for _, m := range t.Mismatches {
			fmt.Println("      - " + m)
		}
	}
	fmt.Printf("  合计 源 %d 行，写入 %d 行\n", totalSrc, totalWritten)
}
