package ticket

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/cache"
	"gvboard/internal/config"
	"gvboard/internal/hook"
	"gvboard/internal/httpx"
	"gvboard/internal/mail"
	"gvboard/internal/model"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/subscription"
	"gvboard/internal/testutil"
)

const testNow = 1_800_000_000

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

type env struct {
	*Service
	auth   *auth.Service
	mail   *fakeMail
	mu     sync.Mutex
	events []Event
	seq    int
}

func newEnv(t *testing.T, driver string) *env {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := &env{mail: &fakeMail{}, auth: auth.New(db, c, "base64:dGlja2V0LXRlc3Qta2V5LTAxMjM0NTY3ODlhYmNkZWZn")}
	e.Service = &Service{
		DB: db, Cache: c, Settings: st, Mail: e.mail,
		Subscriptions: &subscription.Service{DB: db, Cache: c, Settings: st},
		Translate: func(locale, key string, params map[string]any) string {
			return fmt.Sprintf("%s:%s", locale, key)
		},
		OnEvent: func(_ context.Context, ev Event) {
			e.mu.Lock()
			defer e.mu.Unlock()
			e.events = append(e.events, ev)
		},
		now: func() int64 { return testNow },
	}
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
		u, sub, err = account.Create(tx, account.NewUser{Email: fmt.Sprintf("User%d@X.com", e.seq), Password: "password123"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return u, sub
}

func (e *env) ticket(t *testing.T, id int64) model.Ticket {
	t.Helper()
	var tk model.Ticket
	if err := e.DB.Take(&tk, id).Error; err != nil {
		t.Fatal(err)
	}
	return tk
}

func wantErr(t *testing.T, err error, want *httpx.Error) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Msg != want.Msg {
		t.Fatalf("期望错误 %q，实际 %v", want.Msg, err)
	}
}

func ptr[T any](v T) *T { return &v }

func input(subject string) CreateInput {
	return CreateInput{Subject: subject, Level: ptr(1), Message: "你好"}
}

func TestTicket(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			t.Run("新建", func(t *testing.T) { testCreate(t, newEnv(t, driver)) })
			t.Run("并发新建", func(t *testing.T) { testConcurrentCreate(t, newEnv(t, driver)) })
			t.Run("回复与关闭", func(t *testing.T) { testReply(t, newEnv(t, driver)) })
			t.Run("管理端", func(t *testing.T) { testAdmin(t, newEnv(t, driver)) })
			t.Run("提现", func(t *testing.T) { testWithdraw(t, newEnv(t, driver)) })
			t.Run("自动关闭", func(t *testing.T) { testAutoClose(t, newEnv(t, driver)) })
			t.Run("HTTP", func(t *testing.T) { testHTTP(t, newEnv(t, driver)) })
			t.Run("插件事件", func(t *testing.T) { testHooks(t, newEnv(t, driver)) })
		})
	}
}

type recHooks struct {
	mu     sync.Mutex
	names  []string
	events []hook.Ticket
}

func (h *recHooks) Emit(_ context.Context, name string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.names = append(h.names, name)
	h.events = append(h.events, data.(hook.Ticket))
}

func (h *recHooks) Filter(_ context.Context, _ string, v any, _ func(any) any, _ func(hook.Source, any, any) (any, error)) any {
	return v
}

func testHooks(t *testing.T, e *env) {
	ctx := context.Background()
	h := &recHooks{}
	e.Hooks = h
	u, _ := e.user(t)
	admin, _ := e.user(t)
	tk, err := e.Create(ctx, u.ID, input("插件"), "")
	if err != nil {
		t.Fatal(err)
	}
	if err := e.ReplyByAdmin(ctx, admin.ID, tk.ID, "已处理"); err != nil {
		t.Fatal(err)
	}
	if err := e.Reply(ctx, u.ID, tk.ID, "谢谢", ""); err != nil {
		t.Fatal(err)
	}
	want := []string{hook.EventTicketCreated, hook.EventTicketReplied, hook.EventTicketReplied}
	if strings.Join(h.names, ",") != strings.Join(want, ",") {
		t.Fatalf("事件名错误：%v", h.names)
	}
	c, a, r := h.events[0], h.events[1], h.events[2]
	if c.TicketID != tk.ID || c.UserID != u.ID || c.Subject != "插件" || c.Kind != string(EventCreated) || c.Message != "你好" || c.ByAdmin {
		t.Fatalf("新建事件错误：%+v", c)
	}
	if !a.ByAdmin || a.Message != "已处理" || a.UserID != u.ID {
		t.Fatalf("管理员回复事件错误：%+v", a)
	}
	if r.ByAdmin || r.Message != "谢谢" || r.Kind != string(EventReplied) {
		t.Fatalf("用户回复事件错误：%+v", r)
	}
}

func testCreate(t *testing.T, e *env) {
	ctx := context.Background()
	u, sub := e.user(t)
	other, otherSub := e.user(t)

	for _, c := range []struct {
		in   CreateInput
		want *httpx.Error
	}{
		{CreateInput{Level: ptr(0), Message: "m"}, errEmptySubject},
		{CreateInput{Subject: strings.Repeat("长", 256), Level: ptr(0), Message: "m"}, errLongSubject},
		{CreateInput{Subject: "s", Message: "m"}, httpx.Validation("Ticket level cannot be empty")},
		{CreateInput{Subject: "s", Level: ptr(3), Message: "m"}, errLevel},
		{CreateInput{Subject: "s", Level: ptr(0), Message: "  "}, errEmptyMessage},
		{CreateInput{Subject: "s", Level: ptr(0), Message: strings.Repeat("a", maxMessage+1)}, errLongMessage},
		{CreateInput{Subject: "s", Level: ptr(0), Message: "m", SubscriptionID: ptr(int64(0))}, errInvalid},
	} {
		_, err := e.Create(ctx, u.ID, c.in, "")
		wantErr(t, err, c.want)
	}

	e.set(t, map[string]any{"ticket_status": 2})
	_, err := e.Create(ctx, u.ID, input("s"), "")
	wantErr(t, err, errForbidden)
	e.set(t, map[string]any{"ticket_status": 3})
	_, err = e.Create(ctx, u.ID, input("s"), "")
	wantErr(t, err, errUnknownStatus)
	e.set(t, map[string]any{"ticket_status": 1})
	_, err = e.Create(ctx, u.ID, input("s"), "")
	wantErr(t, err, errNeedOrder)
	e.DB.Create(&model.Order{UserID: u.ID, PlanID: 1, Type: model.OrderTypeNew, Period: "month_price",
		TradeNo: "T1", TotalAmount: 100, Status: model.OrderStatusCancelled})
	_, err = e.Create(ctx, u.ID, input("s"), "")
	wantErr(t, err, errNeedOrder)
	e.DB.Create(&model.Order{UserID: u.ID, PlanID: 1, Type: model.OrderTypeNew, Period: "month_price",
		TradeNo: "T2", TotalAmount: 100, Status: model.OrderStatusDiscounted})

	in := input(" 😀 无法连接 ")
	in.SubscriptionID = &otherSub.ID
	_, err = e.Create(ctx, u.ID, in, "")
	wantErr(t, err, subscription.ErrNotFound)

	in.SubscriptionID = &sub.ID
	tk, err := e.Create(ctx, u.ID, in, "1.2.3.4")
	if err != nil {
		t.Fatal(err)
	}
	stored := e.ticket(t, tk.ID)
	if stored.Subject != "😀 无法连接" || stored.Level != 1 || stored.Status != 0 || stored.ReplyStatus != 0 ||
		stored.SubscriptionID == nil || *stored.SubscriptionID != sub.ID {
		t.Fatalf("工单内容有误: %+v", stored)
	}
	if len(e.events) != 1 || e.events[0].Kind != EventCreated || e.events[0].IP != "1.2.3.4" || e.events[0].Message != "你好" {
		t.Fatalf("事件: %+v", e.events)
	}

	_, err = e.Create(ctx, u.ID, input("第二个"), "")
	wantErr(t, err, errHasOpen)
	e.set(t, map[string]any{"ticket_status": 0})
	if _, err := e.Create(ctx, other.ID, input("他人"), ""); err != nil {
		t.Fatalf("其他用户不受影响: %v", err)
	}
	if err := e.Close(ctx, u.ID, tk.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Create(ctx, u.ID, input("第二个"), ""); err != nil {
		t.Fatalf("关闭后可以再开: %v", err)
	}

	list, _ := e.List(ctx, u.ID)
	if len(list) != 2 || list[0].Subject != "第二个" {
		t.Fatalf("列表应按创建时间倒序: %+v", list)
	}
}

func testConcurrentCreate(t *testing.T, e *env) {
	ctx := context.Background()
	u, _ := e.user(t)
	var wg sync.WaitGroup
	var mu sync.Mutex
	ok, conflict := 0, 0
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			_, err := e.Create(ctx, u.ID, input(fmt.Sprintf("并发%d", i)), "")
			mu.Lock()
			defer mu.Unlock()
			switch {
			case err == nil:
				ok++
			case errors.Is(err, errHasOpen):
				conflict++
			default:
				t.Errorf("意外错误: %v", err)
			}
		}(i)
	}
	wg.Wait()
	if ok != 1 || conflict != 7 {
		t.Fatalf("并发新建应只成功一个: 成功 %d，冲突 %d", ok, conflict)
	}
}

func testReply(t *testing.T, e *env) {
	ctx := context.Background()
	u, _ := e.user(t)
	other, _ := e.user(t)
	admin, _ := e.user(t)
	tk, _ := e.Create(ctx, u.ID, input("s"), "")

	wantErr(t, e.Reply(ctx, u.ID, tk.ID, "再问", ""), errWaitReply)
	wantErr(t, e.Reply(ctx, other.ID, tk.ID, "m", ""), errNotFound)
	wantErr(t, e.Reply(ctx, u.ID, tk.ID, " ", ""), errEmptyMessage)

	if err := e.ReplyByAdmin(ctx, admin.ID, tk.ID, "已处理"); err != nil {
		t.Fatal(err)
	}
	if got := e.ticket(t, tk.ID); got.ReplyStatus != model.TicketReplied || got.UpdatedAt != testNow {
		t.Fatalf("管理员回复后应为已回复: %+v", got)
	}
	if err := e.Reply(ctx, u.ID, tk.ID, "谢谢", "5.6.7.8"); err != nil {
		t.Fatal(err)
	}
	if got := e.ticket(t, tk.ID); got.ReplyStatus != model.TicketReplyPending {
		t.Fatalf("用户回复后应为待回复: %+v", got)
	}
	if last := e.events[len(e.events)-1]; last.Kind != EventReplied || last.Message != "谢谢" || last.IP != "5.6.7.8" {
		t.Fatalf("回复事件: %+v", last)
	}

	d, err := e.Get(ctx, u.ID, tk.ID)
	if err != nil || len(d.Messages) != 3 || !d.Messages[0].IsMe || d.Messages[1].IsMe || !d.Messages[2].IsMe {
		t.Fatalf("用户视角 is_me 有误: %+v %v", d, err)
	}
	if _, err := e.Get(ctx, other.ID, tk.ID); err != errNotFound {
		t.Fatalf("他人工单: %v", err)
	}

	wantErr(t, e.Close(ctx, other.ID, tk.ID), errNotFound)
	if err := e.Close(ctx, u.ID, tk.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.Reply(ctx, u.ID, tk.ID, "m", ""), errClosed)
}

func testAdmin(t *testing.T, e *env) {
	ctx := context.Background()
	e.set(t, map[string]any{"app_name": "测试站", "app_url": "https://site.example.com"})
	u1, sub1 := e.user(t)
	u2, _ := e.user(t)
	admin, _ := e.user(t)

	in := input("问题一")
	in.SubscriptionID = &sub1.ID
	t1, _ := e.Create(ctx, u1.ID, in, "")
	t2, _ := e.Create(ctx, u2.ID, input("问题二"), "")
	e.DB.Model(t1).UpdateColumn("updated_at", testNow-100)
	e.DB.Model(t2).UpdateColumn("updated_at", testNow-50)
	e.Close(ctx, 0, t2.ID)
	e.DB.Model(t2).UpdateColumn("updated_at", testNow-50)

	// 管理员回复已关闭的工单会重新打开
	if err := e.ReplyByAdmin(ctx, admin.ID, t2.ID, "<b>回复</b>"); err != nil {
		t.Fatal(err)
	}
	got := e.ticket(t, t2.ID)
	if got.Status != model.TicketStatusOpen || got.ReplyStatus != model.TicketReplied || got.UpdatedAt != testNow {
		t.Fatalf("管理员回复后状态: %+v", got)
	}
	if len(e.mail.sent) != 1 {
		t.Fatalf("应发送 1 封通知: %d", len(e.mail.sent))
	}
	m := e.mail.sent[0]
	if m.Email != u2.Email || m.Subject != "您在测试站的工单得到了回复" || m.TemplateName != mail.TplNotify ||
		m.TemplateValue["content"] != "主题：问题二\r\n回复内容：<b>回复</b>" || m.TemplateValue["url"] != "https://site.example.com" || m.TrustedHTML {
		t.Fatalf("通知邮件: %+v", m)
	}
	// 30 分钟内同一用户不重复通知，其他用户不受影响
	e.ReplyByAdmin(ctx, admin.ID, t2.ID, "补充")
	e.ReplyByAdmin(ctx, admin.ID, t1.ID, "回复一")
	if len(e.mail.sent) != 2 || e.mail.sent[1].Email != u1.Email {
		t.Fatalf("通知节流: %+v", e.mail.sent)
	}
	ttl := e.Cache.TTL(ctx, e.Cache.KeyOf(cache.KeyTicketNotify, u2.ID)).Val()
	if ttl <= 0 || ttl > notifyTTL {
		t.Fatalf("节流键 TTL: %v", ttl)
	}
	wantErr(t, e.ReplyByAdmin(ctx, admin.ID, 999, "m"), errNotFound)
	wantErr(t, e.ReplyByAdmin(ctx, admin.ID, t1.ID, ""), errEmptyMessage)

	// 管理员给自己的工单回复视为用户回复
	own, _ := e.Create(ctx, admin.ID, input("自己的"), "")
	e.ReplyByAdmin(ctx, admin.ID, own.ID, "自答")
	if got := e.ticket(t, own.ID); got.ReplyStatus != model.TicketReplyPending {
		t.Fatalf("回复自己的工单应为待回复: %+v", got)
	}
	e.DB.Model(own).UpdateColumn("updated_at", testNow-1000)

	d, err := e.AdminGet(ctx, t1.ID)
	if err != nil || d.Email != u1.Email || d.SubscriptionName == nil || *d.SubscriptionName != sub1.Name ||
		len(d.Messages) != 2 || d.Messages[0].IsMe || !d.Messages[1].IsMe {
		t.Fatalf("管理端详情: %+v %v", d, err)
	}
	if d.SubscriptionInferred || d.PlanName != nil || d.SubscriptionUsed == nil || d.SubscriptionTotal == nil {
		t.Fatalf("无套餐的关联订阅: %+v", d.AdminTicket)
	}
	plan := &model.Plan{Name: "测试套餐", GroupID: 1, TransferEnable: 100}
	if err := e.DB.Create(plan).Error; err != nil {
		t.Fatal(err)
	}
	e.DB.Model(sub1).UpdateColumn("plan_id", plan.ID)
	if d, _ = e.AdminGet(ctx, t1.ID); d.PlanName == nil || *d.PlanName != plan.Name {
		t.Fatalf("关联订阅的套餐名: %+v", d.AdminTicket)
	}
	// 未关联订阅的工单取用户主订阅
	if d, _ = e.AdminGet(ctx, t2.ID); !d.SubscriptionInferred || d.InferredSubscription == nil || d.SubscriptionName == nil {
		t.Fatalf("未关联订阅应取主订阅: %+v", d.AdminTicket)
	}
	if _, err := e.AdminGet(ctx, 999); err != errNotFound {
		t.Fatalf("不存在的工单: %v", err)
	}

	ids := func(q AdminQuery) []int64 {
		t.Helper()
		p, err := e.AdminList(ctx, q)
		if err != nil {
			t.Fatal(err)
		}
		var out []int64
		for _, x := range p.List.([]AdminTicket) {
			out = append(out, x.ID)
		}
		return out
	}
	eq := func(got []int64, want ...int64) {
		t.Helper()
		if fmt.Sprint(got) != fmt.Sprint(want) {
			t.Fatalf("列表 %v，期望 %v", got, want)
		}
	}
	// 按更新时间倒序：t2、t1 的 updated_at 都是 testNow，同秒按 ID 倒序
	eq(ids(AdminQuery{}), t2.ID, t1.ID, own.ID)
	eq(ids(AdminQuery{ReplyStatus: []int{0}}), own.ID)
	eq(ids(AdminQuery{ReplyStatus: []int{0, 1}, Status: ptr(0)}), t2.ID, t1.ID, own.ID)
	eq(ids(AdminQuery{Status: ptr(1)}))
	eq(ids(AdminQuery{Email: strings.ToLower(u1.Email)}), t1.ID)
	eq(ids(AdminQuery{Email: "nobody@x.com"}))
	p, _ := e.AdminList(ctx, AdminQuery{PageSize: 1})
	if p.Total != 3 || len(p.List.([]AdminTicket)) != 3 {
		t.Fatalf("page_size 小于 10 时按 10 处理: %+v", p)
	}
	if p, _ := e.AdminList(ctx, AdminQuery{Current: 2}); p.Total != 3 || len(p.List.([]AdminTicket)) != 0 {
		t.Fatalf("超出范围的页应为空: %+v", p)
	}
}

func testWithdraw(t *testing.T, e *env) {
	ctx := context.Background()
	u, _ := e.user(t)
	e.set(t, map[string]any{"commission_withdraw_method": []string{"支付宝", "USDT"}, "commission_withdraw_limit": 50.5})
	ok := WithdrawInput{Method: "USDT", Account: " addr "}

	for _, c := range []struct {
		in   WithdrawInput
		want *httpx.Error
	}{
		{WithdrawInput{Account: "a"}, errEmptyMethod},
		{WithdrawInput{Method: "USDT"}, errEmptyAccount},
		{WithdrawInput{Method: "USDT", Account: strings.Repeat("a", 256)}, errLongAccount},
		{WithdrawInput{Method: "PayPal", Account: "a"}, errWithdrawMethod},
	} {
		_, err := e.Withdraw(ctx, u.ID, c.in, "zh-CN", "")
		wantErr(t, err, c.want)
	}
	e.DB.Model(u).Update("commission_balance", 5049)
	_, err := e.Withdraw(ctx, u.ID, ok, "zh-CN", "")
	var he *httpx.Error
	if !errors.As(err, &he) || he.Msg != "The current required minimum withdrawal commission is :limit" || he.Params["limit"] != "50.5" {
		t.Fatalf("最低提现金额: %v", err)
	}
	e.DB.Model(u).Update("commission_balance", 5050)
	e.set(t, map[string]any{"withdraw_close_enable": 1})
	_, err = e.Withdraw(ctx, u.ID, ok, "zh-CN", "")
	wantErr(t, err, errNoWithdraw)
	e.set(t, map[string]any{"withdraw_close_enable": 0})

	// 与原项目一致，提现工单不受“仅一个未关闭工单”与 ticket_status 限制
	e.set(t, map[string]any{"ticket_status": 2})
	e.Create(ctx, u.ID, input("s"), "")
	for i := 0; i < 2; i++ {
		tk, err := e.Withdraw(ctx, u.ID, ok, "en-US", "9.9.9.9")
		if err != nil {
			t.Fatal(err)
		}
		if tk.Subject != "en-US:"+withdrawSubject || tk.Level != 2 {
			t.Fatalf("提现工单: %+v", tk)
		}
	}
	var msg model.TicketMessage
	e.DB.Order("id DESC").Take(&msg)
	if msg.Message != "en-US:Withdrawal method：USDT\r\nen-US:Withdrawal account：addr" || msg.UserID != u.ID {
		t.Fatalf("提现内容: %q", msg.Message)
	}
	if last := e.events[len(e.events)-1]; last.Kind != EventWithdraw || last.UserID != u.ID {
		t.Fatalf("提现事件: %+v", last)
	}
}

func testAutoClose(t *testing.T, e *env) {
	ctx := context.Background()
	u1, _ := e.user(t)
	u2, _ := e.user(t)
	u3, _ := e.user(t)
	replied, _ := e.Create(ctx, u1.ID, input("已回复超时"), "")
	recent, _ := e.Create(ctx, u2.ID, input("已回复未超时"), "")
	pending, _ := e.Create(ctx, u3.ID, input("待回复"), "")
	e.DB.Model(&model.Ticket{}).Where("id IN ?", []int64{replied.ID, recent.ID}).UpdateColumn("reply_status", 1)
	e.DB.Model(replied).UpdateColumn("updated_at", testNow-autoCloseIdle)
	e.DB.Model(recent).UpdateColumn("updated_at", testNow-autoCloseIdle+1)
	e.DB.Model(pending).UpdateColumn("updated_at", testNow-autoCloseIdle*10)

	if err := e.AutoClose(ctx); err != nil {
		t.Fatal(err)
	}
	if got := e.ticket(t, replied.ID); got.Status != 1 || got.UpdatedAt != testNow {
		t.Fatalf("超时的已回复工单应关闭: %+v", got)
	}
	if e.ticket(t, recent.ID).Status != 0 || e.ticket(t, pending.ID).Status != 0 {
		t.Fatal("未超时或待回复的工单不应关闭")
	}
}

func testHTTP(t *testing.T, e *env) {
	ctx := context.Background()
	const secure = "ticketadminpath123"
	e.set(t, map[string]any{"secure_path": secure})
	admin, _ := e.user(t)
	normal, _ := e.user(t)
	e.DB.Model(admin).Update("is_admin", 1)
	adminTok, _ := e.auth.Issue(ctx, admin.ID, "", "")
	userTok, _ := e.auth.Issue(ctx, normal.ID, "", "")

	cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
	r, err := server.NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	r.Mount(&Module{Svc: e.Service, Auth: e.auth, Locale: func(*gin.Context) string { return "zh-CN" }})
	do := func(method, path, body, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if tok != "" {
			req.Header.Set("Authorization", tok)
		}
		w := httptest.NewRecorder()
		r.Engine.ServeHTTP(w, req)
		return w
	}
	notFound := do("GET", "/api/t1/definitely-not-exist", "", "")
	p := "/api/t1/" + secure
	for _, c := range []struct{ method, path, tok string }{
		{"GET", p + "/tickets", userTok}, {"POST", p + "/tickets/1/reply", ""},
	} {
		if w := do(c.method, c.path, "{}", c.tok); w.Code != http.StatusNotFound || w.Body.String() != notFound.Body.String() {
			t.Errorf("%s %s 应返回统一 404，实际 %d", c.method, c.path, w.Code)
		}
	}

	if w := do("POST", "/api/t1/user/tickets", `{"subject":"s","level":"1","message":"m"}`, userTok); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("level 类型错误应 422: %d", w.Code)
	}
	if w := do("POST", "/api/t1/user/tickets", `{"subject":"s","level":0,"message":"m"}`, userTok); w.Code != 200 {
		t.Fatalf("新建工单: %d %s", w.Code, w.Body)
	}
	var tk model.Ticket
	e.DB.Last(&tk)
	if w := do("GET", "/api/t1/user/tickets", "", userTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"subject":"s"`) {
		t.Fatalf("工单列表: %d %s", w.Code, w.Body)
	}
	if w := do("POST", fmt.Sprintf("%s/tickets/%d/reply", p, tk.ID), `{"message":"已处理"}`, adminTok); w.Code != 200 {
		t.Fatalf("管理员回复: %d %s", w.Code, w.Body)
	}
	if w := do("GET", fmt.Sprintf("%s/tickets?reply_status=1&reply_status=0", p), "", adminTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"total":1`) {
		t.Fatalf("管理端列表: %d %s", w.Code, w.Body)
	}
	if w := do("GET", fmt.Sprintf("/api/t1/user/tickets/%d", tk.ID), "", userTok); w.Code != 200 || strings.Count(w.Body.String(), `"is_me"`) != 2 {
		t.Fatalf("工单详情: %d %s", w.Code, w.Body)
	}
	if w := do("POST", fmt.Sprintf("/api/t1/user/tickets/%d/reply", tk.ID), `{"message":"好"}`, userTok); w.Code != 200 {
		t.Fatalf("用户回复: %d %s", w.Code, w.Body)
	}
	if w := do("POST", fmt.Sprintf("/api/t1/user/tickets/%d/reply", tk.ID), `{"message":"好"}`, userTok); w.Code != http.StatusBadRequest {
		t.Fatalf("连续回复应 400: %d", w.Code)
	}
	if w := do("POST", fmt.Sprintf("/api/t1/user/tickets/%d/close", tk.ID), "", userTok); w.Code != 200 {
		t.Fatalf("关闭: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/t1/user/tickets/999", "", userTok); w.Code != http.StatusNotFound {
		t.Fatalf("不存在的工单应 404: %d", w.Code)
	}
	if w := do("GET", fmt.Sprintf("%s/tickets/%d", p, tk.ID), "", adminTok); w.Code != 200 || !strings.Contains(w.Body.String(), normal.Email) {
		t.Fatalf("管理端详情: %d %s", w.Code, w.Body)
	}
	if w := do("POST", fmt.Sprintf("%s/tickets/%d/close", p, tk.ID), "", adminTok); w.Code != 200 {
		t.Fatalf("管理端关闭: %d", w.Code)
	}
	e.set(t, map[string]any{"commission_withdraw_limit": 0})
	if w := do("POST", "/api/t1/user/withdrawals", `{"withdraw_method":"不存在","withdraw_account":"a"}`, userTok); w.Code != http.StatusBadRequest {
		t.Fatalf("不支持的提现方式应 400: %d %s", w.Code, w.Body)
	}
}
