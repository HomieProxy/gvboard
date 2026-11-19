package model

// NodeMachine 节点机（v2_node_machine，Gvboard 新增，决策 D83）：一台运行 gvboard 版 v2node 的服务器，
// 用一次性接入码换取本机专属凭据，节点配置由面板下发。
type NodeMachine struct {
	ID   int64  `gorm:"column:id;primaryKey" json:"id"`
	Name string `gorm:"column:name" json:"name"`
	// ServerIDs 运行的 v2node 节点；AuditIDs 其中开启审计的节点。
	ServerIDs IntList `gorm:"column:server_ids" json:"server_ids"`
	AuditIDs  IntList `gorm:"column:audit_ids" json:"audit_ids"`
	LogLevel  string  `gorm:"column:log_level" json:"log_level"`
	// TokenHash 凭据的 SHA-256（十六进制），未接入或已吊销时为空。
	TokenHash *string `gorm:"column:token_hash" json:"-"`
	// EnrollHash 接入码的 SHA-256，EnrollExpiresAt 之后或使用后失效。
	EnrollHash      *string `gorm:"column:enroll_hash" json:"-"`
	EnrollExpiresAt *int64  `gorm:"column:enroll_expires_at" json:"enroll_expires_at"`
	EnrolledAt      *int64  `gorm:"column:enrolled_at" json:"enrolled_at"`
	Hostname        string  `gorm:"column:hostname" json:"hostname"`
	Version         string  `gorm:"column:version" json:"version"`
	IP              string  `gorm:"column:ip" json:"ip"`
	LastSeenAt      *int64  `gorm:"column:last_seen_at" json:"last_seen_at"`
	CreatedAt       int64   `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt       int64   `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (NodeMachine) TableName() string { return "v2_node_machine" }
