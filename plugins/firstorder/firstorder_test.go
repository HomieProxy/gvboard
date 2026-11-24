package firstorder

import (
	"testing"

	sdk "gvboard/pkg/plugin"
)

func TestParseRule(t *testing.T) {
	r, err := parseRule(sdk.Config{"percent": float64(20), "max_off": float64(500), "plan_ids": " 1, 3 ,"})
	if err != nil {
		t.Fatal(err)
	}
	if r.percent != 20 || r.maxOff != 500 || len(r.plans) != 2 || !r.applies(3) || r.applies(2) {
		t.Fatalf("规则解析错误：%+v", r)
	}
	if r, _ := parseRule(sdk.Config{"percent": "50"}); !r.applies(99) {
		t.Fatal("未填写套餐 ID 时应适用全部套餐")
	}
	if r, _ := parseRule(sdk.Config{"percent": float64(20), "limit_minutes": float64(30)}); r.limit != 30 {
		t.Fatalf("限时解析错误：%+v", r)
	}
	for _, cfg := range []sdk.Config{
		{"percent": float64(0)},
		{"percent": float64(101)},
		{"percent": float64(10), "max_off": float64(-1)},
		{"percent": float64(10), "plan_ids": "1,a"},
		{"percent": float64(10), "limit_minutes": float64(-1)},
		{"percent": float64(10), "limit_minutes": float64(maxLimitMinutes + 1)},
	} {
		if _, err := parseRule(cfg); err == nil {
			t.Fatalf("配置 %v 应校验失败", cfg)
		}
	}
}

func TestDiscount(t *testing.T) {
	cases := []struct {
		r      rule
		amount int64
		off    int64
	}{
		{rule{percent: 20}, 1000, 200},
		{rule{percent: 20}, 999, 199},
		{rule{percent: 20, maxOff: 150}, 1000, 150},
		{rule{percent: 100}, 1000, 1000},
		{rule{percent: 100, maxOff: 5000}, 1000, 1000},
	}
	for _, c := range cases {
		if got := c.r.discount(c.amount); got != c.off {
			t.Fatalf("%+v 金额 %d：减免 %d，应为 %d", c.r, c.amount, got, c.off)
		}
	}
}

func TestBadgeAndWidget(t *testing.T) {
	cases := []struct {
		r            rule
		badge, value string
		text         string
	}{
		{rule{percent: 20}, "首单立减 20%", "-20%", "首次购买套餐立减 20%，下单时自动抵扣"},
		{rule{percent: 30, maxOff: 1050, plans: []int64{1}}, "首单立减 30%", "-30%", "首次购买套餐立减 30%，最多减免 10.50（限指定套餐），下单时自动抵扣"},
		{rule{percent: 100}, "首单免费", "免费", "首次购买套餐免费，下单时自动抵扣"},
	}
	for _, c := range cases {
		w := c.r.widget()
		if c.r.badge() != c.badge || w.Value != c.value || w.Text != c.text || w.Hidden {
			t.Fatalf("%+v：标签 %q，卡片 %+v", c.r, c.r.badge(), w)
		}
		if len(w.Actions) != 1 || w.Actions[0].Link != "/plans" || w.Actions[0].Path != "" {
			t.Fatalf("卡片按钮应跳转到套餐页：%+v", w.Actions)
		}
	}
}

func TestDeadline(t *testing.T) {
	if d := (rule{percent: 20}).deadline(1000); d != 0 || !open(d, 1<<40) {
		t.Fatalf("不限时应始终可享受：%d", d)
	}
	d := rule{percent: 20, limit: 30}.deadline(1000)
	if d != 1000+30*60 {
		t.Fatalf("截止时间 %d", d)
	}
	if !open(d, d-1) || open(d, d) || open(d, d+1) {
		t.Fatal("截止时间之前可享受，到达截止时间后失效")
	}
}

func TestBanner(t *testing.T) {
	b := rule{percent: 20, limit: 30}.banner(12345)
	if b.Hidden || b.Dismissible || b.EndsAt != 12345 || b.Tone != sdk.ToneWarning {
		t.Fatalf("横幅应醒目、带截止时间且不可关闭：%+v", b)
	}
	if b.Text != "新用户限时福利：首次购买套餐立减 20%，到期后失效" {
		t.Fatalf("横幅文字 %q", b.Text)
	}
	if b.Action == nil || b.Action.Link != "/plans" {
		t.Fatalf("横幅按钮应跳转到套餐页：%+v", b.Action)
	}
	b = rule{percent: 20}.banner(0)
	if b.Hidden || b.EndsAt != 0 || b.Text != "新用户专享：首次购买套餐立减 20%，下单时自动抵扣" || b.Action == nil {
		t.Fatalf("不限时横幅应一直显示且没有倒计时：%+v", b)
	}
}
