package usercenter

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
	"gvboard/internal/subscription"
)

// Module 用户中心路由。
type Module struct {
	Svc *Service
}

// Register 挂载原生 API（均需登录）：
//
//	GET    {api}/user/config                              用户端需要的业务设置（货币、提现、分销、换套餐、工单）
//	GET    {api}/user/info                                用户信息
//	GET    {api}/user/stat                                待支付订单、未关闭工单、邀请人数
//	POST   {api}/user/password                            修改密码
//	GET    {api}/user/sessions                            活动会话
//	DELETE {api}/user/sessions/:id                        删除会话
//	POST   {api}/user/telegram/unbind                     解绑 Telegram
//	POST   {api}/user/commission/transfer                 佣金划转到余额
//	POST   {api}/user/giftcard/redeem                     兑换礼品卡
//	GET    {api}/user/subscriptions                       订阅列表
//	GET    {api}/user/subscriptions/:id                   订阅详情
//	PUT    {api}/user/subscriptions/:id                   修改备注名、自动续费与提醒设置
//	POST   {api}/user/subscriptions/:id/reset-security    重置订阅 uuid 与 token
//	POST   {api}/user/subscriptions/:id/new-period        提前开始新周期
//
// 管理端（{api}/{secure_path}，决策 D20）：
//
//	GET giftcards / POST giftcards / PUT giftcards/:id / POST giftcards/batch / DELETE giftcards/:id
func (m *Module) Register(r *server.Router) {
	a := m.Svc.Auth.AdminGroup(r.API, func() string { return m.Svc.Settings.String("secure_path") })
	a.GET("/giftcards", m.adminGiftcards)
	a.POST("/giftcards", m.adminCreateGiftcard)
	a.PUT("/giftcards/:id", m.adminUpdateGiftcard)
	a.POST("/giftcards/batch", m.adminGenerateGiftcards)
	a.DELETE("/giftcards/:id", m.adminDeleteGiftcard)

	g := r.API.Group("/user", m.Svc.Auth.RequireUser())
	g.GET("/config", m.config)
	g.GET("/info", m.info)
	g.GET("/stat", m.stat)
	g.POST("/password", m.password)
	g.GET("/sessions", m.sessions)
	g.DELETE("/sessions/:id", m.removeSession)
	g.POST("/telegram/unbind", m.unbindTelegram)
	g.POST("/commission/transfer", m.transfer)
	g.POST("/giftcard/redeem", m.redeem)
	g.GET("/subscriptions", m.subscriptions)
	g.GET("/subscriptions/:id", m.subscription)
	g.PUT("/subscriptions/:id", m.updateSubscription)
	g.POST("/subscriptions/:id/reset-security", m.resetSecurity)
	g.POST("/subscriptions/:id/new-period", m.newPeriod)
}

var errInvalidSubID = httpx.Validation("Invalid parameters")

func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil && !errors.Is(err, io.EOF) {
		httpx.Fail(c, errInvalidParams)
		return false
	}
	return true
}

func respond(c *gin.Context, data any, err error) {
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, data)
}

func uid(c *gin.Context) int64 { return auth.Current(c).ID }

func subID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, errInvalidSubID)
		return 0, false
	}
	return id, true
}

// baseURL 未设置订阅域名时，订阅链接使用当前请求的地址。
func baseURL(c *gin.Context) string { return httpx.BaseURL(c) }

// UserConfig 用户端需要的业务设置（对应原项目 user/comm/config，字段为 Gvboard 自有命名）。
type UserConfig struct {
	Currency       string `json:"currency"`
	CurrencySymbol string `json:"currency_symbol"`
	Withdraw       struct {
		Closed  bool     `json:"closed"`
		Methods []string `json:"methods"`
		Min     float64  `json:"min"` // 最低提现佣金（元）
	} `json:"withdraw"`
	Distribution struct {
		Enabled bool       `json:"enabled"`
		Levels  []*float64 `json:"levels"` // 三级分销比例（%），未设置为 null
	} `json:"distribution"`
	PlanChange   bool     `json:"plan_change"`   // 是否允许更换套餐
	TicketStatus int64    `json:"ticket_status"` // 0 开放，1 仅限有订阅的用户，2 关闭
	DepositBonus []string `json:"deposit_bonus"` // 充值赠送档位，格式 "充值金额:赠送金额"（元）
}

func (m *Module) config(c *gin.Context) {
	s := m.Svc.Settings
	var v UserConfig
	v.Currency = s.String("currency")
	v.CurrencySymbol = s.String("currency_symbol")
	v.Withdraw.Closed = s.Bool("withdraw_close_enable")
	v.Withdraw.Methods = s.Strings("commission_withdraw_method")
	v.Withdraw.Min = s.Float("commission_withdraw_limit")
	v.Distribution.Enabled = s.Bool("commission_distribution_enable")
	v.Distribution.Levels = []*float64{
		s.FloatPtr("commission_distribution_l1"), s.FloatPtr("commission_distribution_l2"), s.FloatPtr("commission_distribution_l3"),
	}
	v.PlanChange = s.Bool("plan_change_enable")
	v.TicketStatus = s.Int("ticket_status")
	v.DepositBonus = s.Strings("deposit_bounus")
	if v.Withdraw.Methods == nil {
		v.Withdraw.Methods = []string{}
	}
	if v.DepositBonus == nil {
		v.DepositBonus = []string{}
	}
	httpx.OK(c, v)
}

func (m *Module) info(c *gin.Context) {
	v, err := m.Svc.Info(c.Request.Context(), uid(c))
	respond(c, v, err)
}

func (m *Module) stat(c *gin.Context) {
	v, err := m.Svc.Stat(c.Request.Context(), uid(c))
	respond(c, v, err)
}

func (m *Module) password(c *gin.Context) {
	var in struct {
		OldPassword string `json:"old_password"`
		NewPassword string `json:"new_password"`
	}
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.ChangePassword(c.Request.Context(), uid(c), in.OldPassword, in.NewPassword))
}

func (m *Module) sessions(c *gin.Context) {
	v, err := m.Svc.Sessions(c.Request.Context(), auth.Current(c))
	respond(c, v, err)
}

func (m *Module) removeSession(c *gin.Context) {
	respond(c, true, m.Svc.Auth.RemoveSession(c.Request.Context(), uid(c), c.Param("id")))
}

func (m *Module) unbindTelegram(c *gin.Context) {
	respond(c, true, m.Svc.UnbindTelegram(c.Request.Context(), uid(c)))
}

func (m *Module) transfer(c *gin.Context) {
	var in struct {
		Amount int64 `json:"amount"`
	}
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.TransferCommission(c.Request.Context(), uid(c), in.Amount))
}

func (m *Module) redeem(c *gin.Context) {
	var in RedeemInput
	if !bind(c, &in) {
		return
	}
	v, err := m.Svc.RedeemGiftcard(c.Request.Context(), uid(c), in)
	respond(c, v, err)
}

func (m *Module) subscriptions(c *gin.Context) {
	v, err := m.Svc.Subscriptions.List(c.Request.Context(), uid(c), baseURL(c))
	respond(c, v, err)
}

func (m *Module) subscription(c *gin.Context) {
	id, ok := subID(c)
	if !ok {
		return
	}
	v, err := m.Svc.Subscriptions.Get(c.Request.Context(), uid(c), id, baseURL(c))
	respond(c, v, err)
}

func (m *Module) updateSubscription(c *gin.Context) {
	id, ok := subID(c)
	if !ok {
		return
	}
	var in subscription.UpdateInput
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.Subscriptions.Update(c.Request.Context(), uid(c), id, in))
}

func (m *Module) resetSecurity(c *gin.Context) {
	id, ok := subID(c)
	if !ok {
		return
	}
	v, err := m.Svc.Subscriptions.ResetSecurity(c.Request.Context(), uid(c), id, baseURL(c))
	respond(c, v, err)
}

// ---------- 管理端礼品卡 ----------

func (m *Module) adminGiftcards(c *gin.Context) {
	var p GiftcardPage
	if err := c.ShouldBindQuery(&p); err != nil {
		httpx.Fail(c, errInvalidParams)
		return
	}
	v, err := m.Svc.AdminGiftcards(c.Request.Context(), p)
	respond(c, v, err)
}

func (m *Module) adminCreateGiftcard(c *gin.Context) {
	var in GiftcardInput
	if !bind(c, &in) {
		return
	}
	in.ID = 0
	respond(c, true, m.Svc.SaveGiftcard(c.Request.Context(), in))
}

func (m *Module) adminUpdateGiftcard(c *gin.Context) {
	id, ok := subID(c)
	if !ok {
		return
	}
	var in GiftcardInput
	if !bind(c, &in) {
		return
	}
	in.ID = id
	respond(c, true, m.Svc.SaveGiftcard(c.Request.Context(), in))
}

// adminGenerateGiftcards 批量生成，直接返回 CSV 文件（带 UTF-8 BOM，便于 Excel 打开）。
func (m *Module) adminGenerateGiftcards(c *gin.Context) {
	var in struct {
		GiftcardInput
		Count int `json:"count"`
	}
	if !bind(c, &in) {
		return
	}
	b, err := m.Svc.GenerateGiftcards(c.Request.Context(), in.GiftcardInput, in.Count)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	c.Header("Content-Disposition", `attachment; filename="giftcards.csv"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", append([]byte("\xEF\xBB\xBF"), b...))
}

func (m *Module) adminDeleteGiftcard(c *gin.Context) {
	id, ok := subID(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.DeleteGiftcard(c.Request.Context(), id))
}

func (m *Module) newPeriod(c *gin.Context) {
	id, ok := subID(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.Subscriptions.NewPeriod(c.Request.Context(), uid(c), id))
}
