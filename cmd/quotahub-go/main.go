// Command quotahub-go 是 QuotaHub（LLM 平台余额 / 配额看板）的 Go 重写版入口。
//
// 运行形态：
//
//	quotahub-go          启动管理面（HTTP + 内置认证 + 定时采集）
//	quotahub-go serve    同上（显式写法）
//
// 入口只做装配与生命周期：初始化数据目录 → 连库并迁移 → 首启引导 → 组装服务 → 监听 →
// 优雅退出。定时任务统一交给 collect.Monitor 内的 scheduler，不在入口裸起定时 goroutine。
// 数据库固定 MySQL；旧 SQLite 数据由独立工具 tools/migrate-sqlite-to-mysql 一次性搬运。
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/signal"
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
	"quotahub-go/internal/store"
	"quotahub-go/internal/transfer"
)

// maxOpenConns 是 MySQL 连接池上限（够用且不至于把库压满）。
const maxOpenConns = 16

func main() {
	if err := run(os.Args[1:]); err != nil {
		fmt.Fprintf(os.Stderr, "启动失败：%v\n", err)
		os.Exit(1)
	}
}

// run 解析参数并启动服务。
func run(args []string) error {
	logger := newLogger()
	if len(args) > 0 && args[0] != "serve" {
		return fmt.Errorf("未知参数 %q（可用：serve，或直接不带参数启动）", args[0])
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
	if err := os.MkdirAll(cfg.DataDir, 0o755); err != nil {
		return fmt.Errorf("创建数据目录失败: %w", err)
	}
	db, err := store.Open(ctx, buildDSN(cfg), maxOpenConns)
	if err != nil {
		return err
	}
	defer func() { _ = db.Close() }()

	// 历史采样保留窗口（默认 5 年；QUOTAHUB_HISTORY_RETENTION_DAYS 覆盖）。
	db.RetentionDays = cfg.HistoryRetentionDays

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

// buildDSN 依据配置拼出 MySQL DSN：给了 DB_DSN 就整体走它，否则用分段参数。
func buildDSN(cfg *config.Config) string {
	if cfg.DBDSN != "" {
		return cfg.DBDSN
	}
	return mysql.BuildDSN(cfg.DBHost, cfg.DBPort, cfg.DBName, cfg.DBUser, cfg.DBPassword)
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
