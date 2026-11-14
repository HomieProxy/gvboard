package server

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"gvboard/internal/config"
	"gvboard/internal/httpx"
)

func testConfig() *config.Config {
	return &config.Config{
		App:    config.AppConfig{Key: "base64:0123456789abcdef", Timezone: "Asia/Shanghai"},
		Server: config.ServerConfig{Mode: config.ModeAll, MaxBodyBytes: 1024},
		API:    config.APIConfig{Prefix: "/api/testprefix"},
	}
}

func newTestRouter(t *testing.T, cfg *config.Config) *Router {
	t.Helper()
	r, err := NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	return r
}

func do(r *Router, method, path string, header map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	for k, v := range header {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.Engine.ServeHTTP(w, req)
	return w
}

// 统一 404：原项目特征路径、方法不匹配、尾部斜杠、任意路径的响应必须完全一致。
func TestUnifiedNotFound(t *testing.T) {
	r := newTestRouter(t, testConfig())
	// 模拟节点接口用 token 校验失败的场景
	r.NodeV1.GET("/UniProxy/user", httpx.NotFound)

	ref := do(r, http.MethodGet, "/this/path/does/not/exist", nil)
	if ref.Code != http.StatusNotFound {
		t.Fatalf("参考 404 状态码错误: %d", ref.Code)
	}

	paths := []struct{ method, path string }{
		{http.MethodGet, "/api/v1/guest/comm/config"},
		{http.MethodPost, "/api/v1/passport/auth/login"},
		{http.MethodGet, "/api/v1/user/info"},
		{http.MethodGet, "/api/v1/client/subscribe?token=abc"},
		{http.MethodGet, "/api/v1/server/UniProxy/user"},
		{http.MethodGet, "/api/v1/server/UniProxy/user?token=x&node_id=1&node_type=vmess"},
		{http.MethodGet, "/api/v2/server/config"},
		{http.MethodPost, "/api/testprefix/ping"}, // 方法不匹配不能返回 405
		{http.MethodGet, "/api/testprefix/ping/"}, // 尾部斜杠不能重定向
		{http.MethodGet, "/API/TESTPREFIX/PING"},  // 大小写不能修正重定向
		{http.MethodGet, "/theme/default/assets/umi.js"},
		{http.MethodGet, "/"},
	}
	for _, p := range paths {
		w := do(r, p.method, p.path, nil)
		if w.Code != ref.Code {
			t.Errorf("%s %s 状态码 %d，期望 %d", p.method, p.path, w.Code, ref.Code)
		}
		if w.Body.String() != ref.Body.String() {
			t.Errorf("%s %s 响应体与参考 404 不一致", p.method, p.path)
		}
		for k := range ref.Header() {
			if w.Header().Get(k) != ref.Header().Get(k) {
				t.Errorf("%s %s 响应头 %s 不一致: %q != %q", p.method, p.path, k, w.Header().Get(k), ref.Header().Get(k))
			}
		}
		if len(w.Header()) != len(ref.Header()) {
			t.Errorf("%s %s 响应头数量不一致: %v", p.method, p.path, w.Header())
		}
	}
}

func TestNoFingerprintHeaders(t *testing.T) {
	r := newTestRouter(t, testConfig())
	for _, path := range []string{"/api/testprefix/ping", "/nope"} {
		w := do(r, http.MethodGet, path, nil)
		for _, h := range []string{"X-Powered-By", "Server"} {
			if v := w.Header().Get(h); v != "" {
				t.Errorf("%s 不应输出响应头 %s=%s", path, h, v)
			}
		}
		if strings.Contains(strings.ToLower(w.Body.String()), "v2board") {
			t.Errorf("%s 响应体包含 v2board 字样", path)
		}
	}
}

func TestPing(t *testing.T) {
	r := newTestRouter(t, testConfig())
	w := do(r, http.MethodGet, "/api/testprefix/ping", nil)
	if w.Code != http.StatusOK {
		t.Fatalf("ping 状态码 %d", w.Code)
	}
	var body httpx.Body
	if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != 0 || body.Data != "pong" {
		t.Errorf("ping 响应不正确: %s", w.Body.String())
	}
}

func TestLegacyGroupSwitch(t *testing.T) {
	cfg := testConfig()
	r := newTestRouter(t, cfg)
	if r.Legacy != nil {
		t.Error("compat.v2board_api 关闭时 Legacy 分组应为 nil")
	}
	cfg.Compat.V2boardAPI = true
	r = newTestRouter(t, cfg)
	if r.Legacy == nil {
		t.Fatal("compat.v2board_api 开启时 Legacy 分组不应为 nil")
	}
	r.Legacy.GET("/guest/comm/config", func(c *gin.Context) { c.JSON(http.StatusOK, gin.H{"data": 1}) })
	if w := do(r, http.MethodGet, "/api/v1/guest/comm/config", nil); w.Code != http.StatusOK {
		t.Errorf("兼容层开启后接口应可访问，实际 %d", w.Code)
	}
}

func TestCORS(t *testing.T) {
	cfg := testConfig()
	cfg.CORS.AllowOrigins = []string{"https://user.example.com"}
	r := newTestRouter(t, cfg)

	preflight := map[string]string{
		"Origin":                        "https://user.example.com",
		"Access-Control-Request-Method": "POST",
	}
	w := do(r, http.MethodOptions, "/api/testprefix/ping", preflight)
	if w.Code != http.StatusNoContent || w.Header().Get("Access-Control-Allow-Origin") != "https://user.example.com" {
		t.Errorf("白名单来源预检失败: %d %v", w.Code, w.Header())
	}

	preflight["Origin"] = "https://evil.example.com"
	w = do(r, http.MethodOptions, "/api/testprefix/ping", preflight)
	if w.Code != http.StatusNotFound || w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("非白名单来源不应放行: %d %v", w.Code, w.Header())
	}

	preflight["Origin"] = "https://user.example.com"
	w = do(r, http.MethodOptions, "/api/v1/server/UniProxy/user", preflight)
	if w.Header().Get("Access-Control-Allow-Origin") != "" {
		t.Error("CORS 不应作用于原生 API 前缀之外的路径")
	}
}

func TestRecovery(t *testing.T) {
	r := newTestRouter(t, testConfig())
	r.API.GET("/panic", func(c *gin.Context) { panic("boom") })
	w := do(r, http.MethodGet, "/api/testprefix/panic", nil)
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("panic 应返回 500，实际 %d", w.Code)
	}
	if strings.Contains(w.Body.String(), "boom") || strings.Contains(w.Body.String(), "goroutine") {
		t.Error("panic 响应泄露了内部信息")
	}
}

func TestFailHidesInternalError(t *testing.T) {
	r := newTestRouter(t, testConfig())
	r.API.GET("/err", func(c *gin.Context) { httpx.Fail(c, errSecret{}) })
	r.API.GET("/biz", func(c *gin.Context) { httpx.Fail(c, httpx.BadRequest("参数错误")) })

	w := do(r, http.MethodGet, "/api/testprefix/err", nil)
	if w.Code != http.StatusInternalServerError || strings.Contains(w.Body.String(), "secret") {
		t.Errorf("内部错误细节不应返回给客户端: %d %s", w.Code, w.Body.String())
	}
	w = do(r, http.MethodGet, "/api/testprefix/biz", nil)
	var body httpx.Body
	_ = json.Unmarshal(w.Body.Bytes(), &body)
	if w.Code != http.StatusBadRequest || body.Code != httpx.CodeBadRequest || body.Msg != "参数错误" {
		t.Errorf("业务错误响应不正确: %d %s", w.Code, w.Body.String())
	}
}

type errSecret struct{}

func (errSecret) Error() string { return "secret db password leaked" }
