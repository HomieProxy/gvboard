package subscription

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/cache"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/setting"
	"gvboard/internal/testutil"
)

func newService(t *testing.T, driver string) *Service {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &Service{DB: db, Cache: c, Settings: st, Linker: &Linker{Settings: st, Cache: c}}
}

func createUser(t *testing.T, db *gorm.DB, email string) (*model.User, *model.Subscription) {
	t.Helper()
	var u *model.User
	var sub *model.Subscription
	err := db.Transaction(func(tx *gorm.DB) error {
		var err error
		u, sub, err = account.Create(tx, account.NewUser{Email: email, Password: "password123"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	return u, sub
}

func set(t *testing.T, s *Service, kv map[string]any) {
	t.Helper()
	if err := s.Settings.Set(context.Background(), kv); err != nil {
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

func TestService(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			t.Run("查询与链接", func(t *testing.T) { testQueryAndLink(t, newService(t, driver)) })
			t.Run("修改与重置", func(t *testing.T) { testUpdateAndReset(t, newService(t, driver)) })
			t.Run("提前开始新周期", func(t *testing.T) { testNewPeriod(t, newService(t, driver)) })
			t.Run("新开订阅与上限", func(t *testing.T) { testCreateLimit(t, newService(t, driver)) })
		})
	}
}

func testQueryAndLink(t *testing.T, s *Service) {
	ctx := context.Background()
	u, primary := createUser(t, s.DB, "a@x.com")
	other, _ := createUser(t, s.DB, "b@x.com")

	plan := model.Plan{Name: "月付", GroupID: 1, TransferEnable: 100}
	s.DB.Create(&plan)
	var second *model.Subscription
	s.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		second, err = s.Create(tx, NewSubscription{UserID: u.ID, Name: "第二个", Plan: &plan, ExpiredAt: ptr(time.Now().Unix() + 86400*40)})
		return err
	})
	_ = s.Cache.SetJSON(ctx, s.Cache.KeyOf(cache.KeyAliveIP, second.ID), map[string]any{"alive_ip": 3}, time.Minute)

	list, err := s.List(ctx, u.ID, "http://req.host")
	if err != nil || len(list) != 2 {
		t.Fatalf("订阅列表不正确: %v %v", list, err)
	}
	if !list[0].IsPrimary || list[0].ID != primary.ID || list[0].Plan != nil || list[0].ResetDay != nil {
		t.Errorf("主订阅应排在第一位且无套餐: %+v", list[0])
	}
	sv := list[1]
	if sv.Name != "第二个" || sv.Plan == nil || sv.Plan.Name != "月付" || sv.TransferEnable != 100*1073741824 ||
		sv.AliveIP != 3 || sv.ResetDay == nil || !sv.RemindExpire || sv.AutoRenewal {
		t.Errorf("第二个订阅信息不正确: %+v", sv)
	}
	if want := "http://req.host/api/v1/client/subscribe?token=" + primary.Token; list[0].SubscribeURL != want {
		t.Errorf("默认订阅链接不正确: %s", list[0].SubscribeURL)
	}

	if _, err := s.Get(ctx, other.ID, second.ID, ""); !errors.Is(err, ErrNotFound) {
		t.Errorf("不能读取其他用户的订阅，实际 %v", err)
	}

	// 自定义订阅路径与订阅域名
	set(t, s, map[string]any{"subscribe_path": "/s/abc", "subscribe_url": "https://a.example,https://b.example"})
	v, _ := s.Get(ctx, u.ID, primary.ID, "http://req.host")
	if !strings.HasSuffix(v.SubscribeURL, "/s/abc?token="+primary.Token) ||
		!(strings.HasPrefix(v.SubscribeURL, "https://a.example/") || strings.HasPrefix(v.SubscribeURL, "https://b.example/")) {
		t.Errorf("订阅链接应使用订阅域名与路径: %s", v.SubscribeURL)
	}

	// 一次性 token：24 小时内复用，且能反查到订阅 token
	set(t, s, map[string]any{"show_subscribe_method": LinkOneTime})
	v1, _ := s.Get(ctx, u.ID, primary.ID, "")
	v2, _ := s.Get(ctx, u.ID, primary.ID, "")
	otp := v1.SubscribeURL[strings.Index(v1.SubscribeURL, "token=")+6:]
	if !strings.HasSuffix(v2.SubscribeURL, "token="+otp) || otp == primary.Token || len(otp) != 32 {
		t.Errorf("一次性 token 不正确: %s / %s", v1.SubscribeURL, v2.SubscribeURL)
	}
	if got, _ := s.Cache.Get(ctx, s.Cache.KeyOf(cache.KeySubscribeOTPToken, otp)).Result(); got != primary.Token {
		t.Errorf("一次性 token 应能反查订阅 token，实际 %q", got)
	}

	// 时效 token：ID 为订阅 ID
	set(t, s, map[string]any{"show_subscribe_method": LinkTOTP, "show_subscribe_expire": 10})
	now := time.Now()
	s.Linker.Now = func() time.Time { return now }
	v, _ = s.Get(ctx, u.ID, second.ID, "")
	if !strings.HasSuffix(v.SubscribeURL, "?token="+TOTPToken(second.ID, second.Token, 10, now)) {
		t.Errorf("时效 token 不正确: %s", v.SubscribeURL)
	}
}

func testUpdateAndReset(t *testing.T, s *Service) {
	ctx := context.Background()
	u, sub := createUser(t, s.DB, "a@x.com")
	other, _ := createUser(t, s.DB, "b@x.com")

	name, on, off, bad := "  家里用  ", 1, 0, 2
	if err := s.Update(ctx, u.ID, sub.ID, UpdateInput{Name: &name, AutoRenewal: &on, RemindExpire: &off}); err != nil {
		t.Fatal(err)
	}
	var got model.Subscription
	s.DB.Take(&got, sub.ID)
	if got.Name != "家里用" || got.AutoRenewal != 1 || got.RemindExpire != 0 || got.RemindTraffic != 1 {
		t.Errorf("修改结果不正确: %+v", got)
	}
	wantErr(t, s.Update(ctx, u.ID, sub.ID, UpdateInput{RemindTraffic: &bad}), errInvalidParams)
	long := strings.Repeat("名", 65)
	wantErr(t, s.Update(ctx, u.ID, sub.ID, UpdateInput{Name: &long}), errInvalidParams)
	if err := s.Update(ctx, other.ID, sub.ID, UpdateInput{Name: &name}); !errors.Is(err, ErrNotFound) {
		t.Errorf("不能修改其他用户的订阅，实际 %v", err)
	}

	link, err := s.ResetSecurity(ctx, u.ID, sub.ID, "http://h")
	if err != nil {
		t.Fatal(err)
	}
	var after model.Subscription
	s.DB.Take(&after, sub.ID)
	if after.UUID == sub.UUID || after.Token == sub.Token || len(after.Token) != 32 || !strings.HasSuffix(link, "token="+after.Token) {
		t.Errorf("重置后 uuid / token 应改变: %+v %s", after, link)
	}
}

func testNewPeriod(t *testing.T, s *Service) {
	ctx := context.Background()
	u, sub := createUser(t, s.DB, "a@x.com")
	now := time.Now()
	s.Now = func() time.Time { return now }

	wantErr(t, s.NewPeriod(ctx, u.ID, sub.ID), errNewPeriodDisabled)
	set(t, s, map[string]any{"allow_new_period": 1})

	plan := model.Plan{Name: "按到期日", GroupID: 1, TransferEnable: 1, ResetTrafficMethod: ptr(ResetExpireDay)}
	s.DB.Create(&plan)
	exp := now.Unix() + 100*86400
	s.DB.Model(sub).Updates(map[string]any{"plan_id": plan.ID, "transfer_enable": 1073741824, "u": 100, "d": 0, "expired_at": exp})
	wantErr(t, s.NewPeriod(ctx, u.ID, sub.ID), errTrafficRemaining)

	s.DB.Model(sub).Updates(map[string]any{"u": 1073741824, "d": 5})
	var cur model.Subscription
	s.DB.Take(&cur, sub.ID)
	resetDay := *ResetDay(&cur, &plan, 0, now)
	if resetDay <= 0 {
		resetDay = 30
	}
	if err := s.NewPeriod(ctx, u.ID, sub.ID); err != nil {
		t.Fatal(err)
	}
	var got model.Subscription
	s.DB.Take(&got, sub.ID)
	if got.U != 0 || got.D != 0 || *got.ExpiredAt != exp-int64(resetDay)*86400 {
		t.Errorf("提前开始新周期结果不正确: u=%d d=%d expired=%d 期望 %d", got.U, got.D, *got.ExpiredAt, exp-int64(resetDay)*86400)
	}

	// 剩余时间不足一个周期
	s.DB.Model(sub).Updates(map[string]any{"u": 1073741824, "expired_at": now.Unix() + 20*86400})
	wantErr(t, s.NewPeriod(ctx, u.ID, sub.ID), errNotEnoughTime)

	// 套餐不重置流量
	s.DB.Model(&plan).Update("reset_traffic_method", ResetNever)
	s.DB.Model(sub).Update("expired_at", now.Unix()+100*86400)
	wantErr(t, s.NewPeriod(ctx, u.ID, sub.ID), errNoNewPeriod)
}

func testCreateLimit(t *testing.T, s *Service) {
	u, _ := createUser(t, s.DB, "a@x.com")
	create := func() error {
		return s.DB.Transaction(func(tx *gorm.DB) error {
			_, err := s.Create(tx, NewSubscription{UserID: u.ID})
			return err
		})
	}
	set(t, s, map[string]any{"subscription_limit": 3})
	for i := 0; i < 2; i++ {
		if err := create(); err != nil {
			t.Fatalf("第 %d 个订阅创建失败: %v", i+2, err)
		}
	}
	if err := create(); !errors.Is(err, ErrLimit) {
		t.Errorf("超过系统上限应失败，实际 %v", err)
	}
	s.DB.Model(u).Update("subscription_limit", 4)
	if err := create(); err != nil {
		t.Errorf("用户单独上限为 4 时应能创建: %v", err)
	}
	if err := create(); !errors.Is(err, ErrLimit) {
		t.Errorf("超过用户单独上限应失败，实际 %v", err)
	}
	var n int64
	s.DB.Model(&model.Subscription{}).Where("user_id = ? AND is_primary = 1", u.ID).Count(&n)
	if n != 1 {
		t.Errorf("新开的订阅不应是主订阅，主订阅数量 %d", n)
	}
}
