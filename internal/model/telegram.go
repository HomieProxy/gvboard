package model

// UserTelegram 用户的 Telegram 机器人偏好（v2_user_telegram，Gvboard 新增，决策 D51）。
// 绑定关系仍保存在 v2_user.telegram_id，与原项目一致。
type UserTelegram struct {
	ID           int64   `gorm:"column:id;primaryKey" json:"-"`
	UserID       int64   `gorm:"column:user_id" json:"-"`
	Language     *string `gorm:"column:language" json:"language"`
	NotifyOrder  int     `gorm:"column:notify_order" json:"notify_order"`
	NotifyTicket int     `gorm:"column:notify_ticket" json:"notify_ticket"`
	NotifyRemind int     `gorm:"column:notify_remind" json:"notify_remind"`
	CreatedAt    int64   `gorm:"column:created_at;autoCreateTime" json:"-"`
	UpdatedAt    int64   `gorm:"column:updated_at;autoUpdateTime" json:"-"`
}

func (UserTelegram) TableName() string { return "v2_user_telegram" }
