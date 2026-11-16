package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"gvboard/internal/account"
	"gvboard/internal/config"
	"gvboard/internal/model"
	"gvboard/internal/server"
	sdk "gvboard/pkg/plugin"
	"gvboard/plugins/checkin"
	"gvboard/plugins/firstorder"
)

func widgetPlugin(code string, ws ...sdk.Widget) *fake {
	return &fake{code: code, setup: func(h sdk.Host) error {
		for _, w := range ws {
			h.Widget(w)
		}
		return nil
	}}
}

func TestWidgetRegister(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ok := sdk.Widget{Key: "a", Slot: sdk.SlotDashboard, Title: "签到", State: "/widget"}
		with := func(f func(w *sdk.Widget)) sdk.Widget {
			w := ok
			f(&w)
			return w
		}
		six := make([]sdk.Widget, 6)
		for i := range six {
			six[i] = with(func(w *sdk.Widget) { w.Key = "k" + string(rune('a'+i)) })
		}
		bad := map[string][]sdk.Widget{
			"wbadkey":   {with(func(w *sdk.Widget) { w.Key = "A-b" })},
			"wdupkey":   {ok, ok},
			"wtoomany":  six,
			"wbadslot":  {with(func(w *sdk.Widget) { w.Slot = "sidebar" })},
			"wnotitle":  {with(func(w *sdk.Widget) { w.Title = "" })},
			"wlongtitl": {with(func(w *sdk.Widget) { w.Title = strings.Repeat("长", 33) })},
			"wbadicon":  {with(func(w *sdk.Widget) { w.Icon = "<svg>" })},
			"wdotstate": {with(func(w *sdk.Widget) { w.State = "/../user/info" })},
			"wparam":    {with(func(w *sdk.Widget) { w.State = "/w/:id" })},
			"wnostate":  {with(func(w *sdk.Widget) { w.State = "" })},
		}
		plugins := []sdk.Plugin{widgetPlugin("wok", ok, with(func(w *sdk.Widget) { w.Key = "b"; w.Icon = sdk.IconGift }))}
		for code, ws := range bad {
			plugins = append(plugins, widgetPlugin(code, ws...))
		}
		m := e.manager(t, plugins...)

		if v := statusOf(t, m, "wok"); v.Status != StatusOK || len(v.Features.Widgets) != 2 || v.Features.Widgets[0] != "签到 (a)" {
			t.Fatalf("卡片注册错误：%+v", v)
		}
		for code := range bad {
			if v := statusOf(t, m, code); v.Status != StatusError || v.Error == "" || len(v.Features.Widgets) != 0 {
				t.Errorf("%s 应加载失败：%+v", code, v)
			}
		}
		if ws := m.Widgets(sdk.SlotDashboard); len(ws) != 0 {
			t.Fatalf("未启用插件的卡片不应显示：%+v", ws)
		}
		mustEnable(t, m, "wok")
		ws := m.Widgets(sdk.SlotDashboard)
		if len(ws) != 2 || ws[0].Plugin != "wok" || ws[0].Icon != sdk.IconPuzzle || ws[1].Icon != sdk.IconGift {
			t.Fatalf("卡片列表错误：%+v", ws)
		}
	})
}

// widgetEnv 挂载插件路由，返回一个已登录用户与请求函数（返回状态码与 data）。
func widgetEnv(t *testing.T, e *env, plugins ...sdk.Plugin) (*Manager, *model.User, string, func(method, path, tok string) (int, json.RawMessage)) {
	t.Helper()
	u, _, err := account.Create(e.db, account.NewUser{Email: "w@x.com", Password: "password123"})
	if err != nil {
		t.Fatal(err)
	}
	tok, _ := e.auth.Issue(context.Background(), u.ID, "", "")
	m := e.manager(t, plugins...)
	cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
	r, err := server.NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	r.Mount(m)
	return m, u, tok, func(method, path, tok string) (int, json.RawMessage) {
		req := httptest.NewRequest(method, "/api/t1/"+path, nil)
		if tok != "" {
			req.Header.Set("Authorization", tok)
		}
		w := httptest.NewRecorder()
		r.Engine.ServeHTTP(w, req)
		var body struct {
			Data json.RawMessage `json:"data"`
		}
		_ = json.Unmarshal(w.Body.Bytes(), &body)
		return w.Code, body.Data
	}
}

func TestWidgetFirstOrder(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		m, u, tok, do := widgetEnv(t, e, &firstorder.Plugin{})
		mustEnable(t, m, "first_order")
		var list []WidgetView
		if code, data := do("GET", "user/plugin-widgets", tok); code != http.StatusOK || json.Unmarshal(data, &list) != nil ||
			len(list) != 1 || list[0].Plugin != "first_order" || list[0].Icon != sdk.IconSparkles {
			t.Fatalf("卡片列表错误：%d %s", code, data)
		}
		var st sdk.WidgetState
		if code, data := do("GET", "plugin/first_order/widget", tok); code != http.StatusOK || json.Unmarshal(data, &st) != nil ||
			st.Hidden || st.Value != "-20%" || len(st.Actions) != 1 || st.Actions[0].Link != "/plans" {
			t.Fatalf("未购买时应显示首单卡片：%d %s", code, data)
		}
		// 充值与待支付订单不算购买过
		for i, o := range []model.Order{{Type: 9, Status: 3}, {Type: 1, Status: 0}} {
			o.UserID, o.TradeNo, o.Period = u.ID, "fo"+string(rune('a'+i)), "month_price"
			if err := e.db.Create(&o).Error; err != nil {
				t.Fatal(err)
			}
		}
		if _, data := do("GET", "plugin/first_order/widget", tok); json.Unmarshal(data, &st) != nil || st.Hidden {
			t.Fatalf("充值与待支付订单不应隐藏首单卡片：%s", data)
		}
		if err := e.db.Create(&model.Order{UserID: u.ID, PlanID: 1, Type: 1, Status: 3, TradeNo: "fo-done", Period: "month_price"}).Error; err != nil {
			t.Fatal(err)
		}
		st = sdk.WidgetState{}
		if code, data := do("GET", "plugin/first_order/widget", tok); code != http.StatusOK || json.Unmarshal(data, &st) != nil || !st.Hidden {
			t.Fatalf("购买过套餐后应隐藏首单卡片：%d %s", code, data)
		}
	})
}

func TestWidgetCheckin(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		m, u, tok, do := widgetEnv(t, e, &checkin.Plugin{})

		if code, _ := do("GET", "user/plugin-widgets", ""); code == http.StatusOK {
			t.Fatal("未登录不应能获取卡片")
		}
		if code, data := do("GET", "user/plugin-widgets", tok); code != http.StatusOK || string(data) != "[]" {
			t.Fatalf("未启用时卡片应为空：%d %s", code, data)
		}
		if code, _ := do("GET", "user/plugin-widgets?slot=sidebar", tok); code != http.StatusUnprocessableEntity {
			t.Fatalf("无效位置应 422：%d", code)
		}
		mustEnable(t, m, "checkin")
		var list []WidgetView
		if code, data := do("GET", "user/plugin-widgets?slot=dashboard", tok); code != http.StatusOK || json.Unmarshal(data, &list) != nil ||
			len(list) != 1 || list[0].Plugin != "checkin" || list[0].State != "/widget" || list[0].Icon != sdk.IconCalendarCheck {
			t.Fatalf("卡片列表错误：%d %s", code, data)
		}

		var st sdk.WidgetState
		if code, data := do("GET", "plugin/checkin/widget", tok); code != http.StatusOK || json.Unmarshal(data, &st) != nil ||
			st.Hidden || len(st.Actions) != 1 || st.Actions[0].Path != "/checkin" || !strings.Contains(st.Text, "0.01 ~ 0.10") {
			t.Fatalf("签到前卡片状态错误：%d %s", code, data)
		}
		var res struct {
			Reward  int64  `json:"reward"`
			Message string `json:"message"`
		}
		if code, data := do("POST", "plugin/checkin/checkin", tok); code != http.StatusOK || json.Unmarshal(data, &res) != nil ||
			res.Reward < 1 || res.Reward > 10 || !strings.HasPrefix(res.Message, "签到成功") {
			t.Fatalf("签到失败：%d %s", code, data)
		}
		st = sdk.WidgetState{}
		if code, data := do("GET", "plugin/checkin/widget", tok); code != http.StatusOK || json.Unmarshal(data, &st) != nil ||
			len(st.Actions) != 0 || st.Value == "" || st.Text == "" {
			t.Fatalf("签到后卡片状态错误：%d %s", code, data)
		}
		if code, _ := do("POST", "plugin/checkin/checkin", tok); code != http.StatusBadRequest {
			t.Fatalf("重复签到应 400：%d", code)
		}
		var after model.User
		e.db.Take(&after, u.ID)
		if after.Balance != u.Balance+res.Reward {
			t.Fatalf("余额错误：%d → %d，奖励 %d", u.Balance, after.Balance, res.Reward)
		}

		if err := m.Disable(ctx, "checkin"); err != nil {
			t.Fatal(err)
		}
		if code, data := do("GET", "user/plugin-widgets", tok); code != http.StatusOK || string(data) != "[]" {
			t.Fatalf("停用后卡片应为空：%d %s", code, data)
		}
	})
}
