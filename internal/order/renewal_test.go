package order

import (
	"context"
	"fmt"
	"testing"
	"time"

	"gvboard/internal/model"
	"gvboard/internal/testutil"
)

func TestAutoRenew(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) { testAutoRenew(t, newEnv(t, driver)) })
	}
}

func testAutoRenew(t *testing.T, e *env) {
	ctx := context.Background()
	now := time.Date(2026, 11, 14, 22, 30, 0, 0, time.Local)
	e.Now = func() time.Time { return now }
	soon := now.Unix() + 86400
	plan := e.plan(t, "月付", nil)
	noRenew := e.plan(t, "不可续费", func(p *model.Plan) { p.Renew = 0 })
	other := e.plan(t, "其他", nil)

	seq := 0
	// sub 设置订阅的套餐、到期与自动续费；orders 为该订阅已完成的历史订单（周期、套餐），按顺序创建。
	setup := func(sub *model.Subscription, p *model.Plan, expired int64, auto int, orders ...[2]any) {
		t.Helper()
		if err := e.DB.Model(sub).Updates(map[string]any{"plan_id": p.ID, "expired_at": expired, "auto_renewal": auto}).Error; err != nil {
			t.Fatal(err)
		}
		for i, o := range orders {
			seq++
			sid := sub.ID
			err := e.DB.Create(&model.Order{
				UserID: sub.UserID, SubscriptionID: &sid, PlanID: o[1].(*model.Plan).ID, Period: o[0].(string),
				TradeNo: fmt.Sprintf("RENEW%d", seq), Status: model.OrderStatusCompleted,
				TotalAmount: 1000, CreatedAt: now.Unix() - int64(100*(len(orders)-i)),
			}).Error
			if err != nil {
				t.Fatal(err)
			}
		}
	}
	primary := func(u *model.User) *model.Subscription {
		var s model.Subscription
		if err := e.DB.Where("user_id = ? AND is_primary = 1", u.ID).Take(&s).Error; err != nil {
			t.Fatal(err)
		}
		return &s
	}

	a := e.user(t, "a@x.com", 5000)
	aSub := primary(a)
	setup(aSub, plan, soon, 1, [2]any{PeriodMonth, plan})
	aSub2 := &model.Subscription{UserID: a.ID, Name: "s2", UUID: "uuid-renew-2", Token: "token-renew-2"}
	e.DB.Create(aSub2)
	setup(aSub2, plan, soon, 1, [2]any{PeriodQuarter, plan}) // 套餐未开放季付
	b := e.user(t, "b@x.com", 100)
	setup(primary(b), plan, soon, 1, [2]any{PeriodMonth, plan})
	c := e.user(t, "c@x.com", 5000)
	setup(primary(c), noRenew, soon, 1, [2]any{PeriodMonth, noRenew})
	d := e.user(t, "d@x.com", 5000)
	setup(primary(d), plan, soon, 1, [2]any{PeriodMonth, other}) // 订阅已更换套餐
	ee := e.user(t, "e@x.com", 5000)
	setup(primary(ee), plan, soon, 1)
	f := e.user(t, "f@x.com", 5000)
	setup(primary(f), plan, now.Unix()+3*86400, 1, [2]any{PeriodMonth, plan})
	g := e.user(t, "g@x.com", 5000)
	setup(primary(g), plan, soon, 0, [2]any{PeriodMonth, plan})
	h := e.user(t, "h@x.com", 5000)
	setup(primary(h), plan, soon, 1, [2]any{PeriodMonth, plan}, [2]any{PeriodReset, plan}) // 最近一单为重置包

	renewed, failed, err := e.AutoRenew(ctx)
	if err != nil || renewed != 2 || failed != 5 {
		t.Fatalf("续费结果错误：成功 %d 失败 %d %v", renewed, failed, err)
	}
	wantExp := time.Unix(soon, 0).AddDate(0, 1, 0).Unix()
	for _, u := range []*model.User{a, h} {
		s := e.sub(t, primary(u).ID)
		if *s.ExpiredAt != wantExp || s.AutoRenewal != 1 || e.balance(t, u.ID) != 4000 {
			t.Fatalf("%s 续费后状态错误：到期 %d 自动续费 %d 余额 %d", u.Email, *s.ExpiredAt, s.AutoRenewal, e.balance(t, u.ID))
		}
		var o model.Order
		e.DB.Where("subscription_id = ? AND type = ?", s.ID, model.OrderTypeRenew).Take(&o)
		if o.Status != model.OrderStatusCompleted || o.TotalAmount != 0 || o.BalanceAmount == nil || *o.BalanceAmount != 1000 ||
			o.Period != PeriodMonth || o.PlanID != plan.ID || o.UserID != u.ID {
			t.Fatalf("%s 续费订单错误：%+v", u.Email, o)
		}
	}
	for _, sub := range []*model.Subscription{aSub2, primary(b), primary(c), primary(d), primary(ee)} {
		s := e.sub(t, sub.ID)
		if s.AutoRenewal != 0 || *s.ExpiredAt != soon {
			t.Fatalf("续费失败的订阅 %d 应关闭自动续费且到期不变：%d %d", s.ID, s.AutoRenewal, *s.ExpiredAt)
		}
	}
	if e.balance(t, b.ID) != 100 || e.balance(t, c.ID) != 5000 || e.balance(t, d.ID) != 5000 {
		t.Fatal("续费失败不应扣余额")
	}
	for _, u := range []*model.User{f, g} {
		s := e.sub(t, primary(u).ID)
		var n int64
		e.DB.Model(&model.Order{}).Where("user_id = ? AND type = ?", u.ID, model.OrderTypeRenew).Count(&n)
		if n != 0 || e.balance(t, u.ID) != 5000 || (u == f && s.AutoRenewal != 1) {
			t.Fatalf("%s 不应处理", u.Email)
		}
	}

	if renewed, failed, err = e.AutoRenew(ctx); err != nil || renewed != 0 || failed != 0 {
		t.Fatalf("再次执行不应重复续费：%d %d %v", renewed, failed, err)
	}
	if e.balance(t, a.ID) != 4000 {
		t.Fatal("不应重复扣费")
	}
}
