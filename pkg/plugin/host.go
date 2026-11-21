package plugin

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/hook"
)

// Host 插件运行环境，由 Gvboard 在 Setup 时传入。
type Host interface {
	// Code 插件标识。
	Code() string
	// Enabled 插件当前是否启用（多实例部署时各实例自动同步）。
	Enabled() bool
	// Config 当前配置（未配置的项为默认值）。
	Config() Config
	// KV 插件专属的键值存储。
	KV() KV
	// DB 数据库连接（与面板共用，编译期插件可完全访问数据库，请谨慎操作）。
	// 修改用户余额请使用 AddBalance，直接写 SQL 不会记录余额变动日志。
	DB() *gorm.DB
	// Log 插件日志。
	Log() *zap.Logger
	// AddBalance 调整用户余额（分，可为负数，余额不能小于 0），返回调整后的余额；
	// 同时写入余额变动日志，remark 为日志备注（可为空）。
	AddBalance(ctx context.Context, userID, amount int64, remark string) (int64, error)

	// 以下方法只能在 Setup 中调用，参数错误时 Setup 失败。

	// On 订阅事件。处理函数在队列中异步执行，返回错误时最多重试 3 次，处理函数需可重复执行。
	On(event string, h EventHandler)
	// AddFilter 注册过滤器，priority 小的先执行。建议使用带类型的 AddFilter 函数。
	AddFilter(name string, priority int, f FilterFunc)
	// GuestRoutes 访客路由，挂在 {API 前缀}/plugin/{Code}/ 下。
	GuestRoutes() gin.IRouter
	// UserRoutes 用户路由（需登录），同样挂在 {API 前缀}/plugin/{Code}/ 下，与访客路由的路径不能重复。
	UserRoutes() gin.IRouter
	// AdminRoutes 管理端路由（需管理员），挂在 {API 前缀}/{管理端路径}/plugin/{Code}/ 下。
	AdminRoutes() gin.IRouter
	// Job 定时任务（5 段 cron 表达式），name 只能包含小写字母、数字、下划线。插件停用时不执行。
	Job(name, spec string, run func(ctx context.Context) error)
	// Payment 新增支付网关，name 为 1～16 个字符且不能与已有网关重复。插件停用时该网关不可用。
	Payment(name string, g PaymentGateway)
	// SubscribeFormat 新增客户端订阅格式，优先于内置格式匹配。插件停用时不参与匹配。
	SubscribeFormat(f *SubscribeFormat)
	// Widget 注册用户端卡片，每个插件最多 5 张。插件停用时不显示。
	Widget(w Widget)
	// Page 注册用户端页面（出现在侧边导航中），每个插件最多 3 个。插件停用时不显示。
	Page(p Page)
}

// Event 事件。Data 为事件数据的 JSON，使用 Decode 解析为对应的类型（如 OrderEvent）。
type Event struct {
	Name string          `json:"name"`
	Data json.RawMessage `json:"data"`
	Time int64           `json:"time"`
}

// Decode 解析事件数据。
func (e Event) Decode(v any) error { return json.Unmarshal(e.Data, v) }

// EventHandler 事件处理函数。
type EventHandler func(ctx context.Context, e Event) error

// FilterFunc 过滤器函数，返回修改后的值。不要原地修改 v 中的切片、指针指向的数据，应复制后修改。
type FilterFunc func(ctx context.Context, v any) (any, error)

// 事件名称。
const (
	EventUserRegistered      = hook.EventUserRegistered
	EventUserLogin           = hook.EventUserLogin
	EventOrderCreated        = hook.EventOrderCreated
	EventOrderPaid           = hook.EventOrderPaid
	EventOrderOpened         = hook.EventOrderOpened
	EventOrderCancelled      = hook.EventOrderCancelled
	EventSubscriptionChanged = hook.EventSubscriptionChanged
	EventTicketCreated       = hook.EventTicketCreated
	EventTicketReplied       = hook.EventTicketReplied
	EventTrafficReported     = hook.EventTrafficReported
)

// Events 全部事件名称。
func Events() []string { return hook.Events() }

// 事件数据类型。
type (
	UserEvent         = hook.User
	OrderEvent        = hook.Order
	SubscriptionEvent = hook.Subscription
	TicketEvent       = hook.Ticket
	TrafficEvent      = hook.Traffic
)

// 过滤器值类型。
type (
	OrderAmount      = hook.OrderAmount
	AmountItem       = hook.AmountItem
	PlanBadges       = hook.PlanBadges
	PlanBadge        = hook.PlanBadge
	SubscribeServers = hook.SubscribeServers
	SubscribeContent = hook.SubscribeContent
	MailMessage      = hook.MailMessage
	KnowledgeArticle = hook.KnowledgeArticle
)

// 过滤器（类型为 hook.Filter[T]，配合 AddFilter 使用）。
var (
	FilterOrderAmount      = hook.FilterOrderAmount
	FilterPlanBadges       = hook.FilterPlanBadges
	FilterSubscribeServers = hook.FilterSubscribeServers
	FilterSubscribeContent = hook.FilterSubscribeContent
	FilterMailMessage      = hook.FilterMailMessage
	FilterKnowledgeArticle = hook.FilterKnowledgeArticle
)

// AddFilter 注册带类型的过滤器：
//
//	plugin.AddFilter(h, plugin.FilterOrderAmount, 10, func(ctx context.Context, v plugin.OrderAmount) (plugin.OrderAmount, error) {
//		off := v.Amount / 10
//		v.Amount -= off
//		v.Items = append(v.Items, plugin.AmountItem{Label: "九折活动", Amount: -off})
//		return v, nil
//	})
func AddFilter[T any](h Host, f hook.Filter[T], priority int, fn func(ctx context.Context, v T) (T, error)) {
	h.AddFilter(f.Name, priority, func(ctx context.Context, v any) (any, error) {
		t, ok := v.(T)
		if !ok {
			return v, fmt.Errorf("过滤器 %s 的值类型错误", f.Name)
		}
		return fn(ctx, t)
	})
}

// Config 插件配置。
type Config map[string]any

// String 字符串配置项。
func (c Config) String(key string) string {
	switch v := c[key].(type) {
	case nil:
		return ""
	case string:
		return v
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case bool:
		if v {
			return "1"
		}
		return "0"
	default:
		return fmt.Sprint(v)
	}
}

// Float 数值配置项，无法转换时为 0。
func (c Config) Float(key string) float64 {
	switch v := c[key].(type) {
	case float64:
		return v
	case int:
		return float64(v)
	case int64:
		return float64(v)
	case json.Number:
		f, _ := v.Float64()
		return f
	case string:
		f, _ := strconv.ParseFloat(v, 64)
		return f
	case bool:
		if v {
			return 1
		}
	}
	return 0
}

// Int 整数配置项（小数部分截断）。
func (c Config) Int(key string) int64 { return int64(c.Float(key)) }

// Bool 布尔配置项。
func (c Config) Bool(key string) bool {
	switch v := c[key].(type) {
	case bool:
		return v
	case string:
		return v != "" && v != "0" && v != "false"
	}
	return c.Float(key) != 0
}

// 配置项类型。
const (
	FieldString   = "string"   // 单行文本
	FieldText     = "text"     // 多行文本
	FieldPassword = "password" // 密钥（管理端以密码框显示）
	FieldNumber   = "number"   // 数值
	FieldBool     = "bool"     // 开关
	FieldSelect   = "select"   // 单选，值必须为 Options 之一
)

// Option 单选项。
type Option struct {
	Value string `json:"value"`
	Label string `json:"label"`
}

// Field 配置项定义。
type Field struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Type     string   `json:"type"`
	Default  any      `json:"default"`
	Options  []Option `json:"options,omitempty"`
	Required bool     `json:"required"`
	Help     string   `json:"help,omitempty"`
}

// ErrKVNotFound 键不存在或已过期。
var ErrKVNotFound = errors.New("键不存在")

// KV 插件专属的键值存储（存于数据库，多实例共享）。键最长 191 字节，值最大 1 MB。
type KV interface {
	// Get 读取，键不存在或已过期时返回 ErrKVNotFound。
	Get(ctx context.Context, key string) (string, error)
	// Set 写入，ttl 为 0 表示永不过期。
	Set(ctx context.Context, key, value string, ttl time.Duration) error
	// SetNX 键不存在（或已过期）时写入，返回是否写入成功，可用于去重。
	SetNX(ctx context.Context, key, value string, ttl time.Duration) (bool, error)
	// Delete 删除。
	Delete(ctx context.Context, key string) error
}
