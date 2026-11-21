package plugin

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime/multipart"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"net/url"
	"sort"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/config"
	"gvboard/internal/hook"
	"gvboard/internal/model"
	"gvboard/internal/phpx"
	"gvboard/internal/server"
)

func makeZip(t testing.TB, files map[string]string) []byte {
	t.Helper()
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	for _, n := range names {
		w, err := zw.Create(n)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := w.Write([]byte(files[n])); err != nil {
			t.Fatal(err)
		}
	}
	if err := zw.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

func simpleManifest(code, version string) string {
	return fmt.Sprintf(`{"code":%q,"name":"测试","version":%q}`, code, version)
}

func TestReadPackage(t *testing.T) {
	big := strings.Repeat("0", maxFileSize+1)
	many := map[string]string{"plugin.json": simpleManifest("many", "1"), "main.js": ""}
	for i := 0; i < maxPackageFiles; i++ {
		many[fmt.Sprintf("f%d.txt", i)] = ""
	}
	huge := map[string]string{"plugin.json": simpleManifest("huge", "1"), "main.js": ""}
	for i := 0; i < 5; i++ {
		huge[fmt.Sprintf("f%d.txt", i)] = strings.Repeat("0", maxFileSize-10)
	}
	bad := map[string]map[string]string{
		"缺少 plugin.json": {"main.js": ""},
		"未知字段":           {"plugin.json": `{"code":"ab","name":"x","version":"1","foo":1}`, "main.js": ""},
		"标识无效":           {"plugin.json": simpleManifest("Ab", "1"), "main.js": ""},
		"版本无效":           {"plugin.json": simpleManifest("ab", ""), "main.js": ""},
		"缺少名称":           {"plugin.json": `{"code":"ab","version":"1"}`, "main.js": ""},
		"缺少入口":           {"plugin.json": simpleManifest("ab", "1")},
		"入口不是 js":        {"plugin.json": `{"code":"ab","name":"x","version":"1","main":"a.txt"}`, "a.txt": ""},
		"入口越界":           {"plugin.json": `{"code":"ab","name":"x","version":"1","main":"../a.js"}`, "main.js": ""},
		"IP 域名":          {"plugin.json": `{"code":"ab","name":"x","version":"1","http":["127.0.0.1"]}`, "main.js": ""},
		"端口无效":           {"plugin.json": `{"code":"ab","name":"x","version":"1","http":["a.test:0"]}`, "main.js": ""},
		"带协议的域名":         {"plugin.json": `{"code":"ab","name":"x","version":"1","http":["https://a.test"]}`, "main.js": ""},
		"未知权限":           {"plugin.json": `{"code":"ab","name":"x","version":"1","permissions":["db.sql"]}`, "main.js": ""},
		"配置项无效":          {"plugin.json": `{"code":"ab","name":"x","version":"1","config":[{"key":"a","type":"x"}]}`, "main.js": ""},
		"语法错误":           {"plugin.json": simpleManifest("ab", "1"), "main.js": "function ("},
		"非 UTF-8":        {"plugin.json": simpleManifest("ab", "1"), "main.js": "var a = '\xff';"},
		"路径越界":           {"plugin.json": simpleManifest("ab", "1"), "main.js": "", "../x.js": ""},
		"反斜杠路径":          {"plugin.json": simpleManifest("ab", "1"), "main.js": "", `a\b.js`: ""},
		"文件过大":           {"plugin.json": simpleManifest("ab", "1"), "main.js": "", "big.txt": big},
		"文件过多":           many,
		"解压后过大":          huge,
	}
	for name, files := range bad {
		if _, err := readPackage(makeZip(t, files)); err == nil {
			t.Errorf("%s：应校验失败", name)
		}
	}
	for name, raw := range map[string][]byte{"空": nil, "非 zip": []byte("hello"), "过大": make([]byte, maxPackageSize+1)} {
		if _, err := readPackage(raw); err == nil {
			t.Errorf("%s：应校验失败", name)
		}
	}

	raw := makeZip(t, map[string]string{
		"demo/plugin.json": `{"code":"demo","name":"演示","version":"1.0.0",
			"http":["API.gvb.test","api.gvb.test","*.gvb.test:8443"],"permissions":["user.read","order.read","user.read"]}`,
		"demo/main.js":     "require('./lib/a');",
		"demo/lib/a.js":    "module.exports = 1;",
		"demo/readme.txt":  "x",
		"demo/conf/x.json": "{}",
	})
	p, err := readPackage(raw)
	if err != nil {
		t.Fatal(err)
	}
	if p.manifest.Main != "main.js" || len(p.programs) != 2 || p.programs["lib/a.js"] == nil || p.files["conf/x.json"] == nil {
		t.Fatalf("顶级目录未去掉或模块未编译：%v %v", p.manifest.Main, p.programs)
	}
	if strings.Join(p.manifest.HTTP, ",") != "api.gvb.test,*.gvb.test:8443" || strings.Join(p.manifest.Permissions, ",") != "order.read,user.read" {
		t.Fatalf("域名或权限未规范化：%v %v", p.manifest.HTTP, p.manifest.Permissions)
	}
	if len(p.sha256) != 64 || p.size != int64(len(raw)) {
		t.Fatalf("sha256 或大小错误：%s %d", p.sha256, p.size)
	}
}

func TestDomainAndBlockedIP(t *testing.T) {
	exact, _ := parseDomain("api.gvb.test")
	wild, _ := parseDomain("*.gvb.test:8443")
	for _, c := range []struct {
		d                  domain
		host, port, scheme string
		ok                 bool
	}{
		{exact, "api.gvb.test", "", "https", true},
		{exact, "api.gvb.test", "80", "http", true},
		{exact, "api.gvb.test", "8080", "http", false},
		{exact, "x.api.gvb.test", "", "https", false},
		{exact, "gvb.test", "", "https", false},
		{wild, "a.gvb.test", "8443", "https", true},
		{wild, "a.b.gvb.test", "8443", "https", true},
		{wild, "gvb.test", "8443", "https", false},
		{wild, "a.gvb.test", "", "https", false},
		{wild, "evilgvb.test", "8443", "https", false},
	} {
		if got := c.d.match(c.host, c.port, c.scheme); got != c.ok {
			t.Errorf("%s 匹配 %s:%s 应为 %v", c.d, c.host, c.port, c.ok)
		}
	}
	for _, s := range []string{"127.0.0.1", "10.1.2.3", "172.16.0.1", "192.168.1.1", "169.254.169.254", "0.0.0.0", "100.64.0.1",
		"224.0.0.1", "::1", "::", "fe80::1", "fc00::1", "::ffff:127.0.0.1", "64:ff9b::a00:1", "2002:a00:1::1"} {
		if !blockedIP(netip.MustParseAddr(s)) {
			t.Errorf("%s 应被拦截", s)
		}
	}
	for _, s := range []string{"8.8.8.8", "1.1.1.1", "2001:4860:4860::8888"} {
		if blockedIP(netip.MustParseAddr(s)) {
			t.Errorf("%s 不应被拦截", s)
		}
	}
}

func TestJSHTTP(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/echo":
			b, _ := io.ReadAll(r.Body)
			w.Header().Set("X-Reply", "1")
			_ = json.NewEncoder(w).Encode(map[string]string{"method": r.Method, "a": r.Header.Get("X-A"), "ct": r.Header.Get("Content-Type"), "body": string(b)})
		case "/redir":
			http.Redirect(w, r, "http://evil.test/echo", http.StatusFound)
		case "/redir-ok":
			http.Redirect(w, r, "/echo", http.StatusFound)
		case "/loop":
			http.Redirect(w, r, "/loop", http.StatusFound)
		case "/big":
			_, _ = w.Write(bytes.Repeat([]byte("x"), httpMaxResponse+1))
		case "/slow":
			time.Sleep(300 * time.Millisecond)
		}
	}))
	defer srv.Close()
	d, _ := parseDomain("api.gvb.test")
	h := newJSHTTP([]domain{d}, true)
	addr := srv.Listener.Addr().String()
	h.client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, network, addr)
	}
	ctx := context.Background()
	resp, err := h.do(ctx, httpRequest{Method: "post", URL: "http://api.gvb.test/echo", Body: "hi",
		Headers: map[string]string{"X-A": "b", "Host": "evil.test"}})
	if err != nil || resp.Status != 200 || resp.Headers["x-reply"] != "1" ||
		!strings.Contains(resp.Body, `"method":"POST"`) || !strings.Contains(resp.Body, `"a":"b"`) || !strings.Contains(resp.Body, `"body":"hi"`) {
		t.Fatalf("请求错误：%+v %v", resp, err)
	}
	if resp, err := h.do(ctx, httpRequest{URL: "http://api.gvb.test/redir-ok"}); err != nil || !strings.Contains(resp.Body, `"method":"GET"`) {
		t.Fatalf("同域重定向应成功：%+v %v", resp, err)
	}
	for _, c := range []struct {
		r    httpRequest
		want string
	}{
		{httpRequest{URL: "http://api.gvb.test/redir"}, "未声明"},
		{httpRequest{URL: "http://api.gvb.test/loop"}, "重定向"},
		{httpRequest{URL: "http://api.gvb.test/big"}, "超过"},
		{httpRequest{URL: "http://api.gvb.test/slow", Timeout: 50 * time.Millisecond}, "请求失败"},
		{httpRequest{URL: "http://other.test/echo"}, "未声明"},
		{httpRequest{URL: "http://api.gvb.test:8080/echo"}, "未声明"},
		{httpRequest{URL: "ftp://api.gvb.test/echo"}, "http"},
		{httpRequest{URL: "http://u:p@api.gvb.test/echo"}, "用户名"},
		{httpRequest{URL: "http://api.gvb.test/echo", Method: "TRACE"}, "不支持"},
		{httpRequest{URL: "http://api.gvb.test/echo", Body: strings.Repeat("x", httpMaxRequest+1)}, "不能超过"},
	} {
		if _, err := h.do(ctx, c.r); err == nil || !strings.Contains(err.Error(), c.want) {
			t.Errorf("%s 应失败（%s）：%v", c.r.URL, c.want, err)
		}
	}

	strict := newJSHTTP([]domain{d}, false)
	if _, err := strict.client.Transport.(*http.Transport).DialContext(ctx, "tcp", addr); !errors.Is(err, errBlockedAddr) {
		t.Fatalf("连接本机地址应被拦截：%v", err)
	}
}

// jsEnv JS 插件测试环境：管理员、普通用户（含主订阅）与路由。
type jsEnv struct {
	*env
	r              *server.Router
	secure         string
	adminTok, uTok string
	normal         *model.User
	normalSub      *model.Subscription
	adminID        int64
	normalEmail    string
}

func newJSEnv(t *testing.T, e *env, m *Manager) *jsEnv {
	t.Helper()
	ctx := context.Background()
	je := &jsEnv{env: e, secure: "plugintest0123456789", normalEmail: "n@x.com"}
	if err := e.settings.Set(ctx, map[string]any{"secure_path": je.secure}); err != nil {
		t.Fatal(err)
	}
	if err := e.db.Transaction(func(tx *gorm.DB) error {
		admin, _, err := account.Create(tx, account.NewUser{Email: "admin@x.com", Password: "password123"})
		if err != nil {
			return err
		}
		je.adminID = admin.ID
		if je.normal, je.normalSub, err = account.Create(tx, account.NewUser{Email: je.normalEmail, Password: "password123"}); err != nil {
			return err
		}
		return tx.Model(admin).Update("is_admin", 1).Error
	}); err != nil {
		t.Fatal(err)
	}
	je.adminTok, _ = e.auth.Issue(ctx, je.adminID, "", "")
	je.uTok, _ = e.auth.Issue(ctx, je.normal.ID, "", "")
	cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
	r, err := server.NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	r.Mount(m)
	je.r = r
	return je
}

func (je *jsEnv) do(method, path, ct string, body io.Reader, tok string, headers ...string) *httptest.ResponseRecorder {
	req := httptest.NewRequest(method, "/api/t1/"+path, body)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	if tok != "" {
		req.Header.Set("Authorization", tok)
	}
	for i := 0; i+1 < len(headers); i += 2 {
		req.Header.Set(headers[i], headers[i+1])
	}
	w := httptest.NewRecorder()
	je.r.Engine.ServeHTTP(w, req)
	return w
}

func (je *jsEnv) upload(raw []byte, sha string) *httptest.ResponseRecorder {
	var buf bytes.Buffer
	mw := multipart.NewWriter(&buf)
	fw, _ := mw.CreateFormFile("file", "plugin.zip")
	_, _ = fw.Write(raw)
	if sha != "" {
		_ = mw.WriteField("sha256", sha)
	}
	_ = mw.Close()
	return je.do("POST", je.secure+"/plugins/upload", mw.FormDataContentType(), &buf, je.adminTok)
}

// install 预览并安装。
func install(t *testing.T, m *Manager, raw []byte) *View {
	t.Helper()
	ctx := context.Background()
	pv, err := m.Preview(ctx, raw)
	if err != nil {
		t.Fatalf("预览失败：%v", err)
	}
	v, err := m.Install(ctx, raw, pv.SHA256)
	if err != nil {
		t.Fatalf("安装失败：%v", err)
	}
	return v
}

func kvGet(m *Manager, code, k string) string {
	v, _ := (&kv{m: m, code: code}).Get(context.Background(), k)
	return v
}

func jsOf(t *testing.T, m *Manager, code string) *jsPlugin {
	t.Helper()
	e := m.entry(code)
	if e == nil {
		t.Fatalf("插件 %s 未加载", code)
	}
	jp, ok := e.plugin.(*jsPlugin)
	if !ok {
		t.Fatalf("插件 %s 不是 JS 插件", code)
	}
	return jp
}

func waitFor(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("等待超时：%s", what)
		}
		time.Sleep(30 * time.Millisecond)
	}
}

func versionPkg(t *testing.T, code, version, reply string) []byte {
	return makeZip(t, map[string]string{
		"plugin.json": fmt.Sprintf(`{"code":%q,"name":"版本测试","version":%q,"http":["api.gvb.test"],"permissions":["user.read"],
			"config":[{"key":"n","type":"number","default":1}]}`, code, version),
		"main.js": fmt.Sprintf(`plugin.route('guest', 'GET', '/v', function () { return %q; });`, reply),
	})
}

func TestJSInstall(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		m := e.manager(t, &fake{code: "gobuilt"})
		m2 := e.manager(t)
		wctx, cancel := context.WithCancel(ctx)
		defer cancel()
		go m2.Watch(wctx)
		time.Sleep(200 * time.Millisecond)

		v1 := versionPkg(t, "jsx", "1.0.0", "one")
		pv, err := m.Preview(ctx, v1)
		if err != nil {
			t.Fatal(err)
		}
		if pv.Code != "jsx" || pv.InstalledVersion != "" || len(pv.SHA256) != 64 || len(pv.HTTP) != 1 ||
			len(pv.Permissions) != 1 || pv.Permissions[0].Description == "" || len(pv.Fields) != 1 {
			t.Fatalf("预览信息错误：%+v", pv)
		}
		if _, err := m.Install(ctx, v1, strings.Repeat("0", 64)); !errors.Is(err, errPackageChanged) {
			t.Fatalf("sha256 不一致应拒绝：%v", err)
		}
		if _, err := m.Preview(ctx, versionPkg(t, "gobuilt", "1", "x")); !errors.Is(err, errBuiltinConflict) {
			t.Fatalf("与内置插件冲突应拒绝：%v", err)
		}
		if _, err := m.Preview(ctx, []byte("bad")); err == nil {
			t.Fatal("无效安装包应拒绝")
		}
		var n int64
		e.db.Model(&model.Plugin{}).Where("code = ?", "jsx").Count(&n)
		if n != 0 {
			t.Fatal("预览不应安装")
		}

		v := install(t, m, v1)
		if v.Type != model.PluginTypeJS || v.Status != StatusOK || v.Enabled || v.Package == nil || v.Package.SHA256 != pv.SHA256 ||
			len(v.Features.Routes) != 1 || v.Version != "1.0.0" {
			t.Fatalf("安装结果错误：%+v", v)
		}
		waitFor(t, "第二个实例加载 JS 插件", func() bool { e := m2.entry("jsx"); return e != nil && e.sha == pv.SHA256 })
		if err := m.SaveConfig(ctx, "jsx", map[string]any{"n": 5}); err != nil {
			t.Fatal(err)
		}
		mustEnable(t, m, "jsx")
		old := jsOf(t, m, "jsx")

		v2 := versionPkg(t, "jsx", "2.0.0", "two")
		if pv, err := m.Preview(ctx, v2); err != nil || pv.InstalledVersion != "1.0.0" {
			t.Fatalf("更新预览应显示已安装版本：%+v %v", pv, err)
		}
		v = install(t, m, v2)
		if !v.Enabled || v.Version != "2.0.0" || m.config("jsx").Int("n") != 5 {
			t.Fatalf("更新应保留启用状态与配置：%+v", v)
		}
		if !old.closed.Load() {
			t.Fatal("旧版本运行时应释放")
		}
		waitFor(t, "第二个实例更新 JS 插件", func() bool {
			e := m2.entry("jsx")
			return e != nil && e.info.Version == "2.0.0" && m2.Enabled("jsx")
		})
		je := newJSEnv(t, e, m2)
		if w := je.do("GET", "plugin/jsx/v", "", nil, ""); w.Code != 200 || !strings.Contains(w.Body.String(), "two") {
			t.Fatalf("第二个实例应使用新版本：%d %s", w.Code, w.Body)
		}
		var pkgs []model.PluginPackage
		e.db.Find(&pkgs)
		if len(pkgs) != 1 || pkgs[0].SHA256 != v.Package.SHA256 {
			t.Fatalf("安装包记录错误：%d", len(pkgs))
		}

		if err := m.Delete(ctx, "jsx"); !errors.Is(err, errNotDisabled) {
			t.Fatalf("启用中的插件不能删除：%v", err)
		}
		if err := m.Disable(ctx, "jsx"); err != nil {
			t.Fatal(err)
		}
		cur := jsOf(t, m, "jsx")
		_ = (&kv{m: m, code: "jsx"}).Set(ctx, "a", "b", 0)
		if err := m.Delete(ctx, "jsx"); err != nil {
			t.Fatal(err)
		}
		if _, err := m.Get(ctx, "jsx"); !errors.Is(err, errNotFound) || !cur.closed.Load() {
			t.Fatalf("删除后应卸载：%v", err)
		}
		e.db.Model(&model.PluginPackage{}).Count(&n)
		var kvn int64
		e.db.Model(&model.PluginKV{}).Where("plugin_code = ?", "jsx").Count(&kvn)
		if n != 0 || kvn != 0 {
			t.Fatalf("删除后安装包与 KV 应清除：%d %d", n, kvn)
		}
		waitFor(t, "第二个实例卸载 JS 插件", func() bool { return m2.entry("jsx") == nil })
		if w := je.do("GET", "plugin/jsx/v", "", nil, ""); w.Code != http.StatusNotFound {
			t.Fatalf("删除后路由应 404：%d", w.Code)
		}

		// 数据库中损坏的安装包：标记为加载失败
		other := versionPkg(t, "other", "1", "x")
		for code, archive := range map[string]string{"jsbad": "!!!", "jsmis": base64.StdEncoding.EncodeToString(other)} {
			e.db.Create(&model.Plugin{Code: code, Type: model.PluginTypeJS, Version: "1"})
			e.db.Create(&model.PluginPackage{PluginCode: code, SHA256: "x", Archive: archive})
		}
		if err := m.Reload(ctx); err != nil {
			t.Fatal(err)
		}
		for _, code := range []string{"jsbad", "jsmis"} {
			if v := statusOf(t, m, code); v.Status != StatusError || v.Type != model.PluginTypeJS {
				t.Errorf("%s 应加载失败：%+v", code, v)
			}
			if err := m.Enable(ctx, code); err == nil {
				t.Errorf("%s 加载失败时不应能启用", code)
			}
			if err := m.Delete(ctx, code); err != nil {
				t.Errorf("加载失败的 JS 插件应能删除：%v", err)
			}
		}
	})
}

const demoMain = `
var util = require('./lib/util');
var meta = require('./data.json');
plugin.onEnable(function () {
  if (plugin.config().token === 'bad') throw new Error('token 无效');
  kv.set('enabled', '1');
});
plugin.onDisable(function () { kv.set('enabled', '0'); });
plugin.on('user.registered', function (ev) { kv.set('reg:' + ev.data.user_id, ev.name, 60); });
plugin.on('order.paid', function () { throw new Error('处理失败'); });
plugin.filter('order.amount', 10, function (v) { var old = v.amount; v.amount = Math.floor(v.amount * plugin.config().rate / 100); v.items.push({ label: '九折', amount: v.amount - old }); v.user_id = 1; return v; });
plugin.filter('plan.badges', function (v) { v.plans.forEach(function (p) { p.badges.push('JS 标签'); }); return v; });
plugin.filter('knowledge.article', function (v) { v.body = v.body.replace('{x}', v.available ? '用户' + v.user_id : '无'); v.user_id = 9; return v; });
plugin.filter('order.amount', 1, function (v) { if (v.user_id === 999) { while (true) {} } });
plugin.filter('mail.message', function (m) { m.subject = '[' + meta.tag + '] ' + m.subject; return m; });
plugin.filter('subscribe.content', function (c) { c.body = c.body + '\n#' + util.upper(c.client); c.headers.push(['x-demo', '1']); return c; });
plugin.filter('subscribe.servers', function (s) { s.servers = s.servers.filter(function (x) { return x.name !== 'hide'; }); return s; });
plugin.route('guest', 'GET', '/ping/:name', function (req) {
  return { name: req.params.name, q: req.query.a, h: req.headers['x-test'], auth: req.headers['authorization'] || null,
    sum: crypto.sha256('a'), hm: crypto.hmacSHA256('k', 'v').length, b64: crypto.base64Decode(crypto.base64Encode('中文')),
    hex: crypto.randomHex(4).length, code: plugin.code, ver: plugin.info.version };
});
plugin.route('guest', 'POST', '/echo', function (req) {
  return plugin.response({ status: 201, headers: { 'X-Demo': 'yes', 'Bad\r\nHeader': 'x' }, body: req.json().msg, content_type: 'text/plain' });
});
plugin.route('guest', 'GET', '/fail', function () { return plugin.fail(403, '禁止访问'); });
plugin.route('guest', 'GET', '/throw', function () { throw new Error('boom'); });
plugin.route('guest', 'GET', '/pending', function () { return new Promise(function () {}); });
plugin.route('guest', 'GET', '/resolved', async function () { return 'ok'; });
plugin.route('guest', 'GET', '/late', function () { try { plugin.on('user.login', function () {}); } catch (e) { return e.message; } });
plugin.route('guest', 'GET', '/kv', function () { kv.set('a', '1'); var ok = kv.setnx('a', '2'); var v = kv.get('a'); kv.del('a'); return [ok, v, kv.get('a')]; });
plugin.route('guest', 'GET', '/http', function () { try { http.get('https://other.test/'); } catch (e) { return e.message; } });
plugin.route('user', 'GET', '/me', function (req) { return req.user; });
plugin.route('admin', 'POST', '/data', function (req) {
  var b = req.json(), r = {};
  r.user = users.get(b.uid);
  r.byEmail = users.findByEmail(' N@X.com ').id;
  r.none = users.get(999999);
  r.balance = users.addBalance(b.uid, 500, '活动奖励');
  try { users.addBalance(b.uid, -100000); } catch (e) { r.neg = e.message; }
  try { users.addBalance(b.uid, 1.5); } catch (e) { r.frac = e.message; }
  r.subs = subscriptions.list(b.uid);
  subscriptions.addTraffic(b.sid, 1024);
  try { subscriptions.addTraffic(b.sid, -1); } catch (e) { r.negTraffic = e.message; }
  r.extended = subscriptions.extend(b.sid, 3600);
  r.sub = subscriptions.get(b.sid);
  r.orders = orders.list(b.uid, 10).length;
  r.order = orders.get('nope');
  return r;
});
plugin.job('tick', '*/5 * * * *', function () { kv.set('tick', String(Number(kv.get('tick') || 0) + 1)); });
`

func demoPkg(t *testing.T) []byte {
	return makeZip(t, map[string]string{
		"plugin.json": `{"code":"jsdemo","name":"演示插件","version":"1.0.0","http":["api.gvb.test"],
			"permissions":["user.read","user.balance","subscription.read","subscription.write","order.read"],
			"config":[{"key":"token","type":"string"},{"key":"rate","type":"number","default":90}]}`,
		"main.js":      demoMain,
		"lib/util.js":  "exports.upper = function (s) { return s.toUpperCase(); };",
		"data.json":    `{"tag":"GVB"}`,
		"assets/a.txt": "not a module",
	})
}

func TestJSRuntime(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		m := e.manager(t)
		je := newJSEnv(t, e, m)
		if v := install(t, m, demoPkg(t)); v.Status != StatusOK {
			t.Fatalf("安装失败：%+v", v)
		}
		if v := statusOf(t, m, "jsdemo"); len(v.Features.Events) != 2 || len(v.Features.Filters) != 6 || len(v.Features.Routes) != 11 || len(v.Features.Jobs) != 1 {
			t.Fatalf("注册内容错误：%+v", v.Features)
		}
		if err := m.SaveConfig(ctx, "jsdemo", map[string]any{"token": "bad"}); err != nil {
			t.Fatal(err)
		}
		if err := m.Enable(ctx, "jsdemo"); err == nil || !strings.Contains(err.Error(), "token 无效") || m.Enabled("jsdemo") {
			t.Fatalf("onEnable 抛出错误时不应启用：%v", err)
		}
		if err := m.SaveConfig(ctx, "jsdemo", map[string]any{"token": "good"}); err != nil {
			t.Fatal(err)
		}
		mustEnable(t, m, "jsdemo")
		if kvGet(m, "jsdemo", "enabled") != "1" {
			t.Fatal("onEnable 未执行")
		}

		// 过滤器
		in := hook.OrderAmount{UserID: 7, Amount: 1000}
		if out := hook.Apply(m, ctx, hook.FilterOrderAmount, in); out.Amount != 900 || out.UserID != 7 ||
			len(out.Items) != 1 || out.Items[0].Label != "九折" || out.Items[0].Amount != -100 {
			t.Fatalf("订单金额过滤错误（只能修改金额与追加明细）：%+v", out)
		}
		badges := hook.Apply(m, ctx, hook.FilterPlanBadges, hook.PlanBadges{UserID: 1, Plans: []hook.PlanBadge{{PlanID: 3}}})
		if len(badges.Plans) != 1 || len(badges.Plans[0].Badges) != 1 || badges.Plans[0].Badges[0] != "JS 标签" {
			t.Fatalf("套餐标签过滤错误：%+v", badges)
		}
		art := hook.Apply(m, ctx, hook.FilterKnowledgeArticle, hook.KnowledgeArticle{UserID: 3, ArticleID: 1, Available: true, Body: "a{x}b"})
		if art.Body != "a用户3b" || art.UserID != 3 {
			t.Fatalf("知识库文章过滤错误（只能修改正文）：%+v", art)
		}
		for i := 0; i < 2; i++ {
			start := time.Now()
			if out := hook.Apply(m, ctx, hook.FilterOrderAmount, hook.OrderAmount{UserID: 999, Amount: 1000}); out.Amount != 900 {
				t.Fatalf("死循环的过滤器应被中断并跳过：%+v", out)
			}
			if d := time.Since(start); d > 2*time.Second {
				t.Fatalf("中断耗时过长：%s", d)
			}
		}
		if mail := hook.Apply(m, ctx, hook.FilterMailMessage, hook.MailMessage{To: "a@b.c", Subject: "Hi", HTML: "x"}); mail.Subject != "[GVB] Hi" {
			t.Fatalf("邮件过滤错误：%+v", mail)
		}
		content := hook.Apply(m, ctx, hook.FilterSubscribeContent, hook.SubscribeContent{UserID: 1, Client: "clash", Body: []byte("body"),
			Headers: [][2]string{{"a", "b"}}})
		if string(content.Body) != "body\n#CLASH" || len(content.Headers) != 2 || content.Headers[1] != [2]string{"x-demo", "1"} {
			t.Fatalf("订阅内容过滤错误：%+v", content)
		}
		servers := hook.Apply(m, ctx, hook.FilterSubscribeServers, hook.SubscribeServers{UserID: 1,
			Servers: []*phpx.Arr{phpx.New().Set("name", "a").Set("port", 443), phpx.New().Set("name", "hide")}})
		if len(servers.Servers) != 1 || servers.Servers[0].Get("name") != "a" {
			t.Fatalf("节点过滤错误：%+v", servers)
		}

		// 事件
		hook.Emit(m, ctx, hook.EventUserRegistered, hook.User{UserID: 7})
		if kvGet(m, "jsdemo", "reg:7") != "user.registered" {
			t.Fatal("事件处理函数未执行")
		}
		if err := m.handleEvent(ctx, eventTask{Plugin: "jsdemo", Event: hook.EventOrderPaid, Data: json.RawMessage(`{}`)}); err == nil ||
			!strings.Contains(err.Error(), "处理失败") {
			t.Fatalf("事件处理失败应返回错误以便重试：%v", err)
		}

		// 定时任务
		for _, j := range m.Jobs() {
			if strings.HasPrefix(j.Name, "plugin.jsdemo") {
				t.Fatal("JS 插件的定时任务不应注册到调度器")
			}
		}
		if err := m.runJSJobs(ctx); err != nil || kvGet(m, "jsdemo", "tick") != "1" {
			t.Fatalf("到期的定时任务应执行：%v", err)
		}
		e.now = e.now.Add(time.Minute)
		_ = m.runJSJobs(ctx)
		if kvGet(m, "jsdemo", "tick") != "1" {
			t.Fatal("未到期的定时任务不应执行")
		}

		// 路由
		w := je.do("GET", "plugin/jsdemo/ping/bob?a=1", "", nil, je.uTok, "X-Test", "t")
		var ping struct {
			Data map[string]any `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &ping)
		want := map[string]any{"name": "bob", "q": "1", "h": "t", "auth": nil, "hm": float64(64), "b64": "中文", "hex": float64(8),
			"sum": "ca978112ca1bbdcafac231b39a23dc4da786eff8147c4e72b9807785afee48bb", "code": "jsdemo", "ver": "1.0.0"}
		for k, v := range want {
			if ping.Data[k] != v {
				t.Errorf("ping.%s = %v，应为 %v（%s）", k, ping.Data[k], v, w.Body)
			}
		}
		if w := je.do("POST", "plugin/jsdemo/echo", "application/json", strings.NewReader(`{"msg":"hello"}`), ""); w.Code != 201 ||
			w.Body.String() != "hello" || w.Header().Get("X-Demo") != "yes" || w.Header().Get("Bad") != "" || w.Header().Get("Content-Type") != "text/plain" {
			t.Fatalf("原始响应错误：%d %s %v", w.Code, w.Body, w.Header())
		}
		if w := je.do("GET", "plugin/jsdemo/fail", "", nil, ""); w.Code != 403 || !strings.Contains(w.Body.String(), "禁止访问") {
			t.Fatalf("fail 响应错误：%d %s", w.Code, w.Body)
		}
		for _, p := range []string{"throw", "pending"} {
			if w := je.do("GET", "plugin/jsdemo/"+p, "", nil, ""); w.Code != 500 || strings.Contains(w.Body.String(), "boom") {
				t.Errorf("%s 应返回 500 且不暴露错误：%d %s", p, w.Code, w.Body)
			}
		}
		if w := je.do("GET", "plugin/jsdemo/resolved", "", nil, ""); w.Code != 200 || !strings.Contains(w.Body.String(), `"ok"`) {
			t.Fatalf("async 函数结果错误：%d %s", w.Code, w.Body)
		}
		if w := je.do("GET", "plugin/jsdemo/late", "", nil, ""); !strings.Contains(w.Body.String(), "只能在插件加载时") {
			t.Fatalf("加载后不应能注册：%s", w.Body)
		}
		if w := je.do("GET", "plugin/jsdemo/kv", "", nil, ""); !strings.Contains(w.Body.String(), `[false,"1",null]`) {
			t.Fatalf("kv 结果错误：%s", w.Body)
		}
		if w := je.do("GET", "plugin/jsdemo/http", "", nil, ""); !strings.Contains(w.Body.String(), "未声明") {
			t.Fatalf("访问未声明的域名应失败：%s", w.Body)
		}
		if w := je.do("GET", "plugin/jsdemo/me", "", nil, ""); w.Code == 200 {
			t.Fatal("用户路由未登录不应成功")
		}
		if w := je.do("GET", "plugin/jsdemo/me", "", nil, je.uTok); w.Code != 200 || !strings.Contains(w.Body.String(), je.normalEmail) {
			t.Fatalf("用户路由错误：%d %s", w.Code, w.Body)
		}
		if w := je.do("POST", je.secure+"/plugin/jsdemo/data", "", nil, je.uTok); w.Code != http.StatusNotFound {
			t.Fatalf("普通用户访问管理端路由应 404：%d", w.Code)
		}

		// 业务数据接口
		body := fmt.Sprintf(`{"uid":%d,"sid":%d}`, je.normal.ID, je.normalSub.ID)
		w = je.do("POST", je.secure+"/plugin/jsdemo/data", "application/json", strings.NewReader(body), je.adminTok)
		var data struct {
			Data struct {
				User       map[string]any   `json:"user"`
				ByEmail    int64            `json:"byEmail"`
				None       any              `json:"none"`
				Balance    int64            `json:"balance"`
				Neg        string           `json:"neg"`
				Frac       string           `json:"frac"`
				Subs       []map[string]any `json:"subs"`
				NegTraffic string           `json:"negTraffic"`
				Extended   bool             `json:"extended"`
				Sub        struct {
					TransferEnable int64 `json:"transfer_enable"`
					ExpiredAt      int64 `json:"expired_at"`
				} `json:"sub"`
				Orders int `json:"orders"`
				Order  any `json:"order"`
			} `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &data); err != nil || w.Code != 200 {
			t.Fatalf("数据接口错误：%d %s", w.Code, w.Body)
		}
		d := data.Data
		if d.User["email"] != je.normalEmail || d.ByEmail != je.normal.ID || d.None != nil || d.Balance != 500 ||
			!strings.Contains(d.Neg, "余额不足") || !strings.Contains(d.Frac, "整数") || len(d.Subs) != 1 || d.NegTraffic == "" ||
			!d.Extended || d.Sub.TransferEnable != 1024 || d.Sub.ExpiredAt != e.now.Unix()+3600 || d.Orders != 0 || d.Order != nil {
			t.Fatalf("数据接口结果错误：%s", w.Body)
		}
		for _, k := range []string{"password", "token", "uuid", "remember"} {
			if strings.Contains(strings.ToLower(w.Body.String()), `"`+k) {
				t.Fatalf("数据接口不应返回 %s：%s", k, w.Body)
			}
		}
		var u model.User
		e.db.Select("balance").Where("id = ?", je.normal.ID).Take(&u)
		if u.Balance != 500 {
			t.Fatalf("余额应为 500：%d", u.Balance)
		}
		var logs []model.BalanceLog
		e.db.Where("user_id = ?", je.normal.ID).Find(&logs)
		if len(logs) != 1 || logs[0].Type != "plugin" || logs[0].Plugin != "jsdemo" || logs[0].Amount != 500 || logs[0].Remark != "活动奖励" {
			t.Fatalf("插件调整余额应只记录成功的一次：%+v", logs)
		}

		// 并发：运行时池
		var wg sync.WaitGroup
		var mu sync.Mutex
		codes := map[int]int{}
		for i := 0; i < 12; i++ {
			wg.Add(1)
			go func() {
				defer wg.Done()
				w := je.do("GET", "plugin/jsdemo/ping/x", "", nil, "")
				mu.Lock()
				codes[w.Code]++
				mu.Unlock()
			}()
		}
		wg.Wait()
		if codes[200] != 12 {
			t.Fatalf("并发请求应全部成功：%v", codes)
		}
		if n := jsOf(t, m, "jsdemo").created.Load(); n < 1 || n > jsMaxVMs {
			t.Fatalf("运行时数量应在 1～%d：%d", jsMaxVMs, n)
		}

		if err := m.Disable(ctx, "jsdemo"); err != nil || kvGet(m, "jsdemo", "enabled") != "0" {
			t.Fatalf("onDisable 未执行：%v", err)
		}
		if w := je.do("GET", "plugin/jsdemo/ping/x", "", nil, ""); w.Code != http.StatusNotFound {
			t.Fatalf("停用后路由应 404：%d", w.Code)
		}
	})
}

func TestJSPermissionsAndLoadErrors(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		m := e.manager(t)
		je := newJSEnv(t, e, m)

		// 通过管理端接口上传：先预览，再带 sha256 安装
		raw := makeZip(t, map[string]string{
			"plugin.json": simpleManifest("jsnoperm", "1.0.0"),
			"main.js": `plugin.route('guest', 'GET', '/u', function () {
				var r = [];
				[function () { users.get(1); }, function () { users.addBalance(1, 1); }, function () { subscriptions.list(1); },
				 function () { subscriptions.extend(1, 1); }, function () { orders.list(1); }].forEach(function (f) {
					try { f(); r.push('ok'); } catch (e) { r.push(e.message); }
				});
				return r;
			});`,
		})
		w := je.upload(raw, "")
		var pv struct {
			Data PackageView `json:"data"`
		}
		if err := json.Unmarshal(w.Body.Bytes(), &pv); err != nil || w.Code != 200 || pv.Data.Code != "jsnoperm" || pv.Data.SHA256 == "" {
			t.Fatalf("上传预览失败：%d %s", w.Code, w.Body)
		}
		if w := je.upload(raw, "bad"); w.Code != http.StatusBadRequest {
			t.Fatalf("sha256 不一致应 400：%d %s", w.Code, w.Body)
		}
		if w := je.upload([]byte("bad"), ""); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("无效安装包应 422：%d %s", w.Code, w.Body)
		}
		if w := je.upload(make([]byte, maxPackageSize+1), ""); w.Code == 200 {
			t.Fatal("过大的安装包应拒绝")
		}
		if w := je.do("POST", je.secure+"/plugins/upload", "", nil, je.uTok); w.Code != http.StatusNotFound {
			t.Fatalf("普通用户上传应 404：%d", w.Code)
		}
		if w := je.upload(raw, pv.Data.SHA256); w.Code != 200 || !strings.Contains(w.Body.String(), `"type":"js"`) {
			t.Fatalf("上传安装失败：%d %s", w.Code, w.Body)
		}
		mustEnable(t, m, "jsnoperm")
		w = je.do("GET", "plugin/jsnoperm/u", "", nil, "")
		var res struct {
			Data []string `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &res)
		if len(res.Data) != 5 || strings.Count(strings.Join(res.Data, ","), "没有该权限") != 5 {
			t.Fatalf("未声明权限时应拒绝全部数据接口：%s", w.Body)
		}

		// 每次加载注册内容必须一致
		install(t, m, makeZip(t, map[string]string{
			"plugin.json": simpleManifest("jsshape", "1"),
			"main.js":     `if (kv.setnx('first', '1')) { plugin.on('user.login', function () {}); }`,
		}))
		if v := statusOf(t, m, "jsshape"); v.Status != StatusOK || len(v.Features.Events) != 1 {
			t.Fatalf("首次加载应成功：%+v", v)
		}
		if _, err := jsOf(t, m, "jsshape").newVM(); err == nil || !strings.Contains(err.Error(), "不一致") {
			t.Fatalf("注册内容不一致的运行时应拒绝：%v", err)
		}

		// 加载失败：脚本错误、未知事件、死循环、栈溢出、注册数量超限
		for code, main := range map[string]string{
			"jsthrow": `throw new Error('初始化失败');`,
			"jsevent": `plugin.on('no.such', function () {});`,
			"jsroute": `plugin.route('guest', 'GET', 'nope', function () {});`,
			"jsjob":   `plugin.job('tick', 'bad', function () {});`,
			"jsreq":   `require('../x');`,
			"jsloop":  `while (true) {}`,
			"jsstack": `function f() { return f() + 1; } f();`,
			"jsmany":  `for (var i = 0; i < 100; i++) plugin.route('guest', 'GET', '/r' + i, function () {});`,
			"jswidg":  `plugin.widget('x');`,
			"jswidg2": `plugin.widget({ key: 'a', slot: 'dashboard', title: 't', state: '/../x' });`,
			"jswidg3": `for (var i = 0; i < 6; i++) plugin.widget({ key: 'k' + i, slot: 'dashboard', title: 't', state: '/w' });`,
		} {
			v := install(t, m, makeZip(t, map[string]string{"plugin.json": simpleManifest(code, "1"), "main.js": main}))
			if v.Status != StatusError || v.Error == "" {
				t.Errorf("%s 应加载失败：%+v", code, v)
			}
			if err := m.Enable(ctx, code); err == nil {
				t.Errorf("%s 加载失败时不应能启用", code)
			}
		}
		if v := statusOf(t, m, "jsloop"); !strings.Contains(v.Error, errJSInterrupted.Error()) {
			t.Fatalf("死循环应因超时失败：%s", v.Error)
		}
	})
}

func TestJSHTTPFromScript(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			b, _ := io.ReadAll(r.Body)
			_ = json.NewEncoder(w).Encode(map[string]string{"method": r.Method, "ct": r.Header.Get("Content-Type"), "body": string(b), "q": r.URL.RawQuery})
		}))
		defer srv.Close()
		u, _ := url.Parse(srv.URL)
		m := e.manager(t)
		m.jsAllowPrivate = true
		install(t, m, makeZip(t, map[string]string{
			"plugin.json": fmt.Sprintf(`{"code":"jshttp","name":"x","version":"1","http":["api.gvb.test:%s"]}`, u.Port()),
			"main.js": fmt.Sprintf(`
				var base = 'http://api.gvb.test:%s';
				plugin.route('guest', 'GET', '/t', function () {
					var a = http.get(base + '/a?x=1').json();
					var b = http.post(base + '/b', { k: 1 }).json();
					var c = http.request({ method: 'PUT', url: base + '/c', body: 'raw', headers: { 'Content-Type': 'text/plain' } });
					return [a.method, a.q, b.method, b.ct, b.body, c.status, c.json().body];
				});`, u.Port()),
		}))
		jp := jsOf(t, m, "jshttp")
		addr := srv.Listener.Addr().String()
		jp.http.client.Transport.(*http.Transport).DialContext = func(ctx context.Context, network, _ string) (net.Conn, error) {
			return (&net.Dialer{}).DialContext(ctx, network, addr)
		}
		mustEnable(t, m, "jshttp")
		je := newJSEnv(t, e, m)
		w := je.do("GET", "plugin/jshttp/t", "", nil, "")
		if !strings.Contains(w.Body.String(), `["GET","x=1","POST","application/json","{\"k\":1}",200,"raw"]`) {
			t.Fatalf("脚本 HTTP 请求结果错误：%d %s", w.Code, w.Body)
		}
	})
}
