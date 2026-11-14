package subscribe

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"gvboard/internal/cache"
	"gvboard/internal/config"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/server"
	"gvboard/internal/testutil"

	"go.uber.org/zap"
)

func acc(ip, ua string, headers map[string]string) Access {
	return Access{IP: ip, UserAgent: ua, Header: func(n string) string { return headers[n] }}
}

func mustRule(t *testing.T, in RuleInput) *compiledRule {
	t.Helper()
	c, err := compileRule(in.toModel())
	if err != nil {
		t.Fatalf("规则 %+v 编译失败: %v", in, err)
	}
	return c
}

func TestCompileRuleValidation(t *testing.T) {
	bad := []RuleInput{
		{Target: "body", MatchType: "contains", Pattern: "x", Action: "deny"},
		{Target: "ua", MatchType: "like", Pattern: "x", Action: "deny"},
		{Target: "ua", MatchType: "contains", Pattern: "x", Action: "drop"},
		{Target: "ua", MatchType: "contains", Pattern: "  \n ", Action: "deny"},
		{Target: "ua", MatchType: "regex", Pattern: "(", Action: "deny"},
		{Target: "ua", MatchType: "cidr", Pattern: "1.1.1.1", Action: "deny"},
		{Target: "ip", MatchType: "contains", Pattern: "1.1", Action: "deny"},
		{Target: "ip", MatchType: "cidr", Pattern: "1.1.1.1/33", Action: "deny"},
		{Target: "ip", MatchType: "cidr", Pattern: "abc", Action: "deny"},
		{Target: "header", Header: "X Bad", MatchType: "empty", Action: "deny"},
		{Target: "header", Header: "", MatchType: "empty", Action: "deny"},
		{Target: "ua", MatchType: "empty", Action: "deny", Priority: 2000000},
		{Name: string(make([]rune, 65)), Target: "ua", MatchType: "empty", Action: "deny"},
	}
	for _, in := range bad {
		_, err := compileRule(in.toModel())
		var he *httpx.Error
		if !errors.As(err, &he) || he.Status != http.StatusUnprocessableEntity {
			t.Errorf("规则 %+v 应校验失败，实际 %v", in, err)
		}
	}
	c := mustRule(t, RuleInput{Target: "ua", Header: "X-Ignored", MatchType: "contains", Pattern: " a \n\n b ", Action: "deny"})
	if c.Header != "" || *c.Pattern != "a\nb" {
		t.Errorf("规则应规范化匹配值与请求头: %q %q", c.Header, *c.Pattern)
	}
	if c := mustRule(t, RuleInput{Target: "ua", MatchType: "empty", Pattern: "x", Action: "deny"}); c.Pattern != nil {
		t.Errorf("为空匹配不应保存匹配值")
	}
}

func TestRuleMatch(t *testing.T) {
	h := map[string]string{"X-Requested-With": "com.example.app"}
	a := acc("10.1.2.3", "ClashMeta/1.18 Mihomo", h)
	a.Flag = "meta"
	cases := []struct {
		in   RuleInput
		a    Access
		want bool
	}{
		{RuleInput{Target: "ua", MatchType: "contains", Pattern: "x\nMIHOMO"}, a, true},
		{RuleInput{Target: "ua", MatchType: "contains", Pattern: "curl"}, a, false},
		{RuleInput{Target: "ua", MatchType: "equals", Pattern: "clashmeta/1.18 mihomo"}, a, true},
		{RuleInput{Target: "ua", MatchType: "prefix", Pattern: "Clash"}, a, true},
		{RuleInput{Target: "ua", MatchType: "prefix", Pattern: "Mihomo"}, a, false},
		{RuleInput{Target: "ua", MatchType: "regex", Pattern: `^clash\w*/1\.\d+`}, a, true},
		{RuleInput{Target: "ua", MatchType: "empty"}, a, false},
		{RuleInput{Target: "ua", MatchType: "empty"}, acc("1.1.1.1", "", nil), true},
		{RuleInput{Target: "ua", MatchType: "empty", Negate: true}, a, true},
		{RuleInput{Target: "header", Header: "X-Requested-With", MatchType: "equals", Pattern: "com.example.app"}, a, true},
		{RuleInput{Target: "header", Header: "Accept-Language", MatchType: "empty"}, a, true},
		{RuleInput{Target: "header", Header: "Accept-Language", MatchType: "empty"}, Access{IP: "1.1.1.1"}, true},
		{RuleInput{Target: "ip", MatchType: "cidr", Pattern: "10.0.0.0/8"}, a, true},
		{RuleInput{Target: "ip", MatchType: "cidr", Pattern: "10.1.2.3"}, a, true},
		{RuleInput{Target: "ip", MatchType: "cidr", Pattern: "10.0.0.0/8"}, acc("::ffff:10.9.9.9", "", nil), true},
		{RuleInput{Target: "ip", MatchType: "cidr", Pattern: "2001:db8::/32"}, acc("2001:db8::1", "", nil), true},
		{RuleInput{Target: "ip", MatchType: "cidr", Pattern: "10.0.0.0/8", Negate: true}, a, false},
		{RuleInput{Target: "ip", MatchType: "cidr", Pattern: "10.0.0.0/8"}, acc("bad", "", nil), false},
		{RuleInput{Target: "flag", MatchType: "equals", Pattern: "META"}, a, true},
		{RuleInput{Target: "flag", MatchType: "empty"}, acc("1.1.1.1", "x", nil), true},
		{RuleInput{Target: "client", MatchType: "equals", Pattern: "ClashMeta\nClash"}, a, true},
		{RuleInput{Target: "client", MatchType: "equals", Pattern: "Clash", Negate: true}, a, true},
	}
	for _, c := range cases {
		c.in.Action = "deny"
		if got := mustRule(t, c.in).match(c.a, "ClashMeta"); got != c.want {
			t.Errorf("规则 %+v 匹配结果 %v，期望 %v", c.in, got, c.want)
		}
	}
}

func TestEvaluateOrder(t *testing.T) {
	rule := func(id int64, action, pattern string) *compiledRule {
		c := mustRule(t, RuleInput{Target: "ua", MatchType: "contains", Pattern: pattern, Action: action})
		c.ID = id
		return c
	}
	a := acc("1.1.1.1", "curl/8.0", nil)
	v, hits := evaluate([]*compiledRule{rule(1, "log", "curl"), rule(2, "log", "nomatch"), rule(3, "empty", "curl"), rule(4, "deny", "curl")}, a, "General")
	if v.action != "empty" || v.result != model.SubscribeResultRuleEmpty || *v.ruleID != 3 || len(hits) != 2 {
		t.Errorf("只记录规则应继续匹配，拦截规则命中即停止: %+v %v", v, hits)
	}
	v, hits = evaluate([]*compiledRule{rule(1, "allow", "curl"), rule(2, "deny", "curl")}, a, "General")
	if !v.allow || v.action != "" || *v.ruleID != 1 || len(hits) != 1 {
		t.Errorf("放行规则应结束匹配: %+v", v)
	}
	v, hits = evaluate([]*compiledRule{rule(5, "log", "curl")}, a, "General")
	if v.action != "" || v.allow || *v.ruleID != 5 || len(hits) != 1 {
		t.Errorf("只命中记录规则时应正常下发并记录规则: %+v", v)
	}
}

func TestGuard(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			t.Run("规则管理", func(t *testing.T) { testRuleAdmin(t, newEnv(t, driver)) })
			t.Run("风控下发", func(t *testing.T) { testDeliverGuard(t, newEnv(t, driver)) })
			t.Run("频率与防分享", func(t *testing.T) { testLimits(t, newEnv(t, driver)) })
			t.Run("日志查询与清理", func(t *testing.T) { testLogQuery(t, newEnv(t, driver)) })
		})
	}
}

func testRuleAdmin(t *testing.T, e *env) {
	ctx := context.Background()
	s := e.svc
	if _, err := s.SaveRule(ctx, 0, RuleInput{Target: "ua", MatchType: "regex", Pattern: "(", Action: "deny"}); err == nil {
		t.Fatal("非法规则应保存失败")
	}
	r1, err := s.SaveRule(ctx, 0, RuleInput{Name: "拦截 curl", Target: "ua", MatchType: "contains", Pattern: "curl", Action: "deny", Priority: 10, Enabled: true})
	if err != nil || r1.ID == 0 || r1.CreatedAt == 0 {
		t.Fatalf("新建规则失败: %v", err)
	}
	r2, _ := s.SaveRule(ctx, 0, RuleInput{Target: "ip", MatchType: "cidr", Pattern: "10.0.0.0/8", Action: "allow", Priority: 1, Enabled: true})
	list, _ := s.Rules(ctx)
	if len(list) != 2 || list[0].ID != r2.ID {
		t.Fatalf("规则应按优先级排序")
	}
	if got := s.activeRules(ctx); len(got) != 2 {
		t.Fatalf("应加载 2 条启用规则，实际 %d", len(got))
	}

	other := &Service{DB: s.DB, Cache: s.Cache, Settings: s.Settings, Now: s.Now}
	if len(other.activeRules(ctx)) != 2 {
		t.Fatal("其他实例应加载相同规则")
	}
	db := s.DB.Model(&model.SubscribeRule{}).Where("id = ?", r1.ID)
	db.UpdateColumn("hit_count", 7)
	upd, err := s.SaveRule(ctx, r1.ID, RuleInput{Name: "已停用", Target: "ua", MatchType: "contains", Pattern: "curl", Action: "deny", Priority: 10})
	if err != nil || upd.Name != "已停用" || upd.Enabled != 0 || upd.HitCount != 7 {
		t.Fatalf("修改规则失败或重置了命中数: %v %+v", err, upd)
	}
	if got := s.activeRules(ctx); len(got) != 1 {
		t.Errorf("本实例修改后应立即生效，实际 %d 条", len(got))
	}
	if len(other.activeRules(ctx)) != 2 {
		t.Error("其他实例在检查间隔内应继续使用缓存")
	}
	other.rules.checked = time.Time{}
	if len(other.activeRules(ctx)) != 1 {
		t.Error("其他实例检查到版本变化后应重新加载")
	}

	if _, err := s.SaveRule(ctx, 999999, RuleInput{Target: "ua", MatchType: "empty", Action: "deny"}); !errors.Is(err, errRuleNotExist) {
		t.Errorf("修改不存在的规则应返回不存在: %v", err)
	}
	if err := s.DeleteRule(ctx, r2.ID); err != nil {
		t.Fatal(err)
	}
	if err := s.DeleteRule(ctx, r2.ID); !errors.Is(err, errRuleNotExist) {
		t.Errorf("重复删除应返回不存在: %v", err)
	}
	if ok, err := s.TestRule(RuleInput{Target: "client", MatchType: "equals", Pattern: "Singbox", Action: "deny"}, acc("1.1.1.1", "SFA/1.12 (sing-box 1.12.0)", nil), ""); err != nil || !ok {
		t.Errorf("规则测试应按 User-Agent 识别客户端: %v %v", ok, err)
	}
	if names := s.ClientNames(); names[len(names)-1] != clientApp || len(names) != 21 {
		t.Errorf("客户端名称列表不正确: %v", names)
	}
}

func lastLog(t *testing.T, e *env) model.SubscribeLog {
	t.Helper()
	e.svc.FlushLogs(context.Background())
	var l model.SubscribeLog
	if err := e.svc.DB.Order("id DESC").Take(&l).Error; err != nil {
		t.Fatalf("没有请求日志: %v", err)
	}
	return l
}

func testDeliverGuard(t *testing.T, e *env) {
	ctx := context.Background()
	s := e.svc
	u, sub := e.newSub(t, "g@x.com")
	e.create(t, &model.ServerShadowsocks{ServerBase: base("ss", []int64{1}, ptr[int64](0), "8388"), Cipher: "aes-128-gcm"})
	e.set(t, map[string]any{"subscribe_log_headers": []string{"X-Device", "Accept-Language"}})
	req := Request{Flag: "", Host: "sub.example.com", BaseURL: "https://sub.example.com"}

	resp, err := s.Deliver(ctx, sub, req, acc("1.1.1.1", "v2rayN/6.0", map[string]string{"X-Device": "pc"}), false)
	if err != nil || len(resp.Body) == 0 {
		t.Fatalf("无规则时应正常下发: %v", err)
	}
	l := lastLog(t, e)
	if l.Result != model.SubscribeResultOK || l.SubscriptionID != sub.ID || l.UserID != u.ID || l.IP != "1.1.1.1" ||
		l.UserAgent != "v2rayN/6.0" || l.Client != "General" || l.Headers == nil || *l.Headers != `{"X-Device":"pc"}` || l.RuleID != nil {
		t.Errorf("请求日志字段不正确: %+v", l)
	}

	logRule, _ := s.SaveRule(ctx, 0, RuleInput{Target: "header", Header: "X-Device", MatchType: "equals", Pattern: "bot", Action: "log", Enabled: true})
	emptyRule, _ := s.SaveRule(ctx, 0, RuleInput{Target: "ua", MatchType: "contains", Pattern: "python", Action: "empty", Priority: 5, Enabled: true})
	denyRule, _ := s.SaveRule(ctx, 0, RuleInput{Target: "ua", MatchType: "empty", Action: "deny", Priority: 6, Enabled: true})
	allowRule, _ := s.SaveRule(ctx, 0, RuleInput{Target: "ip", MatchType: "cidr", Pattern: "192.168.0.0/16", Action: "allow", Priority: -1, Enabled: true})

	bot := map[string]string{"X-Device": "bot"}
	resp, _ = s.Deliver(ctx, sub, req, acc("1.1.1.1", "python-requests/2.31", bot), false)
	if resp == nil || len(resp.Body) != 0 {
		t.Errorf("命中空内容规则应返回 200 空内容")
	}
	if l := lastLog(t, e); l.Result != model.SubscribeResultRuleEmpty || *l.RuleID != emptyRule.ID {
		t.Errorf("空内容规则日志不正确: %+v", l)
	}
	if resp, _ := s.Deliver(ctx, sub, req, acc("1.1.1.1", "", nil), false); resp != nil {
		t.Errorf("命中拒绝规则应返回 404")
	}
	if l := lastLog(t, e); l.Result != model.SubscribeResultRuleDeny || *l.RuleID != denyRule.ID {
		t.Errorf("拒绝规则日志不正确: %+v", l)
	}
	if resp, _ := s.Deliver(ctx, sub, req, acc("192.168.1.2", "", nil), false); resp == nil || len(resp.Body) == 0 {
		t.Errorf("放行规则应优先于拒绝规则")
	}
	if resp, _ := s.Deliver(ctx, sub, Request{Flag: "clash"}, acc("1.1.1.1", "clash", bot), false); resp == nil || len(resp.Body) == 0 {
		t.Errorf("只记录规则不应影响下发")
	}
	if l := lastLog(t, e); l.Result != model.SubscribeResultOK || *l.RuleID != logRule.ID || l.Client != "Clash" {
		t.Errorf("只记录规则日志不正确: %+v", l)
	}
	ruleOf := func(id int64) model.SubscribeRule {
		var r model.SubscribeRule
		s.DB.Take(&r, id)
		return r
	}
	if hit := ruleOf(logRule.ID); hit.HitCount != 2 || hit.LastHitAt == nil || *hit.LastHitAt != testNow.Unix() {
		t.Errorf("规则命中数应为 2，实际 %d", hit.HitCount)
	}
	if hit := ruleOf(allowRule.ID); hit.HitCount != 1 {
		t.Errorf("放行规则命中数应为 1，实际 %d", hit.HitCount)
	}

	resp, _ = s.Deliver(ctx, sub, Request{}, acc("1.1.1.1", "Tidalab", nil), true)
	if resp == nil || resp.ContentType != "text/yaml" {
		t.Fatalf("App 配置应正常下发")
	}
	if l := lastLog(t, e); l.Client != clientApp {
		t.Errorf("App 配置日志客户端应为 App: %s", l.Client)
	}

	s.DB.Model(sub).Update("banned", 1)
	var cur model.Subscription
	s.DB.Take(&cur, sub.ID)
	resp, _ = s.Deliver(ctx, &cur, req, acc("1.1.1.1", "clash", nil), false)
	if resp == nil || len(resp.Body) != 0 {
		t.Errorf("订阅不可用应返回空内容")
	}
	if l := lastLog(t, e); l.Result != model.SubscribeResultUnavailable {
		t.Errorf("不可用订阅日志结果应为 1: %d", l.Result)
	}

	e.set(t, map[string]any{"subscribe_log_enable": 0})
	var before int64
	s.DB.Model(&model.SubscribeLog{}).Count(&before)
	_, _ = s.Deliver(ctx, sub, req, acc("1.1.1.1", "clash", bot), false)
	s.FlushLogs(ctx)
	var after int64
	s.DB.Model(&model.SubscribeLog{}).Count(&after)
	if hit := ruleOf(logRule.ID); after != before || hit.HitCount != 3 {
		t.Errorf("关闭日志后不应写日志但仍统计命中: %d→%d, 命中 %d", before, after, hit.HitCount)
	}

	cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
	r, err := server.NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	r.Mount(&Module{Svc: s, Auth: e.auth})
	s.DB.Model(sub).Update("banned", 0)
	get := func(path, ua string) int {
		req := httptest.NewRequest("GET", path, nil)
		req.Header.Set("User-Agent", ua)
		w := httptest.NewRecorder()
		r.Engine.ServeHTTP(w, req)
		return w.Code
	}
	if code := get("/api/v1/client/subscribe?token="+sub.Token, ""); code != http.StatusNotFound {
		t.Errorf("HTTP 命中拒绝规则应返回 404，实际 %d", code)
	}
	if code := get("/api/v1/client/subscribe?token="+sub.Token, "clash"); code != 200 {
		t.Errorf("HTTP 正常请求应返回 200，实际 %d", code)
	}
	e.set(t, map[string]any{"subscribe_log_enable": 1})
	for range 3 {
		get("/api/v1/client/subscribe?token=bad", "clash")
	}
	days, err := s.InvalidCounts(ctx, 3)
	if err != nil || len(days) != 3 || days[2].Count != 3 || days[2].Date != testNow.Format("20060102") || days[0].Count != 0 {
		t.Errorf("无效 token 计数不正确: %v %v", days, err)
	}
}

func testLimits(t *testing.T, e *env) {
	ctx := context.Background()
	s := e.svc
	_, sub := e.newSub(t, "l@x.com")
	_, sub2 := e.newSub(t, "l2@x.com")
	e.create(t, &model.ServerShadowsocks{ServerBase: base("ss", []int64{1}, ptr[int64](0), "8388"), Cipher: "aes-128-gcm"})
	req := Request{Flag: "clash"}
	cur := testNow
	s.Now = func() time.Time { return cur }
	deliver := func(sub *model.Subscription, ip string) (*Response, model.SubscribeLog) {
		resp, err := s.Deliver(ctx, sub, req, acc(ip, "clash", nil), false)
		if err != nil {
			t.Fatal(err)
		}
		return resp, lastLog(t, e)
	}
	ok := func(r *Response) bool { return r != nil && len(r.Body) > 0 }

	e.set(t, map[string]any{"subscribe_rate_sub_limit": 2, "subscribe_rate_window": 600})
	deliver(sub, "1.1.1.1")
	deliver(sub, "1.1.1.2")
	if r, l := deliver(sub, "1.1.1.3"); r == nil || len(r.Body) != 0 || l.Result != model.SubscribeResultRateLimited {
		t.Errorf("超过单订阅次数应返回空内容: %+v", l)
	}
	if r, _ := deliver(sub2, "1.1.1.3"); !ok(r) {
		t.Errorf("其他订阅不受影响")
	}
	allow, _ := s.SaveRule(ctx, 0, RuleInput{Target: "ip", MatchType: "cidr", Pattern: "9.9.9.9", Action: "allow", Enabled: true})
	if r, _ := deliver(sub, "9.9.9.9"); !ok(r) {
		t.Errorf("放行规则应跳过频率限制")
	}
	_ = s.DeleteRule(ctx, allow.ID)
	cur = cur.Add(10 * time.Minute)
	if r, _ := deliver(sub, "1.1.1.1"); !ok(r) {
		t.Errorf("进入新窗口后应恢复")
	}

	e.set(t, map[string]any{"subscribe_rate_sub_limit": 0, "subscribe_rate_ip_limit": 1, "subscribe_rate_action": "deny"})
	cur = cur.Add(10 * time.Minute)
	deliver(sub, "2.2.2.2")
	if r, _ := deliver(sub2, "2.2.2.2"); r != nil {
		t.Errorf("单 IP 超限且动作为拒绝时应返回 404")
	}
	e.set(t, map[string]any{"subscribe_rate_ip_limit": 0})

	e.set(t, map[string]any{"subscribe_share_ip_limit": 2, "subscribe_share_window": 1, "subscribe_share_action": "deny"})
	deliver(sub, "3.3.3.1")
	deliver(sub, "3.3.3.2")
	if r, l := deliver(sub, "3.3.3.3"); r != nil || l.Result != model.SubscribeResultShareBlock {
		t.Errorf("第 3 个 IP 应被拦截: %+v", l)
	}
	if r, _ := deliver(sub, "3.3.3.1"); !ok(r) {
		t.Errorf("已记录的 IP 不应受影响")
	}
	if n, _ := s.ShareIPs(ctx, sub.ID); n != 2 {
		t.Errorf("拦截模式下不应记录新 IP，实际 %d 个", n)
	}
	e.set(t, map[string]any{"subscribe_share_action": "log"})
	if r, l := deliver(sub, "3.3.3.4"); !ok(r) || l.Result != model.SubscribeResultShareWarn {
		t.Errorf("仅记录模式应正常下发并标记: %+v", l)
	}
	cur = cur.Add(61 * time.Minute)
	e.set(t, map[string]any{"subscribe_share_action": "empty"})
	if r, l := deliver(sub, "3.3.3.5"); !ok(r) || l.Result != model.SubscribeResultOK {
		t.Errorf("窗口过后旧 IP 应过期: %+v", l)
	}
	s.DB.Model(sub).Update("expired_at", cur.Unix()-1)
	var expired model.Subscription
	s.DB.Take(&expired, sub.ID)
	for _, ip := range []string{"4.4.4.1", "4.4.4.2", "4.4.4.3"} {
		deliver(&expired, ip)
	}
	if n, _ := s.ShareIPs(ctx, sub.ID); n != 1 {
		t.Errorf("不可用订阅的请求不应计入防分享，实际 %d 个", n)
	}
	key := s.Cache.KeyOf(cache.KeySubscribeShare, sub.ID)
	if ttl := s.Cache.TTL(ctx, key).Val(); ttl <= 0 || ttl > time.Hour {
		t.Errorf("防分享记录应设置过期时间: %v", ttl)
	}
}

func testLogQuery(t *testing.T, e *env) {
	ctx := context.Background()
	s := e.svc
	now := testNow.Unix()
	rid := int64(9)
	rows := []model.SubscribeLog{
		{SubscriptionID: 1, UserID: 10, IP: "1.1.1.1", UserAgent: "clash 100%_x", Client: "Clash", Result: 0, CreatedAt: now - 100},
		{SubscriptionID: 1, UserID: 10, IP: "1.1.1.2", UserAgent: "clash 100x", Client: "Clash", Result: 3, RuleID: &rid, CreatedAt: now - 50},
		{SubscriptionID: 1, UserID: 10, IP: "1.1.1.2", UserAgent: "sing-box", Client: "Singbox", Result: 0, CreatedAt: now - 10},
		{SubscriptionID: 2, UserID: 20, IP: "2.2.2.2", UserAgent: "v2rayN", Client: "General", Result: 4, CreatedAt: now - 10},
		{SubscriptionID: 2, UserID: 20, IP: "2.2.2.2", UserAgent: "old", Client: "General", Result: 0, CreatedAt: now - 31*86400},
		{SubscriptionID: 3, UserID: 30, IP: "3.3.3.3", UserAgent: "old", Client: "General", Result: 0, CreatedAt: now - 40*86400},
	}
	if err := s.DB.Create(&rows).Error; err != nil {
		t.Fatal(err)
	}
	zero := 0
	three := 3
	cases := []struct {
		q    LogQuery
		want int64
	}{
		{LogQuery{}, 6},
		{LogQuery{SubscriptionID: 1}, 3},
		{LogQuery{UserID: 20}, 2},
		{LogQuery{IP: "1.1.1.2"}, 2},
		{LogQuery{Client: "Clash"}, 2},
		{LogQuery{UserAgent: "100%_"}, 1},
		{LogQuery{UserAgent: "100"}, 2},
		{LogQuery{Result: &zero}, 4},
		{LogQuery{Result: &three}, 1},
		{LogQuery{RuleID: 9}, 1},
		{LogQuery{Since: now - 60}, 3},
		{LogQuery{Since: now - 60, Until: now - 10}, 1},
	}
	for _, c := range cases {
		page, err := s.Logs(ctx, c.q)
		if err != nil || page.Total != c.want {
			t.Errorf("查询 %+v 应有 %d 条，实际 %v %v", c.q, c.want, page, err)
		}
	}
	page, _ := s.Logs(ctx, LogQuery{PageSize: 10})
	if len(page.Data) != 6 || page.Data[0].ID != rows[5].ID {
		t.Errorf("日志应按 ID 倒序")
	}

	stats, err := s.SubStats(ctx, now-86400, "", 10)
	if err != nil || len(stats) != 2 || stats[0].SubscriptionID != 1 || stats[0].IPs != 2 || stats[0].Requests != 3 ||
		stats[0].Blocked != 1 || stats[0].LastAt != now-10 || stats[0].UserID != 10 || stats[1].Blocked != 1 {
		b, _ := json.Marshal(stats)
		t.Errorf("订阅统计不正确: %s %v", b, err)
	}
	if stats, _ := s.SubStats(ctx, now-86400*60, "requests", 1); len(stats) != 1 || stats[0].SubscriptionID != 1 {
		t.Errorf("按请求数排序与数量限制不正确: %+v", stats)
	}

	n, err := s.CleanLogs(ctx)
	if err != nil || n != 2 {
		t.Errorf("应清理 30 天前的 2 条日志，实际 %d %v", n, err)
	}
	e.set(t, map[string]any{"subscribe_log_retention_days": 1})
	if n, _ := s.CleanLogs(ctx); n != 0 {
		t.Errorf("1 天内的日志不应清理，实际 %d", n)
	}
	var left int64
	s.DB.Model(&model.SubscribeLog{}).Count(&left)
	if left != 4 {
		t.Errorf("剩余日志应为 4 条，实际 %d", left)
	}
}
