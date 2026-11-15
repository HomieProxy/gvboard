package passport

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/auth"
	"gvboard/internal/cache"
	"gvboard/internal/config"
	"gvboard/internal/hook"
	"gvboard/internal/httpx"
	"gvboard/internal/i18n"
	"gvboard/internal/mail"
	"gvboard/internal/model"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/testutil"
	"gvboard/internal/util"
)

type fakeMail struct {
	mu   sync.Mutex
	sent []mail.Payload
	fail bool
}

func (f *fakeMail) Dispatch(_ context.Context, p mail.Payload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	if f.fail {
		return errors.New("队列不可用")
	}
	f.sent = append(f.sent, p)
	return nil
}

func (f *fakeMail) last(t *testing.T) mail.Payload {
	t.Helper()
	f.mu.Lock()
	defer f.mu.Unlock()
	if len(f.sent) == 0 {
		t.Fatal("没有发送邮件")
	}
	return f.sent[len(f.sent)-1]
}

type fakeCaptcha struct{ ok bool }

func (f *fakeCaptcha) Verify(_ context.Context, secret, response string) bool {
	return f.ok && secret != "" && response != ""
}

type env struct {
	svc     *Service
	db      *gorm.DB
	mail    *fakeMail
	captcha *fakeCaptcha
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
	e := &env{db: db, mail: &fakeMail{}, captcha: &fakeCaptcha{}}
	e.svc = &Service{
		DB: db, Cache: c, Settings: st, I18n: tr,
		Auth: auth.New(db, c, "base64:cGFzc3BvcnQtdGVzdC1rZXktMDEyMzQ1Ng=="),
		Mail: e.mail, Captcha: e.captcha,
	}
	return e
}

func (e *env) set(t *testing.T, kv map[string]any) {
	t.Helper()
	if err := e.svc.Settings.Set(context.Background(), kv); err != nil {
		t.Fatal(err)
	}
}

func wantErr(t *testing.T, err error, want *httpx.Error) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Msg != want.Msg {
		t.Fatalf("期望错误 %q，实际 %v", want.Msg, err)
	}
}

// must 出错时 panic（测试会失败并输出调用栈）。
func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

var cl = Client{IP: "10.0.0.1", UA: "test", Locale: "zh-CN"}

func TestPassport(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			t.Run("注册", func(t *testing.T) { testRegister(t, newEnv(t, driver)) })
			t.Run("注册限制", func(t *testing.T) { testRegisterPolicy(t, newEnv(t, driver)) })
			t.Run("邀请码", func(t *testing.T) { testInvite(t, newEnv(t, driver)) })
			t.Run("登录", func(t *testing.T) { testLogin(t, newEnv(t, driver)) })
			t.Run("验证码与找回密码", func(t *testing.T) { testEmailCodeAndReset(t, newEnv(t, driver)) })
			t.Run("快捷登录", func(t *testing.T) { testQuickLogin(t, newEnv(t, driver)) })
			t.Run("HTTP", func(t *testing.T) { testHTTP(t, newEnv(t, driver)) })
			t.Run("插件事件", func(t *testing.T) { testHooks(t, newEnv(t, driver)) })
		})
	}
}

type recHooks struct {
	mu     sync.Mutex
	events []string
	users  []hook.User
}

func (h *recHooks) Emit(_ context.Context, event string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, event)
	h.users = append(h.users, data.(hook.User))
}

func (h *recHooks) Filter(_ context.Context, _ string, v any, _ func(old, new any) error) any {
	return v
}

func testHooks(t *testing.T, e *env) {
	ctx := context.Background()
	h := &recHooks{}
	e.svc.Hooks = h
	res := must(e.svc.Register(ctx, RegisterInput{Email: "hk@x.com", Password: "password123"}, cl))
	id := must(e.svc.Auth.Parse(ctx, res.Token)).ID
	if _, err := e.svc.Register(ctx, RegisterInput{Email: "hk@x.com", Password: "password123"}, cl); err == nil {
		t.Fatal("重复注册应失败")
	}
	if _, err := e.svc.Login(ctx, LoginInput{Email: "hk@x.com", Password: "wrong-password"}, cl); err == nil {
		t.Fatal("密码错误应登录失败")
	}
	must(e.svc.Login(ctx, LoginInput{Email: "hk@x.com", Password: "password123"}, cl))
	want := []string{hook.EventUserRegistered, hook.EventUserLogin, hook.EventUserLogin}
	if strings.Join(h.events, ",") != strings.Join(want, ",") {
		t.Fatalf("事件错误：%v", h.events)
	}
	for _, u := range h.users {
		if u.UserID != id || u.Email != "hk@x.com" || u.IP != cl.IP {
			t.Fatalf("事件数据错误：%+v", u)
		}
	}
}

func testRegister(t *testing.T, e *env) {
	ctx := context.Background()
	res := must(e.svc.Register(ctx, RegisterInput{Email: "New@Example.com", Password: "password123"}, cl))
	id, err := e.svc.Auth.Parse(ctx, res.Token)
	if err != nil || res.IsAdmin {
		t.Fatalf("注册返回的凭证无效: %v", err)
	}

	var u model.User
	e.db.Take(&u, id.ID)
	var subs []model.Subscription
	e.db.Where("user_id = ?", u.ID).Find(&subs)
	if len(subs) != 1 {
		t.Fatalf("注册后应有 1 个订阅，实际 %d", len(subs))
	}
	s := subs[0]
	if s.IsPrimary != 1 || s.UUID != u.UUID || s.Token != u.Token || s.PlanID != nil ||
		s.ExpiredAt == nil || *s.ExpiredAt != 0 || s.RemindExpire != 1 || s.RemindTraffic != 1 || s.AutoRenewal != 0 {
		t.Errorf("主订阅字段不正确: %+v", s)
	}
	if u.ExpiredAt == nil || *u.ExpiredAt != 0 || u.RemindExpire == nil || *u.RemindExpire != 1 ||
		u.AutoRenewal == nil || *u.AutoRenewal != 0 || u.LastLoginAt == nil || len(u.Token) != 32 || len(u.UUID) != 36 {
		t.Errorf("用户字段与原项目默认值不一致: %+v", u)
	}
	if !util.VerifyPassword("", "", "password123", u.Password) || u.PasswordAlgo != nil {
		t.Error("密码应以 bcrypt 保存")
	}

	// 邮箱不区分大小写（决策 D14）
	_, err = e.svc.Register(ctx, RegisterInput{Email: "new@example.COM", Password: "password123"}, cl)
	wantErr(t, err, errEmailExists)

	_, err = e.svc.Register(ctx, RegisterInput{Email: "bad", Password: "password123"}, cl)
	wantErr(t, err, errEmailFormat)
	_, err = e.svc.Register(ctx, RegisterInput{Email: "x@example.com", Password: "short"}, cl)
	wantErr(t, err, errPasswordMin)

	// 试用套餐
	plan := model.Plan{GroupID: 3, TransferEnable: 10, Name: "试用"}
	dl := int64(2)
	plan.DeviceLimit = &dl
	e.db.Create(&plan)
	e.set(t, map[string]any{"try_out_plan_id": plan.ID, "try_out_hour": 2})
	res = must(e.svc.Register(ctx, RegisterInput{Email: "trial@example.com", Password: "password123"}, cl))
	id, _ = e.svc.Auth.Parse(ctx, res.Token)
	var ts model.Subscription
	e.db.Where("user_id = ? AND is_primary = 1", id.ID).Take(&ts)
	now := time.Now().Unix()
	if ts.PlanID == nil || *ts.PlanID != plan.ID || ts.GroupID == nil || *ts.GroupID != 3 ||
		ts.TransferEnable != 10*1073741824 || ts.DeviceLimit == nil || *ts.DeviceLimit != 2 ||
		ts.ExpiredAt == nil || *ts.ExpiredAt < now+7190 || *ts.ExpiredAt > now+7210 {
		t.Errorf("试用套餐未正确开通: %+v", ts)
	}
}

func testRegisterPolicy(t *testing.T, e *env) {
	ctx := context.Background()
	reg := func(email string) error {
		_, err := e.svc.Register(ctx, RegisterInput{Email: email, Password: "password123", Captcha: "c"}, cl)
		return err
	}

	e.set(t, map[string]any{"email_whitelist_enable": 1, "email_whitelist_suffix": []string{"ok.com"}})
	wantErr(t, reg("a@bad.com"), errWhitelist)
	if err := reg("a@ok.com"); err != nil {
		t.Fatal(err)
	}
	e.set(t, map[string]any{"email_whitelist_enable": 0, "email_gmail_limit_enable": 1})
	wantErr(t, reg("a.b@gmail.com"), errGmailAlias)
	e.set(t, map[string]any{"email_gmail_limit_enable": 0, "recaptcha_enable": 1, "recaptcha_key": "secret"})
	wantErr(t, reg("c@x.com"), errCaptcha)
	e.captcha.ok = true
	if err := reg("c@x.com"); err != nil {
		t.Fatal(err)
	}
	e.set(t, map[string]any{"recaptcha_enable": 0, "stop_register": 1})
	wantErr(t, reg("d@x.com"), errRegisterClosed)
	e.set(t, map[string]any{"stop_register": 0})

	// 同一 IP 注册次数限制
	e.set(t, map[string]any{"register_limit_by_ip_enable": 1, "register_limit_count": 1, "register_limit_expire": 30})
	other := Client{IP: "10.9.9.9"}
	if _, err := e.svc.Register(ctx, RegisterInput{Email: "ip1@x.com", Password: "password123"}, other); err != nil {
		t.Fatal(err)
	}
	_, err := e.svc.Register(ctx, RegisterInput{Email: "ip2@x.com", Password: "password123"}, other)
	wantErr(t, err, errRegisterLimit)
	var he *httpx.Error
	if errors.As(err, &he); he.Params["minute"] != int64(30) {
		t.Errorf("提示中的分钟数应为 30，实际 %v", he.Params)
	}
	e.set(t, map[string]any{"register_limit_by_ip_enable": 0})

	// 邮箱验证码
	e.set(t, map[string]any{"email_verify": 1})
	_, err = e.svc.Register(ctx, RegisterInput{Email: "v@x.com", Password: "password123", EmailCode: "123456"}, cl)
	wantErr(t, err, errEmailCode)
	if err := e.svc.SendEmailCode(ctx, EmailCodeInput{Email: "V@x.com", Scene: SceneRegister}, cl); err != nil {
		t.Fatal(err)
	}
	code := e.mail.last(t).TemplateValue["code"].(string)
	if _, err := e.svc.Register(ctx, RegisterInput{Email: "v@x.com", Password: "password123", EmailCode: code}, cl); err != nil {
		t.Fatalf("验证码正确时注册失败（缓存键应不区分大小写）: %v", err)
	}
	_, err = e.svc.Register(ctx, RegisterInput{Email: "v2@x.com", Password: "password123", EmailCode: code}, cl)
	wantErr(t, err, errEmailCode)
}

func testInvite(t *testing.T, e *env) {
	ctx := context.Background()
	inviter := must(e.svc.Register(ctx, RegisterInput{Email: "inviter@x.com", Password: "password123"}, cl))
	inviterID := must(e.svc.Auth.Parse(ctx, inviter.Token)).ID
	e.db.Create(&model.InviteCode{UserID: inviterID, Code: "CODE1"})
	e.db.Create(&model.InviteCode{UserID: 0, Code: "SYS01"})

	e.set(t, map[string]any{"invite_force": 1})
	_, err := e.svc.Register(ctx, RegisterInput{Email: "a@x.com", Password: "password123"}, cl)
	wantErr(t, err, errInviteRequired)
	_, err = e.svc.Register(ctx, RegisterInput{Email: "a@x.com", Password: "password123", InviteCode: "NOPE"}, cl)
	wantErr(t, err, errInviteInvalid)

	res := must(e.svc.Register(ctx, RegisterInput{Email: "a@x.com", Password: "password123", InviteCode: "CODE1"}, cl))
	var u model.User
	e.db.Take(&u, must(e.svc.Auth.Parse(ctx, res.Token)).ID)
	if u.InviteUserID == nil || *u.InviteUserID != inviterID {
		t.Errorf("邀请人不正确: %v", u.InviteUserID)
	}
	var ic model.InviteCode
	e.db.Where("code = ?", "CODE1").Take(&ic)
	if ic.Status != 1 {
		t.Error("邀请码使用后应作废")
	}
	_, err = e.svc.Register(ctx, RegisterInput{Email: "b@x.com", Password: "password123", InviteCode: "CODE1"}, cl)
	wantErr(t, err, errInviteInvalid)

	// 所属用户为 0 的邀请码有效，但没有邀请人（与原项目一致）
	res = must(e.svc.Register(ctx, RegisterInput{Email: "c@x.com", Password: "password123", InviteCode: "SYS01"}, cl))
	var u2 model.User
	e.db.Take(&u2, must(e.svc.Auth.Parse(ctx, res.Token)).ID)
	if u2.InviteUserID != nil {
		t.Errorf("系统邀请码不应产生邀请人: %v", *u2.InviteUserID)
	}

	// 邀请码永不失效
	e.set(t, map[string]any{"invite_never_expire": 1})
	e.db.Create(&model.InviteCode{UserID: inviterID, Code: "CODE2"})
	must(e.svc.Register(ctx, RegisterInput{Email: "d@x.com", Password: "password123", InviteCode: "CODE2"}, cl))
	must(e.svc.Register(ctx, RegisterInput{Email: "e@x.com", Password: "password123", InviteCode: "CODE2"}, cl))

	// 访问统计
	for i := 0; i < 3; i++ {
		if err := e.svc.InviteVisit(ctx, "CODE2"); err != nil {
			t.Fatal(err)
		}
	}
	_ = e.svc.InviteVisit(ctx, "NOPE")
	var ic2 model.InviteCode
	e.db.Where("code = ?", "CODE2").Take(&ic2)
	if ic2.PV != 3 || ic2.Status != 0 {
		t.Errorf("访问次数应为 3 且邀请码仍有效，实际 pv=%d status=%d", ic2.PV, ic2.Status)
	}
}

func testLogin(t *testing.T, e *env) {
	ctx := context.Background()
	must(e.svc.Register(ctx, RegisterInput{Email: "User@x.com", Password: "password123"}, cl))
	login := func(email, pwd string) (*AuthResult, error) {
		return e.svc.Login(ctx, LoginInput{Email: email, Password: pwd}, cl)
	}
	if _, err := login("user@X.com", "password123"); err != nil {
		t.Fatalf("邮箱大小写不同应能登录: %v", err)
	}
	_, err := login("nobody@x.com", "password123")
	wantErr(t, err, errLoginFailed)

	// 原项目导入的旧密码格式
	md5hash := "482c811da5d5b4bc6d497ffa98491e38" // md5("password123")
	algo := "md5"
	e.db.Model(&model.User{}).Where("email = ?", "User@x.com").Updates(map[string]any{"password": md5hash, "password_algo": algo})
	if _, err := login("User@x.com", "password123"); err != nil {
		t.Fatalf("md5 旧密码应能登录: %v", err)
	}

	// 密码错误次数限制（默认开启，5 次）
	for i := 0; i < 5; i++ {
		_, err := login("USER@x.com", "wrong-password")
		wantErr(t, err, errLoginFailed)
	}
	_, err = login("user@x.com", "password123")
	wantErr(t, err, errPasswordLimit)

	e.set(t, map[string]any{"password_limit_enable": 0})
	if _, err := login("user@x.com", "password123"); err != nil {
		t.Fatalf("关闭限制后应能登录: %v", err)
	}

	// 按 IP 限制登录次数（默认关闭）：成功与失败都计数，换 IP 不受影响
	e.set(t, map[string]any{"login_limit_by_ip_enable": 1, "login_limit_by_ip_count": 2, "login_limit_by_ip_expire": 10})
	ipA, ipB := Client{IP: "10.8.8.8"}, Client{IP: "10.8.8.9"}
	if _, err := e.svc.Login(ctx, LoginInput{Email: "user@x.com", Password: "password123"}, ipA); err != nil {
		t.Fatal(err)
	}
	_, err = e.svc.Login(ctx, LoginInput{Email: "nobody@x.com", Password: "password123"}, ipA)
	wantErr(t, err, errLoginFailed)
	_, err = e.svc.Login(ctx, LoginInput{Email: "user@x.com", Password: "password123"}, ipA)
	wantErr(t, err, errTooMany)
	if ttl := e.svc.Cache.TTL(ctx, e.svc.Cache.KeyOf(cache.KeyLoginIPRateLimit, ipA.IP)).Val(); ttl <= 9*time.Minute || ttl > 10*time.Minute {
		t.Errorf("限制时间应为 10 分钟，实际 %v", ttl)
	}
	if _, err := e.svc.Login(ctx, LoginInput{Email: "user@x.com", Password: "password123"}, ipB); err != nil {
		t.Fatalf("其它 IP 不应受影响: %v", err)
	}
	e.set(t, map[string]any{"login_limit_by_ip_enable": 0})
	if _, err := e.svc.Login(ctx, LoginInput{Email: "user@x.com", Password: "password123"}, ipA); err != nil {
		t.Fatalf("关闭后应能登录: %v", err)
	}

	e.db.Model(&model.User{}).Where("email = ?", "User@x.com").Update("banned", 1)
	_, err = login("user@x.com", "password123")
	wantErr(t, err, errSuspended)
}

func testEmailCodeAndReset(t *testing.T, e *env) {
	ctx := context.Background()
	old := must(e.svc.Register(ctx, RegisterInput{Email: "r@x.com", Password: "password123"}, cl))

	send := func(email, scene string, c Client) error {
		return e.svc.SendEmailCode(ctx, EmailCodeInput{Email: email, Scene: scene}, c)
	}
	wantErr(t, send("r@x.com", SceneRegister, Client{IP: "1.1.1.1"}), errEmailRegistered)
	wantErr(t, send("none@x.com", SceneReset, Client{IP: "1.1.1.2"}), errNotRegistered)
	wantErr(t, send("r@x.com", "other", Client{IP: "1.1.1.3"}), errInvalidParams)

	if err := send("r@x.com", SceneReset, Client{IP: "1.1.1.4", Locale: "en-US"}); err != nil {
		t.Fatal(err)
	}
	p := e.mail.last(t)
	code := p.TemplateValue["code"].(string)
	if p.Email != "r@x.com" || p.TemplateName != mail.TplVerify || len(code) != 6 ||
		p.Subject != "GvboardEmail verification code" || p.TemplateValue["name"] != "Gvboard" {
		t.Errorf("验证码邮件不正确: %+v", p)
	}
	wantErr(t, send("r@x.com", "", Client{IP: "1.1.1.5"}), errCodeSent)

	// 同一 IP 每分钟最多 3 次
	ip := Client{IP: "2.2.2.2"}
	for i, email := range []string{"q1@x.com", "q2@x.com", "q3@x.com"} {
		if err := send(email, "", ip); err != nil {
			t.Fatalf("第 %d 次发送失败: %v", i+1, err)
		}
	}
	wantErr(t, send("q4@x.com", "", ip), errTooMany)

	// 投递失败时允许立即重发
	e.mail.fail = true
	if send("f@x.com", "", Client{IP: "3.3.3.3"}) == nil {
		t.Fatal("投递失败应返回错误")
	}
	e.mail.fail = false
	if err := send("f@x.com", "", Client{IP: "3.3.3.4"}); err != nil {
		t.Errorf("投递失败后应能立即重发: %v", err)
	}

	// 找回密码
	reset := func(email, c, pwd string) error {
		return e.svc.ResetPassword(ctx, ResetInput{Email: email, EmailCode: c, Password: pwd})
	}
	wantErr(t, reset("r@x.com", "12a456", "newpassword1"), errEmailCode)
	wantErr(t, reset("r@x.com", "", "newpassword1"), errEmailCodeRequired)
	wrong := "000000"
	if code == wrong {
		wrong = "111111"
	}
	if err := reset("R@x.com", code, "newpassword1"); err != nil {
		t.Fatalf("重置密码失败: %v", err)
	}
	if _, err := e.svc.Auth.Parse(ctx, old.Token); !errors.Is(err, auth.ErrUnauthenticated) {
		t.Error("重置密码后原会话应失效")
	}
	var u model.User
	e.db.Where("email = ?", "r@x.com").Take(&u)
	if u.PasswordAlgo != nil || u.PasswordSalt != nil || !util.VerifyPassword("", "", "newpassword1", u.Password) {
		t.Error("重置后密码应为 bcrypt 且清除旧算法")
	}
	wantErr(t, reset("r@x.com", code, "newpassword2"), errEmailCode)

	// 验证码连续错误 3 次后锁定
	for i := 0; i < 2; i++ {
		wantErr(t, reset("r@x.com", wrong, "newpassword2"), errEmailCode)
	}
	wantErr(t, reset("r@x.com", wrong, "newpassword2"), errResetLimit)
}

func testQuickLogin(t *testing.T, e *env) {
	ctx := context.Background()
	res := must(e.svc.Register(ctx, RegisterInput{Email: "q@x.com", Password: "password123"}, cl))
	uid := must(e.svc.Auth.Parse(ctx, res.Token)).ID

	link := must(e.svc.QuickLoginURL(ctx, uid, "", "http://req.host"))
	if !strings.HasPrefix(link, "http://req.host/#/login?verify=") || !strings.HasSuffix(link, "&redirect=dashboard") {
		t.Errorf("快捷登录链接格式不正确: %s", link)
	}
	e.set(t, map[string]any{"app_url": "https://site.example"})
	link = must(e.svc.QuickLoginURL(ctx, uid, "plan?id=1", "http://req.host"))
	u, err := url.Parse(strings.Replace(link, "/#/", "/", 1))
	if err != nil || u.Host != "site.example" || u.Query().Get("redirect") != "plan?id=1" {
		t.Fatalf("快捷登录链接应使用站点地址并编码跳转参数: %s", link)
	}
	code := u.Query().Get("verify")
	r := must(e.svc.CodeLogin(ctx, code, cl))
	if must(e.svc.Auth.Parse(ctx, r.Token)).ID != uid {
		t.Error("快捷登录得到的用户不正确")
	}
	_, err = e.svc.CodeLogin(ctx, code, cl)
	wantErr(t, err, errTokenInvalid)

	code2 := must(e.svc.Auth.CreateTempToken(ctx, uid))
	e.db.Model(&model.User{}).Where("id = ?", uid).Update("banned", 1)
	_, err = e.svc.CodeLogin(ctx, code2, cl)
	wantErr(t, err, errSuspended)
}

func testHTTP(t *testing.T, e *env) {
	httpx.SetTranslator(func(c *gin.Context, key string, params map[string]any) string {
		return e.svc.I18n.Tr(c, key, params)
	})
	t.Cleanup(func() { httpx.SetTranslator(nil) })

	newRouter := func(mode string) *gin.Engine {
		cfg := &config.Config{Server: config.ServerConfig{Mode: mode}, API: config.APIConfig{Prefix: "/api/t1"}}
		r, err := server.NewRouter(cfg, zap.NewNop(), e.svc.I18n.Middleware())
		if err != nil {
			t.Fatal(err)
		}
		r.Mount(&Module{Svc: e.svc})
		return r.Engine
	}
	all, api := newRouter(config.ModeAll), newRouter(config.ModeAPI)

	do := func(h http.Handler, method, path, body string, header map[string]string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		for k, v := range header {
			req.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		h.ServeHTTP(w, req)
		return w
	}
	decode := func(w *httptest.ResponseRecorder) httpx.Body {
		var b httpx.Body
		if err := json.Unmarshal(w.Body.Bytes(), &b); err != nil {
			t.Fatalf("响应不是 JSON: %s", w.Body)
		}
		return b
	}

	w := do(all, "POST", "/api/t1/auth/register", `{"email":"h@x.com","password":"password123"}`, nil)
	if b := decode(w); w.Code != 200 || b.Code != 0 {
		t.Fatalf("注册失败: %d %s", w.Code, w.Body)
	}
	w = do(all, "POST", "/api/t1/auth/login", `{"email":"h@x.com","password":"password123"}`, nil)
	var login struct {
		Data AuthResult `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &login)
	if w.Code != 200 || login.Data.Token == "" {
		t.Fatalf("登录失败: %d %s", w.Code, w.Body)
	}

	// 错误信息按 Content-Language 翻译，默认中文
	w = do(all, "POST", "/api/t1/auth/login", `{"email":"h@x.com","password":"wrong-password"}`, nil)
	if b := decode(w); w.Code != 400 || b.Msg != "邮箱或密码错误" {
		t.Errorf("默认应返回中文错误: %d %s", w.Code, w.Body)
	}
	w = do(all, "POST", "/api/t1/auth/login", `{"email":"h@x.com","password":"wrong-password"}`, map[string]string{"Content-Language": "en-US"})
	if b := decode(w); b.Msg != "Incorrect email or password" {
		t.Errorf("Content-Language 为 en-US 时应返回英文: %s", w.Body)
	}
	w = do(all, "POST", "/api/t1/auth/login", `{bad json`, nil)
	if b := decode(w); w.Code != 422 || b.Msg != "参数有误" {
		t.Errorf("非法 JSON 应返回 422: %d %s", w.Code, w.Body)
	}

	// 生成快捷登录链接需要登录
	if w := do(all, "POST", "/api/t1/auth/quick-login", "", nil); w.Code != 401 || decode(w).Msg != "未登录或登录已过期" {
		t.Errorf("未登录生成快捷登录链接应返回 401: %d %s", w.Code, w.Body)
	}
	w = do(all, "POST", "/api/t1/auth/quick-login", "", map[string]string{"Authorization": login.Data.Token})
	if b := decode(w); w.Code != 200 || !strings.Contains(b.Data.(string), "/#/login?verify=") {
		t.Errorf("生成快捷登录链接失败: %d %s", w.Code, w.Body)
	}

	// 公开站点配置只在分离部署时提供，不分离部署时与不存在的路径完全一致
	notFound := do(all, "GET", "/api/t1/not-exist", "", nil)
	w = do(all, "GET", "/api/t1/site", "", nil)
	if w.Code != 404 || w.Body.String() != notFound.Body.String() {
		t.Errorf("不分离部署时 /site 应返回统一 404: %d", w.Code)
	}
	e.set(t, map[string]any{"recaptcha_enable": 1, "recaptcha_site_key": "site-key", "recaptcha_key": "secret-key", "email_whitelist_enable": 1})
	w = do(api, "GET", "/api/t1/site", "", nil)
	var site struct {
		Data SiteConfig `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &site)
	if w.Code != 200 || site.Data.Site.Name != "Gvboard" || !site.Data.Site.Home || site.Data.Register.CaptchaKey != "site-key" || len(site.Data.Register.EmailSuffixes) == 0 {
		t.Errorf("公开站点配置不正确（首页默认开启）: %s", w.Body)
	}
	for _, leak := range []string{"secret-key", "is_email_verify", "recaptcha", "v2board"} {
		if strings.Contains(strings.ToLower(w.Body.String()), strings.ToLower(leak)) {
			t.Errorf("公开站点配置不应包含 %q: %s", leak, w.Body)
		}
	}
	e.set(t, map[string]any{"home_enable": 0})
	site.Data = SiteConfig{}
	_ = json.Unmarshal(do(api, "GET", "/api/t1/site", "", nil).Body.Bytes(), &site)
	if site.Data.Site.Home {
		t.Error("关闭首页后 site.home 应为 false")
	}
}
