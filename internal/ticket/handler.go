package ticket

import (
	"errors"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

// Module 工单路由。
type Module struct {
	Svc  *Service
	Auth *auth.Service
	// Locale 返回当前请求语言，提现工单按该语言写入。
	Locale func(c *gin.Context) string
}

// Register 挂载原生 API。
//
// 用户端（需登录）：
//
//	GET  {api}/user/tickets              我的工单
//	GET  {api}/user/tickets/:id          工单详情（含消息）
//	POST {api}/user/tickets              新建工单，{"subject","level","message","subscription_id"?}
//	POST {api}/user/tickets/:id/reply    回复，{"message"}
//	POST {api}/user/tickets/:id/close    关闭
//	POST {api}/user/withdrawals          佣金提现申请，{"withdraw_method","withdraw_account"}
//
// 管理端：
//
//	GET  tickets                 列表，参数 status、reply_status（可多个）、email、current、page_size
//	GET  tickets/:id             详情
//	POST tickets/:id/reply       回复，{"message"}
//	POST tickets/:id/close       关闭
func (m *Module) Register(r *server.Router) {
	u := r.API.Group("/user", m.Auth.RequireUser())
	u.GET("/tickets", m.list)
	u.GET("/tickets/:id", m.get)
	u.POST("/tickets", m.create)
	u.POST("/tickets/:id/reply", m.reply)
	u.POST("/tickets/:id/close", m.close)
	u.POST("/withdrawals", m.withdraw)

	a := m.Auth.AdminGroup(r.API, func() string { return m.Svc.Settings.String("secure_path") })
	a.GET("/tickets", m.adminList)
	a.GET("/tickets/:id", m.adminGet)
	a.POST("/tickets/:id/reply", m.adminReply)
	a.POST("/tickets/:id/close", m.adminClose)
}

func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil && !errors.Is(err, io.EOF) {
		httpx.Fail(c, errInvalid)
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

func idParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, errInvalid)
		return 0, false
	}
	return id, true
}

type messageInput struct {
	Message string `json:"message"`
}

func (m *Module) locale(c *gin.Context) string {
	if m.Locale == nil {
		return ""
	}
	return m.Locale(c)
}

func (m *Module) list(c *gin.Context) {
	v, err := m.Svc.List(c.Request.Context(), uid(c))
	respond(c, v, err)
}

func (m *Module) get(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	v, err := m.Svc.Get(c.Request.Context(), uid(c), id)
	respond(c, v, err)
}

func (m *Module) create(c *gin.Context) {
	var in CreateInput
	if !bind(c, &in) {
		return
	}
	v, err := m.Svc.Create(c.Request.Context(), uid(c), in, c.ClientIP())
	respond(c, v, err)
}

func (m *Module) reply(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in messageInput
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.Reply(c.Request.Context(), uid(c), id, in.Message, c.ClientIP()))
}

func (m *Module) close(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.Close(c.Request.Context(), uid(c), id))
}

func (m *Module) withdraw(c *gin.Context) {
	var in WithdrawInput
	if !bind(c, &in) {
		return
	}
	v, err := m.Svc.Withdraw(c.Request.Context(), uid(c), in, m.locale(c), c.ClientIP())
	respond(c, v, err)
}

func (m *Module) adminList(c *gin.Context) {
	var q AdminQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		httpx.Fail(c, errInvalid)
		return
	}
	v, err := m.Svc.AdminList(c.Request.Context(), q)
	respond(c, v, err)
}

func (m *Module) adminGet(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	v, err := m.Svc.AdminGet(c.Request.Context(), id)
	respond(c, v, err)
}

func (m *Module) adminReply(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in messageInput
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.ReplyByAdmin(c.Request.Context(), uid(c), id, in.Message))
}

func (m *Module) adminClose(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.Close(c.Request.Context(), 0, id))
}
