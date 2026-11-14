// Package sysconfig 管理端系统配置（对应原项目 Admin/ConfigController，决策 D30、D53）。
package sysconfig

import (
	"context"
	"fmt"
	"net/url"
	"regexp"
	"slices"
	"strings"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/httpx"
	"gvboard/internal/mail"
	"gvboard/internal/model"
	"gvboard/internal/setting"
)

var (
	errUnknownGroup = httpx.Validation("配置分组不存在")
	errEmpty        = httpx.Validation("没有需要保存的配置")
)

// Service 系统配置服务。
type Service struct {
	DB       *gorm.DB
	Settings *setting.Service
	Mailer   *mail.Mailer
	Log      *zap.Logger
	// ThemeExists 检查用户端主题是否存在，为 nil 时不检查 frontend_theme。
	ThemeExists func(name string) bool
}

func (s *Service) log() *zap.Logger {
	if s.Log == nil {
		return zap.NewNop()
	}
	return s.Log
}

// Fetch 按分组返回配置（对应原项目 admin/config/fetch）。group 为空时返回全部分组。
// 敏感项与原项目一致明文返回（决策 D53）；原项目已不在管理端展示的遗留项不返回。
func (s *Service) Fetch(group string) (map[string]map[string]any, error) {
	out := map[string]map[string]any{}
	all := s.Settings.All()
	for _, d := range setting.Defs() {
		if d.Group == setting.GroupLegacy || (group != "" && d.Group != group) {
			continue
		}
		if out[d.Group] == nil {
			out[d.Group] = map[string]any{}
		}
		out[d.Group][d.Key] = all[d.Key]
	}
	if group != "" && len(out) == 0 {
		return nil, errUnknownGroup
	}
	return out, nil
}

// Save 保存配置（对应原项目 admin/config/save），只修改提交的项，任一项校验失败时全部不保存。
// 除设置项本身的类型与取值校验外，另按原项目 ConfigSave 规则校验（只在管理端保存时检查，
// 不影响读取原站已有的值）。
func (s *Service) Save(ctx context.Context, values map[string]any) error {
	if len(values) == 0 {
		return errEmpty
	}
	clean := make(map[string]any, len(values))
	for k, v := range values {
		d, ok := setting.Lookup(k)
		if !ok || d.Group == setting.GroupLegacy {
			return httpx.Validation(fmt.Sprintf("未知配置项：%s", k))
		}
		cv, err := setting.Coerce(d, v)
		if err != nil {
			return httpx.Validation(err.Error())
		}
		if check, ok := saveRules[k]; ok && cv != nil {
			if err := check(s, cv); err != nil {
				return httpx.Validation(fmt.Sprintf("设置项 %s: %s", k, err))
			}
		}
		clean[k] = cv
	}
	return s.Settings.Set(ctx, clean)
}

func httpURL(_ *Service, v any) error {
	str, _ := v.(string)
	if str == "" {
		return nil
	}
	u, err := url.Parse(str)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("必须是 http:// 或 https:// 开头的地址")
	}
	return nil
}

func intIn(lo, hi int64) func(*Service, any) error {
	return func(_ *Service, v any) error {
		if n, _ := v.(int64); n < lo || n > hi {
			return fmt.Errorf("取值范围为 %d～%d", lo, hi)
		}
		return nil
	}
}

func numIn(lo, hi float64) func(*Service, any) error {
	return func(_ *Service, v any) error {
		if f, _ := v.(float64); f < lo || f > hi {
			return fmt.Errorf("取值范围为 %g～%g", lo, hi)
		}
		return nil
	}
}

func strIn(opts ...string) func(*Service, any) error {
	return func(_ *Service, v any) error {
		if s, _ := v.(string); !slices.Contains(opts, s) {
			return fmt.Errorf("取值只能是 %v", opts)
		}
		return nil
	}
}

var depositTier = regexp.MustCompile(`^\d+(\.\d+)?:\d+(\.\d+)?$`)

// saveRules 管理端保存时的额外校验，与原项目 ConfigSave 一致，另加若干范围检查。
var saveRules = map[string]func(*Service, any) error{
	"logo":                       httpURL,
	"app_url":                    httpURL,
	"tos_url":                    httpURL,
	"frontend_background_url":    httpURL,
	"telegram_discuss_link":      httpURL,
	"ticket_status":              intIn(0, 2),
	"reset_traffic_method":       intIn(0, 4),
	"show_subscribe_method":      intIn(0, 2),
	"device_limit_mode":          intIn(0, 1),
	"new_order_event_id":         intIn(0, 1),
	"renew_order_event_id":       intIn(0, 1),
	"change_order_event_id":      intIn(0, 1),
	"invite_commission":          intIn(0, 100),
	"invite_gen_limit":           intIn(0, 1000),
	"subscription_limit":         intIn(1, 1000),
	"email_port":                 intIn(0, 65535),
	"server_pull_interval":       intIn(1, 86400),
	"server_push_interval":       intIn(1, 86400),
	"try_out_hour":               numIn(0, 24*3650),
	"commission_withdraw_limit":  numIn(0, 1e9),
	"commission_distribution_l1": numIn(0, 100),
	"commission_distribution_l2": numIn(0, 100),
	"commission_distribution_l3": numIn(0, 100),
	"frontend_theme_sidebar":     strIn("", "dark", "light"),
	"frontend_theme_header":      strIn("", "dark", "light"),
	"frontend_theme_color":       strIn("", "default", "darkblue", "black", "green"),
	"email_encryption": func(_ *Service, v any) error {
		// 与邮件发送一致，不区分大小写。
		if s, _ := v.(string); !slices.Contains([]string{"", "ssl", "tls"}, strings.ToLower(strings.TrimSpace(s))) {
			return fmt.Errorf("取值只能是 ssl、tls 或留空")
		}
		return nil
	},
	"server_token": func(_ *Service, v any) error {
		if s, _ := v.(string); s != "" && len(s) < 16 {
			return fmt.Errorf("通讯密钥长度不能少于 16 位")
		}
		return nil
	},
	"deposit_bounus": func(_ *Service, v any) error {
		tiers, _ := v.([]string)
		for _, tier := range tiers {
			if tier != "" && !depositTier.MatchString(tier) {
				return fmt.Errorf("充值奖励格式不正确，必须为 充值金额:奖励金额")
			}
		}
		return nil
	},
	"frontend_theme": func(s *Service, v any) error {
		// 保持原值不变时不检查（原站的主题名可能不存在，旧管理端保存时会整组提交）。
		t, _ := v.(string)
		if s.ThemeExists != nil && t != s.Settings.String("frontend_theme") && !s.ThemeExists(t) {
			return fmt.Errorf("主题不存在")
		}
		return nil
	},
	// 邮件模板只能选择内置模板（决策 D53）。
	"email_template": func(s *Service, v any) error {
		if t, _ := v.(string); !slices.Contains(s.EmailTemplates(), t) {
			return fmt.Errorf("邮件模板不存在")
		}
		return nil
	},
}

// EmailTemplates 可选的邮件模板（对应原项目 admin/config/getEmailTemplate）。
func (s *Service) EmailTemplates() []string { return s.Mailer.Themes() }

// TestMail 立即向 to 发送一封测试邮件并写入邮件日志，返回日志记录（对应原项目 admin/config/testSendMail）。
func (s *Service) TestMail(ctx context.Context, to, subject, content string) model.MailLog {
	return s.Mailer.Deliver(ctx, s.DB, s.log(), mail.Payload{
		Email: to, Subject: subject, TemplateName: mail.TplNotify,
		TemplateValue: map[string]any{
			"name": s.Settings.String("app_name"), "url": s.Settings.String("app_url"), "content": content,
		},
	})
}
