package order

import (
	"math"
	"strconv"
	"strings"
	"time"

	"gvboard/internal/model"
)

const gb = 1073741824

// 付款周期（v2_order.period，同时是 v2_plan 中价格列的列名）。
const (
	PeriodMonth     = "month_price"
	PeriodQuarter   = "quarter_price"
	PeriodHalfYear  = "half_year_price"
	PeriodYear      = "year_price"
	PeriodTwoYear   = "two_year_price"
	PeriodThreeYear = "three_year_price"
	PeriodOnetime   = "onetime_price"
	PeriodReset     = "reset_price"
	PeriodDeposit   = "deposit"
)

// periodMonths 周期对应的月数（原项目 OrderService::STR_TO_TIME）。
var periodMonths = map[string]int{
	PeriodMonth: 1, PeriodQuarter: 3, PeriodHalfYear: 6, PeriodYear: 12, PeriodTwoYear: 24, PeriodThreeYear: 36,
}

// validPeriod 下单接口允许的周期（原项目 OrderSave 校验规则）。
func validPeriod(p string) bool {
	_, ok := periodMonths[p]
	return ok || p == PeriodOnetime || p == PeriodReset || p == PeriodDeposit
}

// Price 套餐指定周期的价格，未开放该周期时返回 nil。
func Price(p *model.Plan, period string) *int64 {
	switch period {
	case PeriodMonth:
		return p.MonthPrice
	case PeriodQuarter:
		return p.QuarterPrice
	case PeriodHalfYear:
		return p.HalfYearPrice
	case PeriodYear:
		return p.YearPrice
	case PeriodTwoYear:
		return p.TwoYearPrice
	case PeriodThreeYear:
		return p.ThreeYearPrice
	case PeriodOnetime:
		return p.OnetimePrice
	case PeriodReset:
		return p.ResetPrice
	}
	return nil
}

// addMonths 与 PHP strtotime("+N month", ts) 一致：按本地时区加月，日期溢出时顺延（1 月 31 日加一个月为 3 月 3 日）。
func addMonths(ts int64, months int) int64 {
	return time.Unix(ts, 0).AddDate(0, months, 0).Unix()
}

// expireAfter 购买周期后的到期时间（原项目 OrderService::getTime）：已到期时从现在起算。
func expireAfter(period string, expiredAt *int64, now int64) *int64 {
	ts := now
	if expiredAt != nil && *expiredAt >= now {
		ts = *expiredAt
	}
	m, ok := periodMonths[period]
	if !ok {
		return nil
	}
	v := addMonths(ts, m)
	return &v
}

// DepositBonus 充值赠送金额（原项目 getbounus）。设置项 deposit_bounus 每项格式为 "充值金额:赠送金额"（元），
// 取满足条件的最大赠送额。金额乘 100 后截断取整，与 PHP (int) 转换一致。
func DepositBonus(tiers []string, amount int64) int64 {
	var add int64
	for _, tier := range tiers {
		a, b, ok := strings.Cut(tier, ":")
		if !ok {
			continue
		}
		af, err1 := strconv.ParseFloat(strings.TrimSpace(a), 64)
		bf, err2 := strconv.ParseFloat(strings.TrimSpace(b), 64)
		if err1 != nil || err2 != nil {
			continue
		}
		if amount >= int64(af*100) && int64(bf*100) > add {
			add = int64(bf * 100)
		}
	}
	return add
}

// cents 金额入库时四舍五入到分（原项目金额以小数参与计算，写入整数列时由 MySQL 四舍五入）。
func cents(f float64) int64 { return int64(math.Round(f)) }

func centsPtr(f *float64) *int64 {
	if f == nil {
		return nil
	}
	v := cents(*f)
	return &v
}

func val(p *int64) int64 {
	if p == nil {
		return 0
	}
	return *p
}

// surplusByPeriod 周期套餐升级时的剩余价值（原项目 getSurplusValueByPeriod）。
// orders 为目标订阅已完成的周期订单（不含重置流量、一次性、充值）。返回剩余价值（可能为 nil）与参与折抵的订单 ID。
func surplusByPeriod(sub *model.Subscription, orders []model.Order, now int64) (*float64, []int64) {
	if len(orders) == 0 {
		return nil, nil
	}
	ids := make([]int64, 0, len(orders))
	var amountSum float64
	monthSum := 0
	var lastValidateAt *int64
	for i := range orders {
		o := &orders[i]
		ids = append(ids, o.ID)
		m, ok := periodMonths[o.Period]
		if !ok || addMonths(o.CreatedAt, m) < now {
			continue
		}
		if lastValidateAt == nil || o.CreatedAt > *lastValidateAt {
			v := o.CreatedAt
			lastValidateAt = &v
		}
		monthSum += m
		amountSum += float64(o.TotalAmount + val(o.BalanceAmount) + val(o.SurplusAmount) - val(o.RefundAmount))
	}
	if lastValidateAt == nil {
		return nil, ids
	}
	expiredByOrder := addMonths(*lastValidateAt, monthSum)
	if sub.ExpiredAt == nil || expiredByOrder < now || *sub.ExpiredAt < now {
		return nil, ids
	}
	surplusSecond := *sub.ExpiredAt - now
	rangeSecond := expiredByOrder - *lastValidateAt
	// 本周期奖励流量不参与折抵（决策 D87）
	paid := sub.TransferEnable - sub.BonusTraffic
	if paid <= 0 {
		return nil, ids
	}
	trafficRatio := float64(paid-(sub.U+sub.D)) / float64(paid)
	avg := amountSum / float64(rangeSecond)
	var amount float64
	if rangeSecond <= 31*86400 {
		ratio := math.Min(float64(surplusSecond)/float64(rangeSecond), trafficRatio)
		amount = avg * float64(surplusSecond) * ratio
	} else {
		const monthSeconds = 30 * 86400
		first := surplusSecond % monthSeconds
		ratio := math.Min(float64(first)/monthSeconds, trafficRatio)
		amount = avg*monthSeconds*ratio + avg*float64(surplusSecond-first)
	}
	amount = math.Max(amount, 0)
	return &amount, ids
}

// surplusByOneTime 一次性套餐升级时的剩余价值（原项目 getSurplusValueByOneTime），按剩余流量比例折算最近一次一次性订单的实付金额。
func surplusByOneTime(sub *model.Subscription, last *model.Order) *float64 {
	if last == nil {
		return nil
	}
	total := float64(sub.TransferEnable-sub.BonusTraffic) / gb
	if total <= 0 {
		return nil
	}
	paid := float64(last.TotalAmount + val(last.BalanceAmount))
	if paid == 0 {
		return nil
	}
	notUsed := total - float64(sub.U+sub.D)/gb
	amount := math.Max(notUsed/total*paid, 0)
	return &amount
}
