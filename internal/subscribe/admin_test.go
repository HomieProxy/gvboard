package subscribe

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"

	"gvboard/internal/config"
	"gvboard/internal/model"
	"gvboard/internal/server"
	"gvboard/internal/testutil"
)

func TestGuardAdminHTTP(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) { testGuardAdminHTTP(t, newEnv(t, driver)) })
	}
}

func testGuardAdminHTTP(t *testing.T, e *env) {
	ctx := context.Background()
	const secure = "guardadmin0123456789"
	e.set(t, map[string]any{"secure_path": secure, "subscribe_template_surge": "#!MANAGED-CONFIG custom"})
	admin, sub := e.newSub(t, "admin@x.com")
	e.svc.DB.Model(admin).Update("is_admin", 1)
	normal, _ := e.newSub(t, "n@x.com")
	adminTok, _ := e.auth.Issue(ctx, admin.ID, "", "")
	userTok, _ := e.auth.Issue(ctx, normal.ID, "", "")

	cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
	r, err := server.NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	r.Mount(&Module{Svc: e.svc, Auth: e.auth})
	do := func(method, path, body, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/t1/"+secure+path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("Authorization", tok)
		w := httptest.NewRecorder()
		r.Engine.ServeHTTP(w, req)
		return w
	}
	data := func(w *httptest.ResponseRecorder, v any) {
		t.Helper()
		var body struct{ Data json.RawMessage }
		if w.Code != 200 {
			t.Fatalf("请求失败：%d %s", w.Code, w.Body)
		}
		if err := json.Unmarshal(w.Body.Bytes(), &body); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(body.Data, v); err != nil {
			t.Fatal(err)
		}
	}

	for _, p := range []string{"/subscribe-guard/rules", "/subscribe-guard/logs", "/subscribe-templates"} {
		if w := do("GET", p, "", userTok); w.Code != http.StatusNotFound {
			t.Errorf("普通用户访问 %s 应 404，实际 %d", p, w.Code)
		}
	}

	// 规则增删改查
	var rule model.SubscribeRule
	data(do("POST", "/subscribe-guard/rules",
		`{"name":"拦截爬虫","target":"ua","match_type":"contains","pattern":"curl\nwget","action":"deny","priority":5,"enabled":true}`, adminTok), &rule)
	if rule.ID == 0 || rule.Enabled != 1 || *rule.Pattern != "curl\nwget" {
		t.Fatalf("新建规则错误：%+v", rule)
	}
	if w := do("POST", "/subscribe-guard/rules", `{"name":"x","target":"ip","match_type":"contains","pattern":"1","action":"deny"}`, adminTok); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("IP 按包含匹配应 422，实际 %d", w.Code)
	}
	data(do("PUT", fmt.Sprintf("/subscribe-guard/rules/%d", rule.ID),
		`{"name":"改","target":"ip","match_type":"cidr","pattern":"10.0.0.0/8","action":"empty","priority":1,"enabled":false}`, adminTok), &rule)
	if rule.Name != "改" || rule.Target != "ip" || rule.Enabled != 0 {
		t.Fatalf("修改规则错误：%+v", rule)
	}
	if w := do("PUT", "/subscribe-guard/rules/99999", `{"name":"x","target":"ua","match_type":"empty","action":"log"}`, adminTok); w.Code != http.StatusNotFound {
		t.Errorf("修改不存在的规则应 404，实际 %d", w.Code)
	}
	if w := do("PUT", "/subscribe-guard/rules/abc", `{}`, adminTok); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("非法 ID 应 422，实际 %d", w.Code)
	}
	var rules []model.SubscribeRule
	data(do("GET", "/subscribe-guard/rules", "", adminTok), &rules)
	if len(rules) != 1 {
		t.Fatalf("规则列表数量 %d", len(rules))
	}

	// 测试规则
	var res struct{ Matched bool }
	data(do("POST", "/subscribe-guard/rules/test",
		`{"rule":{"target":"header","header":"x-token","match_type":"equals","pattern":"abc","action":"deny"},"headers":{"X-TOKEN":"ABC"}}`, adminTok), &res)
	if !res.Matched {
		t.Error("请求头规则应命中（名称与值不区分大小写）")
	}
	data(do("POST", "/subscribe-guard/rules/test",
		`{"rule":{"target":"client","match_type":"equals","pattern":"clash","action":"deny"},"flag":"clash"}`, adminTok), &res)
	if !res.Matched {
		t.Error("按 flag 识别的客户端格式应命中")
	}
	data(do("POST", "/subscribe-guard/rules/test",
		`{"rule":{"target":"ip","match_type":"cidr","pattern":"10.0.0.0/8","action":"deny"},"ip":"192.168.1.1"}`, adminTok), &res)
	if res.Matched {
		t.Error("IP 不在网段内不应命中")
	}
	var clients []string
	data(do("GET", "/subscribe-guard/clients", "", adminTok), &clients)
	if len(clients) < 5 || clients[len(clients)-1] != clientApp {
		t.Errorf("客户端列表错误：%v", clients)
	}

	if w := do("DELETE", fmt.Sprintf("/subscribe-guard/rules/%d", rule.ID), "", adminTok); w.Code != 200 {
		t.Errorf("删除规则失败：%d", w.Code)
	}
	if w := do("DELETE", fmt.Sprintf("/subscribe-guard/rules/%d", rule.ID), "", adminTok); w.Code != http.StatusNotFound {
		t.Errorf("重复删除应 404，实际 %d", w.Code)
	}

	// 日志、统计、清理
	now := testNow.Unix()
	for i, ip := range []string{"1.1.1.1", "2.2.2.2", "2.2.2.2"} {
		e.create(t, &model.SubscribeLog{SubscriptionID: sub.ID, UserID: admin.ID, IP: ip, UserAgent: "clash-verge/1.0", Client: "clash",
			Result: []int{0, 3, 0}[i], CreatedAt: now - 100})
	}
	e.create(t, &model.SubscribeLog{SubscriptionID: sub.ID, UserID: admin.ID, IP: "3.3.3.3", UserAgent: "old", CreatedAt: now - 40*86400})
	var page LogPage
	data(do("GET", "/subscribe-guard/logs?ip=2.2.2.2&result=3", "", adminTok), &page)
	if page.Total != 1 || len(page.Data) != 1 {
		t.Errorf("日志筛选错误：%+v", page)
	}
	data(do("GET", "/subscribe-guard/logs?user_agent=verge_", "", adminTok), &page)
	if page.Total != 0 {
		t.Error("UA 中的 _ 应按字面匹配")
	}
	if w := do("GET", "/subscribe-guard/logs?result=x", "", adminTok); w.Code != http.StatusUnprocessableEntity {
		t.Errorf("非法查询参数应 422，实际 %d", w.Code)
	}
	var stats []SubStat
	data(do("GET", "/subscribe-guard/stats?hours=24", "", adminTok), &stats)
	if len(stats) != 1 || stats[0].IPs != 2 || stats[0].Requests != 3 || stats[0].Blocked != 1 {
		t.Errorf("统计错误：%+v", stats)
	}
	for _, q := range []string{"hours=0", "limit=500", "order=x"} {
		if w := do("GET", "/subscribe-guard/stats?"+q, "", adminTok); w.Code != http.StatusUnprocessableEntity {
			t.Errorf("stats?%s 应 422，实际 %d", q, w.Code)
		}
	}
	var days []DayCount
	data(do("GET", "/subscribe-guard/invalid?days=7", "", adminTok), &days)
	if len(days) != 7 || days[6].Date != testNow.Format("20060102") {
		t.Errorf("无效请求统计错误：%+v", days)
	}
	var cleaned struct{ Deleted int64 }
	data(do("POST", "/subscribe-guard/logs/clean", "", adminTok), &cleaned)
	if cleaned.Deleted != 1 {
		t.Errorf("应清理 1 条过期日志，实际 %d", cleaned.Deleted)
	}

	// 订阅模板
	var tpls []TemplateInfo
	data(do("GET", "/subscribe-templates", "", adminTok), &tpls)
	if len(tpls) != 7 || tpls[0].Name != "clash" || tpls[0].Format != "yaml" || !strings.Contains(tpls[0].Default, "proxies") {
		t.Fatalf("模板列表错误：%d", len(tpls))
	}
	for _, tp := range tpls {
		if tp.Default == "" || tp.Custom != (tp.Name == "surge") || tp.SettingKey != "subscribe_template_"+tp.Name {
			t.Errorf("模板 %s 信息错误", tp.Name)
		}
	}
}
