package web

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"testing/fstest"

	"go.uber.org/zap"

	"gvboard/internal/config"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/theme"
)

const secure = "webtestsecure0123456"

func themeDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "custom")
	files := map[string]string{
		theme.ManifestFile:   `{"name":"custom","configs":[{"label":"页脚","field_name":"footer","field_type":"textarea","default_value":"</script><b>x</b>"}]}`,
		theme.IndexFile:      "<!DOCTYPE html><HTML><HEAD><TITLE>占位</TITLE></HEAD><body><div id=\"app\"></div></body></HTML>",
		"assets/app-1a2b.js": "console.log(1)",
		"favicon.ico":        "ico",
		".env":               "secret",
		"assets/.git/config": "secret",
	}
	for name, body := range files {
		p := filepath.Join(dir, filepath.FromSlash(name))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return filepath.Dir(dir)
}

func newRouter(t *testing.T, mode string, vals map[string]any) *server.Router {
	t.Helper()
	base := map[string]any{
		"secure_path": secure, "app_name": "测试<站>", "app_description": "描述\"", "app_url": "https://shop.example.com",
		"frontend_theme": "custom", "logo": "https://shop.example.com/logo.png",
	}
	for k, v := range vals {
		base[k] = v
	}
	st := setting.NewStatic(base)
	cfg := &config.Config{Server: config.ServerConfig{Mode: mode}, API: config.APIConfig{Prefix: "/api/t1"}}
	r, err := server.NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	r.Mount(&Module{
		Settings: st,
		Themes:   theme.New(themeDir(t), st),
		admin: fstest.MapFS{
			"index.html":         {Data: []byte("<html><head><title></title></head><body></body></html>")},
			"assets/admin-9z.js": {Data: []byte("admin()")},
		},
	})
	return r
}

func get(r *server.Router, method, path, host string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, path, nil)
	if host != "" {
		req.Host = host
	}
	w := httptest.NewRecorder()
	r.Engine.ServeHTTP(w, req)
	return w
}

func TestUserSide(t *testing.T) {
	r := newRouter(t, config.ModeAll, nil)

	w := get(r, "GET", "/", "")
	body := w.Body.String()
	if w.Code != 200 || w.Header().Get("Content-Type") != "text/html; charset=utf-8" || w.Header().Get("Cache-Control") != "no-store" {
		t.Fatalf("首页响应错误：%d %v", w.Code, w.Header())
	}
	for _, want := range []string{
		"<TITLE>测试&lt;站&gt;</TITLE>",
		`<meta name="description" content="描述&#34;">`,
		`<script>window.APP_CONFIG={"api":"/api/t1",`,
		`"theme":{"config":{"footer":"\u003c/script\u003e\u003cb\u003ex\u003c/b\u003e"},"id":"custom"}`,
		`"name":"测试\u003c站\u003e"`,
		`"home":true`,
		`"register":{`,
		"</script></HEAD>",
	} {
		if !strings.Contains(body, want) {
			t.Fatalf("首页缺少 %s：%s", want, body)
		}
	}
	if strings.Contains(body, "</script><b>") || strings.Contains(body, secure) {
		t.Fatalf("首页注入内容未转义或泄露了管理端路径：%s", body)
	}
	if w := get(r, "HEAD", "/", ""); w.Code != 200 {
		t.Fatalf("HEAD 首页应 200，实际 %d", w.Code)
	}

	w = get(r, "GET", "/assets/app-1a2b.js", "")
	if w.Code != 200 || w.Body.String() != "console.log(1)" || w.Header().Get("Content-Type") != "text/javascript; charset=utf-8" ||
		!strings.Contains(w.Header().Get("Cache-Control"), "immutable") {
		t.Fatalf("静态文件响应错误：%d %v", w.Code, w.Header())
	}
	if w := get(r, "GET", "/favicon.ico", ""); w.Code != 200 || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("根目录文件响应错误：%d %v", w.Code, w.Header())
	}
	for _, p := range []string{
		"/theme.json", "/index.html", "/.env", "/assets/.git/config", "/assets", "/assets/", "/nope",
		"/assets/../theme.json", "/assets//app-1a2b.js", "/login", "/" + secure + "x",
	} {
		if w := get(r, "GET", p, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s 应 404，实际 %d", p, w.Code)
		}
	}
	if w := get(r, "POST", "/", ""); w.Code != http.StatusNotFound {
		t.Fatalf("POST 首页应 404，实际 %d", w.Code)
	}
}

func TestAdminSide(t *testing.T) {
	r := newRouter(t, config.ModeAll, nil)
	w := get(r, "GET", "/"+secure, "")
	if w.Code != http.StatusFound || w.Header().Get("Location") != "/"+secure+"/" {
		t.Fatalf("应跳转到带 / 的地址：%d %v", w.Code, w.Header())
	}
	w = get(r, "GET", "/"+secure+"/", "")
	body := w.Body.String()
	if w.Code != 200 || !strings.Contains(body, `"secure_path":"`+secure+`"`) || !strings.Contains(body, `"api":"/api/t1"`) ||
		!strings.Contains(body, "<title>测试&lt;站&gt;</title>") {
		t.Fatalf("管理端页面错误：%d %s", w.Code, body)
	}
	if w := get(r, "GET", "/"+secure+"/assets/admin-9z.js", ""); w.Code != 200 || w.Body.String() != "admin()" {
		t.Fatalf("管理端静态文件错误：%d", w.Code)
	}
	for _, p := range []string{"/" + secure + "/index.html", "/" + secure + "/nope", "/" + secure + "/assets/app-1a2b.js", "/assets/admin-9z.js"} {
		if w := get(r, "GET", p, ""); w.Code != http.StatusNotFound {
			t.Errorf("%s 应 404，实际 %d", p, w.Code)
		}
	}
}

func TestSafeMode(t *testing.T) {
	r := newRouter(t, config.ModeAll, map[string]any{"safe_mode_enable": int64(1)})
	for _, host := range []string{"shop.example.com", "SHOP.example.com:443"} {
		if w := get(r, "GET", "/", host); w.Code != 200 {
			t.Fatalf("站点域名 %s 应可访问，实际 %d", host, w.Code)
		}
	}
	for _, p := range []string{"/", "/assets/app-1a2b.js"} {
		if w := get(r, "GET", p, "1.2.3.4"); w.Code != http.StatusNotFound {
			t.Fatalf("其它域名访问 %s 应 404，实际 %d", p, w.Code)
		}
	}
	if w := get(r, "GET", "/"+secure+"/", "1.2.3.4"); w.Code != 200 {
		t.Fatalf("安全模式不影响管理端，实际 %d", w.Code)
	}
}

func TestSeparated(t *testing.T) {
	r := newRouter(t, config.ModeAPI, nil)
	for _, p := range []string{"/", "/assets/app-1a2b.js", "/" + secure, "/" + secure + "/"} {
		if w := get(r, "GET", p, ""); w.Code != http.StatusNotFound {
			t.Errorf("分离模式下 %s 应 404，实际 %d", p, w.Code)
		}
	}
}

func TestBuiltinPages(t *testing.T) {
	st := setting.NewStatic(map[string]any{"secure_path": secure, "app_name": "站点"})
	cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
	r, err := server.NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	r.Mount(&Module{Settings: st, Themes: theme.New("", st)})
	for _, p := range []string{"/", "/" + secure + "/"} {
		w := get(r, "GET", p, "")
		if w.Code != 200 || !strings.Contains(w.Body.String(), "<title>站点</title>") ||
			strings.Contains(strings.ToLower(w.Body.String()), "v2board") {
			t.Fatalf("内置页面 %s 错误：%d %s", p, w.Code, w.Body)
		}
	}
}

func TestInject(t *testing.T) {
	out, err := Inject([]byte("<div>无 head</div>"), "", "", map[string]any{"a": "<"})
	if err != nil || string(out) != `<script>window.APP_CONFIG={"a":"\u003c"};</script><div>无 head</div>` {
		t.Fatalf("无 head 时应插在开头：%s %v", out, err)
	}
	out, _ = Inject([]byte("<head><title>旧</title></head>"), "", "", nil)
	if !strings.Contains(string(out), "<title>旧</title>") {
		t.Fatalf("标题为空时应保留原标题：%s", out)
	}
	out, _ = Inject([]byte(`<head><title data-x="1">旧</title></head>`), "新&", "", nil)
	if !strings.Contains(string(out), `<title data-x="1">新&amp;</title>`) {
		t.Fatalf("标题替换错误：%s", out)
	}
}
