package subscribe

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/cache"
	"gvboard/internal/config"
	"gvboard/internal/hook"
	"gvboard/internal/model"
	"gvboard/internal/phpx"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/subscription"
	"gvboard/internal/testutil"
)

var testNow = time.Date(2026, 11, 13, 12, 0, 0, 0, time.UTC)

type env struct {
	svc  *Service
	auth *auth.Service
}

func newEnv(t *testing.T, driver string) *env {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := func() time.Time { return testNow }
	return &env{
		svc: &Service{
			DB: db, Cache: c, Settings: st, Now: now,
			Linker: &subscription.Linker{Settings: st, Cache: c, Now: now},
			Rand:   func(lo, _ int64) int64 { return lo },
		},
		auth: auth.New(db, c, "base64:c3Vic2NyaWJlLXRlc3Qta2V5LTAxMjM0NTY3OA=="),
	}
}

func ptr[T any](v T) *T { return &v }

func (e *env) set(t *testing.T, kv map[string]any) {
	t.Helper()
	if err := e.svc.Settings.Set(context.Background(), kv); err != nil {
		t.Fatal(err)
	}
}

// newSub 创建用户及可用的主订阅（权限组 1、100 GB、长期有效）。
func (e *env) newSub(t *testing.T, email string) (*model.User, *model.Subscription) {
	t.Helper()
	var u *model.User
	var sub *model.Subscription
	err := e.svc.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		u, sub, err = account.Create(tx, account.NewUser{Email: email, Password: "password123"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	sub.GroupID = ptr[int64](1)
	sub.TransferEnable = 100 << 30
	sub.ExpiredAt = nil
	if err := e.svc.DB.Model(sub).Updates(map[string]any{"group_id": 1, "transfer_enable": sub.TransferEnable, "expired_at": nil}).Error; err != nil {
		t.Fatal(err)
	}
	return u, sub
}

func (e *env) create(t *testing.T, v any) {
	t.Helper()
	if err := e.svc.DB.Create(v).Error; err != nil {
		t.Fatal(err)
	}
}

func (e *env) markCheck(t *testing.T, typ string, id int64, at int64) {
	t.Helper()
	key := e.svc.Cache.KeyOf(cache.KeyServerLastCheckAt, cache.ServerKey(typ, id))
	if err := e.svc.Cache.Set(context.Background(), key, at, time.Hour).Err(); err != nil {
		t.Fatal(err)
	}
}

func base(name string, groups []int64, sort *int64, port string) model.ServerBase {
	return model.ServerBase{
		GroupID: model.IntList(groups), RouteID: model.IntList{}, Name: name, Host: name + ".example.com",
		Port: port, ServerPort: 443, Tags: model.StrList{"t"}, Rate: "1", Show: 1, Sort: sort,
		CreatedAt: 1700000000, UpdatedAt: 1700000500,
	}
}

func TestSubscribe(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			t.Run("可用节点", func(t *testing.T) { testServers(t, newEnv(t, driver)) })
			t.Run("token 认证", func(t *testing.T) { testResolve(t, newEnv(t, driver)) })
			t.Run("订阅下发", func(t *testing.T) { testDeliver(t, newEnv(t, driver)) })
			t.Run("HTTP 接口", func(t *testing.T) { testHTTP(t, newEnv(t, driver)) })
			t.Run("插件过滤器与订阅格式", func(t *testing.T) { testPluginExt(t, newEnv(t, driver)) })
		})
	}
}

type nameHooks map[string]func(v any) any

func (nameHooks) Emit(context.Context, string, any) {}

func (h nameHooks) Filter(_ context.Context, name string, v any, _ func(any) any, check func(hook.Source, any, any) (any, error)) any {
	f := h[name]
	if f == nil {
		return v
	}
	out, err := check(hook.Source{}, v, f(v))
	if err != nil {
		return v
	}
	return out
}

func testPluginExt(t *testing.T, e *env) {
	ctx := context.Background()
	_, sub := e.newSub(t, "p@x.com")
	e.create(t, &model.ServerShadowsocks{ServerBase: base("ss", []int64{1}, ptr[int64](0), "8388"), Cipher: "aes-128-gcm"})
	e.create(t, &model.ServerTrojan{ServerBase: base("trojan", []int64{1}, ptr[int64](1), "443")})

	enabled := false
	p := &Protocol{Name: "MyFormat", Flag: "myformat", Handle: func(c *Ctx) (*Response, error) {
		var names []string
		for _, s := range c.Servers {
			names = append(names, s.Str("name"))
		}
		return &Response{ContentType: "text/plain", Body: []byte("my:" + strings.Join(names, ","))}, nil
	}}
	if err := RegisterExt(p, func() bool { return enabled }); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { UnregisterExt(p) })
	if err := RegisterExt(&Protocol{Name: "clash", Flag: "x", Handle: p.Handle}, func() bool { return true }); err == nil {
		t.Fatal("与内置格式重名应失败")
	}
	if err := RegisterExt(&Protocol{Name: "MyFormat", Flag: "y", Handle: p.Handle}, func() bool { return true }); err == nil {
		t.Fatal("重复注册应失败")
	}

	req := Request{Flag: "MyFormat/1.0"}
	resp, err := e.svc.Subscribe(ctx, sub, req)
	if err != nil || strings.HasPrefix(string(resp.Body), "my:") {
		t.Fatalf("插件停用时不应使用插件格式：%q %v", resp.Body, err)
	}
	enabled = true
	resp, err = e.svc.Subscribe(ctx, sub, req)
	if err != nil || string(resp.Body) != "my:ss,trojan" {
		t.Fatalf("插件格式错误：%q %v", resp.Body, err)
	}

	e.svc.Hooks = nameHooks{
		"subscribe.servers": func(v any) any {
			s := v.(hook.SubscribeServers)
			var out []*phpx.Arr
			for _, x := range s.Servers {
				if x.Str("name") != "trojan" {
					out = append(out, x)
				}
			}
			s.Servers = out
			return s
		},
		"subscribe.content": func(v any) any {
			c := v.(hook.SubscribeContent)
			c.Body = append(append([]byte{}, c.Body...), "#end"...)
			c.Headers = append(c.Headers, [2]string{"X-Test", "1"})
			return c
		},
	}
	resp, err = e.svc.Subscribe(ctx, sub, req)
	if err != nil || string(resp.Body) != "my:ss#end" || len(resp.Headers) == 0 || resp.Headers[len(resp.Headers)-1][0] != "X-Test" {
		t.Fatalf("过滤器结果错误：%q %v %v", resp.Body, resp.Headers, err)
	}
	e.svc.Hooks = nameHooks{"subscribe.servers": func(v any) any {
		s := v.(hook.SubscribeServers)
		s.Servers = append(s.Servers, nil)
		return s
	}}
	resp, err = e.svc.Subscribe(ctx, sub, req)
	if err != nil || string(resp.Body) != "my:ss,trojan" {
		t.Fatalf("含 nil 节点的结果应被忽略：%q %v", resp.Body, err)
	}
}

func testServers(t *testing.T, e *env) {
	ctx := context.Background()
	now := testNow.Unix()

	parent := model.ServerShadowsocks{ServerBase: base("ss-parent", []int64{2}, ptr[int64](5), "8388"), Cipher: "aes-128-gcm"}
	parent.CreatedAt = 111
	e.create(t, &parent)
	child := model.ServerShadowsocks{ServerBase: base("ss-child", []int64{1}, ptr[int64](1), "3000-4000"), Cipher: "aes-128-gcm",
		Obfs: ptr("http"), ObfsSettings: model.JSON(`{"host":"h.com","path":"/p"}`)}
	child.ParentID = &parent.ID
	e.create(t, &child)
	hidden := model.ServerShadowsocks{ServerBase: base("ss-hidden", []int64{1}, ptr[int64](0), "1"), Cipher: "aes-128-gcm"}
	e.create(t, &hidden)
	e.svc.DB.Model(&hidden).Update("show", 0)
	vmess := model.ServerVmess{ServerBase: base("vmess", []int64{1, 2}, ptr[int64](1), "443"), Network: "tcp"}
	e.create(t, &vmess)
	trojan := model.ServerTrojan{ServerBase: base("trojan-other-group", []int64{2}, ptr[int64](0), "443")}
	e.create(t, &trojan)
	hy := model.ServerHysteria{ServerBase: base("hy", []int64{1}, ptr[int64](2), "5000-6000"), Version: 2}
	e.create(t, &hy)
	vless := model.ServerVless{ServerBase: base("vless", []int64{1}, ptr[int64](0), "443"), Network: "tcp", TLS: 2,
		TLSSettings:        model.JSON(`{"server_name":"x.com","private_key":"SECRET","ech_key":"E","short_id":"ab"}`),
		EncryptionSettings: model.JSON(`{"private_key":"K","mode":"native"}`)}
	e.create(t, &vless)
	v2 := model.ServerV2node{ServerBase: base("v2node", []int64{1}, nil, "443"), Protocol: "vless", Network: "tcp", TLS: 1,
		TLSSettings: model.JSON(`{"cert_file":"/etc/x.pem","server_name":"v.com"}`), TrustedXForwardedFor: model.JSON(`["1.1.1.1"]`)}
	e.create(t, &v2)

	e.markCheck(t, model.ServerTypeVmess, vmess.ID, now-10)
	e.markCheck(t, model.ServerTypeShadowsocks, parent.ID, now-20)
	e.markCheck(t, model.ServerTypeShadowsocks, child.ID, now-10000)

	if list, err := e.svc.Servers(ctx, nil); err != nil || len(list) != 0 {
		t.Fatalf("无权限组应没有节点: %v %d", err, len(list))
	}
	list, err := e.svc.Servers(ctx, ptr[int64](1))
	if err != nil {
		t.Fatal(err)
	}
	var got []string
	byName := map[string]*phpx.Arr{}
	for _, s := range list {
		got = append(got, s.Str("name"))
		byName[s.Str("name")] = s
	}
	// sort 相同（vless 0 与 v2node null；ss-child 1 与 vmess 1）时按合并顺序 SS → VMess → … → VLESS → v2node。
	if want := "vless,v2node,ss-child,vmess,hy"; strings.Join(got, ",") != want {
		t.Fatalf("节点顺序 %v，期望 %s", got, want)
	}

	c := byName["ss-child"]
	if c.Get("port") != int64(3000) || c.Get("created_at") != int64(111) || c.Get("obfs-host") != "h.com" || c.Get("obfs-path") != "/p" {
		t.Errorf("ss 子节点处理不正确: port=%v created_at=%v obfs-host=%v", c.Get("port"), c.Get("created_at"), c.Get("obfs-host"))
	}
	if c.Get("is_online") != int64(1) || c.Get("last_check_at") != now-20 {
		t.Errorf("子节点应使用父节点的最后检查时间: %v %v", c.Get("last_check_at"), c.Get("is_online"))
	}
	if want := "shadowsocks-" + strconv.FormatInt(child.ID, 10) + "-1700000500-1"; c.Str("cache_key") != want {
		t.Errorf("cache_key = %s，期望 %s", c.Str("cache_key"), want)
	}
	if v := byName["vmess"]; v.Get("is_online") != int64(1) || v.Get("port") != int64(443) {
		t.Errorf("vmess 在线状态或端口不正确: %v %v", v.Get("is_online"), v.Get("port"))
	}
	h := byName["hy"]
	if h.Get("mport") != "5000-6000" || h.Get("port") != "5000-6000" || h.Get("is_online") != int64(0) {
		t.Errorf("hysteria 端口段应保留并设置 mport: %v %v", h.Get("port"), h.Get("mport"))
	}
	if h.Str("server_key") != phpx.ServerKey(int64(1700000000), 16) {
		t.Errorf("hysteria server_key 不正确")
	}
	vl := byName["vless"]
	if vl.Isset("tls_settings", "private_key") || vl.Isset("tls_settings", "ech_key") || vl.Get("tls_settings", "short_id") != "ab" {
		t.Errorf("vless 应移除 TLS 私密字段: %s", phpx.JSONEncode(vl.Get("tls_settings"), 0))
	}
	if vl.Isset("encryption_settings", "private_key") || vl.Get("encryption_settings", "mode") != "native" {
		t.Errorf("vless 应移除加密私钥")
	}
	vn := byName["v2node"]
	if vn.Isset("trusted_x_forwarded_for") || vn.Isset("tls_settings", "cert_file") || vn.Get("tls_settings", "server_name") != "v.com" {
		t.Errorf("v2node 私密字段未移除")
	}
}

func testResolve(t *testing.T, e *env) {
	ctx := context.Background()
	_, sub := e.newSub(t, "r@x.com")
	other, _ := e.newSub(t, "o@x.com")
	_ = other

	for _, tok := range []string{"", "0", "nope"} {
		if _, err := e.svc.Resolve(ctx, tok); !errors.Is(err, ErrToken) {
			t.Errorf("token %q 应无效，实际 %v", tok, err)
		}
	}
	if got, err := e.svc.Resolve(ctx, sub.Token); err != nil || got.ID != sub.ID {
		t.Fatalf("永久 token 认证失败: %v", err)
	}

	tokenOf := func() string {
		link, err := e.svc.Linker.URL(ctx, sub, "http://x")
		if err != nil {
			t.Fatal(err)
		}
		u, _ := url.Parse(link)
		return u.Query().Get("token")
	}

	e.set(t, map[string]any{"show_subscribe_method": subscription.LinkOneTime})
	otp := tokenOf()
	if _, err := e.svc.Resolve(ctx, sub.Token); !errors.Is(err, ErrToken) {
		t.Errorf("一次性模式下原 token 应无效")
	}
	if got, err := e.svc.Resolve(ctx, otp); err != nil || got.ID != sub.ID {
		t.Fatalf("一次性 token 认证失败: %v", err)
	}
	if _, err := e.svc.Resolve(ctx, otp); !errors.Is(err, ErrToken) {
		t.Errorf("一次性 token 只能使用一次")
	}
	if next := tokenOf(); next == otp {
		t.Errorf("使用后应生成新的一次性 token")
	}

	e.set(t, map[string]any{"show_subscribe_method": subscription.LinkTOTP, "show_subscribe_expire": 5})
	totp := tokenOf()
	for i := 0; i < 2; i++ {
		if got, err := e.svc.Resolve(ctx, totp); err != nil || got.ID != sub.ID {
			t.Fatalf("时效 token 第 %d 次认证失败: %v", i+1, err)
		}
	}
	raw, _ := base64.RawURLEncoding.DecodeString(strings.TrimRight(totp, "="))
	id, hash, _ := strings.Cut(string(raw), ":")
	forged := []string{
		base64.RawURLEncoding.EncodeToString([]byte(id + ":" + strings.Repeat("0", len(hash)))),
		base64.RawURLEncoding.EncodeToString([]byte("0:" + hash)),
		base64.RawURLEncoding.EncodeToString([]byte(id)),
		subscription.TOTPToken(sub.ID, sub.Token, 5, testNow.Add(-time.Hour)),
		"!!!",
	}
	for _, f := range forged {
		if _, err := e.svc.Resolve(ctx, f); !errors.Is(err, ErrToken) {
			t.Errorf("伪造或过期的时效 token %q 应无效，实际 %v", f, err)
		}
	}
}

func testDeliver(t *testing.T, e *env) {
	ctx := context.Background()
	u, sub := e.newSub(t, "d@x.com")
	e.create(t, &model.ServerShadowsocks{ServerBase: base("香港 01", []int64{1}, ptr[int64](0), "8388"), Cipher: "aes-128-gcm"})
	e.create(t, &model.ServerTrojan{ServerBase: base("trojan", []int64{1}, ptr[int64](1), "443")})
	req := Request{Flag: "", Host: "sub.example.com", BaseURL: "https://sub.example.com"}

	resp, err := e.svc.Subscribe(ctx, sub, req)
	if err != nil {
		t.Fatal(err)
	}
	body, _ := base64.StdEncoding.DecodeString(string(resp.Body))
	if !strings.HasPrefix(string(body), "ss://") || !strings.Contains(string(body), "trojan://") {
		t.Fatalf("通用格式内容不正确: %q", body)
	}

	e.set(t, map[string]any{"show_info_to_server_enable": 1})
	resp, _ = e.svc.Subscribe(ctx, sub, Request{Flag: "ClashMetaForAndroid/2.10 clash.meta"})
	if s := string(resp.Body); !strings.Contains(s, "剩余流量：100 GB") || !strings.Contains(s, "套餐到期：长期有效") || !strings.Contains(s, "type: ss") {
		t.Errorf("Clash Meta 内容或订阅信息节点不正确:\n%.400s", s)
	}
	resp, _ = e.svc.Subscribe(ctx, sub, Request{Flag: "SFA/1.12.0 (sing-box 1.12.3)"})
	if resp.ContentType != "application/json" || strings.Contains(string(resp.Body), "剩余流量") || !strings.Contains(string(resp.Body), `"domain_resolver":"local"`) {
		t.Errorf("sing-box 1.12 应使用新格式且不插入订阅信息节点")
	}
	resp, _ = e.svc.Subscribe(ctx, sub, Request{Flag: "sing-box 1.11.0"})
	if strings.Contains(string(resp.Body), "domain_resolver") {
		t.Errorf("sing-box 1.11 应使用旧格式")
	}

	unavailable := map[string]func(){
		"订阅封禁": func() { e.svc.DB.Model(sub).Update("banned", 1) },
		"用户封禁": func() { e.svc.DB.Model(u).Update("banned", 1) },
		"已到期":  func() { e.svc.DB.Model(sub).Update("expired_at", testNow.Unix()-1) },
		"没有流量": func() { e.svc.DB.Model(sub).Update("transfer_enable", 0) },
	}
	for name, apply := range unavailable {
		e.svc.DB.Model(sub).Updates(map[string]any{"banned": 0, "expired_at": nil, "transfer_enable": 100 << 30})
		e.svc.DB.Model(u).Update("banned", 0)
		apply()
		var cur model.Subscription
		e.svc.DB.Take(&cur, sub.ID)
		resp, err := e.svc.Subscribe(ctx, &cur, Request{Flag: "clash"})
		if err != nil || len(resp.Body) != 0 {
			t.Errorf("%s：应返回空内容，实际 %d 字节 %v", name, len(resp.Body), err)
		}
	}
}

func testHTTP(t *testing.T, e *env) {
	ctx := context.Background()
	u, sub := e.newSub(t, "h@x.com")
	other, _ := e.newSub(t, "h2@x.com")
	e.create(t, &model.ServerVmess{ServerBase: base("vmess", []int64{1}, ptr[int64](0), "443"), Network: "tcp"})
	e.set(t, map[string]any{"subscribe_path": "/s/abc", "windows_version": "1.0.1", "macos_version": "2.0.0"})
	userTok, _ := e.auth.Issue(ctx, u.ID, "", "")
	otherTok, _ := e.auth.Issue(ctx, other.ID, "", "")

	build := func(legacy bool) *server.Router {
		cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"},
			Compat: config.CompatConfig{LegacySubscribe: legacy}}
		r, err := server.NewRouter(cfg, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		r.Mount(&Module{Svc: e.svc, Auth: e.auth})
		return r
	}
	do := func(r *server.Router, method, path string, h map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		req.Host = "sub.example.com"
		for k, v := range h {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		r.Engine.ServeHTTP(w, req)
		return w
	}

	r := build(false)
	q := "?token=" + sub.Token
	for _, p := range []string{"/s/abc" + q, "/s/abc/" + q, "/s/abc" + q + "&flag=%20clash%20"} {
		if w := do(r, "GET", p, nil); w.Code != 200 || len(w.Body.Bytes()) == 0 {
			t.Errorf("GET %s 应返回订阅内容，实际 %d", p, w.Code)
		}
	}
	if w := do(r, "GET", "/s/abc"+q+"&flag=clash", nil); !strings.Contains(w.Body.String(), "proxies:") ||
		!strings.Contains(w.Header().Get("Content-Disposition"), "filename*=UTF-8''") {
		t.Errorf("flag=clash 应返回 Clash 配置")
	}
	if w := do(r, "HEAD", "/s/abc"+q, nil); w.Code != 200 {
		t.Errorf("HEAD 订阅路径应返回 200，实际 %d", w.Code)
	}
	for _, p := range []string{"/s/abc", "/s/abc?token=bad", "/s/abcd" + q, "/api/v1/client/subscribe" + q, "/api/v1/client/app/getConfig" + q} {
		if w := do(r, "GET", p, nil); w.Code != http.StatusNotFound {
			t.Errorf("GET %s 应返回统一 404，实际 %d", p, w.Code)
		}
	}
	if w := do(r, "POST", "/s/abc"+q, nil); w.Code != http.StatusNotFound {
		t.Errorf("POST 订阅路径应返回 404，实际 %d", w.Code)
	}

	if w := do(r, "GET", "/api/t1/client/app/config"+q, nil); w.Code != 200 || w.Header().Get("Content-Type") != "text/yaml" {
		t.Errorf("App 配置接口不正确: %d %s", w.Code, w.Header().Get("Content-Type"))
	}
	w := do(r, "GET", "/api/t1/client/app/version"+q, map[string]string{"User-Agent": "tidalab/4.0.0 Win64"})
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"data":{"version":"1.0.1","download_url":""}`) {
		t.Errorf("App 版本接口不正确: %s", w.Body)
	}
	if w := do(r, "GET", "/api/t1/client/app/version?token=bad", nil); w.Code != http.StatusNotFound {
		t.Errorf("token 无效应返回 404，实际 %d", w.Code)
	}

	legacy := build(true)
	if w := do(legacy, "GET", "/api/v1/client/subscribe"+q, nil); w.Code != 200 || len(w.Body.Bytes()) == 0 {
		t.Errorf("开启旧订阅路径后应可访问，实际 %d", w.Code)
	}
	w = do(legacy, "GET", "/api/v1/client/app/getVersion"+q, nil)
	if w.Code != 200 || !strings.HasPrefix(w.Body.String(), `{"data":{"windows_version":"1.0.1"`) {
		t.Errorf("旧 App 版本接口格式不正确: %s", w.Body)
	}
	if w := do(legacy, "GET", "/api/v1/client/app/getConfig"+q, nil); w.Code != 200 {
		t.Errorf("旧 App 配置接口不可用: %d", w.Code)
	}

	path := "/api/t1/user/subscriptions/" + strconv.FormatInt(sub.ID, 10) + "/servers"
	if w := do(r, "GET", path, nil); w.Code != http.StatusUnauthorized {
		t.Errorf("未登录应返回 401，实际 %d", w.Code)
	}
	if w := do(r, "GET", path, map[string]string{"Authorization": otherTok}); w.Code != http.StatusNotFound {
		t.Errorf("访问他人订阅应返回 404，实际 %d", w.Code)
	}
	w = do(r, "GET", path, map[string]string{"Authorization": userTok})
	var res struct {
		Data []map[string]any `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &res)
	etag := w.Header().Get("ETag")
	if w.Code != 200 || len(res.Data) != 1 || etag == "" {
		t.Fatalf("节点列表接口不正确: %d %s", w.Code, w.Body)
	}
	for _, k := range []string{"host", "port", "tls", "tlsSettings", "networkSettings"} {
		if _, ok := res.Data[0][k]; ok {
			t.Errorf("节点列表不应包含 %s", k)
		}
	}
	if res.Data[0]["name"] != "vmess" || res.Data[0]["type"] != "vmess" {
		t.Errorf("节点列表字段不正确: %v", res.Data[0])
	}
	if w := do(r, "GET", path, map[string]string{"Authorization": userTok, "If-None-Match": etag}); w.Code != http.StatusNotModified {
		t.Errorf("ETag 未变化应返回 304，实际 %d", w.Code)
	}
}
