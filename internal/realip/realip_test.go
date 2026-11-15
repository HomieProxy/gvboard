package realip

import (
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func hdr(kv ...string) http.Header {
	h := http.Header{}
	for i := 0; i+1 < len(kv); i += 2 {
		h.Add(kv[i], kv[i+1])
	}
	return h
}

func TestResolve(t *testing.T) {
	r, err := New([]string{"127.0.0.1", "::1", "10.0.0.0/8"})
	if err != nil {
		t.Fatal(err)
	}
	cdn := []string{"173.245.48.0/20", "2400:cb00::/32"}
	r.UseDynamic(func() []string { return cdn })

	cases := []struct {
		name    string
		remote  string
		h       http.Header
		want    string
		suspect bool
	}{
		{"直连", "1.2.3.4:5000", hdr(), "1.2.3.4", false},
		{"不可信来源伪造转发头", "1.2.3.4:5000", hdr("X-Forwarded-For", "9.9.9.9"), "1.2.3.4", true},
		{"本机反代", "127.0.0.1:5000", hdr("X-Forwarded-For", "1.2.3.4"), "1.2.3.4", false},
		{"反代加 CDN", "127.0.0.1:5000", hdr("X-Forwarded-For", "1.2.3.4, 173.245.48.7"), "1.2.3.4", false},
		{"用户伪造最左侧地址", "127.0.0.1:5000", hdr("X-Forwarded-For", "8.8.8.8, 1.2.3.4, 173.245.48.7"), "1.2.3.4", true},
		{"CDN 未加入可信代理", "127.0.0.1:5000", hdr("X-Forwarded-For", "1.2.3.4, 104.16.0.1"), "104.16.0.1", true},
		{"IPv6 CDN", "[::1]:5000", hdr("X-Forwarded-For", "2001:db8::1, 2400:cb00::5"), "2001:db8::1", false},
		{"多个转发头", "127.0.0.1:5000", hdr("X-Forwarded-For", "1.2.3.4", "X-Forwarded-For", "173.245.48.7"), "1.2.3.4", false},
		{"全部可信时取最左侧", "127.0.0.1:5000", hdr("X-Forwarded-For", "10.1.1.1, 10.2.2.2"), "10.1.1.1", false},
		{"非法地址退回 X-Real-IP", "127.0.0.1:5000", hdr("X-Forwarded-For", "abc", "X-Real-IP", "5.6.7.8"), "5.6.7.8", false},
		{"只有 X-Real-IP", "127.0.0.1:5000", hdr("X-Real-IP", "5.6.7.8"), "5.6.7.8", false},
		{"可信来源无转发头", "127.0.0.1:5000", hdr(), "127.0.0.1", false},
		{"IPv4 映射地址", "[::ffff:127.0.0.1]:5000", hdr("X-Forwarded-For", "::ffff:1.2.3.4"), "1.2.3.4", false},
	}
	for _, tc := range cases {
		ip, suspect := r.Resolve(tc.remote, tc.h)
		if ip.String() != tc.want || suspect != tc.suspect {
			t.Errorf("%s: got %s %v, want %s %v", tc.name, ip, suspect, tc.want, tc.suspect)
		}
	}
}

func TestDynamicUpdate(t *testing.T) {
	r, _ := New([]string{"127.0.0.1"})
	list := []string{}
	r.UseDynamic(func() []string { return list })
	h := hdr("X-Forwarded-For", "1.2.3.4, 104.16.0.1")
	if ip, _ := r.Resolve("127.0.0.1:1", h); ip.String() != "104.16.0.1" {
		t.Fatalf("未配置 CDN 时 got %s", ip)
	}
	list = []string{"104.16.0.0/13"}
	if ip, _ := r.Resolve("127.0.0.1:1", h); ip.String() != "1.2.3.4" {
		t.Fatalf("加入 CDN 后 got %s", ip)
	}
	list = []string{}
	if ip, _ := r.Resolve("127.0.0.1:1", h); ip.String() != "104.16.0.1" {
		t.Fatalf("移除 CDN 后 got %s", ip)
	}
}

func TestParsePrefixes(t *testing.T) {
	if _, err := ParsePrefixes([]string{"1.2.3.4", " 10.0.0.0/8 ", "::1", ""}); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"1.2.3", "10.0.0.0/33", "abc"} {
		if _, err := ParsePrefixes([]string{bad}); err == nil {
			t.Errorf("%q 应报错", bad)
		}
	}
}

func TestMiddlewareAndReport(t *testing.T) {
	gin.SetMode(gin.TestMode)
	r, _ := New([]string{"127.0.0.1"})
	now := time.Unix(1_700_000_000, 0)
	r.now = func() time.Time { return now }
	e := gin.New()
	e.Use(r.Middleware())
	var got string
	e.GET("/", func(c *gin.Context) { got = c.ClientIP() })

	do := func(xff string) {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		req.RemoteAddr = "127.0.0.1:4000"
		if xff != "" {
			req.Header.Set("X-Forwarded-For", xff)
		}
		e.ServeHTTP(httptest.NewRecorder(), req)
	}
	do("1.2.3.4")
	if got != "1.2.3.4" {
		t.Fatalf("ClientIP = %s", got)
	}
	for i := 0; i < 19; i++ {
		do("1.2.3.4, 104.16.0.1")
	}
	if rep := r.Report(); rep.Warning || rep.Suspect != 19 || rep.Total != 20 {
		t.Fatalf("不足 20 条可疑请求不应提示: %+v", rep)
	}
	do("1.2.3.5, 104.16.0.2")
	rep := r.Report()
	if !rep.Warning || len(rep.Samples) != 2 || rep.Samples[0] != "104.16.0.2" {
		t.Fatalf("应提示: %+v", rep)
	}

	for i := 0; i < 100; i++ {
		do("")
	}
	if rep := r.Report(); rep.Warning {
		t.Fatalf("可疑比例低于 30%% 不应提示: %+v", rep)
	}

	now = now.Add(25 * time.Minute)
	if rep := r.Report(); rep.Total != 0 || rep.Warning || len(rep.Samples) != 0 {
		t.Fatalf("超过统计窗口应清零: %+v", rep)
	}
}
