package order

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/config"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/subscription"
	"gvboard/internal/testutil"
	"gvboard/internal/usercenter"
)

type env struct {
	*Service
	auth       *auth.Service
	mu         sync.Mutex
	dispatched []string
}

func newEnv(t *testing.T, driver string) *env {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := &env{auth: auth.New(db, c, "base64:b3JkZXItdGVzdC1rZXktMDEyMzQ1Njc4OWFi")}
	e.Service = &Service{
		DB: db, Settings: st,
		Subscriptions: &subscription.Service{DB: db, Cache: c, Settings: st, Linker: &subscription.Linker{Settings: st, Cache: c}},
	}
	e.Dispatch = func(_ context.Context, no string) error {
		e.mu.Lock()
		e.dispatched = append(e.dispatched, no)
		e.mu.Unlock()
		return nil
	}
	return e
}

func (e *env) set(t *testing.T, kv map[string]any) {
	t.Helper()
	if err := e.Settings.Set(context.Background(), kv); err != nil {
		t.Fatal(err)
	}
}

func (e *env) user(t *testing.T, email string, balance int64) *model.User {
	t.Helper()
	var u *model.User
	if err := e.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		u, _, err = account.Create(tx, account.NewUser{Email: email, Password: "password123"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if balance != 0 {
		e.DB.Model(u).Update("balance", balance)
		u.Balance = balance
	}
	return u
}

func (e *env) plan(t *testing.T, name string, mut func(p *model.Plan)) *model.Plan {
	t.Helper()
	p := &model.Plan{Name: name, GroupID: 1, TransferEnable: 100, Show: 1, Renew: 1,
		MonthPrice: ptr(int64(1000)), DeviceLimit: ptr(int64(2)), SpeedLimit: ptr(int64(50))}
	if mut != nil {
		mut(p)
	}
	if err := e.DB.Create(p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

func (e *env) order(t *testing.T, no string) model.Order {
	t.Helper()
	var o model.Order
	if err := e.DB.Where("trade_no = ?", no).Take(&o).Error; err != nil {
		t.Fatal(err)
	}
	return o
}

func (e *env) sub(t *testing.T, id int64) model.Subscription {
	t.Helper()
	var s model.Subscription
	if err := e.DB.Take(&s, id).Error; err != nil {
		t.Fatal(err)
	}
	return s
}

func (e *env) balance(t *testing.T, uid int64) int64 {
	t.Helper()
	var u model.User
	e.DB.Select("balance").Take(&u, uid)
	return u.Balance
}

// buy 下单、结算并开通，返回订单。应付金额必须为 0（余额足够）。
func (e *env) buy(t *testing.T, uid int64, in CreateInput) model.Order {
	t.Helper()
	ctx := context.Background()
	no, err := e.Create(ctx, uid, in)
	if err != nil {
		t.Fatalf("下单失败: %v", err)
	}
	if _, err := e.Checkout(ctx, uid, no, PayInput{}); err != nil {
		t.Fatalf("结算失败: %v", err)
	}
	if err := e.Handle(ctx, no); err != nil {
		t.Fatalf("开通失败: %v", err)
	}
	o := e.order(t, no)
	if o.Status != model.OrderStatusCompleted {
		t.Fatalf("订单应已完成，实际状态 %d", o.Status)
	}
	return o
}

func wantErr(t *testing.T, err error, want *httpx.Error) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Msg != want.Msg {
		t.Fatalf("期望错误 %q，实际 %v", want.Msg, err)
	}
}

func near(a, b, tol int64) bool { return a-b <= tol && b-a <= tol }

func TestOrder(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			for name, fn := range map[string]func(*testing.T, *env){
				"参数与充值":     testDeposit,
				"新购金额计算":    testNewOrderAmounts,
				"开通新订阅与续费":  testOpenAndRenew,
				"升级折抵":      testUpgrade,
				"重置包与一次性":   testResetAndOnetime,
				"购买限制":      testRestrictions,
				"优惠券":       testCoupon,
				"并发":        testConcurrency,
				"超时取消与幂等":   testTimeoutAndIdempotent,
				"管理端套餐与优惠券": testAdminPlanCoupon,
				"管理端订单":     testAdminOrder,
				"HTTP":      testHTTP,
				"插件事件与金额过滤": testHooks,
			} {
				t.Run(name, func(t *testing.T) { fn(t, newEnv(t, driver)) })
			}
		})
	}
}

func testDeposit(t *testing.T, e *env) {
	ctx := context.Background()
	u := e.user(t, "d@x.com", 0)
	_, err := e.Create(ctx, u.ID, CreateInput{})
	wantErr(t, err, errPeriodRequired)
	_, err = e.Create(ctx, u.ID, CreateInput{Period: "week_price"})
	wantErr(t, err, errWrongPeriod)
	_, err = e.Create(ctx, u.ID, CreateInput{Period: PeriodDeposit})
	wantErr(t, err, errDepositMin)
	_, err = e.Create(ctx, u.ID, CreateInput{Period: PeriodDeposit, DepositAmount: 9999999})
	wantErr(t, err, errDepositMax)

	e.set(t, map[string]any{"deposit_bounus": []string{"10:1", "100:20"}})
	no, err := e.Create(ctx, u.ID, CreateInput{Period: PeriodDeposit, DepositAmount: 1000})
	if err != nil {
		t.Fatal(err)
	}
	o := e.order(t, no)
	if o.Type != model.OrderTypeDeposit || o.PlanID != 0 || o.Period != PeriodDeposit || o.TotalAmount != 1000 || o.SubscriptionID != nil {
		t.Errorf("充值订单不正确: %+v", o)
	}
	d, err := e.Detail(ctx, u.ID, no)
	if err != nil || *d.Bonus != 100 || *d.GetAmount != 1100 || d.Plan.Name != "deposit" {
		t.Errorf("充值订单详情不正确: %+v %v", d, err)
	}
	// 有未完成订单时不能再下单（决策 D21）
	_, err = e.Create(ctx, u.ID, CreateInput{Period: PeriodDeposit, DepositAmount: 1})
	wantErr(t, err, errPending)

	// 未注入在线支付时不可用
	_, err = e.Checkout(ctx, u.ID, no, PayInput{Method: 1})
	wantErr(t, err, errPaymentUnavailable)
	if err := e.Paid(ctx, no, "cb-1"); err != nil {
		t.Fatal(err)
	}
	if err := e.Handle(ctx, no); err != nil {
		t.Fatal(err)
	}
	if o := e.order(t, no); o.Status != model.OrderStatusCompleted || *o.CallbackNo != "cb-1" || o.PaidAt == nil {
		t.Errorf("充值订单开通后状态不正确: %+v", o)
	}
	if b := e.balance(t, u.ID); b != 1100 {
		t.Errorf("充值 10 元赠送 1 元后余额应为 1100，实际 %d", b)
	}
	if list, _ := e.List(ctx, u.ID, nil); len(list) != 1 || list[0].TradeNo != no {
		t.Errorf("订单列表不正确: %+v", list)
	}
	st := model.OrderStatusPending
	if list, _ := e.List(ctx, u.ID, &st); len(list) != 0 {
		t.Error("按状态筛选不正确")
	}
	if s, _ := e.Status(ctx, u.ID, no); s != model.OrderStatusCompleted {
		t.Error("订单状态接口不正确")
	}
	other := e.user(t, "d2@x.com", 0)
	if _, err := e.Status(ctx, other.ID, no); !errors.Is(err, errOrderNotExist) {
		t.Error("不能查看其他用户的订单")
	}
}

func testNewOrderAmounts(t *testing.T, e *env) {
	ctx := context.Background()
	inviter := e.user(t, "inv@x.com", 0)
	u := e.user(t, "n@x.com", 300)
	e.DB.Model(u).Updates(map[string]any{"discount": 10, "invite_user_id": inviter.ID})
	p := e.plan(t, "A", nil)
	e.DB.Create(&model.Coupon{Code: "P20", Name: "x", Type: CouponPercent, Value: 20, Show: 1, LimitUse: ptr(int64(5)),
		StartedAt: time.Now().Unix() - 60, EndedAt: time.Now().Unix() + 3600})

	no, err := e.Create(ctx, u.ID, CreateInput{PlanID: p.ID, Period: PeriodMonth, CouponCode: "P20"})
	if err != nil {
		t.Fatal(err)
	}
	o := e.order(t, no)
	// 优惠券 20% = 200，会员折扣按原价 10% = 100，余额抵扣 300，应付 400，邀请佣金 10% = 40
	if o.Type != model.OrderTypeNew || o.SubscriptionID != nil || val(o.DiscountAmount) != 300 || val(o.BalanceAmount) != 300 ||
		o.TotalAmount != 400 || o.CouponID == nil || o.InviteUserID == nil || *o.InviteUserID != inviter.ID || o.CommissionBalance != 40 {
		t.Errorf("新购订单金额不正确: %+v", o)
	}
	var c model.Coupon
	e.DB.Where("code = ?", "P20").Take(&c)
	if *c.LimitUse != 4 || e.balance(t, u.ID) != 0 {
		t.Errorf("优惠券次数或余额扣减不正确: limit=%d balance=%d", *c.LimitUse, e.balance(t, u.ID))
	}
	// 取消订单退回余额
	if err := e.Cancel(ctx, u.ID, no); err != nil {
		t.Fatal(err)
	}
	if o := e.order(t, no); o.Status != model.OrderStatusCancelled || e.balance(t, u.ID) != 300 {
		t.Errorf("取消后状态或余额不正确: status=%d balance=%d", o.Status, e.balance(t, u.ID))
	}
	wantErr(t, e.Cancel(ctx, u.ID, no), errOnlyCancelPending)

	// 余额足够时全部用余额支付
	e.DB.Model(u).Updates(map[string]any{"balance": 5000, "discount": nil})
	no, _ = e.Create(ctx, u.ID, CreateInput{PlanID: p.ID, Period: PeriodMonth})
	if o := e.order(t, no); o.TotalAmount != 0 || val(o.BalanceAmount) != 1000 || o.DiscountAmount != nil || e.balance(t, u.ID) != 4000 {
		t.Errorf("余额全额支付不正确: %+v balance=%d", o, e.balance(t, u.ID))
	}
	// 应付为 0 的订单不产生邀请佣金，也不记录邀请人（与原项目一致）
	if o := e.order(t, no); o.InviteUserID != nil || o.CommissionBalance != 0 {
		t.Errorf("应付为 0 时不应有佣金: %+v", o)
	}
}

func testOpenAndRenew(t *testing.T, e *env) {
	ctx := context.Background()
	u := e.user(t, "o@x.com", 100000)
	p := e.plan(t, "A", func(p *model.Plan) { p.QuarterPrice = ptr(int64(2700)) })
	e.set(t, map[string]any{"renew_order_event_id": 1})

	start := time.Now().Unix()
	o := e.buy(t, u.ID, CreateInput{PlanID: p.ID, Period: PeriodMonth})
	if len(e.dispatched) != 1 || e.dispatched[0] != o.TradeNo {
		t.Errorf("免支付订单应投递开通任务: %v", e.dispatched)
	}
	if o.SubscriptionID == nil {
		t.Fatal("新开订阅的订单开通后应回填订阅 ID")
	}
	s := e.sub(t, *o.SubscriptionID)
	if s.UserID != u.ID || s.IsPrimary != 0 || *s.PlanID != p.ID || *s.GroupID != 1 || s.TransferEnable != 100*gb ||
		*s.DeviceLimit != 2 || *s.SpeedLimit != 50 || *s.ExpiredAt < addMonths(start, 1) || *s.ExpiredAt > addMonths(start+5, 1) {
		t.Errorf("新开订阅不正确: %+v", s)
	}
	if s.UUID == "" || s.Token == "" || s.RemindExpire != 1 {
		t.Errorf("新开订阅缺少 uuid / token 或默认值: %+v", s)
	}

	// 续费：在原到期时间上延长，续费事件 1 清零已用流量
	e.DB.Model(&s).Updates(map[string]any{"u": 10, "d": 20})
	o2 := e.buy(t, u.ID, CreateInput{PlanID: p.ID, Period: PeriodQuarter, SubscriptionID: s.ID})
	s2 := e.sub(t, s.ID)
	if o2.Type != model.OrderTypeRenew || *s2.ExpiredAt != addMonths(*s.ExpiredAt, 3) || s2.U != 0 || s2.D != 0 {
		t.Errorf("续费结果不正确: type=%d expired=%d u=%d", o2.Type, *s2.ExpiredAt, s2.U)
	}
	// 提前续费不清零流量；立即生效清零流量并扣除本周期剩余时间（决策 D68）
	e.set(t, map[string]any{"renew_order_event_id": 0})
	e.DB.Model(&model.Plan{}).Where("id = ?", p.ID).Update("reset_traffic_method", subscription.ResetNever)
	e.DB.Model(&s2).Updates(map[string]any{"u": 10, "d": 20})
	o3 := e.buy(t, u.ID, CreateInput{PlanID: p.ID, Period: PeriodMonth, SubscriptionID: s.ID})
	s3 := e.sub(t, s.ID)
	if o3.Immediate != 0 || *s3.ExpiredAt != addMonths(*s2.ExpiredAt, 1) || s3.U != 10 || s3.D != 20 {
		t.Errorf("提前续费结果不正确: %+v %+v", o3, s3)
	}
	now := time.Now().Unix()
	o4 := e.buy(t, u.ID, CreateInput{PlanID: p.ID, Period: PeriodMonth, SubscriptionID: s.ID, Immediate: true})
	s4 := e.sub(t, s.ID)
	if o4.Immediate != 1 || s4.U != 0 || s4.D != 0 || !near(*s4.ExpiredAt, addMonths(*s3.ExpiredAt, 1)-(*s3.ExpiredAt-now), 5) {
		t.Errorf("立即生效结果不正确: immediate=%d u=%d expired=%d", o4.Immediate, s4.U, *s4.ExpiredAt)
	}
	// 主订阅不受影响
	var n int64
	e.DB.Model(&model.Subscription{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 2 {
		t.Errorf("应有主订阅与新开订阅共 2 个，实际 %d", n)
	}
	// 新购订单忽略立即生效
	if o5 := e.buy(t, u.ID, CreateInput{PlanID: p.ID, Period: PeriodMonth, Immediate: true}); o5.Immediate != 0 {
		t.Errorf("新购订单不应标记立即生效: %+v", o5)
	}

	// 订单详情
	d, err := e.Detail(ctx, u.ID, o2.TradeNo)
	if err != nil || d.PlanDetail == nil || d.PlanDetail.ID != p.ID || d.Plan.Name != "A" {
		t.Errorf("订单详情不正确: %+v %v", d, err)
	}
}

func testUpgrade(t *testing.T, e *env) {
	ctx := context.Background()
	u := e.user(t, "up@x.com", 100000)
	a := e.plan(t, "A", nil)
	b := e.plan(t, "B", func(p *model.Plan) { p.MonthPrice = ptr(int64(3000)); p.TransferEnable = 200; p.SpeedLimit = nil })
	cheap := e.plan(t, "C", func(p *model.Plan) { p.MonthPrice = ptr(int64(500)) })

	first := e.buy(t, u.ID, CreateInput{PlanID: a.ID, Period: PeriodMonth})
	subID := *first.SubscriptionID
	e.DB.Model(&model.Subscription{}).Where("id = ?", subID).Updates(map[string]any{"u": 1, "d": 2})

	e.set(t, map[string]any{"plan_change_enable": 0})
	_, err := e.Create(ctx, u.ID, CreateInput{PlanID: b.ID, Period: PeriodMonth, SubscriptionID: subID})
	wantErr(t, err, errPlanChangeDisabled)
	e.set(t, map[string]any{"plan_change_enable": 1})

	balanceBefore := e.balance(t, u.ID)
	no, err := e.Create(ctx, u.ID, CreateInput{PlanID: b.ID, Period: PeriodMonth, SubscriptionID: subID})
	if err != nil {
		t.Fatal(err)
	}
	o := e.order(t, no)
	// 刚买的一个月几乎没用，剩余价值约等于实付 1000
	if o.Type != model.OrderTypeUpgrade || !near(val(o.SurplusAmount), 1000, 2) || len(o.SurplusOrderIDs) != 1 || o.SurplusOrderIDs[0] != first.ID {
		t.Fatalf("升级订单不正确: %+v", o)
	}
	if val(o.BalanceAmount)+o.TotalAmount != 3000-val(o.SurplusAmount) || e.balance(t, u.ID) != balanceBefore-val(o.BalanceAmount) {
		t.Errorf("升级订单应付金额不正确: %+v", o)
	}
	e.Checkout(ctx, u.ID, no, PayInput{})
	if err := e.Handle(ctx, no); err != nil {
		t.Fatal(err)
	}
	s := e.sub(t, subID)
	now := time.Now().Unix()
	if *s.PlanID != b.ID || s.TransferEnable != 200*gb || s.SpeedLimit != nil || s.U != 1 ||
		*s.ExpiredAt < addMonths(now-5, 1) || *s.ExpiredAt > addMonths(now, 1) {
		t.Errorf("升级后订阅不正确（升级从现在起算，不清零流量）: %+v", s)
	}
	if e.order(t, first.TradeNo).Status != model.OrderStatusDiscounted {
		t.Error("被折抵的订单应标记为已折抵")
	}

	// 剩余价值超过新套餐价格：应付 0，差额开通时退回余额
	balanceBefore = e.balance(t, u.ID)
	no, _ = e.Create(ctx, u.ID, CreateInput{PlanID: cheap.ID, Period: PeriodMonth, SubscriptionID: subID})
	o = e.order(t, no)
	if o.TotalAmount != 0 || o.RefundAmount == nil || *o.RefundAmount != val(o.SurplusAmount)-500 || val(o.SurplusAmount) < 2900 {
		t.Fatalf("剩余价值超过价格时应退款: %+v", o)
	}
	e.Checkout(ctx, u.ID, no, PayInput{})
	e.Handle(ctx, no)
	if got := e.balance(t, u.ID); got != balanceBefore+*o.RefundAmount {
		t.Errorf("开通后应退回差额 %d，余额 %d -> %d", *o.RefundAmount, balanceBefore, got)
	}

	// 关闭折抵：按原价计费
	e.set(t, map[string]any{"surplus_enable": 0})
	no, _ = e.Create(ctx, u.ID, CreateInput{PlanID: a.ID, Period: PeriodMonth, SubscriptionID: subID})
	if o := e.order(t, no); o.SurplusAmount != nil || val(o.BalanceAmount) != 1000 || o.RefundAmount != nil {
		t.Errorf("关闭折抵后不应计算剩余价值: %+v", o)
	}
}

func testResetAndOnetime(t *testing.T, e *env) {
	ctx := context.Background()
	u := e.user(t, "r@x.com", 100000)
	a := e.plan(t, "A", func(p *model.Plan) { p.ResetPrice = ptr(int64(300)) })
	ot := e.plan(t, "OT", func(p *model.Plan) { p.MonthPrice = nil; p.OnetimePrice = ptr(int64(2000)); p.TransferEnable = 50 })

	_, err := e.Create(ctx, u.ID, CreateInput{PlanID: a.ID, Period: PeriodReset})
	wantErr(t, err, errResetUnavailable)
	first := e.buy(t, u.ID, CreateInput{PlanID: a.ID, Period: PeriodMonth})
	subID := *first.SubscriptionID
	e.DB.Model(&model.Subscription{}).Where("id = ?", subID).Updates(map[string]any{"u": 100, "d": 200})
	before := e.sub(t, subID)
	o := e.buy(t, u.ID, CreateInput{PlanID: a.ID, Period: PeriodReset, SubscriptionID: subID})
	if s := e.sub(t, subID); o.Type != model.OrderTypeReset || s.U != 0 || s.D != 0 || *s.ExpiredAt != *before.ExpiredAt {
		t.Errorf("流量重置包结果不正确: type=%d %+v", o.Type, s)
	}
	_, err = e.Create(ctx, u.ID, CreateInput{PlanID: ot.ID, Period: PeriodMonth})
	wantErr(t, err, errPeriodUnavailable)

	// 一次性套餐：长期有效；再次购买同一一次性套餐时保留剩余流量
	o1 := e.buy(t, u.ID, CreateInput{PlanID: ot.ID, Period: PeriodOnetime})
	s := e.sub(t, *o1.SubscriptionID)
	if s.ExpiredAt != nil || s.TransferEnable != 50*gb {
		t.Fatalf("一次性套餐开通不正确: %+v", s)
	}
	e.DB.Model(&s).Updates(map[string]any{"u": 10 * gb, "d": 0})
	o2 := e.buy(t, u.ID, CreateInput{PlanID: ot.ID, Period: PeriodOnetime, SubscriptionID: s.ID})
	s = e.sub(t, s.ID)
	if o2.Type != model.OrderTypeNew || s.TransferEnable != 90*gb || s.U != 0 {
		t.Errorf("再次购买一次性套餐应叠加剩余流量: type=%d transfer=%d", o2.Type, s.TransferEnable/gb)
	}
	// 一次性套餐升级为周期套餐：按剩余流量折抵最近一次一次性订单
	no, err := e.Create(ctx, u.ID, CreateInput{PlanID: a.ID, Period: PeriodMonth, SubscriptionID: s.ID})
	if err != nil {
		t.Fatal(err)
	}
	if o := e.order(t, no); o.Type != model.OrderTypeUpgrade || val(o.SurplusAmount) != 2000 || len(o.SurplusOrderIDs) != 2 {
		t.Errorf("一次性套餐升级折抵不正确: %+v", o)
	}
}

func testRestrictions(t *testing.T, e *env) {
	ctx := context.Background()
	u := e.user(t, "l@x.com", 100000)
	other := e.user(t, "l2@x.com", 100000)
	_, err := e.Create(ctx, u.ID, CreateInput{PlanID: 99999, Period: PeriodMonth})
	wantErr(t, err, errPlanNotExist)

	// 容量：按有效订阅数统计（决策 D22）
	capped := e.plan(t, "CAP", func(p *model.Plan) { p.CapacityLimit = ptr(int64(1)) })
	o := e.buy(t, u.ID, CreateInput{PlanID: capped.ID, Period: PeriodMonth})
	_, err = e.Create(ctx, other.ID, CreateInput{PlanID: capped.ID, Period: PeriodMonth})
	wantErr(t, err, errSoldOut)
	_, err = e.Create(ctx, u.ID, CreateInput{PlanID: capped.ID, Period: PeriodMonth})
	wantErr(t, err, errSoldOut)
	if _, err := e.Create(ctx, u.ID, CreateInput{PlanID: capped.ID, Period: PeriodMonth, SubscriptionID: *o.SubscriptionID}); err != nil {
		t.Errorf("续费自己正在使用的套餐不受容量限制: %v", err)
	}
	e.DB.Where("user_id = ? AND status = ?", u.ID, model.OrderStatusPending).Delete(&model.Order{})
	plans, _ := e.Plans(ctx)
	for _, p := range plans {
		if p.ID == capped.ID && *p.CapacityLimit != 0 {
			t.Errorf("套餐列表应显示剩余容量 0，实际 %d", *p.CapacityLimit)
		}
	}

	// 停售但允许续费：只能续费正在使用的订阅
	hidden := e.plan(t, "H", func(p *model.Plan) { p.Show = 0 })
	_, err = e.Create(ctx, u.ID, CreateInput{PlanID: hidden.ID, Period: PeriodMonth})
	wantErr(t, err, errPlanSoldOut)
	if _, err := e.Plan(ctx, u.ID, hidden.ID); !errors.Is(err, errPlanNotExist) {
		t.Error("停售且未使用的套餐详情应不可见")
	}
	sub := e.sub(t, *o.SubscriptionID)
	e.DB.Model(&sub).Update("plan_id", hidden.ID)
	if _, err := e.Plan(ctx, u.ID, hidden.ID); err != nil {
		t.Errorf("正在使用的停售套餐应可见: %v", err)
	}
	if _, err := e.Create(ctx, u.ID, CreateInput{PlanID: hidden.ID, Period: PeriodMonth, SubscriptionID: sub.ID}); err != nil {
		t.Errorf("停售套餐应允许续费: %v", err)
	}
	e.DB.Where("user_id = ? AND status = ?", u.ID, model.OrderStatusPending).Delete(&model.Order{})
	e.DB.Model(&sub).Update("expired_at", time.Now().Unix()-10)
	_, err = e.Create(ctx, u.ID, CreateInput{PlanID: hidden.ID, Period: PeriodMonth, SubscriptionID: sub.ID})
	wantErr(t, err, errPlanExpired)

	// 不允许续费
	noRenew := e.plan(t, "NR", func(p *model.Plan) { p.Renew = 0 })
	o = e.buy(t, u.ID, CreateInput{PlanID: noRenew.ID, Period: PeriodMonth})
	_, err = e.Create(ctx, u.ID, CreateInput{PlanID: noRenew.ID, Period: PeriodMonth, SubscriptionID: *o.SubscriptionID})
	wantErr(t, err, errCannotRenew)

	// 订阅数量上限只在下单时检查；不能指定其他用户的订阅
	e.DB.Model(u).Update("subscription_limit", 3)
	_, err = e.Create(ctx, u.ID, CreateInput{PlanID: noRenew.ID, Period: PeriodMonth})
	if !errors.Is(err, subscription.ErrLimit) {
		t.Errorf("超过订阅上限应不能新开，实际 %v", err)
	}
	var otherSub model.Subscription
	e.DB.Where("user_id = ?", other.ID).Take(&otherSub)
	if _, err := e.Create(ctx, u.ID, CreateInput{PlanID: noRenew.ID, Period: PeriodMonth, SubscriptionID: otherSub.ID}); !errors.Is(err, subscription.ErrNotFound) {
		t.Errorf("不能为其他用户的订阅下单，实际 %v", err)
	}
	if b := e.balance(t, u.ID); b != 100000-4000 {
		t.Errorf("失败的下单不应扣减余额，余额 %d", b)
	}
}

func testCoupon(t *testing.T, e *env) {
	ctx := context.Background()
	u := e.user(t, "c@x.com", 0)
	p := e.plan(t, "A", nil)
	p2 := e.plan(t, "B", nil)
	now := time.Now().Unix()
	mk := func(code string, mut func(c *model.Coupon)) {
		c := model.Coupon{Code: code, Name: code, Type: CouponAmount, Value: 300, Show: 1, StartedAt: now - 60, EndedAt: now + 3600}
		if mut != nil {
			mut(&c)
		}
		e.DB.Create(&c)
	}
	check := func(code string, plan int64, period string) error {
		_, err := e.CheckCoupon(ctx, u.ID, CouponCheckInput{Code: code, PlanID: plan, Period: period})
		return err
	}
	wantErr(t, check("", 0, ""), errCouponRequired)
	wantErr(t, check("NONE", 0, ""), errCouponInvalid)
	mk("HIDE", func(c *model.Coupon) { c.Show = 0 })
	wantErr(t, check("HIDE", 0, ""), errCouponInvalid)
	mk("ZERO", func(c *model.Coupon) { c.LimitUse = ptr(int64(0)) })
	wantErr(t, check("ZERO", 0, ""), errCouponUsedUp)
	mk("LATER", func(c *model.Coupon) { c.StartedAt = now + 600 })
	wantErr(t, check("LATER", 0, ""), errCouponNotStarted)
	mk("OLD", func(c *model.Coupon) { c.EndedAt = now - 1 })
	wantErr(t, check("OLD", 0, ""), errCouponExpired)
	mk("PLAN", func(c *model.Coupon) { c.LimitPlanIDs = model.IntList{p.ID} })
	wantErr(t, check("PLAN", p2.ID, ""), errCouponPlan)
	if err := check("PLAN", 0, ""); err != nil {
		t.Errorf("未指定套餐时不检查套餐限制: %v", err)
	}
	mk("YEAR", func(c *model.Coupon) { c.LimitPeriod = model.StrList{PeriodYear} })
	wantErr(t, check("YEAR", p.ID, PeriodMonth), errCouponPeriod)
	mk("ONCE", func(c *model.Coupon) { c.LimitUseWithUser = ptr(int64(1)) })
	v, err := e.CheckCoupon(ctx, u.ID, CouponCheckInput{Code: "ONCE", PlanID: p.ID})
	if err != nil || v.Value != 300 || v.Type != CouponAmount {
		t.Fatalf("优惠券校验结果不正确: %+v %v", v, err)
	}
	// 金额券超过价格时按价格优惠
	mk("BIG", func(c *model.Coupon) { c.Value = 5000 })
	no, err := e.Create(ctx, u.ID, CreateInput{PlanID: p.ID, Period: PeriodMonth, CouponCode: "BIG"})
	if err != nil {
		t.Fatal(err)
	}
	if o := e.order(t, no); val(o.DiscountAmount) != 1000 || o.TotalAmount != 0 {
		t.Errorf("优惠金额不应超过价格: %+v", o)
	}
	e.Cancel(ctx, u.ID, no)
	// 每人限用：已支付的订单计入次数
	no, _ = e.Create(ctx, u.ID, CreateInput{PlanID: p.ID, Period: PeriodMonth, CouponCode: "ONCE"})
	e.Paid(ctx, no, "")
	e.Handle(ctx, no)
	err = check("ONCE", p.ID, "")
	wantErr(t, err, errCouponPerUser)
	var he *httpx.Error
	errors.As(err, &he)
	if he.Params["limit_use_with_user"] != int64(1) {
		t.Errorf("提示应包含每人限用次数: %v", he.Params)
	}
	_, err = e.Create(ctx, u.ID, CreateInput{PlanID: p.ID, Period: PeriodMonth, CouponCode: "ONCE"})
	wantErr(t, err, errCouponPerUser)
}

func testConcurrency(t *testing.T, e *env) {
	ctx := context.Background()
	p := e.plan(t, "A", nil)
	e.DB.Create(&model.Coupon{Code: "ONE", Name: "x", Type: CouponAmount, Value: 100, Show: 1, LimitUse: ptr(int64(1)),
		StartedAt: time.Now().Unix() - 60, EndedAt: time.Now().Unix() + 3600})

	run := func(n int, fn func(i int) error) (ok int) {
		var wg sync.WaitGroup
		var mu sync.Mutex
		for i := 0; i < n; i++ {
			wg.Add(1)
			go func(i int) {
				defer wg.Done()
				if fn(i) == nil {
					mu.Lock()
					ok++
					mu.Unlock()
				}
			}(i)
		}
		wg.Wait()
		return ok
	}
	// 只能用一次的优惠券被多人同时使用
	var users []*model.User
	for i := 0; i < 5; i++ {
		users = append(users, e.user(t, fmt.Sprintf("cc%d@x.com", i), 0))
	}
	if ok := run(5, func(i int) error {
		_, err := e.Create(ctx, users[i].ID, CreateInput{PlanID: p.ID, Period: PeriodMonth, CouponCode: "ONE"})
		return err
	}); ok != 1 {
		t.Errorf("只能用一次的优惠券并发使用成功 %d 次", ok)
	}
	// 同一用户并发下单：只能成功一笔，余额只扣一次
	u := e.user(t, "same@x.com", 1500)
	if ok := run(5, func(int) error {
		_, err := e.Create(ctx, u.ID, CreateInput{PlanID: p.ID, Period: PeriodMonth})
		return err
	}); ok != 1 || e.balance(t, u.ID) != 500 {
		t.Errorf("同一用户并发下单成功 %d 笔，余额 %d", ok, e.balance(t, u.ID))
	}
	// 同一订单并发开通只开通一次
	var o model.Order
	e.DB.Where("user_id = ?", u.ID).Take(&o)
	e.Paid(ctx, o.TradeNo, "")
	run(5, func(int) error { return e.Handle(ctx, o.TradeNo) })
	var subs []model.Subscription
	e.DB.Where("user_id = ? AND plan_id = ?", u.ID, p.ID).Find(&subs)
	if len(subs) != 1 {
		t.Errorf("并发开通应只新开 1 个订阅，实际 %d", len(subs))
	}
}

func testTimeoutAndIdempotent(t *testing.T, e *env) {
	ctx := context.Background()
	u := e.user(t, "t@x.com", 600)
	p := e.plan(t, "A", nil)
	no, _ := e.Create(ctx, u.ID, CreateInput{PlanID: p.ID, Period: PeriodMonth})
	e.Handle(ctx, no)
	if e.order(t, no).Status != model.OrderStatusPending {
		t.Fatal("未超时的订单不应取消")
	}
	e.DB.Model(&model.Order{}).Where("trade_no = ?", no).Update("created_at", time.Now().Unix()-pendingTimeout-1)
	// 定时检查：投递超时订单
	if err := e.CheckJob().Run(ctx); err != nil {
		t.Fatal(err)
	}
	if len(e.dispatched) != 1 || e.dispatched[0] != no {
		t.Errorf("定时检查应投递超时订单: %v", e.dispatched)
	}
	// 不使用队列时直接处理
	e.Dispatch = nil
	if err := e.CheckJob().Run(ctx); err != nil {
		t.Fatal(err)
	}
	if e.order(t, no).Status != model.OrderStatusCancelled || e.balance(t, u.ID) != 600 {
		t.Error("超时订单应取消并退回余额")
	}

	// 重复支付回调、重复开通
	e.DB.Model(u).Update("balance", 10000)
	no, _ = e.Create(ctx, u.ID, CreateInput{PlanID: p.ID, Period: PeriodMonth})
	e.Paid(ctx, no, "a")
	e.Paid(ctx, no, "b")
	if o := e.order(t, no); *o.CallbackNo != "a" {
		t.Error("重复回调不应覆盖支付单号")
	}
	e.Handle(ctx, no)
	o := e.order(t, no)
	exp := *e.sub(t, *o.SubscriptionID).ExpiredAt
	e.Handle(ctx, no)
	if *e.sub(t, *o.SubscriptionID).ExpiredAt != exp {
		t.Error("重复开通不应再次延长")
	}
	if err := e.Handle(ctx, "not-exist"); err != nil {
		t.Error("订单不存在时任务应直接结束")
	}
}

func testAdminPlanCoupon(t *testing.T, e *env) {
	ctx := context.Background()
	wantErr(t, e.SavePlan(ctx, PlanInput{Name: "x"}), errAdminInvalid)
	in := PlanInput{Name: "新套餐", GroupID: ptr(int64(3)), TransferEnable: ptr(int64(10)), MonthPrice: ptr(int64(990)), SpeedLimit: ptr(int64(5))}
	if err := e.SavePlan(ctx, in); err != nil {
		t.Fatal(err)
	}
	var p model.Plan
	e.DB.Where("name = ?", "新套餐").Take(&p)
	if p.Show != 0 || p.Renew != 1 || *p.MonthPrice != 990 {
		t.Errorf("新建套餐默认值不正确: %+v", p)
	}
	u := e.user(t, "a@x.com", 0)
	sub, _ := e.Subscriptions.Create(e.DB, subscription.NewSubscription{UserID: u.ID, Plan: &p, ExpiredAt: ptr(time.Now().Unix() + 3600)})

	in.ID, in.TransferEnable, in.GroupID, in.MonthPrice = p.ID, ptr(int64(20)), ptr(int64(4)), nil
	e.SavePlan(ctx, in)
	if s := e.sub(t, sub.ID); s.TransferEnable != 10*gb {
		t.Error("未勾选强制更新时不应修改订阅")
	}
	in.ForceUpdate = true
	e.SavePlan(ctx, in)
	e.DB.Take(&p, p.ID)
	if s := e.sub(t, sub.ID); s.TransferEnable != 20*gb || *s.GroupID != 4 || *s.SpeedLimit != 5 || p.MonthPrice != nil {
		t.Errorf("强制更新后订阅或套餐不正确: %+v %+v", s, p)
	}
	list, _ := e.AdminPlans(ctx)
	if len(list) != 1 || list[0].Count != 1 {
		t.Errorf("管理端套餐列表应包含有效订阅数: %+v", list)
	}
	wantErr(t, e.DeletePlan(ctx, p.ID), errAdminPlanHasSub)
	wantErr(t, e.UpdatePlanFlags(ctx, p.ID, PlanFlags{Show: ptr(2)}), errAdminInvalid)
	e.UpdatePlanFlags(ctx, p.ID, PlanFlags{Show: ptr(1)})
	p2 := e.plan(t, "P2", nil)
	e.SortPlans(ctx, []int64{p2.ID, p.ID})
	if list, _ := e.AdminPlans(ctx); list[0].ID != p2.ID || list[1].Show != 1 {
		t.Error("排序或显示开关不正确")
	}
	wantErr(t, e.SortPlans(ctx, []int64{99999}), errAdminPlanNotExist)
	if err := e.DeletePlan(ctx, p2.ID); err != nil {
		t.Errorf("没有订单和订阅的套餐应能删除: %v", err)
	}

	// 优惠券
	now := time.Now().Unix()
	ci := CouponInput{Name: "券", Type: CouponPercent, Value: 15, StartedAt: now, EndedAt: now + 86400, LimitPlanIDs: model.IntList{p.ID}}
	if err := e.SaveCoupon(ctx, ci); err != nil {
		t.Fatal(err)
	}
	var c model.Coupon
	e.DB.Take(&c)
	if len(c.Code) != 8 || c.Show != 0 {
		t.Errorf("新建优惠券应随机生成 8 位券码且默认不启用: %+v", c)
	}
	ci.Code = c.Code
	wantErr(t, e.SaveCoupon(ctx, ci), errAdminCouponDup)
	ci.ID, ci.Code, ci.Value = c.ID, "", 30
	if err := e.SaveCoupon(ctx, ci); err != nil {
		t.Fatal(err)
	}
	e.ToggleCoupon(ctx, c.ID)
	e.DB.Take(&c, c.ID)
	if c.Value != 30 || c.Show != 1 || len(c.Code) != 8 {
		t.Errorf("修改或启用优惠券不正确: %+v", c)
	}
	csvData, err := e.GenerateCoupons(ctx, CouponInput{Name: "批量", Type: CouponAmount, Value: 1050, StartedAt: now, EndedAt: now + 60}, 3)
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimSpace(string(csvData)), "\r\n")
	if len(lines) != 4 || !strings.HasPrefix(lines[1], "批量,金额,10.5,") || !strings.Contains(lines[1], ",不限制,不限制,") {
		t.Errorf("批量生成的 CSV 不正确: %q", csvData)
	}
	_, err = e.GenerateCoupons(ctx, ci, 501)
	wantErr(t, err, errAdminInvalid)
	page, _ := e.AdminCoupons(ctx, Page{}, "value; DROP TABLE v2_coupon", "ASC")
	if page.Total != 4 || len(page.Data) != 4 || page.Data[0].ID != c.ID {
		t.Errorf("优惠券列表不正确（非法排序字段应按 id 排序）: total=%d", page.Total)
	}
	if err := e.DeleteCoupon(ctx, c.ID); err != nil {
		t.Fatal(err)
	}
	wantErr(t, e.DeleteCoupon(ctx, c.ID), errAdminCouponNone)
}

func testAdminOrder(t *testing.T, e *env) {
	ctx := context.Background()
	inviter := e.user(t, "inviter@x.com", 0)
	u := e.user(t, "Buyer@x.com", 0)
	e.DB.Model(u).Update("invite_user_id", inviter.ID)
	a := e.plan(t, "A", nil)
	b := e.plan(t, "B", nil)
	expired, _ := e.Subscriptions.Create(e.DB, subscription.NewSubscription{UserID: u.ID, Plan: a, ExpiredAt: ptr(time.Now().Unix() - 10)})
	active, _ := e.Subscriptions.Create(e.DB, subscription.NewSubscription{UserID: u.ID, Plan: a, ExpiredAt: ptr(time.Now().Unix() + 86400)})

	_, err := e.Assign(ctx, AssignInput{Email: "none@x.com", PlanID: a.ID, Period: PeriodMonth})
	wantErr(t, err, errAdminUserNone)
	_, err = e.Assign(ctx, AssignInput{Email: "buyer@x.com", PlanID: a.ID, Period: PeriodReset})
	wantErr(t, err, errAdminResetNeedSub)
	// 已过期订阅分配其他套餐：视为新购（决策 D24）
	no, err := e.Assign(ctx, AssignInput{Email: "buyer@x.com", PlanID: b.ID, Period: PeriodMonth, TotalAmount: 800, SubscriptionID: expired.ID})
	if err != nil {
		t.Fatal(err)
	}
	o := e.order(t, no)
	if o.Type != model.OrderTypeNew || o.TotalAmount != 800 || len(o.TradeNo) != 32 || *o.SubscriptionID != expired.ID ||
		*o.InviteUserID != inviter.ID || o.CommissionBalance != 80 {
		t.Errorf("分配订单不正确: %+v", o)
	}
	_, err = e.Assign(ctx, AssignInput{Email: "buyer@x.com", PlanID: b.ID, Period: PeriodMonth})
	wantErr(t, err, errAdminUserPending)
	wantErr(t, e.SetCommissionStatus(ctx, no, 2), errAdminInvalid)
	e.SetCommissionStatus(ctx, no, model.CommissionStatusInvalid)
	if err := e.AdminPaid(ctx, no); err != nil {
		t.Fatal(err)
	}
	if o := e.order(t, no); o.CommissionStatus != model.CommissionStatusInvalid || *o.CallbackNo != "manual_operation" {
		t.Errorf("佣金状态或手动支付标记不正确: %+v", o)
	}
	wantErr(t, e.AdminPaid(ctx, no), errAdminOnlyPending)
	e.Handle(ctx, no)
	if s := e.sub(t, expired.ID); *s.PlanID != b.ID || s.U != 0 {
		t.Error("分配订单开通后订阅不正确")
	}
	// 未到期订阅分配其他套餐：升级
	no, _ = e.Assign(ctx, AssignInput{Email: "buyer@x.com", PlanID: b.ID, Period: PeriodMonth, SubscriptionID: active.ID})
	if o := e.order(t, no); o.Type != model.OrderTypeUpgrade || o.SurplusAmount != nil {
		t.Errorf("分配升级订单不正确: %+v", o)
	}
	if err := e.AdminCancel(ctx, no); err != nil {
		t.Fatal(err)
	}

	// 列表筛选
	q := func(f ...OrderFilter) (*PageResult[AdminOrder], error) {
		return e.AdminOrders(ctx, OrderQuery{Filter: f})
	}
	if r, _ := q(OrderFilter{Key: "email", Condition: "模糊", Value: "BUYER"}); r.Total != 2 || r.Data[0].PlanName != "B" ||
		!strings.Contains(strings.ToLower(r.Data[0].Email), "buyer") {
		t.Errorf("按邮箱筛选不正确: %+v", r)
	}
	if r, _ := q(OrderFilter{Key: "status", Condition: "=", Value: "3"}); r.Total != 1 {
		t.Errorf("按状态筛选不正确: %d", r.Total)
	}
	if r, _ := q(OrderFilter{Key: "trade_no", Condition: "模糊", Value: no[3:10]}); r.Total != 1 {
		t.Errorf("按订单号模糊筛选不正确: %d", r.Total)
	}
	if _, err := q(OrderFilter{Key: "password", Condition: "=", Value: "x"}); !errors.Is(err, errAdminInvalid) {
		t.Error("非白名单字段应拒绝")
	}
	if _, err := q(OrderFilter{Key: "status", Condition: "or 1=1 --", Value: "x"}); !errors.Is(err, errAdminInvalid) {
		t.Error("非白名单条件应拒绝")
	}
	if _, err := q(OrderFilter{Key: "status", Condition: "=", Value: "1 or 1=1"}); !errors.Is(err, errAdminInvalid) {
		t.Error("整数字段的非整数值应拒绝")
	}
	if r, _ := e.AdminOrders(ctx, OrderQuery{IsCommission: true}); r.Total != 1 {
		t.Errorf("佣金订单筛选不正确: %d", r.Total)
	}
	d, err := e.AdminDetail(ctx, no)
	if err != nil || d.TradeNo != no || d.CommissionLog == nil {
		t.Errorf("订单详情不正确: %+v %v", d, err)
	}
	if _, err := e.AdminDetail(ctx, "x"); !errors.Is(err, errAdminOrderNone) {
		t.Error("不存在的订单应报错")
	}
}

func testHTTP(t *testing.T, e *env) {
	ctx := context.Background()
	const secure = "abcdefghijklmnop1234"
	e.set(t, map[string]any{"secure_path": secure})
	admin := e.user(t, "admin@x.com", 0)
	e.DB.Model(admin).Update("is_admin", 1)
	normal := e.user(t, "u@x.com", 0)
	adminTok, _ := e.auth.Issue(ctx, admin.ID, "", "")
	userTok, _ := e.auth.Issue(ctx, normal.ID, "", "")
	p := e.plan(t, "A", nil)

	cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
	r, err := server.NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	r.Mount(&usercenter.Module{Svc: &usercenter.Service{DB: e.DB, Settings: e.Settings, Auth: e.auth, Subscriptions: e.Subscriptions}},
		&Module{Svc: e.Service, Auth: e.auth})
	do := func(method, path, body, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		if body != "" {
			req.Header.Set("Content-Type", "application/json")
		}
		if tok != "" {
			req.Header.Set("Authorization", tok)
		}
		w := httptest.NewRecorder()
		r.Engine.ServeHTTP(w, req)
		return w
	}
	notFound := do("GET", "/api/t1/definitely-not-exist", "", "")

	for _, c := range []struct{ path, tok string }{
		{"/api/t1/wrongpath1234567890/plans", adminTok},
		{"/api/t1/" + secure + "/plans", userTok},
		{"/api/t1/" + secure + "/plans", ""},
		{"/api/t1/" + secure + "/plans", "Bearer bad"},
	} {
		w := do("GET", c.path, "", c.tok)
		if w.Code != http.StatusNotFound || w.Body.String() != notFound.Body.String() || w.Header().Get("Content-Type") != notFound.Header().Get("Content-Type") {
			t.Errorf("%s 应返回与不存在路径相同的 404，实际 %d", c.path, w.Code)
		}
	}
	if w := do("GET", "/api/t1/"+secure+"/plans", "", adminTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"count":0`) {
		t.Errorf("管理员访问套餐列表失败: %d %s", w.Code, w.Body)
	}
	// 修改安全路径后旧路径立即失效
	e.set(t, map[string]any{"secure_path": "zyxwvutsrqponmlk9876"})
	if w := do("GET", "/api/t1/"+secure+"/plans", "", adminTok); w.Code != http.StatusNotFound {
		t.Errorf("旧安全路径应失效，实际 %d", w.Code)
	}
	if w := do("POST", "/api/t1/zyxwvutsrqponmlk9876/coupons/batch",
		fmt.Sprintf(`{"name":"b","type":1,"value":100,"started_at":1,"ended_at":%d,"count":2}`, time.Now().Unix()+60), adminTok); w.Code != 200 ||
		!strings.HasPrefix(w.Body.String(), "\xEF\xBB\xBF名称,") || w.Header().Get("Content-Type") != "text/csv; charset=utf-8" {
		t.Errorf("批量生成优惠券接口不正确: %d %s", w.Code, w.Body)
	}

	if w := do("GET", "/api/t1/user/plans", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("未登录访问套餐列表应返回 401，实际 %d", w.Code)
	}
	if w := do("GET", "/api/t1/user/subscriptions", "", userTok); w.Code != 200 {
		t.Errorf("与用户中心路由共存失败: %d", w.Code)
	}
	if w := do("GET", "/api/t1/user/plans/abc", "", userTok); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("非法套餐 ID 应返回 422，实际 %d", w.Code)
	}
	w := do("POST", "/api/t1/user/orders", fmt.Sprintf(`{"plan_id":%d,"period":"month_price"}`, p.ID), userTok)
	if w.Code != 200 {
		t.Fatalf("下单接口失败: %d %s", w.Code, w.Body)
	}
	if w := do("POST", "/api/t1/user/orders", `{"plan_id":"x"}`, userTok); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("非法参数应返回 422，实际 %d", w.Code)
	}
	var o model.Order
	e.DB.Where("user_id = ?", normal.ID).Take(&o)
	if w := do("GET", "/api/t1/user/orders/"+o.TradeNo, "", userTok); w.Code != 200 || strings.Contains(w.Body.String(), `"user_id"`) {
		t.Errorf("订单详情接口不正确或泄露内部字段: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/t1/user/orders/"+o.TradeNo, "", adminTok); w.Code != http.StatusNotFound {
		t.Errorf("不能查看其他用户的订单，实际 %d", w.Code)
	}
	if w := do("POST", "/api/t1/user/orders/"+o.TradeNo+"/cancel", "", userTok); w.Code != 200 {
		t.Errorf("取消订单接口失败: %d %s", w.Code, w.Body)
	}
}
