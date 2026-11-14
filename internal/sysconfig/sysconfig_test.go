package sysconfig

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

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/config"
	"gvboard/internal/httpx"
	"gvboard/internal/mail"
	"gvboard/internal/model"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/testutil"
)

type sent struct{ to, subject, body, host string }

type env struct {
	*Service
	auth *auth.Service
	mu   sync.Mutex
	sent []sent
	fail error
	// failHosts 这些 SMTP 服务器发送失败。
	failHosts map[string]bool
}

func newEnv(t *testing.T, driver string) *env {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	m, err := mail.New(st)
	if err != nil {
		t.Fatal(err)
	}
	e := &env{auth: auth.New(db, c, "base64:c3lzY29uZmlnLXRlc3Qta2V5LTAxMjM0NTY3ODk=")}
	m.SetTransport(func(_ context.Context, c mail.SMTPConfig, to, subject, body string) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		if e.failHosts[c.Host] {
			return errors.New("连接失败")
		}
		e.sent = append(e.sent, sent{to, subject, body, c.Host})
		return e.fail
	})
	e.Service = &Service{DB: db, Settings: st, Mailer: m}
	return e
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

func wantValidation(t *testing.T, err error, contains string) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != http.StatusUnprocessableEntity || !strings.Contains(he.Msg, contains) {
		t.Fatalf("期望包含 %q 的校验错误，实际 %v", contains, err)
	}
}

func TestFetchSave(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		if err := e.Settings.Set(ctx, map[string]any{"email_password": "smtp-secret", "server_token": "0123456789abcdefXYZ"}); err != nil {
			t.Fatal(err)
		}
		all, err := e.Fetch("")
		if err != nil {
			t.Fatal(err)
		}
		if _, ok := all[setting.GroupLegacy]; ok {
			t.Error("不应返回遗留分组")
		}
		if all["email"]["email_password"] != "smtp-secret" || all["server"]["server_token"] != "0123456789abcdefXYZ" {
			t.Error("敏感项应明文返回")
		}
		if all["site"]["app_name"] != "Gvboard" || fmt.Sprint(all["gvboard"]["subscription_limit"]) != "5" {
			t.Errorf("默认值错误：%v %v", all["site"]["app_name"], all["gvboard"]["subscription_limit"])
		}
		one, err := e.Fetch("email")
		if err != nil || len(one) != 1 || len(one["email"]) != 9 {
			t.Fatalf("按分组获取错误：%v %v", one, err)
		}
		if _, err := e.Fetch("legacy"); err == nil {
			t.Error("遗留分组应报错")
		}
		if _, err := e.Fetch("nope"); err == nil {
			t.Error("不存在的分组应报错")
		}

		// 校验失败时全部不保存
		bad := []struct {
			values   map[string]any
			contains string
		}{
			{map[string]any{}, "没有需要保存"},
			{map[string]any{"nope": 1}, "nope"},
			{map[string]any{"stripe_pk_live": "x"}, "stripe_pk_live"},
			{map[string]any{"app_name": "新", "invite_commission": "abc"}, "invite_commission"},
			{map[string]any{"app_name": "新", "app_url": "example.com"}, "app_url"},
			{map[string]any{"logo": "javascript:alert(1)"}, "logo"},
			{map[string]any{"ticket_status": 3}, "ticket_status"},
			{map[string]any{"reset_traffic_method": 5}, "reset_traffic_method"},
			{map[string]any{"invite_commission": 101}, "invite_commission"},
			{map[string]any{"commission_distribution_l1": -1}, "commission_distribution_l1"},
			{map[string]any{"frontend_theme_color": "red"}, "frontend_theme_color"},
			{map[string]any{"email_encryption": "starttls"}, "email_encryption"},
			{map[string]any{"server_token": "short"}, "server_token"},
			{map[string]any{"deposit_bounus": []any{"100:10", "abc"}}, "deposit_bounus"},
			{map[string]any{"email_template": "nope"}, "email_template"},
			{map[string]any{"secure_path": "short"}, "secure_path"},
			{map[string]any{"subscribe_template_clash": "a: [b"}, "subscribe_template_clash"},
			{map[string]any{"subscribe_rate_action": "x"}, "subscribe_rate_action"},
		}
		for _, c := range bad {
			wantValidation(t, e.Save(ctx, c.values), c.contains)
		}
		if e.Settings.String("app_name") != "Gvboard" {
			t.Fatal("校验失败时不应保存任何项")
		}

		err = e.Save(ctx, map[string]any{
			"app_name": "我的站", "app_url": "https://a.example.com", "logo": "", "ticket_status": json.Number("2"),
			"deposit_bounus": []any{"100:10", "", "200.5:30"}, "email_template": "classic", "server_token": "",
			"commission_distribution_l1": json.Number("12.5"), "email_password": nil, "telegram_welcome": "<b>{app_name}</b>",
			"email_encryption": " SSL ",
		})
		if err != nil {
			t.Fatal(err)
		}
		if e.Settings.String("app_name") != "我的站" || e.Settings.Int("ticket_status") != 2 || e.Settings.String("email_template") != "classic" ||
			len(e.Settings.Strings("deposit_bounus")) != 3 || e.Settings.Float("commission_distribution_l1") != 12.5 ||
			e.Settings.String("email_password") != "" || e.Settings.String("telegram_welcome") != "<b>{app_name}</b>" {
			t.Fatal("保存结果错误")
		}
		if e.Settings.String("email_host") != "" {
			t.Fatal("没有提交的项不应修改")
		}
	})
}

func TestTestMail(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		rec := e.TestMail(ctx, "a@x.com", "主题", "内容", nil)
		if rec.Error == nil || *rec.Error != mail.ErrNotConfigured.Error() || len(e.sent) != 0 {
			t.Fatalf("未配置时应记录错误：%+v", rec)
		}
		if err := e.Settings.Set(ctx, map[string]any{"email_host": "smtp.x.com", "email_from_address": "no-reply@x.com", "app_name": "站<点>"}); err != nil {
			t.Fatal(err)
		}
		rec = e.TestMail(ctx, "a@x.com", "主题", "<i>内容</i>", nil)
		if rec.Error != nil || rec.ID == 0 || len(e.sent) != 1 || e.sent[0].to != "a@x.com" || e.sent[0].subject != "主题" ||
			!strings.Contains(e.sent[0].body, "&lt;i&gt;内容&lt;/i&gt;") || strings.Contains(strings.ToLower(e.sent[0].body), "v2board") {
			t.Fatalf("测试邮件错误：%+v %+v", rec, e.sent)
		}
		e.fail = errors.New("535 auth failed")
		rec = e.TestMail(ctx, "a@x.com", "主题", "内容", nil)
		var n int64
		e.DB.Model(&model.MailLog{}).Count(&n)
		if rec.Error == nil || *rec.Error != "535 auth failed" || n != 3 {
			t.Fatalf("发送失败应写入日志：%+v %d", rec, n)
		}
	})
}

func TestMultiSMTP(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		servers := `[{"name":"A","host":"a.com","from_address":"n@a.com","password":"pa","enable":true},` +
			`{"name":"B","host":"b.com","port":587,"encryption":"tls","from_address":"n@b.com","enable":true},` +
			`{"name":"C","host":"c.com","from_address":"n@c.com","enable":false}]`
		wantValidation(t, e.Save(ctx, map[string]any{"email_smtp_servers": `[{"host":"","from_address":"n@a.com"}]`}), "服务器地址")
		wantValidation(t, e.Save(ctx, map[string]any{"email_smtp_servers": `[{"host":"a.com","from_address":"bad"}]`}), "发件地址")
		wantValidation(t, e.Save(ctx, map[string]any{"email_smtp_servers": `{}`}), "格式")
		wantValidation(t, e.Save(ctx, map[string]any{"email_smtp_mode": "random"}), "取值")
		if err := e.Save(ctx, map[string]any{"email_smtp_servers": servers}); err != nil {
			t.Fatal(err)
		}
		if e.Settings.String("email_host") != "a.com" || e.Settings.String("email_password") != "pa" || e.Settings.String("email_from_address") != "n@a.com" {
			t.Fatal("第一个服务器应同步到原单个 SMTP 设置")
		}
		hosts := func() (out []string) {
			for _, s := range e.sent {
				out = append(out, s.host)
			}
			e.sent = nil
			return
		}
		send := func() *string { return e.TestMail(ctx, "a@x.com", "s", "c", nil).Error }

		// 故障转移：总是先用第一个，失败后换下一个；未启用的不使用
		send()
		send()
		if h := strings.Join(hosts(), ","); h != "a.com,a.com" {
			t.Fatalf("故障转移应一直使用第一个：%s", h)
		}
		e.failHosts = map[string]bool{"a.com": true}
		if err := send(); err != nil || strings.Join(hosts(), ",") != "b.com" {
			t.Fatalf("第一个失败应换第二个：%v", err)
		}
		e.failHosts = map[string]bool{"a.com": true, "b.com": true}
		if err := send(); err == nil || !strings.Contains(*err, "A: 连接失败") || !strings.Contains(*err, "B: 连接失败") || len(hosts()) != 0 {
			t.Fatalf("全部失败应记录每个服务器的错误：%v", err)
		}

		// 轮询：每次换一个起点，失败时继续尝试其余服务器
		e.failHosts = nil
		if err := e.Save(ctx, map[string]any{"email_smtp_mode": "round_robin"}); err != nil {
			t.Fatal(err)
		}
		for range 4 {
			send()
		}
		if h := hosts(); h[0] == h[1] || h[0] != h[2] || h[1] != h[3] {
			t.Fatalf("轮询应交替使用：%v", h)
		}
		e.failHosts = map[string]bool{"b.com": true}
		for range 2 {
			if err := send(); err != nil {
				t.Fatalf("轮询中失败应换其它服务器：%v", *err)
			}
		}
		if h := strings.Join(hosts(), ","); h != "a.com,a.com" {
			t.Fatalf("轮询失败转移错误：%s", h)
		}

		// 测试指定服务器：只用该服务器，未启用的也可以测试
		e.failHosts = nil
		two, nine := 2, 9
		if rec := e.TestMail(ctx, "a@x.com", "s", "c", &two); rec.Error != nil || strings.Join(hosts(), ",") != "c.com" {
			t.Fatalf("测试指定服务器错误：%+v", rec)
		}
		if rec := e.TestMail(ctx, "a@x.com", "s", "c", &nine); rec.Error == nil || *rec.Error != mail.ErrNoServer.Error() {
			t.Fatalf("不存在的服务器应报错：%+v", rec)
		}

		// 全部停用时视为未配置
		if err := e.Save(ctx, map[string]any{"email_smtp_servers": `[{"host":"a.com","from_address":"n@a.com","enable":false}]`}); err != nil {
			t.Fatal(err)
		}
		if err := send(); err == nil || *err != mail.ErrNotConfigured.Error() {
			t.Fatalf("全部停用应为未配置：%v", err)
		}
		// 列表为空（未使用过多服务器，如从原项目迁移）时使用原单个配置
		if err := e.Settings.Set(ctx, map[string]any{"email_smtp_servers": "", "email_host": "legacy.com"}); err != nil {
			t.Fatal(err)
		}
		if err := send(); err != nil || strings.Join(hosts(), ",") != "legacy.com" {
			t.Fatal("列表为空时应使用原单个配置")
		}
		// 在管理端清空列表时一并清空单个配置
		if err := e.Save(ctx, map[string]any{"email_smtp_servers": "[]"}); err != nil {
			t.Fatal(err)
		}
		if err := send(); err == nil || *err != mail.ErrNotConfigured.Error() || e.Settings.String("email_host") != "" {
			t.Fatalf("清空列表后应为未配置：%v", err)
		}
	})
}

func TestThemeRule(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		if err := e.Save(ctx, map[string]any{"frontend_theme": "anything"}); err != nil {
			t.Fatalf("未设置主题检查时不应校验：%v", err)
		}
		e.ThemeExists = func(name string) bool { return name == "default" || name == "custom" }
		wantValidation(t, e.Save(ctx, map[string]any{"frontend_theme": "nope"}), "主题不存在")
		if err := e.Save(ctx, map[string]any{"frontend_theme": "anything", "app_name": "x"}); err != nil {
			t.Fatalf("保持原值不变时不应校验：%v", err)
		}
		if err := e.Save(ctx, map[string]any{"frontend_theme": "custom"}); err != nil || e.Settings.String("frontend_theme") != "custom" {
			t.Fatalf("切换主题失败：%v", err)
		}
	})
}

func TestHTTP(t *testing.T) {
	eachDriver(t, func(t *testing.T, e *env) {
		ctx := context.Background()
		const secure = "sysconfig0123456789"
		if err := e.Settings.Set(ctx, map[string]any{"secure_path": secure, "email_host": "smtp.x.com", "email_from_address": "f@x.com"}); err != nil {
			t.Fatal(err)
		}
		var admin, normal *model.User
		e.DB.Transaction(func(tx *gorm.DB) error {
			admin, _, _ = account.Create(tx, account.NewUser{Email: "admin@x.com", Password: "password123"})
			normal, _, _ = account.Create(tx, account.NewUser{Email: "n@x.com", Password: "password123"})
			return tx.Model(admin).Update("is_admin", 1).Error
		})
		adminTok, _ := e.auth.Issue(ctx, admin.ID, "", "")
		userTok, _ := e.auth.Issue(ctx, normal.ID, "", "")
		cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
		r, err := server.NewRouter(cfg, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		r.Mount(&Module{Svc: e.Service, Auth: e.auth, Translate: func(_ *gin.Context, key string, p map[string]any) string {
			return strings.ReplaceAll(key, ":app_name", p["app_name"].(string))
		}})
		do := func(method, path, body, tok string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, "/api/t1/"+path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", tok)
			w := httptest.NewRecorder()
			r.Engine.ServeHTTP(w, req)
			return w
		}
		if w := do("GET", secure+"/config", "", userTok); w.Code != http.StatusNotFound {
			t.Fatalf("普通用户应 404，实际 %d", w.Code)
		}
		if w := do("GET", secure+"/config?group=email", "", adminTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"email_host":"smtp.x.com"`) {
			t.Fatalf("获取配置错误：%d %s", w.Code, w.Body)
		}
		if w := do("PUT", secure+"/config", `{"server_pull_interval": 30, "subscription_limit": 9007199254740993}`, adminTok); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("超出范围应 422，实际 %d", w.Code)
		}
		if w := do("PUT", secure+"/config", `{"server_pull_interval": 30.5}`, adminTok); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("小数应 422，实际 %d", w.Code)
		}
		if w := do("PUT", secure+"/config", `[1]`, adminTok); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("格式错误应 422，实际 %d", w.Code)
		}
		if w := do("PUT", secure+"/config", `{"server_pull_interval": 30, "secure_path": "newsecurepath0123456"}`, adminTok); w.Code != 200 {
			t.Fatalf("保存失败：%d %s", w.Code, w.Body)
		}
		if e.Settings.Int("server_pull_interval") != 30 {
			t.Fatal("保存未生效")
		}
		if w := do("GET", secure+"/config", "", adminTok); w.Code != http.StatusNotFound {
			t.Fatalf("修改管理端路径后旧路径应 404，实际 %d", w.Code)
		}
		if w := do("GET", "newsecurepath0123456/config/email-templates", "", adminTok); w.Code != 200 ||
			!strings.Contains(w.Body.String(), `["classic","default"]`) {
			t.Fatalf("邮件模板列表错误：%s", w.Body)
		}
		e.Settings.Set(ctx, map[string]any{"app_name": "测试站"})
		if w := do("POST", "newsecurepath0123456/config/test-mail", "", adminTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"error":null`) {
			t.Fatalf("测试邮件接口错误：%s", w.Body)
		}
		if len(e.sent) != 1 || e.sent[0].to != "admin@x.com" || e.sent[0].subject != "Test email from 测试站" {
			t.Fatalf("测试邮件收件人或主题错误：%+v", e.sent)
		}
		const p = "newsecurepath0123456/config/mail-templates"
		if w := do("POST", p+"/preview", `{"name":"verify","content":"<b>{{.name}} {{.code}}</b>"}`, adminTok); w.Code != 200 ||
			!strings.Contains(w.Body.String(), `测试站 123456`) {
			t.Fatalf("预览错误：%s", w.Body)
		}
		if w := do("PUT", p+"/brand/verify", `{"content":"x"}`, userTok); w.Code != http.StatusNotFound {
			t.Fatalf("普通用户应 404，实际 %d", w.Code)
		}
		if w := do("POST", p, `{"theme":"brand","from":"classic"}`, adminTok); w.Code != 200 {
			t.Fatalf("新增模板失败：%s", w.Body)
		}
		if w := do("GET", p+"/brand/verify", "", adminTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"custom":true`) ||
			!strings.Contains(w.Body.String(), `"default":""`) {
			t.Fatalf("新增模板内容错误：%s", w.Body)
		}
		if w := do("PUT", p+"/brand/verify", `{"content":"{{if}}"}`, adminTok); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("语法错误应 422，实际 %d", w.Code)
		}
		if w := do("PUT", p+"/brand/verify", `{"content":""}`, adminTok); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("新增模板内容为空应 422，实际 %d", w.Code)
		}
		if w := do("PUT", p+"/brand/verify", `{"content":"<p>品牌 {{.code}}</p>"}`, adminTok); w.Code != 200 {
			t.Fatalf("保存模板失败：%s", w.Body)
		}
		if w := do("PUT", "newsecurepath0123456/config", `{"email_template":"brand"}`, adminTok); w.Code != 200 {
			t.Fatalf("切换到新增模板失败：%s", w.Body)
		}
		if w := do("PUT", "newsecurepath0123456/config", `{"email_templates_custom":"{}"}`, adminTok); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("系统配置接口不能修改自定义模板，实际 %d", w.Code)
		}
		if w := do("GET", "newsecurepath0123456/config", "", adminTok); strings.Contains(w.Body.String(), "email_templates_custom") {
			t.Fatal("系统配置接口不应返回自定义模板")
		}
		classic, _ := e.Mailer.Render("classic", mail.TplNotify, map[string]any{"content": "c"})
		if out, _ := e.Mailer.Render("brand", mail.TplNotify, map[string]any{"content": "c"}); out != classic {
			t.Fatal("新增模板中未修改的模板应与来源模板一致")
		}
		if out, _ := e.Mailer.Render(e.Mailer.Theme(), mail.TplVerify, map[string]any{"code": "7"}); out != "<p>品牌 7</p>" {
			t.Fatalf("切换模板后未使用新增模板：%s", out)
		}
		if w := do("DELETE", p+"/brand", "", adminTok); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("正在使用的模板不能删除，实际 %d", w.Code)
		}
		if w := do("DELETE", p+"/default", "", adminTok); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("内置模板不能删除，实际 %d", w.Code)
		}
		if w := do("PUT", p+"/default/verify", `{"content":"<p>改 {{.code}}</p>"}`, adminTok); w.Code != 200 {
			t.Fatalf("修改内置模板失败：%s", w.Body)
		}
		if out, _ := e.Mailer.Render("default", mail.TplVerify, map[string]any{"code": "9"}); out != "<p>改 9</p>" {
			t.Fatalf("修改内置模板未生效：%s", out)
		}
		if w := do("PUT", p+"/default/verify", `{"content":""}`, adminTok); w.Code != 200 {
			t.Fatalf("恢复默认失败：%s", w.Body)
		}
		e.Settings.Set(ctx, map[string]any{"email_template": "default"})
		if w := do("DELETE", p+"/brand", "", adminTok); w.Code != 200 {
			t.Fatalf("删除模板失败：%s", w.Body)
		}
		if e.Settings.String(mail.CustomKey) != "" {
			t.Fatalf("全部恢复后自定义模板应为空：%s", e.Settings.String(mail.CustomKey))
		}
		for _, bad := range []string{`{"theme":"../x","from":"default"}`, `{"theme":"default","from":"default"}`, `{"theme":"n","from":"nope"}`} {
			if w := do("POST", p, bad, adminTok); w.Code != http.StatusUnprocessableEntity {
				t.Fatalf("%s 应 422，实际 %d", bad, w.Code)
			}
		}
	})
}
