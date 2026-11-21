package adminuser

import (
	"errors"
	"io"
	"net/http"
	"strconv"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

// Module 管理端用户与订阅路由。
type Module struct {
	Svc *Service
}

// Register 挂载管理端原生 API（{api}/{secure_path}，决策 D20）：
//
//	POST   users/query                        用户列表（筛选、排序、分页）
//	POST   users/generate                     生成用户（批量时返回 CSV）
//	POST   users/export                       导出 CSV（每个订阅一行）
//	POST   users/mail                         群发邮件
//	POST   users/ban                          批量封禁
//	POST   users/batch-delete                 批量删除
//	GET    users/:id                          用户详情（含订阅与在线 IP）
//	PUT    users/:id                          修改账号字段
//	DELETE users/:id                          删除用户
//	POST   users/:id/inviter                  设置 / 清除邀请人
//	GET    users/:id/balance-logs             余额变动日志（type、current、page_size）
//	POST   users/:id/subscriptions            新开订阅
//	PUT    subscriptions/:id                  修改订阅
//	DELETE subscriptions/:id                  删除订阅
//	POST   subscriptions/:id/reset-security   重置 uuid 与 token
//	POST   subscriptions/:id/primary          设为主订阅
func (m *Module) Register(r *server.Router) {
	a := m.Svc.Auth.AdminGroup(r.API, func() string { return m.Svc.Settings.String("secure_path") })
	a.POST("/users/query", m.list)
	a.POST("/users/generate", m.generate)
	a.POST("/users/export", m.export)
	a.POST("/users/mail", m.mail)
	a.POST("/users/ban", m.ban)
	a.POST("/users/batch-delete", m.batchDelete)
	a.GET("/users/:id", m.get)
	a.PUT("/users/:id", m.update)
	a.DELETE("/users/:id", m.delete)
	a.POST("/users/:id/inviter", m.inviter)
	a.GET("/users/:id/balance-logs", m.balanceLogs)
	a.POST("/users/:id/subscriptions", m.createSub)
	a.PUT("/subscriptions/:id", m.updateSub)
	a.DELETE("/subscriptions/:id", m.deleteSub)
	a.POST("/subscriptions/:id/reset-security", m.resetSub)
	a.POST("/subscriptions/:id/primary", m.primary)
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

func idParam(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, errInvalid)
		return 0, false
	}
	return id, true
}

func actor(c *gin.Context) int64 { return auth.Current(c).ID }

// baseURL 未设置订阅域名时，订阅链接使用当前请求的地址。
func baseURL(c *gin.Context) string { return httpx.BaseURL(c) }

func sendCSV(c *gin.Context, name string, b []byte) {
	c.Header("Content-Disposition", `attachment; filename="`+name+`"`)
	c.Data(http.StatusOK, "text/csv; charset=utf-8", append([]byte("\xEF\xBB\xBF"), b...))
}

type filterBody struct {
	Filter []Filter `json:"filter"`
}

func (m *Module) list(c *gin.Context) {
	var in Query
	if !bind(c, &in) {
		return
	}
	v, err := m.Svc.List(c.Request.Context(), in, baseURL(c))
	respond(c, v, err)
}

func (m *Module) generate(c *gin.Context) {
	var in GenerateInput
	if !bind(c, &in) {
		return
	}
	b, err := m.Svc.Generate(c.Request.Context(), in, baseURL(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if b == nil {
		httpx.OK(c, true)
		return
	}
	sendCSV(c, "users.csv", b)
}

func (m *Module) export(c *gin.Context) {
	var in filterBody
	if !bind(c, &in) {
		return
	}
	b, err := m.Svc.Export(c.Request.Context(), in.Filter, baseURL(c))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	sendCSV(c, "users.csv", b)
}

func (m *Module) mail(c *gin.Context) {
	var in MailInput
	if !bind(c, &in) {
		return
	}
	n, err := m.Svc.SendMail(c.Request.Context(), in)
	respond(c, gin.H{"count": n}, err)
}

func (m *Module) ban(c *gin.Context) {
	var in filterBody
	if !bind(c, &in) {
		return
	}
	n, err := m.Svc.Ban(c.Request.Context(), actor(c), in.Filter)
	respond(c, gin.H{"count": n}, err)
}

func (m *Module) batchDelete(c *gin.Context) {
	var in filterBody
	if !bind(c, &in) {
		return
	}
	n, err := m.Svc.BatchDelete(c.Request.Context(), actor(c), in.Filter)
	respond(c, gin.H{"count": n}, err)
}

func (m *Module) get(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	v, err := m.Svc.Get(c.Request.Context(), id, baseURL(c))
	respond(c, v, err)
}

func (m *Module) update(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in UpdateInput
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.Update(c.Request.Context(), actor(c), id, in))
}

func (m *Module) delete(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.Delete(c.Request.Context(), actor(c), id))
}

func (m *Module) inviter(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in struct {
		InviteUserEmail string `json:"invite_user_email"`
	}
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.SetInviter(c.Request.Context(), id, in.InviteUserEmail))
}

func (m *Module) balanceLogs(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var q BalanceLogQuery
	if c.ShouldBindQuery(&q) != nil {
		httpx.Fail(c, errInvalid)
		return
	}
	v, err := m.Svc.BalanceLogs(c.Request.Context(), id, q)
	respond(c, v, err)
}

func (m *Module) createSub(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in SubInput
	if !bind(c, &in) {
		return
	}
	v, err := m.Svc.CreateSub(c.Request.Context(), id, in)
	respond(c, v, err)
}

func (m *Module) updateSub(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	var in SubInput
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.UpdateSub(c.Request.Context(), id, in))
}

func (m *Module) deleteSub(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.DeleteSub(c.Request.Context(), id))
}

func (m *Module) resetSub(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.ResetSub(c.Request.Context(), id))
}

func (m *Module) primary(c *gin.Context) {
	id, ok := idParam(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.SetPrimary(c.Request.Context(), id))
}
