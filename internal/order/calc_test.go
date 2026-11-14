package order

import (
	"math"
	"testing"
	"time"
	_ "time/tzdata"

	"gvboard/internal/model"
)

// 以下预期值均由 PHP 8.2 执行原项目 OrderService 中的同名计算得到（时区 Asia/Shanghai）。

func useShanghai(t *testing.T) {
	t.Helper()
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Fatal(err)
	}
	old := time.Local
	time.Local = loc
	t.Cleanup(func() { time.Local = old })
}

func TestAddMonthsMatchesPHP(t *testing.T) {
	useShanghai(t)
	cases := [][3]int64{
		{1706666400, 1, 1709344800}, {1675130400, 1, 1677808800}, {1709136000, 12, 1740758400}, {1711900799, 1, 1714579199},
		{1725062400, 6, 1740960000}, {1735617600, 2, 1740974400}, {1717084800, 24, 1780156800}, {1730304000, 36, 1824912000},
	}
	for _, c := range cases {
		if got := addMonths(c[0], int(c[1])); got != c[2] {
			t.Errorf("addMonths(%d, %d) = %d，PHP 为 %d", c[0], c[1], got, c[2])
		}
	}
	now := int64(1731470400)
	if v := expireAfter(PeriodMonth, nil, now); v == nil || *v != addMonths(now, 1) {
		t.Error("长期有效转周期应从现在起算")
	}
	past := now - 100
	if v := expireAfter(PeriodQuarter, &past, now); *v != addMonths(now, 3) {
		t.Error("已到期应从现在起算")
	}
	future := now + 100
	if v := expireAfter(PeriodYear, &future, now); *v != addMonths(future, 12) {
		t.Error("未到期应在原到期时间上延长")
	}
}

func TestDepositBonusMatchesPHP(t *testing.T) {
	tiers := []string{"100:10", "50:5.5", "0.29:0.01", "abc"}
	for _, c := range [][2]int64{{0, 0}, {28, 1}, {29, 1}, {4999, 1}, {5000, 550}, {9999, 550}, {10000, 1000}, {20000, 1000}} {
		if got := DepositBonus(tiers, c[0]); got != c[1] {
			t.Errorf("充值 %d 赠送 %d，PHP 为 %d", c[0], got, c[1])
		}
	}
	if DepositBonus(nil, 10000) != 0 {
		t.Error("未设置赠送时应为 0")
	}
}

func ord(id int64, period string, created, total, balance, surplus, refund int64) model.Order {
	return model.Order{ID: id, Period: period, CreatedAt: created, TotalAmount: total,
		BalanceAmount: &balance, SurplusAmount: &surplus, RefundAmount: &refund}
}

func TestSurplusByPeriodMatchesPHP(t *testing.T) {
	useShanghai(t)
	const now, g = int64(1731470400), int64(gb)
	sub := func(exp, transfer, u, d int64) *model.Subscription {
		return &model.Subscription{ExpiredAt: &exp, TransferEnable: transfer, U: u, D: d}
	}
	cases := []struct {
		name   string
		sub    *model.Subscription
		orders []model.Order
		want   *float64
	}{
		{"一个月", sub(1733198400, 100*g, 3*g, 7*g), []model.Order{ord(1, PeriodMonth, 1730606400, 1000, 0, 0, 0)}, ptr(444.4444444444444)},
		{"一年（含已过期订单与折抵、退款）", sub(1759550400, 100*g, 0, 90*g),
			[]model.Order{ord(1, PeriodMonth, 1722830400, 999, 0, 0, 0), ord(2, PeriodYear, 1728014400, 12000, 500, 300, 100)}, ptr(10542.739726027397)},
		{"一年流量充足", sub(1759550400, 100*g, 0, 5*g), []model.Order{ord(1, PeriodYear, 1728014400, 12000, 0, 0, 0)}, ptr(10684.931506849314)},
		{"续费过", sub(1740369600, 300*g, 1*g, 2*g),
			[]model.Order{ord(1, PeriodQuarter, 1729742400, 2700, 300, 0, 0), ord(2, PeriodMonth, 1731038400, 1000, 0, 0, 0)}, ptr(3433.3333333333335)},
		{"流量超用", sub(1733198400, 10*g, 6*g, 6*g), []model.Order{ord(1, PeriodMonth, 1730606400, 1000, 0, 0, 0)}, ptr(0.0)},
		{"订单都已过期", sub(1732334400, 10*g, 0, 0), []model.Order{ord(1, PeriodMonth, 1728014400, 1000, 0, 0, 0)}, nil},
		{"订阅已到期", sub(1731470399, 10*g, 0, 0), []model.Order{ord(1, PeriodMonth, 1730606400, 1000, 0, 0, 0)}, nil},
	}
	for _, c := range cases {
		got, ids := surplusByPeriod(c.sub, c.orders, now)
		if (got == nil) != (c.want == nil) || (got != nil && math.Abs(*got-*c.want) > 1e-6) {
			t.Errorf("%s: 剩余价值 %v，PHP 为 %v", c.name, deref(got), deref(c.want))
		}
		if len(ids) != len(c.orders) {
			t.Errorf("%s: 参与折抵的订单应包含全部已完成周期订单，实际 %v", c.name, ids)
		}
	}
}

func TestSurplusByOneTimeMatchesPHP(t *testing.T) {
	const g = int64(gb)
	cases := []struct {
		transfer, u, d, total, balance int64
		want                           *float64
	}{
		{50 * g, 5 * g, 15 * g, 3000, 500, ptr(2100.0)},
		{50 * g, 40 * g, 15 * g, 3000, 0, ptr(0.0)},
		{0, 0, 0, 3000, 0, nil},
		{7 * g, 123456789, 0, 0, 0, nil},
		{7 * g, 123456789, 987654, 1999, 1, ptr(1966.8863088424716)},
	}
	for i, c := range cases {
		got := surplusByOneTime(&model.Subscription{TransferEnable: c.transfer, U: c.u, D: c.d},
			&model.Order{TotalAmount: c.total, BalanceAmount: &c.balance})
		if (got == nil) != (c.want == nil) || (got != nil && math.Abs(*got-*c.want) > 1e-6) {
			t.Errorf("第 %d 组: 剩余价值 %v，PHP 为 %v", i, deref(got), deref(c.want))
		}
	}
	if surplusByOneTime(&model.Subscription{TransferEnable: gb}, nil) != nil {
		t.Error("没有一次性订单时不折抵")
	}
}

func ptr[T any](v T) *T { return &v }

func deref(p *float64) any {
	if p == nil {
		return nil
	}
	return *p
}
