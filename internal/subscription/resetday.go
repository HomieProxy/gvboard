package subscription

import (
	"time"

	"gvboard/internal/model"
)

// 流量重置方式（套餐 reset_traffic_method，为空时使用系统设置）。
const (
	ResetMonthFirstDay = 0 // 每月 1 日
	ResetExpireDay     = 1 // 按到期日每月重置
	ResetNever         = 2 // 不重置
	ResetYearFirstDay  = 3 // 每年 1 月 1 日
	ResetYearExpireDay = 4 // 按到期日每年重置
)

// resetMethod 套餐的重置方式，套餐未设置时使用系统设置 global。
func resetMethod(plan *model.Plan, global int64) int {
	if plan != nil && plan.ResetTrafficMethod != nil {
		return *plan.ResetTrafficMethod
	}
	return int(global)
}

// active 是否有可计算重置日的有效期（与原项目一致：一次性套餐与已到期均不计算）。
func active(sub *model.Subscription, now time.Time) bool {
	return sub.PlanID != nil && sub.ExpiredAt != nil && *sub.ExpiredAt > now.Unix()
}

// ResetDay 距下次流量重置的天数，不重置时返回 nil。对应原项目 UserService::getResetDay。
func ResetDay(sub *model.Subscription, plan *model.Plan, global int64, now time.Time) *int {
	if !active(sub, now) {
		return nil
	}
	var d int
	switch resetMethod(plan, global) {
	case ResetMonthFirstDay:
		d = lastDayOfMonth(now) - now.Day()
	case ResetExpireDay:
		d = resetDayByExpireDay(time.Unix(*sub.ExpiredAt, 0).In(now.Location()), now)
	case ResetYearFirstDay:
		next := time.Date(now.Year()+1, 1, 1, 0, 0, 0, 0, now.Location())
		d = int(next.Unix()-now.Unix()) / 86400
	case ResetYearExpireDay:
		d = resetDayByYearExpireDay(time.Unix(*sub.ExpiredAt, 0).In(now.Location()), now)
	default:
		return nil
	}
	return &d
}

// ResetPeriod 重置周期，不重置时返回 nil。对应原项目 UserService::getResetPeriod：
// 每月 1 日为 1，按到期日每月为 30，每年 1 月 1 日为 12，按到期日每年为 365。
func ResetPeriod(sub *model.Subscription, plan *model.Plan, global int64, now time.Time) *int {
	if !active(sub, now) {
		return nil
	}
	var p int
	switch resetMethod(plan, global) {
	case ResetMonthFirstDay:
		p = 1
	case ResetExpireDay:
		p = 30
	case ResetYearFirstDay:
		p = 12
	case ResetYearExpireDay:
		p = 365
	default:
		return nil
	}
	return &p
}

// CycleEnd 当前流量周期的结束时间：下次流量重置时间与到期时间中较早的一个；不重置时为到期时间。
// 与定时任务一致，重置发生在重置日 0 点，当天已过 0 点即视为已重置。只用于未到期的周期订阅。
func CycleEnd(sub *model.Subscription, plan *model.Plan, global int64, now time.Time) time.Time {
	expire := time.Unix(*sub.ExpiredAt, 0).In(now.Location())
	var next time.Time
	y, m, loc := now.Year(), now.Month(), now.Location()
	switch resetMethod(plan, global) {
	case ResetMonthFirstDay:
		next = time.Date(y, m+1, 1, 0, 0, 0, 0, loc)
	case ResetYearFirstDay:
		next = time.Date(y+1, 1, 1, 0, 0, 0, 0, loc)
	case ResetExpireDay:
		for k := time.Month(0); k <= 2; k++ {
			last := time.Date(y, m+k+1, 0, 0, 0, 0, 0, loc).Day()
			t := time.Date(y, m+k, min(expire.Day(), last), 0, 0, 0, 0, loc)
			if t.After(now) {
				next = t
				break
			}
		}
	case ResetYearExpireDay:
		next = time.Date(y, expire.Month(), expire.Day(), 0, 0, 0, 0, loc)
		if !next.After(now) {
			next = next.AddDate(1, 0, 0)
		}
	default:
		return expire
	}
	if next.IsZero() || next.After(expire) {
		return expire
	}
	return next
}

func lastDayOfMonth(t time.Time) int {
	return time.Date(t.Year(), t.Month()+1, 0, 0, 0, 0, 0, t.Location()).Day()
}

func resetDayByExpireDay(expire, now time.Time) int {
	day, today, last := expire.Day(), now.Day(), lastDayOfMonth(now)
	if day >= today && day >= last {
		return last - today
	}
	if day >= today {
		return day - today
	}
	return last - today + day
}

// resetDayByYearExpireDay 今年的到期月日（2 月 29 日在平年顺延为 3 月 1 日，与 PHP strtotime 一致）。
func resetDayByYearExpireDay(expire, now time.Time) int {
	thisYear := time.Date(now.Year(), expire.Month(), expire.Day(), 0, 0, 0, 0, now.Location())
	if thisYear.After(now) {
		return int(thisYear.Unix()-now.Unix()) / 86400
	}
	return int(thisYear.AddDate(1, 0, 0).Unix()-now.Unix()) / 86400
}
