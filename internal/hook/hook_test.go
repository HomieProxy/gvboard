package hook

import (
	"context"
	"slices"
	"strings"
	"testing"
)

type fakeHooks struct {
	events []string
	filter func(v any) any
}

func (f *fakeHooks) Emit(_ context.Context, event string, _ any) { f.events = append(f.events, event) }

func (f *fakeHooks) Filter(_ context.Context, _ string, v any, clone func(any) any, check func(Source, any, any) (any, error)) any {
	in := v
	if clone != nil {
		in = clone(v)
	}
	out, err := check(Source{Code: "fake", Name: "测试插件"}, v, f.filter(in))
	if err != nil {
		return v
	}
	return out
}

func TestNilHooks(t *testing.T) {
	ctx := context.Background()
	Emit(nil, ctx, EventUserLogin, User{UserID: 1})
	v := OrderAmount{Amount: 100}
	if got := Apply(nil, ctx, FilterOrderAmount, v); got.Amount != 100 || got.Items != nil {
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

func TestOrderAmountItems(t *testing.T) {
	ctx := context.Background()
	prev := []AmountItem{{Label: "活动", Amount: -10, Plugin: "other"}}
	base := OrderAmount{UserID: 1, PlanID: 2, Amount: 100, Items: prev}
	h := &fakeHooks{}
	apply := func(f func(o OrderAmount) OrderAmount) OrderAmount {
		h.filter = func(v any) any { return f(v.(OrderAmount)) }
		return Apply(h, ctx, FilterOrderAmount, base)
	}

	got := apply(func(o OrderAmount) OrderAmount {
		o.Amount -= 30
		o.Items = append(o.Items, AmountItem{Label: "首单优惠", Amount: -20}, AmountItem{Label: "满减", Amount: -10, Plugin: "伪造"})
		return o
	})
	want := []AmountItem{prev[0], {Label: "首单优惠", Amount: -20, Plugin: "fake"}, {Label: "满减", Amount: -10, Plugin: "fake"}}
	if got.Amount != 70 || !slices.Equal(got.Items, want) {
		t.Fatalf("明细应追加并由系统填写插件标识：%+v", got)
	}
	if got := apply(func(o OrderAmount) OrderAmount { o.Amount += 5; return o }); got.Amount != 105 ||
		len(got.Items) != 2 || got.Items[1] != (AmountItem{Label: "测试插件", Amount: 5, Plugin: "fake"}) {
		t.Fatalf("未写明细时应以插件名称生成：%+v", got)
	}
	if got := apply(func(o OrderAmount) OrderAmount { o.UserID, o.PlanID, o.Preview = 9, 9, true; return o }); got.UserID != 1 || got.PlanID != 2 || got.Preview {
		t.Fatalf("只应采用金额与明细：%+v", got)
	}

	for name, f := range map[string]func(o OrderAmount) OrderAmount{
		"合计不一致": func(o OrderAmount) OrderAmount {
			o.Amount -= 30
			o.Items = append(o.Items, AmountItem{Label: "x", Amount: -20})
			return o
		},
		"修改其它插件明细": func(o OrderAmount) OrderAmount { o.Items[0].Amount = -50; return o },
		"删除其它插件明细": func(o OrderAmount) OrderAmount { o.Items = nil; return o },
		"名称为空": func(o OrderAmount) OrderAmount {
			o.Amount--
			o.Items = append(o.Items, AmountItem{Amount: -1})
			return o
		},
		"名称过长": func(o OrderAmount) OrderAmount {
			o.Amount--
			o.Items = append(o.Items, AmountItem{Label: strings.Repeat("长", 33), Amount: -1})
			return o
		},
		"金额为 0": func(o OrderAmount) OrderAmount { o.Items = append(o.Items, AmountItem{Label: "x"}); return o },
		"超过 3 条": func(o OrderAmount) OrderAmount {
			for range 4 {
				o.Amount--
				o.Items = append(o.Items, AmountItem{Label: "x", Amount: -1})
			}
			return o
		},
	} {
		if got := apply(f); got.Amount != 100 || !slices.Equal(got.Items, prev) {
			t.Errorf("%s 时应忽略修改：%+v", name, got)
		}
	}
	if base.Items[0].Amount != -10 {
		t.Fatal("插件原地修改不应影响原值")
	}
}

func TestPlanBadges(t *testing.T) {
	ctx := context.Background()
	base := PlanBadges{UserID: 1, Plans: []PlanBadge{{PlanID: 1, Badges: []string{"热卖"}}, {PlanID: 2, Badges: []string{}}}}
	h := &fakeHooks{}
	apply := func(f func(v PlanBadges) PlanBadges) PlanBadges {
		h.filter = func(v any) any { return f(v.(PlanBadges)) }
		return Apply(h, ctx, FilterPlanBadges, base)
	}
	got := apply(func(v PlanBadges) PlanBadges {
		v.Plans[1].Badges = append(v.Plans[1].Badges, "首单立减 20%")
		return v
	})
	if len(got.Plans) != 2 || !slices.Equal(got.Plans[0].Badges, []string{"热卖"}) || !slices.Equal(got.Plans[1].Badges, []string{"首单立减 20%"}) {
		t.Fatalf("标签追加错误：%+v", got)
	}
	if len(base.Plans[1].Badges) != 0 {
		t.Fatal("插件原地修改不应影响原值")
	}
	for name, f := range map[string]func(v PlanBadges) PlanBadges{
		"增减套餐":   func(v PlanBadges) PlanBadges { v.Plans = v.Plans[:1]; return v },
		"修改套餐":   func(v PlanBadges) PlanBadges { v.Plans[0].PlanID = 3; return v },
		"修改已有标签": func(v PlanBadges) PlanBadges { v.Plans[0].Badges[0] = "x"; return v },
		"标签为空":   func(v PlanBadges) PlanBadges { v.Plans[1].Badges = []string{""}; return v },
		"标签过长":   func(v PlanBadges) PlanBadges { v.Plans[1].Badges = []string{strings.Repeat("长", 17)}; return v },
		"超过 3 个": func(v PlanBadges) PlanBadges {
			v.Plans[0].Badges = append(v.Plans[0].Badges, "a", "b", "c")
			return v
		},
	} {
		if got := apply(f); got.Plans[0].PlanID != 1 || !slices.Equal(got.Plans[0].Badges, []string{"热卖"}) || len(got.Plans[1].Badges) != 0 {
			t.Errorf("%s 时应忽略修改：%+v", name, got)
		}
	}
}

func TestKnowledgeArticle(t *testing.T) {
	ctx := context.Background()
	base := KnowledgeArticle{UserID: 1, ArticleID: 2, Locale: "zh-CN", Available: true, Body: "a{apple_id}b"}
	h := &fakeHooks{}
	apply := func(f func(v KnowledgeArticle) KnowledgeArticle) KnowledgeArticle {
		h.filter = func(v any) any { return f(v.(KnowledgeArticle)) }
		return Apply(h, ctx, FilterKnowledgeArticle, base)
	}
	got := apply(func(v KnowledgeArticle) KnowledgeArticle {
		v.Body = strings.ReplaceAll(v.Body, "{apple_id}", "<b>x</b>")
		v.UserID, v.ArticleID, v.Locale, v.Available = 9, 9, "en-US", false
		return v
	})
	if want := (KnowledgeArticle{UserID: 1, ArticleID: 2, Locale: "zh-CN", Available: true, Body: "a<b>x</b>b"}); got != want {
		t.Fatalf("只能修改正文：%+v", got)
	}
	for name, f := range map[string]func(v KnowledgeArticle) KnowledgeArticle{
		"清空正文": func(v KnowledgeArticle) KnowledgeArticle { v.Body = ""; return v },
		"超过上限": func(v KnowledgeArticle) KnowledgeArticle { v.Body = strings.Repeat("x", maxArticleBody+1); return v },
	} {
		if got := apply(f); got != base {
			t.Errorf("%s 时应忽略修改：%.40s", name, got.Body)
		}
	}
	if got := apply(func(v KnowledgeArticle) KnowledgeArticle { v.Body = strings.Repeat("x", maxArticleBody); return v }); len(got.Body) != maxArticleBody {
		t.Fatal("正好 1 MB 应允许")
	}
}
