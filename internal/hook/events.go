package hook

import (
	"errors"

	"gvboard/internal/phpx"
)

// 事件名称。事件在业务事务提交后发出，插件在队列中异步处理，失败最多重试 3 次（处理函数需可重复执行）。
const (
	EventUserRegistered      = "user.registered"      // 用户注册：User
	EventUserLogin           = "user.login"           // 用户登录（含注册后自动登录）：User
	EventOrderCreated        = "order.created"        // 新订单（用户下单、管理员分配）：Order
	EventOrderPaid           = "order.paid"           // 订单已支付，等待开通：Order
	EventOrderOpened         = "order.opened"         // 订单已开通：Order
	EventOrderCancelled      = "order.cancelled"      // 订单已取消：Order
	EventSubscriptionChanged = "subscription.changed" // 订阅变更：Subscription
	EventTicketCreated       = "ticket.created"       // 用户新建工单（含提现申请）：Ticket
	EventTicketReplied       = "ticket.replied"       // 工单回复（用户或管理员）：Ticket
	EventTrafficReported     = "traffic.reported"     // 节点上报流量：Traffic
)

// Events 全部事件名称。
func Events() []string {
	return []string{
		EventUserRegistered, EventUserLogin,
		EventOrderCreated, EventOrderPaid, EventOrderOpened, EventOrderCancelled,
		EventSubscriptionChanged, EventTicketCreated, EventTicketReplied, EventTrafficReported,
	}
}

// User 用户事件数据。
type User struct {
	UserID       int64  `json:"user_id"`
	Email        string `json:"email"`
	InviteUserID *int64 `json:"invite_user_id,omitempty"`
	IP           string `json:"ip,omitempty"`
}

// Order 订单事件数据，金额单位为分。
type Order struct {
	TradeNo        string `json:"trade_no"`
	UserID         int64  `json:"user_id"`
	PlanID         int64  `json:"plan_id"`
	Period         string `json:"period"`
	Type           int    `json:"type"`
	Status         int    `json:"status"`
	TotalAmount    int64  `json:"total_amount"`
	BalanceAmount  int64  `json:"balance_amount"`
	SubscriptionID *int64 `json:"subscription_id"`
}

// 订阅变更方式。
const (
	SubscriptionOrder         = "order"          // 订单开通
	SubscriptionUpdated       = "updated"        // 用户修改名称、提醒、自动续费等设置
	SubscriptionResetSecurity = "reset_security" // 用户重置订阅链接与 UUID
	SubscriptionNewPeriod     = "new_period"     // 用户提前开始下一个流量周期
	SubscriptionPlugin        = "plugin"         // 插件增加流量或延长到期时间
)

// Subscription 订阅变更事件数据。
type Subscription struct {
	SubscriptionID int64  `json:"subscription_id"`
	UserID         int64  `json:"user_id"`
	Action         string `json:"action"`
	TradeNo        string `json:"trade_no,omitempty"`
}

// Ticket 工单事件数据。Kind 为 created / withdraw / replied；ByAdmin 为 true 表示管理员回复。
type Ticket struct {
	TicketID int64  `json:"ticket_id"`
	UserID   int64  `json:"user_id"`
	Subject  string `json:"subject"`
	Kind     string `json:"kind"`
	Message  string `json:"message"`
	ByAdmin  bool   `json:"by_admin"`
}

// Traffic 节点上报流量事件数据（字节）。
type Traffic struct {
	ServerType string `json:"server_type"`
	ServerID   int64  `json:"server_id"`
	Users      int    `json:"users"`
	Upload     int64  `json:"upload"`
	Download   int64  `json:"download"`
}

// OrderAmount 下单金额过滤器的值：已计算优惠券、会员折扣与升级折抵，尚未扣除余额。只采用修改后的 Amount（分，不能为负）。
type OrderAmount struct {
	UserID         int64  `json:"user_id"`
	PlanID         int64  `json:"plan_id"`
	Period         string `json:"period"`
	SubscriptionID int64  `json:"subscription_id"` // 0 表示新开订阅
	Type           int    `json:"type"`
	Amount         int64  `json:"amount"`
}

// SubscribeServers 订阅下发节点列表过滤器的值，只采用修改后的 Servers。
type SubscribeServers struct {
	UserID         int64       `json:"user_id"`
	SubscriptionID int64       `json:"subscription_id"`
	Client         string      `json:"client"`
	Servers        []*phpx.Arr `json:"servers"`
}

// SubscribeContent 订阅下发内容过滤器的值，只采用修改后的 ContentType、Headers、Body。
type SubscribeContent struct {
	UserID         int64       `json:"user_id"`
	SubscriptionID int64       `json:"subscription_id"`
	Client         string      `json:"client"`
	ContentType    string      `json:"content_type"`
	Headers        [][2]string `json:"headers"`
	Body           []byte      `json:"body"`
}

// MailMessage 邮件内容过滤器的值（模板渲染后、发送前），只采用修改后的 Subject、HTML，收件人不能修改。
type MailMessage struct {
	To       string `json:"to"`
	Subject  string `json:"subject"`
	Template string `json:"template"`
	HTML     string `json:"html"`
}

// maxAmount 过滤器允许的最大订单金额（分），与下单充值上限一致。
const maxAmount = 9999999 * 100

// 过滤器。
var (
	FilterOrderAmount = Filter[OrderAmount]{Name: "order.amount", Check: func(_, n OrderAmount) error {
		if n.Amount < 0 || n.Amount > maxAmount {
			return errors.New("订单金额无效")
		}
		return nil
	}}
	FilterSubscribeServers = Filter[SubscribeServers]{Name: "subscribe.servers", Check: func(_, n SubscribeServers) error {
		for _, s := range n.Servers {
			if s == nil {
				return errors.New("节点不能为 nil")
			}
		}
		return nil
	}}
	FilterSubscribeContent = Filter[SubscribeContent]{Name: "subscribe.content"}
	FilterMailMessage      = Filter[MailMessage]{Name: "mail.message", Check: func(o, n MailMessage) error {
		if n.To != o.To {
			return errors.New("不能修改收件人")
		}
		if n.Subject == "" || n.HTML == "" {
			return errors.New("邮件标题与内容不能为空")
		}
		return nil
	}}
)

// Filters 全部过滤器名称。
func Filters() []string {
	return []string{FilterOrderAmount.Name, FilterSubscribeServers.Name, FilterSubscribeContent.Name, FilterMailMessage.Name}
}
