package model

// 订阅风控规则的匹配对象。
const (
	RuleTargetUA     = "ua"     // User-Agent
	RuleTargetHeader = "header" // 指定请求头
	RuleTargetIP     = "ip"     // 客户端 IP
	RuleTargetFlag   = "flag"   // flag 参数
	RuleTargetClient = "client" // 识别出的客户端格式
)

// 订阅风控规则的匹配方式。
const (
	RuleMatchContains = "contains"
	RuleMatchEquals   = "equals"
	RuleMatchPrefix   = "prefix"
	RuleMatchRegex    = "regex"
	RuleMatchEmpty    = "empty"
	RuleMatchCIDR     = "cidr"
)

// 订阅风控规则的动作。
const (
	RuleActionAllow = "allow" // 放行，跳过后续规则、频率限制与防分享
	RuleActionLog   = "log"   // 只记录命中，继续匹配
	RuleActionEmpty = "empty" // 返回 200 空内容
	RuleActionDeny  = "deny"  // 返回统一 404
)

// SubscribeRule 订阅下发风控规则（v2_subscribe_rule，Gvboard 新增，决策 D46）。
type SubscribeRule struct {
	ID        int64   `gorm:"column:id;primaryKey" json:"id"`
	Name      string  `gorm:"column:name" json:"name"`
	Target    string  `gorm:"column:target" json:"target"`
	Header    string  `gorm:"column:header" json:"header"`
	MatchType string  `gorm:"column:match_type" json:"match_type"`
	Pattern   *string `gorm:"column:pattern" json:"pattern"`
	Negate    int     `gorm:"column:negate" json:"negate"`
	Action    string  `gorm:"column:action" json:"action"`
	Priority  int64   `gorm:"column:priority" json:"priority"`
	Enabled   int     `gorm:"column:enabled" json:"enabled"`
	HitCount  int64   `gorm:"column:hit_count" json:"hit_count"`
	LastHitAt *int64  `gorm:"column:last_hit_at" json:"last_hit_at"`
	CreatedAt int64   `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt int64   `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (SubscribeRule) TableName() string { return "v2_subscribe_rule" }

// 订阅请求日志的结果。
const (
	SubscribeResultOK          = 0 // 正常下发
	SubscribeResultUnavailable = 1 // 订阅不可用（200 空内容）
	SubscribeResultRuleEmpty   = 2 // 规则拦截，返回 200 空内容
	SubscribeResultRuleDeny    = 3 // 规则拦截，返回 404
	SubscribeResultRateLimited = 4 // 超过频率限制
	SubscribeResultShareBlock  = 5 // 超过防分享 IP 数，已拦截
	SubscribeResultShareWarn   = 6 // 超过防分享 IP 数，仅记录（正常下发）
)

// SubscribeLog 订阅请求日志（v2_subscribe_log，Gvboard 新增，决策 D46）。
type SubscribeLog struct {
	ID             int64   `gorm:"column:id;primaryKey" json:"id"`
	SubscriptionID int64   `gorm:"column:subscription_id" json:"subscription_id"`
	UserID         int64   `gorm:"column:user_id" json:"user_id"`
	IP             string  `gorm:"column:ip" json:"ip"`
	UserAgent      string  `gorm:"column:user_agent" json:"user_agent"`
	Client         string  `gorm:"column:client" json:"client"`
	Headers        *string `gorm:"column:headers" json:"headers"`
	Result         int     `gorm:"column:result" json:"result"`
	RuleID         *int64  `gorm:"column:rule_id" json:"rule_id"`
	CreatedAt      int64   `gorm:"column:created_at" json:"created_at"`
	// Token 请求使用的 token 原值（永久 token、一次性 token 或时效 token）。
	Token *string `gorm:"column:token" json:"token"`
	// Path 请求路径，用于区分订阅路径、原订阅路径与 App 接口。
	Path *string `gorm:"column:path" json:"path"`
}

func (SubscribeLog) TableName() string { return "v2_subscribe_log" }
