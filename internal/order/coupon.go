package order

import (
	"context"
	"errors"
	"slices"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/model"
)

// 优惠券类型（v2_coupon.type）。
const (
	CouponAmount  = 1 // 按金额（分）
	CouponPercent = 2 // 按比例（%）
)

// CouponCheckInput 优惠券校验参数。PlanID、Period 为 0 / 空时不检查对应限制（与原项目一致）。
type CouponCheckInput struct {
	Code   string `json:"code"`
	PlanID int64  `json:"plan_id"`
	Period string `json:"period"`
}

// CouponView 用户端看到的优惠券。
type CouponView struct {
	Code         string        `json:"code"`
	Name         string        `json:"name"`
	Type         int           `json:"type"`
	Value        int64         `json:"value"`
	LimitPlanIDs model.IntList `json:"limit_plan_ids"`
	LimitPeriod  model.StrList `json:"limit_period"`
	StartedAt    int64         `json:"started_at"`
	EndedAt      int64         `json:"ended_at"`
}

// CheckCoupon 用户下单前校验优惠券（对应原项目 user/coupon/check）。
func (s *Service) CheckCoupon(ctx context.Context, userID int64, in CouponCheckInput) (*CouponView, error) {
	if in.Code == "" {
		return nil, errCouponRequired
	}
	c, err := s.validCoupon(s.DB.WithContext(ctx), in.Code, in.PlanID, userID, in.Period, false)
	if err != nil {
		return nil, err
	}
	return &CouponView{
		Code: c.Code, Name: c.Name, Type: c.Type, Value: c.Value,
		LimitPlanIDs: c.LimitPlanIDs, LimitPeriod: c.LimitPeriod, StartedAt: c.StartedAt, EndedAt: c.EndedAt,
	}, nil
}

// validCoupon 读取并校验优惠券，检查顺序与原项目 CouponService::check 一致。lock 为 true 时锁定优惠券行。
func (s *Service) validCoupon(db *gorm.DB, code string, planID, userID int64, period string, lock bool) (*model.Coupon, error) {
	q := db
	if lock {
		q = db.Clauses(clause.Locking{Strength: "UPDATE"})
	}
	var c model.Coupon
	err := q.Where("code = ?", code).Take(&c).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errCouponInvalid
	}
	if err != nil {
		return nil, err
	}
	if c.Show == 0 {
		return nil, errCouponInvalid
	}
	if c.LimitUse != nil && *c.LimitUse <= 0 {
		return nil, errCouponUsedUp
	}
	now := s.now().Unix()
	if now < c.StartedAt {
		return nil, errCouponNotStarted
	}
	if now > c.EndedAt {
		return nil, errCouponExpired
	}
	if len(c.LimitPlanIDs) > 0 && planID != 0 && !c.LimitPlanIDs.Contains(planID) {
		return nil, errCouponPlan
	}
	if len(c.LimitPeriod) > 0 && period != "" && !slices.Contains(c.LimitPeriod, period) {
		return nil, errCouponPeriod
	}
	if c.LimitUseWithUser != nil && userID != 0 {
		var used int64
		err := db.Model(&model.Order{}).
			Where("coupon_id = ? AND user_id = ? AND status NOT IN ?", c.ID, userID, []int{model.OrderStatusPending, model.OrderStatusCancelled}).
			Count(&used).Error
		if err != nil {
			return nil, err
		}
		if used >= *c.LimitUseWithUser {
			return nil, errCouponPerUser.With(map[string]any{"limit_use_with_user": *c.LimitUseWithUser})
		}
	}
	return &c, nil
}

// useCoupon 在下单事务 tx 中使用优惠券：计算优惠金额并扣减可用次数（原项目 CouponService::use）。
func (s *Service) useCoupon(tx *gorm.DB, code string, d *draft, userID int64) (int64, error) {
	c, err := s.validCoupon(tx, code, d.planID, userID, d.period, true)
	if err != nil {
		return 0, err
	}
	var discount float64
	switch c.Type {
	case CouponAmount:
		discount = float64(c.Value)
	case CouponPercent:
		discount = d.total * (float64(c.Value) / 100)
	}
	if discount > d.total {
		discount = d.total
	}
	d.discount = &discount
	if c.LimitUse != nil {
		res := tx.Model(&model.Coupon{}).Where("id = ? AND limit_use > 0", c.ID).Update("limit_use", gorm.Expr("limit_use - 1"))
		if res.Error != nil {
			return 0, res.Error
		}
		if res.RowsAffected != 1 {
			return 0, errCouponFailed
		}
	}
	return c.ID, nil
}
