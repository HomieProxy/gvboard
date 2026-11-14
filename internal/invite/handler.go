package invite

import (
	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

// Module 邀请路由。
type Module struct {
	Svc  *Service
	Auth *auth.Service
}

// Register 挂载原生 API（均需登录）：
//
//	GET  {api}/user/invite               未使用的邀请码与邀请统计
//	POST {api}/user/invite/codes         生成邀请码
//	GET  {api}/user/invite/commissions   佣金明细，参数 current、page_size
func (m *Module) Register(r *server.Router) {
	g := r.API.Group("/user/invite", m.Auth.RequireUser())
	g.GET("", m.fetch)
	g.POST("/codes", m.generate)
	g.GET("/commissions", m.details)
}

func respond(c *gin.Context, data any, err error) {
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, data)
}

func uid(c *gin.Context) int64 { return auth.Current(c).ID }

func (m *Module) fetch(c *gin.Context) {
	v, err := m.Svc.Fetch(c.Request.Context(), uid(c))
	respond(c, v, err)
}

func (m *Module) generate(c *gin.Context) {
	v, err := m.Svc.Generate(c.Request.Context(), uid(c))
	respond(c, v, err)
}

func (m *Module) details(c *gin.Context) {
	var p Page
	if err := c.ShouldBindQuery(&p); err != nil {
		httpx.Fail(c, httpx.Validation("Invalid parameters"))
		return
	}
	v, err := m.Svc.Details(c.Request.Context(), uid(c), p)
	respond(c, v, err)
}
