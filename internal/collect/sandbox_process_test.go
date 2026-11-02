package collect

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// buildSandboxBinary 把 quotahub 本体编到临时目录，用于进程隔离的真实验证。
// 单元测试里用的是同一条 --sandbox-runner 协议，和线上完全一致的代码路径。
func buildSandboxBinary(t *testing.T) string {
	t.Helper()
	out := filepath.Join(t.TempDir(), "quotahub-sandbox")
	cmd := exec.Command("go", "build", "-o", out, "./cmd/quotahub-go")
	cmd.Dir = "../../"
	if b, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("构建沙箱二进制失败: %v\n%s", err, b)
	}
	if err := os.Chmod(out, 0o755); err != nil {
		t.Fatalf("chmod 失败: %v", err)
	}
	return out
}

func TestSandboxProcessIsolation(t *testing.T) {
	if testing.Short() {
		t.Skip("跳过长测试（-short）")
	}
	self := buildSandboxBinary(t)

	s := &Sandbox{
		timeout: 400 * time.Millisecond,
		mode:    SandboxModeProcess,
		self:    self,
		uid:     defaultSandboxUID,
	}

	t.Run("正常解析", func(t *testing.T) {
		v, err := s.RunParse(`{"balance": 12.5}`, `function (raw) { return JSON.parse(raw).balance }`)
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		if n, _ := numericOf(v); n != 12.5 {
			t.Fatalf("期望 12.5，得到 %v", v)
		}
	})

	t.Run("meta.headers 可用", func(t *testing.T) {
		v, err := s.RunParseWithHeaders("body", map[string]string{"subscription-userinfo": "total=100"}, `
			function (raw, meta) { return meta.headers['subscription-userinfo'] }`)
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		if v != "total=100" {
			t.Fatalf("期望透出响应头，得到 %v", v)
		}
	})

	t.Run("死循环被 kill 且不拖住宿主", func(t *testing.T) {
		start := time.Now()
		_, err := s.RunParse("x", `function (raw) { for(;;){} }`)
		took := time.Since(start)
		if err == nil || !strings.Contains(err.Error(), "解析函数执行超时") {
			t.Fatalf("期望超时错误，得到: %v", err)
		}
		if took > 2*time.Second {
			t.Fatalf("超时没有及时生效: %v", took)
		}
	})

	t.Run("死循环之后仍可继续执行", func(t *testing.T) {
		v, err := s.RunParse(`{"balance": 1}`, `function (raw) { return JSON.parse(raw).balance }`)
		if err != nil {
			t.Fatalf("沙箱被杀后无法复用: %v", err)
		}
		if n, _ := numericOf(v); n != 1 {
			t.Fatalf("期望 1，得到 %v", v)
		}
	})

	t.Run("内存炸弹不会打穿宿主", func(t *testing.T) {
		_, err := s.RunParse("x", `
			function (raw) {
				var a = [];
				for (var i = 0; i < 20000000; i++) a.push({ i: i, s: 'x'.repeat(120) });
				return a.length;
			}`)
		if err == nil {
			t.Fatalf("期望内存炸弹失败，实际成功")
		}
	})

	t.Run("脚本拿不到宿主能力", func(t *testing.T) {
		v, err := s.RunParse("x", `
			function (raw) {
				return [typeof require, typeof process, typeof fetch, typeof XMLHttpRequest].join(',');
			}`)
		if err != nil {
			t.Fatalf("意外错误: %v", err)
		}
		if v != "undefined,undefined,undefined,undefined" {
			t.Fatalf("脚本看到了宿主能力: %v", v)
		}
	})

	t.Run("语法错误透出中文前缀", func(t *testing.T) {
		_, err := s.RunParse("x", `function (raw) { return ))) }`)
		if err == nil || !strings.Contains(err.Error(), "解析函数执行失败") {
			t.Fatalf("期望语法错误，得到: %v", err)
		}
	})
}
