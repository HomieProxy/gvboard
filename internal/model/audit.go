package model

// 审计规则的动作（决策 D81），按严重程度从低到高。
const (
	AuditActionLog   = "log"   // 只记录
	AuditActionBlock = "block" // 阻断连接
	AuditActionBan   = "ban"   // 阻断并封禁账号
)

// AuditRule 审计规则（v2_audit_rule，Gvboard 新增，决策 D81）。
type AuditRule struct {
	ID      int64   `gorm:"column:id;primaryKey" json:"id"`
	Name    string  `gorm:"column:name" json:"name"`
	Pattern string  `gorm:"column:pattern" json:"pattern"`
	Action  string  `gorm:"column:action" json:"action"`
	GroupID IntList `gorm:"column:group_id" json:"group_id"`
	Enabled int     `gorm:"column:enabled" json:"enabled"`
	// Sort 管理端显示顺序。
	Sort      int64  `gorm:"column:sort" json:"sort"`
	HitCount  int64  `gorm:"column:hit_count" json:"hit_count"`
	LastHitAt *int64 `gorm:"column:last_hit_at" json:"last_hit_at"`
	CreatedAt int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (AuditRule) TableName() string { return "v2_audit_rule" }

// AuditLog 审计日志汇总（v2_audit_log，Gvboard 新增，决策 D81）。
// 同一订阅、规则、节点、自然日（服务器本地时区）合并为一条；具体目标见 AuditLogDetail。
type AuditLog struct {
	ID             int64  `gorm:"column:id;primaryKey" json:"id"`
	SubscriptionID int64  `gorm:"column:subscription_id" json:"subscription_id"`
	UserID         int64  `gorm:"column:user_id" json:"user_id"`
	ServerType     string `gorm:"column:server_type" json:"server_type"`
	ServerID       int64  `gorm:"column:server_id" json:"server_id"`
	RuleID         int64  `gorm:"column:rule_id" json:"rule_id"`
	Action         string `gorm:"column:action" json:"action"`
	// Target / Network / IP 取该日命中次数最多的细节；并列时取最近命中的。
	Target  string `gorm:"column:target" json:"target"`
	Network string `gorm:"column:network" json:"network"`
	IP      string `gorm:"column:ip" json:"ip"`
	Count   int64  `gorm:"column:count" json:"count"`
	// DetailCount 细节条数（不同目标数）。
	DetailCount int64 `gorm:"column:detail_count" json:"detail_count"`
	// Day 该汇总所属自然日的零点 Unix 秒（服务器本地时区）。
	Day int64 `gorm:"column:day" json:"day"`
	// Banned 本条记录是否导致账号被封禁。
	Banned    int   `gorm:"column:banned" json:"banned"`
	HitAt     int64 `gorm:"column:hit_at" json:"hit_at"`
	CreatedAt int64 `gorm:"column:created_at" json:"created_at"`
}

func (AuditLog) TableName() string { return "v2_audit_log" }

// AuditLogDetail 审计日志细节（v2_audit_log_detail）：同一汇总下的具体访问目标。
type AuditLogDetail struct {
	ID      int64  `gorm:"column:id;primaryKey" json:"id"`
	LogID   int64  `gorm:"column:log_id" json:"log_id"`
	Target  string `gorm:"column:target" json:"target"`
	Network string `gorm:"column:network" json:"network"`
	IP      string `gorm:"column:ip" json:"ip"`
	Count   int64  `gorm:"column:count" json:"count"`
	HitAt   int64  `gorm:"column:hit_at" json:"hit_at"`
}

func (AuditLogDetail) TableName() string { return "v2_audit_log_detail" }
