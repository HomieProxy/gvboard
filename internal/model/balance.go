package model

// BalanceLog 余额变动日志（v2_balance_log，Gvboard 新增，决策 D86）。
type BalanceLog struct {
	ID     int64 `gorm:"column:id;primaryKey" json:"id"`
	UserID int64 `gorm:"column:user_id" json:"user_id"`
	// Amount 变动金额（分），正数增加、负数减少；Balance 为变动后的余额。
	Amount     int64  `gorm:"column:amount" json:"amount"`
	Balance    int64  `gorm:"column:balance" json:"balance"`
	Type       string `gorm:"column:type" json:"type"`
	TradeNo    string `gorm:"column:trade_no" json:"trade_no"`
	OperatorID *int64 `gorm:"column:operator_id" json:"operator_id"`
	Plugin     string `gorm:"column:plugin" json:"plugin"`
	Remark     string `gorm:"column:remark" json:"remark"`
	CreatedAt  int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
}

func (BalanceLog) TableName() string { return "v2_balance_log" }
