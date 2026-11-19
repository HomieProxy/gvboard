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

// AuditLog 审计日志（v2_audit_log，Gvboard 新增，决策 D81）。Count 为节点在一个上报周期内合并的次数。
type AuditLog struct {
	ID             int64  `gorm:"column:id;primaryKey" json:"id"`
	SubscriptionID int64  `gorm:"column:subscription_id" json:"subscription_id"`
	UserID         int64  `gorm:"column:user_id" json:"user_id"`
	ServerType     string `gorm:"column:server_type" json:"server_type"`
	ServerID       int64  `gorm:"column:server_id" json:"server_id"`
	RuleID         int64  `gorm:"column:rule_id" json:"rule_id"`
	Action         string `gorm:"column:action" json:"action"`
	Target         string `gorm:"column:target" json:"target"`
	Network        string `gorm:"column:network" json:"network"`
	IP             string `gorm:"column:ip" json:"ip"`
	Count          int64  `gorm:"column:count" json:"count"`
	// Banned 本条记录是否导致账号被封禁。
	Banned    int   `gorm:"column:banned" json:"banned"`
	HitAt     int64 `gorm:"column:hit_at" json:"hit_at"`
	CreatedAt int64 `gorm:"column:created_at" json:"created_at"`
}

func (AuditLog) TableName() string { return "v2_audit_log" }
