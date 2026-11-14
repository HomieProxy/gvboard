package model

// 订单类型
const (
	OrderTypeNew     = 1 // 新购
	OrderTypeRenew   = 2 // 续费
	OrderTypeUpgrade = 3 // 升级
	OrderTypeReset   = 4 // 重置流量
	OrderTypeDeposit = 9 // 充值
)

// 订单状态
const (
	OrderStatusPending    = 0 // 待支付
	OrderStatusProcessing = 1 // 开通中
	OrderStatusCancelled  = 2 // 已取消
	OrderStatusCompleted  = 3 // 已完成
	OrderStatusDiscounted = 4 // 已折抵
)

// 佣金状态
const (
	CommissionStatusPending = 0 // 待确认
	CommissionStatusIssuing = 1 // 发放中
	CommissionStatusValid   = 2 // 有效
	CommissionStatusInvalid = 3 // 无效
)

// Order 订单（v2_order）。
type Order struct {
	ID                      int64   `gorm:"column:id;primaryKey" json:"id"`
	InviteUserID            *int64  `gorm:"column:invite_user_id" json:"invite_user_id"`
	UserID                  int64   `gorm:"column:user_id" json:"user_id"`
	PlanID                  int64   `gorm:"column:plan_id" json:"plan_id"`
	CouponID                *int64  `gorm:"column:coupon_id" json:"coupon_id"`
	PaymentID               *int64  `gorm:"column:payment_id" json:"payment_id"`
	Type                    int     `gorm:"column:type" json:"type"`
	Period                  string  `gorm:"column:period" json:"period"`
	TradeNo                 string  `gorm:"column:trade_no" json:"trade_no"`
	CallbackNo              *string `gorm:"column:callback_no" json:"callback_no"`
	TotalAmount             int64   `gorm:"column:total_amount" json:"total_amount"`
	HandlingAmount          *int64  `gorm:"column:handling_amount" json:"handling_amount"`
	DiscountAmount          *int64  `gorm:"column:discount_amount" json:"discount_amount"`
	SurplusAmount           *int64  `gorm:"column:surplus_amount" json:"surplus_amount"`
	RefundAmount            *int64  `gorm:"column:refund_amount" json:"refund_amount"`
	BalanceAmount           *int64  `gorm:"column:balance_amount" json:"balance_amount"`
	SurplusOrderIDs         IntList `gorm:"column:surplus_order_ids" json:"surplus_order_ids"`
	Status                  int     `gorm:"column:status" json:"status"`
	CommissionStatus        int     `gorm:"column:commission_status" json:"commission_status"`
	CommissionBalance       int64   `gorm:"column:commission_balance" json:"commission_balance"`
	ActualCommissionBalance *int64  `gorm:"column:actual_commission_balance" json:"actual_commission_balance"`
	PaidAt                  *int64  `gorm:"column:paid_at" json:"paid_at"`
	// SubscriptionID 订单作用的订阅（Gvboard 新增）。新开订阅的订单在开通后回填。
	SubscriptionID *int64 `gorm:"column:subscription_id" json:"subscription_id"`
	// Immediate 同套餐续费时立即生效（Gvboard 新增，决策 D68）：清零已用流量并扣除本周期剩余时间。
	Immediate int   `gorm:"column:immediate" json:"immediate"`
	CreatedAt int64 `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt int64 `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Order) TableName() string { return "v2_order" }

// Plan 套餐（v2_plan）。价格单位为分，流量单位为 GB。
type Plan struct {
	ID                 int64   `gorm:"column:id;primaryKey" json:"id"`
	GroupID            int64   `gorm:"column:group_id" json:"group_id"`
	TransferEnable     int64   `gorm:"column:transfer_enable" json:"transfer_enable"`
	DeviceLimit        *int64  `gorm:"column:device_limit" json:"device_limit"`
	Name               string  `gorm:"column:name" json:"name"`
	SpeedLimit         *int64  `gorm:"column:speed_limit" json:"speed_limit"`
	Show               int     `gorm:"column:show" json:"show"`
	Sort               *int64  `gorm:"column:sort" json:"sort"`
	Renew              int     `gorm:"column:renew" json:"renew"`
	Content            *string `gorm:"column:content" json:"content"`
	MonthPrice         *int64  `gorm:"column:month_price" json:"month_price"`
	QuarterPrice       *int64  `gorm:"column:quarter_price" json:"quarter_price"`
	HalfYearPrice      *int64  `gorm:"column:half_year_price" json:"half_year_price"`
	YearPrice          *int64  `gorm:"column:year_price" json:"year_price"`
	TwoYearPrice       *int64  `gorm:"column:two_year_price" json:"two_year_price"`
	ThreeYearPrice     *int64  `gorm:"column:three_year_price" json:"three_year_price"`
	OnetimePrice       *int64  `gorm:"column:onetime_price" json:"onetime_price"`
	ResetPrice         *int64  `gorm:"column:reset_price" json:"reset_price"`
	ResetTrafficMethod *int    `gorm:"column:reset_traffic_method" json:"reset_traffic_method"`
	CapacityLimit      *int64  `gorm:"column:capacity_limit" json:"capacity_limit"`
	CreatedAt          int64   `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt          int64   `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Plan) TableName() string { return "v2_plan" }

// Coupon 优惠券（v2_coupon）。type：1 按金额，2 按比例。
type Coupon struct {
	ID               int64   `gorm:"column:id;primaryKey" json:"id"`
	Code             string  `gorm:"column:code" json:"code"`
	Name             string  `gorm:"column:name" json:"name"`
	Type             int     `gorm:"column:type" json:"type"`
	Value            int64   `gorm:"column:value" json:"value"`
	Show             int     `gorm:"column:show" json:"show"`
	LimitUse         *int64  `gorm:"column:limit_use" json:"limit_use"`
	LimitUseWithUser *int64  `gorm:"column:limit_use_with_user" json:"limit_use_with_user"`
	LimitPlanIDs     IntList `gorm:"column:limit_plan_ids" json:"limit_plan_ids"`
	LimitPeriod      StrList `gorm:"column:limit_period" json:"limit_period"`
	StartedAt        int64   `gorm:"column:started_at" json:"started_at"`
	EndedAt          int64   `gorm:"column:ended_at" json:"ended_at"`
	CreatedAt        int64   `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt        int64   `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Coupon) TableName() string { return "v2_coupon" }

// Giftcard 礼品卡（v2_giftcard）。type：1 余额，2 时长，3 流量，4 重置流量，5 套餐。
type Giftcard struct {
	ID          int64   `gorm:"column:id;primaryKey" json:"id"`
	Code        string  `gorm:"column:code" json:"code"`
	Name        string  `gorm:"column:name" json:"name"`
	Type        int     `gorm:"column:type" json:"type"`
	Value       *int64  `gorm:"column:value" json:"value"`
	PlanID      *int64  `gorm:"column:plan_id" json:"plan_id"`
	LimitUse    *int64  `gorm:"column:limit_use" json:"limit_use"`
	UsedUserIDs IntList `gorm:"column:used_user_ids" json:"used_user_ids"`
	StartedAt   int64   `gorm:"column:started_at" json:"started_at"`
	EndedAt     int64   `gorm:"column:ended_at" json:"ended_at"`
	CreatedAt   int64   `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt   int64   `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Giftcard) TableName() string { return "v2_giftcard" }

// Payment 支付方式（v2_payment）。
type Payment struct {
	ID                 int64    `gorm:"column:id;primaryKey" json:"id"`
	UUID               string   `gorm:"column:uuid" json:"uuid"`
	Payment            string   `gorm:"column:payment" json:"payment"`
	Name               string   `gorm:"column:name" json:"name"`
	Icon               *string  `gorm:"column:icon" json:"icon"`
	Config             JSON     `gorm:"column:config" json:"config"`
	NotifyDomain       *string  `gorm:"column:notify_domain" json:"notify_domain"`
	HandlingFeeFixed   *int64   `gorm:"column:handling_fee_fixed" json:"handling_fee_fixed"`
	HandlingFeePercent *float64 `gorm:"column:handling_fee_percent" json:"handling_fee_percent"`
	Enable             int      `gorm:"column:enable" json:"enable"`
	Sort               *int64   `gorm:"column:sort" json:"sort"`
	CreatedAt          int64    `gorm:"column:created_at;autoCreateTime" json:"created_at"`
	UpdatedAt          int64    `gorm:"column:updated_at;autoUpdateTime" json:"updated_at"`
}

func (Payment) TableName() string { return "v2_payment" }
