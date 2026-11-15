package passport

import "gvboard/internal/setting"

// SiteConfig 前端需要的公开站点配置（对应原项目 guest/comm/config，字段为 Gvboard 自有命名）。
// 不分离部署时由页面渲染注入 HTML，分离部署时通过原生 API 提供（决策 D11）。
type SiteConfig struct {
	Site     SiteInfo     `json:"site"`
	Register RegisterInfo `json:"register"`
}

// SiteInfo 站点信息。
type SiteInfo struct {
	Name        string `json:"name"`
	Description string `json:"description"`
	URL         string `json:"url"`
	Logo        string `json:"logo"`
	TermsURL    string `json:"terms_url"`
	// Home 访客访问根路径时是否显示首页（关闭时直接进入登录页）。
	Home bool `json:"home"`
}

// RegisterInfo 注册页需要的选项。
type RegisterInfo struct {
	Closed         bool     `json:"closed"`
	EmailCode      bool     `json:"email_code"`
	InviteRequired bool     `json:"invite_required"`
	EmailSuffixes  []string `json:"email_suffixes"` // 为空表示不限制邮箱后缀
	CaptchaKey     string   `json:"captcha_key"`    // 为空表示未开启人机验证
}

// BuildSiteConfig 根据当前设置生成公开站点配置。
func BuildSiteConfig(s *setting.Service) SiteConfig {
	cfg := SiteConfig{
		Site: SiteInfo{
			Name:        s.String("app_name"),
			Description: s.String("app_description"),
			URL:         s.String("app_url"),
			Logo:        s.String("logo"),
			TermsURL:    s.String("tos_url"),
			Home:        s.Bool("home_enable"),
		},
		Register: RegisterInfo{
			Closed:         s.Bool("stop_register"),
			EmailCode:      s.Bool("email_verify"),
			InviteRequired: s.Bool("invite_force"),
			EmailSuffixes:  []string{},
		},
	}
	if s.Bool("email_whitelist_enable") {
		cfg.Register.EmailSuffixes = s.Strings("email_whitelist_suffix")
	}
	if s.Bool("recaptcha_enable") {
		cfg.Register.CaptchaKey = s.String("recaptcha_site_key")
	}
	return cfg
}
