package model

// Notice 公告（v2_notice）。
type Notice struct {
	ID        int64   `gorm:"column:id;primaryKey" json:"id"`
	Title     string  `gorm:"column:title" json:"title"`
	Content   string  `gorm:"column:content" json:"content"`
	Show      int     `gorm:"column:show" json:"show"`
	ImgURL    *string `gorm:"column:img_url" json:"img_url"`
	Tags      StrList `gorm:"column:tags" json:"tags"`
	CreatedAt int64   `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt int64   `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Notice) TableName() string { return "v2_notice" }

// Knowledge 知识库（v2_knowledge）。
type Knowledge struct {
	ID        int64  `gorm:"column:id;primaryKey" json:"id"`
	Language  string `gorm:"column:language" json:"language"`
	Category  string `gorm:"column:category" json:"category"`
	Title     string `gorm:"column:title" json:"title"`
	Body      string `gorm:"column:body" json:"body"`
	Sort      *int64 `gorm:"column:sort" json:"sort"`
	Show      int    `gorm:"column:show" json:"show"`
	CreatedAt int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Knowledge) TableName() string { return "v2_knowledge" }

// 工单状态
const (
	TicketStatusOpen   = 0
	TicketStatusClosed = 1
)

// 工单回复状态
const (
	TicketReplyPending = 0 // 待回复（最后一条为用户消息）
	TicketReplied      = 1 // 已回复（最后一条为管理员消息）
)

// Ticket 工单（v2_ticket）。level：0 低，1 中，2 高。
type Ticket struct {
	ID             int64  `gorm:"column:id;primaryKey" json:"id"`
	UserID         int64  `gorm:"column:user_id" json:"user_id"`
	Subject        string `gorm:"column:subject" json:"subject"`
	Level          int    `gorm:"column:level" json:"level"`
	Status         int    `gorm:"column:status" json:"status"`
	ReplyStatus    int    `gorm:"column:reply_status" json:"reply_status"`
	SubscriptionID *int64 `gorm:"column:subscription_id" json:"subscription_id"`
	CreatedAt      int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt      int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Ticket) TableName() string { return "v2_ticket" }

// TicketMessage 工单消息（v2_ticket_message）。
type TicketMessage struct {
	ID        int64  `gorm:"column:id;primaryKey" json:"id"`
	UserID    int64  `gorm:"column:user_id" json:"user_id"`
	TicketID  int64  `gorm:"column:ticket_id" json:"ticket_id"`
	Message   string `gorm:"column:message" json:"message"`
	CreatedAt int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (TicketMessage) TableName() string { return "v2_ticket_message" }

// Log 系统日志（v2_log）。
type Log struct {
	ID        int64   `gorm:"column:id;primaryKey" json:"id"`
	Title     string  `gorm:"column:title" json:"title"`
	Level     *string `gorm:"column:level" json:"level"`
	Host      *string `gorm:"column:host" json:"host"`
	URI       string  `gorm:"column:uri" json:"uri"`
	Method    string  `gorm:"column:method" json:"method"`
	Data      *string `gorm:"column:data" json:"data"`
	IP        *string `gorm:"column:ip" json:"ip"`
	Context   *string `gorm:"column:context" json:"context"`
	CreatedAt int64   `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt int64   `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Log) TableName() string { return "v2_log" }

// MailLog 邮件日志（v2_mail_log）。
type MailLog struct {
	ID           int64   `gorm:"column:id;primaryKey" json:"id"`
	Email        string  `gorm:"column:email" json:"email"`
	Subject      string  `gorm:"column:subject" json:"subject"`
	TemplateName string  `gorm:"column:template_name" json:"template_name"`
	Error        *string `gorm:"column:error" json:"error"`
	CreatedAt    int64   `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt    int64   `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (MailLog) TableName() string { return "v2_mail_log" }

// Stat 每日 / 每月站点统计（v2_stat）。
type Stat struct {
	ID                int64  `gorm:"column:id;primaryKey" json:"id"`
	RecordAt          int64  `gorm:"column:record_at" json:"record_at"`
	RecordType        string `gorm:"column:record_type" json:"record_type"`
	OrderCount        int64  `gorm:"column:order_count" json:"order_count"`
	OrderTotal        int64  `gorm:"column:order_total" json:"order_total"`
	CommissionCount   int64  `gorm:"column:commission_count" json:"commission_count"`
	CommissionTotal   int64  `gorm:"column:commission_total" json:"commission_total"`
	PaidCount         int64  `gorm:"column:paid_count" json:"paid_count"`
	PaidTotal         int64  `gorm:"column:paid_total" json:"paid_total"`
	RegisterCount     int64  `gorm:"column:register_count" json:"register_count"`
	InviteCount       int64  `gorm:"column:invite_count" json:"invite_count"`
	TransferUsedTotal string `gorm:"column:transfer_used_total" json:"transfer_used_total"`
	CreatedAt         int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt         int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Stat) TableName() string { return "v2_stat" }

// StatServer 节点流量统计（v2_stat_server）。
type StatServer struct {
	ID         int64  `gorm:"column:id;primaryKey" json:"id"`
	ServerID   int64  `gorm:"column:server_id" json:"server_id"`
	ServerType string `gorm:"column:server_type" json:"server_type"`
	U          int64  `gorm:"column:u" json:"u"`
	D          int64  `gorm:"column:d" json:"d"`
	RecordType string `gorm:"column:record_type" json:"record_type"`
	RecordAt   int64  `gorm:"column:record_at" json:"record_at"`
	CreatedAt  int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt  int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (StatServer) TableName() string { return "v2_stat_server" }

// StatUser 用户流量统计（v2_stat_user），按订阅、倍率、日期汇总。
type StatUser struct {
	ID         int64   `gorm:"column:id;primaryKey" json:"id"`
	UserID     int64   `gorm:"column:user_id" json:"user_id"`
	ServerRate float64 `gorm:"column:server_rate" json:"server_rate"`
	U          int64   `gorm:"column:u" json:"u"`
	D          int64   `gorm:"column:d" json:"d"`
	RecordType string  `gorm:"column:record_type" json:"record_type"`
	RecordAt   int64   `gorm:"column:record_at" json:"record_at"`
	// SubscriptionID 所属订阅（Gvboard 新增）。迁移前的历史记录回填为原用户 ID（即其主订阅 ID）。
	SubscriptionID *int64 `gorm:"column:subscription_id" json:"subscription_id"`
	CreatedAt      int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt      int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (StatUser) TableName() string { return "v2_stat_user" }
