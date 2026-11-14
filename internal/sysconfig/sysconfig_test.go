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

type sent struct{ to, subject, body string }

type env struct {
	*Service
	auth *auth.Service
	mu   sync.Mutex
	sent []sent
	fail error
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
	m.SetTransport(func(_ context.Context, _ mail.SMTPConfig, to, subject, body string) error {
		e.mu.Lock()
		defer e.mu.Unlock()
		e.sent = append(e.sent, sent{to, subject, body})
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
		if err != nil || len(one) != 1 || len(one["email"]) != 7 {
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
		rec := e.TestMail(ctx, "a@x.com", "主题", "内容")
		if rec.Error == nil || *rec.Error != mail.ErrNotConfigured.Error() || len(e.sent) != 0 {
			t.Fatalf("未配置时应记录错误：%+v", rec)
		}
		if err := e.Settings.Set(ctx, map[string]any{"email_host": "smtp.x.com", "email_from_address": "no-reply@x.com", "app_name": "站<点>"}); err != nil {
			t.Fatal(err)
		}
		rec = e.TestMail(ctx, "a@x.com", "主题", "<i>内容</i>")
		if rec.Error != nil || rec.ID == 0 || len(e.sent) != 1 || e.sent[0].to != "a@x.com" || e.sent[0].subject != "主题" ||
			!strings.Contains(e.sent[0].body, "&lt;i&gt;内容&lt;/i&gt;") || strings.Contains(strings.ToLower(e.sent[0].body), "v2board") {
			t.Fatalf("测试邮件错误：%+v %+v", rec, e.sent)
		}
		e.fail = errors.New("535 auth failed")
		rec = e.TestMail(ctx, "a@x.com", "主题", "内容")
		var n int64
		e.DB.Model(&model.MailLog{}).Count(&n)
		if rec.Error == nil || *rec.Error != "535 auth failed" || n != 3 {
			t.Fatalf("发送失败应写入日志：%+v %d", rec, n)
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
	})
}
