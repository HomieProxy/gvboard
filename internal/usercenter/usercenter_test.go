package usercenter

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"encoding/json"
	"errors"
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
	"gvboard/internal/util"
)

func newSvc(t *testing.T, driver string) *Service {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &Service{
		DB: db, Settings: st,
		Auth:          auth.New(db, c, "base64:dXNlcmNlbnRlci10ZXN0LWtleS0wMTIzNDU="),
		Subscriptions: &subscription.Service{DB: db, Cache: c, Settings: st, Linker: &subscription.Linker{Settings: st, Cache: c}},
	}
}

func createUser(t *testing.T, db *gorm.DB, email string) (*model.User, *model.Subscription) {
	t.Helper()
	var u *model.User
	var sub *model.Subscription
	if err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		u, sub, err = account.Create(tx, account.NewUser{Email: email, Password: "password123"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return u, sub
}

func wantErr(t *testing.T, err error, want *httpx.Error) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Msg != want.Msg {
		t.Fatalf("期望错误 %q，实际 %v", want.Msg, err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestUserCenter(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			t.Run("信息与统计", func(t *testing.T) { testInfoStat(t, newSvc(t, driver)) })
			t.Run("密码与会话", func(t *testing.T) { testPasswordSessions(t, newSvc(t, driver)) })
			t.Run("佣金划转", func(t *testing.T) { testTransfer(t, newSvc(t, driver)) })
			t.Run("礼品卡", func(t *testing.T) { testGiftcard(t, newSvc(t, driver)) })
			t.Run("礼品卡并发", func(t *testing.T) { testGiftcardConcurrent(t, newSvc(t, driver)) })
			t.Run("HTTP", func(t *testing.T) { testHTTP(t, newSvc(t, driver)) })
		})
	}
}

func testInfoStat(t *testing.T, s *Service) {
	ctx := context.Background()
	u, _ := createUser(t, s.DB, "Info@x.com")
	s.DB.Model(u).Updates(map[string]any{"balance": 500, "commission_balance": 30, "telegram_id": 123})

	info, err := s.Info(ctx, u.ID)
	if err != nil {
		t.Fatal(err)
	}
	sum := md5.Sum([]byte("Info@x.com"))
	if info.Email != "Info@x.com" || info.Balance != 500 || info.CommissionBalance != 30 || !info.TelegramBound ||
		info.SubscriptionLimit != 5 || info.AvatarURL != "https://cravatar.cn/avatar/"+hex.EncodeToString(sum[:])+"?s=64&d=identicon" {
		t.Errorf("用户信息不正确: %+v", info)
	}

	for _, st := range []int{0, 0, 1, 3} {
		s.DB.Create(&model.Order{UserID: u.ID, Period: "month_price", TradeNo: util.GenerateOrderNo(), Status: st})
	}
	s.DB.Create(&model.Ticket{UserID: u.ID, Subject: "a", Status: 0})
	s.DB.Create(&model.Ticket{UserID: u.ID, Subject: "b", Status: 1})
	for _, e := range []string{"i1@x.com", "i2@x.com"} {
		iu, _ := createUser(t, s.DB, e)
		s.DB.Model(iu).Update("invite_user_id", u.ID)
	}
	st, err := s.Stat(ctx, u.ID)
	if err != nil || st.PendingOrders != 2 || st.OpenTickets != 1 || st.Invitees != 2 {
		t.Errorf("统计不正确: %+v %v", st, err)
	}

	if err := s.UnbindTelegram(ctx, u.ID); err != nil {
		t.Fatal(err)
	}
	if info, _ := s.Info(ctx, u.ID); info.TelegramBound {
		t.Error("解绑后不应再显示已绑定")
	}
}

func testPasswordSessions(t *testing.T, s *Service) {
	ctx := context.Background()
	u, _ := createUser(t, s.DB, "p@x.com")
	t1, _ := s.Auth.Issue(ctx, u.ID, "1.1.1.1", "A")
	time.Sleep(1100 * time.Millisecond)
	t2, _ := s.Auth.Issue(ctx, u.ID, "2.2.2.2", "B")
	id2, err := s.Auth.Parse(ctx, t2)
	if err != nil {
		t.Fatal(err)
	}
	list, err := s.Sessions(ctx, id2)
	if err != nil || len(list) != 2 || list[0].IP != "2.2.2.2" || !list[0].Current || list[1].Current {
		t.Fatalf("会话列表不正确: %+v %v", list, err)
	}
	if err := s.Auth.RemoveSession(ctx, u.ID, list[1].ID); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Auth.Parse(ctx, t1); err == nil {
		t.Error("删除的会话应失效")
	}

	wantErr(t, s.ChangePassword(ctx, u.ID, "", "newpassword1"), errOldPwdRequired)
	wantErr(t, s.ChangePassword(ctx, u.ID, "password123", "short"), errPasswordMin)
	wantErr(t, s.ChangePassword(ctx, u.ID, "wrong-password", "newpassword1"), errOldPwdWrong)
	s.DB.Model(u).Updates(map[string]any{"password": "482c811da5d5b4bc6d497ffa98491e38", "password_algo": "md5"})
	if err := s.ChangePassword(ctx, u.ID, "password123", "newpassword1"); err != nil {
		t.Fatal(err)
	}
	var got model.User
	s.DB.Take(&got, u.ID)
	if got.PasswordAlgo != nil || !util.VerifyPassword("", "", "newpassword1", got.Password) {
		t.Error("修改后应为 bcrypt 并清除旧算法")
	}
	if _, err := s.Auth.Parse(ctx, t2); err == nil {
		t.Error("修改密码后全部会话应失效")
	}
}

func testTransfer(t *testing.T, s *Service) {
	ctx := context.Background()
	inviter, _ := createUser(t, s.DB, "inviter@x.com")
	u, _ := createUser(t, s.DB, "t@x.com")
	s.DB.Model(inviter).Update("commission_type", 1)
	s.DB.Model(u).Updates(map[string]any{"commission_balance": 1000, "balance": 10, "invite_user_id": inviter.ID})

	wantErr(t, s.TransferCommission(ctx, u.ID, 0), errTransferAmount)
	wantErr(t, s.TransferCommission(ctx, u.ID, 1001), errInsufficient)
	if err := s.TransferCommission(ctx, u.ID, 300); err != nil {
		t.Fatal(err)
	}
	var got model.User
	s.DB.Take(&got, u.ID)
	if got.CommissionBalance != 700 || got.Balance != 310 {
		t.Errorf("划转后余额不正确: commission=%d balance=%d", got.CommissionBalance, got.Balance)
	}
	var o model.Order
	s.DB.Where("user_id = ?", u.ID).Take(&o)
	if o.Type != 9 || o.Status != 3 || o.Period != "deposit" || o.TotalAmount != 0 || o.SurplusAmount == nil || *o.SurplusAmount != 300 ||
		o.CallbackNo == nil || *o.CallbackNo != "佣金划转 Commission transfer" || len(o.TradeNo) != 25 || o.SubscriptionID != nil {
		t.Errorf("划转订单不正确: %+v", o)
	}
	if o.InviteUserID != nil || o.CommissionBalance != 0 {
		t.Errorf("划转订单不应产生邀请佣金（决策 D17）: invite=%v commission=%d", o.InviteUserID, o.CommissionBalance)
	}
}

func testGiftcard(t *testing.T, s *Service) {
	ctx := context.Background()
	u, primary := createUser(t, s.DB, "g@x.com")
	now := time.Now().Unix()
	plan := model.Plan{Name: "礼品套餐", GroupID: 7, TransferEnable: 50, DeviceLimit: ptr(int64(3)), SpeedLimit: ptr(int64(100))}
	s.DB.Create(&plan)
	card := func(code string, typ int, value int64, mut func(g *model.Giftcard)) {
		g := model.Giftcard{Code: code, Name: code, Type: typ, Value: &value}
		if mut != nil {
			mut(&g)
		}
		if err := s.DB.Create(&g).Error; err != nil {
			t.Fatal(err)
		}
	}
	redeem := func(code string, subID int64) (*RedeemResult, error) {
		return s.RedeemGiftcard(ctx, u.ID, RedeemInput{Code: code, SubscriptionID: subID})
	}
	sub := func(id int64) model.Subscription {
		var x model.Subscription
		s.DB.Take(&x, id)
		return x
	}

	_, err := redeem("", 0)
	wantErr(t, err, errGiftcardRequired)
	_, err = redeem("NONE", 0)
	wantErr(t, err, errGiftcardNotExist)
	card("FUTURE", 1, 1, func(g *model.Giftcard) { g.StartedAt = now + 3600 })
	_, err = redeem("FUTURE", 0)
	wantErr(t, err, errGiftcardNotYet)
	card("PAST", 1, 1, func(g *model.Giftcard) { g.EndedAt = now - 3600 })
	_, err = redeem("PAST", 0)
	wantErr(t, err, errGiftcardExpired)
	card("ZERO", 1, 1, func(g *model.Giftcard) { g.LimitUse = ptr(int64(0)) })
	_, err = redeem("ZERO", 0)
	wantErr(t, err, errGiftcardLimit)
	card("UNKNOWN", 9, 1, nil)
	_, err = redeem("UNKNOWN", 0)
	wantErr(t, err, errGiftcardUnknown)

	// 1 余额：可用次数减一、记录已使用用户、同一用户不能重复使用
	card("BAL", 1, 888, func(g *model.Giftcard) { g.LimitUse = ptr(int64(5)) })
	res, err := redeem("BAL", 0)
	if err != nil || res.Type != 1 || res.Value != 888 {
		t.Fatalf("余额礼品卡兑换失败: %+v %v", res, err)
	}
	var gu model.User
	s.DB.Take(&gu, u.ID)
	var gc model.Giftcard
	s.DB.Where("code = ?", "BAL").Take(&gc)
	if gu.Balance != 888 || *gc.LimitUse != 4 || len(gc.UsedUserIDs) != 1 || gc.UsedUserIDs[0] != u.ID {
		t.Errorf("余额礼品卡结果不正确: balance=%d limit=%d used=%v", gu.Balance, *gc.LimitUse, gc.UsedUserIDs)
	}
	_, err = redeem("BAL", 0)
	wantErr(t, err, errGiftcardUsed)

	// 2 时长：不指定订阅时作用于主订阅；已到期从现在起算；长期有效的订阅不适用
	card("DAYS", 2, 10, nil)
	if _, err := redeem("DAYS", 0); err != nil {
		t.Fatal(err)
	}
	if e := *sub(primary.ID).ExpiredAt; e < now+10*86400-5 || e > now+10*86400+5 {
		t.Errorf("已到期订阅加时长应从现在起算: %d", e)
	}
	card("DAYS2", 2, 5, nil)
	before := *sub(primary.ID).ExpiredAt
	redeem("DAYS2", primary.ID)
	if *sub(primary.ID).ExpiredAt != before+5*86400 {
		t.Error("未到期订阅应在原到期时间上延长")
	}
	s.DB.Model(&model.Subscription{}).Where("id = ?", primary.ID).Update("expired_at", nil)
	card("DAYS3", 2, 5, nil)
	_, err = redeem("DAYS3", 0)
	wantErr(t, err, errGiftcardNotSuit)

	// 3 流量、4 重置流量
	card("GB", 3, 2, nil)
	redeem("GB", primary.ID)
	if sub(primary.ID).TransferEnable != 2*1073741824 {
		t.Error("流量礼品卡应增加 2GB")
	}
	// 本周期奖励流量 1GB（决策 D87）：重置时收回，流量礼品卡增加的流量保留
	s.DB.Model(&model.Subscription{}).Where("id = ?", primary.ID).Updates(map[string]any{"u": 10, "d": 20,
		"transfer_enable": 3 * 1073741824, "bonus_traffic": 1073741824})
	card("RESET", 4, 0, nil)
	redeem("RESET", primary.ID)
	if x := sub(primary.ID); x.U != 0 || x.D != 0 || x.TransferEnable != 2*1073741824 || x.BonusTraffic != 0 {
		t.Errorf("重置流量礼品卡应清零已用流量并收回奖励流量：%+v", x)
	}

	// 不能作用于其他用户的订阅
	_, otherSub := createUser(t, s.DB, "other@x.com")
	card("GB2", 3, 1, nil)
	if _, err := redeem("GB2", otherSub.ID); !errors.Is(err, subscription.ErrNotFound) {
		t.Errorf("不能作用于其他用户的订阅，实际 %v", err)
	}
	var gb2 model.Giftcard
	s.DB.Where("code = ?", "GB2").Take(&gb2)
	if len(gb2.UsedUserIDs) != 0 {
		t.Error("兑换失败时不应记录使用")
	}

	// 5 套餐：不指定订阅时新开订阅，含限速（决策 D18、D19）
	card("PLAN", 5, 30, func(g *model.Giftcard) { g.PlanID = &plan.ID })
	res, err = redeem("PLAN", 0)
	if err != nil || res.SubscriptionID == 0 || res.SubscriptionID == primary.ID {
		t.Fatalf("套餐礼品卡应新开订阅: %+v %v", res, err)
	}
	ns := sub(res.SubscriptionID)
	if ns.UserID != u.ID || ns.IsPrimary != 0 || *ns.PlanID != plan.ID || *ns.GroupID != 7 || ns.TransferEnable != 50*1073741824 ||
		*ns.DeviceLimit != 3 || ns.SpeedLimit == nil || *ns.SpeedLimit != 100 || *ns.ExpiredAt < now+30*86400-5 {
		t.Errorf("新开订阅不正确: %+v", ns)
	}

	// 套餐卡作用于未到期的订阅：不适用；作用于已到期的订阅：开通套餐，天数为 0 时长期有效
	card("PLAN2", 5, 0, func(g *model.Giftcard) { g.PlanID = &plan.ID })
	_, err = redeem("PLAN2", ns.ID)
	wantErr(t, err, errGiftcardNotSuit)
	s.DB.Model(&model.Subscription{}).Where("id = ?", ns.ID).Updates(map[string]any{"expired_at": now - 10, "u": 5, "speed_limit": nil})
	if _, err := redeem("PLAN2", ns.ID); err != nil {
		t.Fatal(err)
	}
	if x := sub(ns.ID); x.ExpiredAt != nil || x.U != 0 || x.SpeedLimit == nil || *x.SpeedLimit != 100 {
		t.Errorf("已到期订阅开通套餐结果不正确: %+v", x)
	}

	// 套餐卡新开订阅受数量上限约束
	s.DB.Model(&model.User{}).Where("id = ?", u.ID).Update("subscription_limit", 2)
	card("PLAN3", 5, 30, func(g *model.Giftcard) { g.PlanID = &plan.ID })
	if _, err := redeem("PLAN3", 0); !errors.Is(err, subscription.ErrLimit) {
		t.Errorf("超过订阅上限时不能新开，实际 %v", err)
	}
}

func testGiftcardConcurrent(t *testing.T, s *Service) {
	ctx := context.Background()
	one := int64(1)
	value := int64(100)
	s.DB.Create(&model.Giftcard{Code: "ONCE", Name: "x", Type: 1, Value: &value, LimitUse: &one})
	var ids []int64
	for i := 0; i < 5; i++ {
		u, _ := createUser(t, s.DB, string(rune('a'+i))+"@x.com")
		ids = append(ids, u.ID)
	}
	var wg sync.WaitGroup
	var mu sync.Mutex
	success := 0
	for _, id := range ids {
		wg.Add(1)
		go func(id int64) {
			defer wg.Done()
			if _, err := s.RedeemGiftcard(ctx, id, RedeemInput{Code: "ONCE"}); err == nil {
				mu.Lock()
				success++
				mu.Unlock()
			}
		}(id)
	}
	wg.Wait()
	var total int64
	s.DB.Model(&model.User{}).Where("id IN ?", ids).Select("COALESCE(SUM(balance), 0)").Scan(&total)
	if success != 1 || total != 100 {
		t.Errorf("只能使用一次的礼品卡并发兑换：成功 %d 次，发放余额 %d", success, total)
	}
}

func testHTTP(t *testing.T, s *Service) {
	ctx := context.Background()
	u, primary := createUser(t, s.DB, "h@x.com")
	token, _ := s.Auth.Issue(ctx, u.ID, "", "")
	cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
	r, err := server.NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	r.Mount(&Module{Svc: s})
	do := func(method, path, body, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Host = "panel.example"
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

	if w := do("GET", "/api/t1/user/subscriptions", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("未登录应返回 401，实际 %d", w.Code)
	}
	w := do("GET", "/api/t1/user/subscriptions", "", token)
	var list struct {
		Data []subscription.View `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &list)
	if w.Code != 200 || len(list.Data) != 1 || list.Data[0].SubscribeURL != "http://panel.example/api/v1/client/subscribe?token="+primary.Token {
		t.Errorf("订阅列表接口不正确: %d %s", w.Code, w.Body)
	}
	if w := do("PUT", "/api/t1/user/subscriptions/abc", `{"name":"x"}`, token); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("非法订阅 ID 应返回 422，实际 %d", w.Code)
	}
	if w := do("PUT", "/api/t1/user/subscriptions/999999", `{"name":"x"}`, token); w.Code != http.StatusNotFound {
		t.Errorf("不存在的订阅应返回 404，实际 %d", w.Code)
	}
	if w := do("PUT", "/api/t1/user/subscriptions/"+itoa(primary.ID), `{"name":"手机"}`, token); w.Code != 200 {
		t.Errorf("修改订阅失败: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/t1/user/info", "", token); w.Code != 200 || !strings.Contains(w.Body.String(), `"email":"h@x.com"`) {
		t.Errorf("用户信息接口不正确: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/t1/user/sessions", "", token); w.Code != 200 || strings.Contains(w.Body.String(), token) {
		t.Errorf("会话列表不应包含登录凭证: %s", w.Body)
	}

	if w := do("GET", "/api/t1/user/config", "", ""); w.Code != http.StatusUnauthorized {
		t.Errorf("用户配置未登录应返回 401，实际 %d", w.Code)
	}
	w = do("GET", "/api/t1/user/config", "", token)
	var uc struct {
		Data UserConfig `json:"data"`
	}
	_ = json.Unmarshal(w.Body.Bytes(), &uc)
	if w.Code != 200 || uc.Data.Currency != "CNY" || uc.Data.Withdraw.Min != 100 || len(uc.Data.Withdraw.Methods) == 0 ||
		len(uc.Data.Distribution.Levels) != 3 || uc.Data.Distribution.Levels[0] != nil || !uc.Data.PlanChange || uc.Data.DepositBonus == nil {
		t.Errorf("用户配置接口不正确: %d %s", w.Code, w.Body)
	}
}

func itoa(n int64) string {
	b, _ := json.Marshal(n)
	return string(b)
}
