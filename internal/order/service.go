// Package order 套餐、订单与优惠券，对应原项目 OrderService、CouponService、PlanService
// 及用户端 / 管理端的 Order、Plan、Coupon 控制器。多订阅后订单作用于指定订阅（决策 D6）。
package order

import (
	"context"
	"errors"
	"net/http"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/balance"
	"gvboard/internal/hook"
	"gvboard/internal/model"
	"gvboard/internal/setting"
	"gvboard/internal/subscription"
	"gvboard/internal/util"
)

// Payer 在线支付（由 payment 包实现）。返回值 type、data 的含义与原项目 checkout 接口一致。
type Payer interface {
	Pay(ctx context.Context, o *model.Order, in PayInput) (typ int, data any, err error)
}

// PayInput 结算参数。Request、ClientIP 供支付网关生成回调 / 跳转地址、识别终端类型。
type PayInput struct {
	Method   int64         `json:"method"`
	Token    string        `json:"token"`
	Request  *http.Request `json:"-"`
	ClientIP string        `json:"-"`
}

// Service 订单服务。
type Service struct {
	DB            *gorm.DB
	Settings      *setting.Service
	Subscriptions *subscription.Service
	// Dispatch 投递订单处理任务（开通 / 超时取消），为 nil 时只依赖定时任务兜底。
	Dispatch func(ctx context.Context, tradeNo string) error
	Payer    Payer
	// Hooks 插件事件与下单金额过滤器，可为 nil。
	Hooks hook.Hooks
	Log   *zap.Logger
	Now   func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *zap.Logger {
	if s.Log != nil {
		return s.Log
	}
	return zap.NewNop()
}

// draft 下单过程中的金额。原项目金额以小数参与计算，这里同样保留小数，写入订单时再四舍五入到分。
type draft struct {
	preview      bool
	tradeNo      string
	planID       int64
	period       string
	subID        *int64
	couponID     *int64
	typ          int
	price        int64   // 原价
	coupon       float64 // 优惠券减免
	userOff      float64 // 会员折扣
	items        []hook.AmountItem
	total        float64
	discount     *float64
	surplus      *float64
	refund       *float64
	balance      *float64
	commission   float64
	surplusIDs   []int64
	inviteUserID *int64
}

func (d *draft) fill(o *model.Order) {
	o.Type = d.typ
	o.TotalAmount = cents(d.total)
	o.DiscountAmount = centsPtr(d.discount)
	o.SurplusAmount = centsPtr(d.surplus)
	o.RefundAmount = centsPtr(d.refund)
	o.BalanceAmount = centsPtr(d.balance)
	o.SurplusOrderIDs = d.surplusIDs
	o.InviteUserID = d.inviteUserID
	o.CommissionBalance = cents(d.commission)
}

// CreateInput 下单参数。PlanID 为 0 表示充值；SubscriptionID 为 0 表示新开订阅，否则续费 / 升级 / 重置指定订阅。
type CreateInput struct {
	PlanID         int64  `json:"plan_id"`
	Period         string `json:"period"`
	CouponCode     string `json:"coupon_code"`
	DepositAmount  int64  `json:"deposit_amount"`
	SubscriptionID int64  `json:"subscription_id"`
	// Immediate 同套餐续费时立即生效，其他订单类型忽略（决策 D68）。
	Immediate bool `json:"immediate"`
}

// Create 下单（对应原项目 user/order/save），返回订单号。校验顺序与原项目一致。
// 整个过程锁定用户行：同一用户的并发下单串行执行，未完成订单检查与余额扣减都不会被绕过。
func (s *Service) Create(ctx context.Context, userID int64, in CreateInput) (string, error) {
	if in.Period == "" {
		return "", errPeriodRequired
	}
	if !validPeriod(in.Period) {
		return "", errWrongPeriod
	}
	var created *model.Order
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		u, err := lockUser(tx, userID)
		if err != nil {
			return err
		}
		if pending, err := hasUnfinished(tx, userID); err != nil {
			return err
		} else if pending {
			return errPending
		}
		o := &model.Order{UserID: userID, PlanID: in.PlanID, TradeNo: util.GenerateOrderNo()}
		var items []hook.AmountItem
		if in.PlanID == 0 {
			if in.DepositAmount <= 0 {
				return errDepositMin
			}
			if in.DepositAmount >= 9999999 {
				return errDepositMax
			}
			d := &draft{period: PeriodDeposit, typ: model.OrderTypeDeposit, total: float64(in.DepositAmount)}
			if err := s.setInvite(tx, u, d); err != nil {
				return err
			}
			o.Period = PeriodDeposit
			d.fill(o)
		} else if items, err = s.createPlanOrder(ctx, tx, u, in, o); err != nil {
			return err
		}
		if err := tx.Create(o).Error; err != nil {
			return err
		}
		if len(items) > 0 {
			rows := make([]model.OrderAdjustment, len(items))
			for i, it := range items {
				rows[i] = model.OrderAdjustment{OrderID: o.ID, Plugin: it.Plugin, Label: it.Label, Amount: it.Amount}
			}
			if err := tx.Create(&rows).Error; err != nil {
				return err
			}
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

// createPlanOrder 计算套餐订单金额、扣减优惠券次数与余额并填入 o，返回插件的调整明细。
func (s *Service) createPlanOrder(ctx context.Context, tx *gorm.DB, u *model.User, in CreateInput, o *model.Order) ([]hook.AmountItem, error) {
	d := &draft{tradeNo: o.TradeNo}
	if err := s.quotePlan(ctx, tx, u, in, d); err != nil {
		return nil, err
	}
	if err := s.setInvite(tx, u, d); err != nil {
		return nil, err
	}
	o.Period, o.SubscriptionID, o.CouponID = in.Period, d.subID, d.couponID
	d.fill(o)
	if in.Immediate && o.Type == model.OrderTypeRenew && in.Period != PeriodOnetime {
		o.Immediate = 1
	}
	return d.items, nil
}

// quotePlan 校验并计算套餐订单金额，结果写入 d（下单与试算共用）。
// d.preview 为 true 时只读取：不锁定优惠券、不扣减优惠券次数与余额。
func (s *Service) quotePlan(ctx context.Context, tx *gorm.DB, u *model.User, in CreateInput, d *draft) error {
	now := s.now().Unix()
	var plan model.Plan
	if err := tx.Take(&plan, in.PlanID).Error; errors.Is(err, gorm.ErrRecordNotFound) {
		return errPlanNotExist
	} else if err != nil {
		return err
	}
	var sub *model.Subscription
	if in.SubscriptionID != 0 {
		var err error
		if sub, err = s.Subscriptions.Find(ctx, tx, u.ID, in.SubscriptionID); err != nil {
			return err
		}
	}
	samePlan := sub != nil && sub.PlanID != nil && *sub.PlanID == plan.ID
	reset := in.Period == PeriodReset

	if !samePlan && !reset {
		ok, err := s.haveCapacity(tx, &plan, now)
		if err != nil {
			return err
		}
		if !ok {
			return errSoldOut
		}
	}
	price := Price(&plan, in.Period)
	if price == nil {
		return errPeriodUnavailable
	}
	if reset && (sub == nil || !subscription.Available(sub, now) || !samePlan) {
		return errResetUnavailable
	}
	if !reset && ((plan.Show == 0 && plan.Renew == 0) || (plan.Show == 0 && !samePlan)) {
		return errPlanSoldOut
	}
	if !reset && plan.Renew == 0 && samePlan {
		return errCannotRenew
	}
	if plan.Show == 0 && plan.Renew != 0 && (sub == nil || !subscription.Available(sub, now)) {
		return errPlanExpired
	}
	if sub == nil {
		if err := s.Subscriptions.CheckLimit(tx, u.ID); err != nil {
			return err
		}
	} else {
		id := sub.ID
		d.subID = &id
	}

	d.planID, d.period, d.price, d.total = plan.ID, in.Period, *price, float64(*price)
	if in.CouponCode != "" {
		id, err := s.useCoupon(tx, in.CouponCode, d, u.ID)
		if err != nil {
			return err
		}
		d.couponID = &id
	}
	// 会员折扣（原项目 setVipDiscount）：按原价计算，与优惠券叠加。
	// 叠加后最多减到 0：原项目没有上限，金额为负时升级订单会把负数部分计入退款、加到余额。
	if u.Discount != nil && *u.Discount != 0 {
		v := 0.0
		if d.discount != nil {
			v = *d.discount
		}
		d.userOff = min(d.total*(float64(*u.Discount)/100), d.total-v)
		v += d.userOff
		d.discount = &v
	}
	if d.discount != nil {
		d.total -= *d.discount
	}
	if err := s.setOrderType(tx, sub, d, now); err != nil {
		return err
	}
	d.total = float64(cents(d.total))
	if s.Hooks != nil {
		v := hook.Apply(s.Hooks, ctx, hook.FilterOrderAmount, hook.OrderAmount{
			UserID: u.ID, PlanID: plan.ID, Period: in.Period, SubscriptionID: in.SubscriptionID, Type: d.typ, Amount: int64(d.total),
			Preview: d.preview, Items: []hook.AmountItem{},
		})
		d.total, d.items = float64(v.Amount), v.Items
		// 插件的减免计入订单优惠金额（加价只记录在明细中）
		var off float64
		for _, it := range v.Items {
			if it.Amount < 0 {
				off -= float64(it.Amount)
			}
		}
		if off > 0 {
			if d.discount == nil {
				d.discount = new(float64)
			}
			*d.discount += off
		}
	}
	// 余额抵扣：余额足够时全部用余额支付，否则用完余额后支付剩余部分
	if u.Balance > 0 && d.total > 0 {
		use := min(u.Balance, int64(d.total))
		if !d.preview {
			_, err := balance.Apply(tx, balance.Change{UserID: u.ID, Amount: -use, Type: balance.TypeOrderPay, TradeNo: d.tradeNo})
			if errors.Is(err, balance.ErrInsufficient) {
				return errInsufficient
			}
			if err != nil {
				return err
			}
		}
		b := float64(use)
		d.balance = &b
		d.total -= b
	}
	return nil
}

// setOrderType 判断订单类型（原项目 setOrderType）。sub 为空表示新开订阅，视为新购。
func (s *Service) setOrderType(tx *gorm.DB, sub *model.Subscription, d *draft, now int64) error {
	switch {
	case d.period == PeriodDeposit:
		d.typ = model.OrderTypeDeposit
	case d.period == PeriodReset:
		d.typ = model.OrderTypeReset
	case sub != nil && sub.PlanID != nil && *sub.PlanID != d.planID && (sub.ExpiredAt == nil || *sub.ExpiredAt > now):
		if !s.Settings.Bool("plan_change_enable") {
			return errPlanChangeDisabled
		}
		d.typ = model.OrderTypeUpgrade
		if s.Settings.Bool("surplus_enable") {
			if err := s.surplus(tx, sub, d, now); err != nil {
				return err
			}
		}
		surplus := 0.0
		if d.surplus != nil {
			surplus = *d.surplus
		}
		if surplus >= d.total {
			refund := surplus - d.total
			d.refund = &refund
			d.total = 0
		} else {
			d.total -= surplus
		}
	case sub != nil && sub.ExpiredAt != nil && *sub.ExpiredAt > now && sub.PlanID != nil && *sub.PlanID == d.planID:
		d.typ = model.OrderTypeRenew
	default:
		d.typ = model.OrderTypeNew
	}
	return nil
}

// surplus 升级折抵：只统计目标订阅关联的订单（需求文档 4.2 第 4 条）。
func (s *Service) surplus(tx *gorm.DB, sub *model.Subscription, d *draft, now int64) error {
	if sub.ExpiredAt == nil {
		var last model.Order
		err := tx.Where("subscription_id = ? AND period = ? AND status = ?", sub.ID, PeriodOnetime, model.OrderStatusCompleted).
			Order("id DESC").Take(&last).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		if err != nil {
			return err
		}
		amount := surplusByOneTime(sub, &last)
		if amount == nil {
			return nil
		}
		d.surplus = amount
		return tx.Model(&model.Order{}).Where("subscription_id = ? AND period <> ? AND status = ?", sub.ID, PeriodReset, model.OrderStatusCompleted).
			Pluck("id", &d.surplusIDs).Error
	}
	var orders []model.Order
	err := tx.Where("subscription_id = ? AND period NOT IN ? AND status = ?", sub.ID,
		[]string{PeriodReset, PeriodOnetime, PeriodDeposit}, model.OrderStatusCompleted).Order("id ASC").Find(&orders).Error
	if err != nil {
		return err
	}
	d.surplus, d.surplusIDs = surplusByPeriod(sub, orders, now)
	return nil
}

// setInvite 记录邀请人并计算邀请佣金（原项目 setInvite）。"首次购买"按用户维度判断（需求文档 4.2 第 5 条）。
func (s *Service) setInvite(tx *gorm.DB, u *model.User, d *draft) error {
	if u.InviteUserID != nil && *u.InviteUserID != 0 && d.total <= 0 {
		return nil
	}
	d.inviteUserID = u.InviteUserID
	if u.InviteUserID == nil {
		return nil
	}
	var inviter model.User
	if err := tx.Select("id", "commission_type", "commission_rate").Take(&inviter, *u.InviteUserID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil
		}
		return err
	}
	var give bool
	switch inviter.CommissionType {
	case 0:
		if !s.Settings.Bool("commission_first_time_enable") {
			give = true
		} else {
			had, err := haveValidOrder(tx, u.ID)
			if err != nil {
				return err
			}
			give = !had
		}
	case 1:
		give = true
	case 2:
		had, err := haveValidOrder(tx, u.ID)
		if err != nil {
			return err
		}
		give = !had
	}
	if !give {
		return nil
	}
	rate := float64(s.Settings.Int("invite_commission"))
	if inviter.CommissionRate != nil && *inviter.CommissionRate != 0 {
		rate = float64(*inviter.CommissionRate)
	}
	d.commission = d.total * (rate / 100)
	return nil
}

func lockUser(tx *gorm.DB, id int64) (*model.User, error) {
	var u model.User
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Take(&u, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errUserNotExist
	}
	return &u, err
}

// hasUnfinished 用户是否有待支付或开通中的订单（决策 D21：按用户整体限制）。
func hasUnfinished(tx *gorm.DB, userID int64) (bool, error) {
	var n int64
	err := tx.Model(&model.Order{}).Where("user_id = ? AND status IN ?", userID,
		[]int{model.OrderStatusPending, model.OrderStatusProcessing}).Limit(1).Count(&n).Error
	return n > 0, err
}

// haveValidOrder 用户是否有过已支付的订单（状态不是待支付、已取消）。
func haveValidOrder(tx *gorm.DB, userID int64) (bool, error) {
	var n int64
	err := tx.Model(&model.Order{}).Where("user_id = ? AND status NOT IN ?", userID,
		[]int{model.OrderStatusPending, model.OrderStatusCancelled}).Limit(1).Count(&n).Error
	return n > 0, err
}

func (s *Service) userOrder(ctx context.Context, userID int64, tradeNo string) (*model.Order, error) {
	if tradeNo == "" {
		return nil, errInvalidParams
	}
	var o model.Order
	err := s.DB.WithContext(ctx).Where("trade_no = ? AND user_id = ?", tradeNo, userID).Take(&o).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errOrderNotExist
	}
	return &o, err
}

// Status 订单状态（对应原项目 user/order/check）。
func (s *Service) Status(ctx context.Context, userID int64, tradeNo string) (int, error) {
	o, err := s.userOrder(ctx, userID, tradeNo)
	if err != nil {
		return 0, err
	}
	return o.Status, nil
}

// CheckoutResult 结算结果。Type 为 -1 表示无需支付、已直接开通（与原项目一致）。
type CheckoutResult struct {
	Type int `json:"type"`
	Data any `json:"data"`
}

// Checkout 结算（对应原项目 user/order/checkout）。应付金额为 0 时直接标记为已支付并开通；
// 否则交给在线支付（单元 07）。
func (s *Service) Checkout(ctx context.Context, userID int64, tradeNo string, in PayInput) (*CheckoutResult, error) {
	o, err := s.userOrder(ctx, userID, tradeNo)
	if errors.Is(err, errOrderNotExist) || (err == nil && o.Status != model.OrderStatusPending) {
		return nil, errOrderNotPending
	}
	if err != nil {
		return nil, err
	}
	if o.TotalAmount <= 0 {
		if err := s.Paid(ctx, o.TradeNo, ""); err != nil {
			return nil, err
		}
		return &CheckoutResult{Type: -1, Data: true}, nil
	}
	if s.Payer == nil {
		return nil, errPaymentUnavailable
	}
	typ, data, err := s.Payer.Pay(ctx, o, in)
	if err != nil {
		return nil, err
	}
	return &CheckoutResult{Type: typ, Data: data}, nil
}

// Paid 标记订单已支付并投递开通任务（原项目 OrderService::paid）。订单不是待支付状态时直接返回成功，
// 支付平台重复回调不会重复处理。投递失败时由每分钟的订单检查任务兜底开通。
func (s *Service) Paid(ctx context.Context, tradeNo, callbackNo string) error {
	_, err := s.MarkPaid(ctx, tradeNo, callbackNo)
	return err
}

// MarkPaid 同 Paid，并返回本次调用是否把订单从待支付改为已支付（并发回调时只有一次为 true）。
func (s *Service) MarkPaid(ctx context.Context, tradeNo, callbackNo string) (bool, error) {
	now := s.now().Unix()
	res := s.DB.WithContext(ctx).Model(&model.Order{}).
		Where("trade_no = ? AND status = ?", tradeNo, model.OrderStatusPending).
		Updates(map[string]any{"status": model.OrderStatusProcessing, "paid_at": now, "callback_no": callbackNo})
	if res.Error != nil {
		return false, res.Error
	}
	if res.RowsAffected == 0 {
		return false, nil
	}
	s.emitOrderByNo(ctx, hook.EventOrderPaid, tradeNo)
	s.dispatch(ctx, tradeNo)
	return true, nil
}

func (s *Service) dispatch(ctx context.Context, tradeNo string) {
	if s.Dispatch == nil {
		return
	}
	if err := s.Dispatch(ctx, tradeNo); err != nil {
		s.log().Warn("投递订单处理任务失败，将由定时任务处理", zap.String("trade_no", tradeNo), zap.Error(err))
	}
}

// Cancel 用户取消待支付订单（对应原项目 user/order/cancel）。
func (s *Service) Cancel(ctx context.Context, userID int64, tradeNo string) error {
	o, err := s.userOrder(ctx, userID, tradeNo)
	if err != nil {
		return err
	}
	if o.Status != model.OrderStatusPending {
		return errOnlyCancelPending
	}
	ok, err := s.cancel(ctx, o)
	if err != nil {
		return err
	}
	if !ok {
		return errCancelFailed
	}
	return nil
}

// cancel 取消待支付订单并退回已抵扣的余额（原项目 OrderService::cancel）。订单已不是待支付状态时返回 false。
func (s *Service) cancel(ctx context.Context, o *model.Order) (bool, error) {
	ok := false
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.Order{}).Where("id = ? AND status = ?", o.ID, model.OrderStatusPending).
			Update("status", model.OrderStatusCancelled)
		if res.Error != nil || res.RowsAffected != 1 {
			return res.Error
		}
		if b := val(o.BalanceAmount); b != 0 {
			if _, err := balance.Apply(tx, balance.Change{UserID: o.UserID, Amount: b, Type: balance.TypeOrderCancel, TradeNo: o.TradeNo}); err != nil {
				return err
			}
		}
		ok = true
		return nil
	})
	if ok && err == nil {
		c := *o
		c.Status = model.OrderStatusCancelled
		s.emitOrder(ctx, hook.EventOrderCancelled, &c)
	}
	return ok && err == nil, err
}
