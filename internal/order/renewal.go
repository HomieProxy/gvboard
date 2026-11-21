package order

import (
	"context"
	"errors"
	"fmt"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/balance"
	"gvboard/internal/hook"
	"gvboard/internal/model"
	"gvboard/internal/scheduler"
	"gvboard/internal/util"
)

// renewWindow 到期前多久自动续费（与原项目一致为 2 天）。
const renewWindow = 2 * 86400

// errRenewSkipped 续费前状态已变化（订阅已续费、已关闭自动续费等），跳过且不关闭自动续费。
var errRenewSkipped = errors.New("skipped")

// RenewalJob 余额自动续费（原项目 check:renewal，每天 22:30）。
func (s *Service) RenewalJob() scheduler.Job {
	return scheduler.Job{Name: "check_renewal", Spec: "30 22 * * *", NoOverlap: true, Run: func(ctx context.Context) error {
		_, _, err := s.AutoRenew(ctx)
		return err
	}}
}

// AutoRenew 对开启自动续费、2 天内到期的周期订阅用余额续费（决策 D54）：
// 与原项目一致只扣余额并延长到期时间，生成 0 元的已完成续费订单，周期取该订阅最近一次已完成的周期订单。
// 无可用周期、套餐不存在 / 不允许续费 / 订阅已更换套餐、该周期价格不存在或余额不足时，关闭该订阅的自动续费。
func (s *Service) AutoRenew(ctx context.Context) (renewed, failed int, err error) {
	now := s.now().Unix()
	var ids []int64
	err = s.DB.WithContext(ctx).Model(&model.Subscription{}).
		Where("auto_renewal = 1 AND plan_id IS NOT NULL AND expired_at IS NOT NULL AND expired_at > ? AND expired_at < ?", now, now+renewWindow).
		Order("id").Pluck("id", &ids).Error
	if err != nil {
		return 0, 0, err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return renewed, failed, ctx.Err()
		}
		o, reason := s.renewOne(ctx, id)
		switch {
		case reason == nil:
			renewed++
			s.emitOrder(ctx, hook.EventOrderOpened, o)
		case errors.Is(reason, errRenewSkipped):
		default:
			failed++
			s.log().Info("订阅自动续费失败，已关闭自动续费", zap.Int64("subscription_id", id), zap.String("reason", reason.Error()))
			if err := s.DB.WithContext(ctx).Model(&model.Subscription{}).Where("id = ?", id).Update("auto_renewal", 0).Error; err != nil {
				s.log().Warn("关闭自动续费失败", zap.Int64("subscription_id", id), zap.Error(err))
			}
		}
	}
	return renewed, failed, nil
}

// renewOne 续费一个订阅，成功时返回生成的订单。错误为 errRenewSkipped 表示跳过，其他错误为续费失败的原因。
func (s *Service) renewOne(ctx context.Context, subID int64) (*model.Order, error) {
	var created *model.Order
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var sub model.Subscription
		if err := tx.Take(&sub, subID).Error; err != nil {
			return errRenewSkipped
		}
		u, err := lockUser(tx, sub.UserID)
		if err != nil {
			return errRenewSkipped
		}
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&sub, subID).Error; err != nil {
			return errRenewSkipped
		}
		now := s.now().Unix()
		if sub.AutoRenewal != 1 || sub.PlanID == nil || sub.ExpiredAt == nil || *sub.ExpiredAt <= now || *sub.ExpiredAt >= now+renewWindow {
			return errRenewSkipped
		}
		var last model.Order
		err = tx.Where("user_id = ? AND subscription_id = ? AND status = ? AND period NOT IN ?", sub.UserID, sub.ID,
			model.OrderStatusCompleted, []string{PeriodReset, PeriodOnetime, PeriodDeposit}).
			Order("created_at DESC").Order("id DESC").Take(&last).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errors.New("no valid order")
		}
		if err != nil {
			return err
		}
		if _, ok := periodMonths[last.Period]; !ok {
			return fmt.Errorf("invalid period %s", last.Period)
		}
		var plan model.Plan
		if err := tx.Where("id = ?", *sub.PlanID).Take(&plan).Error; err != nil {
			return errors.New("no such plan")
		}
		if plan.Renew == 0 {
			return errors.New("plan cannot be renewed")
		}
		if last.PlanID != plan.ID {
			return errors.New("subscription plan changed")
		}
		price := Price(&plan, last.Period)
		if price == nil {
			return errors.New("period price not available")
		}
		if u.Balance < *price {
			return errors.New("not enough balance")
		}
		tradeNo := util.GenerateOrderNo()
		if _, err := balance.Apply(tx, balance.Change{UserID: u.ID, Amount: -*price, Type: balance.TypeRenewal, TradeNo: tradeNo}); err != nil {
			return err
		}
		if err := tx.Model(&sub).Update("expired_at", expireAfter(last.Period, sub.ExpiredAt, now)).Error; err != nil {
			return err
		}
		subID := sub.ID
		o := &model.Order{
			UserID: u.ID, PlanID: plan.ID, Period: last.Period, TradeNo: tradeNo, SubscriptionID: &subID,
			TotalAmount: 0, BalanceAmount: price, Type: model.OrderTypeRenew, Status: model.OrderStatusCompleted,
		}
		if err := tx.Create(o).Error; err != nil {
			return err
		}
		created = o
		return nil
	})
	return created, err
}
