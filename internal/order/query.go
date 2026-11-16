package order

import (
	"context"
	"errors"

	"gorm.io/gorm"

	"gvboard/internal/model"
)

// activeCounts 各套餐的有效订阅数（未到期或长期有效），用于套餐容量（决策 D22）。
func activeCounts(db *gorm.DB, now int64, planIDs ...int64) (map[int64]int64, error) {
	var rows []struct {
		PlanID int64
		Count  int64
	}
	q := db.Model(&model.Subscription{}).Select("plan_id, COUNT(*) AS count").
		Where("plan_id IS NOT NULL AND (expired_at >= ? OR expired_at IS NULL)", now)
	if len(planIDs) > 0 {
		q = q.Where("plan_id IN ?", planIDs)
	}
	if err := q.Group("plan_id").Scan(&rows).Error; err != nil {
		return nil, err
	}
	out := make(map[int64]int64, len(rows))
	for _, r := range rows {
		out[r.PlanID] = r.Count
	}
	return out, nil
}

// haveCapacity 套餐是否还有容量（原项目 PlanService::haveCapacity）。
func (s *Service) haveCapacity(db *gorm.DB, p *model.Plan, now int64) (bool, error) {
	if p.CapacityLimit == nil {
		return true, nil
	}
	counts, err := activeCounts(db, now, p.ID)
	if err != nil {
		return false, err
	}
	return *p.CapacityLimit-counts[p.ID] > 0, nil
}

// Plans 用户端套餐列表：只列出销售中的套餐，按排序升序，容量限制替换为剩余容量（对应原项目 user/plan/fetch）。
func (s *Service) Plans(ctx context.Context) ([]model.Plan, error) {
	db := s.DB.WithContext(ctx)
	var plans []model.Plan
	if err := db.Where(map[string]any{"show": 1}).Order("sort ASC").Find(&plans).Error; err != nil {
		return nil, err
	}
	counts, err := activeCounts(db, s.now().Unix())
	if err != nil {
		return nil, err
	}
	for i := range plans {
		p := &plans[i]
		if c, ok := counts[p.ID]; ok && p.CapacityLimit != nil {
			left := *p.CapacityLimit - c
			p.CapacityLimit = &left
		}
	}
	return plans, nil
}

// Plan 用户端套餐详情。停售的套餐只有其订阅正在使用且允许续费时可见。
func (s *Service) Plan(ctx context.Context, userID, id int64) (*model.Plan, error) {
	db := s.DB.WithContext(ctx)
	var p model.Plan
	if err := db.Take(&p, id).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errPlanNotExist
	} else if err != nil {
		return nil, err
	}
	if p.Show == 0 {
		if p.Renew == 0 {
			return nil, errPlanNotExist
		}
		var n int64
		if err := db.Model(&model.Subscription{}).Where("user_id = ? AND plan_id = ?", userID, id).Limit(1).Count(&n).Error; err != nil {
			return nil, err
		}
		if n == 0 {
			return nil, errPlanNotExist
		}
	}
	return &p, nil
}

// PlanBrief 订单中的套餐简要信息。
type PlanBrief struct {
	ID   int64  `json:"id"`
	Name string `json:"name"`
}

// OrderView 用户端看到的订单，不含内部 ID、邀请人、佣金与支付平台单号。
type OrderView struct {
	TradeNo        string     `json:"trade_no"`
	PlanID         int64      `json:"plan_id"`
	Plan           *PlanBrief `json:"plan"`
	SubscriptionID *int64     `json:"subscription_id"`
	Type           int        `json:"type"`
	Period         string     `json:"period"`
	TotalAmount    int64      `json:"total_amount"`
	HandlingAmount *int64     `json:"handling_amount"`
	DiscountAmount *int64     `json:"discount_amount"`
	SurplusAmount  *int64     `json:"surplus_amount"`
	RefundAmount   *int64     `json:"refund_amount"`
	BalanceAmount  *int64     `json:"balance_amount"`
	Status         int        `json:"status"`
	PaidAt         *int64     `json:"paid_at"`
	CreatedAt      int64      `json:"created_at"`
	UpdatedAt      int64      `json:"updated_at"`
}

func orderView(o *model.Order, plans map[int64]string) OrderView {
	v := OrderView{
		TradeNo: o.TradeNo, PlanID: o.PlanID, SubscriptionID: o.SubscriptionID, Type: o.Type, Period: o.Period,
		TotalAmount: o.TotalAmount, HandlingAmount: o.HandlingAmount, DiscountAmount: o.DiscountAmount,
		SurplusAmount: o.SurplusAmount, RefundAmount: o.RefundAmount, BalanceAmount: o.BalanceAmount,
		Status: o.Status, PaidAt: o.PaidAt, CreatedAt: o.CreatedAt, UpdatedAt: o.UpdatedAt,
	}
	if o.PlanID == 0 {
		v.Plan = &PlanBrief{ID: 0, Name: PeriodDeposit}
	} else if name, ok := plans[o.PlanID]; ok {
		v.Plan = &PlanBrief{ID: o.PlanID, Name: name}
	}
	return v
}

func planNames(db *gorm.DB, orders []model.Order) (map[int64]string, error) {
	var ids []int64
	for _, o := range orders {
		if o.PlanID != 0 {
			ids = append(ids, o.PlanID)
		}
	}
	out := map[int64]string{}
	if len(ids) == 0 {
		return out, nil
	}
	var plans []model.Plan
	if err := db.Select("id", "name").Where("id IN ?", ids).Find(&plans).Error; err != nil {
		return nil, err
	}
	for _, p := range plans {
		out[p.ID] = p.Name
	}
	return out, nil
}

// List 用户的订单，按创建时间倒序，status 不为空时按状态筛选（对应原项目 user/order/fetch）。
func (s *Service) List(ctx context.Context, userID int64, status *int) ([]OrderView, error) {
	db := s.DB.WithContext(ctx)
	q := db.Where("user_id = ?", userID)
	if status != nil {
		q = q.Where("status = ?", *status)
	}
	var orders []model.Order
	if err := q.Order("created_at DESC").Order("id DESC").Find(&orders).Error; err != nil {
		return nil, err
	}
	names, err := planNames(db, orders)
	if err != nil {
		return nil, err
	}
	out := make([]OrderView, 0, len(orders))
	for i := range orders {
		out = append(out, orderView(&orders[i], names))
	}
	return out, nil
}

// OrderDetail 订单详情。充值订单附带赠送金额与到账金额；套餐订单附带完整套餐与被折抵的订单。
type OrderDetail struct {
	OrderView
	PlanDetail    *model.Plan `json:"plan_detail,omitempty"`
	Bonus         *int64      `json:"bonus,omitempty"`
	GetAmount     *int64      `json:"get_amount,omitempty"`
	TryOutPlanID  int64       `json:"try_out_plan_id"`
	SurplusOrders []OrderView `json:"surplus_orders,omitempty"`
	// Adjustments 插件调整明细，减免部分已计入 discount_amount
	Adjustments []model.OrderAdjustment `json:"adjustments"`
}

// Detail 订单详情（对应原项目 user/order/detail）。
func (s *Service) Detail(ctx context.Context, userID int64, tradeNo string) (*OrderDetail, error) {
	o, err := s.userOrder(ctx, userID, tradeNo)
	if err != nil {
		return nil, err
	}
	db := s.DB.WithContext(ctx)
	if o.PlanID == 0 {
		bonus := DepositBonus(s.Settings.Strings("deposit_bounus"), o.TotalAmount)
		get := o.TotalAmount + bonus
		return &OrderDetail{OrderView: orderView(o, nil), Bonus: &bonus, GetAmount: &get, Adjustments: []model.OrderAdjustment{}}, nil
	}
	adj, err := adjustments(db, o.ID)
	if err != nil {
		return nil, err
	}
	var p model.Plan
	if err := db.Take(&p, o.PlanID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errPlanNotExist
	} else if err != nil {
		return nil, err
	}
	d := &OrderDetail{
		OrderView:    orderView(o, map[int64]string{p.ID: p.Name}),
		PlanDetail:   &p,
		TryOutPlanID: s.Settings.Int("try_out_plan_id"),
		Adjustments:  adj,
	}
	if len(o.SurplusOrderIDs) > 0 {
		var orders []model.Order
		if err := db.Where("id IN ? AND user_id = ?", []int64(o.SurplusOrderIDs), userID).Find(&orders).Error; err != nil {
			return nil, err
		}
		names, err := planNames(db, orders)
		if err != nil {
			return nil, err
		}
		for i := range orders {
			d.SurplusOrders = append(d.SurplusOrders, orderView(&orders[i], names))
		}
	}
	return d, nil
}
