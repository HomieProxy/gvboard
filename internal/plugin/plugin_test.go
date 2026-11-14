package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/hibiken/asynq"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/cache"
	"gvboard/internal/config"
	"gvboard/internal/hook"
	"gvboard/internal/model"
	"gvboard/internal/payment"
	"gvboard/internal/queue"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/testutil"
	sdk "gvboard/pkg/plugin"
)

func init() { gin.SetMode(gin.TestMode) }

// fake 测试插件。
type fake struct {
	code    string
	version string
	fields  []sdk.Field
	setup   func(h sdk.Host) error
	enable  func(ctx context.Context, h sdk.Host) error
	disable func(ctx context.Context, h sdk.Host) error
}

func (f *fake) Info() sdk.Info {
	v := f.version
	if v == "" {
		v = "1.0.0"
	}
	return sdk.Info{Code: f.code, Name: f.code, Version: v}
}
func (f *fake) Fields() []sdk.Field { return f.fields }
func (f *fake) Setup(h sdk.Host) error {
	if f.setup == nil {
		return nil
	}
	return f.setup(h)
}
func (f *fake) Enable(ctx context.Context, h sdk.Host) error {
	if f.enable == nil {
		return nil
	}
	return f.enable(ctx, h)
}
func (f *fake) Disable(ctx context.Context, h sdk.Host) error {
	if f.disable == nil {
		return nil
	}
	return f.disable(ctx, h)
}

type fakeGateway struct{}

func (fakeGateway) Form() payment.Form { return payment.Form{} }
func (fakeGateway) Pay(*payment.Env, *payment.Order) (*payment.Result, error) {
	return nil, errors.New("x")
}
func (fakeGateway) Notify(*payment.Env, *payment.Notification) (*payment.Paid, error) {
	return nil, errors.New("x")
}

type env struct {
	db       *gorm.DB
	cache    *cache.Cache
	auth     *auth.Service
	settings *setting.Service
	now      time.Time
}

func newEnv(t *testing.T, driver string) *env {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	return &env{db: db, cache: c, auth: auth.New(db, c, "base64:cGx1Z2luLXRlc3Qta2V5LTAxMjM0NTY3ODkwMTI="), settings: st,
		now: time.Date(2026, 11, 14, 12, 0, 0, 0, time.Local)}
}

func eachDriver(t *testing.T, f func(t *testing.T, e *env)) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, d := range drivers {
		t.Run(d, func(t *testing.T) { f(t, newEnv(t, d)) })
	}
}

func (e *env) manager(t *testing.T, plugins ...sdk.Plugin) *Manager {
	t.Helper()
	m := &Manager{DB: e.db, Cache: e.cache, Auth: e.auth, Settings: e.settings, Log: zap.NewNop(),
		FilterTimeout: 200 * time.Millisecond, Now: func() time.Time { return e.now }}
	t.Cleanup(m.Close)
	if err := m.Load(context.Background(), plugins); err != nil {
		t.Fatal(err)
	}
	return m
}

func mustEnable(t *testing.T, m *Manager, codes ...string) {
	t.Helper()
	for _, c := range codes {
		if err := m.Enable(context.Background(), c); err != nil {
			t.Fatalf("启用 %s 失败：%v", c, err)
		}
	}
}

func statusOf(t *testing.T, m *Manager, code string) View {
	t.Helper()
	v, err := m.Get(context.Background(), code)
	if err != nil {
		t.Fatalf("获取插件 %s 失败：%v", code, err)
	}
	return *v
}

func TestLoadInvalid(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		m := &Manager{DB: e.db, Auth: e.auth, Settings: e.settings}
		t.Cleanup(m.Close)
		ctx := context.Background()
		for _, ps := range [][]sdk.Plugin{
			{&fake{code: "Bad"}},
			{&fake{code: "x"}},
			{&fake{code: "dup"}, &fake{code: "dup"}},
			{nil},
		} {
			if err := m.Load(ctx, ps); err == nil {
				t.Errorf("应加载失败：%v", ps)
			}
		}
	})
}

func TestLoad(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		var late sdk.Host
		ok := &fake{code: "okp", setup: func(h sdk.Host) error {
			h.On(sdk.EventUserRegistered, func(context.Context, sdk.Event) error { return nil })
			sdk.AddFilter(h, sdk.FilterOrderAmount, 0, func(_ context.Context, v sdk.OrderAmount) (sdk.OrderAmount, error) { return v, nil })
			h.Job("tick", "*/5 * * * *", func(context.Context) error { return nil })
			h.GuestRoutes().GET("/ping", func(c *gin.Context) {})
			h.AdminRoutes().GET("/stat", func(c *gin.Context) {})
			late = h
			return nil
		}}
		plugins := []sdk.Plugin{
			ok,
			&fake{code: "seterr", setup: func(h sdk.Host) error {
				h.Payment("plugbad", fakeGateway{})
				return errors.New("boom")
			}},
			&fake{code: "panics", setup: func(sdk.Host) error { panic("oops") }},
			&fake{code: "badevent", setup: func(h sdk.Host) error {
				h.On("no.such", func(context.Context, sdk.Event) error { return nil })
				return nil
			}},
			&fake{code: "badfilter", setup: func(h sdk.Host) error {
				h.AddFilter("no.such", 0, func(_ context.Context, v any) (any, error) { return v, nil })
				return nil
			}},
			&fake{code: "badjob", setup: func(h sdk.Host) error {
				h.Job("tick", "bad spec", func(context.Context) error { return nil })
				return nil
			}},
			&fake{code: "badfield", fields: []sdk.Field{{Key: "a", Type: "x"}}},
			&fake{code: "dupformat", setup: func(h sdk.Host) error {
				h.SubscribeFormat(&sdk.SubscribeFormat{Name: "Clash", Flag: "clash", Handle: func(*sdk.SubscribeContext) (*sdk.SubscribeResponse, error) { return nil, nil }})
				return nil
			}},
		}
		if err := e.db.Create(&model.Plugin{Code: "jsone", Type: model.PluginTypeJS, Version: "1"}).Error; err != nil {
			t.Fatal(err)
		}
		if err := e.db.Create(&model.Plugin{Code: "gone", Type: model.PluginTypeGo, Version: "1", Enabled: 1}).Error; err != nil {
			t.Fatal(err)
		}
		plugins = append(plugins, &fake{code: "jsone"})
		m := e.manager(t, plugins...)

		if v := statusOf(t, m, "okp"); v.Status != StatusOK || v.Enabled ||
			len(v.Features.Events) != 1 || len(v.Features.Filters) != 1 || len(v.Features.Jobs) != 1 || len(v.Features.Routes) != 2 {
			t.Fatalf("正常插件信息错误：%+v", v)
		}
		for _, code := range []string{"seterr", "panics", "badevent", "badfilter", "badjob", "badfield", "dupformat", "jsone"} {
			v := statusOf(t, m, code)
			if v.Status != StatusError || v.Error == "" {
				t.Errorf("%s 应加载失败：%+v", code, v)
			}
			if err := m.Enable(ctx, code); err == nil {
				t.Errorf("加载失败的 %s 不应能启用", code)
			}
		}
		if _, ok := payment.Lookup("plugbad"); ok {
			t.Error("加载失败插件的支付网关应被注销")
		}
		if err := m.Enable(ctx, "nope"); !errors.Is(err, errNotFound) {
			t.Errorf("不存在的插件应返回 404：%v", err)
		}
		if err := m.Enable(ctx, "gone"); !errors.Is(err, errMissing) {
			t.Errorf("已移除插件不能启用：%v", err)
		}

		// Setup 之后注册无效
		late.On(sdk.EventOrderPaid, func(context.Context, sdk.Event) error { return nil })
		late.GuestRoutes().GET("/late", func(c *gin.Context) {})
		if v := statusOf(t, m, "okp"); len(v.Features.Events) != 1 || len(v.Features.Routes) != 2 {
			t.Fatalf("Setup 之后不应能注册：%+v", v.Features)
		}

		list, err := m.List(ctx)
		if err != nil || len(list) != len(plugins)+1 || list[len(list)-1].Code != "gone" || list[len(list)-1].Status != StatusMissing {
			t.Fatalf("列表错误：%d %v", len(list), err)
		}
		var rows []model.Plugin
		e.db.Order("code").Find(&rows)
		if len(rows) != len(plugins)+1 {
			t.Fatalf("应登记 %d 个插件，实际 %d", len(plugins)+1, len(rows))
		}
		for _, r := range rows {
			if r.Enabled != 0 && r.Code != "gone" {
				t.Errorf("新插件应默认停用：%s", r.Code)
			}
		}

		if err := m.Delete(ctx, "okp"); !errors.Is(err, errNotMissing) {
			t.Errorf("程序中的插件不能删除：%v", err)
		}
		if err := m.Delete(ctx, "gone"); !errors.Is(err, errNotDisabled) {
			t.Errorf("启用中的插件不能删除：%v", err)
		}
		if err := m.Disable(ctx, "gone"); err != nil {
			t.Fatal(err)
		}
		e.db.Create(&model.PluginKV{PluginCode: "gone", K: "a", V: "b"})
		if err := m.Delete(ctx, "gone"); err != nil {
			t.Fatal(err)
		}
		var n int64
		e.db.Model(&model.PluginKV{}).Where("plugin_code = ?", "gone").Count(&n)
		if _, err := m.Get(ctx, "gone"); !errors.Is(err, errNotFound) || n != 0 {
			t.Fatalf("删除后记录与 KV 应清除：%v %d", err, n)
		}

		// 版本更新
		m.Close()
		m2 := e.manager(t, &fake{code: "okp", version: "2.0.0"})
		if v := statusOf(t, m2, "okp"); v.Version != "2.0.0" {
			t.Fatalf("版本未更新：%+v", v.Info)
		}
		var row model.Plugin
		e.db.Where("code = ?", "okp").Take(&row)
		if row.Version != "2.0.0" {
			t.Fatalf("数据库版本未更新：%s", row.Version)
		}
	})
}

func TestEnableConfig(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		var mu sync.Mutex
		disabled := 0
		newPlugin := func() *fake {
			return &fake{
				code: "cfg",
				fields: []sdk.Field{
					{Key: "token", Type: sdk.FieldPassword, Required: true},
					{Key: "n", Type: sdk.FieldNumber, Default: 5},
					{Key: "mode", Type: sdk.FieldSelect, Default: "a", Options: []sdk.Option{{Label: "A", Value: "a"}, {Label: "B", Value: "b"}}},
					{Key: "on", Type: sdk.FieldBool},
				},
				enable: func(_ context.Context, h sdk.Host) error {
					if h.Config().String("token") == "bad" {
						return errors.New("token 无效")
					}
					return nil
				},
				disable: func(context.Context, sdk.Host) error {
					mu.Lock()
					defer mu.Unlock()
					disabled++
					return errors.New("忽略")
				},
			}
		}
		m := e.manager(t, newPlugin())
		if err := m.Enable(ctx, "cfg"); err == nil {
			t.Fatal("缺少必填配置时不应能启用")
		}
		for _, in := range []map[string]any{{"x": 1}, {"n": "abc"}, {"mode": "c"}, {"on": "maybe"}, {"token": strings.Repeat("a", 2001)}, {"token": ""}} {
			if err := m.SaveConfig(ctx, "cfg", in); err == nil {
				t.Errorf("配置应校验失败：%v", in)
			}
		}
		if err := m.SaveConfig(ctx, "cfg", map[string]any{"token": "bad"}); err != nil {
			t.Fatal(err)
		}
		if cfg := m.config("cfg"); cfg.Int("n") != 5 || cfg.String("mode") != "a" || cfg.Bool("on") {
			t.Fatalf("默认值错误：%v", cfg)
		}
		if err := m.Enable(ctx, "cfg"); err == nil || m.Enabled("cfg") {
			t.Fatal("Enable 回调失败时不应启用")
		}
		if err := m.SaveConfig(ctx, "cfg", map[string]any{"token": "good", "n": "7", "on": true}); err != nil {
			t.Fatal(err)
		}
		mustEnable(t, m, "cfg")
		if cfg := m.config("cfg"); !m.Enabled("cfg") || cfg.Int("n") != 7 || cfg.String("token") != "good" || !cfg.Bool("on") {
			t.Fatalf("启用或配置错误：%v %v", m.Enabled("cfg"), cfg)
		}

		// 多实例同步
		m2 := e.manager(t, newPlugin())
		wctx, cancel := context.WithCancel(ctx)
		defer cancel()
		go m2.Watch(wctx)
		time.Sleep(300 * time.Millisecond)
		if !m2.Enabled("cfg") {
			t.Fatal("第二个实例应读取到启用状态")
		}
		if err := m.SaveConfig(ctx, "cfg", map[string]any{"n": 9}); err != nil {
			t.Fatal(err)
		}
		if err := m.Disable(ctx, "cfg"); err != nil {
			t.Fatal(err)
		}
		deadline := time.Now().Add(5 * time.Second)
		for (m2.Enabled("cfg") || m2.config("cfg").Int("n") != 9) && time.Now().Before(deadline) {
			time.Sleep(50 * time.Millisecond)
		}
		if m2.Enabled("cfg") || m2.config("cfg").Int("n") != 9 {
			t.Fatal("第二个实例未同步变更")
		}
		mu.Lock()
		defer mu.Unlock()
		if disabled != 1 || m.Enabled("cfg") {
			t.Fatalf("停用回调应调用一次：%d", disabled)
		}
	})
}

func TestEventsAndFilters(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		var mu sync.Mutex
		var got []string
		record := func(code string) sdk.EventHandler {
			return func(_ context.Context, ev sdk.Event) error {
				var u sdk.UserEvent
				if err := ev.Decode(&u); err != nil {
					return err
				}
				mu.Lock()
				defer mu.Unlock()
				got = append(got, fmt.Sprintf("%s:%s:%d", code, ev.Name, u.UserID))
				return nil
			}
		}
		amount := func(code string, priority int, fn func(v sdk.OrderAmount) (sdk.OrderAmount, error)) *fake {
			return &fake{code: code, setup: func(h sdk.Host) error {
				sdk.AddFilter(h, sdk.FilterOrderAmount, priority, func(_ context.Context, v sdk.OrderAmount) (sdk.OrderAmount, error) { return fn(v) })
				return nil
			}}
		}
		m := e.manager(t,
			&fake{code: "ev_a", setup: func(h sdk.Host) error {
				h.On(sdk.EventUserRegistered, record("ev_a"))
				h.On(sdk.EventUserRegistered, func(context.Context, sdk.Event) error { panic("x") })
				return nil
			}},
			&fake{code: "ev_b", setup: func(h sdk.Host) error {
				h.On(sdk.EventUserRegistered, record("ev_b"))
				return nil
			}},
			amount("f_disc", 10, func(v sdk.OrderAmount) (sdk.OrderAmount, error) { v.Amount = v.Amount * 9 / 10; return v, nil }),
			amount("f_add", 5, func(v sdk.OrderAmount) (sdk.OrderAmount, error) { v.Amount += 100; return v, nil }),
			amount("f_err", 1, func(v sdk.OrderAmount) (sdk.OrderAmount, error) { v.Amount = 1; return v, errors.New("x") }),
			amount("f_panic", 1, func(v sdk.OrderAmount) (sdk.OrderAmount, error) { panic("x") }),
			amount("f_slow", 1, func(v sdk.OrderAmount) (sdk.OrderAmount, error) {
				time.Sleep(time.Second)
				v.Amount = 1
				return v, nil
			}),
			amount("f_neg", 20, func(v sdk.OrderAmount) (sdk.OrderAmount, error) { v.Amount = -5; return v, nil }),
			&fake{code: "f_type", setup: func(h sdk.Host) error {
				h.AddFilter(hook.FilterOrderAmount.Name, 1, func(context.Context, any) (any, error) { return "str", nil })
				return nil
			}},
		)
		in := hook.OrderAmount{UserID: 1, Amount: 1000}
		if out := hook.Apply(m, ctx, hook.FilterOrderAmount, in); out != in {
			t.Fatalf("插件未启用时不应修改：%+v", out)
		}
		hook.Emit(m, ctx, hook.EventUserRegistered, hook.User{UserID: 7})
		if len(got) != 0 {
			t.Fatalf("插件未启用时不应收到事件：%v", got)
		}

		mustEnable(t, m, "ev_b", "f_disc", "f_add", "f_err", "f_panic", "f_slow", "f_neg", "f_type")
		start := time.Now()
		if out := hook.Apply(m, ctx, hook.FilterOrderAmount, in); out.Amount != 990 || out.UserID != 1 {
			t.Fatalf("过滤结果应为 (1000+100)*0.9=990：%+v", out)
		}
		if d := time.Since(start); d > 900*time.Millisecond {
			t.Fatalf("超时的过滤器应被跳过：%s", d)
		}

		hook.Emit(m, ctx, hook.EventUserRegistered, hook.User{UserID: 7})
		hook.Emit(m, ctx, hook.EventUserLogin, hook.User{UserID: 7})
		if len(got) != 1 || got[0] != "ev_b:user.registered:7" {
			t.Fatalf("只有已启用的订阅插件应收到事件：%v", got)
		}
		mustEnable(t, m, "ev_a")
		got = nil
		hook.Emit(m, ctx, hook.EventUserRegistered, hook.User{UserID: 8})
		if len(got) != 2 {
			t.Fatalf("两个插件都应收到事件（panic 不影响其它插件）：%v", got)
		}
		if err := m.handleEvent(ctx, eventTask{Plugin: "ev_a", Event: hook.EventUserRegistered, Data: json.RawMessage(`{"user_id":1}`)}); err == nil {
			t.Fatal("处理函数 panic 应返回错误以便重试")
		}
	})
}

func TestQueueEvents(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		rc := config.RedisConfig{Addr: e.cache.Options().Addr, Prefix: e.cache.Prefix()}
		q := queue.New(rc, zap.NewNop())
		t.Cleanup(func() {
			q.Close()
			insp := asynq.NewInspector(asynq.RedisClientOpt{Addr: rc.Addr})
			_ = insp.DeleteQueue(q.Name(queue.QueuePlugin), true)
			insp.Close()
		})
		got := make(chan sdk.Event, 4)
		m := &Manager{DB: e.db, Cache: e.cache, Auth: e.auth, Settings: e.settings, Log: zap.NewNop()}
		t.Cleanup(m.Close)
		m.RegisterTask(q)
		if err := m.Load(ctx, []sdk.Plugin{&fake{code: "qp", setup: func(h sdk.Host) error {
			h.On(sdk.EventOrderPaid, func(_ context.Context, ev sdk.Event) error { got <- ev; return nil })
			return nil
		}}}); err != nil {
			t.Fatal(err)
		}
		mustEnable(t, m, "qp")
		rctx, cancel := context.WithCancel(ctx)
		done := make(chan struct{})
		go func() { _ = q.Run(rctx, 2); close(done) }()
		defer func() { cancel(); <-done }()

		hook.Emit(m, ctx, hook.EventOrderPaid, hook.Order{TradeNo: "T1", TotalAmount: 100})
		select {
		case ev := <-got:
			var o sdk.OrderEvent
			if err := ev.Decode(&o); err != nil || o.TradeNo != "T1" || ev.Name != sdk.EventOrderPaid || ev.Time == 0 {
				t.Fatalf("事件内容错误：%+v %v", ev, err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("队列未处理插件事件")
		}
	})
}

func TestRoutes(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		const secure = "plugintest0123456789"
		if err := e.settings.Set(ctx, map[string]any{"secure_path": secure}); err != nil {
			t.Fatal(err)
		}
		var admin, normal *model.User
		if err := e.db.Transaction(func(tx *gorm.DB) error {
			var err error
			if admin, _, err = account.Create(tx, account.NewUser{Email: "admin@x.com", Password: "password123"}); err != nil {
				return err
			}
			if normal, _, err = account.Create(tx, account.NewUser{Email: "n@x.com", Password: "password123"}); err != nil {
				return err
			}
			return tx.Model(admin).Update("is_admin", 1).Error
		}); err != nil {
			t.Fatal(err)
		}
		adminTok, _ := e.auth.Issue(ctx, admin.ID, "", "")
		userTok, _ := e.auth.Issue(ctx, normal.ID, "", "")

		m := e.manager(t, &fake{code: "rt", fields: []sdk.Field{{Key: "n", Type: sdk.FieldNumber}}, setup: func(h sdk.Host) error {
			h.GuestRoutes().GET("/ping", func(c *gin.Context) { sdk.OK(c, "pong") })
			h.GuestRoutes().GET("/boom", func(c *gin.Context) { panic("x") })
			h.UserRoutes().GET("/me", func(c *gin.Context) { sdk.OK(c, sdk.CurrentUser(c).Email) })
			h.AdminRoutes().GET("/stat", func(c *gin.Context) {
				if u := sdk.CurrentUser(c); u == nil || !u.IsAdmin {
					sdk.Fail(c, 403, "no")
					return
				}
				sdk.OK(c, "stat")
			})
			return nil
		}})
		cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
		r, err := server.NewRouter(cfg, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		r.Mount(m)
		do := func(method, path, body, tok string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, "/api/t1/"+path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			if tok != "" {
				req.Header.Set("Authorization", tok)
			}
			w := httptest.NewRecorder()
			r.Engine.ServeHTTP(w, req)
			return w
		}
		if w := do("GET", "plugin/rt/ping", "", ""); w.Code != http.StatusNotFound {
			t.Fatalf("未启用的插件路由应 404：%d", w.Code)
		}
		if w := do("GET", secure+"/plugins", "", userTok); w.Code != http.StatusNotFound {
			t.Fatalf("普通用户访问管理端应 404：%d", w.Code)
		}
		if w := do("GET", secure+"/plugins", "", adminTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"code":"rt"`) {
			t.Fatalf("插件列表错误：%d %s", w.Code, w.Body)
		}
		if w := do("PUT", secure+"/plugins/rt/config", `{"config":{"n":"x"}}`, adminTok); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("无效配置应 422：%d %s", w.Code, w.Body)
		}
		if w := do("PUT", secure+"/plugins/rt/config", `{"config":{"n":3}}`, adminTok); w.Code != 200 {
			t.Fatalf("保存配置失败：%d %s", w.Code, w.Body)
		}
		if w := do("POST", secure+"/plugins/rt/enable", "", adminTok); w.Code != 200 {
			t.Fatalf("启用失败：%d %s", w.Code, w.Body)
		}
		if w := do("GET", secure+"/plugins/rt", "", adminTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"enabled":true`) {
			t.Fatalf("插件详情错误：%d %s", w.Code, w.Body)
		}

		if w := do("GET", "plugin/rt/ping", "", ""); w.Code != 200 || !strings.Contains(w.Body.String(), "pong") {
			t.Fatalf("访客路由错误：%d %s", w.Code, w.Body)
		}
		if w := do("GET", "plugin/rt/boom", "", ""); w.Code != http.StatusInternalServerError {
			t.Fatalf("panic 应返回 500：%d", w.Code)
		}
		for _, p := range []string{"plugin/rt/nope", "plugin/other/ping", "plugin/rt/stat", "plugin/rt/ping/"} {
			if w := do("GET", p, "", ""); w.Code != http.StatusNotFound {
				t.Errorf("%s 应 404：%d", p, w.Code)
			}
		}
		if w := do("POST", "plugin/rt/ping", "", ""); w.Code != http.StatusNotFound {
			t.Errorf("方法不匹配应 404：%d", w.Code)
		}
		if w := do("GET", "plugin/rt/me", "", ""); w.Code == 200 {
			t.Fatal("用户路由未登录不应成功")
		}
		if w := do("GET", "plugin/rt/me", "", userTok); w.Code != 200 || !strings.Contains(w.Body.String(), "n@x.com") {
			t.Fatalf("用户路由错误：%d %s", w.Code, w.Body)
		}
		if w := do("GET", secure+"/plugin/rt/stat", "", userTok); w.Code != http.StatusNotFound {
			t.Fatalf("普通用户访问插件管理端路由应 404：%d", w.Code)
		}
		if w := do("GET", secure+"/plugin/rt/stat", "", adminTok); w.Code != 200 || !strings.Contains(w.Body.String(), "stat") {
			t.Fatalf("插件管理端路由错误：%d %s", w.Code, w.Body)
		}
		if w := do("GET", "wrongsecurepath0123/plugin/rt/stat", "", adminTok); w.Code != http.StatusNotFound {
			t.Fatalf("错误的安全路径应 404：%d", w.Code)
		}

		if w := do("POST", secure+"/plugins/rt/disable", "", adminTok); w.Code != 200 {
			t.Fatalf("停用失败：%d %s", w.Code, w.Body)
		}
		if w := do("GET", "plugin/rt/ping", "", ""); w.Code != http.StatusNotFound {
			t.Fatalf("停用后路由应 404：%d", w.Code)
		}
		if w := do("DELETE", secure+"/plugins/rt", "", adminTok); w.Code != http.StatusBadRequest {
			t.Fatalf("程序中的插件不能删除：%d", w.Code)
		}
	})
}

func TestKV(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		m := e.manager(t, &fake{code: "kvp"}, &fake{code: "kvq"})
		s, other := &kv{m: m, code: "kvp"}, &kv{m: m, code: "kvq"}
		if _, err := s.Get(ctx, "a"); !errors.Is(err, sdk.ErrKVNotFound) {
			t.Fatalf("不存在的键应返回 ErrKVNotFound：%v", err)
		}
		if err := s.Set(ctx, "a", "1", 0); err != nil {
			t.Fatal(err)
		}
		if err := s.Set(ctx, "a", "2", 0); err != nil {
			t.Fatal(err)
		}
		if v, err := s.Get(ctx, "a"); err != nil || v != "2" {
			t.Fatalf("覆盖写入失败：%q %v", v, err)
		}
		if _, err := other.Get(ctx, "a"); !errors.Is(err, sdk.ErrKVNotFound) {
			t.Fatal("不同插件的 KV 应隔离")
		}
		if ok, err := s.SetNX(ctx, "a", "3", 0); err != nil || ok {
			t.Fatalf("已存在的键 SetNX 应失败：%v %v", ok, err)
		}
		if ok, err := s.SetNX(ctx, "t", "x", time.Minute); err != nil || !ok {
			t.Fatalf("SetNX 失败：%v %v", ok, err)
		}
		if ok, _ := s.SetNX(ctx, "t", "y", time.Minute); ok {
			t.Fatal("未过期的键 SetNX 应失败")
		}
		if err := s.Set(ctx, "e", "x", time.Minute); err != nil {
			t.Fatal(err)
		}
		e.now = e.now.Add(2 * time.Minute)
		if _, err := s.Get(ctx, "t"); !errors.Is(err, sdk.ErrKVNotFound) {
			t.Fatal("过期的键应读取不到")
		}
		if ok, err := s.SetNX(ctx, "t", "z", 0); err != nil || !ok {
			t.Fatalf("过期的键 SetNX 应成功：%v %v", ok, err)
		}
		if v, _ := s.Get(ctx, "t"); v != "z" {
			t.Fatalf("SetNX 值错误：%q", v)
		}
		if n, err := m.CleanKV(ctx); err != nil || n != 1 {
			t.Fatalf("应清理 1 个过期键：%d %v", n, err)
		}
		if err := s.Delete(ctx, "a"); err != nil {
			t.Fatal(err)
		}
		if _, err := s.Get(ctx, "a"); !errors.Is(err, sdk.ErrKVNotFound) {
			t.Fatal("删除后应读取不到")
		}
		if err := s.Set(ctx, strings.Repeat("k", maxKeyLen+1), "v", 0); err == nil {
			t.Fatal("过长的键应报错")
		}
		if err := s.Set(ctx, "", "v", 0); err == nil {
			t.Fatal("空键应报错")
		}
		if err := s.Set(ctx, "big", strings.Repeat("v", maxValueLen+1), 0); err == nil {
			t.Fatal("过大的值应报错")
		}
	})
}

func TestJobsAndPayment(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		calls := 0
		m := e.manager(t,
			&fake{code: "jobp", setup: func(h sdk.Host) error {
				h.Job("tick", "*/5 * * * *", func(context.Context) error { calls++; return nil })
				h.Job("boom", "0 * * * *", func(context.Context) error { panic("x") })
				h.Payment("plugtest", fakeGateway{})
				return nil
			}},
			&fake{code: "dupay", setup: func(h sdk.Host) error {
				h.Payment("plugtest", fakeGateway{})
				return nil
			}},
		)
		if v := statusOf(t, m, "dupay"); v.Status != StatusError {
			t.Fatalf("重复的支付网关应加载失败：%+v", v)
		}
		jobs := map[string]func(context.Context) error{}
		for _, j := range m.Jobs() {
			jobs[j.Name] = j.Run
		}
		if len(jobs) != 4 || jobs["plugin_kv_clean"] == nil || jobs["plugin_js_jobs"] == nil || jobs["plugin.jobp.tick"] == nil {
			t.Fatalf("定时任务错误：%v", jobs)
		}
		if _, ok := payment.Lookup("plugtest"); ok {
			t.Fatal("插件未启用时支付网关不可用")
		}
		if err := jobs["plugin.jobp.tick"](ctx); err != nil || calls != 0 {
			t.Fatal("插件未启用时不应执行定时任务")
		}
		mustEnable(t, m, "jobp")
		if _, ok := payment.Lookup("plugtest"); !ok {
			t.Fatal("插件启用后支付网关应可用")
		}
		if err := jobs["plugin.jobp.tick"](ctx); err != nil || calls != 1 {
			t.Fatalf("定时任务未执行：%v %d", err, calls)
		}
		if err := jobs["plugin.jobp.boom"](ctx); err == nil {
			t.Fatal("定时任务 panic 应返回错误")
		}
		m.Close()
		if _, ok := payment.Lookup("plugtest"); ok {
			t.Fatal("Close 后支付网关应注销")
		}
	})
}
