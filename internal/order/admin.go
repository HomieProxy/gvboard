package order

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/database"
	"gvboard/internal/hook"
	"gvboard/internal/model"
	"gvboard/internal/util"
)

// ---------- 套餐 ----------

// AdminPlan 管理端套餐，附带使用中的有效订阅数。
type AdminPlan struct {
	model.Plan
	Count int64 `json:"count"`
}

// AdminPlans 全部套餐，按排序升序（对应原项目 admin/plan/fetch）。
func (s *Service) AdminPlans(ctx context.Context) ([]AdminPlan, error) {
	db := s.DB.WithContext(ctx)
	var plans []model.Plan
	if err := db.Order("sort ASC").Order("id ASC").Find(&plans).Error; err != nil {
		return nil, err
	}
	counts, err := activeCounts(db, s.now().Unix())
	if err != nil {
		return nil, err
	}
	out := make([]AdminPlan, 0, len(plans))
	for _, p := range plans {
		out = append(out, AdminPlan{Plan: p, Count: counts[p.ID]})
	}
	return out, nil
}

// PlanInput 套餐保存参数。价格单位为分，流量单位为 GB，未开放的周期价格为空。
type PlanInput struct {
	ID                 int64   `json:"id"`
	Name               string  `json:"name"`
	Content            *string `json:"content"`
	GroupID            *int64  `json:"group_id"`
	TransferEnable     *int64  `json:"transfer_enable"`
	DeviceLimit        *int64  `json:"device_limit"`
	SpeedLimit         *int64  `json:"speed_limit"`
	CapacityLimit      *int64  `json:"capacity_limit"`
	ResetTrafficMethod *int    `json:"reset_traffic_method"`
	MonthPrice         *int64  `json:"month_price"`
	QuarterPrice       *int64  `json:"quarter_price"`
	HalfYearPrice      *int64  `json:"half_year_price"`
	YearPrice          *int64  `json:"year_price"`
	TwoYearPrice       *int64  `json:"two_year_price"`
	ThreeYearPrice     *int64  `json:"three_year_price"`
	OnetimePrice       *int64  `json:"onetime_price"`
	ResetPrice         *int64  `json:"reset_price"`
	// ForceUpdate 同时把权限组、流量、设备数、限速同步到正在使用该套餐的全部订阅。
	ForceUpdate bool `json:"force_update"`
}

var planColumns = []string{"name", "content", "group_id", "transfer_enable", "device_limit", "speed_limit", "capacity_limit",
	"reset_traffic_method", "month_price", "quarter_price", "half_year_price", "year_price", "two_year_price",
	"three_year_price", "onetime_price", "reset_price"}

// SavePlan 新建（ID 为 0）或修改套餐（对应原项目 admin/plan/save）。新建的套餐默认不显示、允许续费。
func (s *Service) SavePlan(ctx context.Context, in PlanInput) error {
	in.Name = strings.TrimSpace(in.Name)
	if in.Name == "" || in.GroupID == nil || in.TransferEnable == nil {
		return errAdminInvalid
	}
	if m := in.ResetTrafficMethod; m != nil && (*m < 0 || *m > 4) {
		return errAdminInvalid
	}
	p := model.Plan{
		Name: in.Name, Content: in.Content, GroupID: *in.GroupID, TransferEnable: *in.TransferEnable,
		DeviceLimit: in.DeviceLimit, SpeedLimit: in.SpeedLimit, CapacityLimit: in.CapacityLimit, ResetTrafficMethod: in.ResetTrafficMethod,
		MonthPrice: in.MonthPrice, QuarterPrice: in.QuarterPrice, HalfYearPrice: in.HalfYearPrice, YearPrice: in.YearPrice,
		TwoYearPrice: in.TwoYearPrice, ThreeYearPrice: in.ThreeYearPrice, OnetimePrice: in.OnetimePrice, ResetPrice: in.ResetPrice,
	}
	db := s.DB.WithContext(ctx)
	if in.ID == 0 {
		p.Show, p.Renew = 0, 1
		return db.Create(&p).Error
	}
	return db.Transaction(func(tx *gorm.DB) error {
		var old model.Plan
		if err := tx.Select("id").Take(&old, in.ID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
			return errAdminPlanNotExist
		} else if err != nil {
			return err
		}
		if in.ForceUpdate {
			err := tx.Model(&model.Subscription{}).Where("plan_id = ?", in.ID).Updates(map[string]any{
				"group_id": p.GroupID, "transfer_enable": p.TransferEnable * gb,
				"device_limit": p.DeviceLimit, "speed_limit": p.SpeedLimit,
			}).Error
			if err != nil {
				return err
			}
		}
		p.ID = in.ID
		return tx.Model(&p).Select(planColumns).Updates(&p).Error
	})
}

// DeletePlan 删除套餐。存在相关订单或订阅时不能删除（对应原项目 admin/plan/drop）。
func (s *Service) DeletePlan(ctx context.Context, id int64) error {
	db := s.DB.WithContext(ctx)
	var n int64
	if err := db.Model(&model.Order{}).Where("plan_id = ?", id).Limit(1).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return errAdminPlanHasOrder
	}
	if err := db.Model(&model.Subscription{}).Where("plan_id = ?", id).Limit(1).Count(&n).Error; err != nil {
		return err
	}
	if n > 0 {
		return errAdminPlanHasSub
	}
	res := db.Delete(&model.Plan{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errAdminPlanNotExist
	}
	return nil
}

// PlanFlags 套餐销售与续费开关，未传的项不修改。
type PlanFlags struct {
	Show  *int `json:"show"`
	Renew *int `json:"renew"`
}

// UpdatePlanFlags 修改销售、续费开关（对应原项目 admin/plan/update）。
func (s *Service) UpdatePlanFlags(ctx context.Context, id int64, in PlanFlags) error {
	values := map[string]any{}
	for col, v := range map[string]*int{"show": in.Show, "renew": in.Renew} {
		if v == nil {
			continue
		}
		if *v != 0 && *v != 1 {
			return errAdminInvalid
		}
		values[col] = *v
	}
	db := s.DB.WithContext(ctx)
	var p model.Plan
	if err := db.Select("id").Take(&p, id).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return errAdminPlanNotExist
	} else if err != nil {
		return err
	}
	if len(values) == 0 {
		return nil
	}
	return db.Model(&p).Updates(values).Error
}

// SortPlans 按给定顺序设置排序（对应原项目 admin/plan/sort）。
func (s *Service) SortPlans(ctx context.Context, ids []int64) error {
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for i, id := range ids {
			res := tx.Model(&model.Plan{}).Where("id = ?", id).Update("sort", i+1)
			if res.Error != nil {
				return res.Error
			}
			if res.RowsAffected == 0 {
				return errAdminPlanNotExist
			}
		}
		return nil
	})
}

// ---------- 优惠券 ----------

// Page 分页参数。原项目 pageSize 小于 10 时按 10 处理，这里另外限制最大 100。
type Page struct {
	Current  int `json:"current" form:"current"`
	PageSize int `json:"page_size" form:"page_size"`
}

func (p Page) normalize() (offset, limit int) {
	cur, size := p.Current, p.PageSize
	if cur < 1 {
		cur = 1
	}
	if size < 10 {
		size = 10
	}
	if size > 100 {
		size = 100
	}
	return (cur - 1) * size, size
}

// PageResult 分页结果。
type PageResult[T any] struct {
	Data  []T   `json:"data"`
	Total int64 `json:"total"`
}

func clauseOrder(col string, desc bool) clause.OrderByColumn {
	return clause.OrderByColumn{Column: clause.Column{Name: col}, Desc: desc}
}

var couponSortable = []string{"id", "name", "code", "type", "value", "show", "limit_use", "started_at", "ended_at", "created_at", "updated_at"}

// AdminCoupons 优惠券列表（对应原项目 admin/coupon/fetch）。排序字段只允许白名单内的列。
func (s *Service) AdminCoupons(ctx context.Context, page Page, sort, sortType string) (*PageResult[model.Coupon], error) {
	if !slices.Contains(couponSortable, sort) {
		sort = "id"
	}
	desc := !strings.EqualFold(sortType, "ASC")
	db := s.DB.WithContext(ctx).Model(&model.Coupon{})
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}
	offset, limit := page.normalize()
	var rows []model.Coupon
	q := s.DB.WithContext(ctx).Order(clauseOrder(sort, desc)).Offset(offset).Limit(limit)
	if err := q.Find(&rows).Error; err != nil {
		return nil, err
	}
	return &PageResult[model.Coupon]{Data: rows, Total: total}, nil
}

// ToggleCoupon 切换优惠券的显示（启用）状态（对应原项目 admin/coupon/show）。
func (s *Service) ToggleCoupon(ctx context.Context, id int64) error {
	db := s.DB.WithContext(ctx)
	var c model.Coupon
	if err := db.Select("id", "show").Take(&c, id).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return errAdminCouponNone
	} else if err != nil {
		return err
	}
	return db.Model(&c).Update("show", 1-c.Show).Error
}

// CouponInput 优惠券保存参数。按金额时 value 单位为分，按比例时为百分数。
type CouponInput struct {
	ID               int64         `json:"id"`
	Code             string        `json:"code"`
	Name             string        `json:"name"`
	Type             int           `json:"type"`
	Value            int64         `json:"value"`
	StartedAt        int64         `json:"started_at"`
	EndedAt          int64         `json:"ended_at"`
	LimitUse         *int64        `json:"limit_use"`
	LimitUseWithUser *int64        `json:"limit_use_with_user"`
	LimitPlanIDs     model.IntList `json:"limit_plan_ids"`
	LimitPeriod      model.StrList `json:"limit_period"`
}

func (in *CouponInput) validate() error {
	in.Name, in.Code = strings.TrimSpace(in.Name), strings.TrimSpace(in.Code)
	if in.Name == "" || (in.Type != CouponAmount && in.Type != CouponPercent) || in.StartedAt <= 0 || in.EndedAt <= 0 {
		return errAdminInvalid
	}
	for _, p := range in.LimitPeriod {
		if !validPeriod(p) {
			return errAdminInvalid
		}
	}
	return nil
}

func (in *CouponInput) model() model.Coupon {
	return model.Coupon{
		Code: in.Code, Name: in.Name, Type: in.Type, Value: in.Value, StartedAt: in.StartedAt, EndedAt: in.EndedAt,
		LimitUse: in.LimitUse, LimitUseWithUser: in.LimitUseWithUser, LimitPlanIDs: in.LimitPlanIDs, LimitPeriod: in.LimitPeriod,
	}
}

// SaveCoupon 新建（ID 为 0）或修改优惠券（对应原项目 admin/coupon/generate 单个生成）。
// 新建时未填券码则随机生成 8 位；新建的优惠券默认不启用（与原项目一致）。券码不能与其他优惠券重复。
func (s *Service) SaveCoupon(ctx context.Context, in CouponInput) error {
	if err := in.validate(); err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if in.ID != 0 {
			var old model.Coupon
			if err := tx.Take(&old, in.ID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
				return errAdminCouponNone
			} else if err != nil {
				return err
			}
			if in.Code == "" {
				in.Code = old.Code
			}
		} else if in.Code == "" {
			in.Code = util.RandomChar(8, false)
		}
		var n int64
		if err := tx.Model(&model.Coupon{}).Where("code = ? AND id <> ?", in.Code, in.ID).Count(&n).Error; err != nil {
			return err
		}
		if n > 0 {
			return errAdminCouponDup
		}
		c := in.model()
		if in.ID == 0 {
			return tx.Create(&c).Error
		}
		c.ID = in.ID
		return tx.Model(&c).Select("code", "name", "type", "value", "started_at", "ended_at", "limit_use",
			"limit_use_with_user", "limit_plan_ids", "limit_period").Updates(&c).Error
	})
}

// GenerateCoupons 批量生成优惠券（最多 500 个，默认启用），返回 CSV 内容（对应原项目 admin/coupon/generate 批量生成）。
func (s *Service) GenerateCoupons(ctx context.Context, in CouponInput, count int) ([]byte, error) {
	if count < 1 || count > 500 {
		return nil, errAdminInvalid
	}
	if err := in.validate(); err != nil {
		return nil, err
	}
	rows := make([]model.Coupon, 0, count)
	for i := 0; i < count; i++ {
		c := in.model()
		c.Code, c.Show = util.RandomChar(8, false), 1
		rows = append(rows, c)
	}
	if err := s.DB.WithContext(ctx).CreateInBatches(rows, 100).Error; err != nil {
		return nil, err
	}

	var buf bytes.Buffer
	w := csv.NewWriter(&buf)
	w.UseCRLF = true
	_ = w.Write([]string{"名称", "类型", "金额或比例", "开始时间", "结束时间", "可用次数", "可用于订阅", "券码", "生成时间"})
	const layout = "2006-01-02 15:04:05"
	for _, c := range rows {
		typ, value := "比例", strconv.FormatInt(c.Value, 10)
		if c.Type == CouponAmount {
			typ, value = "金额", strconv.FormatFloat(float64(c.Value)/100, 'f', -1, 64)
		}
		limitUse := "不限制"
		if c.LimitUse != nil {
			limitUse = strconv.FormatInt(*c.LimitUse, 10)
		}
		plans := "不限制"
		if c.LimitPlanIDs != nil {
			ids := make([]string, 0, len(c.LimitPlanIDs))
			for _, id := range c.LimitPlanIDs {
				ids = append(ids, strconv.FormatInt(id, 10))
			}
			plans = strings.Join(ids, "/")
		}
		_ = w.Write([]string{c.Name, typ, value, time.Unix(c.StartedAt, 0).Format(layout), time.Unix(c.EndedAt, 0).Format(layout),
			limitUse, plans, c.Code, time.Unix(c.CreatedAt, 0).Format(layout)})
	}
	w.Flush()
	return buf.Bytes(), w.Error()
}

// DeleteCoupon 删除优惠券。
func (s *Service) DeleteCoupon(ctx context.Context, id int64) error {
	res := s.DB.WithContext(ctx).Delete(&model.Coupon{}, id)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		return errAdminCouponNone
	}
	return nil
}

// ---------- 订单 ----------

// OrderFilter 订单筛选条件。Condition 为 >、<、=、>=、<=、!=、模糊。
type OrderFilter struct {
	Key       string `json:"key"`
	Condition string `json:"condition"`
	Value     string `json:"value"`
}

// OrderQuery 管理端订单查询参数。
type OrderQuery struct {
	Page
	IsCommission bool          `json:"is_commission"`
	Filter       []OrderFilter `json:"filter"`
}

// AdminOrder 管理端订单，附带套餐名称。
type AdminOrder struct {
	model.Order
	PlanName string `json:"plan_name,omitempty"`
}

var (
	orderFilterKeys = []string{"email", "trade_no", "status", "commission_status", "user_id", "invite_user_id",
		"callback_no", "commission_balance", "subscription_id", "plan_id"}
	// orderNumericKeys 整数列：值必须是整数，且不支持模糊匹配（PostgreSQL 不能对整数列使用 LIKE）。
	orderNumericKeys = []string{"status", "commission_status", "user_id", "invite_user_id", "commission_balance", "subscription_id", "plan_id"}
	filterConditions = map[string]string{">": ">", "<": "<", "=": "=", ">=": ">=", "<=": "<=", "!=": "<>", "模糊": "LIKE"}
)

// AdminOrders 订单列表（对应原项目 admin/order/fetch）。筛选字段与条件只允许白名单内的值。
// email 按邮箱模糊匹配用户（原项目此处缺少 like，实际无法匹配）。
func (s *Service) AdminOrders(ctx context.Context, in OrderQuery) (*PageResult[AdminOrder], error) {
	db := s.DB.WithContext(ctx)
	q := db.Model(&model.Order{})
	if in.IsCommission {
		q = q.Where("invite_user_id IS NOT NULL AND status NOT IN ? AND commission_balance > 0",
			[]int{model.OrderStatusPending, model.OrderStatusCancelled})
	}
	for _, f := range in.Filter {
		op, ok := filterConditions[f.Condition]
		if !ok || !slices.Contains(orderFilterKeys, f.Key) {
			return nil, errAdminInvalid
		}
		if f.Key == "email" {
			users := db.Model(&model.User{}).Select("id").Where("LOWER(email) LIKE ?", "%"+strings.ToLower(f.Value)+"%")
			q = q.Where("user_id IN (?)", users)
			continue
		}
		var v any = f.Value
		if slices.Contains(orderNumericKeys, f.Key) {
			n, err := strconv.ParseInt(f.Value, 10, 64)
			if err != nil || op == "LIKE" {
				return nil, errAdminInvalid
			}
			v = n
		} else if op == "LIKE" {
			v = "%" + f.Value + "%"
		}
		q = q.Where(f.Key+" "+op+" ?", v)
	}
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	offset, limit := in.Page.normalize()
	var orders []model.Order
	if err := q.Order("created_at DESC").Order("id DESC").Offset(offset).Limit(limit).Find(&orders).Error; err != nil {
		return nil, err
	}
	names, err := planNames(db, orders)
	if err != nil {
		return nil, err
	}
	out := make([]AdminOrder, 0, len(orders))
	for _, o := range orders {
		out = append(out, AdminOrder{Order: o, PlanName: names[o.PlanID]})
	}
	return &PageResult[AdminOrder]{Data: out, Total: total}, nil
}

// AdminOrderDetail 管理端订单详情，附带佣金记录与被折抵的订单。
type AdminOrderDetail struct {
	model.Order
	CommissionLog []model.CommissionLog `json:"commission_log"`
	SurplusOrders []model.Order         `json:"surplus_orders"`
}

func (s *Service) adminOrder(db *gorm.DB, tradeNo string) (*model.Order, error) {
	var o model.Order
	err := db.Where("trade_no = ?", tradeNo).Take(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errAdminOrderNone
	}
	return &o, err
}

// AdminDetail 订单详情（对应原项目 admin/order/detail）。
func (s *Service) AdminDetail(ctx context.Context, tradeNo string) (*AdminOrderDetail, error) {
	db := s.DB.WithContext(ctx)
	o, err := s.adminOrder(db, tradeNo)
	if err != nil {
		return nil, err
	}
	d := &AdminOrderDetail{Order: *o, CommissionLog: []model.CommissionLog{}, SurplusOrders: []model.Order{}}
	if err := db.Where("trade_no = ?", o.TradeNo).Find(&d.CommissionLog).Error; err != nil {
		return nil, err
	}
	if len(o.SurplusOrderIDs) > 0 {
		if err := db.Where("id IN ?", []int64(o.SurplusOrderIDs)).Find(&d.SurplusOrders).Error; err != nil {
			return nil, err
		}
	}
	return d, nil
}

// AdminPaid 手动标记待支付订单为已支付（对应原项目 admin/order/paid）。
func (s *Service) AdminPaid(ctx context.Context, tradeNo string) error {
	o, err := s.adminOrder(s.DB.WithContext(ctx), tradeNo)
	if err != nil {
		return err
	}
	if o.Status != model.OrderStatusPending {
		return errAdminOnlyPending
	}
	return s.Paid(ctx, o.TradeNo, "manual_operation")
}

// AdminCancel 取消待支付订单（对应原项目 admin/order/cancel）。
func (s *Service) AdminCancel(ctx context.Context, tradeNo string) error {
	o, err := s.adminOrder(s.DB.WithContext(ctx), tradeNo)
	if err != nil {
		return err
	}
	if o.Status != model.OrderStatusPending {
		return errAdminOnlyPending
	}
	ok, err := s.cancel(ctx, o)
	if err != nil {
		return err
	}
	if !ok {
		return errAdminOnlyPending
	}
	return nil
}

// SetCommissionStatus 修改佣金状态，只允许 0 待确认、1 发放中、3 无效（对应原项目 admin/order/update）。
func (s *Service) SetCommissionStatus(ctx context.Context, tradeNo string, status int) error {
	if status != model.CommissionStatusPending && status != model.CommissionStatusIssuing && status != model.CommissionStatusInvalid {
		return errAdminInvalid
	}
	db := s.DB.WithContext(ctx)
	o, err := s.adminOrder(db, tradeNo)
	if err != nil {
		return err
	}
	return db.Model(o).Update("commission_status", status).Error
}

// AssignInput 手动分配订单参数。SubscriptionID 为 0 表示新开订阅。
type AssignInput struct {
	Email          string `json:"email"`
	PlanID         int64  `json:"plan_id"`
	Period         string `json:"period"`
	TotalAmount    int64  `json:"total_amount"`
	SubscriptionID int64  `json:"subscription_id"`
}

// Assign 为用户创建待支付订单（对应原项目 admin/order/assign），返回订单号。
// 订单类型判断与用户下单一致（决策 D24），不计算折抵；管理员分配不受订阅数量上限约束。
func (s *Service) Assign(ctx context.Context, in AssignInput) (string, error) {
	if in.Email == "" || in.PlanID == 0 || in.TotalAmount < 0 || in.Period == PeriodDeposit || !validPeriod(in.Period) {
		return "", errAdminInvalid
	}
	if in.Period == PeriodReset && in.SubscriptionID == 0 {
		return "", errAdminResetNeedSub
	}
	db := s.DB.WithContext(ctx)
	var u model.User
	if err := database.WhereEmail(db, in.Email).Select("id").Take(&u).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return "", errAdminUserNone
	} else if err != nil {
		return "", err
	}
	var plan model.Plan
	if err := db.Select("id").Take(&plan, in.PlanID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return "", errAdminPlanNotExist
	} else if err != nil {
		return "", err
	}
	var created *model.Order
	err := db.Transaction(func(tx *gorm.DB) error {
		user, err := lockUser(tx, u.ID)
		if err != nil {
			return err
		}
		if pending, err := hasUnfinished(tx, u.ID); err != nil {
			return err
		} else if pending {
			return errAdminUserPending
		}
		o := &model.Order{UserID: u.ID, PlanID: plan.ID, Period: in.Period, TradeNo: util.GUID()}
		now := s.now().Unix()
		var sub *model.Subscription
		if in.SubscriptionID != 0 {
			if sub, err = s.Subscriptions.Find(ctx, tx, u.ID, in.SubscriptionID); err != nil {
				return err
			}
			id := sub.ID
			o.SubscriptionID = &id
		}
		d := &draft{planID: plan.ID, period: in.Period, total: float64(in.TotalAmount)}
		switch {
		case in.Period == PeriodReset:
			d.typ = model.OrderTypeReset
		case sub != nil && sub.PlanID != nil && *sub.PlanID != plan.ID && (sub.ExpiredAt == nil || *sub.ExpiredAt > now):
			d.typ = model.OrderTypeUpgrade
		case sub != nil && sub.ExpiredAt != nil && *sub.ExpiredAt > now && sub.PlanID != nil && *sub.PlanID == plan.ID:
			d.typ = model.OrderTypeRenew
		default:
			d.typ = model.OrderTypeNew
		}
		if err := s.setInvite(tx, user, d); err != nil {
			return err
		}
		d.fill(o)
		if err := tx.Create(o).Error; err != nil {
			return err
		}
		created = o
		return nil
	})
	if err != nil {
		return "", err
	}
	s.emitOrder(ctx, hook.EventOrderCreated, created)
	return created.TradeNo, nil
}
