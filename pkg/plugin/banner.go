package plugin

// Banner 用户端横幅，在 Setup 中通过 Host.Banner 注册，适合限时活动、重要通知。
// 显示位置由主题决定（默认主题在仪表盘公告下方）。
//
// 用户端请求 State 路由取得横幅状态（BannerState），按钮规则与卡片相同。
type Banner struct {
	// Key 插件内唯一的标识：小写字母开头，只能包含小写字母、数字、下划线，最多 32 个字符。
	Key string `json:"key"`
	// Title 标题，可以为空，最多 32 个字符。
	Title string `json:"title"`
	// Icon 图标，可用值同卡片图标，为空时使用 IconMegaphone。
	Icon string `json:"icon"`
	// State 返回 BannerState 的用户 GET 路由，如 "/banner"。
	State string `json:"state"`
}

// BannerState 横幅状态，由 State 路由以 plugin.OK(c, state) 返回。用户端按纯文本显示。
type BannerState struct {
	// Hidden 为 true 时不显示该横幅。
	Hidden bool `json:"hidden,omitempty"`
	// Text 文字，最多 200 个字符。
	Text string `json:"text,omitempty"`
	// Tone 颜色（ToneInfo / ToneSuccess / ToneWarning / ToneDanger），为空时为 ToneInfo。
	Tone string `json:"tone,omitempty"`
	// EndsAt 截止时间（Unix 秒），不为 0 时用户端显示倒计时，到期后隐藏。
	EndsAt int64 `json:"ends_at,omitempty"`
	// Action 按钮，可以为空。
	Action *WidgetAction `json:"action,omitempty"`
	// Dismissible 为 true 时用户可以关闭横幅（记录保存在浏览器中，Text 变化后重新显示）。
	Dismissible bool `json:"dismissible,omitempty"`
}
