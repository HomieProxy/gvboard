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
	for _, cfg := range []sdk.Config{
		{"percent": float64(0)},
		{"percent": float64(101)},
		{"percent": float64(10), "max_off": float64(-1)},
		{"percent": float64(10), "plan_ids": "1,a"},
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
