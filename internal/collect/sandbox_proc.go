// 沙箱子进程：把一次脚本执行放进独立进程（可选独立网络命名空间 + 降权），
// 由父进程决定生死。父进程只传一次 JSON、只读一次 JSON，除此之外无任何 IPC 通道。
package collect

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime/debug"
	"syscall"
	"time"

	"golang.org/x/sys/unix"
)

// 沙箱子进程的加固参数。
const (
	runnerMaxStdinBytes = 8 << 20   // 请求体上限（raw 响应体之外不应有大载荷）
	runnerRLimitCPU     = 5         // 秒；再长也会被父进程先 kill
	runnerRLimitAS      = 4 << 30   // 4GiB 虚拟地址空间兜底（Go 运行时启动就要预留 >1GiB，设小了直接起不来）
	runnerRLimitNoFile  = 64        // 句柄数
	runnerHeapLimit     = 256 << 20 // Go 软内存上限：真正的内存闸门在这里
)

// sandboxRequest 是父进程 → 子进程的唯一载荷。
type sandboxRequest struct {
	Kind      string            `json:"kind"` // parse | extract | evaljs
	Src       string            `json:"src"`
	Raw       string            `json:"raw,omitempty"`
	Headers   map[string]string `json:"headers,omitempty"`
	Data      json.RawMessage   `json:"data,omitempty"`
	TimeoutMs int               `json:"timeout_ms"`
}

// sandboxResponse 是子进程 → 父进程的唯一回执。
type sandboxResponse struct {
	OK      bool            `json:"ok"`
	Value   json.RawMessage `json:"value,omitempty"`
	Error   string          `json:"error,omitempty"`
	Timeout bool            `json:"timeout,omitempty"`
}

// RunSandboxRunner 是子进程入口：加固 → 读一次请求 → 执行 → 写一次回执。
// 返回进程退出码（0 表示协议层面成功，脚本错误体现在回执里）。
func RunSandboxRunner() int {
	hardenRunner()

	var req sandboxRequest
	dec := json.NewDecoder(io.LimitReader(os.Stdin, runnerMaxStdinBytes))
	if err := dec.Decode(&req); err != nil {
		writeRunnerResponse(sandboxResponse{Error: "沙箱请求解析失败: " + err.Error()})
		return 2
	}

	sb := &Sandbox{
		timeout: time.Duration(req.TimeoutMs) * time.Millisecond,
		mode:    SandboxModeInline,
	}
	if sb.timeout <= 0 {
		sb.timeout = 5 * time.Second
	}

	v, err := sb.runInline(req)
	switch {
	case err == nil:
		raw, mErr := json.Marshal(encodeForWire(v))
		if mErr != nil {
			writeRunnerResponse(sandboxResponse{Error: "结果无法序列化: " + mErr.Error()})
			return 0
		}
		writeRunnerResponse(sandboxResponse{OK: true, Value: raw})
	case errors.Is(err, errTimeout):
		writeRunnerResponse(sandboxResponse{Timeout: true, Error: "脚本执行超时"})
	default:
		writeRunnerResponse(sandboxResponse{Error: err.Error()})
	}
	return 0
}

// writeRunnerResponse 把回执写到 stdout（协议唯一出口）。
func writeRunnerResponse(resp sandboxResponse) {
	_ = json.NewEncoder(os.Stdout).Encode(resp)
}

// hardenRunner 收紧子进程：不可提权、不可 dump、CPU/内存/句柄都有硬上限。
// 即使 JS 引擎被攻破，攻击者拿到的也只是一个马上会被 kill 的、几乎没权限的进程。
func hardenRunner() {
	_ = unix.Prctl(unix.PR_SET_NO_NEW_PRIVS, 1, 0, 0, 0)
	_ = unix.Prctl(unix.PR_SET_DUMPABLE, 0, 0, 0, 0)

	limit := func(res int, cur uint64) {
		_ = unix.Setrlimit(res, &unix.Rlimit{Cur: cur, Max: cur})
	}
	limit(unix.RLIMIT_CPU, runnerRLimitCPU)
	limit(unix.RLIMIT_AS, runnerRLimitAS)
	limit(unix.RLIMIT_NOFILE, runnerRLimitNoFile)
	limit(unix.RLIMIT_CORE, 0)

	// Go 自己的软内存上限：比 RLIMIT_AS 更早触发、更干净（直接 OOM 退出而不是映射失败）。
	debug.SetMemoryLimit(runnerHeapLimit)
}

// execProcess 在一次性子进程里执行请求。
func (s *Sandbox) execProcess(req sandboxRequest) (any, error) {
	payload, err := json.Marshal(req)
	if err != nil {
		return nil, fmt.Errorf("序列化沙箱请求失败: %w", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), s.timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, s.self, "--sandbox-runner")
	// 不继承 quotahub 的环境变量：数据库口令、会话密钥一个都不该出现在子进程里。
	cmd.Env = []string{"PATH=/usr/local/bin:/usr/bin:/bin", "HOME=/nonexistent", "TMPDIR=/nonexistent"}
	cmd.Dir = "/"
	cmd.Stdin = bytes.NewReader(payload)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	cmd.SysProcAttr = s.procAttr()
	cmd.WaitDelay = 500 * time.Millisecond
	cmd.Cancel = func() error {
		if cmd.Process == nil {
			return nil
		}
		// 整个进程组一起杀，避免脚本或引擎派生的任何子进程残留。
		_ = syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)
		return cmd.Process.Kill()
	}

	runErr := cmd.Run()
	if ctx.Err() == context.DeadlineExceeded {
		return nil, errTimeout
	}

	var resp sandboxResponse
	if err := json.Unmarshal(stdout.Bytes(), &resp); err != nil {
		// 没有可解析的回执：子进程被打死（OOM / CPU 限额 / 崩溃）。
		hint := "沙箱进程异常退出"
		if runErr != nil {
			hint = fmt.Sprintf("沙箱进程异常退出 (%v)", runErr)
		}
		if detail := bytes.TrimSpace(stderr.Bytes()); len(detail) > 0 {
			hint += "：" + truncateForLog(string(detail), 200)
		}
		return nil, errors.New(hint)
	}
	if resp.Timeout {
		return nil, errTimeout
	}
	if !resp.OK {
		if resp.Error == "" {
			resp.Error = "脚本执行失败"
		}
		return nil, errors.New(resp.Error)
	}
	if len(resp.Value) == 0 || string(resp.Value) == "null" {
		return nil, nil
	}
	var raw any
	if err := json.Unmarshal(resp.Value, &raw); err != nil {
		return nil, fmt.Errorf("沙箱结果解析失败: %w", err)
	}
	return decodeFromWire(raw), nil
}

// procAttr 返回子进程的隔离属性：
//   - Setpgid：父进程可以整组 SIGKILL
//   - Pdeathsig：quotahub 退出时子进程一起走
//   - root 运行时：独立网络命名空间 + 降权到 nobody
func (s *Sandbox) procAttr() *syscall.SysProcAttr {
	attr := &syscall.SysProcAttr{Setpgid: true, Pdeathsig: syscall.SIGKILL}
	if os.Geteuid() != 0 {
		return attr
	}
	// 独立 netns：脚本连不上内网、连不上云元数据地址，也解析不了任何域名。
	attr.Cloneflags = syscall.CLONE_NEWNET
	// 二进制必须对其他用户可执行，否则降权后 exec 会 EACCES。
	if selfExecutableByOthers(s.self) {
		attr.Credential = &syscall.Credential{
			Uid:         uint32(s.uid),
			Gid:         uint32(s.uid),
			NoSetGroups: true,
		}
	}
	return attr
}

// selfExecutableByOthers 判断二进制是否对其他用户可执行（降权前置条件）：
// 二进制本身要 o+x，**沿途每一级目录也必须 o+x**——只要有一级是 700（比如 /root），
// 降权后 exec 就会 EACCES，此时宁可不降权也不能让采集整体失败。
func selfExecutableByOthers(path string) bool {
	abs, err := filepath.Abs(path)
	if err != nil {
		return false
	}
	for cur := abs; ; cur = filepath.Dir(cur) {
		info, statErr := os.Stat(cur)
		if statErr != nil {
			return false
		}
		if info.Mode().Perm()&0o001 == 0 {
			return false
		}
		if parent := filepath.Dir(cur); parent == cur {
			break
		}
	}
	return true
}

// truncateForLog 截断过长的错误文本。
func truncateForLog(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
