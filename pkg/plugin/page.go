package plugin

// 用户端导航分组。
const (
	NavSubscribe = "subscribe" // 订阅
	NavFinance   = "finance"   // 财务
	NavMore      = "more"      // 更多（默认）
	NavUser      = "user"      // 用户
)

// Page 用户端页面，在 Setup 中通过 Host.Page 注册。插件启用时出现在用户端侧边导航中，
// 地址为 /plugin/{Code}/{Key}；停用时导航中不显示，打开该地址提示页面不存在。
//
// 用户端打开页面时以 GET 请求 Content 路由取得页面内容（PageContent），按区块渲染为与主题一致的样式，
// 插件不能在浏览器中执行自己的脚本。主题可以为某个插件页面提供专门的界面（如内置的幸运大转盘），
// 此时不请求 Content，Content 仍需提供，供未适配的主题显示。
type Page struct {
	// Key 插件内唯一的标识：小写字母开头，只能包含小写字母、数字、下划线，最多 32 个字符。
	Key string `json:"key"`
	// Title 导航与页面标题，最多 24 个字符（Titles 中的标题同样）。
	Title string `json:"title"`
	// Titles 其它语言的标题（如 {"en-US": "Lucky Wheel"}），用户端按当前语言选择，没有时使用 Title。
	Titles map[string]string `json:"titles,omitempty"`
	// Icon 图标，可用值同卡片图标，为空时使用 IconPuzzle。
	Icon string `json:"icon"`
	// Group 导航分组（NavSubscribe / NavFinance / NavMore / NavUser），为空时为 NavMore。
	Group string `json:"group"`
	// Content 返回 PageContent 的用户 GET 路由，如 "/page"。
	Content string `json:"content"`
}

// 页面区块类型。
const (
	BlockMarkdown = "markdown" // Text 为 Markdown（不支持 HTML 与图片）
	BlockAlert    = "alert"    // Text 为纯文本提示，Tone 为颜色
	BlockFields   = "fields"   // Fields 键值列表，可设置一键复制、默认隐藏
	BlockTable    = "table"    // Columns 表头与 Rows 单元格（纯文本）
	BlockActions  = "actions"  // Actions 按钮，行为同卡片按钮，成功后刷新页面
)

// 提示区块颜色。
const (
	ToneInfo    = "info"
	ToneSuccess = "success"
	ToneWarning = "warning"
	ToneDanger  = "danger"
)

// PageContent 页面内容，由 Content 路由以 plugin.OK(c, content) 返回。
// 用户端最多显示 20 个区块，超出长度的文字会被截断。
type PageContent struct {
	Blocks []Block `json:"blocks"`
}

// Block 页面区块，按 Type 使用对应字段。Title 为区块标题（可选，最多 32 个字符）。
type Block struct {
	Type  string `json:"type"`
	Title string `json:"title,omitempty"`
	// Text markdown 区块最多 20000 个字符，alert 区块最多 500 个字符。
	Text string `json:"text,omitempty"`
	Tone string `json:"tone,omitempty"`
	// Fields 最多 30 项。
	Fields []PageField `json:"fields,omitempty"`
	// Columns 最多 10 列，Rows 最多 200 行，单元格最多 200 个字符。
	Columns []string   `json:"columns,omitempty"`
	Rows    [][]string `json:"rows,omitempty"`
	// Actions 最多 4 个。
	Actions []WidgetAction `json:"actions,omitempty"`
}

// PageField 键值项。Copy 为 true 时显示复制按钮；Secret 为 true 时默认隐藏值，点击后显示。
type PageField struct {
	Label  string `json:"label"`
	Value  string `json:"value"`
	Copy   bool   `json:"copy,omitempty"`
	Secret bool   `json:"secret,omitempty"`
}
