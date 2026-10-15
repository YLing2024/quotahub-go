package collect

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// newAllowAllFetcher 构造放开内网限制、脚本超时 200ms 的抓取器（本地测试服务用）。
func newAllowAllFetcher() *Fetcher { return NewFetcher(true, 200) }

// newDenyPrivateFetcher 构造默认 SSRF 口径的抓取器。
func newDenyPrivateFetcher() *Fetcher { return NewFetcher(false, 2000) }

func mustFetch(t *testing.T, f *Fetcher, target FetchTarget) float64 {
	t.Helper()
	value, err := f.FetchBalance(context.Background(), target)
	if err != nil {
		t.Fatalf("抓取失败: %v", err)
	}
	return value
}

func fetchErr(t *testing.T, f *Fetcher, target FetchTarget) error {
	t.Helper()
	_, err := f.FetchBalance(context.Background(), target)
	if err == nil {
		t.Fatalf("期望报错但没有")
	}
	return err
}

func TestFetchBalanceLocalServer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/json":
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"data":{"quota":500000}}`))
		case "/js":
			w.Header().Set("Content-Type", "text/javascript")
			_, _ = w.Write([]byte(`;0x1f;$R = { total: 88.5 };$R`))
		case "/boom":
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte("server exploded"))
		case "/echo-method":
			_, _ = w.Write([]byte(`{"method":"` + r.Method + `"}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer server.Close()
	f := newAllowAllFetcher()

	t.Run("单函数模型 handler", func(t *testing.T) {
		got := mustFetch(t, f, FetchTarget{
			Request: map[string]any{"url": server.URL + "/json"},
			Handler: `function (raw) { return JSON.parse(raw).data.quota / 500000 }`,
		})
		if got != 1 {
			t.Fatalf("期望 1，得到 %v", got)
		}
	})

	t.Run("旧模型 JSON.parse + extractor", func(t *testing.T) {
		got := mustFetch(t, f, FetchTarget{
			Request:   map[string]any{"url": server.URL + "/json"},
			Extractor: `function (data) { return data.data.quota / 1000 }`,
		})
		if got != 500 {
			t.Fatalf("期望 500，得到 %v", got)
		}
	})

	t.Run("JS 响应回退 evalJsResponse", func(t *testing.T) {
		got := mustFetch(t, f, FetchTarget{
			Request:   map[string]any{"url": server.URL + "/js"},
			Extractor: `function (data) { return data.total }`,
		})
		if got != 88.5 {
			t.Fatalf("期望 88.5，得到 %v", got)
		}
	})

	t.Run("response.path + divider", func(t *testing.T) {
		got := mustFetch(t, f, FetchTarget{
			Request:  map[string]any{"url": server.URL + "/json"},
			Response: map[string]any{"path": "data.quota", "divider": 500000},
		})
		if got != 1 {
			t.Fatalf("期望 1，得到 %v", got)
		}
	})

	t.Run("POST 方法透传", func(t *testing.T) {
		got := mustFetch(t, f, FetchTarget{
			Request: map[string]any{"method": "POST", "url": server.URL + "/echo-method", "headers": map[string]any{}, "body": map[string]any{"a": 1}},
			Handler: `function (raw) { return JSON.parse(raw).method === "POST" ? 7 : 0 }`,
		})
		if got != 7 {
			t.Fatalf("期望 7，得到 %v", got)
		}
	})

	t.Run("HTTP 500 中文错误信息", func(t *testing.T) {
		err := fetchErr(t, f, FetchTarget{
			Request: map[string]any{"url": server.URL + "/boom"},
			Handler: `function(r){return 1}`,
		})
		if !strings.Contains(err.Error(), "HTTP 500: server exploded") {
			t.Fatalf("错误信息不符: %v", err)
		}
	})
}

func TestFetchBalanceNumericSemantics(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"data":{"quota":500000}}`))
	}))
	defer server.Close()
	f := newAllowAllFetcher()

	cases := []struct {
		name    string
		handler string
		want    string
	}{
		{"未返回", `function (raw) { return undefined }`, "处理函数未返回余额"},
		{"返回 null", `function (raw) { return null }`, "处理函数未返回余额"},
		{"空白字符串", `function (raw) { return "   " }`, "处理函数未返回余额"},
		{"返回字符串", `function (raw) { return "$8.51" }`, "处理函数必须返回数字(当前返回 string)"},
		{"返回布尔", `function (raw) { return true }`, "处理函数必须返回数字(当前返回 boolean)"},
		{"返回 NaN", `function (raw) { return NaN }`, "处理函数必须返回数字(当前返回 number)"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			err := fetchErr(t, f, FetchTarget{
				Request: map[string]any{"url": server.URL + "/json"},
				Handler: tc.handler,
			})
			if err.Error() != tc.want {
				t.Fatalf("期望 %q，得到 %q", tc.want, err.Error())
			}
		})
	}

	t.Run("未配置处理函数", func(t *testing.T) {
		err := fetchErr(t, f, FetchTarget{Request: map[string]any{"url": server.URL + "/json"}})
		if err.Error() != "未配置处理函数" {
			t.Fatalf("错误信息不符: %v", err)
		}
	})
}

func TestFetchBalanceSSRF(t *testing.T) {
	f := newDenyPrivateFetcher()

	t.Run("未配置请求 URL", func(t *testing.T) {
		err := fetchErr(t, f, FetchTarget{})
		if err.Error() != "未配置请求 URL" {
			t.Fatalf("错误信息不符: %v", err)
		}
	})

	t.Run("非 http/https 拒绝", func(t *testing.T) {
		err := fetchErr(t, f, FetchTarget{Request: map[string]any{"url": "ftp://example.com/x"}})
		if err.Error() != "仅支持 http/https 请求" {
			t.Fatalf("错误信息不符: %v", err)
		}
	})

	blocked := []string{
		"http://127.0.0.1:9/x",
		"https://localhost/x",
		"http://169.254.169.254/latest/meta-data/",
		"http://10.0.0.5/api",
		"http://192.168.1.2/api",
		"http://172.16.0.1/api",
		"http://[::1]/api",
		"http://[fd00::1]/api",
	}
	for _, rawURL := range blocked {
		t.Run(rawURL, func(t *testing.T) {
			err := fetchErr(t, f, FetchTarget{Request: map[string]any{"url": rawURL}})
			if !strings.Contains(err.Error(), "目标地址为环回/内网地址，已阻止") {
				t.Fatalf("期望被阻止，得到: %v", err)
			}
		})
	}

	t.Run("放开内网后可访问本机", func(t *testing.T) {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{}`))
		}))
		defer server.Close()
		if _, err := f.FetchBalance(context.Background(), FetchTarget{
			Request: map[string]any{"url": server.URL},
			Handler: `function(){ return 1 }`,
		}); err == nil {
			t.Fatalf("默认口径下不应访问内网")
		}
		allow := newAllowAllFetcher()
		if got := mustFetch(t, allow, FetchTarget{
			Request: map[string]any{"url": server.URL},
			Handler: `function(){ return 2 }`,
		}); got != 2 {
			t.Fatalf("放开内网后应可访问，得到 %v", got)
		}
	})
}
