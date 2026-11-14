package legacy

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/adminuser"
	"gvboard/internal/auth"
	"gvboard/internal/config"
	"gvboard/internal/content"
	"gvboard/internal/i18n"
	"gvboard/internal/invite"
	"gvboard/internal/mail"
	"gvboard/internal/model"
	"gvboard/internal/node"
	"gvboard/internal/nodeapi"
	"gvboard/internal/order"
	"gvboard/internal/passport"
	"gvboard/internal/payment"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/subscribe"
	"gvboard/internal/subscription"
	"gvboard/internal/sysconfig"
	"gvboard/internal/telegram"
	"gvboard/internal/testutil"
	"gvboard/internal/theme"
	"gvboard/internal/ticket"
	"gvboard/internal/usercenter"
)

type fakeMail struct {
	mu   sync.Mutex
	sent []mail.Payload
}

func (f *fakeMail) Dispatch(_ context.Context, p mail.Payload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, p)
	return nil
}

func (f *fakeMail) DispatchMass(ctx context.Context, p mail.Payload) error { return f.Dispatch(ctx, p) }

func (f *fakeMail) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.sent)
}

type env struct {
	t    *testing.T
	db   *gorm.DB
	st   *setting.Service
	tr   *i18n.Translator
	auth *auth.Service
	subs *subscription.Service
	mail *fakeMail
	mod  *Module
	r    *server.Router
}

func newEnv(t *testing.T, driver string) *env {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	tr, err := i18n.New("zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	mailer, err := mail.New(st)
	if err != nil {
		t.Fatal(err)
	}
	translate := func(locale, key string, params map[string]any) string { return tr.T(locale, key, params) }
	au := auth.New(db, c, "base64:bGVnYWN5LXRlc3Qta2V5LTAxMjM0NTY3OA==")
	subs := &subscription.Service{DB: db, Cache: c, Settings: st, Linker: &subscription.Linker{Settings: st, Cache: c}}
	orders := &order.Service{DB: db, Settings: st, Subscriptions: subs}
	pays := &payment.Service{DB: db, Settings: st, Orders: orders, APIPrefix: "/api/t1"}
	orders.Payer = pays
	subscribeSvc := &subscribe.Service{DB: db, Cache: c, Settings: st, Linker: subs.Linker}
	tickets := &ticket.Service{DB: db, Cache: c, Settings: st, Subscriptions: subs, Translate: translate}
	e := &env{t: t, db: db, st: st, tr: tr, auth: au, subs: subs, mail: &fakeMail{}}
	themes := theme.New(t.TempDir(), st)
	e.mod = &Module{
		DB: db, Settings: st, Auth: au, I18n: tr,
		Passport: &passport.Service{DB: db, Cache: c, Settings: st, Auth: au, Mail: e.mail, I18n: tr},
		Users:    &usercenter.Service{DB: db, Settings: st, Auth: au, Subscriptions: subs},
		Subs:     subs, Orders: orders, Payments: pays,
		Invites: &invite.Service{DB: db, Settings: st},
		Tickets: tickets,
		Content: &content.Service{DB: db, Settings: st, Subscriptions: subs, Translate: func(locale, key string) string {
			return tr.T(locale, key, nil)
		}},
		Subscribe: subscribeSvc,
		Traffic:   &nodeapi.Service{DB: db, Cache: c, Settings: st},
		Bot:       &telegram.Service{DB: db, Cache: c, Settings: st, I18n: tr},
		AdminUsers: &adminuser.Service{
			DB: db, Settings: st, Auth: au, Subscriptions: subs, Mail: e.mail, Guard: subscribeSvc,
		},
		Nodes:     &node.Service{DB: db, Cache: c, Settings: st},
		SysConfig: &sysconfig.Service{DB: db, Settings: st, Mailer: mailer, ThemeExists: themes.Exists},
		Cache:     c,
		Queue:     &fakeQueue{workers: 1},
		Themes:    themes,
	}
	e.r = e.router(true)
	return e
}

func (e *env) router(compat bool) *server.Router {
	cfg := &config.Config{
		Server: config.ServerConfig{Mode: config.ModeAll},
		API:    config.APIConfig{Prefix: "/api/t1"},
		Compat: config.CompatConfig{V2boardAPI: compat},
		CORS:   config.CORSConfig{AllowOrigins: []string{"https://front.example"}},
	}
	r, err := server.NewRouter(cfg, zap.NewNop(), e.tr.Middleware())
	if err != nil {
		e.t.Fatal(err)
	}
	r.Mount(e.mod)
	return r
}

func (e *env) set(kv map[string]any) {
	e.t.Helper()
	if err := e.st.Set(context.Background(), kv); err != nil {
		e.t.Fatal(err)
	}
}

func (e *env) zh(key string) string { return e.tr.T("zh-CN", key, nil) }

type resp struct {
	code int
	hdr  http.Header
	raw  string
	body map[string]any
}

func (r resp) data() any { return r.body["data"] }

func (r resp) obj(t *testing.T) map[string]any {
	t.Helper()
	m, ok := r.data().(map[string]any)
	if !ok {
		t.Fatalf("data 不是对象: %d %s", r.code, r.raw)
	}
	return m
}

func (r resp) list(t *testing.T) []any {
	t.Helper()
	l, ok := r.data().([]any)
	if !ok {
		t.Fatalf("data 不是数组: %d %s", r.code, r.raw)
	}
	return l
}

// do 发送请求。body 为 url.Values 时按表单提交，为 map 时按 JSON 提交。
func (e *env) do(r *server.Router, method, path string, body any, hdr map[string]string) resp {
	e.t.Helper()
	var rd *bytes.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
		rd = bytes.NewReader(nil)
	case url.Values:
		rd = bytes.NewReader([]byte(b.Encode()))
		ct = "application/x-www-form-urlencoded"
	default:
		raw, _ := json.Marshal(b)
		rd = bytes.NewReader(raw)
		ct = "application/json"
	}
	req := httptest.NewRequest(method, path, rd)
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.Engine.ServeHTTP(w, req)
	out := resp{code: w.Code, hdr: w.Header(), raw: w.Body.String()}
	_ = json.Unmarshal(w.Body.Bytes(), &out.body)
	return out
}

func (e *env) get(path, token string) resp {
	e.t.Helper()
	return e.do(e.r, http.MethodGet, path, nil, authHdr(token))
}

func (e *env) post(path, token string, body any) resp {
	e.t.Helper()
	return e.do(e.r, http.MethodPost, path, body, authHdr(token))
}

func authHdr(token string) map[string]string {
	if token == "" {
		return nil
	}
	return map[string]string{"Authorization": token}
}

func (e *env) createUser(email string) (*model.User, *model.Subscription) {
	e.t.Helper()
	var u *model.User
	var sub *model.Subscription
	if err := e.db.Transaction(func(tx *gorm.DB) error {
		var err error
		u, sub, err = account.Create(tx, account.NewUser{Email: email, Password: "password123"})
		return err
	}); err != nil {
		e.t.Fatal(err)
	}
	return u, sub
}

func (e *env) login(userID int64) string {
	e.t.Helper()
	token, err := e.auth.Issue(context.Background(), userID, "1.1.1.1", "ua")
	if err != nil {
		e.t.Fatal(err)
	}
	return token
}

func (e *env) sub(id int64) model.Subscription {
	e.t.Helper()
	var s model.Subscription
	if err := e.db.Take(&s, id).Error; err != nil {
		e.t.Fatal(err)
	}
	return s
}

func wantMsg(t *testing.T, r resp, code int, msg string) {
	t.Helper()
	if r.code != code || r.body["message"] != msg {
		t.Fatalf("期望 %d %q，实际 %d %s", code, msg, r.code, r.raw)
	}
}

func wantInvalid(t *testing.T, r resp, field string) {
	t.Helper()
	errs, _ := r.body["errors"].(map[string]any)
	if r.code != http.StatusUnprocessableEntity || r.body["message"] != msgInvalidData || errs[field] == nil {
		t.Fatalf("期望字段 %s 校验失败，实际 %d %s", field, r.code, r.raw)
	}
}

func ptr[T any](v T) *T { return &v }

func TestLegacy(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			t.Run("开关", func(t *testing.T) { testSwitch(t, newEnv(t, driver)) })
			t.Run("passport", func(t *testing.T) { testPassport(t, newEnv(t, driver)) })
			t.Run("用户", func(t *testing.T) { testUser(t, newEnv(t, driver)) })
			t.Run("订单", func(t *testing.T) { testOrder(t, newEnv(t, driver)) })
			t.Run("其它", func(t *testing.T) { testMisc(t, newEnv(t, driver)) })
			t.Run("管理端认证", func(t *testing.T) { testAdminAuth(t, newEnv(t, driver)) })
			t.Run("管理端套餐", func(t *testing.T) { testAdminPlan(t, newEnv(t, driver)) })
			t.Run("管理端内容", func(t *testing.T) { testAdminContent(t, newEnv(t, driver)) })
			t.Run("管理端支付", func(t *testing.T) { testAdminPayment(t, newEnv(t, driver)) })
			t.Run("管理端用户", func(t *testing.T) { testAdminUser(t, newEnv(t, driver)) })
			t.Run("管理端订单", func(t *testing.T) { testAdminOrder(t, newEnv(t, driver)) })
			t.Run("管理端节点", func(t *testing.T) { testAdminServer(t, newEnv(t, driver)) })
			t.Run("管理端系统", func(t *testing.T) { testAdminSystem(t, newEnv(t, driver)) })
			t.Run("管理端主题", func(t *testing.T) { testAdminTheme(t, newEnv(t, driver)) })
		})
	}
}

func testSwitch(t *testing.T, e *env) {
	u, _ := e.createUser("off@x.com")
	token := e.login(u.ID)
	off := e.router(false)
	for _, p := range []string{"/api/v1/user/info", "/api/v1/guest/comm/config", "/api/v1/passport/auth/token2Login"} {
		if r := e.do(off, http.MethodGet, p, nil, authHdr(token)); r.code != http.StatusNotFound {
			t.Errorf("兼容层关闭时 %s 应返回 404，实际 %d", p, r.code)
		}
	}
	if r := e.get("/api/v1/user/info", token); r.code != http.StatusOK {
		t.Errorf("兼容层开启时应可访问: %d %s", r.code, r.raw)
	}

	preflight := map[string]string{"Origin": "https://front.example", "Access-Control-Request-Method": "POST"}
	if r := e.do(e.r, http.MethodOptions, "/api/v1/passport/auth/login", nil, preflight); r.code != http.StatusNoContent ||
		r.hdr.Get("Access-Control-Allow-Origin") != "https://front.example" {
		t.Errorf("兼容层开启时 /api/v1 应支持跨域预检: %d %v", r.code, r.hdr)
	}
	if r := e.do(off, http.MethodOptions, "/api/v1/passport/auth/login", nil, preflight); r.hdr.Get("Access-Control-Allow-Origin") != "" {
		t.Errorf("兼容层关闭时 /api/v1 不应返回跨域头: %v", r.hdr)
	}
}

func testPassport(t *testing.T, e *env) {
	// 表单校验与原生服务的格式校验都返回 422
	wantInvalid(t, e.post("/api/v1/passport/auth/register", "", url.Values{"password": {"pass1234"}}), "email")
	wantInvalid(t, e.post("/api/v1/passport/auth/register", "", url.Values{"email": {"bad"}, "password": {"pass1234"}}), "email")
	wantInvalid(t, e.post("/api/v1/passport/auth/register", "", url.Values{"email": {"a@x.com"}, "password": {"short"}}), "password")

	// 密码不去除首尾空白（TrimStrings 的例外），邮箱去除
	r := e.post("/api/v1/passport/auth/register", "", url.Values{"email": {"  reg@x.com "}, "password": {" pass1234 "}})
	if r.code != http.StatusOK {
		t.Fatalf("注册失败: %d %s", r.code, r.raw)
	}
	data := r.obj(t)
	var u model.User
	e.db.Where("email = ?", "reg@x.com").Take(&u)
	primary, err := e.subs.Primary(context.Background(), e.db, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if data["token"] != primary.Token || data["is_admin"] != float64(0) || data["auth_data"] == "" {
		t.Errorf("注册返回不正确: %s", r.raw)
	}
	wantMsg(t, e.post("/api/v1/passport/auth/login", "", map[string]any{"email": "reg@x.com", "password": "pass1234"}),
		http.StatusInternalServerError, e.zh("Incorrect email or password"))
	r = e.post("/api/v1/passport/auth/login", "", map[string]any{"email": "reg@x.com", "password": " pass1234 "})
	if r.code != http.StatusOK || r.obj(t)["token"] != primary.Token {
		t.Fatalf("登录失败: %d %s", r.code, r.raw)
	}
	token := r.obj(t)["auth_data"].(string)

	// token2Login：token 参数跳转前端，verify 参数换取凭证
	r = e.get("/api/v1/passport/auth/token2Login?token=abc", "")
	if r.code != http.StatusFound || r.hdr.Get("Location") != "http://example.com/#/login?verify=abc&redirect=dashboard" {
		t.Errorf("token2Login 跳转不正确: %d %v", r.code, r.hdr)
	}
	wantMsg(t, e.post("/api/v1/passport/auth/getQuickLoginUrl", "", nil), http.StatusForbidden, msgLoginRequired)
	r = e.post("/api/v1/passport/auth/getQuickLoginUrl?auth_data="+url.QueryEscape(token), "", nil)
	link, _ := r.data().(string)
	_, frag, _ := strings.Cut(link, "#/login?")
	q, _ := url.ParseQuery(frag)
	if r.code != http.StatusOK || q.Get("verify") == "" || q.Get("redirect") != "dashboard" {
		t.Fatalf("快捷登录链接不正确: %d %s", r.code, r.raw)
	}
	r = e.get("/api/v1/passport/auth/token2Login?verify="+q.Get("verify"), "")
	if r.code != http.StatusOK || r.obj(t)["auth_data"] == "" {
		t.Fatalf("快捷登录失败: %d %s", r.code, r.raw)
	}
	wantMsg(t, e.get("/api/v1/passport/auth/token2Login?verify="+q.Get("verify"), ""),
		http.StatusInternalServerError, e.zh("Token error"))

	// 发送验证码：isforget 检查邮箱是否注册；同一 IP 每分钟最多 3 次，超过返回 429
	wantInvalid(t, e.post("/api/v1/passport/comm/sendEmailVerify", "", nil), "email")
	wantMsg(t, e.post("/api/v1/passport/comm/sendEmailVerify", "", url.Values{"email": {"none@x.com"}, "isforget": {"1"}}),
		http.StatusInternalServerError, e.zh("This email is not registered in the system"))
	wantMsg(t, e.post("/api/v1/passport/comm/sendEmailVerify", "", url.Values{"email": {"reg@x.com"}, "isforget": {"0"}}),
		http.StatusInternalServerError, e.zh("This email is registered"))
	if r := e.post("/api/v1/passport/comm/sendEmailVerify", "", url.Values{"email": {"new@x.com"}}); r.code != http.StatusOK || r.data() != true || e.mail.count() != 1 {
		t.Fatalf("发送验证码失败: %d %s", r.code, r.raw)
	}
	wantMsg(t, e.post("/api/v1/passport/comm/sendEmailVerify", "", url.Values{"email": {"new2@x.com"}}),
		http.StatusTooManyRequests, e.zh(msgTooMany))

	wantInvalid(t, e.post("/api/v1/passport/auth/forget", "", url.Values{"email": {"reg@x.com"}, "password": {"newpass123"}}), "email_code")
	wantMsg(t, e.post("/api/v1/passport/auth/forget", "", url.Values{"email": {"reg@x.com"}, "password": {"newpass123"}, "email_code": {"123456"}}),
		http.StatusInternalServerError, e.zh("Incorrect email verification code"))

	e.db.Create(&model.InviteCode{UserID: u.ID, Code: "INV1"})
	if r := e.post("/api/v1/passport/comm/pv", "", url.Values{"invite_code": {"INV1"}}); r.data() != true {
		t.Errorf("pv 失败: %s", r.raw)
	}
	var ic model.InviteCode
	if e.db.Where("code = ?", "INV1").Take(&ic); ic.PV != 1 {
		t.Errorf("邀请码访问次数应为 1，实际 %d", ic.PV)
	}

	// guest/comm/config
	g := e.get("/api/v1/guest/comm/config", "").obj(t)
	if g["email_whitelist_suffix"] != float64(0) || g["tos_url"] != nil || g["is_email_verify"] != float64(0) {
		t.Errorf("站点配置不正确: %v", g)
	}
	e.set(map[string]any{"email_whitelist_enable": 1, "email_whitelist_suffix": []string{"a.com", "b.com"}, "tos_url": "https://t.example.com"})
	g = e.get("/api/v1/guest/comm/config", "").obj(t)
	if l, _ := g["email_whitelist_suffix"].([]any); len(l) != 2 || g["tos_url"] != "https://t.example.com" {
		t.Errorf("开启白名单后站点配置不正确: %v", g)
	}
}

func testUser(t *testing.T, e *env) {
	ctx := context.Background()
	u, primary := e.createUser("user@x.com")
	token := e.login(u.ID)
	var other *model.Subscription
	if err := e.db.Transaction(func(tx *gorm.DB) error {
		var err error
		other, err = e.subs.Create(tx, subscription.NewSubscription{UserID: u.ID, Name: "second"})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// 认证：Authorization 请求头或 auth_data 参数，失败统一 403
	wantMsg(t, e.get("/api/v1/user/info", ""), http.StatusForbidden, msgLoginRequired)
	wantMsg(t, e.get("/api/v1/user/info", "bad-token"), http.StatusForbidden, msgLoginRequired)
	if r := e.do(e.r, http.MethodGet, "/api/v1/user/info?auth_data="+url.QueryEscape(token), nil, nil); r.code != http.StatusOK {
		t.Fatalf("auth_data 参数认证失败: %d %s", r.code, r.raw)
	}

	// info / getSubscribe 的订阅字段取自主订阅
	plan := model.Plan{Name: "P1", GroupID: 3, TransferEnable: 50, Show: 1, Renew: 1, MonthPrice: ptr(int64(1000))}
	e.db.Create(&plan)
	exp := time.Now().Add(48 * time.Hour).Unix()
	e.db.Model(primary).Updates(map[string]any{"plan_id": plan.ID, "group_id": 3, "transfer_enable": 1 << 30, "expired_at": exp, "u": 10, "d": 20})
	e.db.Model(other).Updates(map[string]any{"transfer_enable": 999})
	info := e.get("/api/v1/user/info", token).obj(t)
	if info["plan_id"] != float64(plan.ID) || info["transfer_enable"] != float64(1<<30) || info["uuid"] != primary.UUID ||
		info["email"] != "user@x.com" || info["expired_at"] != float64(exp) || info["avatar_url"] == "" {
		t.Errorf("用户信息不正确: %v", info)
	}
	s := e.get("/api/v1/user/getSubscribe", token).obj(t)
	planObj, _ := s["plan"].(map[string]any)
	if s["token"] != primary.Token || s["u"] != float64(10) || s["d"] != float64(20) || planObj["name"] != "P1" ||
		!strings.Contains(s["subscribe_url"].(string), primary.Token) || s["alive_ip"] != float64(0) {
		t.Errorf("订阅信息不正确: %v", s)
	}

	// update / resetSecurity 只作用于主订阅
	wantInvalid(t, e.post("/api/v1/user/update", token, url.Values{"auto_renewal": {"2"}}), "auto_renewal")
	if r := e.post("/api/v1/user/update", token, url.Values{"auto_renewal": {"1"}, "remind_expire": {"0"}}); r.data() != true {
		t.Fatalf("修改设置失败: %s", r.raw)
	}
	if p := e.sub(primary.ID); p.AutoRenewal != 1 || p.RemindExpire != 0 {
		t.Errorf("主订阅设置未修改: %+v", p)
	}
	if o := e.sub(other.ID); o.AutoRenewal != 0 || o.RemindExpire != 1 {
		t.Errorf("其它订阅不应被修改: %+v", o)
	}
	r := e.get("/api/v1/user/resetSecurity", token)
	if p := e.sub(primary.ID); p.Token == primary.Token || !strings.Contains(r.data().(string), p.Token) {
		t.Errorf("主订阅 token 应已重置: %s", r.raw)
	}
	if o := e.sub(other.ID); o.Token != other.Token || o.UUID != other.UUID {
		t.Error("其它订阅的 token 不应被重置")
	}

	// 礼品卡作用于主订阅，返回 data/type/value
	e.db.Create(&model.Giftcard{Code: "G1", Name: "g", Type: 3, Value: ptr(int64(2))})
	wantInvalid(t, e.post("/api/v1/user/redeemgiftcard", token, nil), "giftcard")
	r = e.post("/api/v1/user/redeemgiftcard", token, url.Values{"giftcard": {"G1"}})
	if r.data() != true || r.body["type"] != float64(3) || r.body["value"] != float64(2) {
		t.Fatalf("兑换礼品卡返回不正确: %s", r.raw)
	}
	if p := e.sub(primary.ID); p.TransferEnable != 1<<30+2<<30 {
		t.Errorf("流量应加到主订阅: %d", p.TransferEnable)
	}
	if o := e.sub(other.ID); o.TransferEnable != 999 {
		t.Error("其它订阅的流量不应变化")
	}
	wantMsg(t, e.post("/api/v1/user/redeemgiftcard", token, url.Values{"giftcard": {"G1"}}),
		http.StatusInternalServerError, e.zh("The gift card has already been used by this user"))

	// getStat、checkLogin
	if l := e.get("/api/v1/user/getStat", token).list(t); len(l) != 3 {
		t.Errorf("统计应为 3 项: %v", l)
	}
	if c := e.get("/api/v1/user/checkLogin", token).obj(t); c["is_login"] != true || c["is_admin"] != nil {
		t.Errorf("checkLogin 不正确: %v", c)
	}
	e.db.Model(u).Update("is_admin", 1)
	if c := e.get("/api/v1/user/checkLogin", token).obj(t); c["is_admin"] != true {
		t.Errorf("管理员 checkLogin 应带 is_admin: %v", c)
	}

	// transfer
	wantInvalid(t, e.post("/api/v1/user/transfer", token, url.Values{"transfer_amount": {"abc"}}), "transfer_amount")
	wantInvalid(t, e.post("/api/v1/user/transfer", token, url.Values{"transfer_amount": {"0"}}), "transfer_amount")
	wantMsg(t, e.post("/api/v1/user/transfer", token, url.Values{"transfer_amount": {"100"}}),
		http.StatusInternalServerError, e.zh("Insufficient commission balance"))
	e.db.Model(u).Update("commission_balance", 300)
	if r := e.post("/api/v1/user/transfer", token, map[string]any{"transfer_amount": 100}); r.data() != true {
		t.Fatalf("划转失败: %s", r.raw)
	}

	// 会话
	other2 := e.login(u.ID)
	sessions := e.get("/api/v1/user/getActiveSession", token).obj(t)
	if len(sessions) != 2 {
		t.Fatalf("应有 2 个会话: %v", sessions)
	}
	id2, _ := e.auth.Parse(ctx, other2)
	if _, ok := sessions[id2.Session]; !ok {
		t.Fatal("会话列表缺少第二个会话")
	}
	e.post("/api/v1/user/removeActiveSession", token, url.Values{"session_id": {id2.Session}})
	if _, err := e.auth.Parse(ctx, other2); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Error("删除的会话应失效")
	}

	// 快捷登录链接、修改密码校验
	if r := e.post("/api/v1/user/getQuickLoginUrl", token, url.Values{"redirect": {"order"}}); !strings.HasSuffix(r.data().(string), "&redirect=order") {
		t.Errorf("快捷登录链接不正确: %s", r.raw)
	}
	wantInvalid(t, e.post("/api/v1/user/changePassword", token, url.Values{"old_password": {"password123"}, "new_password": {"short"}}), "new_password")
	wantMsg(t, e.post("/api/v1/user/changePassword", token, url.Values{"old_password": {"wrong-pass"}, "new_password": {"newpass123"}}),
		http.StatusInternalServerError, e.zh("The old password is wrong"))

	// newPeriod 未开启
	wantMsg(t, e.post("/api/v1/user/newPeriod", token, nil), http.StatusInternalServerError, e.zh("Renewal is not allowed"))

	// 封禁后会话失效
	e.db.Model(u).Update("banned", 1)
	wantMsg(t, e.get("/api/v1/user/info", token), http.StatusForbidden, msgLoginRequired)
}

func testOrder(t *testing.T, e *env) {
	u, primary := e.createUser("order@x.com")
	token := e.login(u.ID)
	plan := model.Plan{Name: "P1", GroupID: 1, TransferEnable: 10, Show: 1, Renew: 1, MonthPrice: ptr(int64(1000))}
	free := model.Plan{Name: "Free", GroupID: 1, TransferEnable: 10, Show: 1, Renew: 1, MonthPrice: ptr(int64(0))}
	e.db.Create(&plan)
	e.db.Create(&free)

	if l := e.get("/api/v1/user/plan/fetch", token).list(t); len(l) != 2 {
		t.Errorf("套餐列表应有 2 个: %v", l)
	}
	if p := e.get("/api/v1/user/plan/fetch?id="+itoa(plan.ID), token).obj(t); p["name"] != "P1" {
		t.Errorf("套餐详情不正确: %v", p)
	}
	wantMsg(t, e.get("/api/v1/user/plan/fetch?id=abc", token), http.StatusInternalServerError, e.zh("Subscription plan does not exist"))

	wantInvalid(t, e.post("/api/v1/user/order/save", token, url.Values{"period": {"month_price"}}), "plan_id")
	wantInvalid(t, e.post("/api/v1/user/order/save", token, url.Values{"plan_id": {itoa(plan.ID)}, "period": {"week"}}), "period")

	// 套餐订单作用于主订阅
	r := e.post("/api/v1/user/order/save", token, url.Values{"plan_id": {itoa(plan.ID)}, "period": {"month_price"}})
	tradeNo, _ := r.data().(string)
	if r.code != http.StatusOK || tradeNo == "" {
		t.Fatalf("下单失败: %d %s", r.code, r.raw)
	}
	var o model.Order
	e.db.Where("trade_no = ?", tradeNo).Take(&o)
	if o.SubscriptionID == nil || *o.SubscriptionID != primary.ID {
		t.Errorf("订单应作用于主订阅: %v", o.SubscriptionID)
	}
	wantMsg(t, e.post("/api/v1/user/order/save", token, url.Values{"plan_id": {itoa(plan.ID)}, "period": {"month_price"}}),
		http.StatusInternalServerError, e.zh("You have an unpaid or pending order, please try again later or cancel it"))

	list := e.get("/api/v1/user/order/fetch", token).list(t)
	item, _ := list[0].(map[string]any)
	if len(list) != 1 || item["trade_no"] != tradeNo || item["id"] != nil || item["user_id"] != nil {
		t.Errorf("订单列表应隐藏 id 与 user_id: %v", list)
	}
	if _, has := item["id"]; has {
		t.Error("订单列表不应包含 id 键")
	}
	if p, _ := item["plan"].(map[string]any); p["name"] != "P1" {
		t.Errorf("订单列表应附带套餐: %v", item)
	}
	if l := e.get("/api/v1/user/order/fetch?status=3", token).list(t); len(l) != 0 {
		t.Errorf("按状态筛选不正确: %v", l)
	}
	d := e.get("/api/v1/user/order/detail?trade_no="+tradeNo, token).obj(t)
	if p, _ := d["plan"].(map[string]any); p["name"] != "P1" || d["try_out_plan_id"] != float64(0) || d["id"] == nil {
		t.Errorf("订单详情不正确: %v", d)
	}
	wantMsg(t, e.get("/api/v1/user/order/detail?trade_no=none", token), http.StatusInternalServerError, e.zh("Order does not exist or has been paid"))
	if r := e.get("/api/v1/user/order/check?trade_no="+tradeNo, token); r.data() != float64(0) {
		t.Errorf("订单状态应为 0: %s", r.raw)
	}
	wantMsg(t, e.post("/api/v1/user/order/cancel", token, nil), http.StatusInternalServerError, e.zh("Invalid parameter"))
	if r := e.post("/api/v1/user/order/cancel", token, url.Values{"trade_no": {tradeNo}}); r.data() != true {
		t.Fatalf("取消订单失败: %s", r.raw)
	}

	// 充值订单
	e.set(map[string]any{"deposit_bounus": []string{"10:5"}})
	r = e.post("/api/v1/user/order/save", token, map[string]any{"plan_id": 0, "period": "deposit", "deposit_amount": 1000})
	depositNo, _ := r.data().(string)
	d = e.get("/api/v1/user/order/detail?trade_no="+depositNo, token).obj(t)
	if p, _ := d["plan"].(map[string]any); p["name"] != "deposit" || d["bounus"] != float64(500) || d["get_amount"] != float64(1500) {
		t.Errorf("充值订单详情不正确: %v", d)
	}
	e.post("/api/v1/user/order/cancel", token, url.Values{"trade_no": {depositNo}})

	// 优惠券
	now := time.Now().Unix()
	e.db.Create(&model.Coupon{Code: "C1", Name: "c", Type: 1, Value: 100, Show: 1, StartedAt: now - 10, EndedAt: now + 1000})
	wantMsg(t, e.post("/api/v1/user/coupon/check", token, nil), http.StatusInternalServerError, e.zh("Coupon cannot be empty"))
	cp := e.post("/api/v1/user/coupon/check", token, url.Values{"code": {"C1"}, "plan_id": {itoa(plan.ID)}}).obj(t)
	if _, has := cp["limit_use"]; cp["code"] != "C1" || !has {
		t.Errorf("优惠券应返回完整字段: %v", cp)
	}

	if l := e.get("/api/v1/user/order/getPaymentMethod", token).list(t); len(l) != 0 {
		t.Errorf("支付方式应为空: %v", l)
	}

	// 免费订单结算：{"type":-1,"data":true}，不包在 data 中
	r = e.post("/api/v1/user/order/save", token, url.Values{"plan_id": {itoa(free.ID)}, "period": {"month_price"}})
	freeNo, _ := r.data().(string)
	r = e.post("/api/v1/user/order/checkout", token, url.Values{"trade_no": {freeNo}})
	if r.code != http.StatusOK || r.body["type"] != float64(-1) || r.body["data"] != true {
		t.Fatalf("免费订单结算不正确: %d %s", r.code, r.raw)
	}
}

func testMisc(t *testing.T, e *env) {
	u, primary := e.createUser("misc@x.com")
	token := e.login(u.ID)
	var other *model.Subscription
	e.db.Transaction(func(tx *gorm.DB) error {
		var err error
		other, err = e.subs.Create(tx, subscription.NewSubscription{UserID: u.ID})
		return err
	})

	// 邀请
	if r := e.get("/api/v1/user/invite/save", token); r.data() != true {
		t.Fatalf("生成邀请码失败: %s", r.raw)
	}
	inv := e.get("/api/v1/user/invite/fetch", token).obj(t)
	codes, _ := inv["codes"].([]any)
	stat, _ := inv["stat"].([]any)
	if c, _ := codes[0].(map[string]any); len(codes) != 1 || c["user_id"] != float64(u.ID) || len(stat) != 5 || stat[3] != float64(10) {
		t.Errorf("邀请信息不正确: %v", inv)
	}
	if r := e.get("/api/v1/user/invite/details", token); r.body["total"] != float64(0) {
		t.Errorf("佣金明细不正确: %s", r.raw)
	}

	// 公告：参数 pageSize，详情不存在时 404
	e.db.Create(&model.Notice{Title: "n1", Content: "c", Show: 1})
	e.db.Create(&model.Notice{Title: "n2", Content: "c", Show: 0})
	if r := e.get("/api/v1/user/notice/fetch?pageSize=1", token); len(r.list(t)) != 1 || r.body["total"] != float64(1) {
		t.Errorf("公告列表不正确: %s", r.raw)
	}
	wantMsg(t, e.get("/api/v1/user/notice/fetch?id=99999", token), http.StatusNotFound, "Notice not found")

	// 工单：消息列表键为 message
	wantInvalid(t, e.post("/api/v1/user/ticket/save", token, url.Values{"subject": {"s"}, "level": {"5"}, "message": {"m"}}), "level")
	if r := e.post("/api/v1/user/ticket/save", token, url.Values{"subject": {"s"}, "level": {"1"}, "message": {"hello"}}); r.data() != true {
		t.Fatalf("新建工单失败: %s", r.raw)
	}
	tickets := e.get("/api/v1/user/ticket/fetch", token).list(t)
	tk, _ := tickets[0].(map[string]any)
	tid := itoa(int64(tk["id"].(float64)))
	detail := e.get("/api/v1/user/ticket/fetch?id="+tid, token).obj(t)
	msgs, _ := detail["message"].([]any)
	if m0, _ := msgs[0].(map[string]any); len(msgs) != 1 || m0["is_me"] != true || m0["message"] != "hello" {
		t.Errorf("工单详情不正确: %v", detail)
	}
	wantMsg(t, e.post("/api/v1/user/ticket/reply", token, url.Values{"id": {tid}, "message": {"again"}}),
		http.StatusInternalServerError, e.zh("Please wait for the technical enginneer to reply"))
	wantMsg(t, e.post("/api/v1/user/ticket/reply", token, url.Values{"id": {tid}}), http.StatusInternalServerError, e.zh("Message cannot be empty"))
	if r := e.post("/api/v1/user/ticket/close", token, url.Values{"id": {tid}}); r.data() != true {
		t.Errorf("关闭工单失败: %s", r.raw)
	}
	wantInvalid(t, e.post("/api/v1/user/ticket/withdraw", token, url.Values{"withdraw_method": {"USDT"}}), "withdraw_account")

	// 节点列表：主订阅不可用时为空，ETag 命中返回 304
	r := e.get("/api/v1/user/server/fetch", token)
	etag := strings.Trim(r.hdr.Get("ETag"), `"`)
	if r.code != http.StatusOK || len(r.list(t)) != 0 || etag == "" {
		t.Fatalf("节点列表不正确: %d %v %s", r.code, r.hdr, r.raw)
	}
	r = e.do(e.r, http.MethodGet, "/api/v1/user/server/fetch", nil, map[string]string{"Authorization": token, "If-None-Match": `"` + etag + `"`})
	if r.code != http.StatusNotModified {
		t.Errorf("ETag 命中应返回 304，实际 %d", r.code)
	}

	// Telegram 未开启、配置
	wantMsg(t, e.get("/api/v1/user/telegram/getBotInfo", token), http.StatusInternalServerError, e.zh("Telegram bot is not enabled"))
	cfg := e.get("/api/v1/user/comm/config", token).obj(t)
	if m, _ := cfg["withdraw_methods"].([]any); cfg["currency"] != "CNY" || cfg["is_telegram"] != float64(0) || len(m) != 3 || cfg["stripe_pk"] != nil {
		t.Errorf("用户端配置不正确: %v", cfg)
	}

	// 知识库：列表为 {分类: [文章]}，保持排序；文章中的订阅变量按主订阅替换
	e.db.Create(&model.Knowledge{Language: "zh-CN", Category: "B", Title: "t1", Body: "token={{subscribeToken}}", Show: 1, Sort: ptr(int64(1))})
	e.db.Create(&model.Knowledge{Language: "zh-CN", Category: "A", Title: "t2", Body: "x", Show: 1, Sort: ptr(int64(2))})
	r = e.get("/api/v1/user/knowledge/fetch?language=zh-CN", token)
	groups := r.obj(t)
	if len(groups) != 2 || strings.Index(r.raw, `"B"`) > strings.Index(r.raw, `"A"`) {
		t.Errorf("知识库分组不正确: %s", r.raw)
	}
	b, _ := groups["B"].([]any)
	art, _ := b[0].(map[string]any)
	aid := itoa(int64(art["id"].(float64)))
	if a := e.get("/api/v1/user/knowledge/fetch?id="+aid, token).obj(t); a["body"] != "token="+primary.Token {
		t.Errorf("文章变量应按主订阅替换: %v", a["body"])
	}
	if r := e.get("/api/v1/user/knowledge/getCategory", token); r.code != http.StatusInternalServerError {
		t.Errorf("getCategory 应返回 500，实际 %d", r.code)
	}

	// 流量明细只含主订阅
	now := time.Now().Unix()
	e.db.Create(&model.StatUser{UserID: u.ID, SubscriptionID: &primary.ID, U: 1, D: 2, RecordType: "d", RecordAt: now, ServerRate: 1})
	e.db.Create(&model.StatUser{UserID: u.ID, SubscriptionID: &other.ID, U: 5, D: 5, RecordType: "d", RecordAt: now, ServerRate: 1})
	logs := e.get("/api/v1/user/stat/getTrafficLog", token).list(t)
	if l0, _ := logs[0].(map[string]any); len(logs) != 1 || l0["d"] != float64(2) {
		t.Errorf("流量明细应只含主订阅: %v", logs)
	}
}

func itoa(n int64) string { return strconv.FormatInt(n, 10) }
