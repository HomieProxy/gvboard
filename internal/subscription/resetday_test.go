package subscription

import (
	"testing"
	"time"

	"gvboard/internal/model"
)

// 期望值由 PHP 8.2 运行原项目 UserService 的算法得到（时区 Asia/Shanghai）：
// {当前时间, 到期时间, 每月1日, 按到期日每月, 每年1月1日, 按到期日每年}
var phpResetDays = [][6]int64{
	{1769824800, 1801353600, 0, 0, 334, 364},
	{1769824800, 1773504000, 0, 15, 334, 42},
	{1769824800, 1709179200, 0, 29, 334, 28},
	{1769824800, 1815580800, 0, 15, 334, 164},
	{1769824800, 1785546000, 0, 1, 334, 181},
	{1769824800, 1830268799, 0, 0, 334, 333},
	{1772294399, 1801353600, 0, 0, 306, 336},
	{1772294399, 1773504000, 0, 15, 306, 14},
	{1772294399, 1709179200, 0, 0, 306, 0},
	{1772294399, 1815580800, 0, 15, 306, 136},
	{1772294399, 1785546000, 0, 1, 306, 153},
	{1772294399, 1830268799, 0, 0, 306, 305},
	{1835409600, 1801353600, 0, 0, 306, 336},
	{1835409600, 1773504000, 0, 15, 306, 14},
	{1835409600, 1709179200, 0, 0, 306, 365},
	{1835409600, 1815580800, 0, 15, 306, 136},
	{1835409600, 1785546000, 0, 1, 306, 153},
	{1835409600, 1830268799, 0, 0, 306, 305},
	{1798729200, 1801353600, 0, 0, 0, 30},
	{1798729200, 1773504000, 0, 15, 0, 73},
	{1798729200, 1709179200, 0, 29, 0, 59},
	{1798729200, 1815580800, 0, 15, 0, 195},
	{1798729200, 1785546000, 0, 1, 0, 212},
	{1798729200, 1830268799, 0, 0, 0, 364},
	{1784044800, 1801353600, 16, 16, 170, 200},
	{1784044800, 1773504000, 16, 0, 170, 243},
	{1784044800, 1709179200, 16, 14, 170, 229},
	{1784044800, 1815580800, 16, 0, 170, 365},
	{1784044800, 1785546000, 16, 17, 170, 17},
	{1784044800, 1830268799, 16, 16, 170, 169},
	{1772294401, 1801353600, 30, 30, 305, 335},
	{1772294401, 1773504000, 30, 14, 305, 13},
	{1772294401, 1709179200, 30, 28, 305, 364},
	{1772294401, 1815580800, 30, 14, 305, 135},
	{1772294401, 1785546000, 30, 0, 305, 152},
	{1772294401, 1830268799, 30, 30, 305, 304},
}

func ptr[T any](v T) *T { return &v }

func TestResetDayMatchesPHP(t *testing.T) {
	loc, err := time.LoadLocation("Asia/Shanghai")
	if err != nil {
		t.Skip("缺少时区数据")
	}
	planID := int64(1)
	methods := []int{ResetMonthFirstDay, ResetExpireDay, ResetYearFirstDay, ResetYearExpireDay}
	for _, c := range phpResetDays {
		now := time.Unix(c[0], 0).In(loc)
		exp := time.Unix(c[1], 0).In(loc)
		// 部分用例的到期时间早于当前时间（ResetDay 对已到期订阅不计算），按到期日计算的两种方式直接比对日期算法。
		future := &model.Subscription{PlanID: &planID, ExpiredAt: ptr(c[0] + 86400)}
		got := []int{
			*ResetDay(future, &model.Plan{ResetTrafficMethod: ptr(ResetMonthFirstDay)}, 0, now),
			resetDayByExpireDay(exp, now),
			*ResetDay(future, &model.Plan{ResetTrafficMethod: ptr(ResetYearFirstDay)}, 0, now),
			resetDayByYearExpireDay(exp, now),
		}
		for i, m := range methods {
			if int64(got[i]) != c[i+2] {
				t.Errorf("now=%s exp=%s 方式 %d：Go=%d PHP=%d", now.Format(time.DateTime), exp.Format(time.DateTime), m, got[i], c[i+2])
			}
		}
		if c[1] <= c[0] {
			continue
		}
		sub := &model.Subscription{PlanID: &planID, ExpiredAt: ptr(c[1])}
		for i, m := range methods {
			plan := &model.Plan{ResetTrafficMethod: ptr(m)}
			if d := ResetDay(sub, plan, 0, now); d == nil || int64(*d) != c[i+2] {
				t.Errorf("ResetDay now=%d exp=%d 方式 %d 结果不一致", c[0], c[1], m)
			}
			// 套餐未设置时使用系统设置
			if d := ResetDay(sub, &model.Plan{}, int64(m), now); d == nil || int64(*d) != c[i+2] {
				t.Errorf("ResetDay 使用系统设置 方式 %d 结果不一致", m)
			}
		}
	}
}

func TestResetDayNil(t *testing.T) {
	now := time.Unix(1784044800, 0)
	planID := int64(1)
	future := now.Unix() + 86400*40
	cases := map[string]struct {
		sub  *model.Subscription
		plan *model.Plan
	}{
		"无套餐":    {&model.Subscription{ExpiredAt: &future}, nil},
		"一次性套餐":  {&model.Subscription{PlanID: &planID}, &model.Plan{}},
		"已到期":    {&model.Subscription{PlanID: &planID, ExpiredAt: ptr(now.Unix())}, &model.Plan{}},
		"到期为 0":  {&model.Subscription{PlanID: &planID, ExpiredAt: ptr(int64(0))}, &model.Plan{}},
		"套餐不重置":  {&model.Subscription{PlanID: &planID, ExpiredAt: &future}, &model.Plan{ResetTrafficMethod: ptr(ResetNever)}},
		"未知重置方式": {&model.Subscription{PlanID: &planID, ExpiredAt: &future}, &model.Plan{ResetTrafficMethod: ptr(9)}},
	}
	for name, c := range cases {
		if d := ResetDay(c.sub, c.plan, 0, now); d != nil {
			t.Errorf("%s 不应有重置日，实际 %d", name, *d)
		}
		if p := ResetPeriod(c.sub, c.plan, 0, now); p != nil {
			t.Errorf("%s 不应有重置周期，实际 %d", name, *p)
		}
	}
	sub := &model.Subscription{PlanID: &planID, ExpiredAt: &future}
	for m, want := range map[int]int{ResetMonthFirstDay: 1, ResetExpireDay: 30, ResetYearFirstDay: 12, ResetYearExpireDay: 365} {
		if p := ResetPeriod(sub, &model.Plan{ResetTrafficMethod: ptr(m)}, 0, now); p == nil || *p != want {
			t.Errorf("方式 %d 的重置周期应为 %d", m, want)
		}
	}
	if d := ResetDay(sub, &model.Plan{}, ResetNever, now); d != nil {
		t.Error("系统设置为不重置时不应有重置日")
	}
}

func TestCycleEnd(t *testing.T) {
	loc := time.FixedZone("CST", 8*3600)
	at := func(y int, m time.Month, d, h int) time.Time { return time.Date(y, m, d, h, 0, 0, 0, loc) }
	now := at(2026, 1, 31, 10)
	planID := int64(1)
	cases := []struct {
		name   string
		method int
		expire time.Time
		want   time.Time
	}{
		{"每月 1 日", ResetMonthFirstDay, at(2026, 6, 15, 12), at(2026, 2, 1, 0)},
		{"按到期日每月：顺延到下月最后一天", ResetExpireDay, at(2026, 6, 30, 12), at(2026, 2, 28, 0)},
		{"按到期日每月：当天 0 点已重置", ResetExpireDay, at(2026, 7, 31, 12), at(2026, 2, 28, 0)},
		{"每年 1 月 1 日", ResetYearFirstDay, at(2027, 6, 1, 0), at(2027, 1, 1, 0)},
		{"按到期日每年", ResetYearExpireDay, at(2027, 3, 5, 8), at(2026, 3, 5, 0)},
		{"不重置时为到期时间", ResetNever, at(2026, 3, 5, 8), at(2026, 3, 5, 8)},
		{"到期早于重置日", ResetMonthFirstDay, at(2026, 1, 31, 20), at(2026, 1, 31, 20)},
	}
	for _, c := range cases {
		exp := c.expire.Unix()
		sub := &model.Subscription{PlanID: &planID, ExpiredAt: &exp}
		if got := CycleEnd(sub, &model.Plan{ResetTrafficMethod: ptr(c.method)}, 0, now); !got.Equal(c.want) {
			t.Errorf("%s：期望 %s，实际 %s", c.name, c.want, got)
		}
	}
}

func TestTOTPTokenMatchesPHP(t *testing.T) {
	got := TOTPToken(123, "f0e1d2c3b4a5968778695a4b3c2d1e0f", 5, time.Unix(1794563356, 0))
	if want := "MTIzOjIxYTFiNDA0MWRiMDA0ODAxYWQwZTgwOTY4MzRjM2MzNjVhOTgzYWE"; got != want {
		t.Errorf("时效 token 与 PHP 不一致：%s", got)
	}
}
