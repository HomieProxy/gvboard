package invite

import (
	"context"
	"errors"
	"math"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/model"
	"gvboard/internal/scheduler"
)

// autoCheckDelay 订单完成（最后更新）多久后自动确认佣金。
const autoCheckDelay = 3 * 24 * 3600

// maxLevels 分销最多向上发放的层级数。
const maxLevels = 3

// CheckJob 佣金定时任务（原项目 check:commission，每 15 分钟）：自动确认，再发放已确认的佣金。
func (s *Service) CheckJob() scheduler.Job {
	return scheduler.Job{
		Name: "check_commission", Spec: "*/15 * * * *", NoOverlap: true,
		Run: s.CheckCommission,
	}
}

// CheckCommission 执行一次佣金确认与发放。
func (s *Service) CheckCommission(ctx context.Context) error {
	if err := s.AutoCheck(ctx); err != nil {
		return err
	}
	return s.AutoPay(ctx)
}

// AutoCheck 开启自动确认时，把已完成或已折抵、且 3 天内没有变动的待确认佣金改为发放中。
func (s *Service) AutoCheck(ctx context.Context) error {
	if !s.Settings.Bool("commission_auto_check_enable") {
		return nil
	}
	return s.DB.WithContext(ctx).Model(&model.Order{}).
		Where("commission_status = ? AND invite_user_id IS NOT NULL AND status IN ? AND updated_at <= ?",
			model.CommissionStatusPending, []int{model.OrderStatusCompleted, model.OrderStatusDiscounted},
			s.now().Unix()-autoCheckDelay).
		Update("commission_status", model.CommissionStatusIssuing).Error
}

// AutoPay 逐个发放"发放中"订单的佣金，单个订单失败只记录日志、回滚该订单，不影响其他订单。
func (s *Service) AutoPay(ctx context.Context) error {
	var ids []int64
	err := s.DB.WithContext(ctx).Model(&model.Order{}).
		Where("commission_status = ? AND invite_user_id IS NOT NULL", model.CommissionStatusIssuing).
		Order("id").Pluck("id", &ids).Error
	if err != nil {
		return err
	}
	for _, id := range ids {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		if err := s.payOrder(ctx, id); err != nil {
			s.log().Warn("佣金发放失败", zap.Int64("order_id", id), zap.Error(err))
		}
	}
	return nil
}

// shares 各层级分成百分比。未开启分销时只有直接邀请人获得全部佣金；
// 开启时按三级比例，未填写视为 0，小数按原项目 (int) 截断。
func (s *Service) shares() []int64 {
	if !s.Settings.Bool("commission_distribution_enable") {
		return []int64{100}
	}
	out := make([]int64, 0, maxLevels)
	for _, k := range []string{"commission_distribution_l1", "commission_distribution_l2", "commission_distribution_l3"} {
		var v int64
		if p := s.Settings.FloatPtr(k); p != nil {
			v = int64(*p)
		}
		out = append(out, v)
	}
	return out
}

// payOrder 发放单个订单的佣金。锁定订单行并重新检查状态，保证多实例或管理员同时修改状态时不会重复发放。
// 每一级的金额按原项目的浮点计算后四舍五入到分（原项目由 MySQL 写入整数列时取整）；
// 某一级比例为 0 时不发放但继续向上，某一级邀请人不存在时停止（决策 D31）。
func (s *Service) payOrder(ctx context.Context, id int64) error {
	shares := s.shares()
	toBalance := s.Settings.Bool("withdraw_close_enable")
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var o model.Order
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).
			Where("id = ? AND commission_status = ? AND invite_user_id IS NOT NULL", id, model.CommissionStatusIssuing).
			Take(&o).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		actual := 0.0
		if o.ActualCommissionBalance != nil {
			actual = float64(*o.ActualCommissionBalance)
		}
		col := "commission_balance"
		if toBalance {
			col = "balance"
		}
		inviterID := *o.InviteUserID
		for l := 0; l < len(shares); l++ {
			var inviter model.User
			err := tx.Select("id", "invite_user_id").Take(&inviter, inviterID).Error
			if errors.Is(err, gorm.ErrRecordNotFound) {
				break
			}
			if err != nil {
				return err
			}
			if amount := float64(o.CommissionBalance) * (float64(shares[l]) / 100); amount != 0 {
				get := int64(math.Round(amount))
				if err := tx.Model(&model.User{}).Where("id = ?", inviter.ID).
					Update(col, gorm.Expr(col+" + ?", get)).Error; err != nil {
					return err
				}
				if err := tx.Create(&model.CommissionLog{
					InviteUserID: inviter.ID, UserID: o.UserID, TradeNo: o.TradeNo,
					OrderAmount: o.TotalAmount, GetAmount: get,
				}).Error; err != nil {
					return err
				}
				actual += amount
			}
			if inviter.InviteUserID == nil {
				break
			}
			inviterID = *inviter.InviteUserID
		}
		return tx.Model(&o).Updates(map[string]any{
			"commission_status":         model.CommissionStatusValid,
			"actual_commission_balance": int64(math.Round(actual)),
		}).Error
	})
}
