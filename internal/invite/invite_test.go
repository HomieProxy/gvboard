package invite

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"regexp"
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
	"gvboard/internal/testutil"
)

type env struct {
	*Service
	auth *auth.Service
	seq  int
}

func newEnv(t *testing.T, driver string) *env {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	now := time.Unix(1_800_000_000, 0)
	return &env{
		Service: &Service{DB: db, Settings: st, Now: func() time.Time { return now }},
		auth:    auth.New(db, c, "base64:aW52aXRlLXRlc3Qta2V5LTAxMjM0NTY3ODlhYmNk"),
	}
}

func (e *env) set(t *testing.T, kv map[string]any) {
	t.Helper()
	if err := e.Settings.Set(context.Background(), kv); err != nil {
		t.Fatal(err)
	}
}

// user 创建用户，inviter 不为 0 时设置邀请人。
func (e *env) user(t *testing.T, inviter int64) *model.User {
	t.Helper()
	e.seq++
	var u *model.User
	if err := e.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		u, _, err = account.Create(tx, account.NewUser{Email: fmt.Sprintf("u%d@x.com", e.seq), Password: "password123"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if inviter != 0 {
		e.DB.Model(u).Update("invite_user_id", inviter)
		u.InviteUserID = &inviter
	}
	return u
}

type orderOpt struct {
	userID, inviter    int64
	status, commission int
	balance            int64
	updatedAt          int64
}

func (e *env) order(t *testing.T, o orderOpt) *model.Order {
	t.Helper()
	e.seq++
	m := &model.Order{
		UserID: o.userID, PlanID: 1, Type: model.OrderTypeNew, Period: "month_price",
		TradeNo: fmt.Sprintf("T%020d", e.seq), TotalAmount: 10000,
		Status: o.status, CommissionStatus: o.commission, CommissionBalance: o.balance,
		UpdatedAt: o.updatedAt,
	}
	if o.inviter != 0 {
		m.InviteUserID = &o.inviter
	}
	if err := e.DB.Create(m).Error; err != nil {
		t.Fatal(err)
	}
	if o.updatedAt != 0 {
		e.DB.Model(m).UpdateColumn("updated_at", o.updatedAt)
	}
	return m
}

// reload 重新读取记录。先清零，否则 GORM 会把结构体中已有的主键一并作为查询条件。
func (e *env) reload(t *testing.T, v any, id int64) {
	t.Helper()
	reflect.ValueOf(v).Elem().SetZero()
	if err := e.DB.Take(v, id).Error; err != nil {
		t.Fatal(err)
	}
}

func wantErr(t *testing.T, err error, want *httpx.Error) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Msg != want.Msg {
		t.Fatalf("期望错误 %q，实际 %v", want.Msg, err)
	}
}

func TestInvite(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			t.Run("邀请码", func(t *testing.T) { testCodes(t, newEnv(t, driver)) })
			t.Run("邀请码并发上限", func(t *testing.T) { testCodesConcurrent(t, newEnv(t, driver)) })
			t.Run("邀请统计", func(t *testing.T) { testStat(t, newEnv(t, driver)) })
			t.Run("佣金明细", func(t *testing.T) { testDetails(t, newEnv(t, driver)) })
			t.Run("自动确认", func(t *testing.T) { testAutoCheck(t, newEnv(t, driver)) })
			t.Run("佣金发放", func(t *testing.T) { testPay(t, newEnv(t, driver)) })
			t.Run("三级分销", func(t *testing.T) { testDistribution(t, newEnv(t, driver)) })
			t.Run("并发发放", func(t *testing.T) { testPayConcurrent(t, newEnv(t, driver)) })
			t.Run("HTTP", func(t *testing.T) { testHTTP(t, newEnv(t, driver)) })
		})
	}
}

var codeRe = regexp.MustCompile(`^[A-Za-z0-9]{8}$`)

func testCodes(t *testing.T, e *env) {
	ctx := context.Background()
	e.set(t, map[string]any{"invite_gen_limit": 2})
	u := e.user(t, 0)
	other := e.user(t, 0)
	c1, err := e.Generate(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	c2, err := e.Generate(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if !codeRe.MatchString(c1.Code) || c1.Code == c2.Code {
		t.Fatalf("邀请码格式错误或重复: %q %q", c1.Code, c2.Code)
	}
	_, err = e.Generate(ctx, u.ID)
	wantErr(t, err, errGenLimit)
	// 其他用户不受影响
	if _, err := e.Generate(ctx, other.ID); err != nil {
		t.Fatal(err)
	}
	// 已使用的邀请码不计入上限，也不出现在列表中
	e.DB.Model(&model.InviteCode{}).Where("id = ?", c1.ID).Update("status", 1)
	c3, err := e.Generate(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	ov, err := e.Fetch(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	if len(ov.Codes) != 2 || ov.Codes[0].Code != c2.Code || ov.Codes[1].Code != c3.Code {
		t.Fatalf("邀请码列表错误: %+v", ov.Codes)
	}
	_, err = e.Generate(ctx, 999999)
	wantErr(t, err, errUserNotExist)
	_, err = e.Fetch(ctx, 999999)
	wantErr(t, err, errUserNotExist)
}

func testCodesConcurrent(t *testing.T, e *env) {
	e.set(t, map[string]any{"invite_gen_limit": 3})
	u := e.user(t, 0)
	var wg sync.WaitGroup
	for range 10 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, _ = e.Generate(context.Background(), u.ID)
		}()
	}
	wg.Wait()
	var n int64
	e.DB.Model(&model.InviteCode{}).Where("user_id = ?", u.ID).Count(&n)
	if n != 3 {
		t.Fatalf("并发生成后邀请码数量应为 3，实际 %d", n)
	}
}

func testStat(t *testing.T, e *env) {
	ctx := context.Background()
	a := e.user(t, 0)
	b := e.user(t, a.ID)
	c := e.user(t, a.ID)
	e.user(t, b.ID)
	e.DB.Model(a).Update("commission_balance", 777)
	e.order(t, orderOpt{userID: b.ID, inviter: a.ID, status: model.OrderStatusCompleted, balance: 1000})
	e.order(t, orderOpt{userID: c.ID, inviter: a.ID, status: model.OrderStatusCompleted, balance: 300})
	// 以下不计入确认中：已折抵、已确认、其他邀请人
	e.order(t, orderOpt{userID: c.ID, inviter: a.ID, status: model.OrderStatusDiscounted, balance: 500})
	e.order(t, orderOpt{userID: c.ID, inviter: a.ID, status: model.OrderStatusCompleted, commission: model.CommissionStatusIssuing, balance: 500})
	e.order(t, orderOpt{userID: c.ID, inviter: b.ID, status: model.OrderStatusCompleted, balance: 500})
	e.DB.Create(&model.CommissionLog{InviteUserID: a.ID, UserID: b.ID, TradeNo: "x1", OrderAmount: 100, GetAmount: 30})
	e.DB.Create(&model.CommissionLog{InviteUserID: a.ID, UserID: b.ID, TradeNo: "x2", OrderAmount: 100, GetAmount: 12})
	e.DB.Create(&model.CommissionLog{InviteUserID: b.ID, UserID: c.ID, TradeNo: "x3", OrderAmount: 100, GetAmount: 99})

	ov, err := e.Fetch(ctx, a.ID)
	if err != nil {
		t.Fatal(err)
	}
	want := Stat{Invitees: 2, TotalCommission: 42, PendingCommission: 1300, CommissionRate: 10, CommissionBalance: 777}
	if ov.Stat != want || ov.Codes == nil {
		t.Fatalf("统计错误: %+v", ov.Stat)
	}
	// 用户单独设置的比例优先，0 视为未设置
	e.DB.Model(a).Update("commission_rate", 0)
	if ov, _ = e.Fetch(ctx, a.ID); ov.Stat.CommissionRate != 10 {
		t.Fatalf("比例为 0 时应使用全局比例，实际 %d", ov.Stat.CommissionRate)
	}
	e.DB.Model(a).Update("commission_rate", 25)
	if ov, _ = e.Fetch(ctx, a.ID); ov.Stat.CommissionRate != 25 {
		t.Fatalf("应使用用户单独比例，实际 %d", ov.Stat.CommissionRate)
	}
	// 开启分销时确认中的佣金按一级比例折算，可以是小数
	e.set(t, map[string]any{"commission_distribution_enable": 1, "commission_distribution_l1": 33.5})
	if ov, _ = e.Fetch(ctx, a.ID); ov.Stat.PendingCommission != 1300*(33.5/100) {
		t.Fatalf("分销折算错误: %v", ov.Stat.PendingCommission)
	}
	e.set(t, map[string]any{"commission_distribution_l1": nil})
	if ov, _ = e.Fetch(ctx, a.ID); ov.Stat.PendingCommission != 0 {
		t.Fatalf("一级比例未填写时应为 0: %v", ov.Stat.PendingCommission)
	}
}

func testDetails(t *testing.T, e *env) {
	ctx := context.Background()
	a := e.user(t, 0)
	b := e.user(t, a.ID)
	for i := range 25 {
		e.DB.Create(&model.CommissionLog{InviteUserID: a.ID, UserID: b.ID, TradeNo: fmt.Sprintf("N%02d", i),
			OrderAmount: 1000, GetAmount: int64(i), CreatedAt: int64(1000 + i)})
	}
	e.DB.Create(&model.CommissionLog{InviteUserID: b.ID, UserID: a.ID, TradeNo: "other", OrderAmount: 1, GetAmount: 1})

	d, err := e.Details(ctx, a.ID, Page{})
	if err != nil {
		t.Fatal(err)
	}
	// 金额为 0 的记录不显示；默认每页 10 条，按时间倒序
	if d.Total != 24 || len(d.Data) != 10 || d.Data[0].TradeNo != "N24" || d.Data[9].TradeNo != "N15" {
		t.Fatalf("第 1 页错误: total=%d len=%d", d.Total, len(d.Data))
	}
	if d, _ = e.Details(ctx, a.ID, Page{Current: 3, PageSize: 5}); len(d.Data) != 4 || d.Data[3].TradeNo != "N01" {
		t.Fatalf("pageSize 小于 10 应按 10 处理: len=%d", len(d.Data))
	}
	if d, _ = e.Details(ctx, a.ID, Page{PageSize: 100000}); len(d.Data) != 24 {
		t.Fatalf("大分页错误: len=%d", len(d.Data))
	}
	if d, _ = e.Details(ctx, e.user(t, 0).ID, Page{}); d.Total != 0 || d.Data == nil {
		t.Fatalf("无记录时应返回空数组: %+v", d)
	}
}

func testAutoCheck(t *testing.T, e *env) {
	ctx := context.Background()
	a := e.user(t, 0)
	b := e.user(t, a.ID)
	old := e.now().Unix() - autoCheckDelay
	recent := old + 1
	o := func(status, commission int, inviter, updated int64) *model.Order {
		return e.order(t, orderOpt{userID: b.ID, inviter: inviter, status: status, commission: commission, balance: 100, updatedAt: updated})
	}
	completed := o(model.OrderStatusCompleted, 0, a.ID, old)
	discounted := o(model.OrderStatusDiscounted, 0, a.ID, old)
	tooNew := o(model.OrderStatusCompleted, 0, a.ID, recent)
	cancelled := o(model.OrderStatusCancelled, 0, a.ID, old)
	pending := o(model.OrderStatusPending, 0, a.ID, old)
	noInviter := o(model.OrderStatusCompleted, 0, 0, old)
	invalid := o(model.OrderStatusCompleted, model.CommissionStatusInvalid, a.ID, old)

	e.set(t, map[string]any{"commission_auto_check_enable": 0})
	if err := e.AutoCheck(ctx); err != nil {
		t.Fatal(err)
	}
	var got model.Order
	e.reload(t, &got, completed.ID)
	if got.CommissionStatus != 0 {
		t.Fatal("关闭自动确认时不应修改")
	}
	e.set(t, map[string]any{"commission_auto_check_enable": 1})
	if err := e.AutoCheck(ctx); err != nil {
		t.Fatal(err)
	}
	for _, c := range []struct {
		o    *model.Order
		want int
	}{
		{completed, 1}, {discounted, 1}, {tooNew, 0}, {cancelled, 0}, {pending, 0}, {noInviter, 0}, {invalid, 3},
	} {
		e.reload(t, &got, c.o.ID)
		if got.CommissionStatus != c.want {
			t.Errorf("订单 %d 状态 %d 佣金状态应为 %d，实际 %d", c.o.ID, c.o.Status, c.want, got.CommissionStatus)
		}
	}
}

func testPay(t *testing.T, e *env) {
	ctx := context.Background()
	a := e.user(t, 0)
	b := e.user(t, a.ID)
	o1 := e.order(t, orderOpt{userID: b.ID, inviter: a.ID, status: model.OrderStatusCompleted, commission: 1, balance: 1000})
	// 管理员标记为无效或仍待确认的不发放
	o2 := e.order(t, orderOpt{userID: b.ID, inviter: a.ID, status: model.OrderStatusCompleted, commission: 3, balance: 1000})
	o3 := e.order(t, orderOpt{userID: b.ID, inviter: a.ID, status: model.OrderStatusCompleted, commission: 0, balance: 1000, updatedAt: e.now().Unix()})
	// 邀请人已删除：订单标记为有效但不发放
	o4 := e.order(t, orderOpt{userID: b.ID, inviter: 999999, status: model.OrderStatusCompleted, commission: 1, balance: 1000})
	// 佣金为 0：不产生记录
	o5 := e.order(t, orderOpt{userID: b.ID, inviter: a.ID, status: model.OrderStatusCompleted, commission: 1, balance: 0})

	if err := e.CheckCommission(ctx); err != nil {
		t.Fatal(err)
	}
	if err := e.CheckCommission(ctx); err != nil {
		t.Fatal(err)
	}
	var ua model.User
	e.reload(t, &ua, a.ID)
	if ua.CommissionBalance != 1000 || ua.Balance != 0 {
		t.Fatalf("邀请人佣金余额应为 1000，实际 %d / 余额 %d", ua.CommissionBalance, ua.Balance)
	}
	var logs []model.CommissionLog
	e.DB.Where("invite_user_id = ?", a.ID).Find(&logs)
	if len(logs) != 1 || logs[0].TradeNo != o1.TradeNo || logs[0].GetAmount != 1000 || logs[0].OrderAmount != 10000 || logs[0].UserID != b.ID {
		t.Fatalf("佣金记录错误: %+v", logs)
	}
	for _, c := range []struct {
		o      *model.Order
		status int
		actual int64
	}{{o1, 2, 1000}, {o2, 3, -1}, {o3, 0, -1}, {o4, 2, 0}, {o5, 2, 0}} {
		var got model.Order
		e.reload(t, &got, c.o.ID)
		if got.CommissionStatus != c.status {
			t.Errorf("订单 %s 佣金状态应为 %d，实际 %d", c.o.TradeNo, c.status, got.CommissionStatus)
		}
		if c.actual >= 0 && (got.ActualCommissionBalance == nil || *got.ActualCommissionBalance != c.actual) {
			t.Errorf("订单 %s 实际佣金应为 %d，实际 %v", c.o.TradeNo, c.actual, got.ActualCommissionBalance)
		}
	}

	// 关闭提现时佣金直接进入余额
	e.set(t, map[string]any{"withdraw_close_enable": 1})
	e.order(t, orderOpt{userID: b.ID, inviter: a.ID, status: model.OrderStatusCompleted, commission: 1, balance: 250})
	if err := e.CheckCommission(ctx); err != nil {
		t.Fatal(err)
	}
	e.reload(t, &ua, a.ID)
	if ua.CommissionBalance != 1000 || ua.Balance != 250 {
		t.Fatalf("关闭提现后应进入余额: 佣金 %d 余额 %d", ua.CommissionBalance, ua.Balance)
	}
}

func testDistribution(t *testing.T, e *env) {
	ctx := context.Background()
	// 邀请链：a3 → a2 → a1 → buyer
	a3 := e.user(t, 0)
	a2 := e.user(t, a3.ID)
	a1 := e.user(t, a2.ID)
	buyer := e.user(t, a1.ID)
	balances := func() [3]int64 {
		var out [3]int64
		for i, u := range []*model.User{a1, a2, a3} {
			var got model.User
			e.reload(t, &got, u.ID)
			out[i] = got.CommissionBalance
		}
		return out
	}

	// 二级未填写：二级不发放，三级仍发给 a3（D31，原项目会把三级分成发给 a2）
	e.set(t, map[string]any{"commission_distribution_enable": 1, "commission_distribution_l1": 50, "commission_distribution_l2": nil, "commission_distribution_l3": 10.9})
	o := e.order(t, orderOpt{userID: buyer.ID, inviter: a1.ID, status: model.OrderStatusCompleted, commission: 1, balance: 1000})
	if err := e.CheckCommission(ctx); err != nil {
		t.Fatal(err)
	}
	if got := balances(); got != [3]int64{500, 0, 100} {
		t.Fatalf("分销金额错误: %v", got)
	}
	var got model.Order
	e.reload(t, &got, o.ID)
	if got.CommissionStatus != 2 || *got.ActualCommissionBalance != 600 {
		t.Fatalf("订单实际佣金错误: %v", *got.ActualCommissionBalance)
	}
	var n int64
	e.DB.Model(&model.CommissionLog{}).Where("trade_no = ?", o.TradeNo).Count(&n)
	if n != 2 {
		t.Fatalf("佣金记录数应为 2，实际 %d", n)
	}

	// 一级为 0：二、三级照常发给对应层级
	e.set(t, map[string]any{"commission_distribution_l1": 0, "commission_distribution_l2": 20, "commission_distribution_l3": 10})
	e.order(t, orderOpt{userID: buyer.ID, inviter: a1.ID, status: model.OrderStatusCompleted, commission: 1, balance: 1000})
	if err := e.CheckCommission(ctx); err != nil {
		t.Fatal(err)
	}
	if got := balances(); got != [3]int64{500, 200, 200} {
		t.Fatalf("一级为 0 时分销错误: %v", got)
	}

	// 四舍五入到分：3 × 50% = 1.5 → 2；3 × 10% = 0.3 → 0（与原项目一致仍记录一条 0 元记录）；订单实际佣金 1.8 → 2
	e.set(t, map[string]any{"commission_distribution_l1": 50, "commission_distribution_l2": 0, "commission_distribution_l3": 10})
	o = e.order(t, orderOpt{userID: buyer.ID, inviter: a1.ID, status: model.OrderStatusCompleted, commission: 1, balance: 3})
	if err := e.CheckCommission(ctx); err != nil {
		t.Fatal(err)
	}
	if got := balances(); got != [3]int64{502, 200, 200} {
		t.Fatalf("取整错误: %v", got)
	}
	e.reload(t, &got, o.ID)
	if *got.ActualCommissionBalance != 2 {
		t.Fatalf("订单实际佣金取整错误: %d", *got.ActualCommissionBalance)
	}

	// 中间层级用户被删除：停止向上
	e.DB.Delete(&model.User{}, a2.ID)
	e.set(t, map[string]any{"commission_distribution_l1": 10, "commission_distribution_l2": 10, "commission_distribution_l3": 10})
	e.order(t, orderOpt{userID: buyer.ID, inviter: a1.ID, status: model.OrderStatusCompleted, commission: 1, balance: 1000})
	if err := e.CheckCommission(ctx); err != nil {
		t.Fatal(err)
	}
	var top model.User
	e.reload(t, &top, a3.ID)
	var mid model.User
	e.reload(t, &mid, a1.ID)
	if mid.CommissionBalance != 602 || top.CommissionBalance != 200 {
		t.Fatalf("上级被删除后应停止向上: a1=%d a3=%d", mid.CommissionBalance, top.CommissionBalance)
	}
}

func testPayConcurrent(t *testing.T, e *env) {
	a := e.user(t, 0)
	b := e.user(t, a.ID)
	for range 20 {
		e.order(t, orderOpt{userID: b.ID, inviter: a.ID, status: model.OrderStatusCompleted, commission: 1, balance: 100})
	}
	var wg sync.WaitGroup
	for range 4 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_ = e.AutoPay(context.Background())
		}()
	}
	wg.Wait()
	var ua model.User
	e.reload(t, &ua, a.ID)
	var n int64
	e.DB.Model(&model.CommissionLog{}).Where("invite_user_id = ?", a.ID).Count(&n)
	if ua.CommissionBalance != 2000 || n != 20 {
		t.Fatalf("并发发放后佣金应为 2000 / 20 条，实际 %d / %d", ua.CommissionBalance, n)
	}
}

func testHTTP(t *testing.T, e *env) {
	ctx := context.Background()
	u := e.user(t, 0)
	tok, _ := e.auth.Issue(ctx, u.ID, "", "")
	cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
	r, err := server.NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	r.Mount(&Module{Svc: e.Service, Auth: e.auth})
	do := func(method, path, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, nil)
		if tok != "" {
			req.Header.Set("Authorization", tok)
		}
		w := httptest.NewRecorder()
		r.Engine.ServeHTTP(w, req)
		return w
	}
	for _, p := range [][2]string{{"GET", "/api/t1/user/invite"}, {"POST", "/api/t1/user/invite/codes"}, {"GET", "/api/t1/user/invite/commissions"}} {
		if w := do(p[0], p[1], ""); w.Code != http.StatusUnauthorized {
			t.Errorf("%s 未登录应返回 401，实际 %d", p[1], w.Code)
		}
	}
	if w := do("POST", "/api/t1/user/invite/codes", tok); w.Code != 200 || !strings.Contains(w.Body.String(), `"code":"`) {
		t.Fatalf("生成邀请码失败: %d %s", w.Code, w.Body)
	}
	w := do("GET", "/api/t1/user/invite", tok)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"invitees":0`) || strings.Contains(w.Body.String(), "user_id") {
		t.Fatalf("邀请信息错误: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/t1/user/invite/commissions?current=1&page_size=20", tok); w.Code != 200 || !strings.Contains(w.Body.String(), `"data":[]`) {
		t.Fatalf("佣金明细错误: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/t1/user/invite/commissions?page_size=abc", tok); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("非法分页参数应返回 422，实际 %d", w.Code)
	}
}
