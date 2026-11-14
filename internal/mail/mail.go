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
	"sync"
	"sync/atomic"
	"time"

	gomail "github.com/wneessen/go-mail"

	"gvboard/internal/hook"
	"gvboard/internal/setting"
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
	Name       string // 多个服务器时的名称，用于错误信息
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
	templates map[string]*template.Template // 内置模板，键为 "风格/模板名"
	sources   map[string]string             // 内置模板源码，键同上
	send      Transport
	// Hooks 插件过滤器（邮件内容），启动时设置，可为 nil。
	Hooks hook.Hooks

	mu     sync.Mutex
	custom *customSet
	rr     atomic.Uint64 // 轮询计数
}

// New 加载内置模板。
func New(s Settings) (*Mailer, error) {
	m := &Mailer{settings: s, templates: map[string]*template.Template{}, sources: map[string]string{}, send: SMTPTransport}
	err := fs.WalkDir(templateFS, "templates", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || path.Ext(p) != ".html" {
			return err
		}
		b, err := templateFS.ReadFile(p)
		if err != nil {
			return err
		}
		rel := strings.TrimSuffix(strings.TrimPrefix(p, "templates/"), ".html")
		t, err := parse(rel, string(b))
		if err != nil {
			return fmt.Errorf("邮件模板 %s 解析失败: %w", rel, err)
		}
		m.templates[rel] = t
		m.sources[rel] = string(b)
		return nil
	})
	return m, err
}

func parse(name, src string) (*template.Template, error) {
	return template.New(name).Funcs(template.FuncMap{"nl2br": nl2br}).Option("missingkey=zero").Parse(src)
}

// SetTransport 替换发送实现（测试用）。
func (m *Mailer) SetTransport(t Transport) { m.send = t }

// Themes 返回可用的模板风格（内置与自定义）。
func (m *Mailer) Themes() []string {
	seen := map[string]bool{}
	for k := range m.templates {
		seen[strings.SplitN(k, "/", 2)[0]] = true
	}
	for theme := range m.customTemplates().src {
		seen[theme] = true
	}
	out := make([]string, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Builtin 是否为内置风格。
func (m *Mailer) Builtin(theme string) bool {
	_, ok := m.templates[theme+"/"+TplVerify]
	return ok
}

// Theme 返回当前设置的模板风格，不存在时使用默认风格。
func (m *Mailer) Theme() string {
	theme := m.settings.String("email_template")
	if m.Builtin(theme) {
		return theme
	}
	if _, ok := m.customTemplates().src[theme]; ok {
		return theme
	}
	return DefaultTheme
}

// Render 渲染模板：依次使用该风格的自定义内容、内置内容、默认风格的内置内容。
func (m *Mailer) Render(theme, name string, vars map[string]any) (string, error) {
	t, ok := m.customTemplates().tpls[theme+"/"+name]
	if !ok {
		t, ok = m.templates[theme+"/"+name]
	}
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

// ErrNoServer 指定的 SMTP 服务器不存在（指定服务器时不检查是否启用，便于测试停用的服务器）。
var ErrNoServer = errors.New("指定的 SMTP 服务器不存在")

func newConfig(name, host string, port int, enc, user, pass, from, fromName string) SMTPConfig {
	c := SMTPConfig{
		Name: name, Host: strings.TrimSpace(host), Port: port, Encryption: strings.ToLower(strings.TrimSpace(enc)),
		Username: user, Password: pass, From: strings.TrimSpace(from), FromName: fromName,
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
	return c
}

// Servers 从业务设置读取可用的 SMTP 服务器，按配置顺序排列。每次发送时读取，管理端修改后立即生效。
// 设置了多个 SMTP 服务器（email_smtp_servers，决策 D72）时只使用其中已启用的，否则使用原项目的单个配置。
func (m *Mailer) Servers() ([]SMTPConfig, error) {
	fromName := m.settings.String("app_name")
	list, err := setting.ParseSMTPServers(m.settings.String("email_smtp_servers"))
	if err != nil {
		return nil, err
	}
	if len(list) > 0 {
		out := []SMTPConfig{}
		for i, s := range list {
			if !s.Enable {
				continue
			}
			name := s.Name
			if name == "" {
				name = fmt.Sprintf("SMTP %d", i+1)
			}
			out = append(out, newConfig(name, s.Host, s.Port, s.Encryption, s.Username, s.Password, s.FromAddress, fromName))
		}
		if len(out) == 0 {
			return nil, ErrNotConfigured
		}
		return out, nil
	}
	c := newConfig("", m.settings.String("email_host"), int(m.settings.Int("email_port")), m.settings.String("email_encryption"),
		m.settings.String("email_username"), m.settings.String("email_password"), m.settings.String("email_from_address"), fromName)
	if c.Host == "" || c.From == "" {
		return nil, ErrNotConfigured
	}
	return []SMTPConfig{c}, nil
}

// Config 第一个可用的 SMTP 服务器。
func (m *Mailer) Config() (SMTPConfig, error) {
	list, err := m.Servers()
	if err != nil {
		return SMTPConfig{}, err
	}
	return list[0], nil
}

// order 本次发送尝试服务器的顺序：故障转移从第一个开始；轮询每次换一个起点。失败时都依次尝试后面的服务器。
func (m *Mailer) order(n int) []int {
	start := 0
	if n > 1 && m.settings.String("email_smtp_mode") == "round_robin" {
		start = int(m.rr.Add(1)-1) % n
	}
	out := make([]int, n)
	for i := range out {
		out[i] = (start + i) % n
	}
	return out
}

// Send 渲染并发送邮件。
func (m *Mailer) Send(ctx context.Context, to, subject, theme, name string, vars map[string]any) error {
	return m.SendVia(ctx, -1, to, subject, theme, name, vars)
}

// SendVia 同 Send；server 不小于 0 时只用 email_smtp_servers 中该序号的服务器（测试单个服务器用，不论是否启用）。
func (m *Mailer) SendVia(ctx context.Context, server int, to, subject, theme, name string, vars map[string]any) error {
	body, err := m.Render(theme, name, vars)
	if err != nil {
		return err
	}
	var servers []SMTPConfig
	if server >= 0 {
		list, err := setting.ParseSMTPServers(m.settings.String("email_smtp_servers"))
		if err != nil {
			return err
		}
		if server >= len(list) {
			return ErrNoServer
		}
		s := list[server]
		servers = []SMTPConfig{newConfig(s.Name, s.Host, s.Port, s.Encryption, s.Username, s.Password, s.FromAddress, m.settings.String("app_name"))}
	} else if servers, err = m.Servers(); err != nil {
		return err
	}
	if m.Hooks != nil {
		v := hook.Apply(m.Hooks, ctx, hook.FilterMailMessage, hook.MailMessage{To: to, Subject: subject, Template: name, HTML: body})
		subject, body = v.Subject, v.HTML
	}
	var errs []error
	for _, i := range m.order(len(servers)) {
		c := servers[i]
		err := m.send(ctx, c, to, subject, body)
		if err == nil {
			return nil
		}
		if len(servers) == 1 {
			return err
		}
		errs = append(errs, fmt.Errorf("%s: %w", c.Name, err))
		if ctx.Err() != nil {
			break
		}
	}
	return errors.Join(errs...)
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
