package plugin

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

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
		plugins := []sdk.Plugin{
			widgetPlugin("wok", ok, with(func(w *sdk.Widget) { w.Key = "b"; w.Icon = sdk.IconGift })),
			widgetPlugin("wnoslot", with(func(w *sdk.Widget) { w.Slot = "" })),
		}
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
		// 建议位置为空的卡片只在不指定位置时返回
		mustEnable(t, m, "wnoslot")
		if ws := m.Widgets(sdk.SlotDashboard); len(ws) != 2 {
			t.Fatalf("指定位置时不应返回未建议位置的卡片：%+v", ws)
		}
		if ws := m.Widgets(""); len(ws) != 3 || ws[0].Plugin != "wnoslot" {
			t.Fatalf("不指定位置应返回全部卡片：%+v", ws)
		}
	})
}

func bannerPlugin(code string, bs ...sdk.Banner) *fake {
	return &fake{code: code, setup: func(h sdk.Host) error {
		for _, b := range bs {
			h.Banner(b)
		}
		return nil
	}}
}

func TestBannerRegister(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ok := sdk.Banner{Key: "a", State: "/banner"}
		with := func(f func(b *sdk.Banner)) sdk.Banner {
			b := ok
			f(&b)
			return b
		}
		four := make([]sdk.Banner, 4)
		for i := range four {
			four[i] = with(func(b *sdk.Banner) { b.Key = "k" + string(rune('a'+i)) })
		}
		bad := map[string][]sdk.Banner{
			"bbadkey":   {with(func(b *sdk.Banner) { b.Key = "A-b" })},
			"bdupkey":   {ok, ok},
			"btoomany":  four,
			"blongtitl": {with(func(b *sdk.Banner) { b.Title = strings.Repeat("长", 33) })},
			"bbadicon":  {with(func(b *sdk.Banner) { b.Icon = "<svg>" })},
			"bdotstate": {with(func(b *sdk.Banner) { b.State = "/../user/info" })},
			"bnostate":  {with(func(b *sdk.Banner) { b.State = "" })},
		}
		plugins := []sdk.Plugin{bannerPlugin("bok", ok, with(func(b *sdk.Banner) { b.Key, b.Title, b.Icon = "b", "限时活动", sdk.IconGift }))}
		for code, bs := range bad {
			plugins = append(plugins, bannerPlugin(code, bs...))
		}
		m := e.manager(t, plugins...)

		if v := statusOf(t, m, "bok"); v.Status != StatusOK || len(v.Features.Banners) != 2 || v.Features.Banners[0] != "a (a)" || v.Features.Banners[1] != "限时活动 (b)" {
			t.Fatalf("横幅注册错误：%+v", v)
		}
		for code := range bad {
			if v := statusOf(t, m, code); v.Status != StatusError || v.Error == "" || len(v.Features.Banners) != 0 {
				t.Errorf("%s 应加载失败：%+v", code, v)
			}
		}
		if bs := m.Banners(); len(bs) != 0 {
			t.Fatalf("未启用插件的横幅不应显示：%+v", bs)
		}
		mustEnable(t, m, "bok")
		bs := m.Banners()
		if len(bs) != 2 || bs[0].Plugin != "bok" || bs[0].Icon != sdk.IconMegaphone || bs[1].Icon != sdk.IconGift {
			t.Fatalf("横幅列表错误：%+v", bs)
		}
	})
}

func TestDisplaySwitch(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		all := func(code string) *fake {
			return &fake{code: code, setup: func(h sdk.Host) error {
				h.Widget(sdk.Widget{Key: "w", Title: "卡片", State: "/w"})
				h.Banner(sdk.Banner{Key: "b", State: "/b"})
				h.Page(sdk.Page{Key: "p", Title: "页面", Content: "/p"})
				return nil
			}}
		}
		m := e.manager(t, all("da"), all("db"), widgetPlugin("dw", sdk.Widget{Key: "w", Title: "卡片", State: "/w"}))
		for _, code := range []string{"da", "db", "dw"} {
			mustEnable(t, m, code)
		}
		count := func() (int, int, int) { return len(m.Widgets("")), len(m.Banners()), len(m.Pages()) }
		if v := statusOf(t, m, "da"); len(v.Display) != 3 || !v.Display["widget"] || !v.Display["banner"] || !v.Display["page"] {
			t.Fatalf("默认应开启全部已注册形式：%+v", v.Display)
		}
		if v := statusOf(t, m, "dw"); len(v.Display) != 1 || !v.Display["widget"] {
			t.Fatalf("只应包含已注册的形式：%+v", v.Display)
		}
		if w, b, p := count(); w != 3 || b != 2 || p != 2 {
			t.Fatalf("开启时数量错误：%d %d %d", w, b, p)
		}

		// 关闭 da 的卡片：只影响 da 的卡片；未注册的形式忽略
		if err := m.SetDisplay(ctx, "da", map[string]bool{"widget": false, "x": false}); err != nil {
			t.Fatal(err)
		}
		if err := m.SetDisplay(ctx, "dw", map[string]bool{"banner": false}); err != nil {
			t.Fatal(err)
		}
		if w, b, p := count(); w != 2 || b != 2 || p != 2 {
			t.Fatalf("关闭卡片后数量错误：%d %d %d", w, b, p)
		}
		for _, x := range m.Widgets("") {
			if x.Plugin == "da" {
				t.Fatal("已关闭的卡片不应返回")
			}
		}
		// 未提交的形式保持原值
		if err := m.SetDisplay(ctx, "da", map[string]bool{"page": false}); err != nil {
			t.Fatal(err)
		}
		if v := statusOf(t, m, "da"); v.Display["widget"] || !v.Display["banner"] || v.Display["page"] {
			t.Fatalf("开关保存错误：%+v", v.Display)
		}
		if w, b, p := count(); w != 2 || b != 2 || p != 1 {
			t.Fatalf("关闭页面后数量错误：%d %d %d", w, b, p)
		}
		if err := m.SetDisplay(ctx, "da", map[string]bool{"widget": true, "page": true}); err != nil {
			t.Fatal(err)
		}
		if w, b, p := count(); w != 3 || b != 2 || p != 2 {
			t.Fatalf("重新开启后数量错误：%d %d %d", w, b, p)
		}
		if got := m.Settings.Strings(displayKey); len(got) != 0 {
			t.Fatalf("全部开启后不应保存关闭记录：%v", got)
		}
		if err := m.SetDisplay(ctx, "nope", map[string]bool{"widget": false}); err == nil {
			t.Fatal("不存在的插件应报错")
		}
	})
}

func pagePlugin(code string, ps ...sdk.Page) *fake {
	return &fake{code: code, setup: func(h sdk.Host) error {
		for _, p := range ps {
			h.Page(p)
		}
		return nil
	}}
}

func TestPageRegister(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ok := sdk.Page{Key: "a", Title: "共享账号", Content: "/page"}
		with := func(f func(p *sdk.Page)) sdk.Page {
			p := ok
			f(&p)
			return p
		}
		four := make([]sdk.Page, 4)
		for i := range four {
			four[i] = with(func(p *sdk.Page) { p.Key = "k" + string(rune('a'+i)) })
		}
		bad := map[string][]sdk.Page{
			"pbadkey":   {with(func(p *sdk.Page) { p.Key = "A-b" })},
			"pdupkey":   {ok, ok},
			"ptoomany":  four,
			"pnotitle":  {with(func(p *sdk.Page) { p.Title = "" })},
			"plongtitl": {with(func(p *sdk.Page) { p.Title = strings.Repeat("长", 25) })},
			"pbadlang":  {with(func(p *sdk.Page) { p.Titles = map[string]string{"<x>": "A"} })},
			"pemptylng": {with(func(p *sdk.Page) { p.Titles = map[string]string{"en-US": ""} })},
			"pbadicon":  {with(func(p *sdk.Page) { p.Icon = "<svg>" })},
			"pbadgroup": {with(func(p *sdk.Page) { p.Group = "admin" })},
			"pdotpath":  {with(func(p *sdk.Page) { p.Content = "/../user/info" })},
			"pnopath":   {with(func(p *sdk.Page) { p.Content = "" })},
		}
		second := with(func(p *sdk.Page) {
			p.Key, p.Icon, p.Group, p.Titles = "b", sdk.IconKey, sdk.NavUser, map[string]string{"en-US": "Shared ID"}
		})
		plugins := []sdk.Plugin{pagePlugin("pok", ok, second)}
		for code, ps := range bad {
			plugins = append(plugins, pagePlugin(code, ps...))
		}
		m := e.manager(t, plugins...)

		if v := statusOf(t, m, "pok"); v.Status != StatusOK || len(v.Features.Pages) != 2 || v.Features.Pages[0] != "共享账号 (/plugin/pok/a)" {
			t.Fatalf("页面注册错误：%+v", v)
		}
		for code := range bad {
			if v := statusOf(t, m, code); v.Status != StatusError || v.Error == "" || len(v.Features.Pages) != 0 {
				t.Errorf("%s 应加载失败：%+v", code, v)
			}
		}
		if ps := m.Pages(); len(ps) != 0 {
			t.Fatalf("未启用插件的页面不应显示：%+v", ps)
		}
		mustEnable(t, m, "pok")
		ps := m.Pages()
		if len(ps) != 2 || ps[0].Plugin != "pok" || ps[0].Icon != sdk.IconPuzzle || ps[0].Group != sdk.NavMore ||
			ps[1].Icon != sdk.IconKey || ps[1].Group != sdk.NavUser || ps[1].Titles["en-US"] != "Shared ID" {
			t.Fatalf("页面列表错误：%+v", ps)
		}
		if err := m.Disable(context.Background(), "pok"); err != nil {
			t.Fatal(err)
		}
		if ps := m.Pages(); len(ps) != 0 {
			t.Fatalf("停用后页面不应显示：%+v", ps)
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

		// 限时：未设置时显示不带倒计时的横幅；设置后在时限内显示倒计时横幅，超过时限后卡片与横幅都隐藏
		var banners []BannerView
		if code, data := do("GET", "user/plugin-banners", tok); code != http.StatusOK || json.Unmarshal(data, &banners) != nil ||
			len(banners) != 1 || banners[0].Plugin != "first_order" || banners[0].State != "/banner" {
			t.Fatalf("横幅列表错误：%d %s", code, data)
		}
		var bs sdk.BannerState
		if _, data := do("GET", "plugin/first_order/banner", tok); json.Unmarshal(data, &bs) != nil || bs.Hidden || bs.EndsAt != 0 || bs.Text == "" {
			t.Fatalf("未设置限时应显示不带倒计时的横幅：%s", data)
		}
		if err := m.SaveConfig(context.Background(), "first_order", map[string]any{"limit_minutes": 30}); err != nil {
			t.Fatal(err)
		}
		now := time.Now().Unix()
		if err := e.db.Model(&model.User{}).Where("id = ?", u.ID).Update("created_at", now-60).Error; err != nil {
			t.Fatal(err)
		}
		bs = sdk.BannerState{}
		if _, data := do("GET", "plugin/first_order/banner", tok); json.Unmarshal(data, &bs) != nil || bs.Hidden ||
			bs.EndsAt != now-60+30*60 || bs.Dismissible || bs.Action == nil {
			t.Fatalf("时限内应显示倒计时横幅：%s", data)
		}
		var el struct {
			Eligible bool  `json:"eligible"`
			EndsAt   int64 `json:"ends_at"`
		}
		if _, data := do("GET", "plugin/first_order/eligible", tok); json.Unmarshal(data, &el) != nil || !el.Eligible || el.EndsAt != bs.EndsAt {
			t.Fatalf("时限内应可享受：%s", data)
		}
		if err := e.db.Model(&model.User{}).Where("id = ?", u.ID).Update("created_at", now-31*60).Error; err != nil {
			t.Fatal(err)
		}
		bs, st = sdk.BannerState{}, sdk.WidgetState{}
		if _, data := do("GET", "plugin/first_order/banner", tok); json.Unmarshal(data, &bs) != nil || !bs.Hidden {
			t.Fatalf("超过时限应隐藏横幅：%s", data)
		}
		if _, data := do("GET", "plugin/first_order/widget", tok); json.Unmarshal(data, &st) != nil || !st.Hidden {
			t.Fatalf("超过时限应隐藏卡片：%s", data)
		}
		if _, data := do("GET", "plugin/first_order/eligible", tok); json.Unmarshal(data, &el) != nil || el.Eligible {
			t.Fatalf("超过时限不应享受：%s", data)
		}
		if err := m.SaveConfig(context.Background(), "first_order", map[string]any{"limit_minutes": 0}); err != nil {
			t.Fatal(err)
		}
		st = sdk.WidgetState{}
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
		var logs []model.BalanceLog
		e.db.Where("user_id = ?", u.ID).Find(&logs)
		if len(logs) != 1 || logs[0].Plugin != "checkin" || logs[0].Amount != res.Reward || logs[0].Balance != after.Balance || logs[0].Remark != "每日签到" {
			t.Fatalf("签到应记录一条余额变动日志：%+v", logs)
		}

		if err := m.Disable(ctx, "checkin"); err != nil {
			t.Fatal(err)
		}
		if code, data := do("GET", "user/plugin-widgets", tok); code != http.StatusOK || string(data) != "[]" {
			t.Fatalf("停用后卡片应为空：%d %s", code, data)
		}
	})
}
