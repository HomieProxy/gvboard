// Package setting 管理存放在数据库中的业务设置（对应原项目 config/v2board.php）。
package setting

import (
	"fmt"
	"net/netip"
	"net/url"
	"regexp"
	"unicode/utf8"

	"gvboard/internal/phpx"
)

// Type 设置值类型。
type Type int

const (
	TypeString  Type = iota
	TypeInt          // 整数
	TypeNumber       // 小数
	TypeBool         // 开关，存储为 0/1，与原项目一致
	TypeStrings      // 字符串数组
)

// Def 设置项定义。
type Def struct {
	Key      string
	Type     Type
	Default  any
	Group    string
	Secret   bool // 敏感信息（密码、密钥），对外展示时需脱敏
	Validate func(v any) error
}

// 分组与原项目管理端系统配置一致，另加 gvboard 分组存放新增设置。
const (
	GroupSite      = "site"
	GroupSafe      = "safe"
	GroupSubscribe = "subscribe"
	GroupInvite    = "invite"
	GroupDeposit   = "deposit"
	GroupTicket    = "ticket"
	GroupFrontend  = "frontend"
	GroupServer    = "server"
	GroupEmail     = "email"
	GroupTelegram  = "telegram"
	GroupApp       = "app"
	GroupClient    = "client"
	GroupGvboard   = "gvboard"
	GroupLegacy    = "legacy"   // 原项目中存在但已不在管理端展示的项
	GroupInternal  = "internal" // 由专用接口维护，不在系统配置中读取与保存
)

// Editable 是否可通过系统配置读取与保存。
func (d Def) Editable() bool { return d.Group != GroupLegacy && d.Group != GroupInternal }

// 原项目默认值（Dict.php）
var (
	defaultEmailWhitelistSuffix = []string{"gmail.com", "qq.com", "163.com", "yahoo.com", "sina.com", "126.com", "outlook.com", "yeah.net", "foxmail.com"}
	defaultWithdrawMethods      = []string{"支付宝", "USDT", "Paypal"}
)

// SecurePathMinLen 管理端路径最小长度（决策 D11）。
const SecurePathMinLen = 16

var securePathPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)

func validateSecurePath(v any) error {
	s, _ := v.(string)
	if len(s) < SecurePathMinLen || !securePathPattern.MatchString(s) {
		return fmt.Errorf("管理端路径长度不能少于 %d 位，且只能包含字母、数字、下划线、短横线", SecurePathMinLen)
	}
	return nil
}

var subscribePathPattern = regexp.MustCompile(`^(/[A-Za-z0-9_-]+)+$`)

func validateSubscribePath(v any) error {
	s, _ := v.(string)
	if s == "" {
		return nil
	}
	if !subscribePathPattern.MatchString(s) {
		return fmt.Errorf("订阅路径必须以 / 开头，且只能包含字母、数字、下划线、短横线")
	}
	return nil
}

func validateYAMLTemplate(v any) error {
	s, _ := v.(string)
	if s == "" {
		return nil
	}
	if _, ok := phpx.ParseYAMLArr([]byte(s)); !ok {
		return fmt.Errorf("模板不是有效的 YAML 配置")
	}
	return nil
}

func validateJSONTemplate(v any) error {
	s, _ := v.(string)
	if s == "" {
		return nil
	}
	if _, ok := phpx.ParseJSONArr([]byte(s)); !ok {
		return fmt.Errorf("模板不是有效的 JSON 配置")
	}
	return nil
}

func intRange(lo, hi int64) func(any) error {
	return func(v any) error {
		if n, _ := v.(int64); n < lo || n > hi {
			return fmt.Errorf("取值范围为 %d～%d", lo, hi)
		}
		return nil
	}
}

func maxLen(n int) func(any) error {
	return func(v any) error {
		if s, _ := v.(string); utf8.RuneCountInString(s) > n {
			return fmt.Errorf("长度不能超过 %d 个字符", n)
		}
		return nil
	}
}

func oneOf(opts ...string) func(any) error {
	return func(v any) error {
		s, _ := v.(string)
		for _, o := range opts {
			if s == o {
				return nil
			}
		}
		return fmt.Errorf("取值只能是 %v", opts)
	}
}

// validateTelegramAPI Bot API 地址：http(s) 地址，不含查询串。
func validateTelegramAPI(v any) error {
	s, _ := v.(string)
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.RawQuery != "" || u.Fragment != "" {
		return fmt.Errorf("Bot API 地址必须是 http 或 https 地址")
	}
	return nil
}

// validateTelegramProxy 代理地址：为空不使用代理，支持 http、https、socks5。
func validateTelegramProxy(v any) error {
	s, _ := v.(string)
	if s == "" {
		return nil
	}
	u, err := url.Parse(s)
	if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5") {
		return fmt.Errorf("代理地址格式应为 http://、https:// 或 socks5://主机:端口")
	}
	return nil
}

var chatIDPattern = regexp.MustCompile(`^-?[0-9]{1,20}$`)

// validateChatID Telegram 群组 ID：为空或整数（超级群组为 -100 开头）。
func validateChatID(v any) error {
	if s, _ := v.(string); s != "" && !chatIDPattern.MatchString(s) {
		return fmt.Errorf("群组 ID 必须是数字，如 -1001234567890")
	}
	return nil
}

var headerNamePattern = regexp.MustCompile(`^[A-Za-z0-9-]{1,64}$`)

// validateLogHeaders 订阅日志额外记录的请求头：最多 10 个合法请求头名。
func validateLogHeaders(v any) error {
	list, _ := v.([]string)
	if len(list) > 10 {
		return fmt.Errorf("最多记录 10 个请求头")
	}
	for _, h := range list {
		if !headerNamePattern.MatchString(h) {
			return fmt.Errorf("请求头名称 %q 不合法", h)
		}
	}
	return nil
}

// validateProxyCIDRs 可信代理 IP 段：IP 或 CIDR，拒绝过大的范围（IPv4 大于 /8、IPv6 大于 /16），
// 否则任何人都能通过 X-Forwarded-For 伪造 IP。
func validateProxyCIDRs(v any) error {
	list, _ := v.([]string)
	if len(list) > 500 {
		return fmt.Errorf("最多填写 500 个 IP 段")
	}
	for _, s := range list {
		p, err := netip.ParsePrefix(s)
		if err != nil {
			a, err := netip.ParseAddr(s)
			if err != nil || a.Zone() != "" {
				return fmt.Errorf("%q 不是合法的 IP 或 IP 段", s)
			}
			continue
		}
		if p.Addr().Zone() != "" {
			return fmt.Errorf("%q 不是合法的 IP 或 IP 段", s)
		}
		bits, minBits := p.Bits(), 16
		if p.Addr().Is4() {
			minBits = 8
		} else if p.Addr().Is4In6() {
			bits, minBits = bits-96, 8
		}
		if bits < minBits {
			return fmt.Errorf("IP 段 %q 范围过大，IPv4 不能大于 /8，IPv6 不能大于 /16", s)
		}
	}
	return nil
}

var defs = []Def{
	// 站点
	{Key: "app_name", Type: TypeString, Default: "Gvboard", Group: GroupSite},
	{Key: "app_description", Type: TypeString, Default: "", Group: GroupSite},
	{Key: "app_url", Type: TypeString, Default: "", Group: GroupSite},
	{Key: "logo", Type: TypeString, Default: "", Group: GroupSite},
	{Key: "force_https", Type: TypeBool, Default: 0, Group: GroupSite},
	{Key: "stop_register", Type: TypeBool, Default: 0, Group: GroupSite},
	{Key: "home_enable", Type: TypeBool, Default: 1, Group: GroupSite},
	{Key: "subscribe_url", Type: TypeString, Default: "", Group: GroupSite},
	{Key: "subscribe_path", Type: TypeString, Default: "", Group: GroupSite, Validate: validateSubscribePath},
	{Key: "try_out_enable", Type: TypeBool, Default: 0, Group: GroupSite},
	{Key: "try_out_plan_id", Type: TypeInt, Default: 0, Group: GroupSite},
	{Key: "try_out_hour", Type: TypeNumber, Default: 1, Group: GroupSite},
	{Key: "tos_url", Type: TypeString, Default: "", Group: GroupSite},
	{Key: "currency", Type: TypeString, Default: "CNY", Group: GroupSite},
	{Key: "currency_symbol", Type: TypeString, Default: "¥", Group: GroupSite},

	// 订阅
	{Key: "plan_change_enable", Type: TypeBool, Default: 1, Group: GroupSubscribe},
	{Key: "reset_traffic_method", Type: TypeInt, Default: 0, Group: GroupSubscribe},
	{Key: "surplus_enable", Type: TypeBool, Default: 1, Group: GroupSubscribe},
	{Key: "allow_new_period", Type: TypeBool, Default: 0, Group: GroupSubscribe},
	{Key: "new_order_event_id", Type: TypeInt, Default: 0, Group: GroupSubscribe},
	{Key: "renew_order_event_id", Type: TypeInt, Default: 0, Group: GroupSubscribe},
	{Key: "change_order_event_id", Type: TypeInt, Default: 0, Group: GroupSubscribe},
	{Key: "show_info_to_server_enable", Type: TypeBool, Default: 0, Group: GroupSubscribe},
	{Key: "show_subscribe_method", Type: TypeInt, Default: 0, Group: GroupSubscribe},
	{Key: "show_subscribe_expire", Type: TypeInt, Default: 5, Group: GroupSubscribe},

	// 邀请与佣金
	{Key: "invite_force", Type: TypeBool, Default: 0, Group: GroupInvite},
	{Key: "invite_commission", Type: TypeInt, Default: 10, Group: GroupInvite},
	{Key: "invite_gen_limit", Type: TypeInt, Default: 5, Group: GroupInvite},
	{Key: "invite_never_expire", Type: TypeBool, Default: 0, Group: GroupInvite},
	{Key: "commission_first_time_enable", Type: TypeBool, Default: 1, Group: GroupInvite},
	{Key: "commission_auto_check_enable", Type: TypeBool, Default: 1, Group: GroupInvite},
	{Key: "commission_withdraw_limit", Type: TypeNumber, Default: 100, Group: GroupInvite},
	{Key: "commission_withdraw_method", Type: TypeStrings, Default: defaultWithdrawMethods, Group: GroupInvite},
	{Key: "withdraw_close_enable", Type: TypeBool, Default: 0, Group: GroupInvite},
	{Key: "commission_distribution_enable", Type: TypeBool, Default: 0, Group: GroupInvite},
	{Key: "commission_distribution_l1", Type: TypeNumber, Default: nil, Group: GroupInvite},
	{Key: "commission_distribution_l2", Type: TypeNumber, Default: nil, Group: GroupInvite},
	{Key: "commission_distribution_l3", Type: TypeNumber, Default: nil, Group: GroupInvite},

	// 充值
	{Key: "deposit_bounus", Type: TypeStrings, Default: []string{}, Group: GroupDeposit},

	// 工单
	{Key: "ticket_status", Type: TypeInt, Default: 0, Group: GroupTicket},

	// 前端
	{Key: "frontend_theme", Type: TypeString, Default: "default", Group: GroupFrontend},
	{Key: "frontend_theme_sidebar", Type: TypeString, Default: "light", Group: GroupFrontend},
	{Key: "frontend_theme_header", Type: TypeString, Default: "dark", Group: GroupFrontend},
	{Key: "frontend_theme_color", Type: TypeString, Default: "default", Group: GroupFrontend},
	{Key: "frontend_background_url", Type: TypeString, Default: "", Group: GroupFrontend},

	// 节点
	{Key: "server_api_url", Type: TypeString, Default: "", Group: GroupServer},
	{Key: "server_token", Type: TypeString, Default: "", Group: GroupServer, Secret: true},
	{Key: "server_pull_interval", Type: TypeInt, Default: 60, Group: GroupServer},
	{Key: "server_push_interval", Type: TypeInt, Default: 60, Group: GroupServer},
	{Key: "server_node_report_min_traffic", Type: TypeInt, Default: 0, Group: GroupServer},
	{Key: "server_device_online_min_traffic", Type: TypeInt, Default: 0, Group: GroupServer},
	{Key: "device_limit_mode", Type: TypeInt, Default: 0, Group: GroupServer},
	// Gvboard 新增（决策 D55）：节点掉线自动检查并通知管理员
	{Key: "server_offline_notify", Type: TypeBool, Default: 1, Group: GroupServer},
	// 节点机一次性接入（决策 D83），存在已接入的节点机时不能关闭。
	{Key: "node_machine_enable", Type: TypeBool, Default: 0, Group: GroupServer},

	// 邮件
	{Key: "email_template", Type: TypeString, Default: "default", Group: GroupEmail},
	{Key: "email_host", Type: TypeString, Default: "", Group: GroupEmail},
	{Key: "email_port", Type: TypeInt, Default: 0, Group: GroupEmail},
	{Key: "email_username", Type: TypeString, Default: "", Group: GroupEmail},
	{Key: "email_password", Type: TypeString, Default: "", Group: GroupEmail, Secret: true},
	{Key: "email_encryption", Type: TypeString, Default: "", Group: GroupEmail},
	{Key: "email_from_address", Type: TypeString, Default: "", Group: GroupEmail},
	// Gvboard 新增（决策 D72）：多个 SMTP 服务器（JSON 数组，非空时取代上面的单个配置）及使用方式。
	{Key: "email_smtp_servers", Type: TypeString, Default: "", Group: GroupEmail, Secret: true, Validate: validateSMTPServers},
	{Key: "email_smtp_mode", Type: TypeString, Default: "failover", Group: GroupEmail, Validate: oneOf("failover", "round_robin")},

	// Telegram
	{Key: "telegram_bot_enable", Type: TypeBool, Default: 0, Group: GroupTelegram},
	{Key: "telegram_bot_token", Type: TypeString, Default: "", Group: GroupTelegram, Secret: true},
	{Key: "telegram_discuss_link", Type: TypeString, Default: "", Group: GroupTelegram},
	{Key: "telegram_discuss_id", Type: TypeString, Default: "", Group: GroupTelegram},
	{Key: "telegram_channel_id", Type: TypeString, Default: "", Group: GroupTelegram},
	// Gvboard 新增（决策 D51）
	{Key: "telegram_mode", Type: TypeString, Default: "webhook", Group: GroupTelegram, Validate: oneOf("webhook", "polling")},
	{Key: "telegram_api_url", Type: TypeString, Default: "https://api.telegram.org", Group: GroupTelegram, Validate: validateTelegramAPI},
	{Key: "telegram_proxy", Type: TypeString, Default: "", Group: GroupTelegram, Secret: true, Validate: validateTelegramProxy},
	{Key: "telegram_webhook_secret", Type: TypeString, Default: "", Group: GroupTelegram, Secret: true},
	// 欢迎消息（Telegram HTML，变量 {app_name}），为空使用内置文案（决策 D52）。
	{Key: "telegram_welcome", Type: TypeString, Default: "", Group: GroupTelegram, Validate: maxLen(3000)},
	// 管理员通知发送到话题群组（决策 D72）：群组 ID 为空时仍私聊通知管理员；话题 ID 为 0 时发到群组的 General 话题。
	{Key: "telegram_admin_chat_id", Type: TypeString, Default: "", Group: GroupTelegram, Validate: validateChatID},
	{Key: "telegram_topic_order", Type: TypeInt, Default: 0, Group: GroupTelegram, Validate: intRange(0, 1<<31-1)},
	{Key: "telegram_topic_ticket", Type: TypeInt, Default: 0, Group: GroupTelegram, Validate: intRange(0, 1<<31-1)},
	{Key: "telegram_topic_alert", Type: TypeInt, Default: 0, Group: GroupTelegram, Validate: intRange(0, 1<<31-1)},
	{Key: "telegram_topic_daily", Type: TypeInt, Default: 0, Group: GroupTelegram, Validate: intRange(0, 1<<31-1)},
	{Key: "telegram_daily_report", Type: TypeBool, Default: 1, Group: GroupTelegram},

	// App
	{Key: "windows_version", Type: TypeString, Default: "", Group: GroupApp},
	{Key: "windows_download_url", Type: TypeString, Default: "", Group: GroupApp},
	{Key: "macos_version", Type: TypeString, Default: "", Group: GroupApp},
	{Key: "macos_download_url", Type: TypeString, Default: "", Group: GroupApp},
	{Key: "android_version", Type: TypeString, Default: "", Group: GroupApp},
	{Key: "android_download_url", Type: TypeString, Default: "", Group: GroupApp},

	// Gvboard 新增（决策 D67）：只允许使用客户端时用户端不显示订阅链接，改为展示客户端下载。
	{Key: "client_only_enable", Type: TypeBool, Default: 0, Group: GroupClient},
	{Key: "client_downloads", Type: TypeString, Default: "", Group: GroupClient, Validate: validateClients},

	// 安全
	{Key: "email_verify", Type: TypeBool, Default: 0, Group: GroupSafe},
	{Key: "safe_mode_enable", Type: TypeBool, Default: 0, Group: GroupSafe},
	{Key: "secure_path", Type: TypeString, Default: "", Group: GroupSafe, Validate: validateSecurePath},
	{Key: "email_whitelist_enable", Type: TypeBool, Default: 0, Group: GroupSafe},
	{Key: "email_whitelist_suffix", Type: TypeStrings, Default: defaultEmailWhitelistSuffix, Group: GroupSafe},
	{Key: "email_gmail_limit_enable", Type: TypeBool, Default: 0, Group: GroupSafe},
	{Key: "recaptcha_enable", Type: TypeBool, Default: 0, Group: GroupSafe},
	{Key: "recaptcha_key", Type: TypeString, Default: "", Group: GroupSafe, Secret: true},
	{Key: "recaptcha_site_key", Type: TypeString, Default: "", Group: GroupSafe},
	{Key: "register_limit_by_ip_enable", Type: TypeBool, Default: 0, Group: GroupSafe},
	{Key: "register_limit_count", Type: TypeInt, Default: 3, Group: GroupSafe},
	{Key: "register_limit_expire", Type: TypeInt, Default: 60, Group: GroupSafe},
	{Key: "password_limit_enable", Type: TypeBool, Default: 1, Group: GroupSafe},
	{Key: "password_limit_count", Type: TypeInt, Default: 5, Group: GroupSafe},
	{Key: "password_limit_expire", Type: TypeInt, Default: 60, Group: GroupSafe},
	{Key: "login_limit_by_ip_enable", Type: TypeBool, Default: 0, Group: GroupSafe},
	{Key: "login_limit_by_ip_count", Type: TypeInt, Default: 30, Group: GroupSafe, Validate: intRange(1, 100000)},
	{Key: "login_limit_by_ip_expire", Type: TypeInt, Default: 10, Group: GroupSafe, Validate: intRange(1, 1440)},
	{Key: "trusted_proxy_cidrs", Type: TypeStrings, Default: []string{}, Group: GroupSafe, Validate: validateProxyCIDRs},

	// Gvboard 新增
	{Key: "subscription_limit", Type: TypeInt, Default: 5, Group: GroupGvboard},
	// 订阅模板（对应原项目 resources/rules/custom.*），留空使用内置默认模板（决策 D44）。
	{Key: "subscribe_template_clash", Type: TypeString, Default: "", Group: GroupGvboard, Validate: validateYAMLTemplate},
	{Key: "subscribe_template_stash", Type: TypeString, Default: "", Group: GroupGvboard, Validate: validateYAMLTemplate},
	{Key: "subscribe_template_app_clash", Type: TypeString, Default: "", Group: GroupGvboard, Validate: validateYAMLTemplate},
	{Key: "subscribe_template_singbox", Type: TypeString, Default: "", Group: GroupGvboard, Validate: validateJSONTemplate},
	{Key: "subscribe_template_singbox_old", Type: TypeString, Default: "", Group: GroupGvboard, Validate: validateJSONTemplate},
	{Key: "subscribe_template_surge", Type: TypeString, Default: "", Group: GroupGvboard},
	{Key: "subscribe_template_surfboard", Type: TypeString, Default: "", Group: GroupGvboard},
	// 订阅下发风控与请求日志（决策 D46）。次数 / IP 数为 0 表示关闭对应限制。
	{Key: "subscribe_log_enable", Type: TypeBool, Default: 1, Group: GroupGvboard},
	{Key: "subscribe_log_retention_days", Type: TypeInt, Default: 30, Group: GroupGvboard, Validate: intRange(1, 180)},
	{Key: "subscribe_log_headers", Type: TypeStrings, Default: []string{}, Group: GroupGvboard, Validate: validateLogHeaders},
	{Key: "subscribe_rate_window", Type: TypeInt, Default: 3600, Group: GroupGvboard, Validate: intRange(60, 86400)},
	{Key: "subscribe_rate_sub_limit", Type: TypeInt, Default: 0, Group: GroupGvboard, Validate: intRange(0, 1000000)},
	{Key: "subscribe_rate_ip_limit", Type: TypeInt, Default: 0, Group: GroupGvboard, Validate: intRange(0, 1000000)},
	{Key: "subscribe_rate_action", Type: TypeString, Default: "empty", Group: GroupGvboard, Validate: oneOf("empty", "deny")},
	{Key: "subscribe_share_ip_limit", Type: TypeInt, Default: 0, Group: GroupGvboard, Validate: intRange(0, 10000)},
	{Key: "subscribe_share_window", Type: TypeInt, Default: 24, Group: GroupGvboard, Validate: intRange(1, 168)},
	{Key: "subscribe_share_action", Type: TypeString, Default: "log", Group: GroupGvboard, Validate: oneOf("log", "empty", "deny")},
	// 审计日志保留天数（决策 D81）。
	{Key: "audit_log_retention_days", Type: TypeInt, Default: 30, Group: GroupGvboard, Validate: intRange(1, 365)},
	// 余额变动日志保留天数（决策 D86）。
	{Key: "balance_log_retention_days", Type: TypeInt, Default: 365, Group: GroupGvboard, Validate: intRange(1, 3650)},

	// 自定义邮件模板，JSON：{"风格": {"模板名": "内容"}}，内置风格中的项覆盖内置内容（决策 D71）。
	{Key: "email_templates_custom", Type: TypeString, Default: "", Group: GroupInternal},
	// 管理员已确认过数据库访问警告的 Go 插件标识（决策 D86）。
	{Key: "plugin_db_warning_ack", Type: TypeStrings, Default: []string{}, Group: GroupInternal},
	// 管理员关闭的插件用户端显示形式，值为 "插件标识:widget|banner|page"（决策 D91）。
	{Key: "plugin_ui_off", Type: TypeStrings, Default: []string{}, Group: GroupInternal},

	// 原项目遗留项
	{Key: "stripe_pk_live", Type: TypeString, Default: "", Group: GroupLegacy},
	{Key: "server_log_enable", Type: TypeBool, Default: 0, Group: GroupLegacy},
}

var defIndex = func() map[string]Def {
	m := make(map[string]Def, len(defs))
	for _, d := range defs {
		if _, dup := m[d.Key]; dup {
			panic("重复的设置项定义: " + d.Key)
		}
		m[d.Key] = d
	}
	return m
}()

// Lookup 查找设置项定义。
func Lookup(key string) (Def, bool) {
	d, ok := defIndex[key]
	return d, ok
}

// Defs 返回全部设置项定义（只读）。
func Defs() []Def { return defs }
