package payment

import (
	"errors"
	"io"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

// Module 支付路由。
type Module struct {
	Svc  *Service
	Auth *auth.Service
}

// Register 挂载原生 API。
//
// 用户端（需登录）：
//
//	GET  {api}/user/payment-methods                     可用的支付方式
//	GET  {api}/user/payment-methods/:id/stripe-key      StripeCredit 公钥
//
// 支付回调（访客）：
//
//	GET|POST {api}/payment/notify/:method/:uuid
//	GET|POST /api/v1/guest/payment/notify/:method/:uuid  原路径，仅 compat.legacy_callback 开启时挂载
//
// 管理端（{api}/{secure_path}，决策 D20）：
//
//	GET  payments/gateways / GET payments / GET payments/form?payment=&id=
//	POST payments / PUT payments/:id / POST payments/:id/toggle / DELETE payments/:id / POST payments/sort
func (m *Module) Register(r *server.Router) {
	u := r.API.Group("/user", m.Auth.RequireUser())
	u.GET("/payment-methods", m.methods)
	u.GET("/payment-methods/:id/stripe-key", m.stripeKey)

	r.API.GET("/payment/notify/:method/:uuid", m.notify)
	r.API.POST("/payment/notify/:method/:uuid", m.notify)
	if r.Config.Compat.LegacyCallback {
		r.LegacyRoot.GET("/guest/payment/notify/:method/:uuid", m.notify)
		r.LegacyRoot.POST("/guest/payment/notify/:method/:uuid", m.notify)
	}

	a := m.Auth.AdminGroup(r.API, func() string { return m.Svc.Settings.String("secure_path") })
	a.GET("/payments/gateways", func(c *gin.Context) { httpx.OK(c, Names()) })
	a.GET("/payments", m.adminList)
	a.GET("/payments/form", m.adminForm)
	a.POST("/payments", m.adminCreate)
	a.PUT("/payments/:id", m.adminUpdate)
	a.POST("/payments/:id/toggle", m.adminToggle)
	a.DELETE("/payments/:id", m.adminDelete)
	a.POST("/payments/sort", m.adminSort)
}

func respond(c *gin.Context, data any, err error) {
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, data)
}

func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil && !errors.Is(err, io.EOF) {
		httpx.Fail(c, errAdminInvalid)
		return false
	}
	return true
}

func idParam(c *gin.Context, raw string) (int64, bool) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, errAdminInvalid)
		return 0, false
	}
	return id, true
}

func (m *Module) methods(c *gin.Context) {
	v, err := m.Svc.Methods(c.Request.Context())
	respond(c, v, err)
}

func (m *Module) stripeKey(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil {
		httpx.Fail(c, errStripeNotFound)
		return
	}
	v, err := m.Svc.StripePublicKey(c.Request.Context(), id)
	respond(c, v, err)
}

// notify 支付回调。成功时按网关要求返回应答内容，任何失败都返回统一 404（决策 D29）。
func (m *Module) notify(c *gin.Context) {
	body, err := io.ReadAll(c.Request.Body)
	if err != nil {
		httpx.NotFound(c)
		return
	}
	n := &Notification{Params: parseParams(c.Request, body), Body: body, Header: c.Request.Header}
	reply, err := m.Svc.Notify(c.Request.Context(), c.Param("method"), c.Param("uuid"), n)
	if err != nil {
		httpx.NotFound(c)
		return
	}
	ct := "text/plain; charset=utf-8"
	if strings.HasPrefix(reply, "<xml>") {
		ct = "text/xml; charset=utf-8"
	}
	c.Data(http.StatusOK, ct, []byte(reply))
}

// ---------- 管理端 ----------

func (m *Module) adminList(c *gin.Context) {
	v, err := m.Svc.AdminList(c.Request.Context(), m.Svc.SiteURL(c.Request))
	respond(c, v, err)
}

func (m *Module) adminForm(c *gin.Context) {
	var id int64
	if s := c.Query("id"); s != "" {
		var ok bool
		if id, ok = idParam(c, s); !ok {
			return
		}
	}
	v, err := m.Svc.AdminForm(c.Request.Context(), c.Query("payment"), id)
	respond(c, v, err)
}

func (m *Module) adminCreate(c *gin.Context) {
	var in SaveInput
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.Save(c.Request.Context(), in))
}

func (m *Module) adminUpdate(c *gin.Context) {
	id, ok := idParam(c, c.Param("id"))
	if !ok {
		return
	}
	var in SaveInput
	if !bind(c, &in) {
		return
	}
	in.ID = id
	respond(c, true, m.Svc.Save(c.Request.Context(), in))
}

func (m *Module) adminToggle(c *gin.Context) {
	id, ok := idParam(c, c.Param("id"))
	if !ok {
		return
	}
	respond(c, true, m.Svc.Toggle(c.Request.Context(), id))
}

func (m *Module) adminDelete(c *gin.Context) {
	id, ok := idParam(c, c.Param("id"))
	if !ok {
		return
	}
	respond(c, true, m.Svc.Delete(c.Request.Context(), id))
}

func (m *Module) adminSort(c *gin.Context) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.Sort(c.Request.Context(), in.IDs))
}
