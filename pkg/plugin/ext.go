package plugin

import (
	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/payment"
	"gvboard/internal/phpx"
	"gvboard/internal/subscribe"
)

// 支付网关扩展（与内置网关使用同一套接口，参考 internal/payment 中的实现）。
type (
	PaymentGateway      = payment.Gateway
	PaymentForm         = payment.Form
	PaymentField        = payment.Field
	PaymentEnv          = payment.Env
	PaymentConfig       = payment.Config
	PaymentOrder        = payment.Order
	PaymentResult       = payment.Result
	PaymentNotification = payment.Notification
	PaymentPaid         = payment.Paid
	PaymentParams       = payment.Params
)

// 订阅格式扩展。SubscribeFormat.Flag 为 User-Agent（或 flag 参数，小写）中用于识别客户端的子串。
type (
	SubscribeFormat   = subscribe.Protocol
	SubscribeContext  = subscribe.Ctx
	SubscribeResponse = subscribe.Response
	// Server 节点（与原项目一致的有序数组结构）。
	Server = phpx.Arr
)

// User 当前登录用户（用户路由与管理端路由中可用）。
type User struct {
	ID      int64
	Email   string
	IsAdmin bool
	IsStaff bool
}

// CurrentUser 返回当前登录用户，访客路由中为 nil。
func CurrentUser(c *gin.Context) *User {
	id := auth.Current(c)
	if id == nil {
		return nil
	}
	return &User{ID: id.ID, Email: id.Email, IsAdmin: id.IsAdmin, IsStaff: id.IsStaff}
}

// OK 返回成功响应（与面板接口格式一致）。
func OK(c *gin.Context, data any) { httpx.OK(c, data) }

// Fail 返回错误响应，status 为 HTTP 状态码（400～599），msg 原样返回给客户端。
func Fail(c *gin.Context, status int, msg string) {
	if status < 400 || status > 599 {
		status = 400
	}
	httpx.Fail(c, &httpx.Error{Status: status, Code: status, Msg: msg})
}

// NotFound 返回与面板一致的 404 页面。
func NotFound(c *gin.Context) { httpx.NotFound(c) }
