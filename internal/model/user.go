package model

// User 用户（v2_user）。
// 多订阅改造后，套餐、流量、到期、uuid、token 等订阅数据以 Subscription 为准；
// 本表中的同名字段为原项目遗留，仅在迁移时读取，新用户注册时与主订阅保持一致以满足非空约束。
type User struct {
	ID                int64   `gorm:"column:id;primaryKey" json:"id"`
	InviteUserID      *int64  `gorm:"column:invite_user_id" json:"invite_user_id"`
	TelegramID        *int64  `gorm:"column:telegram_id" json:"telegram_id"`
	Email             string  `gorm:"column:email" json:"email"`
	Password          string  `gorm:"column:password" json:"-"`
	PasswordAlgo      *string `gorm:"column:password_algo" json:"-"`
	PasswordSalt      *string `gorm:"column:password_salt" json:"-"`
	Balance           int64   `gorm:"column:balance" json:"balance"`
	Discount          *int64  `gorm:"column:discount" json:"discount"`
	CommissionType    int     `gorm:"column:commission_type" json:"commission_type"`
	CommissionRate    *int64  `gorm:"column:commission_rate" json:"commission_rate"`
	CommissionBalance int64   `gorm:"column:commission_balance" json:"commission_balance"`
	T                 int64   `gorm:"column:t" json:"t"`
	U                 int64   `gorm:"column:u" json:"u"`
	D                 int64   `gorm:"column:d" json:"d"`
	TransferEnable    int64   `gorm:"column:transfer_enable" json:"transfer_enable"`
	DeviceLimit       *int64  `gorm:"column:device_limit" json:"device_limit"`
	Banned            int     `gorm:"column:banned" json:"banned"`
	IsAdmin           int     `gorm:"column:is_admin" json:"is_admin"`
	LastLoginAt       *int64  `gorm:"column:last_login_at" json:"last_login_at"`
	IsStaff           int     `gorm:"column:is_staff" json:"is_staff"`
	LastLoginIP       *int64  `gorm:"column:last_login_ip" json:"last_login_ip"`
	UUID              string  `gorm:"column:uuid" json:"uuid"`
	GroupID           *int64  `gorm:"column:group_id" json:"group_id"`
	PlanID            *int64  `gorm:"column:plan_id" json:"plan_id"`
	SpeedLimit        *int64  `gorm:"column:speed_limit" json:"speed_limit"`
	AutoRenewal       *int    `gorm:"column:auto_renewal" json:"auto_renewal"`
	RemindExpire      *int    `gorm:"column:remind_expire" json:"remind_expire"`
	RemindTraffic     *int    `gorm:"column:remind_traffic" json:"remind_traffic"`
	Token             string  `gorm:"column:token" json:"token"`
	ExpiredAt         *int64  `gorm:"column:expired_at" json:"expired_at"`
	Remarks           *string `gorm:"column:remarks" json:"remarks"`
	// SubscriptionLimit 单用户订阅数量上限（Gvboard 新增），为空使用全局设置。
	SubscriptionLimit *int64 `gorm:"column:subscription_limit" json:"subscription_limit"`
	CreatedAt         int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt         int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (User) TableName() string { return "v2_user" }

// Subscription 订阅（v2_subscription，Gvboard 新增）。
// 节点后端看到的"用户"即订阅：id、uuid 用于节点认证，流量与在线设备按订阅统计。
type Subscription struct {
	ID             int64  `gorm:"column:id;primaryKey" json:"id"`
	UserID         int64  `gorm:"column:user_id" json:"user_id"`
	Name           string `gorm:"column:name" json:"name"`
	IsPrimary      int    `gorm:"column:is_primary" json:"is_primary"`
	PlanID         *int64 `gorm:"column:plan_id" json:"plan_id"`
	GroupID        *int64 `gorm:"column:group_id" json:"group_id"`
	UUID           string `gorm:"column:uuid" json:"uuid"`
	Token          string `gorm:"column:token" json:"token"`
	TransferEnable int64  `gorm:"column:transfer_enable" json:"transfer_enable"`
	U              int64  `gorm:"column:u" json:"u"`
	D              int64  `gorm:"column:d" json:"d"`
	T              int64  `gorm:"column:t" json:"t"`
	DeviceLimit    *int64 `gorm:"column:device_limit" json:"device_limit"`
	SpeedLimit     *int64 `gorm:"column:speed_limit" json:"speed_limit"`
	// ExpiredAt 为空表示一次性套餐长期有效；0 或过去的时间表示已到期（与原项目一致）。
	ExpiredAt     *int64 `gorm:"column:expired_at" json:"expired_at"`
	AutoRenewal   int    `gorm:"column:auto_renewal" json:"auto_renewal"`
	RemindExpire  int    `gorm:"column:remind_expire" json:"remind_expire"`
	RemindTraffic int    `gorm:"column:remind_traffic" json:"remind_traffic"`
	Banned        int    `gorm:"column:banned" json:"banned"`
	// BonusTraffic 本周期奖励流量（已计入 TransferEnable），流量重置时从 TransferEnable 中扣回（决策 D87）。
	BonusTraffic int64 `gorm:"column:bonus_traffic" json:"bonus_traffic"`
	CreatedAt    int64 `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt    int64 `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Subscription) TableName() string { return "v2_subscription" }

// InviteCode 邀请码（v2_invite_code）。
type InviteCode struct {
	ID        int64  `gorm:"column:id;primaryKey" json:"id"`
	UserID    int64  `gorm:"column:user_id" json:"user_id"`
	Code      string `gorm:"column:code" json:"code"`
	Status    int    `gorm:"column:status" json:"status"`
	PV        int64  `gorm:"column:pv" json:"pv"`
	CreatedAt int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (InviteCode) TableName() string { return "v2_invite_code" }

// CommissionLog 佣金记录（v2_commission_log）。
type CommissionLog struct {
	ID           int64  `gorm:"column:id;primaryKey" json:"id"`
	InviteUserID int64  `gorm:"column:invite_user_id" json:"invite_user_id"`
	UserID       int64  `gorm:"column:user_id" json:"user_id"`
	TradeNo      string `gorm:"column:trade_no" json:"trade_no"`
	OrderAmount  int64  `gorm:"column:order_amount" json:"order_amount"`
	GetAmount    int64  `gorm:"column:get_amount" json:"get_amount"`
	CreatedAt    int64  `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt    int64  `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (CommissionLog) TableName() string { return "v2_commission_log" }
