package legacy

import (
	"errors"
	"math"
	"slices"
	"strconv"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gvboard/internal/database"
	"gvboard/internal/model"
	"gvboard/internal/order"
	"gvboard/internal/subscription"
)

var (
	orderFilterKeys       = []string{"email", "trade_no", "status", "commission_status", "user_id", "invite_user_id", "callback_no", "commission_balance"}
	orderFilterConditions = []string{">", "<", "=", ">=", "<=", "模糊", "!="}
	assignPeriods         = []string{"month_price", "quarter_price", "half_year_price", "year_price", "two_year_price",
		"three_year_price", "onetime_price", "reset_price"}
)

func (m *Module) adminOrderFetch(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	var filters []order.OrderFilter
	list, _ := in.values("filter")
	for i, raw := range list {
		f, _ := raw.(map[string]any)
		fin := input(f)
		prefix := "filter." + strconv.Itoa(i) + "."
		if v.check(prefix+"key", fin.raw("key") != nil, "过滤键不能为空") {
			v.check(prefix+"key", slices.Contains(orderFilterKeys, fin.str("key")), "过滤键参数有误")
		}
		if v.check(prefix+"condition", fin.raw("condition") != nil, "过滤条件不能为空") {
			v.check(prefix+"condition", slices.Contains(orderFilterConditions, fin.str("condition")), "过滤条件参数有误")
		}
		filters = append(filters, order.OrderFilter{Key: fin.str("key"), Condition: fin.str("condition"), Value: fin.str("value")})
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	current, size := in.page()
	res, err := m.Orders.AdminOrders(c.Request.Context(), order.OrderQuery{
		Page: order.Page{Current: current, PageSize: size}, IsCommission: in.truthy("is_commission"), Filter: filters,
	})
	if err != nil {
		m.fail(c, err)
		return
	}
	paged(c, res.Data, res.Total)
}

// adminOrderDetail 原项目按订单 ID 查询详情。
func (m *Module) adminOrderDetail(c *gin.Context) {
	id, _ := params(c).id()
	var o model.Order
	err := m.DB.WithContext(c.Request.Context()).Select("trade_no").Take(&o, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) || id == 0 {
		m.fail(c, abortRaw("订单不存在"))
		return
	} else if err != nil {
		m.fail(c, err)
		return
	}
	d, err := m.Orders.AdminDetail(c.Request.Context(), o.TradeNo)
	m.respond(c, d, err)
}

func (m *Module) adminOrderPaid(c *gin.Context) {
	m.respond(c, true, m.Orders.AdminPaid(c.Request.Context(), params(c).str("trade_no")))
}

func (m *Module) adminOrderCancel(c *gin.Context) {
	m.respond(c, true, m.Orders.AdminCancel(c.Request.Context(), params(c).str("trade_no")))
}

// adminOrderUpdate 只修改佣金状态（原项目 status 参数只校验不修改）。
func (m *Module) adminOrderUpdate(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	if in.has("status") {
		s := in.str("status")
		v.check("status", s == "0" || s == "1" || s == "2" || s == "3", "销售状态格式不正确")
	}
	if in.has("commission_status") {
		s := in.str("commission_status")
		v.check("commission_status", s == "0" || s == "1" || s == "3", "佣金状态格式不正确")
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	ctx := c.Request.Context()
	tradeNo := in.str("trade_no")
	if !in.has("commission_status") {
		var o model.Order
		err := m.DB.WithContext(ctx).Select("id").Where("trade_no = ?", tradeNo).Take(&o).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			err = abortRaw("订单不存在")
		}
		m.respond(c, true, err)
		return
	}
	m.respond(c, true, m.Orders.SetCommissionStatus(ctx, tradeNo, int(in.intOr("commission_status", 0))))
}

// adminOrderAssign 为用户分配订单，作用于用户的主订阅；没有主订阅时新开订阅（决策 D58）。
func (m *Module) adminOrderAssign(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	v.required("plan_id", "订阅不能为空")
	v.required("email", "邮箱不能为空")
	v.required("total_amount", "支付金额不能为空")
	if v.required("period", "订阅周期不能为空") {
		v.check("period", slices.Contains(assignPeriods, in.str("period")), "订阅周期格式有误")
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	ctx := c.Request.Context()
	var u model.User
	err := database.WhereEmail(m.DB.WithContext(ctx), in.str("email")).Select("id").Take(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		m.fail(c, abortRaw("该用户不存在"))
		return
	} else if err != nil {
		m.fail(c, err)
		return
	}
	planID, isInt := in.int("plan_id")
	amount, isNum := in.float("total_amount")
	if !isInt || planID <= 0 {
		m.fail(c, abortRaw("该订阅不存在"))
		return
	}
	if !isNum {
		m.fail(c, abortRaw("订单创建失败"))
		return
	}
	var subID int64
	sub, err := m.Subs.Primary(ctx, m.DB, u.ID)
	switch {
	case err == nil:
		subID = sub.ID
	case !errors.Is(err, subscription.ErrNotFound):
		m.fail(c, err)
		return
	}
	tradeNo, err := m.Orders.Assign(ctx, order.AssignInput{
		Email: in.str("email"), PlanID: planID, Period: in.str("period"),
		TotalAmount: int64(math.Round(amount)), SubscriptionID: subID,
	})
	m.respond(c, tradeNo, err)
}
