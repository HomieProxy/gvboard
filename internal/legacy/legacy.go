// Package legacy 原 v2board /api/v1 接口兼容层（决策 D58）：请求参数、返回格式、错误格式与原 PHP 控制器一致，
// 业务逻辑全部调用原生服务。原接口中属于订阅的字段读写都作用于用户的主订阅。
// 仅在 compat.v2board_api 开启时挂载。
package legacy

import (
	"errors"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/adminstat"
	"gvboard/internal/adminuser"
	"gvboard/internal/auth"
	"gvboard/internal/cache"
	"gvboard/internal/content"
	"gvboard/internal/httpx"
	"gvboard/internal/i18n"
	"gvboard/internal/invite"
	"gvboard/internal/model"
	"gvboard/internal/node"
	"gvboard/internal/nodeapi"
	"gvboard/internal/order"
	"gvboard/internal/passport"
	"gvboard/internal/payment"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/subscribe"
	"gvboard/internal/subscription"
	"gvboard/internal/sysconfig"
	"gvboard/internal/telegram"
	"gvboard/internal/theme"
	"gvboard/internal/ticket"
	"gvboard/internal/usercenter"
)

// Module 兼容层路由，依赖的服务与原生 API 共用同一实例。
type Module struct {
	DB        *gorm.DB
	Settings  *setting.Service
	Auth      *auth.Service
	I18n      *i18n.Translator
	Log       *zap.Logger
	Passport  *passport.Service
	Users     *usercenter.Service
	Subs      *subscription.Service
	Orders    *order.Service
	Payments  *payment.Service
	Invites   *invite.Service
	Tickets   *ticket.Service
	Content   *content.Service
	Subscribe *subscribe.Service
	Traffic   *nodeapi.Service
	Bot       *telegram.Service

	AdminUsers *adminuser.Service
	Nodes      *node.Service
	SysConfig  *sysconfig.Service
	Cache      *cache.Cache
	Queue      adminstat.QueueInfo
	Themes     *theme.Manager
}

func (m *Module) log() *zap.Logger {
	if m.Log == nil {
		return zap.NewNop()
	}
	return m.Log
}

// Register 挂载原项目 passport、guest、user、管理端路由（路径与请求方法与原项目一致）。
// guest/telegram/webhook、guest/payment/notify、client/subscribe 由各模块按单独的开关挂载。
func (m *Module) Register(r *server.Router) {
	if r.Legacy == nil {
		return
	}
	p := r.Legacy.Group("/passport")
	p.POST("/auth/register", m.register)
	p.POST("/auth/login", m.login)
	p.GET("/auth/token2Login", m.token2Login)
	p.POST("/auth/forget", m.forget)
	p.POST("/auth/getQuickLoginUrl", m.requireUser, m.quickLoginURL)
	p.POST("/comm/sendEmailVerify", m.sendEmailVerify)
	p.POST("/comm/pv", m.pv)

	r.Legacy.GET("/guest/comm/config", m.guestConfig)

	u := r.Legacy.Group("/user", m.requireUser)
	u.GET("/unbindTelegram", m.unbindTelegram)
	u.GET("/resetSecurity", m.resetSecurity)
	u.GET("/info", m.info)
	u.POST("/newPeriod", m.newPeriod)
	u.POST("/redeemgiftcard", m.redeemGiftcard)
	u.POST("/changePassword", m.changePassword)
	u.POST("/update", m.update)
	u.GET("/getSubscribe", m.getSubscribe)
	u.GET("/getStat", m.getStat)
	u.GET("/checkLogin", m.checkLogin)
	u.POST("/transfer", m.transfer)
	u.POST("/getQuickLoginUrl", m.quickLoginURL)
	u.GET("/getActiveSession", m.activeSessions)
	u.POST("/removeActiveSession", m.removeSession)
	u.POST("/order/save", m.orderSave)
	u.POST("/order/checkout", m.orderCheckout)
	u.GET("/order/check", m.orderCheck)
	u.GET("/order/detail", m.orderDetail)
	u.GET("/order/fetch", m.orderFetch)
	u.GET("/order/getPaymentMethod", m.paymentMethods)
	u.POST("/order/cancel", m.orderCancel)
	u.GET("/plan/fetch", m.planFetch)
	u.GET("/invite/save", m.inviteSave)
	u.GET("/invite/fetch", m.inviteFetch)
	u.GET("/invite/details", m.inviteDetails)
	u.GET("/notice/fetch", m.noticeFetch)
	u.POST("/ticket/reply", m.ticketReply)
	u.POST("/ticket/close", m.ticketClose)
	u.POST("/ticket/save", m.ticketSave)
	u.GET("/ticket/fetch", m.ticketFetch)
	u.POST("/ticket/withdraw", m.ticketWithdraw)
	u.GET("/server/fetch", m.serverFetch)
	u.POST("/coupon/check", m.couponCheck)
	u.GET("/telegram/getBotInfo", m.botInfo)
	u.GET("/comm/config", m.userConfig)
	u.POST("/comm/getStripePublicKey", m.stripeKey)
	u.GET("/knowledge/fetch", m.knowledgeFetch)
	u.GET("/knowledge/getCategory", m.missingMethod)
	u.GET("/stat/getTrafficLog", m.trafficLog)

	m.registerAdmin(r)
}

// requireUser 原项目 User 中间件：凭证取自 auth_data 参数或 Authorization 请求头，失败统一 403。
// 已封禁用户的会话同样失效（决策 D16）。
func (m *Module) requireUser(c *gin.Context) {
	raw := params(c).str("auth_data")
	if raw == "" {
		raw = c.GetHeader("Authorization")
	}
	id, err := m.Auth.Parse(c.Request.Context(), raw)
	switch {
	case err == nil:
		auth.SetCurrent(c, id)
		c.Next()
	case errors.Is(err, auth.ErrUnauthenticated), errors.Is(err, auth.ErrBanned):
		unauthorized(c)
	default:
		m.internal(c, err)
	}
}

func uid(c *gin.Context) int64 { return auth.Current(c).ID }

// primary 用户的主订阅，没有时返回 nil（决策 D58：读取为空值，修改时报错）。
func (m *Module) primary(c *gin.Context) (*model.Subscription, error) {
	sub, err := m.Subs.Primary(c.Request.Context(), m.DB, uid(c))
	if errors.Is(err, subscription.ErrNotFound) {
		return nil, nil
	}
	return sub, err
}

// primaryID 主订阅 ID，没有主订阅时为 0。
func (m *Module) primaryID(c *gin.Context) (int64, error) {
	sub, err := m.primary(c)
	if err != nil || sub == nil {
		return 0, err
	}
	return sub.ID, nil
}

func baseURL(c *gin.Context) string { return httpx.BaseURL(c) }

func (m *Module) client(c *gin.Context) passport.Client {
	return passport.Client{IP: c.ClientIP(), UA: c.Request.UserAgent(), Locale: m.I18n.Locale(c)}
}
