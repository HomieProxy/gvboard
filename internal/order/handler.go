package order

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

// Module 套餐、订单、优惠券路由。
type Module struct {
	Svc  *Service
	Auth *auth.Service
}

// Register 挂载原生 API。
//
// 用户端（需登录）：
//
//	GET  {api}/user/plans                          销售中的套餐
//	GET  {api}/user/plans/:id                      套餐详情
//	GET  {api}/user/orders                         订单列表，可选 ?status=
//	POST {api}/user/orders                         下单
//	GET  {api}/user/orders/:trade_no               订单详情
//	GET  {api}/user/orders/:trade_no/status        订单状态
//	POST {api}/user/orders/:trade_no/checkout      结算
//	POST {api}/user/orders/:trade_no/cancel        取消订单
//	POST {api}/user/coupons/check                  校验优惠券
//
// 管理端（{api}/{secure_path}，决策 D20）：
//
//	GET    plans / POST plans / PUT plans/:id / PATCH plans/:id / DELETE plans/:id / POST plans/sort
//	GET    coupons / POST coupons / PUT coupons/:id / POST coupons/batch / POST coupons/:id/toggle / DELETE coupons/:id
//	POST   orders/query / POST orders/assign / GET orders/:trade_no
//	POST   orders/:trade_no/paid / POST orders/:trade_no/cancel / PUT orders/:trade_no
func (m *Module) Register(r *server.Router) {
	u := r.API.Group("/user", m.Auth.RequireUser())
	u.GET("/plans", m.plans)
	u.GET("/plans/:id", m.plan)
	u.GET("/orders", m.orders)
	u.POST("/orders", m.create)
	u.GET("/orders/:trade_no", m.detail)
	u.GET("/orders/:trade_no/status", m.status)
	u.POST("/orders/:trade_no/checkout", m.checkout)
	u.POST("/orders/:trade_no/cancel", m.cancel)
	u.POST("/coupons/check", m.checkCoupon)

	a := m.Auth.AdminGroup(r.API, func() string { return m.Svc.Settings.String("secure_path") })
	a.GET("/plans", m.adminPlans)
	a.POST("/plans", m.adminCreatePlan)
	a.PUT("/plans/:id", m.adminUpdatePlan)
	a.PATCH("/plans/:id", m.adminPlanFlags)
	a.DELETE("/plans/:id", m.adminDeletePlan)
	a.POST("/plans/sort", m.adminSortPlans)
	a.GET("/coupons", m.adminCoupons)
	a.POST("/coupons", m.adminCreateCoupon)
	a.PUT("/coupons/:id", m.adminUpdateCoupon)
	a.POST("/coupons/batch", m.adminGenerateCoupons)
	a.POST("/coupons/:id/toggle", m.adminToggleCoupon)
	a.DELETE("/coupons/:id", m.adminDeleteCoupon)
	a.POST("/orders/query", m.adminOrders)
	a.POST("/orders/assign", m.adminAssign)
	a.GET("/orders/:trade_no", m.adminDetail)
	a.POST("/orders/:trade_no/paid", m.adminPaid)
	a.POST("/orders/:trade_no/cancel", m.adminCancel)
	a.POST("/orders/:trade_no/restore", m.adminRestore)
	a.PUT("/orders/:trade_no", m.adminUpdateOrder)
}

func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil && !errors.Is(err, io.EOF) {
		httpx.Fail(c, errInvalidParams)
		return false
	}
	return true
}

func respond(c *gin.Context, data any, err error) {
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, data)
}

func uid(c *gin.Context) int64 { return auth.Current(c).ID }

func idParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, errInvalidParams)
		return 0, false
	}
	return id, true
}

// ---------- 用户端 ----------

func (m *Module) plans(c *gin.Context) {
	v, err := m.Svc.Plans(c.Request.Context())
	respond(c, v, err)
}

func (m *Module) plan(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	v, err := m.Svc.Plan(c.Request.Context(), uid(c), id)
	respond(c, v, err)
}

func (m *Module) orders(c *gin.Context) {
	var status *int
	if s := c.Query("status"); s != "" {
		n, err := strconv.Atoi(s)
		if err != nil {
			httpx.Fail(c, errInvalidParams)
			return
		}
		status = &n
	}
	v, err := m.Svc.List(c.Request.Context(), uid(c), status)
	respond(c, v, err)
}

func (m *Module) create(c *gin.Context) {
	var in CreateInput
	if !bind(c, &in) {
		return
	}
	v, err := m.Svc.Create(c.Request.Context(), uid(c), in)
	respond(c, v, err)
}

func (m *Module) detail(c *gin.Context) {
	v, err := m.Svc.Detail(c.Request.Context(), uid(c), c.Param("trade_no"))
	respond(c, v, err)
}

func (m *Module) status(c *gin.Context) {
	v, err := m.Svc.Status(c.Request.Context(), uid(c), c.Param("trade_no"))
	respond(c, v, err)
}

func (m *Module) checkout(c *gin.Context) {
	var in PayInput
	if !bind(c, &in) {
		return
	}
	in.Request, in.ClientIP = c.Request, c.ClientIP()
	v, err := m.Svc.Checkout(c.Request.Context(), uid(c), c.Param("trade_no"), in)
	respond(c, v, err)
}

func (m *Module) cancel(c *gin.Context) {
	respond(c, true, m.Svc.Cancel(c.Request.Context(), uid(c), c.Param("trade_no")))
}

func (m *Module) checkCoupon(c *gin.Context) {
	var in CouponCheckInput
	if !bind(c, &in) {
		return
	}
	v, err := m.Svc.CheckCoupon(c.Request.Context(), uid(c), in)
	respond(c, v, err)
}

// ---------- 管理端 ----------

func (m *Module) adminPlans(c *gin.Context) {
	v, err := m.Svc.AdminPlans(c.Request.Context())
	respond(c, v, err)
}

func (m *Module) adminCreatePlan(c *gin.Context) {
	var in PlanInput
	if !bind(c, &in) {
		return
	}
	in.ID = 0
	respond(c, true, m.Svc.SavePlan(c.Request.Context(), in))
}

func (m *Module) adminUpdatePlan(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in PlanInput
	if !bind(c, &in) {
		return
	}
	in.ID = id
	respond(c, true, m.Svc.SavePlan(c.Request.Context(), in))
}

func (m *Module) adminPlanFlags(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in PlanFlags
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.UpdatePlanFlags(c.Request.Context(), id, in))
}

func (m *Module) adminDeletePlan(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.DeletePlan(c.Request.Context(), id))
}

func (m *Module) adminSortPlans(c *gin.Context) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.SortPlans(c.Request.Context(), in.IDs))
}

func (m *Module) adminCoupons(c *gin.Context) {
	var p Page
	if err := c.ShouldBindQuery(&p); err != nil {
		httpx.Fail(c, errInvalidParams)
		return
	}
	v, err := m.Svc.AdminCoupons(c.Request.Context(), p, c.Query("sort"), c.Query("sort_type"))
	respond(c, v, err)
}

func (m *Module) adminCreateCoupon(c *gin.Context) {
	var in CouponInput
	if !bind(c, &in) {
		return
	}
	in.ID = 0
	respond(c, true, m.Svc.SaveCoupon(c.Request.Context(), in))
}

func (m *Module) adminUpdateCoupon(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in CouponInput
	if !bind(c, &in) {
		return
	}
	in.ID = id
	respond(c, true, m.Svc.SaveCoupon(c.Request.Context(), in))
}

// adminGenerateCoupons 批量生成，直接返回 CSV 文件（带 UTF-8 BOM，便于 Excel 打开）。
func (m *Module) adminGenerateCoupons(c *gin.Context) {
	var in struct {
		CouponInput
		Count int `json:"count"`
	}
	if !bind(c, &in) {
		return
	}
	b, err := m.Svc.GenerateCoupons(c.Request.Context(), in.CouponInput, in.Count)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="coupons.csv"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", append([]byte("\xEF\xBB\xBF"), b...))
}

func (m *Module) adminToggleCoupon(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.ToggleCoupon(c.Request.Context(), id))
}

func (m *Module) adminDeleteCoupon(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.DeleteCoupon(c.Request.Context(), id))
}

func (m *Module) adminOrders(c *gin.Context) {
	var in OrderQuery
	if !bind(c, &in) {
		return
	}
	v, err := m.Svc.AdminOrders(c.Request.Context(), in)
	respond(c, v, err)
}

func (m *Module) adminAssign(c *gin.Context) {
	var in AssignInput
	if !bind(c, &in) {
		return
	}
	v, err := m.Svc.Assign(c.Request.Context(), in)
	respond(c, v, err)
}

func (m *Module) adminDetail(c *gin.Context) {
	v, err := m.Svc.AdminDetail(c.Request.Context(), c.Param("trade_no"))
	respond(c, v, err)
}

func (m *Module) adminRestore(c *gin.Context) {
	var in struct {
		CallbackNo string `json:"callback_no"`
	}
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.AdminRestore(c.Request.Context(), c.Param("trade_no"), in.CallbackNo))
}

func (m *Module) adminPaid(c *gin.Context) {
	respond(c, true, m.Svc.AdminPaid(c.Request.Context(), c.Param("trade_no")))
}

func (m *Module) adminCancel(c *gin.Context) {
	respond(c, true, m.Svc.AdminCancel(c.Request.Context(), c.Param("trade_no")))
}

func (m *Module) adminUpdateOrder(c *gin.Context) {
	var in struct {
		CommissionStatus *int `json:"commission_status"`
	}
	if !bind(c, &in) {
		return
	}
	if in.CommissionStatus == nil {
		httpx.Fail(c, errAdminInvalid)
		return
	}
	respond(c, true, m.Svc.SetCommissionStatus(c.Request.Context(), c.Param("trade_no"), *in.CommissionStatus))
}
