package model

// LuckyWheelLog 内置插件「幸运大转盘」抽奖记录（v2_lucky_wheel_log，Gvboard 新增，决策 D87）。
type LuckyWheelLog struct {
	ID             int64  `gorm:"column:id;primaryKey" json:"id"`
	UserID         int64  `gorm:"column:user_id" json:"user_id"`
	SubscriptionID int64  `gorm:"column:subscription_id" json:"subscription_id"`
	PrizeName      string `gorm:"column:prize_name" json:"prize_name"`
	// PrizeType balance / days / traffic / none；PrizeValue 单位分 / 天 / 字节。
	PrizeType  string `gorm:"column:prize_type" json:"prize_type"`
	PrizeValue int64  `gorm:"column:prize_value" json:"prize_value"`
	// CostType free / balance / traffic；Cost 单位分 / 字节。
	CostType  string `gorm:"column:cost_type" json:"cost_type"`
	Cost      int64  `gorm:"column:cost" json:"cost"`
	CreatedAt int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (LuckyWheelLog) TableName() string { return "v2_lucky_wheel_log" }
