package plugin

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"go.uber.org/zap"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/config"
	"gvboard/internal/model"
	"gvboard/internal/server"
	sdk "gvboard/pkg/plugin"
	"gvboard/plugins/luckywheel"
)

const wheelGB = int64(1073741824)

func TestLuckyWheel(t *testing.T) {
	cooldown := luckywheel.Cooldown
	luckywheel.Cooldown = 0
	t.Cleanup(func() { luckywheel.Cooldown = cooldown })
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		u, primary, err := account.Create(e.db, account.NewUser{Email: "wheel@x.com", Password: "password123"})
		if err != nil {
			t.Fatal(err)
		}
		other, otherSub, err := account.Create(e.db, account.NewUser{Email: "other@x.com", Password: "password123"})
		if err != nil {
			t.Fatal(err)
		}
		exp := time.Now().Unix() + 30*86400
		e.db.Model(primary).Updates(map[string]any{"transfer_enable": 10 * wheelGB, "expired_at": exp})
		e.db.Model(otherSub).Updates(map[string]any{"transfer_enable": 10 * wheelGB, "expired_at": exp})
		onetime := &model.Subscription{UserID: u.ID, Name: "一次性", UUID: "uuid-wheel-ot", Token: "token-wheel-ot", TransferEnable: 10 * wheelGB}
		expired := &model.Subscription{UserID: u.ID, Name: "过期", UUID: "uuid-wheel-ex", Token: "token-wheel-ex", TransferEnable: 10 * wheelGB, ExpiredAt: ptrInt(1)}
		for _, s := range []*model.Subscription{onetime, expired} {
			if err := e.db.Create(s).Error; err != nil {
				t.Fatal(err)
			}
		}
		tok, _ := e.auth.Issue(ctx, u.ID, "", "")
		cur := tok
		m := e.manager(t, &luckywheel.Plugin{})
		cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
		r, err := server.NewRouter(cfg, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		r.Mount(m)
		do := func(method, path string, body any, out any) int {
			t.Helper()
			var rd *bytes.Reader
			if body != nil {
				b, _ := json.Marshal(body)
				rd = bytes.NewReader(b)
			} else {
				rd = bytes.NewReader(nil)
			}
			req := httptest.NewRequest(method, "/api/t1/"+path, rd)
			req.Header.Set("Authorization", cur)
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			r.Engine.ServeHTTP(w, req)
			var resp struct {
				Data json.RawMessage `json:"data"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if out != nil && w.Code == http.StatusOK {
				if err := json.Unmarshal(resp.Data, out); err != nil {
					t.Fatalf("%s 响应解析失败：%s", path, w.Body.String())
				}
			}
			return w.Code
		}
		admin := func(method, path string, body any, out any) int {
			t.Helper()
			b, _ := json.Marshal(body)
			id := &auth.Identity{ID: u.ID, IsAdmin: true}
			req := httptest.NewRequest(method, path, bytes.NewReader(b)).WithContext(context.WithValue(ctx, identityKey{}, id))
			req.Header.Set("Content-Type", "application/json")
			w := httptest.NewRecorder()
			m.entry(luckywheel.Code).admin.ServeHTTP(w, req)
			var resp struct {
				Data json.RawMessage `json:"data"`
			}
			_ = json.Unmarshal(w.Body.Bytes(), &resp)
			if out != nil && w.Code == http.StatusOK {
				_ = json.Unmarshal(resp.Data, out)
			}
			return w.Code
		}
		sub := func(id int64) model.Subscription {
			var s model.Subscription
			e.db.Take(&s, id)
			return s
		}

		if v := statusOf(t, m, luckywheel.Code); v.Status != StatusOK || v.DBWarning {
			t.Fatalf("内置插件状态错误：%+v", v)
		}
		mustEnable(t, m, luckywheel.Code)

		// 卡片跳转转盘页
		var st sdk.WidgetState
		if code := do("GET", "plugin/lucky_wheel/widget", nil, &st); code != http.StatusOK || len(st.Actions) != 1 ||
			st.Actions[0].Link != "/plugin/lucky_wheel/wheel" || !strings.Contains(st.Text, "1 次") {
			t.Fatalf("卡片状态错误：%d %+v", code, st)
		}
		// 导航页面与未适配主题的页面内容
		var pages []PageView
		if code := do("GET", "user/plugin-pages", nil, &pages); code != http.StatusOK || len(pages) != 1 || pages[0].Plugin != luckywheel.Code ||
			pages[0].Key != "wheel" || pages[0].Group != sdk.NavMore || pages[0].Icon != sdk.IconFerrisWheel || pages[0].Titles["en-US"] != "Lucky Wheel" {
			t.Fatalf("页面列表错误：%d %+v", code, pages)
		}
		var content sdk.PageContent
		if code := do("GET", "plugin/lucky_wheel/page", nil, &content); code != http.StatusOK || len(content.Blocks) != 3 ||
			content.Blocks[1].Fields[0].Value != "1" || len(content.Blocks[2].Rows) != 4 {
			t.Fatalf("页面内容错误：%d %+v", code, content)
		}

		// 配置校验
		var def luckywheel.Settings
		if code := admin("GET", "/settings", nil, &def); code != http.StatusOK || len(def.Prizes) != 4 || def.FreeDaily != 1 {
			t.Fatalf("默认配置错误：%d %+v", code, def)
		}
		bad := []func(s *luckywheel.Settings){
			func(s *luckywheel.Settings) { s.Prizes = s.Prizes[:1] },
			func(s *luckywheel.Settings) { s.Prizes[0].Type = "coupon" },
			func(s *luckywheel.Settings) { s.Prizes[1].Value = 0 },
			func(s *luckywheel.Settings) { s.Prizes[0].Value = 5 },
			func(s *luckywheel.Settings) { s.Prizes[0].Name = strings.Repeat("长", 17) },
			func(s *luckywheel.Settings) {
				for i := range s.Prizes {
					s.Prizes[i].Weight = 0
				}
			},
			func(s *luckywheel.Settings) { s.FreeDaily = -1 },
			func(s *luckywheel.Settings) { s.BalanceEnable, s.BalanceCost = true, 0 },
			func(s *luckywheel.Settings) { s.RetentionDays = 0 },
		}
		for i, f := range bad {
			s := def
			s.Prizes = append([]luckywheel.Prize(nil), def.Prizes...)
			f(&s)
			if code := admin("POST", "/settings", s, nil); code != http.StatusUnprocessableEntity {
				t.Errorf("第 %d 个无效配置应 422：%d", i, code)
			}
		}

		// 只会抽中 1GB 流量；每日免费 1 次，余额 0.50 / 流量 100MB 付费，每日付费最多 2 次
		set := luckywheel.Settings{FreeDaily: 1, BalanceEnable: true, BalanceCost: 50, TrafficEnable: true, TrafficCost: 100,
			PaidDaily: 2, RetentionDays: 30, Prizes: []luckywheel.Prize{
				{Name: "1G", Type: luckywheel.PrizeTraffic, Value: 1024, Weight: 1},
				{Name: "谢谢", Type: luckywheel.PrizeNone, Weight: 0},
			}}
		if code := admin("POST", "/settings", set, nil); code != http.StatusOK {
			t.Fatalf("保存配置失败：%d", code)
		}
		var info luckywheel.Info
		if code := do("GET", "plugin/lucky_wheel/info", nil, &info); code != http.StatusOK || info.FreeLeft != 1 || info.PaidLeft != 2 ||
			len(info.Prizes) != 2 || info.BalanceCost != 50 || info.TrafficCost != 100 {
			t.Fatalf("转盘信息错误：%d %+v", code, info)
		}

		type body = map[string]any
		for name, id := range map[string]int64{"不存在": 99999, "他人": otherSub.ID, "已到期": expired.ID} {
			if code := do("POST", "plugin/lucky_wheel/draw", body{"subscription_id": id}, nil); code != http.StatusBadRequest {
				t.Errorf("%s订阅不能抽奖：%d", name, code)
			}
		}
		var res luckywheel.Result
		if code := do("POST", "plugin/lucky_wheel/draw", body{"subscription_id": primary.ID}, &res); code != http.StatusOK ||
			res.Index != 0 || res.CostType != luckywheel.CostFree || !strings.Contains(res.Message, "1 GB") {
			t.Fatalf("免费抽奖错误：%d %+v", code, res)
		}
		if s := sub(primary.ID); s.TransferEnable != 11*wheelGB || s.BonusTraffic != wheelGB {
			t.Fatalf("流量奖励应计入本周期奖励流量：%+v", s)
		}

		// 免费次数用完：需选择付费方式；余额不足
		if code := do("POST", "plugin/lucky_wheel/draw", body{"subscription_id": primary.ID}, nil); code != http.StatusBadRequest {
			t.Fatalf("免费次数用完后未选付费方式应 400：%d", code)
		}
		if code := do("POST", "plugin/lucky_wheel/draw", body{"subscription_id": primary.ID, "pay": "balance"}, nil); code != http.StatusBadRequest {
			t.Fatalf("余额不足应 400：%d", code)
		}
		e.db.Model(&model.User{}).Where("id = ?", u.ID).Update("balance", 80)
		if code := do("POST", "plugin/lucky_wheel/draw", body{"subscription_id": primary.ID, "pay": "balance"}, &res); code != http.StatusOK || res.CostType != luckywheel.CostBalance {
			t.Fatalf("余额抽奖失败：%d %+v", code, res)
		}
		var after model.User
		e.db.Take(&after, u.ID)
		var logs []model.BalanceLog
		e.db.Where("user_id = ?", u.ID).Find(&logs)
		if after.Balance != 30 || len(logs) != 1 || logs[0].Amount != -50 || logs[0].Plugin != luckywheel.Code {
			t.Fatalf("余额抽奖扣费错误：余额 %d %+v", after.Balance, logs)
		}
		if code := do("POST", "plugin/lucky_wheel/draw", body{"subscription_id": primary.ID, "pay": "traffic"}, &res); code != http.StatusOK || res.Cost != 100*1048576 {
			t.Fatalf("流量抽奖失败：%d %+v", code, res)
		}
		if s := sub(primary.ID); s.D != 100*1048576 || s.TransferEnable != 13*wheelGB || s.BonusTraffic != 3*wheelGB {
			t.Fatalf("流量抽奖应计入已用流量：%+v", s)
		}
		if code := do("POST", "plugin/lucky_wheel/draw", body{"subscription_id": primary.ID, "pay": "traffic"}, nil); code != http.StatusBadRequest {
			t.Fatalf("付费次数达到上限应 400：%d", code)
		}
		if code := do("GET", "plugin/lucky_wheel/info", nil, &info); code != http.StatusOK || info.FreeLeft != 0 || info.PaidLeft != 0 {
			t.Fatalf("剩余次数错误：%+v", info)
		}

		// 一次性订阅没有到期时间，只有到期时间奖品时不能抽
		set.PaidDaily = 0
		set.Prizes = []luckywheel.Prize{{Name: "1天", Type: luckywheel.PrizeDays, Value: 1, Weight: 1}, {Name: "谢谢", Type: luckywheel.PrizeNone, Weight: 0}}
		admin("POST", "/settings", set, nil)
		if code := do("POST", "plugin/lucky_wheel/draw", body{"subscription_id": onetime.ID, "pay": "traffic"}, nil); code != http.StatusBadRequest {
			t.Fatalf("一次性订阅不能抽到期时间奖品：%d", code)
		}
		if s := sub(onetime.ID); s.D != 0 {
			t.Fatalf("抽奖失败不应扣流量：%+v", s)
		}
		if code := do("POST", "plugin/lucky_wheel/draw", body{"subscription_id": primary.ID, "pay": "traffic"}, &res); code != http.StatusOK {
			t.Fatalf("到期时间抽奖失败：%d", code)
		}
		if s := sub(primary.ID); *s.ExpiredAt != exp+86400 {
			t.Fatalf("到期时间奖励错误：%d → %d", exp, *s.ExpiredAt)
		}

		// 抽奖冷却：成功后 3 秒内再抽返回 429
		luckywheel.Cooldown = 3 * time.Second
		if code := do("POST", "plugin/lucky_wheel/draw", body{"subscription_id": primary.ID, "pay": "traffic"}, nil); code != http.StatusOK {
			t.Fatalf("冷却前抽奖失败：%d", code)
		}
		if code := do("POST", "plugin/lucky_wheel/draw", body{"subscription_id": primary.ID, "pay": "traffic"}, nil); code != http.StatusTooManyRequests {
			t.Fatalf("冷却期内应 429：%d", code)
		}
		luckywheel.Cooldown = 0

		// 流量明细“其它扣除”：本月消耗流量抽奖的记录，可按订阅筛选
		var costs []struct {
			SubscriptionID int64 `json:"subscription_id"`
			Bytes          int64 `json:"bytes"`
		}
		if code := do("GET", "plugin/lucky_wheel/traffic-costs", nil, &costs); code != http.StatusOK || len(costs) != 3 ||
			costs[0].Bytes != 100*1048576 || costs[0].SubscriptionID != primary.ID {
			t.Fatalf("流量消耗记录错误：%d %+v", code, costs)
		}
		if code := do("GET", "plugin/lucky_wheel/traffic-costs?subscription_id="+strconv.FormatInt(onetime.ID, 10), nil, &costs); code != http.StatusOK || len(costs) != 0 {
			t.Fatalf("按订阅筛选流量消耗错误：%d %+v", code, costs)
		}

		// 记录：用户只看自己的，管理员可按邮箱筛选
		var mine struct {
			Data  []model.LuckyWheelLog `json:"data"`
			Total int64                 `json:"total"`
		}
		if code := do("GET", "plugin/lucky_wheel/records?page_size=2", nil, &mine); code != http.StatusOK || mine.Total != 5 || len(mine.Data) != 2 ||
			mine.Data[0].PrizeType != luckywheel.PrizeDays {
			t.Fatalf("我的记录错误：%d %+v", code, mine)
		}
		e.db.Create(&model.LuckyWheelLog{UserID: other.ID, SubscriptionID: otherSub.ID, PrizeName: "x", PrizeType: "none", CostType: "free"})
		var all struct {
			Data []struct {
				Email            string `json:"email"`
				SubscriptionName string `json:"subscription_name"`
			} `json:"data"`
			Total int64 `json:"total"`
		}
		if code := admin("GET", "/records", nil, &all); code != http.StatusOK || all.Total != 6 {
			t.Fatalf("全部记录错误：%d %+v", code, all)
		}
		e.db.Model(&model.Subscription{}).Where("id = ?", otherSub.ID).Update("name", "备用订阅")
		if code := admin("GET", "/records?email=other@x.com", nil, &all); code != http.StatusOK || all.Total != 1 || all.Data[0].Email != "other@x.com" ||
			all.Data[0].SubscriptionName != "备用订阅" {
			t.Fatalf("按邮箱筛选错误：%d %+v", code, all)
		}

		// 并发抽奖：每日 1 次免费、不允许付费时只能成功 1 次
		set.BalanceEnable, set.TrafficEnable = false, false
		set.Prizes = []luckywheel.Prize{{Name: "谢谢", Type: luckywheel.PrizeNone, Weight: 1}, {Name: "1G", Type: luckywheel.PrizeTraffic, Value: 1024, Weight: 0}}
		admin("POST", "/settings", set, nil)
		c, cs, err := account.Create(e.db, account.NewUser{Email: "race@x.com", Password: "password123"})
		if err != nil {
			t.Fatal(err)
		}
		e.db.Model(cs).Updates(map[string]any{"transfer_enable": wheelGB, "expired_at": exp})
		cur, _ = e.auth.Issue(ctx, c.ID, "", "")
		var wg sync.WaitGroup
		var ok, failed atomic.Int32
		for range 10 {
			wg.Go(func() {
				switch do("POST", "plugin/lucky_wheel/draw", body{"subscription_id": cs.ID}, nil) {
				case http.StatusOK:
					ok.Add(1)
				case http.StatusTooManyRequests, http.StatusBadRequest:
				default:
					failed.Add(1)
				}
			})
		}
		wg.Wait()
		if failed.Load() != 0 {
			t.Fatalf("并发抽奖不应返回 429 / 400 以外的错误：%d 个", failed.Load())
		}
		var n int64
		e.db.Model(&model.LuckyWheelLog{}).Where("user_id = ?", c.ID).Count(&n)
		if ok.Load() != 1 || n != 1 {
			t.Fatalf("并发抽奖应只成功 1 次：成功 %d，记录 %d", ok.Load(), n)
		}
	})
}

func ptrInt(v int64) *int64 { return &v }
