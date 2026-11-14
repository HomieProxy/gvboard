package hook

import (
	"context"
	"testing"
)

type fakeHooks struct {
	events []string
	filter func(v any) any
}

func (f *fakeHooks) Emit(_ context.Context, event string, _ any) { f.events = append(f.events, event) }

func (f *fakeHooks) Filter(_ context.Context, _ string, v any, check func(old, new any) error) any {
	out := f.filter(v)
	if check(v, out) != nil {
		return v
	}
	return out
}

func TestNilHooks(t *testing.T) {
	ctx := context.Background()
	Emit(nil, ctx, EventUserLogin, User{UserID: 1})
	v := OrderAmount{Amount: 100}
	if got := Apply(nil, ctx, FilterOrderAmount, v); got != v {
		t.Fatalf("Hooks 为 nil 时应原样返回，实际 %+v", got)
	}
}

func TestApply(t *testing.T) {
	ctx := context.Background()
	h := &fakeHooks{}
	Emit(h, ctx, EventOrderPaid, Order{})
	if len(h.events) != 1 || h.events[0] != EventOrderPaid {
		t.Fatalf("事件未转发：%v", h.events)
	}

	h.filter = func(v any) any { o := v.(OrderAmount); o.Amount /= 2; return o }
	if got := Apply(h, ctx, FilterOrderAmount, OrderAmount{Amount: 100}); got.Amount != 50 {
		t.Fatalf("过滤结果错误：%+v", got)
	}
	h.filter = func(v any) any { o := v.(OrderAmount); o.Amount = -1; return o }
	if got := Apply(h, ctx, FilterOrderAmount, OrderAmount{Amount: 100}); got.Amount != 100 {
		t.Fatalf("负数金额应被拒绝：%+v", got)
	}
	h.filter = func(v any) any { return "x" }
	if got := Apply(h, ctx, FilterOrderAmount, OrderAmount{Amount: 100}); got.Amount != 100 {
		t.Fatalf("类型错误的结果应被拒绝：%+v", got)
	}

	h.filter = func(v any) any { m := v.(MailMessage); m.To = "b@x.com"; return m }
	if got := Apply(h, ctx, FilterMailMessage, MailMessage{To: "a@x.com", Subject: "s", HTML: "h"}); got.To != "a@x.com" {
		t.Fatalf("不能修改收件人：%+v", got)
	}
	h.filter = func(v any) any { m := v.(MailMessage); m.HTML = ""; return m }
	if got := Apply(h, ctx, FilterMailMessage, MailMessage{To: "a@x.com", Subject: "s", HTML: "h"}); got.HTML != "h" {
		t.Fatalf("内容不能为空：%+v", got)
	}
	h.filter = func(v any) any { s := v.(SubscribeServers); s.Servers = append(s.Servers, nil); return s }
	if got := Apply(h, ctx, FilterSubscribeServers, SubscribeServers{}); len(got.Servers) != 0 {
		t.Fatalf("nil 节点应被拒绝：%+v", got)
	}
}
