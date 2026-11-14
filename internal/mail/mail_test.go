package mail

import (
	"context"
	"errors"
	"html/template"
	"strings"
	"testing"

	"gvboard/internal/hook"
)

type fakeSettings map[string]any

func (f fakeSettings) String(k string) string {
	s, _ := f[k].(string)
	return s
}

func (f fakeSettings) Int(k string) int64 {
	n, _ := f[k].(int)
	return int64(n)
}

func newMailer(t *testing.T, s fakeSettings) *Mailer {
	t.Helper()
	m, err := New(s)
	if err != nil {
		t.Fatal(err)
	}
	return m
}

func TestTemplatesRender(t *testing.T) {
	m := newMailer(t, fakeSettings{})
	if got := strings.Join(m.Themes(), ","); got != "classic,default" {
		t.Fatalf("模板风格 %s", got)
	}
	vars := map[string]any{"name": "测试站", "url": "https://example.com", "code": "123456", "link": "https://example.com/login?t=abc", "content": "第一行\n第二行"}
	for _, theme := range m.Themes() {
		for _, name := range []string{TplVerify, TplNotify, TplRemindExpire, TplRemindTraffic, TplMailLogin} {
			out, err := m.Render(theme, name, vars)
			if err != nil {
				t.Fatalf("%s/%s: %v", theme, name, err)
			}
			if !strings.Contains(out, "测试站") || !strings.Contains(out, "https://example.com") {
				t.Errorf("%s/%s 未替换站点名称或地址", theme, name)
			}
			for _, bad := range []string{"{{", "}}", "{!!", "$name", "<no value>", "v2board", "V2Board"} {
				if strings.Contains(out, bad) {
					t.Errorf("%s/%s 输出包含 %q", theme, name, bad)
				}
			}
			switch name {
			case TplVerify:
				if !strings.Contains(out, "123456") {
					t.Errorf("%s/%s 缺少验证码", theme, name)
				}
			case TplMailLogin:
				if !strings.Contains(out, "https://example.com/login?t=abc") {
					t.Errorf("%s/%s 缺少登录链接", theme, name)
				}
			case TplNotify:
				if !strings.Contains(out, "第一行<br />\n第二行") {
					t.Errorf("%s/%s 换行未转换", theme, name)
				}
			}
		}
	}
}

func TestNotifyEscaping(t *testing.T) {
	m := newMailer(t, fakeSettings{})
	// 普通内容（如工单主题、回复）必须转义，防止向邮件注入 HTML
	out, _ := m.Render(DefaultTheme, TplNotify, map[string]any{"name": "<b>站</b>", "url": "u", "content": `<a href="https://evil">点我</a>`})
	if strings.Contains(out, `<a href="https://evil">`) || strings.Contains(out, "<b>站</b>") {
		t.Error("普通内容未转义")
	}
	// 管理员群发标记为可信 HTML，原样输出（与原项目一致）
	out, _ = m.Render(DefaultTheme, TplNotify, map[string]any{"name": "站", "url": "u", "content": template.HTML("<b>公告</b>\n内容")})
	if !strings.Contains(out, "<b>公告</b><br />\n内容") {
		t.Error("可信 HTML 未原样输出")
	}
}

func TestThemeFallback(t *testing.T) {
	m := newMailer(t, fakeSettings{"email_template": "classic"})
	if m.Theme() != "classic" {
		t.Error("应使用 classic")
	}
	m = newMailer(t, fakeSettings{"email_template": "../../etc"})
	if m.Theme() != DefaultTheme {
		t.Error("不存在的风格应回退默认")
	}
	if _, err := m.Render("nope", TplVerify, nil); err != nil {
		t.Errorf("不存在的风格应回退默认模板: %v", err)
	}
	if _, err := m.Render(DefaultTheme, "nope", nil); err == nil {
		t.Error("不存在的模板应报错")
	}
}

func TestConfig(t *testing.T) {
	m := newMailer(t, fakeSettings{})
	if _, err := m.Config(); !errors.Is(err, ErrNotConfigured) {
		t.Error("未配置时应返回 ErrNotConfigured")
	}
	for enc, port := range map[string]int{"ssl": 465, "TLS": 587, "": 25} {
		m = newMailer(t, fakeSettings{"email_host": "smtp.x.com", "email_from_address": "a@x.com", "email_encryption": enc, "app_name": "站"})
		c, err := m.Config()
		if err != nil || c.Port != port || c.FromName != "站" {
			t.Errorf("加密方式 %q 默认端口应为 %d: %+v %v", enc, port, c, err)
		}
	}
	m = newMailer(t, fakeSettings{"email_host": "smtp.x.com", "email_from_address": "a@x.com", "email_port": 2525})
	if c, _ := m.Config(); c.Port != 2525 {
		t.Error("应使用设置的端口")
	}
}

func TestSend(t *testing.T) {
	m := newMailer(t, fakeSettings{"email_host": "smtp.x.com", "email_from_address": "a@x.com", "app_name": "站"})
	var got struct{ to, subject, body string }
	m.SetTransport(func(_ context.Context, c SMTPConfig, to, subject, body string) error {
		got.to, got.subject, got.body = to, subject, body
		return nil
	})
	if err := m.Send(context.Background(), "u@x.com", "主题", DefaultTheme, TplVerify, map[string]any{"name": "站", "code": "999"}); err != nil {
		t.Fatal(err)
	}
	if got.to != "u@x.com" || got.subject != "主题" || !strings.Contains(got.body, "999") {
		t.Errorf("发送参数错误: %+v", got)
	}

	m.Hooks = filterHooks(func(v any) any {
		msg := v.(hook.MailMessage)
		msg.Subject, msg.HTML = "[插件]"+msg.Subject, msg.HTML+"<p>footer</p>"
		return msg
	})
	if err := m.Send(context.Background(), "u@x.com", "主题", DefaultTheme, TplVerify, map[string]any{"name": "站", "code": "999"}); err != nil {
		t.Fatal(err)
	}
	if got.to != "u@x.com" || got.subject != "[插件]主题" || !strings.HasSuffix(got.body, "<p>footer</p>") {
		t.Errorf("邮件过滤器未生效: %+v", got.subject)
	}
	m.Hooks = filterHooks(func(v any) any { msg := v.(hook.MailMessage); msg.To = "evil@x.com"; return msg })
	if err := m.Send(context.Background(), "u@x.com", "主题", DefaultTheme, TplVerify, map[string]any{"name": "站", "code": "999"}); err != nil {
		t.Fatal(err)
	}
	if got.to != "u@x.com" || got.subject != "主题" {
		t.Errorf("修改收件人的结果应被忽略: %+v", got.to)
	}
}

type filterHooks func(v any) any

func (filterHooks) Emit(context.Context, string, any) {}

func (f filterHooks) Filter(_ context.Context, _ string, v any, check func(old, new any) error) any {
	out := f(v)
	if check(v, out) != nil {
		return v
	}
	return out
}
