package plugin

// 卡片位置。
const (
	SlotDashboard = "dashboard" // 用户端仪表盘
)

// 卡片图标（用户端只显示这些图标，其它值按 IconPuzzle 显示）。
const (
	IconPuzzle        = "puzzle"
	IconGift          = "gift"
	IconCalendarCheck = "calendar-check"
	IconStar          = "star"
	IconTrophy        = "trophy"
	IconTicket        = "ticket"
	IconSparkles      = "sparkles"
	IconCoins         = "coins"
	IconMegaphone     = "megaphone"
	IconFerrisWheel   = "ferris-wheel"
	IconKey           = "key"
	IconUsers         = "users"
	IconBook          = "book"
	IconShield        = "shield"
)

// Widget 用户端卡片，在 Setup 中通过 Host.Widget 注册。
//
// 用户端先请求 State 路由取得卡片状态（WidgetState），用户点击按钮时以 POST 请求按钮的 Path，
// 两者都是本插件的用户路由（UserRoutes），路径相对于 {API 前缀}/plugin/{Code}/。
type Widget struct {
	// Key 插件内唯一的标识：小写字母开头，只能包含小写字母、数字、下划线，最多 32 个字符。
	Key string `json:"key"`
	// Slot 位置，目前只有 SlotDashboard。
	Slot string `json:"slot"`
	// Title 标题，最多 32 个字符。
	Title string `json:"title"`
	// Icon 图标，为空时使用 IconPuzzle。
	Icon string `json:"icon"`
	// State 返回 WidgetState 的用户 GET 路由，如 "/widget"。
	State string `json:"state"`
}

// WidgetState 卡片状态，由 State 路由以 plugin.OK(c, state) 返回。用户端按纯文本显示。
type WidgetState struct {
	// Hidden 为 true 时不显示该卡片（例如活动未开始）。
	Hidden bool `json:"hidden,omitempty"`
	// Text 说明文字，最多 200 个字符。
	Text string `json:"text,omitempty"`
	// Value 醒目显示的数值，如 "+0.05"，最多 32 个字符。
	Value string `json:"value,omitempty"`
	// Actions 按钮，最多 2 个。
	Actions []WidgetAction `json:"actions,omitempty"`
}

// WidgetAction 卡片按钮。点击后以 POST 请求本插件的用户路由 Path，
// 成功时若返回的数据中有 message 字段则作为提示显示，之后刷新卡片。
// Link 不为空时改为跳转到用户端页面（如 "/plans"），不请求 Path。
type WidgetAction struct {
	Label    string `json:"label"`
	Path     string `json:"path,omitempty"`
	Link     string `json:"link,omitempty"`
	Disabled bool   `json:"disabled,omitempty"`
	// Confirm 不为空时点击后先弹出确认框。
	Confirm string `json:"confirm,omitempty"`
}
