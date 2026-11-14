package passport

import (
	"errors"
	"io"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/config"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

// Module 认证模块路由。
type Module struct {
	Svc *Service
}

// Register 挂载原生 API：
//
//	POST {api}/auth/register        注册
//	POST {api}/auth/login           密码登录
//	POST {api}/auth/reset-password  通过邮箱验证码重置密码
//	POST {api}/auth/email-code      发送邮箱验证码
//	POST {api}/auth/code-login      快捷登录码登录
//	POST {api}/auth/quick-login     生成快捷登录链接（需登录）
//	POST {api}/invite/visit         邀请链接访问统计
//	GET  {api}/site                 公开站点配置（仅分离部署）
func (m *Module) Register(r *server.Router) {
	g := r.API
	g.POST("/auth/register", m.register)
	g.POST("/auth/login", m.login)
	g.POST("/auth/reset-password", m.resetPassword)
	g.POST("/auth/email-code", m.emailCode)
	g.POST("/auth/code-login", m.codeLogin)
	g.POST("/auth/quick-login", m.Svc.Auth.RequireUser(), m.quickLogin)
	g.POST("/invite/visit", m.inviteVisit)
	if r.Config.Server.Mode == config.ModeAPI {
		g.GET("/site", m.site)
	}
}

func (m *Module) client(c *gin.Context) Client {
	return Client{IP: c.ClientIP(), UA: c.Request.UserAgent(), Locale: m.Svc.I18n.Locale(c)}
}

func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBind(v); err != nil && !errors.Is(err, io.EOF) {
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

func (m *Module) register(c *gin.Context) {
	var in RegisterInput
	if !bind(c, &in) {
		return
	}
	res, err := m.Svc.Register(c.Request.Context(), in, m.client(c))
	respond(c, res, err)
}

func (m *Module) login(c *gin.Context) {
	var in LoginInput
	if !bind(c, &in) {
		return
	}
	res, err := m.Svc.Login(c.Request.Context(), in, m.client(c))
	respond(c, res, err)
}

func (m *Module) resetPassword(c *gin.Context) {
	var in ResetInput
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.ResetPassword(c.Request.Context(), in))
}

func (m *Module) emailCode(c *gin.Context) {
	var in EmailCodeInput
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.SendEmailCode(c.Request.Context(), in, m.client(c)))
}

func (m *Module) codeLogin(c *gin.Context) {
	var in struct {
		Code string `json:"code" form:"code"`
	}
	if !bind(c, &in) {
		return
	}
	res, err := m.Svc.CodeLogin(c.Request.Context(), in.Code, m.client(c))
	respond(c, res, err)
}

func (m *Module) quickLogin(c *gin.Context) {
	var in struct {
		Redirect string `json:"redirect" form:"redirect"`
	}
	if !bind(c, &in) {
		return
	}
	u, err := m.Svc.QuickLoginURL(c.Request.Context(), auth.Current(c).ID, in.Redirect, httpx.BaseURL(c))
	respond(c, u, err)
}

func (m *Module) inviteVisit(c *gin.Context) {
	var in struct {
		Code string `json:"code" form:"code"`
	}
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.InviteVisit(c.Request.Context(), in.Code))
}

func (m *Module) site(c *gin.Context) {
	httpx.OK(c, BuildSiteConfig(m.Svc.Settings))
}
