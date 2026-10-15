package store

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// openTestDB 打开测试用 MySQL：未配置 QUOTAHUB_TEST_MYSQL_DSN 时跳过。
// 存储层已收敛为仅 MySQL，故这里的用例只在有真实 MySQL 时运行（见 docs/DB.md）。
func openTestDB(t *testing.T) *DB {
	t.Helper()
	dsn := os.Getenv("QUOTAHUB_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未设置 QUOTAHUB_TEST_MYSQL_DSN，跳过 MySQL 存储测试")
	}
	db, err := Open(context.Background(), dsn, 4)
	if err != nil {
		t.Fatalf("打开测试库失败: %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	return db
}

// unique 生成一次测试唯一的后缀，避免共享库上用例互相干扰。
func unique(prefix string) string {
	return prefix + "-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}

func iso(offset time.Duration) string {
	return time.Now().UTC().Add(offset).Format("2006-01-02T15:04:05.000Z")
}

func TestRecordSampleAndLatest(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	pid := unique("plat")

	for i, at := range []string{iso(-2 * time.Minute), iso(-time.Minute), iso(0)} {
		ok, err := db.RecordSample(ctx, pid, float64(i)+0.5, at)
		if err != nil || !ok {
			t.Fatalf("写入采样失败: ok=%v err=%v", ok, err)
		}
	}

	latest, err := db.LatestSample(ctx, pid)
	if err != nil || latest == nil {
		t.Fatalf("读取最新采样失败: %v", err)
	}
	if latest.V != 2.5 {
		t.Fatalf("最新值应为 2.5，得到 %v", latest.V)
	}

	points, err := db.Samples(ctx, pid)
	if err != nil {
		t.Fatalf("读取采样序列失败: %v", err)
	}
	if len(points) != 3 {
		t.Fatalf("应有 3 个采样点，得到 %d", len(points))
	}
	if points[0].V != 0.5 || points[2].V != 2.5 {
		t.Fatalf("采样点应按时间升序，得到 %+v", points)
	}

	if err := db.RemoveSamples(ctx, pid); err != nil {
		t.Fatalf("删除采样失败: %v", err)
	}
	if got, _ := db.Samples(ctx, pid); len(got) != 0 {
		t.Fatalf("删除后应为空，得到 %d", len(got))
	}
}

// TestTrimKeepsLatest 验证单平台只保留最新 MaxPointsPerPlatform 点。
func TestTrimKeepsLatest(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	pid := unique("trim")

	// 用较粗的时间粒度（每点 +1s）写入上限 + 少量，验证裁剪。
	base := time.Now().UTC().Add(-time.Duration(MaxPointsPerPlatform+5) * time.Second)
	total := MaxPointsPerPlatform + 5
	for i := 0; i < total; i++ {
		at := base.Add(time.Duration(i) * time.Second).Format("2006-01-02T15:04:05.000Z")
		if _, err := db.RecordSample(ctx, pid, float64(i), at); err != nil {
			t.Fatalf("第 %d 次写入失败: %v", i, err)
		}
	}

	var count int
	if err := db.SQL.QueryRowContext(ctx,
		"SELECT COUNT(*) FROM history_samples WHERE platform_id = ?", pid).Scan(&count); err != nil {
		t.Fatalf("统计失败: %v", err)
	}
	if count != MaxPointsPerPlatform {
		t.Fatalf("应保留 %d 点，得到 %d", MaxPointsPerPlatform, count)
	}
	latest, _ := db.LatestSample(ctx, pid)
	if latest == nil || latest.V != float64(total-1) {
		t.Fatalf("最新点应为最后写入的 %d，得到 %+v", total-1, latest)
	}
	if err := db.RemoveSamples(ctx, pid); err != nil {
		t.Fatalf("清理失败: %v", err)
	}
}

func TestAccountAndSessionLifecycle(t *testing.T) {
	db := openTestDB(t)
	ctx := context.Background()
	username := unique("user")

	if err := db.InsertUser(ctx, username, "scrypt$aa$bb", iso(0)); err != nil {
		t.Fatalf("建账号失败: %v", err)
	}
	u, err := db.FindUserByUsername(ctx, username)
	if err != nil || u == nil || u.PasswordHash != "scrypt$aa$bb" {
		t.Fatalf("查账号失败: %v %+v", err, u)
	}

	token := unique("tok")
	if err := db.InsertSession(ctx, SessionRow{
		Token: token, Username: username, CreatedAt: iso(0), ExpiresAt: iso(time.Hour),
	}); err != nil {
		t.Fatalf("建会话失败: %v", err)
	}
	sess, err := db.FindValidSession(ctx, token, iso(0))
	if err != nil || sess == nil || sess.Username != username {
		t.Fatalf("会话应有效: %v %+v", err, sess)
	}
	if expired, _ := db.FindValidSession(ctx, token, iso(2*time.Hour)); expired != nil {
		t.Fatalf("过期会话不应命中: %+v", expired)
	}
	if err := db.TouchSession(ctx, token, iso(3*time.Hour)); err != nil {
		t.Fatalf("续期失败: %v", err)
	}
	if refreshed, _ := db.FindValidSession(ctx, token, iso(2*time.Hour)); refreshed == nil {
		t.Fatal("续期后应仍有效")
	}
	if err := db.DeleteSession(ctx, token); err != nil {
		t.Fatalf("删除会话失败: %v", err)
	}
	if gone, _ := db.FindValidSession(ctx, token, iso(0)); gone != nil {
		t.Fatal("删除后不应命中")
	}
}

// TestMigrateIdempotent 验证重复打开（含迁移）不报错，且缺列会被补齐。
func TestMigrateIdempotent(t *testing.T) {
	dsn := os.Getenv("QUOTAHUB_TEST_MYSQL_DSN")
	if dsn == "" {
		t.Skip("未设置 QUOTAHUB_TEST_MYSQL_DSN，跳过")
	}
	ctx := context.Background()
	for i := 0; i < 2; i++ {
		db, err := Open(ctx, dsn, 4)
		if err != nil {
			t.Fatalf("第 %d 次打开失败: %v", i+1, err)
		}
		exists, err := columnExists(ctx, db.SQL, "history_samples", "value_text")
		if err != nil || !exists {
			t.Fatalf("value_text 应存在: %v exists=%v", err, exists)
		}
		_ = db.Close()
	}
}
