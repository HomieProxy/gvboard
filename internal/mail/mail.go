// Package mail 邮件发送，对应原项目 SendEmailJob 与 resources/views/mail 模板。
package mail

import (
	"bytes"
	"context"
	"embed"
	"errors"
	"fmt"
	"html"
	"html/template"
	"io/fs"
	"path"
	"sort"
	"strings"
	"time"

	gomail "github.com/wneessen/go-mail"

	"gvboard/internal/hook"
)

//go:embed templates
var templateFS embed.FS

// 模板名，与原项目一致。
const (
	TplVerify        = "verify"
	TplNotify        = "notify"
	TplRemindExpire  = "remindExpire"
	TplRemindTraffic = "remindTraffic"
	TplMailLogin     = "mailLogin"
)

// DefaultTheme 默认邮件模板风格。
const DefaultTheme = "default"

// Settings 邮件模块读取的业务设置。
type Settings interface {
	String(key string) string
	Int(key string) int64
}

// SMTPConfig SMTP 连接参数。
type SMTPConfig struct {
	Host       string
	Port       int
	Encryption string // ssl：隐式 TLS；tls：STARTTLS；空：服务器支持时使用 STARTTLS
	Username   string
	Password   string
	From       string
	FromName   string
}

// Transport 实际发送邮件，测试时可替换。
type Transport func(ctx context.Context, c SMTPConfig, to, subject, htmlBody string) error

// Mailer 邮件服务。
type Mailer struct {
	settings  Settings
	templates map[string]*template.Template // 键为 "风格/模板名"
	send      Transport
	// Hooks 插件过滤器（邮件内容），启动时设置，可为 nil。
	Hooks hook.Hooks
}

// New 加载内置模板。
func New(s Settings) (*Mailer, error) {
	m := &Mailer{settings: s, templates: map[string]*template.Template{}, send: SMTPTransport}
	err := fs.WalkDir(templateFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".html" {
			return err
		}
		b, err := templateFS.ReadFile(p)
		if err != nil {
			return err
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".html")
		t, err := template.New(rel).Funcs(template.FuncMap{"nl2br": nl2br}).Option("missingkey=zero").Parse(string(b))
		if err != nil {
			return fmt.Errorf("邮件模板 %s 解析失败: %w", rel, err)
		}
		m.templates[rel] = t
		return nil
	})
	return m, err
}

// SetTransport 替换发送实现（测试用）。
func (m *Mailer) SetTransport(t Transport) { m.send = t }

// Themes 返回可用的模板风格。
func (m *Mailer) Themes() []string {
	seen := map[string]bool{}
	for k := range m.templates {
		seen[strings.SplitN(k, "/", 2)[0]] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Theme 返回当前设置的模板风格，不存在时使用默认风格。
func (m *Mailer) Theme() string {
	theme := m.settings.String("email_template")
	for k := range m.templates {
		if strings.HasPrefix(k, theme+"/") {
			return theme
		}
	}
	return DefaultTheme
}

// Render 渲染模板。
func (m *Mailer) Render(theme, name string, vars map[string]any) (string, error) {
	t, ok := m.templates[theme+"/"+name]
	if !ok {
		if t, ok = m.templates[DefaultTheme+"/"+name]; !ok {
			return "", fmt.Errorf("邮件模板不存在: %s", name)
		}
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("渲染邮件模板 %s 失败: %w", name, err)
	}
	return buf.String(), nil
}

// ErrNotConfigured 未配置 SMTP。
var ErrNotConfigured = errors.New("邮件服务未配置")

// Config 从业务设置读取 SMTP 参数。每次发送时读取，管理端修改后立即生效。
func (m *Mailer) Config() (SMTPConfig, error) {
	c := SMTPConfig{
		Host:       strings.TrimSpace(m.settings.String("email_host")),
		Port:       int(m.settings.Int("email_port")),
		Encryption: strings.ToLower(strings.TrimSpace(m.settings.String("email_encryption"))),
		Username:   m.settings.String("email_username"),
		Password:   m.settings.String("email_password"),
		From:       strings.TrimSpace(m.settings.String("email_from_address")),
		FromName:   m.settings.String("app_name"),
	}
	if c.Host == "" || c.From == "" {
		return c, ErrNotConfigured
	}
	if c.Port == 0 {
		switch c.Encryption {
		case "ssl":
			c.Port = 465
		case "tls":
			c.Port = 587
		default:
			c.Port = 25
		}
	}
	return c, nil
}

// Send 渲染并发送邮件。
func (m *Mailer) Send(ctx context.Context, to, subject, theme, name string, vars map[string]any) error {
	body, err := m.Render(theme, name, vars)
	if err != nil {
		return err
	}
	c, err := m.Config()
	if err != nil {
		return err
	}
	if m.Hooks != nil {
		v := hook.Apply(m.Hooks, ctx, hook.FilterMailMessage, hook.MailMessage{To: to, Subject: subject, Template: name, HTML: body})
		subject, body = v.Subject, v.HTML
	}
	return m.send(ctx, c, to, subject, body)
}

// SMTPTransport 通过 SMTP 发送。
func SMTPTransport(ctx context.Context, c SMTPConfig, to, subject, htmlBody string) error {
	msg := gomail.NewMsg()
	if err := msg.FromFormat(c.FromName, c.From); err != nil {
		return fmt.Errorf("发件地址无效: %w", err)
	}
	if err := msg.To(to); err != nil {
		return fmt.Errorf("收件地址无效: %w", err)
	}
	msg.Subject(subject)
	msg.SetBodyString(gomail.TypeTextHTML, htmlBody)

	opts := []gomail.Option{gomail.WithPort(c.Port), gomail.WithTimeout(10 * time.Second)}
	switch c.Encryption {
	case "ssl":
		opts = append(opts, gomail.WithSSL())
	case "tls":
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSMandatory))
	default:
		opts = append(opts, gomail.WithTLSPolicy(gomail.TLSOpportunistic))
	}
	if c.Username != "" {
		opts = append(opts, gomail.WithSMTPAuth(gomail.SMTPAuthAutoDiscover),
			gomail.WithUsername(c.Username), gomail.WithPassword(c.Password))
	}
	client, err := gomail.NewClient(c.Host, opts...)
	if err != nil {
		return err
	}
	return client.DialAndSendWithContext(ctx, msg)
}

// nl2br 与 PHP nl2br 一致，在换行前插入 <br />。普通字符串先做 HTML 转义；
// 只有调用方明确标记为可信 HTML（template.HTML）的内容才原样输出。
func nl2br(v any) template.HTML {
	var s string
	switch x := v.(type) {
	case template.HTML:
		s = string(x)
	case nil:
		return ""
	default:
		s = html.EscapeString(fmt.Sprint(x))
	}
	r := strings.NewReplacer("\r\n", "<br />\r\n", "\n\r", "<br />\n\r", "\n", "<br />\n", "\r", "<br />\r")
	return template.HTML(r.Replace(s))
}
