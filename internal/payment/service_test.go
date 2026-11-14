package payment

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"testing"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/config"
	"gvboard/internal/model"
	"gvboard/internal/order"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/subscription"
	"gvboard/internal/testutil"
)

type svcEnv struct {
	*Service
	auth *auth.Service
	paid atomic.Int32
}

func newSvcEnv(t *testing.T, driver string) *svcEnv {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(context.Background(), map[string]any{"app_url": "https://site.example.com", "secure_path": "secure1234567890ab"}); err != nil {
		t.Fatal(err)
	}
	orders := &order.Service{DB: db, Settings: st,
		Subscriptions: &subscription.Service{DB: db, Cache: c, Settings: st, Linker: &subscription.Linker{Settings: st, Cache: c}}}
	e := &svcEnv{auth: auth.New(db, c, "base64:cGF5bWVudC10ZXN0LWtleS0wMTIzNDU2Nzg5YWJj")}
	e.Service = &Service{DB: db, Settings: st, Orders: orders, APIPrefix: "/api/t1",
		HTTP: &http.Client{Transport: roundTrip(func(r *http.Request) (*http.Response, error) {
			return nil, fmt.Errorf("测试中不应访问外部地址: %s", r.URL)
		})}}
	e.OnPaid = func(context.Context, *model.Order) { e.paid.Add(1) }
	orders.Payer = e.Service
	return e
}

func (e *svcEnv) user(t *testing.T, email string) *model.User {
	t.Helper()
	var u *model.User
	if err := e.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		u, _, err = account.Create(tx, account.NewUser{Email: email, Password: "password123"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return u
}

func (e *svcEnv) payment(t *testing.T, gateway string, cfg string, mut func(p *model.Payment)) *model.Payment {
	t.Helper()
	p := &model.Payment{UUID: strings.ToLower(gateway[:4]) + "uuid", Payment: gateway, Name: gateway, Config: model.JSON(cfg), Enable: 1}
	if mut != nil {
		mut(p)
	}
	if err := e.DB.Create(p).Error; err != nil {
		t.Fatal(err)
	}
	return p
}

// deposit 创建一笔待支付的充值订单。
func (e *svcEnv) deposit(t *testing.T, uid, amount int64) model.Order {
	t.Helper()
	no, err := e.Orders.Create(context.Background(), uid, order.CreateInput{Period: order.PeriodDeposit, DepositAmount: amount})
	if err != nil {
		t.Fatal(err)
	}
	var o model.Order
	e.DB.Where("trade_no = ?", no).Take(&o)
	return o
}

func (e *svcEnv) reload(t *testing.T, o *model.Order) {
	t.Helper()
	if err := e.DB.Where("trade_no = ?", o.TradeNo).Take(o).Error; err != nil {
		t.Fatal(err)
	}
}

func TestService(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			for name, fn := range map[string]func(*testing.T, *svcEnv){
				"结算与手续费":  testCheckout,
				"回调开通与幂等": testNotify,
				"回调拒绝":    testNotifyReject,
				"管理端":     testAdmin,
				"HTTP":    testPaymentHTTP,
			} {
				t.Run(name, func(t *testing.T) { fn(t, newSvcEnv(t, driver)) })
			}
		})
	}
}

const epayCfg = `{"url":"https://pay.example.com","pid":"1001","key":"k3y","type":"alipay"}`

func testCheckout(t *testing.T, e *svcEnv) {
	ctx := context.Background()
	u := e.user(t, "c@x.com")
	o := e.deposit(t, u.ID, 1000)

	off := e.payment(t, "EPay", epayCfg, func(p *model.Payment) { p.Enable = 0; p.UUID = "off00001" })
	if _, err := e.Orders.Checkout(ctx, u.ID, o.TradeNo, order.PayInput{Method: off.ID}); err != errPaymentUnavailable {
		t.Errorf("停用的支付方式应不可用: %v", err)
	}
	if _, err := e.Orders.Checkout(ctx, u.ID, o.TradeNo, order.PayInput{Method: 9999}); err != errPaymentUnavailable {
		t.Errorf("不存在的支付方式应不可用: %v", err)
	}

	// 百分比 + 固定手续费：round(1000 * 0.6% + 100) = 106
	pct, fixed := 0.6, int64(100)
	p := e.payment(t, "EPay", epayCfg, func(p *model.Payment) {
		p.HandlingFeePercent, p.HandlingFeeFixed = &pct, &fixed
		p.NotifyDomain = ptr("https://cb.example.com")
	})
	req := httptest.NewRequest("POST", "http://api.example.com/api/t1/user/orders/x/checkout", nil)
	req.Header.Set("Origin", "https://web.example.com")
	res, err := e.Orders.Checkout(ctx, u.ID, o.TradeNo, order.PayInput{Method: p.ID, Request: req})
	if err != nil || res.Type != 1 {
		t.Fatalf("结算失败: %+v %v", res, err)
	}
	q, _ := url.ParseQuery(strings.SplitN(res.Data.(string), "?", 2)[1])
	if q.Get("money") != "11.06" || q.Get("notify_url") != "https://cb.example.com/api/t1/payment/notify/EPay/"+p.UUID ||
		q.Get("return_url") != "https://web.example.com/#/order/"+o.TradeNo {
		t.Errorf("支付参数不正确: %v", q)
	}
	e.reload(t, &o)
	if o.HandlingAmount == nil || *o.HandlingAmount != 106 || o.PaymentID == nil || *o.PaymentID != p.ID || o.TotalAmount != 1000 {
		t.Errorf("订单应记录手续费与支付方式: %+v", o)
	}

	// 只设置 0% 时原项目也会写入 0 手续费；都未设置时清空
	zero := 0.0
	p2 := e.payment(t, "EPay", epayCfg, func(p *model.Payment) { p.HandlingFeePercent = &zero; p.UUID = "zero0001" })
	if _, err := e.Orders.Checkout(ctx, u.ID, o.TradeNo, order.PayInput{Method: p2.ID, Request: req}); err != nil {
		t.Fatal(err)
	}
	e.reload(t, &o)
	if o.HandlingAmount == nil || *o.HandlingAmount != 0 {
		t.Errorf("手续费百分比为 0 时应记为 0: %v", o.HandlingAmount)
	}
	p3 := e.payment(t, "EPay", epayCfg, func(p *model.Payment) { p.UUID = "none0001" })
	req2 := httptest.NewRequest("POST", "http://api.example.com/x", nil)
	req2.Header.Set("X-Forwarded-Proto", "https")
	res, _ = e.Orders.Checkout(ctx, u.ID, o.TradeNo, order.PayInput{Method: p3.ID, Request: req2})
	e.reload(t, &o)
	q, _ = url.ParseQuery(strings.SplitN(res.Data.(string), "?", 2)[1])
	if o.HandlingAmount != nil || q.Get("money") != "10" || q.Get("notify_url") != "https://api.example.com/api/t1/payment/notify/EPay/none0001" ||
		q.Get("return_url") != "https://api.example.com/#/order/"+o.TradeNo {
		t.Errorf("无手续费 / 无 Origin 时: %v %v", o.HandlingAmount, q)
	}

	// 用户端支付方式列表：只含启用的，按 sort 排序（NULL 在前）
	e.DB.Model(&model.Payment{}).Where("id = ?", p.ID).Update("sort", 2)
	e.DB.Model(&model.Payment{}).Where("id = ?", p2.ID).Update("sort", 1)
	ms, err := e.Methods(ctx)
	if err != nil || len(ms) != 3 || ms[0].ID != p3.ID || ms[1].ID != p2.ID || ms[2].ID != p.ID || ms[2].HandlingFeeFixed == nil {
		t.Errorf("支付方式列表: %+v %v", ms, err)
	}
}

func epayNotify(p *model.Payment, tradeNo, status string) *Notification {
	params := Params{"pid": "1001", "trade_no": "CB" + tradeNo, "out_trade_no": tradeNo, "money": "10", "trade_status": status}
	params["sign"] = md5hex(rawQuery(params) + "k3y")
	return &Notification{Params: params}
}

func testNotify(t *testing.T, e *svcEnv) {
	ctx := context.Background()
	u := e.user(t, "n@x.com")
	p := e.payment(t, "EPay", epayCfg, nil)
	o := e.deposit(t, u.ID, 1000)

	// 并发重复回调：都应答 success，订单只标记一次
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if reply, err := e.Notify(ctx, "EPay", p.UUID, epayNotify(p, o.TradeNo, "TRADE_SUCCESS")); err != nil || reply != "success" {
				t.Errorf("回调失败: %q %v", reply, err)
			}
		}()
	}
	wg.Wait()
	e.reload(t, &o)
	if o.Status != model.OrderStatusProcessing || o.CallbackNo == nil || *o.CallbackNo != "CB"+o.TradeNo || o.PaidAt == nil || e.paid.Load() != 1 {
		t.Errorf("订单应标记为已支付且只通知一次: status=%d paid=%d", o.Status, e.paid.Load())
	}
	// 已开通 / 已取消的订单再回调也应答成功（原项目行为）
	if reply, err := e.Notify(ctx, "EPay", p.UUID, epayNotify(p, o.TradeNo, "TRADE_SUCCESS")); err != nil || reply != "success" {
		t.Errorf("重复回调: %q %v", reply, err)
	}

	// 自定义应答与金额校验（EPaySubmitQrcode：订单金额 + 手续费）
	sp := e.payment(t, "EPaySubmitQrcode", `{"url":"https://pay.example.com","pid":"1001","key":"k3y"}`, nil)
	o2 := e.deposit(t, e.user(t, "n2@x.com").ID, 1000)
	e.DB.Model(&model.Order{}).Where("id = ?", o2.ID).Update("handling_amount", 50)
	mk := func(money string) *Notification {
		params := Params{"pid": "1001", "trade_no": "S1", "out_trade_no": o2.TradeNo, "money": money, "trade_status": "TRADE_SUCCESS"}
		params["sign"] = epaySign(params, "k3y")
		return &Notification{Params: params}
	}
	if _, err := e.Notify(ctx, "EPaySubmitQrcode", sp.UUID, mk("10.00")); err == nil {
		t.Error("金额未包含手续费时应拒绝")
	}
	if _, err := e.Notify(ctx, "EPaySubmitQrcode", sp.UUID, mk("10.50")); err != nil {
		t.Errorf("金额等于订单金额 + 手续费时应通过: %v", err)
	}
}

func testNotifyReject(t *testing.T, e *svcEnv) {
	ctx := context.Background()
	u := e.user(t, "r@x.com")
	p := e.payment(t, "EPay", epayCfg, nil)
	o := e.deposit(t, u.ID, 1000)
	cases := map[string]func() (string, error){
		"网关名不匹配": func() (string, error) {
			return e.Notify(ctx, "EPay2", p.UUID, epayNotify(p, o.TradeNo, "TRADE_SUCCESS"))
		},
		"uuid 错误": func() (string, error) {
			return e.Notify(ctx, "EPay", "nouuid00", epayNotify(p, o.TradeNo, "TRADE_SUCCESS"))
		},
		"未支付状态": func() (string, error) { return e.Notify(ctx, "EPay", p.UUID, epayNotify(p, o.TradeNo, "WAIT")) },
		"订单不存在": func() (string, error) {
			return e.Notify(ctx, "EPay", p.UUID, epayNotify(p, "nonexistent", "TRADE_SUCCESS"))
		},
	}
	for name, fn := range cases {
		if _, err := fn(); err != errNotifyRejected {
			t.Errorf("%s: 应拒绝，实际 %v", name, err)
		}
	}
	e.DB.Model(&model.Payment{}).Where("id = ?", p.ID).Update("enable", 0)
	if _, err := e.Notify(ctx, "EPay", p.UUID, epayNotify(p, o.TradeNo, "TRADE_SUCCESS")); err != errNotifyRejected {
		t.Error("停用的支付方式应拒绝回调")
	}
	// 未配置密钥时，即使签名按「空密钥」计算正确也拒绝
	np := e.payment(t, "EPay", `{"url":"https://pay.example.com","pid":"1001","key":" "}`, func(p *model.Payment) { p.UUID = "nokey001" })
	params := Params{"pid": "1001", "trade_no": "X", "out_trade_no": o.TradeNo, "trade_status": "TRADE_SUCCESS"}
	params["sign"] = md5hex(rawQuery(params) + " ")
	if _, err := e.Notify(ctx, "EPay", np.UUID, &Notification{Params: params}); err != errNotifyRejected {
		t.Error("未配置密钥时应拒绝回调")
	}
	e.reload(t, &o)
	if o.Status != model.OrderStatusPending || e.paid.Load() != 0 {
		t.Errorf("被拒绝的回调不应改变订单: %d", o.Status)
	}
}

func testAdmin(t *testing.T, e *svcEnv) {
	ctx := context.Background()
	fee := 0.05
	for in, want := range map[*SaveInput]error{
		{Payment: "EPay", Config: map[string]any{"a": 1}}:                                      errAdminName,
		{Name: "a", Config: map[string]any{"a": 1}}:                                            errAdminPayment,
		{Name: "a", Payment: "EPay", Config: map[string]any{}}:                                 errAdminConfig,
		{Name: "a", Payment: "EPay", Config: map[string]any{"a": 1}, NotifyDomain: ptr("abc")}: errAdminDomain,
		{Name: "a", Payment: "EPay", Config: map[string]any{"a": 1}, HandlingFeePercent: &fee}: errAdminFeePercent,
		{Name: "a", Payment: "Nope", Config: map[string]any{"a": 1}}:                           errAdminGateway,
	} {
		if err := e.Save(ctx, *in); err != want {
			t.Errorf("保存校验 %+v: 期望 %v，实际 %v", *in, want, err)
		}
	}

	in := SaveInput{Name: " 支付宝 ", Payment: "EPay", Icon: ptr(""), NotifyDomain: ptr(" "),
		Config: map[string]any{"url": "https://pay.example.com", "pid": "1001", "key": " k3y ", "type": ""}}
	if err := e.Save(ctx, in); err != nil {
		t.Fatal(err)
	}
	var p model.Payment
	e.DB.Take(&p)
	cfg := loadConfig(p.Config)
	if p.Name != "支付宝" || p.Icon != nil || p.NotifyDomain != nil || p.Enable != 0 || len(p.UUID) != 8 ||
		cfg["key"] != "k3y" || cfg["type"] != nil {
		t.Errorf("保存的支付方式不正确: %+v %v", p, cfg)
	}
	form, err := e.AdminForm(ctx, "EPay", p.ID)
	if err != nil || form[0].Value != "https://pay.example.com" || form[3].Value != nil {
		t.Errorf("配置表单应填入现有配置: %+v %v", form, err)
	}
	if _, err := e.AdminForm(ctx, "Nope", 0); err != errAdminGateway {
		t.Errorf("未知网关: %v", err)
	}
	if _, err := e.AdminForm(ctx, "EPay", 9999); err != errAdminNone {
		t.Errorf("支付方式不存在: %v", err)
	}

	in.ID, in.Name, in.Config = p.ID, "易支付", map[string]any{"url": "https://pay2.example.com", "pid": "1", "key": "k"}
	in.NotifyDomain, in.HandlingFeeFixed = ptr("https://cb.example.com"), ptr(int64(30))
	if err := e.Save(ctx, in); err != nil {
		t.Fatal(err)
	}
	if err := e.Toggle(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	e.DB.Take(&p, p.ID)
	if p.Name != "易支付" || p.Enable != 1 || p.HandlingFeeFixed == nil || *p.HandlingFeeFixed != 30 {
		t.Errorf("修改或启用失败: %+v", p)
	}

	e.payment(t, "Paytaro", `{"pid":"a","key":"b"}`, nil)
	list, err := e.AdminList(ctx, "https://api.example.com")
	if err != nil || len(list) != 2 || list[0].NotifyURL != "https://cb.example.com/api/t1/payment/notify/EPay/"+p.UUID ||
		list[1].NotifyURL != "https://api.example.com/api/t1/payment/notify/Paytaro/paytuuid" {
		t.Errorf("管理端列表: %+v %v", list, err)
	}
	if err := e.Sort(ctx, []int64{list[1].ID, list[0].ID}); err != nil {
		t.Fatal(err)
	}
	if list, _ = e.AdminList(ctx, ""); list[0].Payment.Payment != "Paytaro" {
		t.Errorf("排序后顺序不正确: %+v", list)
	}
	if err := e.Sort(ctx, []int64{list[0].ID, 9999}); err != errAdminNone {
		t.Errorf("排序含不存在的 ID: %v", err)
	}
	if err := e.Delete(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.Delete(ctx, p.ID); err != errAdminNone {
		t.Errorf("重复删除: %v", err)
	}

	e.Settings.Set(ctx, map[string]any{"app_url": ""})
	if err := e.Save(ctx, SaveInput{Name: "a", Payment: "EPay", Config: map[string]any{"a": 1}}); err != errAdminAppURL {
		t.Errorf("未设置站点地址: %v", err)
	}
}

func testPaymentHTTP(t *testing.T, e *svcEnv) {
	ctx := context.Background()
	u := e.user(t, "h@x.com")
	admin := e.user(t, "admin@x.com")
	e.DB.Model(admin).Update("is_admin", 1)
	userTok, _ := e.auth.Issue(ctx, u.ID, "", "")
	adminTok, _ := e.auth.Issue(ctx, admin.ID, "", "")
	p := e.payment(t, "EPay", epayCfg, nil)
	sc := e.payment(t, "StripeCredit", `{"stripe_pk_live":"pk_test_1","stripe_webhook_key":"w"}`, nil)
	o := e.deposit(t, u.ID, 1000)

	mount := func(legacy bool) *server.Router {
		cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
		cfg.Compat.LegacyCallback = legacy
		r, err := server.NewRouter(cfg, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		r.Mount(&order.Module{Svc: e.Orders, Auth: e.auth}, &Module{Svc: e.Service, Auth: e.auth})
		return r
	}
	r := mount(false)
	do := func(r *server.Router, method, path, body, tok string) *httptest.ResponseRecorder {
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
	notFound := do(r, "GET", "/api/t1/definitely-not-exist", "", "")
	same404 := func(name string, w *httptest.ResponseRecorder) {
		t.Helper()
		if w.Code != http.StatusNotFound || w.Body.String() != notFound.Body.String() {
			t.Errorf("%s 应返回统一 404，实际 %d %s", name, w.Code, w.Body)
		}
	}

	if w := do(r, "GET", "/api/t1/user/payment-methods", "", userTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"payment":"EPay"`) ||
		strings.Contains(w.Body.String(), "k3y") {
		t.Errorf("用户端支付方式列表不正确或泄露配置: %d %s", w.Code, w.Body)
	}
	if w := do(r, "GET", fmt.Sprintf("/api/t1/user/payment-methods/%d/stripe-key", sc.ID), "", userTok); !strings.Contains(w.Body.String(), `"data":"pk_test_1"`) {
		t.Errorf("Stripe 公钥: %s", w.Body)
	}
	if w := do(r, "GET", fmt.Sprintf("/api/t1/user/payment-methods/%d/stripe-key", p.ID), "", userTok); w.Code != http.StatusNotFound {
		t.Errorf("非 StripeCredit 不应返回公钥: %d", w.Code)
	}
	w := do(r, "POST", "/api/t1/user/orders/"+o.TradeNo+"/checkout", fmt.Sprintf(`{"method":%d}`, p.ID), userTok)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `"type":1`) || !strings.Contains(w.Body.String(), "submit.php?money=10") {
		t.Errorf("结算接口: %d %s", w.Code, w.Body)
	}

	// 回调：失败统一 404，成功返回 success
	n := epayNotify(p, o.TradeNo, "TRADE_SUCCESS")
	form := url.Values{}
	for k, v := range n.Params {
		form.Set(k, phpStr(v))
	}
	native := "/api/t1/payment/notify/EPay/" + p.UUID
	legacy := "/api/v1/guest/payment/notify/EPay/" + p.UUID
	same404("错误签名", do(r, "GET", native+"?"+strings.Replace(form.Encode(), "TRADE_SUCCESS", "TRADE_FAIL", 1), "", ""))
	same404("错误 uuid", do(r, "GET", "/api/t1/payment/notify/EPay/zzzzzzzz?"+form.Encode(), "", ""))
	same404("旧回调路径默认关闭", do(r, "GET", legacy+"?"+form.Encode(), "", ""))
	req := httptest.NewRequest("POST", native, strings.NewReader(form.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	rec := httptest.NewRecorder()
	r.Engine.ServeHTTP(rec, req)
	if rec.Code != 200 || rec.Body.String() != "success" {
		t.Errorf("表单回调应答 success: %d %s", rec.Code, rec.Body)
	}
	if w := do(mount(true), "GET", legacy+"?"+form.Encode(), "", ""); w.Code != 200 || w.Body.String() != "success" {
		t.Errorf("开启 compat.legacy_callback 后旧路径可用: %d %s", w.Code, w.Body)
	}
	e.reload(t, &o)
	if o.Status != model.OrderStatusProcessing {
		t.Errorf("回调后订单应为已支付: %d", o.Status)
	}

	// 管理端
	base := "/api/t1/secure1234567890ab/payments"
	same404("普通用户访问管理端", do(r, "GET", base, "", userTok))
	if w := do(r, "GET", base+"/gateways", "", adminTok); !strings.Contains(w.Body.String(), `["PaytaroQR","Paytaro","AlipayF2F"`) {
		t.Errorf("网关列表: %s", w.Body)
	}
	if w := do(r, "GET", base, "", adminTok); !strings.Contains(w.Body.String(), `"notify_url":"http://example.com/api/t1/payment/notify/EPay/`) ||
		!strings.Contains(w.Body.String(), `"key":"k3y"`) {
		t.Errorf("管理端列表应含回调地址与完整配置: %s", w.Body)
	}
	if w := do(r, "GET", base+"/form?payment=EPay&id="+fmt.Sprint(p.ID), "", adminTok); !strings.Contains(w.Body.String(), `"url":{"description":"","label":"URL","type":"input","value":"https://pay.example.com"}`) {
		t.Errorf("配置表单: %s", w.Body)
	}
	if w := do(r, "POST", base, `{"name":"x","payment":"Coinbase","config":{"coinbase_url":"https://c"},"handling_fee_percent":1.5}`, adminTok); w.Code != 200 {
		t.Errorf("新增支付方式: %d %s", w.Code, w.Body)
	}
	if w := do(r, "PUT", base+"/"+fmt.Sprint(p.ID), `{"name":"y","payment":"EPay","config":{"key":"k"},"handling_fee_fixed":"x"}`, adminTok); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("非法参数应返回 422: %d", w.Code)
	}
	if w := do(r, "POST", base+"/"+fmt.Sprint(p.ID)+"/toggle", "", adminTok); w.Code != 200 {
		t.Errorf("切换启用: %d", w.Code)
	}
	if w := do(r, "POST", base+"/sort", fmt.Sprintf(`{"ids":[%d,%d]}`, sc.ID, p.ID), adminTok); w.Code != 200 {
		t.Errorf("排序: %d %s", w.Code, w.Body)
	}
	if w := do(r, "DELETE", base+"/"+fmt.Sprint(sc.ID), "", adminTok); w.Code != 200 {
		t.Errorf("删除: %d", w.Code)
	}
}

func ptr[T any](v T) *T { return &v }
