package legacy

import (
	"errors"
	"net/http"
	"slices"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gvboard/internal/model"
	"gvboard/internal/order"
)

var orderPeriods = []string{
	"month_price", "quarter_price", "half_year_price", "year_price", "two_year_price",
	"three_year_price", "onetime_price", "reset_price", "deposit",
}

// orderSave 原项目 user/order/save。套餐订单作用于主订阅（续费 / 升级 / 重置流量由主订阅当前状态决定），
// 没有主订阅时新开订阅；plan_id 为 0 时为充值订单。
func (m *Module) orderSave(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	v.required("plan_id", "Plan ID cannot be empty")
	if v.required("period", "Plan period cannot be empty") {
		v.check("period", slices.Contains(orderPeriods, in.str("period")), "Wrong plan period")
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	planID, isInt := in.int("plan_id")
	if !isInt || planID < 0 {
		m.fail(c, abort("Subscription plan does not exist"))
		return
	}
	ci := order.CreateInput{
		PlanID: planID, Period: in.str("period"), CouponCode: in.str("coupon_code"),
		DepositAmount: in.intOr("deposit_amount", 0),
	}
	if planID != 0 {
		id, err := m.primaryID(c)
		if err != nil {
			m.fail(c, err)
			return
		}
		ci.SubscriptionID = id
	}
	tradeNo, err := m.Orders.Create(c.Request.Context(), uid(c), ci)
	m.respond(c, tradeNo, err)
}

// orderCheckout 返回 {"type", "data"}（不包在 data 中），type 为 -1 表示无需支付已直接开通。
func (m *Module) orderCheckout(c *gin.Context) {
	in := params(c)
	res, err := m.Orders.Checkout(c.Request.Context(), uid(c), in.str("trade_no"), order.PayInput{
		Method: in.intOr("method", 0), Token: in.str("token"), Request: c.Request, ClientIP: c.ClientIP(),
	})
	if err != nil {
		m.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"type": res.Type, "data": res.Data})
}

func (m *Module) orderCheck(c *gin.Context) {
	status, err := m.Orders.Status(c.Request.Context(), uid(c), params(c).str("trade_no"))
	m.respond(c, status, err)
}

// orderDetail 订单完整字段；充值订单附带 plan {id:0,name:deposit}、bounus、get_amount，
// 套餐订单附带完整套餐、try_out_plan_id 与被折抵的订单。
type orderDetail struct {
	model.Order
	Plan          any           `json:"plan"`
	Bounus        *int64        `json:"bounus,omitempty"`
	GetAmount     *int64        `json:"get_amount,omitempty"`
	TryOutPlanID  *int64        `json:"try_out_plan_id,omitempty"`
	SurplusOrders []model.Order `json:"surplus_orders,omitempty"`
}

func (m *Module) orderDetail(c *gin.Context) {
	db := m.DB.WithContext(c.Request.Context())
	var o model.Order
	err := db.Where("user_id = ? AND trade_no = ?", uid(c), params(c).str("trade_no")).Take(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		m.fail(c, abort("Order does not exist or has been paid"))
		return
	} else if err != nil {
		m.fail(c, err)
		return
	}
	if o.PlanID == 0 {
		bonus := order.DepositBonus(m.Settings.Strings("deposit_bounus"), o.TotalAmount)
		get := o.TotalAmount + bonus
		ok(c, orderDetail{Order: o, Plan: gin.H{"id": 0, "name": "deposit"}, Bounus: &bonus, GetAmount: &get})
		return
	}
	var p model.Plan
	if err := db.Take(&p, o.PlanID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		m.fail(c, abort("Subscription plan does not exist"))
		return
	} else if err != nil {
		m.fail(c, err)
		return
	}
	tryOut := m.Settings.Int("try_out_plan_id")
	d := orderDetail{Order: o, Plan: &p, TryOutPlanID: &tryOut}
	if len(o.SurplusOrderIDs) > 0 {
		if err := db.Where("id IN ? AND user_id = ?", []int64(o.SurplusOrderIDs), o.UserID).Find(&d.SurplusOrders).Error; err != nil {
			m.fail(c, err)
			return
		}
	}
	ok(c, d)
}

// orderItem 订单列表项：隐藏 id 与 user_id（外层同名字段为空时被省略并遮蔽内层字段），附带完整套餐。
type orderItem struct {
	model.Order
	ID     *int64      `json:"id,omitempty"`
	UserID *int64      `json:"user_id,omitempty"`
	Plan   *model.Plan `json:"plan,omitempty"`
}

func (m *Module) orderFetch(c *gin.Context) {
	in := params(c)
	db := m.DB.WithContext(c.Request.Context())
	q := db.Where("user_id = ?", uid(c))
	if in.raw("status") != nil {
		status, isInt := in.int("status")
		if !isInt {
			ok(c, []orderItem{})
			return
		}
		q = q.Where("status = ?", status)
	}
	var orders []model.Order
	if err := q.Order("created_at DESC").Order("id DESC").Find(&orders).Error; err != nil {
		m.fail(c, err)
		return
	}
	var plans []model.Plan
	if err := db.Find(&plans).Error; err != nil {
		m.fail(c, err)
		return
	}
	byID := make(map[int64]*model.Plan, len(plans))
	for i := range plans {
		byID[plans[i].ID] = &plans[i]
	}
	out := make([]orderItem, len(orders))
	for i, o := range orders {
		out[i] = orderItem{Order: o, Plan: byID[o.PlanID]}
	}
	ok(c, out)
}

func (m *Module) paymentMethods(c *gin.Context) {
	list, err := m.Payments.Methods(c.Request.Context())
	m.respond(c, list, err)
}

func (m *Module) orderCancel(c *gin.Context) {
	in := params(c)
	if !in.truthy("trade_no") {
		m.fail(c, abort("Invalid parameter"))
		return
	}
	m.respond(c, true, m.Orders.Cancel(c.Request.Context(), uid(c), in.str("trade_no")))
}

// planFetch 带 id 时返回套餐详情，否则返回销售中的套餐（容量限制替换为剩余容量）。
func (m *Module) planFetch(c *gin.Context) {
	in := params(c)
	ctx := c.Request.Context()
	if in.truthy("id") {
		id, isInt := in.int("id")
		if !isInt {
			m.fail(c, abort("Subscription plan does not exist"))
			return
		}
		p, err := m.Orders.Plan(ctx, uid(c), id)
		m.respond(c, p, err)
		return
	}
	list, err := m.Orders.Plans(ctx)
	m.respond(c, list, err)
}

// couponCheck 校验通过后返回优惠券完整字段（原项目 CouponService::getCoupon）。
func (m *Module) couponCheck(c *gin.Context) {
	in := params(c)
	if !in.truthy("code") {
		m.fail(c, abort("Coupon cannot be empty"))
		return
	}
	ctx := c.Request.Context()
	code := in.str("code")
	if _, err := m.Orders.CheckCoupon(ctx, uid(c), order.CouponCheckInput{Code: code, PlanID: in.intOr("plan_id", 0)}); err != nil {
		m.fail(c, err)
		return
	}
	var cp model.Coupon
	err := m.DB.WithContext(ctx).Where("code = ?", code).Take(&cp).Error
	m.respond(c, cp, err)
}
