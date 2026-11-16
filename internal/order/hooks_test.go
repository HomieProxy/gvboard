package order

import (
	"context"
	"sync"
	"testing"
	"time"

	"gvboard/internal/hook"
	"gvboard/internal/model"
)

type recHooks struct {
	mu     sync.Mutex
	events []string
	data   []any
	filter func(v any) any
}

func (h *recHooks) Emit(_ context.Context, event string, data any) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.events = append(h.events, event)
	h.data = append(h.data, data)
}

func (h *recHooks) Filter(_ context.Context, _ string, v any, clone func(any) any, check func(hook.Source, any, any) (any, error)) any {
	if h.filter == nil {
		return v
	}
	in := v
	if clone != nil {
		in = clone(v)
	}
	out, err := check(hook.Source{Code: "rec", Name: "测试插件"}, v, h.filter(in))
	if err != nil {
		return v
	}
	return out
}

func (h *recHooks) take() []string {
	h.mu.Lock()
	defer h.mu.Unlock()
	out := h.events
	h.events, h.data = nil, nil
	return out
}

func testHooks(t *testing.T, e *env) {
	ctx := context.Background()
	h := &recHooks{}
	e.Hooks = h
	u := e.user(t, "h@x.com", 0)
	p := e.plan(t, "A", nil)

	h.filter = func(v any) any { a := v.(hook.OrderAmount); a.Amount /= 2; return a }
	no, err := e.Create(ctx, u.ID, CreateInput{PlanID: p.ID, Period: PeriodMonth})
	if err != nil {
		t.Fatal(err)
	}
	if o := e.order(t, no); o.TotalAmount != 500 {
		t.Fatalf("过滤器应把金额改为 500，实际 %d", o.TotalAmount)
	}
	if ev := h.take(); len(ev) != 1 || ev[0] != hook.EventOrderCreated {
		t.Fatalf("下单事件错误：%v", ev)
	}
	if err := e.Paid(ctx, no, "cb-h"); err != nil {
		t.Fatal(err)
	}
	if err := e.Handle(ctx, no); err != nil {
		t.Fatal(err)
	}
	ev := h.take()
	if len(ev) != 3 || ev[0] != hook.EventOrderPaid || ev[1] != hook.EventOrderOpened || ev[2] != hook.EventSubscriptionChanged {
		t.Fatalf("支付与开通事件错误：%v", ev)
	}

	// 未通过校验的结果被忽略
	h.filter = func(v any) any { a := v.(hook.OrderAmount); a.Amount = -1; return a }
	no, err = e.Create(ctx, u.ID, CreateInput{PlanID: p.ID, Period: PeriodMonth})
	if err != nil {
		t.Fatal(err)
	}
	if o := e.order(t, no); o.TotalAmount != 1000 {
		t.Fatalf("负数金额应被忽略，实际 %d", o.TotalAmount)
	}
	if err := e.Cancel(ctx, u.ID, no); err != nil {
		t.Fatal(err)
	}
	if ev := h.take(); len(ev) != 2 || ev[1] != hook.EventOrderCancelled {
		t.Fatalf("取消事件错误：%v", ev)
	}
}

// testPreview 结算试算与下单金额一致、不产生副作用；插件调整明细随订单保存；套餐标签。
func testPreview(t *testing.T, e *env) {
	ctx := context.Background()
	h := &recHooks{}
	e.Hooks = h
	u := e.user(t, "pv@x.com", 300)
	e.DB.Model(u).Update("discount", 10)
	p := e.plan(t, "A", nil)
	e.DB.Create(&model.Coupon{Code: "PV20", Name: "x", Type: CouponPercent, Value: 20, Show: 1, LimitUse: ptr(int64(5)),
		StartedAt: time.Now().Unix() - 60, EndedAt: time.Now().Unix() + 3600})
	in := CreateInput{PlanID: p.ID, Period: PeriodMonth, CouponCode: "PV20"}

	var badgeUser int64
	h.filter = func(v any) any {
		switch a := v.(type) {
		case hook.OrderAmount:
			a.Amount -= 100
			a.Items = append(a.Items, hook.AmountItem{Label: "首单优惠", Amount: -100})
			return a
		case hook.PlanBadges:
			badgeUser = a.UserID
			for i := range a.Plans {
				a.Plans[i].Badges = append(a.Plans[i].Badges, "首单立减")
			}
			return a
		}
		return v
	}

	// 原价 1000，优惠券 200，会员折扣按原价 10% = 100，插件 -100，余额抵扣 300，应付 300
	q, err := e.Preview(ctx, u.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	if q.Price != 1000 || q.Coupon != 200 || q.UserDiscount != 100 || q.Balance != 300 || q.Total != 300 ||
		len(q.Items) != 1 || q.Items[0] != (hook.AmountItem{Label: "首单优惠", Amount: -100, Plugin: "rec"}) {
		t.Fatalf("试算结果错误：%+v", q)
	}
	var c model.Coupon
	e.DB.Where("code = ?", "PV20").Take(&c)
	var n int64
	e.DB.Model(&model.Order{}).Where("user_id = ?", u.ID).Count(&n)
	if *c.LimitUse != 5 || e.balance(t, u.ID) != 300 || n != 0 || len(h.take()) != 0 {
		t.Fatalf("试算不应扣减优惠券次数与余额、不应创建订单与发出事件：%d %d %d", *c.LimitUse, e.balance(t, u.ID), n)
	}

	no, err := e.Create(ctx, u.ID, in)
	if err != nil {
		t.Fatal(err)
	}
	o := e.order(t, no)
	if o.TotalAmount != q.Total || val(o.BalanceAmount) != q.Balance || val(o.DiscountAmount) != q.Coupon+q.UserDiscount+100 {
		t.Fatalf("下单金额应与试算一致：%+v %+v", o, q)
	}
	d, err := e.Detail(ctx, u.ID, no)
	if err != nil || len(d.Adjustments) != 1 || d.Adjustments[0].Label != "首单优惠" || d.Adjustments[0].Amount != -100 || d.Adjustments[0].Plugin != "rec" {
		t.Fatalf("订单详情应返回调整明细：%+v %v", d, err)
	}
	ad, err := e.AdminDetail(ctx, no)
	if err != nil || len(ad.Adjustments) != 1 {
		t.Fatalf("管理端订单详情应返回调整明细：%v", err)
	}
	if err := e.Cancel(ctx, u.ID, no); err != nil {
		t.Fatal(err)
	}

	// 修改金额但没有明细时以插件名称生成；明细合计与金额变化不一致时忽略修改
	in.CouponCode = ""
	h.filter = func(v any) any { a := v.(hook.OrderAmount); a.Amount -= 50; return a }
	if q, err := e.Preview(ctx, u.ID, in); err != nil || len(q.Items) != 1 || q.Items[0].Label != "测试插件" || q.Items[0].Amount != -50 {
		t.Fatalf("应自动生成调整明细：%+v %v", q, err)
	}
	h.filter = func(v any) any {
		a := v.(hook.OrderAmount)
		a.Amount -= 50
		a.Items = append(a.Items, hook.AmountItem{Label: "不一致", Amount: -10})
		return a
	}
	if q, err := e.Preview(ctx, u.ID, in); err != nil || len(q.Items) != 0 || q.Total != 1000-100-300 {
		t.Fatalf("明细合计不一致时应忽略插件的修改：%+v %v", q, err)
	}

	// 套餐标签
	h.filter = func(v any) any {
		a := v.(hook.PlanBadges)
		badgeUser = a.UserID
		for i := range a.Plans {
			a.Plans[i].Badges = append(a.Plans[i].Badges, "首单立减")
		}
		return a
	}
	list, err := e.UserPlans(ctx, u.ID)
	if err != nil || len(list) == 0 || badgeUser != u.ID {
		t.Fatalf("套餐列表错误：%v", err)
	}
	for _, v := range list {
		if len(v.Badges) != 1 || v.Badges[0] != "首单立减" {
			t.Fatalf("套餐应带有插件标签：%+v", v.Badges)
		}
	}
	if v, err := e.UserPlan(ctx, u.ID, p.ID); err != nil || len(v.Badges) != 1 {
		t.Fatalf("套餐详情应带有插件标签：%+v %v", v, err)
	}
}
