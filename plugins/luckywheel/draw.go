package luckywheel

import (
	"context"
	"errors"
	"math/rand/v2"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/balance"
	"gvboard/internal/model"
	"gvboard/internal/subscription"
	sdk "gvboard/pkg/plugin"
)

// drawError 可以直接展示给用户的抽奖失败原因。
type drawError string

func (e drawError) Error() string { return string(e) }

type prizeView struct {
	Name  string `json:"name"`
	Type  string `json:"type"`
	Value int64  `json:"value"`
}

// Info 转盘页面信息。PaidLeft 为 -1 表示付费次数不限。
type Info struct {
	Prizes        []prizeView `json:"prizes"`
	FreeDaily     int64       `json:"free_daily"`
	FreeLeft      int64       `json:"free_left"`
	BalanceEnable bool        `json:"balance_enable"`
	BalanceCost   int64       `json:"balance_cost"`
	TrafficEnable bool        `json:"traffic_enable"`
	TrafficCost   int64       `json:"traffic_cost"`
	PaidDaily     int64       `json:"paid_daily"`
	PaidLeft      int64       `json:"paid_left"`
}

// Result 抽奖结果。Index 为奖品在奖品列表中的位置。
type Result struct {
	Index    int       `json:"index"`
	Prize    prizeView `json:"prize"`
	Message  string    `json:"message"`
	CostType string    `json:"cost_type"`
	Cost     int64     `json:"cost"`
}

func dayStart(now time.Time) int64 {
	y, m, d := now.Date()
	return time.Date(y, m, d, 0, 0, 0, 0, now.Location()).Unix()
}

// counts 用户今日免费与付费抽奖次数。
func counts(db *gorm.DB, uid int64, now time.Time) (free, paid int64, err error) {
	var rows []struct {
		CostType string
		N        int64
	}
	err = db.Model(&model.LuckyWheelLog{}).Select("cost_type, COUNT(*) AS n").
		Where("user_id = ? AND created_at >= ?", uid, dayStart(now)).
		Group("cost_type").Scan(&rows).Error
	for _, r := range rows {
		if r.CostType == CostFree {
			free += r.N
		} else {
			paid += r.N
		}
	}
	return free, paid, err
}

func todayCounts(ctx context.Context, h sdk.Host, uid int64) (int64, int64, error) {
	return counts(h.DB().WithContext(ctx), uid, time.Now())
}

func userInfo(ctx context.Context, h sdk.Host, s Settings, uid int64) (Info, error) {
	free, paid, err := todayCounts(ctx, h, uid)
	if err != nil {
		return Info{}, err
	}
	info := Info{
		FreeDaily: s.FreeDaily, FreeLeft: max(s.FreeDaily-free, 0),
		BalanceEnable: s.BalanceEnable, TrafficEnable: s.TrafficEnable, PaidDaily: s.PaidDaily, PaidLeft: -1,
	}
	if s.BalanceEnable {
		info.BalanceCost = s.BalanceCost
	}
	if s.TrafficEnable {
		info.TrafficCost = s.TrafficCost
	}
	if s.PaidDaily > 0 {
		info.PaidLeft = max(s.PaidDaily-paid, 0)
	}
	for _, p := range s.Prizes {
		info.Prizes = append(info.Prizes, prizeView{Name: p.Name, Type: p.Type, Value: p.Value})
	}
	return info, nil
}

// pick 在订阅能领取的奖品中按权重抽取，返回奖品位置；没有可抽取的奖品时返回 -1。
// 长期 / 一次性订阅没有到期时间，排除到期时间奖品。
func pick(prizes []Prize, sub *model.Subscription) int {
	ok := func(p Prize) bool { return p.Weight > 0 && (p.Type != PrizeDays || sub.ExpiredAt != nil) }
	var total int64
	for _, p := range prizes {
		if ok(p) {
			total += p.Weight
		}
	}
	if total <= 0 {
		return -1
	}
	n := rand.Int64N(total)
	for i, p := range prizes {
		if !ok(p) {
			continue
		}
		if n < p.Weight {
			return i
		}
		n -= p.Weight
	}
	return -1
}

// draw 抽奖：同一事务内锁定用户与订阅、检查次数、扣费、发奖并记录。
func draw(ctx context.Context, h sdk.Host, s Settings, uid, subID int64, pay string) (*Result, error) {
	var res *Result
	err := h.DB().WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		now := time.Now()
		lock := clause.Locking{Strength: "UPDATE"}
		var u model.User
		if err := tx.Clauses(lock).Select("id").Take(&u, uid).Error; err != nil {
			return err
		}
		var sub model.Subscription
		err := tx.Clauses(lock).Where("id = ? AND user_id = ?", subID, uid).Take(&sub).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || err == nil && !subscription.Available(&sub, now.Unix()) {
			return drawError("所选订阅无效或已到期，无法抽奖")
		}
		if err != nil {
			return err
		}
		free, paid, err := counts(tx, uid, now)
		if err != nil {
			return err
		}

		log := model.LuckyWheelLog{UserID: uid, SubscriptionID: sub.ID, CostType: CostFree}
		values := map[string]any{}
		if free >= s.FreeDaily {
			if !s.BalanceEnable && !s.TrafficEnable {
				return drawError("今日抽奖次数已用完，明天再来")
			}
			if s.PaidDaily > 0 && paid >= s.PaidDaily {
				return drawError("今日抽奖次数已用完，明天再来")
			}
			switch {
			case pay == CostBalance && s.BalanceEnable:
				log.CostType, log.Cost = CostBalance, s.BalanceCost
				_, err := balance.Apply(tx, balance.Change{UserID: uid, Amount: -s.BalanceCost, Type: balance.TypePlugin, Plugin: Code, Remark: "幸运大转盘抽奖"})
				if errors.Is(err, balance.ErrInsufficient) {
					return drawError("余额不足")
				}
				if err != nil {
					return err
				}
			case pay == CostTraffic && s.TrafficEnable:
				bytes := s.TrafficCost * mb
				if sub.TransferEnable-sub.U-sub.D < bytes {
					return drawError("所选订阅剩余流量不足")
				}
				log.CostType, log.Cost = CostTraffic, bytes
				values["d"] = gorm.Expr("d + ?", bytes)
			default:
				return drawError("今日免费次数已用完，请选择消耗余额或流量抽奖")
			}
		}

		i := pick(s.Prizes, &sub)
		if i < 0 {
			return drawError("所选订阅没有可抽取的奖品")
		}
		p := s.Prizes[i]
		log.PrizeName, log.PrizeType, log.PrizeValue = p.Name, p.Type, p.Value
		switch p.Type {
		case PrizeBalance:
			_, err := balance.Apply(tx, balance.Change{UserID: uid, Amount: p.Value, Type: balance.TypePlugin, Plugin: Code, Remark: "幸运大转盘：" + p.Name})
			if err != nil {
				return err
			}
		case PrizeDays:
			values["expired_at"] = max(*sub.ExpiredAt, now.Unix()) + p.Value*86400
		case PrizeTraffic:
			bytes := p.Value * mb
			log.PrizeValue = bytes
			values["transfer_enable"] = gorm.Expr("transfer_enable + ?", bytes)
			values["bonus_traffic"] = gorm.Expr("bonus_traffic + ?", bytes)
		}
		if len(values) > 0 {
			if err := tx.Model(&sub).Updates(values).Error; err != nil {
				return err
			}
		}
		if err := tx.Create(&log).Error; err != nil {
			return err
		}
		msg := "谢谢惠顾，再接再厉"
		if p.Type != PrizeNone {
			msg = "恭喜获得「" + p.Name + "」：" + p.describe()
		}
		res = &Result{Index: i, Prize: prizeView{Name: p.Name, Type: p.Type, Value: p.Value}, Message: msg, CostType: log.CostType, Cost: log.Cost}
		return nil
	})
	return res, err
}
