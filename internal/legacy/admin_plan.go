package legacy

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/order"
	"gvboard/internal/usercenter"
)

// abortCode 原项目 abort(status, '中文消息')，状态码不是 500 的情况。
func abortCode(status int, msg string) error {
	return &abortError{status: status, key: msg, raw: true}
}

// csvOut 原项目批量生成时直接输出的 CSV 内容。
func csvOut(c *gin.Context, data []byte) {
	c.Data(http.StatusOK, "text/csv; charset=UTF-8", data)
}

// integer 可选整数参数（nullable|integer）的校验。
func (v *validator) integer(field, key string) {
	if v.present(field) {
		_, ok := v.in.int(field)
		v.check(field, ok, key)
	}
}

// optInt 参数存在时取参数值（null 为 nil），否则保留 cur。
func (in input) optInt(k string, cur *int64) *int64 {
	if !in.has(k) {
		return cur
	}
	if n, ok := in.int(k); ok {
		return &n
	}
	return nil
}

func isNotFound(err error) bool {
	var he *httpx.Error
	return errors.As(err, &he) && he.Status == http.StatusNotFound
}

// ---------- 套餐 ----------

func (m *Module) adminPlanFetch(c *gin.Context) {
	list, err := m.Orders.AdminPlans(c.Request.Context())
	m.respond(c, list, err)
}

var planPrices = map[string]string{
	"month_price": "月付金额格式有误", "quarter_price": "季付金额格式有误", "half_year_price": "半年付金额格式有误",
	"year_price": "年付金额格式有误", "two_year_price": "两年付金额格式有误", "three_year_price": "三年付金额格式有误",
	"onetime_price": "一次性金额有误", "reset_price": "流量重置包金额有误",
}

// adminPlanSave 新建或修改套餐。修改时只更新请求中出现的字段（原项目 $plan->update($request->validated())）。
func (m *Module) adminPlanSave(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	v.required("name", "套餐名称不能为空")
	v.required("group_id", "权限组不能为空")
	v.required("transfer_enable", "流量不能为空")
	v.integer("device_limit", "设备数限制格式有误")
	for _, k := range []string{"month_price", "quarter_price", "half_year_price", "year_price", "two_year_price",
		"three_year_price", "onetime_price", "reset_price"} {
		v.integer(k, planPrices[k])
	}
	v.integer("reset_traffic_method", "流量重置方式格式有误")
	if v.present("reset_traffic_method") {
		n, _ := in.int("reset_traffic_method")
		v.check("reset_traffic_method", n >= 0 && n <= 4, "流量重置方式格式有误")
	}
	v.integer("capacity_limit", "容纳用户量限制格式有误")
	v.integer("speed_limit", "限速格式有误")
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	groupID, ok1 := in.int("group_id")
	transfer, ok2 := in.int("transfer_enable")
	if !ok1 || !ok2 {
		m.fail(c, abortRaw("保存失败"))
		return
	}

	var cur model.Plan
	p := order.PlanInput{Name: in.str("name"), GroupID: &groupID, TransferEnable: &transfer, ForceUpdate: in.truthy("force_update")}
	if in.truthy("id") {
		id, _ := in.id()
		err := m.DB.WithContext(c.Request.Context()).Take(&cur, id).Error
		if errors.Is(err, gorm.ErrRecordNotFound) || id == 0 {
			m.fail(c, abortRaw("该订阅不存在"))
			return
		} else if err != nil {
			m.fail(c, err)
			return
		}
		p.ID = cur.ID
	}
	p.Content = cur.Content
	if in.has("content") {
		p.Content = nil
		if in.raw("content") != nil {
			s := in.str("content")
			p.Content = &s
		}
	}
	p.DeviceLimit = in.optInt("device_limit", cur.DeviceLimit)
	p.SpeedLimit = in.optInt("speed_limit", cur.SpeedLimit)
	p.CapacityLimit = in.optInt("capacity_limit", cur.CapacityLimit)
	p.MonthPrice = in.optInt("month_price", cur.MonthPrice)
	p.QuarterPrice = in.optInt("quarter_price", cur.QuarterPrice)
	p.HalfYearPrice = in.optInt("half_year_price", cur.HalfYearPrice)
	p.YearPrice = in.optInt("year_price", cur.YearPrice)
	p.TwoYearPrice = in.optInt("two_year_price", cur.TwoYearPrice)
	p.ThreeYearPrice = in.optInt("three_year_price", cur.ThreeYearPrice)
	p.OnetimePrice = in.optInt("onetime_price", cur.OnetimePrice)
	p.ResetPrice = in.optInt("reset_price", cur.ResetPrice)
	p.ResetTrafficMethod = cur.ResetTrafficMethod
	if in.has("reset_traffic_method") {
		p.ResetTrafficMethod = nil
		if n, ok := in.int("reset_traffic_method"); ok {
			v := int(n)
			p.ResetTrafficMethod = &v
		}
	}
	err := m.Orders.SavePlan(c.Request.Context(), p)
	if err != nil && !isNotFound(err) {
		err = fromNative(err, "保存失败")
	}
	m.respond(c, true, err)
}

func (m *Module) adminPlanDrop(c *gin.Context) {
	in := params(c)
	id, ok := in.id()
	if !in.truthy("id") || !ok {
		m.fail(c, abortRaw("该订阅ID不存在"))
		return
	}
	err := m.Orders.DeletePlan(c.Request.Context(), id)
	if isNotFound(err) {
		err = abortRaw("该订阅ID不存在")
	}
	m.respond(c, true, err)
}

func (m *Module) adminPlanUpdate(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	var flags order.PlanFlags
	for _, f := range []struct {
		key, msg string
		dst      **int
	}{{"show", "销售状态格式不正确", &flags.Show}, {"renew", "续费状态格式不正确", &flags.Renew}} {
		if !in.has(f.key) {
			continue
		}
		s := in.str(f.key)
		if v.check(f.key, s == "0" || s == "1", f.msg) {
			n := int(in.intOr(f.key, 0))
			*f.dst = &n
		}
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	id, _ := in.id()
	m.respond(c, true, m.Orders.UpdatePlanFlags(c.Request.Context(), id, flags))
}

func (m *Module) adminPlanSort(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	if v.required("plan_ids", "订阅计划ID不能为空") {
		v.check("plan_ids", in.isArray("plan_ids"), "订阅计划ID格式有误")
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	ids, ok := in.intList("plan_ids")
	if !ok {
		m.fail(c, abortRaw("保存失败"))
		return
	}
	m.respond(c, true, fromNative(m.Orders.SortPlans(c.Request.Context(), ids), "保存失败"))
}

// ---------- 优惠券 ----------

// sortParams 原项目列表排序参数：sort_type 只能是 ASC / DESC（默认 DESC），sort 默认 id。
func (in input) sortParams() (sort, sortType string) {
	sortType = in.str("sort_type")
	if sortType != "ASC" && sortType != "DESC" {
		sortType = "DESC"
	}
	sort = in.str("sort")
	if sort == "" {
		sort = "id"
	}
	return sort, sortType
}

func (m *Module) adminCouponFetch(c *gin.Context) {
	in := params(c)
	current, size := in.page()
	sort, sortType := in.sortParams()
	res, err := m.Orders.AdminCoupons(c.Request.Context(), order.Page{Current: current, PageSize: size}, sort, sortType)
	if err != nil {
		m.fail(c, err)
		return
	}
	paged(c, res.Data, res.Total)
}

func (m *Module) adminCouponShow(c *gin.Context) {
	in := params(c)
	if !in.truthy("id") {
		m.fail(c, abortRaw("参数有误"))
		return
	}
	id, _ := in.id()
	m.respond(c, true, m.Orders.ToggleCoupon(c.Request.Context(), id))
}

// generateCount 批量生成数量（nullable|integer|max:500）。
func (v *validator) generateCount() {
	if v.present("generate_count") {
		n, ok := v.in.int("generate_count")
		if v.check("generate_count", ok, "生成数量必须为数字") {
			v.check("generate_count", n <= 500, "生成数量最大为500个")
		}
	}
}

func (m *Module) adminCouponGenerate(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	v.generateCount()
	v.required("name", "名称不能为空")
	if v.required("type", "类型不能为空") {
		s := in.str("type")
		v.check("type", s == "1" || s == "2", "类型格式有误")
	}
	if v.required("value", "金额或比例不能为空") {
		v.integer("value", "金额或比例格式有误")
	}
	if v.required("started_at", "开始时间不能为空") {
		v.integer("started_at", "开始时间格式有误")
	}
	if v.required("ended_at", "结束时间不能为空") {
		v.integer("ended_at", "结束时间格式有误")
	}
	v.integer("limit_use", "最大使用次数格式有误")
	v.integer("limit_use_with_user", "限制用户使用次数格式有误")
	if v.present("limit_plan_ids") {
		v.check("limit_plan_ids", in.isArray("limit_plan_ids"), "指定订阅格式有误")
	}
	if v.present("limit_period") {
		v.check("limit_period", in.isArray("limit_period"), "指定周期格式有误")
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	cp := order.CouponInput{
		Code: in.str("code"), Name: in.str("name"), Type: int(in.intOr("type", 0)), Value: in.intOr("value", 0),
		StartedAt: in.intOr("started_at", 0), EndedAt: in.intOr("ended_at", 0),
		LimitUse: in.optInt("limit_use", nil), LimitUseWithUser: in.optInt("limit_use_with_user", nil),
	}
	if in.raw("limit_plan_ids") != nil {
		ids, ok := in.intList("limit_plan_ids")
		if !ok {
			m.fail(c, abortRaw("保存失败"))
			return
		}
		cp.LimitPlanIDs = ids
	}
	if in.raw("limit_period") != nil {
		cp.LimitPeriod = in.strList("limit_period")
	}
	ctx := c.Request.Context()
	if in.truthy("generate_count") {
		data, err := m.Orders.GenerateCoupons(ctx, cp, int(in.intOr("generate_count", 0)))
		if err != nil {
			m.fail(c, fromNative(err, "生成失败"))
			return
		}
		csvOut(c, data)
		return
	}
	if in.truthy("id") {
		id, ok := in.id()
		if !ok {
			m.fail(c, abortRaw("保存失败"))
			return
		}
		cp.ID = id
	}
	err := m.Orders.SaveCoupon(ctx, cp)
	if isNotFound(err) {
		err = abortRaw("保存失败")
	}
	m.respond(c, true, err)
}

func (m *Module) adminCouponDrop(c *gin.Context) {
	in := params(c)
	if !in.truthy("id") {
		m.fail(c, abortRaw("参数有误"))
		return
	}
	id, _ := in.id()
	m.respond(c, true, m.Orders.DeleteCoupon(c.Request.Context(), id))
}

// ---------- 礼品卡 ----------

func (m *Module) adminGiftcardFetch(c *gin.Context) {
	in := params(c)
	current, size := in.page()
	sort, sortType := in.sortParams()
	res, err := m.Users.AdminGiftcards(c.Request.Context(), usercenter.GiftcardPage{
		Current: current, PageSize: size, Sort: sort, SortType: sortType,
	})
	if err != nil {
		m.fail(c, err)
		return
	}
	paged(c, res.Data, res.Total)
}

func (m *Module) adminGiftcardGenerate(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	v.generateCount()
	v.required("name", "名称不能为空")
	typ := in.str("type")
	if v.required("type", "类型不能为空") {
		v.check("type", typ == "1" || typ == "2" || typ == "3" || typ == "4" || typ == "5", "类型格式有误")
	}
	if typ == "1" || typ == "2" || typ == "3" || typ == "5" {
		v.required("value", "数值不能为空")
	}
	v.integer("value", "数值格式有误")
	if typ == "5" {
		v.required("plan_id", "订阅不能为空")
	}
	v.integer("plan_id", "validation.integer")
	if v.required("started_at", "开始时间不能为空") {
		v.integer("started_at", "开始时间格式有误")
	}
	if v.required("ended_at", "结束时间不能为空") {
		v.integer("ended_at", "结束时间格式有误")
	}
	v.integer("limit_use", "最大使用次数格式有误")
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	g := usercenter.GiftcardInput{
		Code: in.str("code"), Name: in.str("name"), Type: int(in.intOr("type", 0)),
		Value: in.optInt("value", nil), PlanID: in.optInt("plan_id", nil), LimitUse: in.optInt("limit_use", nil),
		StartedAt: in.intOr("started_at", 0), EndedAt: in.intOr("ended_at", 0),
	}
	ctx := c.Request.Context()
	if in.truthy("generate_count") {
		data, err := m.Users.GenerateGiftcards(ctx, g, int(in.intOr("generate_count", 0)))
		if err != nil {
			m.fail(c, fromNative(err, "礼品卡批量生成失败"))
			return
		}
		csvOut(c, data)
		return
	}
	if in.truthy("id") {
		id, ok := in.id()
		if !ok {
			m.fail(c, abortCode(http.StatusNotFound, "礼品卡不存在"))
			return
		}
		g.ID = id
	}
	err := m.Users.SaveGiftcard(ctx, g)
	switch {
	case isNotFound(err) && g.ID != 0:
		err = abortCode(http.StatusNotFound, "礼品卡不存在")
	case err != nil:
		err = fromNative(err, "礼品卡保存失败")
	}
	m.respond(c, true, err)
}

func (m *Module) adminGiftcardDrop(c *gin.Context) {
	in := params(c)
	if !in.truthy("id") {
		m.fail(c, abortCode(http.StatusBadRequest, "未找到礼品卡"))
		return
	}
	id, _ := in.id()
	err := m.Users.DeleteGiftcard(c.Request.Context(), id)
	if isNotFound(err) {
		err = abortCode(http.StatusNotFound, "礼品卡不存在")
	}
	m.respond(c, true, err)
}
