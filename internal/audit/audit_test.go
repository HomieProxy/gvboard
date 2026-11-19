package audit

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/setting"
	"gvboard/internal/testutil"
)

// ---------- 不依赖数据库的测试 ----------

func TestParsePattern(t *testing.T) {
	_, lines, err := parsePattern(strings.Join([]string{
		"# 注释", "", "  *.Example.COM  ", "full:A.example.com:443", "keyword:Torrent", "regexp:^DL\\d+\\.",
		"1.2.3.4", "10.0.0.0/8", "10.1.2.3/8:25", "[2001:DB8::1]:443", "2001:db8::/32", "::ffff:1.2.3.4",
		":25", ":6881-6889", "example.org:1000-2000",
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	want := []string{
		"example.com", "full:a.example.com:443", "keyword:torrent", "regexp:^DL\\d+\\.",
		"1.2.3.4", "10.0.0.0/8", "10.0.0.0/8:25", "[2001:db8::1]:443", "2001:db8::/32", "1.2.3.4",
		":25", ":6881-6889", "example.org:1000-2000",
	}
	if !slices.Equal(lines, want) {
		t.Fatalf("规范化结果不符:\n%q\n%q", lines, want)
	}

	for _, bad := range []string{
		"", "# 只有注释", "example.com:", "example.com:0", "example.com:70000", "example.com:20-10", ":abc",
		"exa mple.com", "full:", "full::25", "keyword:", "regexp:(", "[::1", "[::1]x", "a/b", "http://example.com",
		"10.0.0.0/33",
	} {
		if _, _, err := parsePattern(bad); err == nil {
			t.Errorf("%q 应校验失败", bad)
		}
	}
	if _, _, err := parsePattern(strings.Repeat("a.com\n", maxLines+1)); err == nil {
		t.Error("超过行数上限应失败")
	}
}

func TestMatch(t *testing.T) {
	entries, _, err := parsePattern(strings.Join([]string{
		"example.com", "full:only.test", "keyword:torrent", "regexp:^dl\\d+\\.cdn\\.",
		"1.2.3.0/24:25", "[2001:db8::1]:443", ":6881-6889", "smtp.test:465",
	}, "\n"))
	if err != nil {
		t.Fatal(err)
	}
	hit := func(target string) bool {
		tg, err := ParseTarget(target)
		if err != nil {
			t.Fatalf("%s: %v", target, err)
		}
		for _, e := range entries {
			if e.match(tg) {
				return true
			}
		}
		return false
	}
	for target, want := range map[string]bool{
		"example.com":         true,
		"www.example.com:443": true,
		"badexample.com":      false,
		"only.test":           true,
		"a.only.test":         false,
		"my-torrent.site:80":  true,
		"dl12.cdn.net":        true,
		"xdl12.cdn.net":       false,
		"1.2.3.9:25":          true,
		"1.2.3.9:26":          false,
		"[2001:db8::1]:443":   true,
		"[2001:db8::2]:443":   false,
		"9.9.9.9:6885":        true,
		"9.9.9.9:6890":        false,
		"smtp.test:465":       true,
		"smtp.test:25":        false,
		"1.2.3.4":             false,
	} {
		if got := hit(target); got != want {
			t.Errorf("%s 期望 %v，实际 %v", target, want, got)
		}
	}
	if _, err := ParseTarget("a.com:x"); err == nil {
		t.Error("非法端口应失败")
	}
}

// ---------- 数据库测试 ----------

type env struct {
	*Service
	now     time.Time
	removed []int64
	bans    []BanInfo
}

func newEnv(t *testing.T, driver string) *env {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	e := &env{now: time.Unix(1_800_000_000, 0)}
	e.Service = &Service{
		DB: db, Settings: st, Now: func() time.Time { return e.now },
		RemoveSessions: func(_ context.Context, uid int64) error { e.removed = append(e.removed, uid); return nil },
		OnBan:          func(_ context.Context, b BanInfo) { e.bans = append(e.bans, b) },
	}
	return e
}

func wantMsg(t *testing.T, err error, prefix string) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || !strings.HasPrefix(he.Msg, prefix) {
		t.Fatalf("期望错误 %q，实际 %v", prefix, err)
	}
}

func (e *env) user(t *testing.T, email string, admin bool) (*model.User, *model.Subscription) {
	t.Helper()
	var u *model.User
	var sub *model.Subscription
	err := e.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		u, sub, err = account.Create(tx, account.NewUser{Email: email, Password: "password123"})
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if admin {
		e.DB.Model(u).Update("is_admin", 1)
	}
	return u, sub
}

func TestAudit(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			t.Run("规则", func(t *testing.T) { testRules(t, newEnv(t, driver)) })
			t.Run("上报与封禁", func(t *testing.T) { testReport(t, newEnv(t, driver)) })
		})
	}
}

func testRules(t *testing.T, e *env) {
	ctx := context.Background()
	g1, g2 := &model.ServerGroup{Name: "g1"}, &model.ServerGroup{Name: "g2"}
	e.DB.Create(g1)
	e.DB.Create(g2)

	_, err := e.SaveRule(ctx, 0, RuleInput{Name: " ", Pattern: "a.com", Action: "log"})
	wantMsg(t, err, "规则名称")
	_, err = e.SaveRule(ctx, 0, RuleInput{Name: "x", Pattern: "a.com", Action: "kill"})
	wantMsg(t, err, "动作不正确")
	_, err = e.SaveRule(ctx, 0, RuleInput{Name: "x", Pattern: "a.com:0", Action: "log"})
	wantMsg(t, err, "匹配内容有误")
	_, err = e.SaveRule(ctx, 0, RuleInput{Name: "x", Pattern: "a.com", Action: "log", GroupID: []int64{999999}})
	wantMsg(t, err, "权限组不存在")

	all, err := e.SaveRule(ctx, 0, RuleInput{Name: "全部", Pattern: "# 说明\nA.com\n:25", Action: "block", Enabled: true, Sort: 2})
	if err != nil {
		t.Fatal(err)
	}
	only1, err := e.SaveRule(ctx, 0, RuleInput{Name: "仅 g1", Pattern: "b.com", Action: "ban", GroupID: []int64{g1.ID, g1.ID}, Enabled: true, Sort: 1})
	if err != nil || len(only1.GroupID) != 1 {
		t.Fatalf("保存失败: %v %v", err, only1)
	}
	off, _ := e.SaveRule(ctx, 0, RuleInput{Name: "停用", Pattern: "c.com", Action: "log"})

	rules, _ := e.Rules(ctx)
	if len(rules) != 3 || rules[0].ID != off.ID || rules[1].ID != only1.ID {
		t.Fatalf("排序不符: %+v", rules)
	}

	srv := func(groups ...int64) *model.ServerV2node {
		return &model.ServerV2node{ServerBase: model.ServerBase{ID: 7, Name: "n", GroupID: groups}}
	}
	got, err := e.NodeRules(ctx, srv(g2.ID))
	if err != nil || len(got) != 1 || got[0].ID != all.ID || !slices.Equal(got[0].Match, []string{"a.com", ":25"}) {
		t.Fatalf("g2 节点规则不符: %+v %v", got, err)
	}
	got, _ = e.NodeRules(ctx, srv(g1.ID, g2.ID))
	if len(got) != 2 || got[0].ID != only1.ID || got[0].Action != "ban" {
		t.Fatalf("g1 节点规则不符: %+v", got)
	}

	if _, err := e.SaveRule(ctx, off.ID, RuleInput{Name: "改名", Pattern: "c.com", Action: "block", Enabled: true}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.SaveRule(ctx, 999999, RuleInput{Name: "x", Pattern: "c.com", Action: "log"}); !errors.Is(err, errRuleNotExist) {
		t.Fatalf("修改不存在的规则应失败: %v", err)
	}

	hits, err := e.Test(ctx, "www.a.com:443")
	if err != nil || len(hits) != 1 || hits[0].ID != all.ID || hits[0].Line != "a.com" {
		t.Fatalf("测试结果不符: %+v %v", hits, err)
	}
	if hits, _ := e.Test(ctx, "1.1.1.1:25"); len(hits) != 1 || hits[0].Line != ":25" {
		t.Fatalf("端口测试不符: %+v", hits)
	}

	if err := e.DeleteRule(ctx, off.ID); err != nil {
		t.Fatal(err)
	}
	if err := e.DeleteRule(ctx, off.ID); !errors.Is(err, errRuleNotExist) {
		t.Fatalf("重复删除应失败: %v", err)
	}
}

func testReport(t *testing.T, e *env) {
	ctx := context.Background()
	g1, g2 := &model.ServerGroup{Name: "g1"}, &model.ServerGroup{Name: "g2"}
	e.DB.Create(g1)
	e.DB.Create(g2)
	ban, _ := e.SaveRule(ctx, 0, RuleInput{Name: "封禁", Pattern: "bad.com", Action: "ban", Enabled: true})
	block, _ := e.SaveRule(ctx, 0, RuleInput{Name: "阻断", Pattern: ":25", Action: "block", Enabled: true})
	other, _ := e.SaveRule(ctx, 0, RuleInput{Name: "别的组", Pattern: "x.com", Action: "log", GroupID: []int64{g2.ID}, Enabled: true})

	u1, s1 := e.user(t, "u1@x.com", false)
	adm, sa := e.user(t, "admin@x.com", true)
	_, s3 := e.user(t, "u3@x.com", false)
	srv := &model.ServerV2node{ServerBase: model.ServerBase{ID: 9, Name: "香港", GroupID: model.IntList{g1.ID}}}

	at := e.now.Unix() - 30
	err := e.Report(ctx, model.ServerTypeV2node, srv, []Record{
		{UserID: s1.ID, RuleID: ban.ID, Action: "ban", Target: "bad.com:443", Network: "tcp", IP: "1.1.1.1", Count: 2, Time: at},
		{UserID: s1.ID, RuleID: block.ID, Action: "block", Target: "9.9.9.9:25", Network: "tcp", Count: 0, Time: e.now.Unix() + 3600},
		{UserID: sa.ID, RuleID: ban.ID, Action: "ban", Target: "bad.com:443", Network: "tcp", Count: 1},
		{UserID: s3.ID, RuleID: other.ID, Action: "log", Target: "x.com:80"},    // 规则对该节点不生效
		{UserID: 999999, RuleID: ban.ID, Action: "ban", Target: "bad.com:443"},  // 订阅不存在
		{UserID: s3.ID, RuleID: block.ID, Action: "drop", Target: "9.9.9.9:25"}, // 动作不合法
	})
	if err != nil {
		t.Fatal(err)
	}

	var logs []model.AuditLog
	e.DB.Order("id").Find(&logs)
	if len(logs) != 3 {
		t.Fatalf("应写入 3 条汇总日志，实际 %d", len(logs))
	}
	if l := logs[0]; l.UserID != u1.ID || l.Banned != 1 || l.Count != 2 || l.HitAt != at || l.ServerID != 9 || l.ServerType != "v2node" || l.DetailCount != 1 || l.Day == 0 {
		t.Fatalf("第一条日志不符: %+v", l)
	}
	if l := logs[1]; l.Banned != 0 || l.Count != 1 || l.HitAt != e.now.Unix() {
		t.Fatalf("第二条日志不符: %+v", l)
	}
	if l := logs[2]; l.UserID != adm.ID || l.Banned != 0 {
		t.Fatalf("管理员不应被封禁: %+v", l)
	}
	var dets []model.AuditLogDetail
	e.DB.Where("log_id = ?", logs[0].ID).Find(&dets)
	if len(dets) != 1 || dets[0].Target != "bad.com:443" || dets[0].Count != 2 {
		t.Fatalf("第一条细节不符: %+v", dets)
	}

	var u model.User
	e.DB.Take(&u, u1.ID)
	if u.Banned != 1 || u.Remarks == nil || !strings.Contains(*u.Remarks, "规则「封禁」") || !strings.Contains(*u.Remarks, "v2node#9 香港") {
		t.Fatalf("账号应被封禁并写入备注: %+v", u)
	}
	var au model.User
	e.DB.Take(&au, adm.ID)
	if au.Banned != 0 {
		t.Fatal("管理员账号不应被封禁")
	}
	if !slices.Equal(e.removed, []int64{u1.ID}) || len(e.bans) != 1 || e.bans[0].Email != "u1@x.com" || e.bans[0].Target != "bad.com:443" {
		t.Fatalf("封禁回调不符: %v %+v", e.removed, e.bans)
	}

	var r model.AuditRule
	e.DB.Take(&r, ban.ID)
	if r.HitCount != 3 || r.LastHitAt == nil || *r.LastHitAt != e.now.Unix() {
		t.Fatalf("命中数不符: %+v", r)
	}

	// 已封禁的账号再次命中不重复封禁（同日合并进原汇总）；规则改为记录后只写日志
	e.Report(ctx, model.ServerTypeV2node, srv, []Record{{UserID: s1.ID, RuleID: ban.ID, Action: "ban", Target: "bad.com:443", Network: "tcp"}})
	var merged model.AuditLog
	e.DB.Take(&merged, logs[0].ID)
	if merged.Count != 3 || merged.DetailCount != 1 {
		t.Fatalf("同日同目标应合并次数: %+v", merged)
	}
	e.SaveRule(ctx, ban.ID, RuleInput{Name: "封禁", Pattern: "bad.com", Action: "log", Enabled: true})
	e.Report(ctx, model.ServerTypeV2node, srv, []Record{{UserID: s3.ID, RuleID: ban.ID, Action: "ban", Target: "bad.com:443", Network: "tcp"}})
	if len(e.bans) != 1 {
		t.Fatalf("不应再次封禁: %+v", e.bans)
	}

	// 同日不同子域名合并为一条汇总，细节可查
	logRule, _ := e.SaveRule(ctx, 0, RuleInput{Name: "记录", Pattern: "bing.com", Action: "log", Enabled: true})
	err = e.Report(ctx, model.ServerTypeV2node, srv, []Record{
		{UserID: s1.ID, RuleID: logRule.ID, Action: "log", Target: "www.bing.com:443", Network: "tcp", IP: "1.1.1.1", Count: 1, Time: at},
		{UserID: s1.ID, RuleID: logRule.ID, Action: "log", Target: "th.bing.com:443", Network: "tcp", IP: "1.1.1.1", Count: 2, Time: at + 10},
		{UserID: s1.ID, RuleID: logRule.ID, Action: "log", Target: "c.bing.com:443", Network: "tcp", IP: "1.1.1.1", Count: 1, Time: at + 20},
	})
	if err != nil {
		t.Fatal(err)
	}
	var bing model.AuditLog
	e.DB.Where("rule_id = ? AND subscription_id = ?", logRule.ID, s1.ID).Take(&bing)
	if bing.Count != 4 || bing.DetailCount != 3 || bing.Target != "th.bing.com:443" {
		t.Fatalf("按天合并不符: %+v", bing)
	}
	details, err := e.LogDetails(ctx, bing.ID)
	if err != nil || len(details) != 3 || details[0].Target != "c.bing.com:443" || details[0].HitAt != at+20 {
		t.Fatalf("细节查询不符: %+v %v", details, err)
	}

	// 查询（u1：封禁汇总、阻断、记录 bing 共 3 条；同日再次封禁已合并）
	page, err := e.Logs(ctx, LogQuery{UserID: u1.ID})
	if err != nil || page.Total != 3 || page.Data[0].Email != "u1@x.com" {
		t.Fatalf("按用户查询不符: %+v %v", page, err)
	}
	page, _ = e.Logs(ctx, LogQuery{Target: "th.bing"})
	if page.Total != 1 || page.Data[0].ID != bing.ID {
		t.Fatalf("按细节目标查询应命中汇总: %+v", page)
	}
	one := 1
	page, _ = e.Logs(ctx, LogQuery{Banned: &one})
	if page.Total != 1 || page.Data[0].RuleName != "封禁" {
		t.Fatalf("按封禁查询不符: %+v", page)
	}
	page, _ = e.Logs(ctx, LogQuery{Target: "9.9.9", Action: "block"})
	if page.Total != 1 {
		t.Fatalf("按目标查询不符: %+v", page)
	}
	page, _ = e.Logs(ctx, LogQuery{Target: "%"})
	if page.Total != 0 {
		t.Fatalf("LIKE 通配符应被转义: %+v", page)
	}

	// 清理：默认保留 30 天
	e.DB.Model(&model.AuditLog{}).Where("id = ?", logs[0].ID).Update("created_at", e.now.Unix()-31*86400)
	n, err := e.CleanLogs(ctx)
	if err != nil || n != 1 {
		t.Fatalf("清理不符: %d %v", n, err)
	}
}
