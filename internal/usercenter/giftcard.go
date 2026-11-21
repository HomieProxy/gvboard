package usercenter

import (
	"context"
	"errors"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/balance"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/subscription"
)

// 礼品卡类型（v2_giftcard.type）。
const (
	GiftcardBalance      = 1 // 余额（分）
	GiftcardDuration     = 2 // 时长（天）
	GiftcardTraffic      = 3 // 流量（GB）
	GiftcardResetTraffic = 4 // 重置已用流量
	GiftcardPlan         = 5 // 套餐，value 为天数，0 表示长期有效
)

// RedeemInput 兑换参数。SubscriptionID 为作用的订阅：
// 时长、流量、重置流量类不填时作用于主订阅；套餐类不填时新开订阅（决策 D18）。余额类忽略。
type RedeemInput struct {
	Code           string `json:"code"`
	SubscriptionID int64  `json:"subscription_id"`
}

// RedeemResult 兑换结果。
type RedeemResult struct {
	Type           int   `json:"type"`
	Value          int64 `json:"value"`
	SubscriptionID int64 `json:"subscription_id,omitempty"`
}

// RedeemGiftcard 兑换礼品卡，校验与各类型的处理与原项目 user/redeemgiftcard 一致。
// 礼品卡行在事务中加锁，防止并发兑换超出使用次数。
func (s *Service) RedeemGiftcard(ctx context.Context, userID int64, in RedeemInput) (*RedeemResult, error) {
	if in.Code == "" {
		return nil, errGiftcardRequired
	}
	var res *RedeemResult
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var u model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&u, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errUserNotExist
			}
			return err
		}
		var gc model.Giftcard
		err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Where("code = ?", in.Code).Take(&gc).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return errGiftcardNotExist
		}
		if err != nil {
			return err
		}
		now := s.now().Unix()
		if gc.StartedAt != 0 && now < gc.StartedAt {
			return errGiftcardNotYet
		}
		if gc.EndedAt != 0 && now > gc.EndedAt {
			return errGiftcardExpired
		}
		if gc.LimitUse != nil && *gc.LimitUse <= 0 {
			return errGiftcardLimit
		}
		if slices.Contains(gc.UsedUserIDs, userID) {
			return errGiftcardUsed
		}
		value := int64(0)
		if gc.Value != nil {
			value = *gc.Value
		}

		res = &RedeemResult{Type: gc.Type, Value: value}
		switch gc.Type {
		case GiftcardBalance:
			if _, err := balance.Apply(tx, balance.Change{UserID: userID, Amount: value, Type: balance.TypeGiftcard, Remark: gc.Code}); err != nil {
				return err
			}
		case GiftcardDuration, GiftcardTraffic, GiftcardResetTraffic, GiftcardPlan:
			subID, err := s.applyToSubscription(ctx, tx, &gc, in.SubscriptionID, userID, value, now)
			if err != nil {
				return err
			}
			res.SubscriptionID = subID
		default:
			return errGiftcardUnknown
		}

		updates := map[string]any{"used_user_ids": append(gc.UsedUserIDs, userID)}
		if gc.LimitUse != nil {
			updates["limit_use"] = *gc.LimitUse - 1
		}
		return tx.Model(&gc).Updates(updates).Error
	})
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) {
			return nil, err
		}
		return nil, httpx.Internal(err)
	}
	return res, nil
}

// applyToSubscription 时长、流量、重置流量、套餐类礼品卡作用于订阅，返回订阅 ID。
func (s *Service) applyToSubscription(ctx context.Context, tx *gorm.DB, gc *model.Giftcard, subID, userID, value, now int64) (int64, error) {
	if gc.Type == GiftcardPlan && subID == 0 {
		plan, err := s.giftcardPlan(tx, gc)
		if err != nil {
			return 0, err
		}
		sub, err := s.Subscriptions.Create(tx, subscription.NewSubscription{UserID: userID, Plan: plan, ExpiredAt: planExpire(value, now)})
		if err != nil {
			return 0, err
		}
		return sub.ID, nil
	}

	var sub *model.Subscription
	var err error
	if subID == 0 {
		sub, err = s.Subscriptions.Primary(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), userID)
	} else {
		sub, err = s.Subscriptions.Find(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), userID, subID)
	}
	if err != nil {
		return 0, err
	}

	values := map[string]any{}
	switch gc.Type {
	case GiftcardDuration:
		if sub.ExpiredAt == nil {
			return 0, errGiftcardNotSuit
		}
		if *sub.ExpiredAt <= now {
			values["expired_at"] = now + value*86400
		} else {
			values["expired_at"] = *sub.ExpiredAt + value*86400
		}
	case GiftcardTraffic:
		values["transfer_enable"] = sub.TransferEnable + value*1073741824
	case GiftcardResetTraffic:
		subscription.ResetTraffic(sub)
		values["u"], values["d"] = 0, 0
		values["transfer_enable"], values["bonus_traffic"] = sub.TransferEnable, 0
	case GiftcardPlan:
		// 只能作用于没有套餐或已到期的订阅（与原项目一致）
		if sub.PlanID != nil && (sub.ExpiredAt == nil || *sub.ExpiredAt >= now) {
			return 0, errGiftcardNotSuit
		}
		plan, err := s.giftcardPlan(tx, gc)
		if err != nil {
			return 0, err
		}
		subscription.ApplyPlan(sub, plan)
		values["plan_id"], values["group_id"] = sub.PlanID, sub.GroupID
		values["transfer_enable"], values["u"], values["d"], values["bonus_traffic"] = sub.TransferEnable, 0, 0, 0
		values["device_limit"], values["speed_limit"] = sub.DeviceLimit, sub.SpeedLimit
		values["expired_at"] = planExpire(value, now)
	}
	if err := tx.Model(sub).Updates(values).Error; err != nil {
		return 0, err
	}
	return sub.ID, nil
}

func (s *Service) giftcardPlan(tx *gorm.DB, gc *model.Giftcard) (*model.Plan, error) {
	if gc.PlanID == nil {
		return nil, errPlanNotExist
	}
	var p model.Plan
	err := tx.Take(&p, *gc.PlanID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errPlanNotExist
	}
	return &p, err
}

// planExpire 套餐类礼品卡的到期时间：天数为 0 时长期有效。
func planExpire(days, now int64) *int64 {
	if days == 0 {
		return nil
	}
	exp := now + days*86400
	return &exp
}
