package order

import (
	"context"
	"sync"
	"testing"

	"gvboard/internal/hook"
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

func (h *recHooks) Filter(_ context.Context, _ string, v any, check func(old, new any) error) any {
	if h.filter == nil {
		return v
	}
	out := h.filter(v)
	if check(v, out) != nil {
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
