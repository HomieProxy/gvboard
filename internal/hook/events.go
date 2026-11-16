package hook

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"unicode/utf8"

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

// OrderAmount 下单金额过滤器的值：已计算优惠券、会员折扣与升级折抵，尚未扣除余额。
// 只采用修改后的 Amount（分，不能为负）与新追加的 Items。
type OrderAmount struct {
	UserID         int64  `json:"user_id"`
	PlanID         int64  `json:"plan_id"`
	Period         string `json:"period"`
	SubscriptionID int64  `json:"subscription_id"` // 0 表示新开订阅
	Type           int    `json:"type"`
	Amount         int64  `json:"amount"`
	// Preview 为 true 时是结算页试算，不会创建订单。过滤器在试算与下单时都会执行，不要在其中修改数据。
	Preview bool `json:"preview"`
	// Items 之前的插件追加的调整明细。修改 Amount 时追加说明，合计必须等于 Amount 的变化；
	// 不追加时以插件名称自动生成一条。
	Items []AmountItem `json:"items"`
}

// AmountItem 订单金额调整明细，显示在结算页与订单详情。
type AmountItem struct {
	Label  string `json:"label"`  // 名称，如“首单优惠”，1～32 个字符
	Amount int64  `json:"amount"` // 调整金额（分），负数为减免
	Plugin string `json:"plugin"` // 插件标识，由系统填写
}

// PlanBadges 套餐标签过滤器的值：当前用户看到的套餐。插件只能向 Badges 追加标签。
type PlanBadges struct {
	UserID int64       `json:"user_id"`
	Plans  []PlanBadge `json:"plans"`
}

// PlanBadge 一个套餐的标签。
type PlanBadge struct {
	PlanID int64    `json:"plan_id"`
	Badges []string `json:"badges"` // 每个 1～16 个字符，每个套餐最多 3 个
}

// 调整明细与标签的限制。
const (
	maxItemsPerPlugin = 3
	maxItems          = 20
	maxItemLabel      = 32
	maxBadges         = 3
	maxBadgeLabel     = 16
)

func validLabel(s string, max int) bool {
	return s != "" && s == strings.TrimSpace(s) && utf8.RuneCountInString(s) <= max
}

// checkOrderAmount 只采用 Amount 与新追加的明细；修改了金额但没有明细时以插件名称生成一条。
func checkOrderAmount(src Source, o, n OrderAmount) (OrderAmount, error) {
	if n.Amount < 0 || n.Amount > maxAmount {
		return o, errors.New("订单金额无效")
	}
	if len(n.Items) < len(o.Items) || !slices.Equal(n.Items[:len(o.Items)], o.Items) {
		return o, errors.New("不能修改其它插件的调整明细")
	}
	added := slices.Clone(n.Items[len(o.Items):])
	diff := n.Amount - o.Amount
	if len(added) == 0 && diff != 0 {
		added = []AmountItem{{Label: src.Name, Amount: diff}}
	}
	if len(added) > maxItemsPerPlugin {
		return o, fmt.Errorf("每个插件最多追加 %d 条调整明细", maxItemsPerPlugin)
	}
	var sum int64
	for i := range added {
		if !validLabel(added[i].Label, maxItemLabel) || added[i].Amount == 0 {
			return o, errors.New("调整明细的名称或金额无效")
		}
		sum += added[i].Amount
		added[i].Plugin = src.Code
	}
	if sum != diff {
		return o, errors.New("调整明细合计与金额变化不一致")
	}
	if len(o.Items)+len(added) > maxItems {
		return o, errors.New("调整明细过多")
	}
	out := o
	out.Amount = n.Amount
	out.Items = append(slices.Clone(o.Items), added...)
	return out, nil
}

// checkPlanBadges 只采用各套餐新追加的标签。
func checkPlanBadges(_ Source, o, n PlanBadges) (PlanBadges, error) {
	if len(n.Plans) != len(o.Plans) {
		return o, errors.New("不能增减套餐")
	}
	out := PlanBadges{UserID: o.UserID, Plans: make([]PlanBadge, len(o.Plans))}
	for i, p := range o.Plans {
		q := n.Plans[i]
		if q.PlanID != p.PlanID || len(q.Badges) < len(p.Badges) || !slices.Equal(q.Badges[:len(p.Badges)], p.Badges) {
			return o, errors.New("不能修改套餐或其它插件的标签")
		}
		if len(q.Badges) > maxBadges {
			return o, fmt.Errorf("每个套餐最多 %d 个标签", maxBadges)
		}
		for _, b := range q.Badges[len(p.Badges):] {
			if !validLabel(b, maxBadgeLabel) {
				return o, errors.New("标签无效")
			}
		}
		out.Plans[i] = PlanBadge{PlanID: p.PlanID, Badges: slices.Clone(q.Badges)}
	}
	return out, nil
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

// KnowledgeArticle 知识库文章过滤器的值（用户查看文章时，访问限制与订阅变量处理之后），只采用修改后的 Body。
// Body 为 HTML 或 Markdown，插件可替换其中的自定义占位符；插入用户数据时必须自行转义。
type KnowledgeArticle struct {
	UserID    int64  `json:"user_id"`
	ArticleID int64  `json:"article_id"`
	Locale    string `json:"locale"`
	// Available 用户是否有可用订阅（与访问限制区块的判断相同）。
	Available bool   `json:"available"`
	Body      string `json:"body"`
}

// maxArticleBody 过滤后的文章正文上限。
const maxArticleBody = 1 << 20

// maxAmount 过滤器允许的最大订单金额（分），与下单充值上限一致。
const maxAmount = 9999999 * 100

// 过滤器。
var (
	FilterOrderAmount = Filter[OrderAmount]{Name: "order.amount", Check: checkOrderAmount, Clone: func(v OrderAmount) OrderAmount {
		v.Items = slices.Clone(v.Items)
		return v
	}}
	FilterPlanBadges = Filter[PlanBadges]{Name: "plan.badges", Check: checkPlanBadges, Clone: func(v PlanBadges) PlanBadges {
		v.Plans = slices.Clone(v.Plans)
		for i := range v.Plans {
			v.Plans[i].Badges = slices.Clone(v.Plans[i].Badges)
		}
		return v
	}}
	FilterSubscribeServers = Filter[SubscribeServers]{Name: "subscribe.servers", Check: func(_ Source, _, n SubscribeServers) (SubscribeServers, error) {
		for _, s := range n.Servers {
			if s == nil {
				return n, errors.New("节点不能为 nil")
			}
		}
		return n, nil
	}}
	FilterSubscribeContent = Filter[SubscribeContent]{Name: "subscribe.content"}
	FilterMailMessage      = Filter[MailMessage]{Name: "mail.message", Check: func(_ Source, o, n MailMessage) (MailMessage, error) {
		if n.To != o.To {
			return n, errors.New("不能修改收件人")
		}
		if n.Subject == "" || n.HTML == "" {
			return n, errors.New("邮件标题与内容不能为空")
		}
		return n, nil
	}}
	FilterKnowledgeArticle = Filter[KnowledgeArticle]{Name: "knowledge.article", Check: func(_ Source, o, n KnowledgeArticle) (KnowledgeArticle, error) {
		if len(n.Body) > maxArticleBody {
			return o, fmt.Errorf("文章内容不能超过 %d KB", maxArticleBody>>10)
		}
		if n.Body == "" && o.Body != "" {
			return o, errors.New("文章内容不能为空")
		}
		out := o
		out.Body = n.Body
		return out, nil
	}}
)

// Filters 全部过滤器名称。
func Filters() []string {
	return []string{FilterOrderAmount.Name, FilterPlanBadges.Name, FilterSubscribeServers.Name, FilterSubscribeContent.Name,
		FilterMailMessage.Name, FilterKnowledgeArticle.Name}
}
