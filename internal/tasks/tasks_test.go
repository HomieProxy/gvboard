package tasks

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/cache"
	"gvboard/internal/mail"
	"gvboard/internal/model"
	"gvboard/internal/setting"
	"gvboard/internal/telegram"
	"gvboard/internal/testutil"
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

type notice struct {
	user  int64 // 0 为通知管理员
	kind  telegram.NotifyKind
	text  string
	staff bool
	topic string
}

type fakeNotify struct {
	mu   sync.Mutex
	sent []notice
}

func (f *fakeNotify) NotifyAdmins(_ context.Context, topic string, staff bool, build func(string) string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, notice{text: build("zh-CN"), staff: staff, topic: topic})
}

func (f *fakeNotify) NotifyUser(_ context.Context, userID int64, kind telegram.NotifyKind, build func(string) string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.sent = append(f.sent, notice{user: userID, kind: kind, text: build("zh-CN")})
}

type env struct {
	*Service
	mail   *fakeMail
	notify *fakeNotify
	at     time.Time
}

func newEnv(t *testing.T, driver string) *env {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := &env{mail: &fakeMail{}, notify: &fakeNotify{}, at: time.Date(2026, 11, 14, 11, 30, 0, 0, time.Local)}
	e.Service = &Service{
		DB: db, Cache: c, Settings: st, Mail: e.mail, Notify: e.notify,
		Translate: func(locale, key string, params map[string]any) string {
			out := "[" + locale + "]" + key
			for k, v := range params {
				out = strings.ReplaceAll(out, ":"+k, fmt.Sprint(v))
			}
			if strings.HasPrefix(key, "telegram.") {
				out += " " + fmt.Sprint(params)
			}
			return out
		},
		Now: func() time.Time { return e.at },
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

func (e *env) create(t *testing.T, v any) {
	t.Helper()
	if err := e.DB.Create(v).Error; err != nil {
		t.Fatal(err)
	}
}

// user 创建用户，返回用户与主订阅。
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

func (e *env) set(t *testing.T, kv map[string]any) {
	t.Helper()
	if err := e.Settings.Set(context.Background(), kv); err != nil {
		t.Fatal(err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestJobs(t *testing.T) {
	want := map[string]string{
		"statistics": "10 0 * * *", "daily_report": "20 0 * * *", "reset_traffic": "0 0 * * *", "reset_log": "0 0 * * *",
		"send_remind_mail": "30 11 * * *", "check_server": "*/5 * * * *",
	}
	jobs := (&Service{}).Jobs()
	if len(jobs) != len(want) {
		t.Fatalf("任务数量错误：%d", len(jobs))
	}
	for _, j := range jobs {
		if want[j.Name] != j.Spec || !j.NoOverlap || j.Run == nil {
			t.Errorf("任务 %s 配置错误：%+v", j.Name, j)
		}
	}
}

func TestStatistics(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		day := time.Date(2026, 11, 13, 15, 0, 0, 0, time.Local)
		from := time.Date(2026, 11, 13, 0, 0, 0, 0, time.Local).Unix()
		to := from + 86400
		a, _ := e.user(t, "a@x.com")
		b, _ := e.user(t, "b@x.com")
		c, _ := e.user(t, "c@x.com")
		e.DB.Model(a).Update("created_at", from+10)
		e.DB.Model(b).Updates(map[string]any{"created_at": from + 20, "invite_user_id": a.ID})
		e.DB.Model(c).Update("created_at", to)
		orders := []model.Order{
			{TotalAmount: 1000, Status: 3, CreatedAt: from + 1, PaidAt: ptr(from + 2)},
			{TotalAmount: 500, Status: 0, CreatedAt: from + 1},
			{TotalAmount: 300, Status: 2, CreatedAt: from + 1, PaidAt: ptr(from + 2)},
			{TotalAmount: 700, Status: 4, CreatedAt: from - 100, PaidAt: ptr(from + 50)},
			{TotalAmount: 900, Status: 3, CreatedAt: to, PaidAt: ptr(to)},
		}
		for i := range orders {
			orders[i].UserID, orders[i].PlanID, orders[i].Period = a.ID, 1, "month_price"
			orders[i].TradeNo = fmt.Sprintf("ST%d", i)
			e.create(t, &orders[i])
		}
		e.create(t, &model.CommissionLog{InviteUserID: a.ID, UserID: b.ID, TradeNo: "C1", GetAmount: 30, CreatedAt: from + 5})
		e.create(t, &model.CommissionLog{InviteUserID: a.ID, UserID: b.ID, TradeNo: "C2", GetAmount: 70, CreatedAt: to})
		e.create(t, &model.StatServer{ServerID: 1, ServerType: "vmess", U: 100, D: 200, RecordType: "d", RecordAt: from})
		e.create(t, &model.StatServer{ServerID: 2, ServerType: "trojan", U: 1, D: 2, RecordType: "d", RecordAt: from})
		e.create(t, &model.StatServer{ServerID: 1, ServerType: "vmess", U: 999, D: 999, RecordType: "d", RecordAt: to})

		if err := e.Statistics(ctx, day); err != nil {
			t.Fatal(err)
		}
		var got model.Stat
		if err := e.DB.Where("record_at = ? AND record_type = ?", from, "d").Take(&got).Error; err != nil {
			t.Fatal(err)
		}
		want := model.Stat{
			OrderCount: 3, OrderTotal: 1800, PaidCount: 2, PaidTotal: 1700, CommissionCount: 1, CommissionTotal: 30,
			RegisterCount: 2, InviteCount: 1, TransferUsedTotal: "303",
		}
		got.ID, got.RecordAt, got.RecordType, got.CreatedAt, got.UpdatedAt = 0, 0, "", 0, 0
		if got != want {
			t.Fatalf("统计错误：\n实际 %+v\n期望 %+v", got, want)
		}

		// 再次执行时覆盖同一条记录
		e.DB.Model(&model.Order{}).Where("trade_no = ?", "ST1").Updates(map[string]any{"status": 3, "paid_at": from + 3})
		if err := e.Statistics(ctx, day); err != nil {
			t.Fatal(err)
		}
		var rows []model.Stat
		e.DB.Where("record_type = ?", "d").Find(&rows)
		if len(rows) != 1 || rows[0].PaidCount != 3 || rows[0].PaidTotal != 2200 {
			t.Fatalf("重复执行应覆盖：%+v", rows)
		}
	})
}

func TestDailyReport(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		day := time.Date(2026, 11, 13, 15, 0, 0, 0, time.Local)
		from := time.Date(2026, 11, 13, 0, 0, 0, 0, time.Local).Unix()
		a, _ := e.user(t, "a@x.com")
		e.DB.Model(a).Update("created_at", from+10)
		e.create(t, &model.Ticket{UserID: a.ID, Subject: "s", Level: 0, CreatedAt: from + 1, UpdatedAt: from + 1})
		e.create(t, &model.Ticket{UserID: a.ID, Subject: "s", Level: 0, CreatedAt: from - 1, UpdatedAt: from - 1})
		e.create(t, &model.Order{UserID: a.ID, PlanID: 1, Period: "month_price", TradeNo: "D1", TotalAmount: 1234, Status: 3,
			CreatedAt: from + 1, PaidAt: ptr(from + 2)})
		e.create(t, &model.StatServer{ServerID: 1, ServerType: "vmess", U: 1 << 30, D: 1 << 30, RecordType: "d", RecordAt: from})

		// 统计记录不存在时先生成
		if err := e.DailyReport(ctx, day); err != nil {
			t.Fatal(err)
		}
		var n int64
		e.DB.Model(&model.Stat{}).Where("record_at = ?", from).Count(&n)
		if n != 1 || len(e.notify.sent) != 1 {
			t.Fatalf("应生成统计并发送一条：%d %+v", n, e.notify.sent)
		}
		got := e.notify.sent[0]
		for _, s := range []string{"2026-11-13", "12.34", "2 GB"} {
			if !strings.Contains(got.text, s) {
				t.Errorf("每日统计缺少 %q：%s", s, got.text)
			}
		}
		if got.topic != telegram.TopicDaily || got.user != 0 || !strings.Contains(got.text, "tickets:1") {
			t.Fatalf("每日统计内容错误：%+v", got)
		}

		e.Notify = nil
		if err := e.DailyReport(ctx, day); err != nil {
			t.Fatal(err)
		}
	})
}

func TestCleanLogs(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		statLimit := e.at.AddDate(0, -2, 0).Unix()
		logLimit := e.at.AddDate(0, -1, 0).Unix()
		for i := range 3 {
			e.create(t, &model.StatUser{UserID: 1, ServerRate: 1, RecordType: "d", RecordAt: statLimit - 1 - int64(i)})
		}
		e.create(t, &model.StatUser{UserID: 1, ServerRate: 1, RecordType: "d", RecordAt: statLimit})
		e.create(t, &model.StatServer{ServerID: 1, ServerType: "vmess", RecordType: "d", RecordAt: statLimit - 1})
		e.create(t, &model.StatServer{ServerID: 1, ServerType: "vmess", RecordType: "d", RecordAt: statLimit + 1})
		e.create(t, &model.Log{Title: "old", CreatedAt: logLimit - 1})
		e.create(t, &model.Log{Title: "new", CreatedAt: logLimit})
		e.CleanSubscribeLogs = func(context.Context) (int64, error) { return 7, nil }

		r, err := e.CleanLogs(context.Background())
		if err != nil || *r != (CleanResult{StatUser: 3, StatServer: 1, Log: 1, Subscribe: 7}) {
			t.Fatalf("清理结果错误：%+v %v", r, err)
		}
		var su, ss, lg int64
		e.DB.Model(&model.StatUser{}).Count(&su)
		e.DB.Model(&model.StatServer{}).Count(&ss)
		e.DB.Model(&model.Log{}).Count(&lg)
		if su != 1 || ss != 1 || lg != 1 {
			t.Fatalf("剩余记录错误：%d %d %d", su, ss, lg)
		}
	})
}

func TestResetTraffic(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		resetRetryDelay = 0
		plan := func(method *int) *model.Plan {
			p := &model.Plan{Name: "p", GroupID: 1, TransferEnable: 100, Show: 1, Renew: 1, ResetTrafficMethod: method}
			e.create(t, p)
			return p
		}
		global := plan(nil)
		expireDay := plan(ptr(ResetExpireDay))
		never := plan(ptr(ResetNever))
		yearFirst := plan(ptr(ResetYearFirstDay))
		expireYear := plan(ptr(ResetExpireYear))
		n := 0
		sub := func(p *model.Plan, expired *int64) *model.Subscription {
			t.Helper()
			n++
			s := &model.Subscription{
				UserID: 1, Name: "s", UUID: fmt.Sprintf("uuid-reset-%d", n), Token: fmt.Sprintf("token-reset-%d", n),
				PlanID: &p.ID, ExpiredAt: expired, TransferEnable: 100, U: 10, D: 20,
			}
			e.create(t, s)
			return s
		}
		at := func(y int, m time.Month, d, h int) *int64 {
			v := time.Date(y, m, d, h, 0, 0, 0, time.Local).Unix()
			return &v
		}
		used := func(s *model.Subscription) int64 {
			var got model.Subscription
			e.DB.Take(&got, s.ID)
			return got.U + got.D
		}
		restore := func() { e.DB.Model(&model.Subscription{}).Where("1 = 1").Updates(map[string]any{"u": 10, "d": 20}) }
		run := func(now time.Time, method int) {
			t.Helper()
			e.at = now
			e.set(t, map[string]any{"reset_traffic_method": method})
			restore()
			if _, err := e.ResetTraffic(ctx); err != nil {
				t.Fatal(err)
			}
			if n, _ := e.Cache.Exists(ctx, e.Cache.KeyOf(cache.KeyTrafficResetLock, nil)).Result(); n != 0 {
				t.Fatal("结束后应删除流量重置锁")
			}
		}

		gYear := sub(global, at(2027, 11, 14, 10))      // 全局方式；到期日与 11-14 相同
		gOther := sub(global, at(2027, 3, 5, 10))       // 全局方式
		gExpired := sub(global, at(2026, 11, 1, 0))     // 已到期
		gOnetime := sub(global, nil)                    // 一次性
		eDay := sub(expireDay, at(2027, 1, 14, 9))      // 每月 14 日
		eSoon := sub(expireDay, at(2026, 12, 1, 0))     // 1 日到期
		eNear := sub(expireDay, at(2026, 11, 14, 23))   // 今天到期但不足 25 天
		e31 := sub(expireDay, at(2027, 1, 31, 9))       // 31 日到期
		nv := sub(never, at(2027, 1, 1, 0))             // 不重置
		yf := sub(yearFirst, at(2027, 5, 5, 0))         // 每年 1 月 1 日
		ey := sub(expireYear, at(2027, 11, 14, 8))      // 每年到期日
		eyOther := sub(expireYear, at(2027, 11, 15, 8)) // 明天
		check := func(name string, want map[*model.Subscription]bool) {
			t.Helper()
			for s, reset := range want {
				if got := used(s) == 0; got != reset {
					t.Errorf("%s：订阅 %d 期望重置 %v，实际 %v", name, s.ID, reset, got)
				}
			}
		}

		// 11 月 14 日，全局为"每年 1 月 1 日"：不应再执行"每年到期日"重置（修复原项目 bug）
		run(time.Date(2026, 11, 14, 0, 0, 0, 0, time.Local), ResetYearFirstDay)
		check("11-14 全局每年1月1日", map[*model.Subscription]bool{
			gYear: false, gOther: false, eDay: true, eSoon: false, eNear: false, e31: false, nv: false, yf: false,
			ey: true, eyOther: false, gExpired: false, gOnetime: false,
		})
		// 全局为"每年到期日"
		run(time.Date(2026, 11, 14, 0, 0, 0, 0, time.Local), ResetExpireYear)
		check("11-14 全局每年到期日", map[*model.Subscription]bool{gYear: true, gOther: false})
		// 11 月 30 日（月末）：31 日到期的按月末重置
		run(time.Date(2026, 11, 30, 0, 0, 0, 0, time.Local), ResetNever)
		check("11-30", map[*model.Subscription]bool{e31: true, eDay: false, gYear: false})
		// 12 月 1 日，全局为每月 1 日：全局套餐的有效订阅重置，1 日到期的不足 25 天不重置
		run(time.Date(2026, 12, 1, 0, 0, 0, 0, time.Local), ResetMonthFirstDay)
		check("12-01", map[*model.Subscription]bool{
			gYear: true, gOther: true, gExpired: false, gOnetime: false, eSoon: false, yf: false, nv: false,
		})
		// 1 月 1 日
		run(time.Date(2027, 1, 1, 0, 0, 0, 0, time.Local), ResetNever)
		check("01-01", map[*model.Subscription]bool{yf: true, nv: false, gYear: false})
	})
}

func TestRemind(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		e.set(t, map[string]any{"app_name": "测试站", "app_url": "https://x.com"})
		now := e.at.Unix()
		u, primary := e.user(t, "u@x.com")
		e.DB.Model(primary).Updates(map[string]any{"expired_at": now + 3600, "transfer_enable": 100, "u": 10})
		named := &model.Subscription{UserID: u.ID, Name: "家<里>", UUID: "uuid-rm-1", Token: "token-rm-1",
			ExpiredAt: ptr(now + 86400*10), TransferEnable: 100, U: 50, D: 46, RemindExpire: 1, RemindTraffic: 1}
		e.create(t, named)
		mk := func(i int, mut func(s *model.Subscription)) *model.Subscription {
			s := &model.Subscription{UserID: u.ID, Name: fmt.Sprintf("s%d", i), UUID: fmt.Sprintf("uuid-rm-x%d", i),
				Token: fmt.Sprintf("token-rm-x%d", i), ExpiredAt: ptr(now + 86400*10), TransferEnable: 100, RemindExpire: 1, RemindTraffic: 1}
			mut(s)
			e.create(t, s)
			return s
		}
		mk(1, func(s *model.Subscription) { s.ExpiredAt, s.RemindExpire = ptr(now+3600), 0 }) // 关闭到期提醒
		mk(2, func(s *model.Subscription) { s.ExpiredAt = ptr(now - 10) })                    // 已到期
		mk(3, func(s *model.Subscription) { s.ExpiredAt = ptr(now + 86400 + 10) })            // 超过 24 小时
		mk(4, func(s *model.Subscription) { s.U = 100 })                                      // 流量已用完
		mk(5, func(s *model.Subscription) { s.U = 94 })                                       // 不足 95%
		mk(6, func(s *model.Subscription) { s.U, s.RemindTraffic = 96, 0 })                   // 关闭流量提醒
		mk(7, func(s *model.Subscription) { s.U, s.ExpiredAt = 96, ptr(now-10) })             // 已到期
		forever := mk(8, func(s *model.Subscription) { s.U, s.ExpiredAt = 95, nil })          // 长期有效、正好 95%
		mk(9, func(s *model.Subscription) { s.TransferEnable, s.U = 0, 0 })                   // 无流量
		_, other := e.user(t, "o@x.com")
		e.DB.Model(other).Update("expired_at", now+60)

		r, err := e.Remind(ctx)
		if err != nil || *r != (RemindResult{Expire: 2, Traffic: 2}) {
			t.Fatalf("提醒数量错误：%+v %v", r, err)
		}
		bySub := map[string]mail.Payload{}
		for _, p := range e.mail.sent {
			bySub[p.TemplateName+"|"+p.TemplateValue["subscription"].(string)] = p
			if p.TemplateValue["name"] != "测试站" || p.TemplateValue["url"] != "https://x.com" {
				t.Errorf("模板变量错误：%v", p.TemplateValue)
			}
		}
		pe := bySub[mail.TplRemindExpire+"|#"+fmt.Sprint(primary.ID)]
		if pe.Email != "u@x.com" || pe.Subject != "[]The service in 测试站 is about to expire" {
			t.Fatalf("到期提醒邮件错误：%+v", e.mail.sent)
		}
		if _, ok := bySub[mail.TplRemindExpire+"|#"+fmt.Sprint(other.ID)]; !ok {
			t.Fatal("其他用户的到期提醒缺失")
		}
		pt := bySub[mail.TplRemindTraffic+"|家<里>"]
		if pt.Email != "u@x.com" || pt.Subject != "[]The traffic usage in 测试站 has reached 95%" {
			t.Fatalf("流量提醒邮件错误：%+v", e.mail.sent)
		}
		if _, ok := bySub[mail.TplRemindTraffic+"|s8"]; !ok || forever.ID == 0 {
			t.Fatal("长期有效订阅的流量提醒缺失")
		}

		var tg []notice
		for _, n := range e.notify.sent {
			if n.user != u.ID || n.kind != telegram.NotifyRemind {
				continue
			}
			tg = append(tg, n)
		}
		if len(e.notify.sent) != 4 || len(tg) != 3 {
			t.Fatalf("Telegram 提醒数量错误：%+v", e.notify.sent)
		}
		var found bool
		for _, n := range tg {
			if strings.Contains(n.text, "telegram.remind_traffic") && strings.Contains(n.text, "<b>家&lt;里&gt;</b>") &&
				strings.Contains(n.text, "used:0.00 GB") && strings.Contains(n.text, "total:0.00 GB") {
				found = true
			}
		}
		if !found {
			t.Fatalf("Telegram 流量提醒内容错误（订阅名称应转义）：%+v", tg)
		}

		// 第二天：到期提醒照常发送，流量提醒 24 小时内不重复
		e.mail.sent = nil
		if r, _ = e.Remind(ctx); *r != (RemindResult{Expire: 2}) {
			t.Fatalf("流量提醒不应重复：%+v", r)
		}
	})
}

func TestRemindTemplates(t *testing.T) {
	m, err := mail.New(nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, theme := range m.Themes() {
		for _, tpl := range []string{mail.TplRemindExpire, mail.TplRemindTraffic} {
			out, err := m.Render(theme, tpl, map[string]any{"name": "站", "url": "https://x.com", "subscription": "家<里>"})
			if err != nil || !strings.Contains(out, "<strong>家&lt;里&gt;</strong>") {
				t.Errorf("%s/%s 应显示转义后的订阅名称：%v", theme, tpl, err)
			}
			out, _ = m.Render(theme, tpl, map[string]any{"name": "站", "url": "https://x.com"})
			if strings.Contains(out, "<strong></strong>") || strings.Contains(out, "订阅 <strong>") {
				t.Errorf("%s/%s 未提供订阅名称时应保持原文案", theme, tpl)
			}
		}
	}
}

func TestCheckServers(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		now := e.at.Unix()
		base := func(name string, parent *int64) model.ServerBase {
			return model.ServerBase{GroupID: model.IntList{1}, RouteID: model.IntList{}, Name: name, Host: name + ".example.com",
				Port: "443", ServerPort: 443, Tags: model.StrList{}, Rate: "1", Show: 1, ParentID: parent}
		}
		down := &model.ServerVmess{ServerBase: base("down<1>", nil), Network: "tcp"}
		e.create(t, down)
		child := &model.ServerVmess{ServerBase: base("child", &down.ID), Network: "tcp"}
		e.create(t, child)
		up := &model.ServerTrojan{ServerBase: base("up", nil)}
		e.create(t, up)
		never := &model.ServerTrojan{ServerBase: base("never", nil)}
		e.create(t, never)
		zero := &model.ServerVmess{ServerBase: base("zero-parent", ptr(int64(0))), Network: "tcp"}
		e.create(t, zero)
		set := func(typ string, id, v int64) {
			if err := e.Cache.SetJSON(ctx, e.Cache.KeyOf(cache.KeyServerLastCheckAt, cache.ServerKey(typ, id)), v, time.Hour); err != nil {
				t.Fatal(err)
			}
		}
		set(model.ServerTypeVmess, down.ID, now-1801)
		set(model.ServerTypeVmess, child.ID, now-5000)
		set(model.ServerTypeTrojan, up.ID, now-1800)
		set(model.ServerTypeVmess, zero.ID, now-4000)

		list, err := e.CheckServers(ctx)
		if err != nil || len(list) != 2 || list[0].Name != "down<1>" || list[1].Name != "zero-parent" {
			t.Fatalf("掉线检查错误：%+v %v", list, err)
		}
		if len(e.notify.sent) != 2 || e.notify.sent[0].user != 0 || e.notify.sent[0].staff ||
			!strings.Contains(e.notify.sent[0].text, "down&lt;1&gt;") || !strings.Contains(e.notify.sent[0].text, "down&lt;1&gt;.example.com") {
			t.Fatalf("掉线通知错误：%+v", e.notify.sent)
		}
		if list, _ = e.CheckServers(ctx); len(list) != 0 {
			t.Fatalf("同一次掉线只通知一次：%+v", list)
		}
		e.Notify = nil
		set(model.ServerTypeVmess, down.ID, now-2000)
		if list, err = e.CheckServers(ctx); err != nil || len(list) != 1 {
			t.Fatalf("未配置通知时仍应检查：%+v %v", list, err)
		}
	})
}

func TestCheckServerSwitch(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		if !e.Settings.Bool("server_offline_notify") {
			t.Fatal("掉线通知默认应开启")
		}
		var job func(context.Context) error
		for _, j := range e.Jobs() {
			if j.Name == "check_server" {
				job = j.Run
			}
		}
		srv := &model.ServerTrojan{ServerBase: model.ServerBase{GroupID: model.IntList{1}, RouteID: model.IntList{}, Name: "n",
			Host: "n.example.com", Port: "443", ServerPort: 443, Tags: model.StrList{}, Rate: "1", Show: 1}}
		e.create(t, srv)
		key := e.Cache.KeyOf(cache.KeyServerLastCheckAt, cache.ServerKey(model.ServerTypeTrojan, srv.ID))
		if err := e.Cache.SetJSON(ctx, key, e.at.Unix()-3600, time.Hour); err != nil {
			t.Fatal(err)
		}

		e.set(t, map[string]any{"server_offline_notify": 0})
		if err := job(ctx); err != nil || len(e.notify.sent) != 0 {
			t.Fatalf("关闭后不应检查与通知：%v %+v", err, e.notify.sent)
		}
		if n, _ := e.Cache.Exists(ctx, key).Result(); n != 1 {
			t.Fatal("关闭时不应清除节点心跳")
		}
		e.set(t, map[string]any{"server_offline_notify": 1})
		if err := job(ctx); err != nil || len(e.notify.sent) != 1 {
			t.Fatalf("开启后应通知：%v %+v", err, e.notify.sent)
		}
	})
}
