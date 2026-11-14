package telegram

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/cache"
	"gvboard/internal/config"
	"gvboard/internal/i18n"
	"gvboard/internal/invite"
	"gvboard/internal/mail"
	"gvboard/internal/model"
	"gvboard/internal/order"
	"gvboard/internal/payment"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/subscribe"
	"gvboard/internal/subscription"
	"gvboard/internal/testutil"
	"gvboard/internal/ticket"
)

const testToken = "123456:TEST-token"

// ---------- 模拟 Bot API ----------

type apiCall struct {
	Method string
	Params map[string]any
}

type fakeAPI struct {
	mu      sync.Mutex
	calls   []apiCall
	updates [][]Update
	fail    map[string]int
	srv     *httptest.Server
}

func newFakeAPI(t *testing.T) *fakeAPI {
	f := &fakeAPI{fail: map[string]int{}}
	f.srv = httptest.NewServer(http.HandlerFunc(f.serve))
	t.Cleanup(f.srv.Close)
	return f
}

func (f *fakeAPI) serve(w http.ResponseWriter, r *http.Request) {
	prefix := "/bot" + testToken + "/"
	if !strings.HasPrefix(r.URL.Path, prefix) {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"ok":false,"error_code":401,"description":"Unauthorized"}`))
		return
	}
	method := strings.TrimPrefix(r.URL.Path, prefix)
	p := map[string]any{}
	if strings.HasPrefix(r.Header.Get("Content-Type"), "multipart/") {
		if err := r.ParseMultipartForm(4 << 20); err == nil {
			for k, v := range r.MultipartForm.Value {
				p[k] = v[0]
			}
			if fh := r.MultipartForm.File["photo"]; len(fh) == 1 {
				p["photo_size"] = float64(fh[0].Size)
			}
		}
	} else {
		_ = json.NewDecoder(r.Body).Decode(&p)
	}
	f.mu.Lock()
	f.calls = append(f.calls, apiCall{Method: method, Params: p})
	code := f.fail[method]
	if text, _ := p["text"].(string); strings.Contains(text, "<bad>") {
		code = 400
	}
	var ups []Update
	if method == "getUpdates" && len(f.updates) > 0 {
		ups, f.updates = f.updates[0], f.updates[1:]
	}
	f.mu.Unlock()
	if code != 0 {
		_, _ = fmt.Fprintf(w, `{"ok":false,"error_code":%d,"description":"failed"}`, code)
		return
	}
	var result any = true
	switch method {
	case "getMe":
		result = map[string]any{"id": 42, "is_bot": true, "username": "gvb_test_bot"}
	case "getUpdates":
		if ups == nil {
			time.Sleep(20 * time.Millisecond)
			ups = []Update{}
		}
		result = ups
	case "getWebhookInfo":
		result = map[string]any{"url": "", "pending_update_count": 3}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"ok": true, "result": result})
}

func (f *fakeAPI) messages(chatID int64) []string {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []string
	for _, c := range f.calls {
		if (c.Method == "sendMessage" || c.Method == "editMessageText") && chatOf(c) == chatID {
			out = append(out, c.Params["text"].(string))
		}
	}
	return out
}

func chatOf(c apiCall) int64 {
	switch v := c.Params["chat_id"].(type) {
	case float64:
		return int64(v)
	case string:
		n, _ := strconv.ParseInt(v, 10, 64)
		return n
	}
	return 0
}

func (f *fakeAPI) last(chatID int64) string {
	m := f.messages(chatID)
	if len(m) == 0 {
		return ""
	}
	return m[len(m)-1]
}

func (f *fakeAPI) called(method string) []apiCall {
	f.mu.Lock()
	defer f.mu.Unlock()
	var out []apiCall
	for _, c := range f.calls {
		if c.Method == method {
			out = append(out, c)
		}
	}
	return out
}

func (f *fakeAPI) reset() {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = nil
}

// ---------- 测试环境 ----------

type fakeMail struct{}

func (fakeMail) Dispatch(context.Context, mail.Payload) error { return nil }

type fakeGeo struct{}

func (fakeGeo) Locate(ip string) string {
	if ip == "1.2.3.4" {
		return "中国 广东 深圳 电信"
	}
	return ""
}
func (fakeGeo) Close() {}

type fakePayer struct {
	typ  int
	data any
}

func (p *fakePayer) Pay(context.Context, *model.Order, order.PayInput) (int, any, error) {
	return p.typ, p.data, nil
}

type fakeMethods []payment.Method

func (f fakeMethods) Methods(context.Context) ([]payment.Method, error) { return f, nil }

type env struct {
	*Service
	api     *fakeAPI
	auth    *auth.Service
	tickets *ticket.Service
	payer   *fakePayer
	seq     int
}

var updateSeq atomic.Int64

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
	e := &env{api: newFakeAPI(t), auth: auth.New(db, c, "base64:dGVsZWdyYW0tdGVzdC1rZXktMDEyMzQ1Njc4OWFiY2Q=")}
	subs := &subscription.Service{DB: db, Cache: c, Settings: st, Linker: &subscription.Linker{Settings: st, Cache: c}}
	e.tickets = &ticket.Service{
		DB: db, Cache: c, Settings: st, Subscriptions: subs, Mail: fakeMail{},
		Translate: func(locale, key string, params map[string]any) string { return tr.T(locale, key, params) },
	}
	e.payer = &fakePayer{}
	e.Service = &Service{
		DB: db, Cache: c, Settings: st, I18n: tr, Geo: fakeGeo{}, APIPrefix: "/api/t1",
		Subscribe: &subscribe.Service{DB: db, Cache: c, Settings: st, Linker: subs.Linker},
		Tickets:   e.tickets, Subscriptions: subs,
		Orders:   &order.Service{DB: db, Settings: st, Subscriptions: subs, Payer: e.payer},
		Invites:  &invite.Service{DB: db, Settings: st},
		Payments: fakeMethods{{ID: 1, Name: "支付宝", Payment: "EPay"}, {ID: 2, Name: "信用卡", Payment: "StripeCredit"}},
	}
	e.tickets.OnEvent = e.OnTicket
	e.tickets.OnAdminReply = e.OnAdminReply
	e.set(t, map[string]any{
		"telegram_bot_enable": 1, "telegram_bot_token": testToken, "telegram_api_url": e.api.srv.URL,
		"app_name": "Gvb<Test>", "app_url": "https://gvb.example",
	})
	return e
}

func (e *env) set(t *testing.T, kv map[string]any) {
	t.Helper()
	if err := e.Settings.Set(context.Background(), kv); err != nil {
		t.Fatal(err)
	}
}

func (e *env) user(t *testing.T) (*model.User, *model.Subscription) {
	t.Helper()
	e.seq++
	var u *model.User
	var sub *model.Subscription
	if err := e.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		u, sub, err = account.Create(tx, account.NewUser{Email: fmt.Sprintf("tg%d@x.com", e.seq), Password: "password123"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return u, sub
}

func (e *env) bindDirect(t *testing.T, u *model.User, tgID int64) {
	t.Helper()
	if err := e.DB.Model(&model.User{}).Where("id = ?", u.ID).Update("telegram_id", tgID).Error; err != nil {
		t.Fatal(err)
	}
}

func (e *env) telegramID(t *testing.T, userID int64) *int64 {
	t.Helper()
	var u model.User
	if err := e.DB.Select("telegram_id").Where("id = ?", userID).Take(&u).Error; err != nil {
		t.Fatal(err)
	}
	return u.TelegramID
}

func message(from int64, lang, text string) Update {
	id := updateSeq.Add(1)
	return Update{UpdateID: id, Message: &Message{
		MessageID: id, From: &User{ID: from, LanguageCode: lang}, Chat: Chat{ID: from, Type: "private"}, Text: text,
	}}
}

// send 发送一条消息，返回机器人因此发出 / 修改的全部消息文本。
func (e *env) send(from int64, lang, text string) string {
	n := len(e.api.messages(from))
	e.HandleUpdate(context.Background(), message(from, lang, text))
	return strings.Join(e.api.messages(from)[n:], "\n----\n")
}

func wantContains(t *testing.T, got string, subs ...string) {
	t.Helper()
	for _, s := range subs {
		if !strings.Contains(got, s) {
			t.Errorf("消息应包含 %q，实际：%q", s, got)
		}
	}
}

func eachDriver(t *testing.T, f func(t *testing.T, e *env)) {
	for _, d := range testutil.Drivers() {
		t.Run(d, func(t *testing.T) { f(t, newEnv(t, d)) })
	}
}

// ---------- 测试 ----------

func TestHelpers(t *testing.T) {
	for b, want := range map[int64]string{0: "0MB", 1 << 20: "1MB", 1536 << 10: "1.5MB", 1 << 30: "1GB", 3<<30 + 1<<28: "3.25GB", -(1 << 20): "-1MB"} {
		if got := flow(b); got != want {
			t.Errorf("flow(%d) = %s，期望 %s", b, got, want)
		}
	}
	for c, want := range map[int64]string{0: "0", 1000: "10", 1050: "10.5", 1: "0.01"} {
		if got := money(c); got != want {
			t.Errorf("money(%d) = %s，期望 %s", c, got, want)
		}
	}
	if got := truncate("你好世界", 2); got != "你好…" {
		t.Errorf("truncate = %q", got)
	}
	tr, _ := i18n.New("en-US")
	s := &Service{I18n: tr}
	for code, want := range map[string]string{"zh-hans": "zh-CN", "zh-TW": "zh-CN", "en": "en-US", "ru": "en-US", "": "en-US"} {
		if got := s.LocaleOf(code); got != want {
			t.Errorf("LocaleOf(%q) = %s，期望 %s", code, got, want)
		}
	}
}

func TestClientHidesToken(t *testing.T) {
	c := &Client{Base: "http://127.0.0.1:1", Token: testToken, HTTP: &http.Client{Timeout: time.Second}}
	err := c.Call(context.Background(), "getMe", struct{}{}, nil)
	if err == nil || strings.Contains(err.Error(), testToken) {
		t.Fatalf("网络错误中不应包含 token：%v", err)
	}
	api := newFakeAPI(t)
	api.fail["sendMessage"] = 403
	c = &Client{Base: api.srv.URL, Token: testToken, HTTP: http.DefaultClient}
	err = c.SendMessage(context.Background(), OutMessage{ChatID: 1, Text: "x"})
	if !IsAPIError(err, 403) || IsAPIError(err, 400) {
		t.Fatalf("应返回 403 APIError：%v", err)
	}
}

func TestCommandsAndBinding(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u1, sub1 := e.user(t)
		u2, sub2 := e.user(t)
		const tg = int64(1001)

		// 未绑定的欢迎信息：中文、HTML 转义站点名称、列出命令
		wantContains(t, e.send(tg, "zh-hans", "/start"), "欢迎使用 Gvb&lt;Test&gt;", "您还没有绑定账号", "/bind — 使用订阅链接绑定账号", "/getlatesturl")
		wantContains(t, e.send(tg, "ru", "/traffic"), "没有查询到您的用户信息")
		wantContains(t, e.send(tg, "zh", "/nope"), "未知命令")

		// 网站一键绑定
		link, err := e.NewBindLink(ctx, u1.ID)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(link.URL, "https://t.me/gvb_test_bot?start=bind_") || link.ExpiresIn != 600 {
			t.Fatalf("绑定链接错误：%+v", link)
		}
		code := strings.TrimPrefix(link.URL, "https://t.me/gvb_test_bot?start=")
		wantContains(t, e.send(tg, "en", "/start "+code), "Bind successfully")
		if id := e.telegramID(t, u1.ID); id == nil || *id != tg {
			t.Fatalf("绑定后 telegram_id 错误：%v", id)
		}
		p, _ := e.prefs(ctx, u1.ID)
		if p.Language == nil || *p.Language != "en-US" || p.NotifyOrder != 1 {
			t.Fatalf("绑定后应记录语言并默认开启通知：%+v", p)
		}
		wantContains(t, e.send(tg, "en", "/start "+code), "The bind link has expired")
		if _, err := e.NewBindLink(ctx, u1.ID); !errors.Is(err, errAccountBound) {
			t.Fatalf("已绑定时生成链接应失败：%v", err)
		}
		wantContains(t, e.send(tg, "zh", "/start"), "已绑定账号：<code>tg1@x.com</code>")

		// /bind：参数检查；该 Telegram 已绑定其他账号
		wantContains(t, e.send(tg, "zh", "/bind"), "参数有误")
		wantContains(t, e.send(tg, "zh", "/bind https://x.com/s?token=nope"), "订阅地址无效")
		subURL := "https://sub.example/api/v1/client/subscribe?token=" + sub2.Token
		wantContains(t, e.send(tg, "zh", "/bind "+subURL), "已绑定其他账号")

		// /traffic 列出所有订阅；@其他机器人与群聊忽略
		e.api.reset()
		wantContains(t, e.send(tg, "zh", "/traffic@gvb_test_bot"), "🚥 流量查询", "<b>", "计划流量：<code>")
		_ = sub1
		e.api.reset()
		e.send(tg, "zh", "/traffic@other_bot")
		group := message(tg, "zh", "/traffic")
		group.Message.Chat = Chat{ID: -100, Type: "supergroup"}
		e.HandleUpdate(ctx, group)
		if n := len(e.api.called("sendMessage")); n != 0 {
			t.Fatalf("应忽略其他机器人的命令与群聊中的私聊命令，实际发送 %d 条", n)
		}
		group = message(tg, "zh", "/getlatesturl")
		group.Message.Chat = Chat{ID: -100, Type: "supergroup"}
		e.HandleUpdate(ctx, group)
		wantContains(t, e.api.last(-100), "Gvb&lt;Test&gt; 的最新网址是：https://gvb.example")

		// 同一更新重复投递只处理一次
		e.api.reset()
		up := message(tg, "zh", "/getlatesturl")
		e.HandleUpdate(ctx, up)
		e.HandleUpdate(ctx, up)
		if n := len(e.api.messages(tg)); n != 1 {
			t.Fatalf("重复更新应只处理一次，实际回复 %d 次", n)
		}

		// /unbind 后用订阅链接绑定另一个账号
		wantContains(t, e.send(tg, "zh", "/unbind"), "解绑成功")
		if e.telegramID(t, u1.ID) != nil {
			t.Fatal("解绑后 telegram_id 应为空")
		}
		wantContains(t, e.send(tg, "zh", "/bind "+subURL), "绑定成功")
		if id := e.telegramID(t, u2.ID); id == nil || *id != tg {
			t.Fatal("订阅链接绑定失败")
		}
		// 站点账号已绑定其他 Telegram
		wantContains(t, e.send(2002, "zh", "/bind "+subURL), "该账号已经绑定了 Telegram 账号")
	})
}

func TestBindConcurrent(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		var users []int64
		for i := 0; i < 6; i++ {
			u, _ := e.user(t)
			users = append(users, u.ID)
		}
		var wg sync.WaitGroup
		for _, id := range users {
			wg.Add(1)
			go func(id int64) {
				defer wg.Done()
				_ = e.Bind(context.Background(), id, 5005, "zh-CN")
			}(id)
		}
		wg.Wait()
		var n int64
		e.DB.Model(&model.User{}).Where("telegram_id = ?", 5005).Count(&n)
		if n != 1 {
			t.Fatalf("一个 Telegram 账号只能绑定一个站点账号，实际 %d 个", n)
		}
	})
}

func TestTicketNotifications(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		admin, _ := e.user(t)
		staff, _ := e.user(t)
		user, sub := e.user(t)
		e.DB.Model(admin).Update("is_admin", 1)
		e.DB.Model(staff).Update("is_staff", 1)
		e.bindDirect(t, admin, 2001)
		e.bindDirect(t, staff, 3001)
		e.bindDirect(t, user, 4001)
		e.DB.Model(&model.User{}).Where("id = ?", user.ID).Updates(map[string]any{"balance": 1050, "commission_balance": 200})
		exp := int64(1_900_000_000)
		e.DB.Model(sub).Updates(map[string]any{"transfer_enable": int64(10) << 30, "u": int64(1) << 30, "d": int64(512) << 20, "expired_at": exp})

		tk := model.Ticket{UserID: user.ID, Subject: "<b>求助</b>", Level: 1, ReplyStatus: 0}
		if err := e.DB.Create(&tk).Error; err != nil {
			t.Fatal(err)
		}
		e.OnTicket(ctx, ticket.Event{Kind: ticket.EventCreated, Ticket: tk, Message: "连不上 <x>", UserID: user.ID, IP: "1.2.3.4"})
		full := e.api.last(2001)
		wantContains(t, full, fmt.Sprintf("📮工单提醒 #%d", tk.ID), "<code>tg3@x.com</code>", "<code>中国 广东 深圳 电信</code>",
			"10GB/8.5GB", "1GB/512MB", "10.5/2", "<code>&lt;b&gt;求助&lt;/b&gt;</code>", "连不上 &lt;x&gt;", "未找到套餐信息")
		if e.api.last(3001) != full || len(e.api.messages(4001)) != 0 {
			t.Fatal("工单提醒应发给管理员和客服，不发给普通用户")
		}
		e.OnTicket(ctx, ticket.Event{Kind: ticket.EventWithdraw, Ticket: tk, Message: "alipay", UserID: user.ID, IP: "9.9.9.9"})
		if got := e.api.last(2001); strings.Contains(got, "邮箱") || !strings.Contains(got, "alipay") {
			t.Fatalf("提现工单应为简短提醒：%q", got)
		}
		e.OnTicket(ctx, ticket.Event{Kind: ticket.EventReplied, Ticket: tk, Message: "?", UserID: user.ID, IP: "9.9.9.9"})
		wantContains(t, e.api.last(2001), "<code>未知位置</code>")

		// 管理员在 Telegram 中回复工单提醒
		e.api.reset()
		reply := message(2001, "zh", "已处理 <ok>")
		reply.Message.ReplyToMessage = &Message{Text: full}
		e.HandleUpdate(ctx, reply)
		wantContains(t, e.api.messages(2001)[0], fmt.Sprintf("#<code>%d</code> 的工单已回复成功", tk.ID))
		wantContains(t, e.api.last(3001), "已由 tg1@x.com 进行回复")
		wantContains(t, e.api.last(4001), fmt.Sprintf("💬 您的工单 #%d 有新回复", tk.ID), "已处理 &lt;ok&gt;")
		var msgs []model.TicketMessage
		e.DB.Where("ticket_id = ?", tk.ID).Find(&msgs)
		if len(msgs) != 1 || msgs[0].UserID != admin.ID || msgs[0].Message != "已处理 <ok>" {
			t.Fatalf("工单回复未写入：%+v", msgs)
		}

		// 普通用户回复提醒消息不能回复工单
		reply = message(4001, "zh", "谢谢")
		reply.Message.ReplyToMessage = &Message{Text: e.api.last(4001)}
		e.HandleUpdate(ctx, reply)
		wantContains(t, e.api.last(4001), "请前往网站回复工单")

		// 关闭工单通知后不再发送
		if err := e.UpdatePrefs(ctx, user.ID, PrefsInput{NotifyTicket: new(bool)}); err != nil {
			t.Fatal(err)
		}
		e.api.reset()
		if err := e.tickets.ReplyByAdmin(ctx, admin.ID, tk.ID, "again"); err != nil {
			t.Fatal(err)
		}
		if n := len(e.api.messages(4001)); n != 0 {
			t.Fatalf("已关闭工单通知，实际发送 %d 条", n)
		}

		// 收款通知
		e.OnPaid(ctx, &model.Order{UserID: user.ID, TradeNo: "T<1>", TotalAmount: 1050})
		wantContains(t, e.api.last(2001), "💰成功收款 10.5 元", "<code>T&lt;1&gt;</code>")
		wantContains(t, e.api.last(4001), "✅ 订单支付成功")
		if len(e.api.messages(3001)) != 0 {
			t.Fatal("收款通知只发给管理员")
		}

		// 关闭机器人后不再发送
		e.set(t, map[string]any{"telegram_bot_enable": 0})
		e.api.reset()
		e.OnPaid(ctx, &model.Order{UserID: user.ID, TradeNo: "T2", TotalAmount: 100})
		if len(e.api.called("sendMessage")) != 0 {
			t.Fatal("机器人关闭后不应发送")
		}
	})
}

func TestJoinRequest(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ok, okSub := e.user(t)
		expired, sub := e.user(t)
		e.bindDirect(t, ok, 6001)
		e.bindDirect(t, expired, 6002)
		e.DB.Model(okSub).Updates(map[string]any{"transfer_enable": int64(1) << 30, "expired_at": nil})
		e.DB.Model(sub).Updates(map[string]any{"transfer_enable": int64(1) << 30, "expired_at": 1})
		for _, from := range []int64{6001, 6002, 6003} {
			e.HandleUpdate(context.Background(), Update{UpdateID: updateSeq.Add(1),
				ChatJoinRequest: &ChatJoinRequest{Chat: Chat{ID: -200}, From: User{ID: from}}})
		}
		if a, d := e.api.called("approveChatJoinRequest"), e.api.called("declineChatJoinRequest"); len(a) != 1 || len(d) != 2 ||
			a[0].Params["user_id"].(float64) != 6001 {
			t.Fatalf("入群申请处理错误：通过 %v，拒绝 %v", a, d)
		}
	})
}

func TestHTTP(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		const secure = "tgadmin0123456789"
		e.set(t, map[string]any{"secure_path": secure})
		admin, _ := e.user(t)
		normal, _ := e.user(t)
		e.DB.Model(admin).Update("is_admin", 1)
		adminTok, _ := e.auth.Issue(ctx, admin.ID, "", "")
		userTok, _ := e.auth.Issue(ctx, normal.ID, "", "")

		cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"},
			Compat: config.CompatConfig{LegacyCallback: true}}
		r, err := server.NewRouter(cfg, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		r.Mount(&Module{Svc: e.Service, Auth: e.auth})
		do := func(method, path, body string, hdr map[string]string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			for k, v := range hdr {
				req.Header.Set(k, v)
			}
			w := httptest.NewRecorder()
			r.Engine.ServeHTTP(w, req)
			return w
		}
		decode := func(w *httptest.ResponseRecorder, v any) {
			t.Helper()
			var body struct {
				Code int             `json:"code"`
				Data json.RawMessage `json:"data"`
			}
			if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil || body.Code != 0 {
				t.Fatalf("响应错误 %d：%s", w.Code, w.Body.String())
			}
			if v != nil {
				_ = json.Unmarshal(body.Data, v)
			}
		}
		user := map[string]string{"Authorization": userTok}

		// 用户端
		var st Status
		decode(do("GET", "/api/t1/user/telegram", "", user), &st)
		if !st.Enabled || st.Username != "gvb_test_bot" || st.Bound || !st.Prefs.NotifyRemind {
			t.Fatalf("状态错误：%+v", st)
		}
		var link BindLink
		decode(do("POST", "/api/t1/user/telegram/bind", "", user), &link)
		if !strings.Contains(link.URL, "start=bind_") {
			t.Fatalf("绑定链接错误：%+v", link)
		}
		decode(do("PUT", "/api/t1/user/telegram/prefs", `{"notify_remind":false}`, user), nil)
		decode(do("GET", "/api/t1/user/telegram", "", user), &st)
		if st.Prefs.NotifyRemind || !st.Prefs.NotifyOrder {
			t.Fatalf("偏好未保存：%+v", st.Prefs)
		}
		if w := do("GET", "/api/t1/user/telegram", "", nil); w.Code != http.StatusUnauthorized && w.Code != http.StatusForbidden {
			t.Fatalf("未登录应拒绝，实际 %d", w.Code)
		}

		// 管理端：Webhook 模式自动生成密钥并设置
		adm := map[string]string{"Authorization": adminTok}
		var res ApplyResult
		decode(do("POST", "/api/t1/"+secure+"/telegram/apply", "", adm), &res)
		secret := e.Settings.String("telegram_webhook_secret")
		if len(secret) != 32 || res.Mode != "webhook" || res.WebhookURL != "http://example.com/api/t1/telegram/webhook/"+secret {
			t.Fatalf("应用结果错误：%+v", res)
		}
		hooks := e.api.called("setWebhook")
		if len(hooks) != 1 || hooks[0].Params["secret_token"] != secret || hooks[0].Params["url"] != res.WebhookURL {
			t.Fatalf("setWebhook 参数错误：%+v", hooks)
		}
		if n := len(e.api.called("setMyCommands")); n != 3 {
			t.Fatalf("应设置默认、中文、英文命令菜单，实际 %d 次", n)
		}
		if w := do("POST", "/api/t1/"+secure+"/telegram/apply", "", user); w.Code != http.StatusNotFound {
			t.Fatalf("普通用户访问管理端应 404，实际 %d", w.Code)
		}
		var info WebhookInfo
		decode(do("GET", "/api/t1/"+secure+"/telegram/info", "", adm), &info)
		if info.PendingUpdateCount != 3 {
			t.Fatalf("Webhook 信息错误：%+v", info)
		}

		// Webhook：路径与请求头密钥都正确才处理，其余统一 404
		upd, _ := json.Marshal(message(7001, "zh", "/getlatesturl"))
		hookPath := "/api/t1/telegram/webhook/"
		for _, c := range []struct {
			path, header string
			want         int
		}{
			{hookPath + secret, "", http.StatusNotFound},
			{hookPath + "wrong", secret, http.StatusNotFound},
			{hookPath + secret, "wrong", http.StatusNotFound},
		} {
			if w := do("POST", c.path, string(upd), map[string]string{secretHeader: c.header}); w.Code != c.want {
				t.Fatalf("%s 应返回 %d，实际 %d", c.path, c.want, w.Code)
			}
		}
		if len(e.api.messages(7001)) != 0 {
			t.Fatal("密钥错误的请求不应被处理")
		}
		if w := do("POST", hookPath+secret, string(upd), map[string]string{secretHeader: secret}); w.Code != http.StatusOK {
			t.Fatalf("Webhook 应返回 200，实际 %d", w.Code)
		}
		wantContains(t, e.api.last(7001), "最新网址")
		if w := do("POST", hookPath+secret, "not json", map[string]string{secretHeader: secret}); w.Code != http.StatusOK {
			t.Fatalf("无效内容也应返回 200，实际 %d", w.Code)
		}

		// 原路径：access_token = md5(token)
		sum := md5.Sum([]byte(testToken))
		upd, _ = json.Marshal(message(7002, "zh", "/getlatesturl"))
		if w := do("POST", "/api/v1/guest/telegram/webhook?access_token=bad", string(upd), nil); w.Code != http.StatusNotFound {
			t.Fatalf("原路径 token 错误应 404，实际 %d", w.Code)
		}
		if w := do("POST", "/api/v1/guest/telegram/webhook?access_token="+hex.EncodeToString(sum[:]), string(upd), nil); w.Code != http.StatusOK {
			t.Fatalf("原路径应返回 200，实际 %d", w.Code)
		}
		wantContains(t, e.api.last(7002), "最新网址")

		// 长轮询模式：应用时删除 Webhook
		e.set(t, map[string]any{"telegram_mode": "polling"})
		var poll ApplyResult
		decode(do("POST", "/api/t1/"+secure+"/telegram/apply", "", adm), &poll)
		if poll.Mode != "polling" || poll.WebhookURL != "" || len(e.api.called("deleteWebhook")) != 1 {
			t.Fatalf("长轮询模式应用错误：%+v", poll)
		}

		// token 错误时返回 Telegram 的错误信息
		e.set(t, map[string]any{"telegram_bot_token": "999:bad"})
		if w := do("POST", "/api/t1/"+secure+"/telegram/apply", "", adm); w.Code != http.StatusBadRequest || strings.Contains(w.Body.String(), "999:bad") {
			t.Fatalf("token 错误应返回 400 且不含 token：%d %s", w.Code, w.Body.String())
		}
	})
}

func TestPoller(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		e.set(t, map[string]any{"telegram_mode": "polling"})
		first := message(8001, "zh", "/getlatesturl")
		first.UpdateID = 500
		e.api.mu.Lock()
		e.api.updates = [][]Update{{first}}
		e.api.mu.Unlock()

		ctx, cancel := context.WithCancel(context.Background())
		done := make(chan struct{})
		go func() {
			defer close(done)
			e.RunPoller(ctx)
		}()
		deadline := time.Now().Add(10 * time.Second)
		for len(e.api.messages(8001)) == 0 || len(e.api.called("getUpdates")) < 2 {
			if time.Now().After(deadline) {
				cancel()
				t.Fatal("长轮询未处理更新")
			}
			time.Sleep(20 * time.Millisecond)
		}
		cancel()
		<-done
		if len(e.api.called("deleteWebhook")) != 1 {
			t.Fatal("开始长轮询前应删除 Webhook")
		}
		if off := e.api.called("getUpdates")[1].Params["offset"].(float64); off != 501 {
			t.Fatalf("下一次轮询 offset 应为 501，实际 %v", off)
		}
		v, _ := e.Cache.Get(context.Background(), e.Cache.KeyOf(cache.KeyTelegramOffset, tokenDigest(testToken))).Result()
		if v != "501" {
			t.Fatalf("offset 应保存到 Redis，实际 %q", v)
		}
		if _, err := e.Cache.TryLock(context.Background(), pollLockName, time.Second); err != nil {
			t.Fatalf("停止后应释放轮询锁：%v", err)
		}
	})
}
