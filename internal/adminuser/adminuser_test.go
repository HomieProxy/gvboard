package adminuser

import (
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/balance"
	"gvboard/internal/cache"
	"gvboard/internal/config"
	"gvboard/internal/httpx"
	"gvboard/internal/mail"
	"gvboard/internal/model"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/subscription"
	"gvboard/internal/testutil"
	"gvboard/internal/util"
)

type fakeMail struct {
	mu   sync.Mutex
	sent []mail.Payload
}

func (f *fakeMail) DispatchMass(_ context.Context, p mail.Payload) error {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, p)
	return nil
}

type fakeGuard int64

func (f fakeGuard) ShareIPs(context.Context, int64) (int64, error) { return int64(f), nil }

type env struct {
	*Service
	cache *cache.Cache
	mail  *fakeMail
}

func newEnv(t *testing.T, driver string) *env {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := &env{cache: c, mail: &fakeMail{}}
	e.Service = &Service{
		DB: db, Settings: st, Mail: e.mail,
		Auth:          auth.New(db, c, "base64:YWRtaW51c2VyLXRlc3Qta2V5LTAxMjM0NTY3ODk="),
		Subscriptions: &subscription.Service{DB: db, Cache: c, Settings: st, Linker: &subscription.Linker{Settings: st, Cache: c}},
	}
	return e
}

func eachDriver(t *testing.T, f func(t *testing.T, e *env)) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, d := range drivers {
		t.Run(d, func(t *testing.T) { f(t, newEnv(t, d)) })
	}
}

func (e *env) user(t *testing.T, email string) (*model.User, *model.Subscription) {
	t.Helper()
	var u *model.User
	var sub *model.Subscription
	if err := e.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		u, sub, err = account.Create(tx, account.NewUser{Email: email, Password: "password123"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return u, sub
}

func (e *env) plan(t *testing.T, name string) *model.Plan {
	t.Helper()
	p := &model.Plan{Name: name, GroupID: 7, TransferEnable: 100, Show: 1, Renew: 1, DeviceLimit: ptr(int64(3))}
	if err := e.DB.Create(p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) reload(t *testing.T, v any, id int64) {
	t.Helper()
	switch p := v.(type) {
	case *model.User:
		*p = model.User{}
	case *model.Subscription:
		*p = model.Subscription{}
	}
	if err := e.DB.Take(v, id).Error; err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

func wantErr(t *testing.T, err error, want *httpx.Error) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Msg != want.Msg {
		t.Fatalf("期望错误 %q，实际 %v", want.Msg, err)
	}
}

func ids(rows []ListRow) []int64 {
	out := make([]int64, len(rows))
	for i, r := range rows {
		out[i] = r.ID
	}
	return out
}

func TestListAndFilter(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.plan(t, "金牌")
		a, _ := e.user(t, "Alice@X.com")
		b, bSub := e.user(t, "bob@y.com")
		c, _ := e.user(t, "carol@x.com")
		e.DB.Model(bSub).Updates(map[string]any{"transfer_enable": 10 * gb, "d": 5 * gb, "plan_id": p.ID, "name": "B 主"})
		// carol 的第二个订阅使用套餐，主订阅没有套餐
		var cSub2 *model.Subscription
		e.DB.Transaction(func(tx *gorm.DB) error {
			var err error
			cSub2, err = e.Subscriptions.Create(tx, subscription.NewSubscription{UserID: c.ID, Name: "第二", Plan: p, IgnoreLimit: true})
			return err
		})
		e.DB.Model(c).Updates(map[string]any{"invite_user_id": b.ID, "banned": 1, "remarks": "VIP_100%"})
		if err := e.cache.SetJSON(ctx, e.cache.KeyOf(cache.KeyAliveIP, cSub2.ID), map[string]any{"alive_ip": 2}, time.Minute); err != nil {
			t.Fatal(err)
		}

		list := func(f ...Filter) []int64 {
			t.Helper()
			res, err := e.List(ctx, Query{Filter: f, Sort: "id", SortType: "ASC"}, "https://site")
			if err != nil {
				t.Fatal(err)
			}
			return ids(res.Data)
		}
		eq := func(got []int64, want ...int64) {
			t.Helper()
			if fmt.Sprint(got) != fmt.Sprint(want) {
				t.Fatalf("期望 %v，实际 %v", want, got)
			}
		}
		eq(list(), a.ID, b.ID, c.ID)
		eq(list(Filter{"email", "模糊", "X.COM"}), a.ID, c.ID)
		eq(list(Filter{"email", "=", "alice@x.com"}), a.ID)
		eq(list(Filter{"plan_id", "=", fmt.Sprint(p.ID)}), b.ID, c.ID)
		eq(list(Filter{"plan_id", "=", "null"}), a.ID, c.ID)
		eq(list(Filter{"transfer_enable", ">", "5"}), b.ID, c.ID)
		eq(list(Filter{"d", ">=", "5"}), b.ID)
		eq(list(Filter{"invite_by_email", "=", "BOB@y.com"}), c.ID)
		eq(list(Filter{"subscription_id", "=", fmt.Sprint(cSub2.ID)}), c.ID)
		eq(list(Filter{"banned", "=", "1"}, Filter{"email", "模糊", "x.com"}), c.ID)
		eq(list(Filter{"remarks", "模糊", "_100%"}), c.ID)
		eq(list(Filter{"remarks", "模糊", "P%0"})) // % 按字面匹配
		for _, f := range []Filter{{"id; DROP TABLE v2_user", "=", "1"}, {"id", "LIKE", "1"}, {"id", "模糊", "1"}, {"id", "=", "x"}, {"plan_id", ">", "null"}} {
			if _, err := e.List(ctx, Query{Filter: []Filter{f}}, ""); !errors.Is(err, errInvalid) {
				t.Errorf("非法筛选 %+v 应报错，实际 %v", f, err)
			}
		}

		res, err := e.List(ctx, Query{Sort: "email; drop", SortType: "ASC", Page: Page{PageSize: 1}}, "https://site")
		if err != nil || res.Total != 3 || len(res.Data) != 3 {
			t.Fatalf("分页错误：%+v %v", res, err)
		}
		res, _ = e.List(ctx, Query{Filter: []Filter{{"id", "=", fmt.Sprint(c.ID)}}}, "https://site")
		row := res.Data[0]
		if row.SubscriptionCount != 2 || row.AliveIP != 2 || row.Primary == nil || row.Primary.PlanName != "" ||
			!strings.HasPrefix(row.Primary.SubscribeURL, "https://site") || row.Remarks == nil {
			t.Fatalf("列表行错误：%+v primary=%+v", row, row.Primary)
		}
		res, _ = e.List(ctx, Query{Filter: []Filter{{"id", "=", fmt.Sprint(b.ID)}}}, "")
		if res.Data[0].Primary.PlanName != "金牌" || res.Data[0].Primary.Name != "B 主" {
			t.Fatalf("主订阅摘要错误：%+v", res.Data[0].Primary)
		}
	})
}

func TestDetailUpdateInviter(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		admin, _ := e.user(t, "admin@x.com")
		e.DB.Model(admin).Update("is_admin", 1)
		u, sub := e.user(t, "u@x.com")
		inv, _ := e.user(t, "inv@x.com")
		e.DB.Model(u).Updates(map[string]any{"invite_user_id": inv.ID, "balance": 500, "telegram_id": 42, "commission_rate": 30})
		e.cache.HSet(ctx, e.cache.KeyOf(cache.KeyAliveNodes, sub.ID), "vmess3", `{"aliveips":["1.1.1.1_3","2.2.2.2_3"],"lastupdateAt":100}`)

		e.Guard = fakeGuard(3)
		d, err := e.Get(ctx, u.ID, "https://s")
		if err != nil || d.InviteUser == nil || d.InviteUser.Email != "inv@x.com" || len(d.Subscriptions) != 1 || d.Subscriptions[0].ShareIPs != 3 ||
			len(d.Subscriptions[0].Devices) != 2 || d.Subscriptions[0].Devices[0].IP != "1.1.1.1" || d.Subscriptions[0].Devices[0].Node != "vmess3" {
			t.Fatalf("详情错误：%+v %v", d, err)
		}
		if _, err := e.Get(ctx, 99999, ""); !errors.Is(err, errUserNotExist) {
			t.Fatal("不存在的用户应报错")
		}

		// 修改：没有传的字段不变，null 清空
		err = e.Update(ctx, admin.ID, u.ID, UpdateInput{Email: ptr(" New@x.com "), Password: ptr("newpass123"), IsStaff: ptr(1),
			CommissionRate: httpx.Optional[int64]{Set: true}, TelegramID: httpx.Optional[int64]{Set: true}, Remarks: httpx.Some("备注")})
		if err != nil {
			t.Fatal(err)
		}
		var got model.User
		e.reload(t, &got, u.ID)
		if got.Email != "New@x.com" || got.Balance != 500 || got.IsStaff != 1 || got.CommissionRate != nil || got.TelegramID != nil ||
			got.Remarks == nil || *got.Remarks != "备注" || got.PasswordAlgo != nil || !util.VerifyPassword("", "", "newpass123", got.Password) {
			t.Fatalf("修改结果错误：%+v", got)
		}
		cases := []struct {
			in   UpdateInput
			want *httpx.Error
		}{
			{UpdateInput{Email: ptr("INV@x.com")}, errEmailUsed},
			{UpdateInput{Email: ptr("bad")}, errEmailFormat},
			{UpdateInput{Password: ptr("short")}, errPasswordMin},
			{UpdateInput{CommissionRate: httpx.Some(int64(101))}, errInvalid},
			{UpdateInput{Discount: httpx.Some(int64(-1))}, errInvalid},
			{UpdateInput{Banned: ptr(2)}, errInvalid},
			{UpdateInput{CommissionType: ptr(3)}, errInvalid},
			{UpdateInput{TelegramID: httpx.Some(int64(1))}, errInvalid},
		}
		for _, c := range cases {
			wantErr(t, e.Update(ctx, admin.ID, u.ID, c.in), c.want)
		}
		wantErr(t, e.Update(ctx, admin.ID, admin.ID, UpdateInput{Banned: ptr(1)}), errSelf)
		wantErr(t, e.Update(ctx, admin.ID, admin.ID, UpdateInput{IsAdmin: ptr(0)}), errSelf)
		wantErr(t, e.Update(ctx, admin.ID, 99999, UpdateInput{Balance: ptr(int64(1))}), errUserNotExist)
		// 修改为自己原来的邮箱（大小写不同）不算重复
		if err := e.Update(ctx, admin.ID, u.ID, UpdateInput{Email: ptr("new@X.com")}); err != nil {
			t.Fatal(err)
		}

		// 修改余额：记录变动金额、操作管理员与原因；余额不变时不记录；原因超长报错且不修改
		wantErr(t, e.Update(ctx, admin.ID, u.ID, UpdateInput{Balance: ptr(int64(1)), BalanceRemark: strings.Repeat("a", 256)}), errBalanceRemark)
		if err := e.Update(ctx, admin.ID, u.ID, UpdateInput{Balance: ptr(int64(380)), BalanceRemark: " 补偿 "}); err != nil {
			t.Fatal(err)
		}
		if err := e.Update(ctx, admin.ID, u.ID, UpdateInput{Balance: ptr(int64(380))}); err != nil {
			t.Fatal(err)
		}
		page, err := e.BalanceLogs(ctx, u.ID, BalanceLogQuery{})
		if err != nil || page.Total != 1 || len(page.Data) != 1 {
			t.Fatalf("余额变动日志条数错误：%+v %v", page, err)
		}
		if l := page.Data[0]; l.Type != balance.TypeAdmin || l.Amount != -120 || l.Balance != 380 || l.Remark != "补偿" ||
			l.OperatorID == nil || *l.OperatorID != admin.ID || l.OperatorEmail != "admin@x.com" {
			t.Fatalf("余额变动日志错误：%+v", l)
		}
		if page, _ := e.BalanceLogs(ctx, u.ID, BalanceLogQuery{Type: balance.TypeGiftcard}); page == nil || page.Total != 0 {
			t.Fatal("按类型筛选错误")
		}
		if _, err := e.BalanceLogs(ctx, u.ID, BalanceLogQuery{Type: "bad"}); !errors.Is(err, errInvalid) {
			t.Fatal("未知类型应报错")
		}
		if _, err := e.BalanceLogs(ctx, 99999, BalanceLogQuery{}); !errors.Is(err, errUserNotExist) {
			t.Fatal("不存在的用户应报错")
		}

		// 全站余额明细：邮箱模糊筛选、时间范围、增减合计
		now := time.Now().Unix()
		e.DB.Create(&model.BalanceLog{UserID: admin.ID, Amount: 200, Balance: 200, Type: balance.TypeGiftcard})
		e.DB.Create(&model.BalanceLog{UserID: admin.ID, Amount: -50, Balance: 150, Type: balance.TypePlugin})
		all, err := e.AllBalanceLogs(ctx, BalanceLogQuery{Email: "X.COM"})
		if err != nil || all.Total != 3 || all.Income != 200 || all.Expense != -170 {
			t.Fatalf("全站余额明细错误：%+v %v", all, err)
		}
		if all, _ := e.AllBalanceLogs(ctx, BalanceLogQuery{Email: "new@"}); all == nil || all.Total != 1 || all.Data[0].Email != "new@X.com" || all.Expense != -120 {
			t.Fatalf("按邮箱筛选错误：%+v", all)
		}
		if all, _ := e.AllBalanceLogs(ctx, BalanceLogQuery{Email: "admin@", Type: balance.TypeGiftcard, Since: now - 60, Until: now + 60}); all == nil || all.Total != 1 || all.Income != 200 {
			t.Fatalf("按类型与时间筛选错误：%+v", all)
		}
		if all, _ := e.AllBalanceLogs(ctx, BalanceLogQuery{Until: now - 3600}); all == nil || all.Total != 0 || all.Income != 0 {
			t.Fatalf("时间范围外应为空：%+v", all)
		}
		if _, err := e.AllBalanceLogs(ctx, BalanceLogQuery{Since: now, Until: now - 1}); !errors.Is(err, errInvalid) {
			t.Fatal("开始时间晚于结束时间应报错")
		}

		// 封禁时删除会话
		tok, _ := e.Auth.Issue(ctx, u.ID, "", "")
		if tok == "" {
			t.Fatal("签发失败")
		}
		if err := e.Update(ctx, admin.ID, u.ID, UpdateInput{Banned: ptr(1)}); err != nil {
			t.Fatal(err)
		}
		if s, _ := e.Auth.Sessions(ctx, u.ID); len(s) != 0 {
			t.Fatal("封禁后应删除会话")
		}

		// 邀请人：不存在、自己、循环、清除
		wantErr(t, e.SetInviter(ctx, u.ID, "none@x.com"), errInviterNotExist)
		wantErr(t, e.SetInviter(ctx, u.ID, "NEW@x.com"), errInviterSelf)
		wantErr(t, e.SetInviter(ctx, inv.ID, "new@x.com"), errInviterSelf) // u 由 inv 邀请
		c, _ := e.user(t, "c@x.com")
		e.DB.Model(c).Update("invite_user_id", u.ID)
		wantErr(t, e.SetInviter(ctx, inv.ID, "c@x.com"), errInviterSelf) // inv → u → c
		if err := e.SetInviter(ctx, inv.ID, "ADMIN@x.com"); err != nil {
			t.Fatal(err)
		}
		e.reload(t, &got, inv.ID)
		if got.InviteUserID == nil || *got.InviteUserID != admin.ID {
			t.Fatal("设置邀请人失败")
		}
		if err := e.SetInviter(ctx, inv.ID, ""); err != nil {
			t.Fatal(err)
		}
		e.reload(t, &got, inv.ID)
		if got.InviteUserID != nil {
			t.Fatal("清除邀请人失败")
		}
	})
}

func TestDeleteAndBatch(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		admin, _ := e.user(t, "admin@x.com")
		e.DB.Model(admin).Update("is_admin", 1)
		u, sub := e.user(t, "del@x.com")
		invitee, _ := e.user(t, "invitee@x.com")
		e.DB.Model(invitee).Update("invite_user_id", u.ID)
		e.DB.Transaction(func(tx *gorm.DB) error {
			_, err := e.Subscriptions.Create(tx, subscription.NewSubscription{UserID: u.ID, IgnoreLimit: true})
			return err
		})
		e.DB.Create(&model.Order{UserID: u.ID, TradeNo: "DEL1", PlanID: 1, Period: "month_price", TotalAmount: 1})
		e.DB.Create(&model.InviteCode{UserID: u.ID, Code: "abc"})
		tk := &model.Ticket{UserID: u.ID, Subject: "s"}
		e.DB.Create(tk)
		e.DB.Create(&model.TicketMessage{UserID: u.ID, TicketID: tk.ID, Message: "m"})
		e.DB.Create(&model.UserTelegram{UserID: u.ID, NotifyOrder: 1})
		e.Auth.Issue(ctx, u.ID, "", "")

		wantErr(t, e.Delete(ctx, admin.ID, admin.ID), errSelf)
		wantErr(t, e.Delete(ctx, admin.ID, 99999), errUserNotExist)
		if err := e.Delete(ctx, admin.ID, u.ID); err != nil {
			t.Fatal(err)
		}
		for name, m := range map[string]any{"用户": &model.User{}, "订阅": &model.Subscription{}, "订单": &model.Order{}, "邀请码": &model.InviteCode{},
			"工单": &model.Ticket{}, "Telegram": &model.UserTelegram{}} {
			col := "user_id"
			if name == "用户" {
				col = "id"
			}
			var n int64
			e.DB.Model(m).Where(col+" = ?", u.ID).Count(&n)
			if n != 0 {
				t.Errorf("%s未删除", name)
			}
		}
		var n int64
		e.DB.Model(&model.TicketMessage{}).Where("ticket_id = ?", tk.ID).Count(&n)
		var inv model.User
		e.reload(t, &inv, invitee.ID)
		if n != 0 || inv.InviteUserID != nil {
			t.Fatal("工单消息或被邀请人的邀请关系未清理")
		}
		if s, _ := e.Auth.Sessions(ctx, u.ID); len(s) != 0 {
			t.Fatal("删除后应清除会话")
		}
		if err := e.DB.Take(&model.Subscription{}, sub.ID).Error; !errors.Is(err, gorm.ErrRecordNotFound) {
			t.Fatal("主订阅未删除")
		}
	})
}

func TestBanBatchDeleteMail(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		admin, _ := e.user(t, "admin@x.com")
		e.DB.Model(admin).Update("is_admin", 1)
		a, _ := e.user(t, "a@spam.com")
		b, _ := e.user(t, "b@spam.com")
		ok, _ := e.user(t, "ok@good.com")
		e.Auth.Issue(ctx, a.ID, "", "")

		if _, err := e.Ban(ctx, admin.ID, nil); !errors.Is(err, errFilterRequired) {
			t.Fatal("批量封禁必须有筛选条件")
		}
		n, err := e.Ban(ctx, admin.ID, []Filter{{"email", "模糊", "@"}})
		if err != nil || n != 3 {
			t.Fatalf("封禁人数错误：%d %v", n, err)
		}
		var got model.User
		e.reload(t, &got, admin.ID)
		if got.Banned != 0 {
			t.Fatal("不能封禁自己")
		}
		if s, _ := e.Auth.Sessions(ctx, a.ID); len(s) != 0 {
			t.Fatal("封禁后应删除会话")
		}

		// 群发：没有筛选时发给全部用户
		if _, err := e.SendMail(ctx, MailInput{Subject: " ", Content: "x"}); !errors.Is(err, errMailRequired) {
			t.Fatal("主题为空应报错")
		}
		n, err = e.SendMail(ctx, MailInput{Filter: []Filter{{"email", "模糊", "spam"}}, Subject: "通知", Content: "<b>hi</b>"})
		if err != nil || n != 2 || len(e.mail.sent) != 2 || !e.mail.sent[0].TrustedHTML || e.mail.sent[0].TemplateName != "notify" ||
			e.mail.sent[0].TemplateValue["content"] != "<b>hi</b>" {
			t.Fatalf("群发错误：%d %v %+v", n, err, e.mail.sent)
		}
		if n, _ := e.SendMail(ctx, MailInput{Subject: "s", Content: "c"}); n != 4 {
			t.Fatalf("无筛选应发给全部 4 个用户，实际 %d", n)
		}

		if _, err := e.BatchDelete(ctx, admin.ID, nil); !errors.Is(err, errFilterRequired) {
			t.Fatal("批量删除必须有筛选条件")
		}
		n, err = e.BatchDelete(ctx, admin.ID, []Filter{{"email", "模糊", "spam"}, {"id", ">", "0"}})
		if err != nil || n != 2 {
			t.Fatalf("批量删除人数错误：%d %v", n, err)
		}
		var left []int64
		e.DB.Model(&model.User{}).Order("id").Pluck("id", &left)
		if fmt.Sprint(left) != fmt.Sprint([]int64{admin.ID, ok.ID}) {
			t.Fatalf("剩余用户错误：%v", left)
		}
		var subs int64
		e.DB.Model(&model.Subscription{}).Where("user_id IN ?", []int64{a.ID, b.ID}).Count(&subs)
		if subs != 0 {
			t.Fatal("订阅未删除")
		}
		if n, _ := e.BatchDelete(ctx, admin.ID, []Filter{{"id", "=", fmt.Sprint(admin.ID)}}); n != 0 {
			t.Fatal("不能删除自己")
		}
	})
}

func readCSV(t *testing.T, b []byte) [][]string {
	t.Helper()
	rows, err := csv.NewReader(strings.NewReader(string(b))).ReadAll()
	if err != nil {
		t.Fatal(err)
	}
	return rows
}

func TestGenerateExport(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.plan(t, "套餐")
		e.user(t, "Taken@gen.com")
		exp := time.Now().Add(48 * time.Hour).Unix()

		if _, err := e.Generate(ctx, GenerateInput{EmailPrefix: "taken", EmailSuffix: "GEN.com"}, ""); !errors.Is(err, errEmailUsed) {
			t.Fatalf("邮箱已存在应报错：%v", err)
		}
		if _, err := e.Generate(ctx, GenerateInput{EmailPrefix: "a b", EmailSuffix: "gen.com"}, ""); !errors.Is(err, errEmailFormat) {
			t.Fatal("邮箱格式错误应报错")
		}
		if _, err := e.Generate(ctx, GenerateInput{EmailPrefix: "x", EmailSuffix: "gen.com", PlanID: 9999}, ""); !errors.Is(err, errPlanNotExist) {
			t.Fatal("套餐不存在应报错")
		}
		if b, err := e.Generate(ctx, GenerateInput{EmailPrefix: "one", EmailSuffix: "gen.com", PlanID: p.ID, ExpiredAt: &exp}, ""); err != nil || b != nil {
			t.Fatal(err)
		}
		var one model.User
		e.DB.Where("email = ?", "one@gen.com").Take(&one)
		sub, _ := e.Subscriptions.Primary(ctx, e.DB, one.ID)
		if !util.VerifyPassword("", "", "one@gen.com", one.Password) || sub == nil || sub.PlanID == nil || *sub.PlanID != p.ID ||
			sub.TransferEnable != 100*gb || sub.ExpiredAt == nil || *sub.ExpiredAt != exp || one.ExpiredAt == nil || *one.ExpiredAt != exp {
			t.Fatalf("单个生成结果错误：%+v %+v", one, sub)
		}

		for _, n := range []int{0, 501} {
			if _, err := e.Generate(ctx, GenerateInput{EmailSuffix: "gen.com", GenerateCount: n}, ""); !errors.Is(err, errGenerateCount) {
				t.Fatalf("数量 %d 应报错", n)
			}
		}
		b, err := e.Generate(ctx, GenerateInput{EmailSuffix: "batch.com", GenerateCount: 20, PlanID: p.ID, Password: "samepass1"}, "https://s")
		if err != nil {
			t.Fatal(err)
		}
		rows := readCSV(t, b)
		if len(rows) != 21 || rows[0][0] != "账号" || rows[1][1] != "samepass1" || rows[1][2] != "长期有效" || !strings.HasPrefix(rows[1][5], "https://s") {
			t.Fatalf("批量生成 CSV 错误：%v", rows[:2])
		}
		var n int64
		e.DB.Model(&model.User{}).Where("email LIKE ?", "%@batch.com").Count(&n)
		var first model.User
		e.DB.Where("email = ?", rows[1][0]).Take(&first)
		if n != 20 || !util.VerifyPassword("", "", "samepass1", first.Password) {
			t.Fatalf("批量生成用户数 %d", n)
		}

		// 导出：每个订阅一行，公式字符加前缀
		e.DB.Transaction(func(tx *gorm.DB) error {
			_, err := e.Subscriptions.Create(tx, subscription.NewSubscription{UserID: one.ID, Name: "=HYPERLINK(1)", IgnoreLimit: true})
			return err
		})
		b, err = e.Export(ctx, []Filter{{"email", "=", "one@gen.com"}}, "https://s")
		if err != nil {
			t.Fatal(err)
		}
		rows = readCSV(t, b)
		if len(rows) != 3 || rows[1][5] != "是" || rows[1][6] != "100" || rows[1][10] != "套餐" || rows[2][4] != "'=HYPERLINK(1)" || rows[2][9] != "长期有效" {
			t.Fatalf("导出错误：%v", rows)
		}
		b, _ = e.Export(ctx, nil, "")
		if rows := readCSV(t, b); len(rows) != 1+1+1+1+20 {
			t.Fatalf("全部导出行数错误：%d", len(rows))
		}
	})
}

func TestSubscriptionAdmin(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		p := e.plan(t, "P")
		u, primary := e.user(t, "s@x.com")
		e.DB.Model(u).Update("subscription_limit", 1)

		sub, err := e.CreateSub(ctx, u.ID, SubInput{Name: ptr("第二"), PlanID: httpx.Some(p.ID), TransferEnable: ptr(5 * gb)})
		if err != nil {
			t.Fatal(err)
		}
		if sub.IsPrimary != 0 || sub.PlanID == nil || *sub.GroupID != 7 || sub.TransferEnable != 5*gb || sub.ExpiredAt != nil || *sub.DeviceLimit != 3 {
			t.Fatalf("新开订阅错误（不受数量上限限制、按套餐设置后覆盖）：%+v", sub)
		}
		if _, err := e.CreateSub(ctx, 99999, SubInput{}); !errors.Is(err, errUserNotExist) {
			t.Fatal("用户不存在应报错")
		}
		if _, err := e.CreateSub(ctx, u.ID, SubInput{PlanID: httpx.Some(int64(9999))}); !errors.Is(err, errPlanNotExist) {
			t.Fatal("套餐不存在应报错")
		}

		exp := int64(2_000_000_000)
		if err := e.UpdateSub(ctx, sub.ID, SubInput{PlanID: httpx.Optional[int64]{Set: true}, ExpiredAt: httpx.Some(exp),
			DeviceLimit: httpx.Optional[int64]{Set: true}, U: ptr(int64(1)), Banned: ptr(1)}); err != nil {
			t.Fatal(err)
		}
		var got model.Subscription
		e.reload(t, &got, sub.ID)
		if got.PlanID != nil || got.GroupID != nil || got.DeviceLimit != nil || *got.ExpiredAt != exp || got.U != 1 || got.Banned != 1 ||
			got.TransferEnable != 5*gb || got.Name != "第二" {
			t.Fatalf("修改订阅错误：%+v", got)
		}
		// 修改流量上限时清零本周期奖励流量（决策 D87），只改其它字段时保留
		e.DB.Model(&got).Update("bonus_traffic", gb)
		if err := e.UpdateSub(ctx, sub.ID, SubInput{U: ptr(int64(2))}); err != nil {
			t.Fatal(err)
		}
		if e.reload(t, &got, sub.ID); got.BonusTraffic != gb {
			t.Fatalf("未修改流量上限时应保留奖励流量：%+v", got)
		}
		if err := e.UpdateSub(ctx, sub.ID, SubInput{TransferEnable: ptr(int64(6 * gb))}); err != nil {
			t.Fatal(err)
		}
		if e.reload(t, &got, sub.ID); got.TransferEnable != 6*gb || got.BonusTraffic != 0 {
			t.Fatalf("修改流量上限应清零奖励流量：%+v", got)
		}
		for _, in := range []SubInput{{U: ptr(int64(-1))}, {ExpiredAt: httpx.Some(int64(-1))}, {Banned: ptr(5)}, {Name: ptr(strings.Repeat("长", 65))}} {
			wantErr(t, e.UpdateSub(ctx, sub.ID, in), errInvalid)
		}
		wantErr(t, e.UpdateSub(ctx, 99999, SubInput{}), errSubNotExist)

		// 重置与主订阅
		if err := e.ResetSub(ctx, sub.ID); err != nil {
			t.Fatal(err)
		}
		var after model.Subscription
		e.reload(t, &after, sub.ID)
		if after.Token == got.Token || after.UUID == got.UUID {
			t.Fatal("重置失败")
		}
		if err := e.SetPrimary(ctx, sub.ID); err != nil {
			t.Fatal(err)
		}
		var old model.Subscription
		e.reload(t, &old, primary.ID)
		e.reload(t, &after, sub.ID)
		if old.IsPrimary != 0 || after.IsPrimary != 1 {
			t.Fatal("设为主订阅失败")
		}

		// 删除：主订阅、有未完成订单的不能删除
		wantErr(t, e.DeleteSub(ctx, sub.ID), errSubPrimary)
		o := &model.Order{UserID: u.ID, TradeNo: "SUBORD", PlanID: p.ID, Period: "month_price", TotalAmount: 1, SubscriptionID: &primary.ID}
		e.DB.Create(o)
		wantErr(t, e.DeleteSub(ctx, primary.ID), errSubHasOrder)
		e.DB.Model(o).Update("status", model.OrderStatusCompleted)
		if err := e.DeleteSub(ctx, primary.ID); err != nil {
			t.Fatal(err)
		}
		wantErr(t, e.DeleteSub(ctx, primary.ID), errSubNotExist)
	})
}

func TestHTTP(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		const secure = "adminuser0123456789"
		if err := e.Settings.Set(ctx, map[string]any{"secure_path": secure}); err != nil {
			t.Fatal(err)
		}
		admin, _ := e.user(t, "admin@x.com")
		e.DB.Model(admin).Update("is_admin", 1)
		u, sub := e.user(t, "u@x.com")
		adminTok, _ := e.Auth.Issue(ctx, admin.ID, "", "")
		userTok, _ := e.Auth.Issue(ctx, u.ID, "", "")
		cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
		r, err := server.NewRouter(cfg, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		r.Mount(&Module{Svc: e.Service})
		do := func(method, path, body, tok string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, "/api/t1/"+secure+path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if tok != "" {
				req.Header.Set("Authorization", tok)
			}
			w := httptest.NewRecorder()
			r.Engine.ServeHTTP(w, req)
			return w
		}
		if w := do("POST", "/users/query", `{}`, userTok); w.Code != http.StatusNotFound {
			t.Fatalf("普通用户应 404，实际 %d", w.Code)
		}
		if w := do("POST", "/users/query", `{"filter":[{"key":"email","condition":"模糊","value":"u@"}]}`, adminTok); w.Code != 200 ||
			!strings.Contains(w.Body.String(), `"total":1`) || strings.Contains(w.Body.String(), `"password"`) {
			t.Fatalf("列表错误：%d %s", w.Code, w.Body)
		}
		if w := do("PUT", fmt.Sprintf("/users/%d", u.ID), `{"remarks":"r","discount":null}`, adminTok); w.Code != 200 {
			t.Fatalf("修改失败：%d %s", w.Code, w.Body)
		}
		if w := do("PUT", fmt.Sprintf("/users/%d", u.ID), `{"discount":"x"}`, adminTok); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("类型错误应 422，实际 %d", w.Code)
		}
		if w := do("GET", fmt.Sprintf("/users/%d", u.ID), "", adminTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"remarks":"r"`) ||
			!strings.Contains(w.Body.String(), sub.Token) {
			t.Fatalf("详情错误：%s", w.Body)
		}
		if w := do("POST", "/users/generate", `{"email_suffix":"h.com","generate_count":2}`, adminTok); w.Code != 200 ||
			!strings.HasPrefix(w.Body.String(), "\xEF\xBB\xBF账号,") || w.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
			t.Fatalf("批量生成接口错误：%d %s", w.Code, w.Body)
		}
		if w := do("POST", "/users/export", `{}`, adminTok); w.Code != 200 || !strings.HasPrefix(w.Body.String(), "\xEF\xBB\xBF邮箱,") {
			t.Fatalf("导出接口错误：%d", w.Code)
		}
		if w := do("POST", fmt.Sprintf("/users/%d/subscriptions", u.ID), `{"name":"n","expired_at":null}`, adminTok); w.Code != 200 ||
			!strings.Contains(w.Body.String(), `"name":"n"`) {
			t.Fatalf("新开订阅接口错误：%s", w.Body)
		}
		if w := do("POST", "/users/ban", `{"filter":[]}`, adminTok); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("无筛选封禁应 422，实际 %d", w.Code)
		}
		if w := do("DELETE", fmt.Sprintf("/users/%d", admin.ID), "", adminTok); w.Code != http.StatusBadRequest {
			t.Fatalf("删除自己应 400，实际 %d", w.Code)
		}
		if w := do("POST", fmt.Sprintf("/users/%d/inviter", u.ID), `{"invite_user_email":"admin@x.com"}`, adminTok); w.Code != 200 {
			t.Fatalf("设置邀请人失败：%s", w.Body)
		}
	})
}
