package order

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"gvboard/internal/hook"
	"gvboard/internal/model"
)

// Quote 结算试算结果（分），各项与下单时的计算一致（决策 D76）。
type Quote struct {
	Type         int               `json:"type"`          // 订单类型
	Price        int64             `json:"price"`         // 原价
	Coupon       int64             `json:"coupon"`        // 优惠券减免
	UserDiscount int64             `json:"user_discount"` // 会员折扣
	Surplus      int64             `json:"surplus"`       // 更换套餐时旧订阅折抵
	Refund       int64             `json:"refund"`        // 折抵超出订单金额的部分
	Items        []hook.AmountItem `json:"items"`         // 插件调整明细
	Balance      int64             `json:"balance"`       // 余额抵扣
	Total        int64             `json:"total"`         // 应付
}

// Preview 结算试算（POST /user/orders/preview）：参数与下单相同，不创建订单、不扣减优惠券次数与余额。
func (s *Service) Preview(ctx context.Context, userID int64, in CreateInput) (*Quote, error) {
	if in.Period == "" {
		return nil, errPeriodRequired
	}
	if !validPeriod(in.Period) {
		return nil, errWrongPeriod
	}
	if in.PlanID == 0 {
		return nil, errInvalidParams
	}
	db := s.DB.WithContext(ctx)
	var u model.User
	if err := db.Take(&u, userID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errUserNotExist
	} else if err != nil {
		return nil, err
	}
	d := &draft{preview: true}
	if err := s.quotePlan(ctx, db, &u, in, d); err != nil {
		return nil, err
	}
	q := &Quote{
		Type: d.typ, Price: d.price, Coupon: cents(d.coupon), UserDiscount: cents(d.userOff),
		Surplus: val(centsPtr(d.surplus)), Refund: val(centsPtr(d.refund)),
		Items: d.items, Balance: val(centsPtr(d.balance)), Total: cents(d.total),
	}
	if q.Items == nil {
		q.Items = []hook.AmountItem{}
	}
	return q, nil
}

// PlanView 用户端看到的套餐，附带插件提供的标签。
type PlanView struct {
	model.Plan
	Badges []string `json:"badges"`
}

// withBadges 交给插件为套餐追加标签（过滤器 plan.badges）。
func (s *Service) withBadges(ctx context.Context, userID int64, plans []model.Plan) []PlanView {
	out := make([]PlanView, len(plans))
	v := hook.PlanBadges{UserID: userID, Plans: make([]hook.PlanBadge, len(plans))}
	for i := range plans {
		out[i] = PlanView{Plan: plans[i], Badges: []string{}}
		v.Plans[i] = hook.PlanBadge{PlanID: plans[i].ID, Badges: []string{}}
	}
	if s.Hooks == nil || len(plans) == 0 {
		return out
	}
	v = hook.Apply(s.Hooks, ctx, hook.FilterPlanBadges, v)
	for i := range out {
		if i < len(v.Plans) && len(v.Plans[i].Badges) > 0 {
			out[i].Badges = v.Plans[i].Badges
		}
	}
	return out
}

// UserPlans 用户端套餐列表（含标签）。
func (s *Service) UserPlans(ctx context.Context, userID int64) ([]PlanView, error) {
	plans, err := s.Plans(ctx)
	if err != nil {
		return nil, err
	}
	return s.withBadges(ctx, userID, plans), nil
}

// UserPlan 用户端套餐详情（含标签）。
func (s *Service) UserPlan(ctx context.Context, userID, id int64) (*PlanView, error) {
	p, err := s.Plan(ctx, userID, id)
	if err != nil {
		return nil, err
	}
	return &s.withBadges(ctx, userID, []model.Plan{*p})[0], nil
}

// adjustments 订单的插件调整明细。
func adjustments(db *gorm.DB, orderID int64) ([]model.OrderAdjustment, error) {
	out := []model.OrderAdjustment{}
	err := db.Where("order_id = ?", orderID).Order("id ASC").Find(&out).Error
	return out, err
}
