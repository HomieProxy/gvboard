package machine

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/netip"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/nodeapi"
	"gvboard/internal/server"
)

var errInvalid = httpx.Validation("参数有误")

// Module 节点机路由。
type Module struct {
	Svc  *Service
	Auth *auth.Service
	// Allow 节点接口来源 IP 白名单（与节点接口共用 node.ip_whitelist），为空不限制。
	Allow []netip.Prefix
}

// Register 挂载路由。
//
// 管理端（{api}/{secure_path}）：
//
//	GET    machines              节点机列表
//	POST   machines              新建，返回一次性接入码
//	PUT    machines/:id          修改名称、节点、审计、日志级别
//	POST   machines/:id/enroll   重新生成接入码（同时吊销现有凭据）
//	POST   machines/:id/revoke   吊销凭据
//	DELETE machines/:id          删除
//	GET    machines/releases     查询可更新到的 v2node 版本（请求 GitHub）
//	POST   machines/update       {"ids","version"} 下发 v2node 更新
//	POST   machines/:id/update/cancel  取消待执行的更新
//
// 节点机（未开启节点机、来源 IP 不在白名单、接入码或凭据无效时统一 404）：
//
//	POST /api/v2/server/machine/enroll  {"code","hostname","version"} 换取凭据
//	GET  /api/v2/server/machine/config  下发配置，凭据放在 Authorization: Bearer 或参数 token，支持 ETag；
//	                                    参数 update_id、update_error 上报上次更新失败的原因
//	POST /api/v2/server/machine/node-errors  {"errors":[{"node_id","error"}]} 上报被跳过的节点，空列表表示全部恢复，成功 204
func (m *Module) Register(r *server.Router) {
	a := m.Auth.AdminGroup(r.API, func() string { return m.Svc.Settings.String("secure_path") })
	a.GET("/machines", m.list)
	a.GET("/machines/releases", m.releases)
	a.POST("/machines/update", m.requestUpdate)
	a.POST("/machines/:id/update/cancel", m.cancelUpdate)
	a.POST("/machines", m.create)
	a.PUT("/machines/:id", m.update)
	a.POST("/machines/:id/enroll", m.reenroll)
	a.POST("/machines/:id/revoke", m.revoke)
	a.DELETE("/machines/:id", m.remove)

	n := r.NodeV2.Group("/machine", m.guard)
	n.POST("/enroll", m.enroll)
	n.GET("/config", m.config)
	n.POST("/node-errors", m.nodeErrors)
}

func (m *Module) guard(c *gin.Context) {
	if !nodeapi.AllowIP(m.Allow, c.ClientIP()) || !m.Svc.Enabled() {
		httpx.NotFound(c)
		return
	}
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 64<<10)
	c.Next()
}

func respond(c *gin.Context, data any, err error) {
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, data)
}

func pathID(c *gin.Context) (int64, bool) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, errInvalid)
		return 0, false
	}
	return id, true
}

func bindInput(c *gin.Context) (Input, bool) {
	var in Input
	if err := c.ShouldBindJSON(&in); err != nil && !errors.Is(err, io.EOF) {
		httpx.Fail(c, errInvalid)
		return in, false
	}
	return in, true
}

func (m *Module) list(c *gin.Context) {
	v, err := m.Svc.List(c.Request.Context())
	respond(c, v, err)
}

func (m *Module) create(c *gin.Context) {
	in, ok := bindInput(c)
	if !ok {
		return
	}
	v, err := m.Svc.Create(c.Request.Context(), in)
	respond(c, v, err)
}

func (m *Module) update(c *gin.Context) {
	id, ok := pathID(c)
	if !ok {
		return
	}
	in, ok := bindInput(c)
	if !ok {
		return
	}
	v, err := m.Svc.Update(c.Request.Context(), id, in)
	respond(c, v, err)
}

func (m *Module) reenroll(c *gin.Context) {
	if id, ok := pathID(c); ok {
		v, err := m.Svc.Reenroll(c.Request.Context(), id)
		respond(c, v, err)
	}
}

func (m *Module) revoke(c *gin.Context) {
	if id, ok := pathID(c); ok {
		respond(c, true, m.Svc.Revoke(c.Request.Context(), id))
	}
}

func (m *Module) remove(c *gin.Context) {
	if id, ok := pathID(c); ok {
		respond(c, true, m.Svc.Delete(c.Request.Context(), id))
	}
}

func (m *Module) releases(c *gin.Context) {
	v, err := m.Svc.Releases(c.Request.Context())
	respond(c, v, err)
}

func (m *Module) requestUpdate(c *gin.Context) {
	var in struct {
		IDs     []int64 `json:"ids"`
		Version string  `json:"version"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, errInvalid)
		return
	}
	n, err := m.Svc.RequestUpdate(c.Request.Context(), in.IDs, in.Version)
	respond(c, n, err)
}

func (m *Module) cancelUpdate(c *gin.Context) {
	if id, ok := pathID(c); ok {
		respond(c, true, m.Svc.CancelUpdate(c.Request.Context(), id))
	}
}

func (m *Module) enroll(c *gin.Context) {
	var in struct {
		Code     string `json:"code"`
		Hostname string `json:"hostname"`
		Version  string `json:"version"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.NotFound(c)
		return
	}
	v, err := m.Svc.Enroll(c.Request.Context(), strings.TrimSpace(in.Code), in.Hostname, in.Version, c.ClientIP())
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if v == nil {
		httpx.NotFound(c)
		return
	}
	c.JSON(http.StatusOK, v)
}

// Token 读取节点机凭据：Authorization: Bearer 优先，其次参数 token。
func Token(c *gin.Context) string {
	if h := c.GetHeader("Authorization"); len(h) > 7 && strings.EqualFold(h[:7], "Bearer ") {
		return strings.TrimSpace(h[7:])
	}
	return c.Query("token")
}

func (m *Module) config(c *gin.Context) {
	mc := m.Svc.Auth(c.Request.Context(), Token(c))
	if mc == nil {
		httpx.NotFound(c)
		return
	}
	rep := Report{Error: c.Query("update_error")}
	rep.ID, _ = strconv.ParseInt(c.Query("update_id"), 10, 64)
	cfg, err := m.Svc.Config(c.Request.Context(), mc, c.ClientIP(), c.Query("version"), rep)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	body, err := json.Marshal(cfg)
	if err != nil {
		httpx.Fail(c, httpx.Internal(err))
		return
	}
	sum := sha256.Sum256(body)
	etag := `"` + hex.EncodeToString(sum[:8]) + `"`
	c.Header("ETag", etag)
	if c.GetHeader("If-None-Match") == etag {
		c.Status(http.StatusNotModified)
		return
	}
	c.Data(http.StatusOK, "application/json", body)
}

func (m *Module) nodeErrors(c *gin.Context) {
	mc := m.Svc.Auth(c.Request.Context(), Token(c))
	if mc == nil {
		httpx.NotFound(c)
		return
	}
	var in struct {
		Errors []NodeError `json:"errors"`
	}
	if err := c.ShouldBindJSON(&in); err != nil {
		httpx.Fail(c, errInvalid)
		return
	}
	if err := m.Svc.ReportNodeErrors(c.Request.Context(), mc, in.Errors); err != nil {
		httpx.Fail(c, err)
		return
	}
	c.Status(http.StatusNoContent)
}
