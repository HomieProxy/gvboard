package node

import (
	"errors"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

// maxBody 节点保存请求体上限（证书、ECH 密钥等文本也在其中）。
const maxBody = 1 << 20

// Module 节点管理路由。
type Module struct {
	Svc  *Service
	Auth *auth.Service
}

// Register 挂载管理端原生 API（{api}/{secure_path}，决策 D20）：
//
//	GET    servers                         节点总览
//	POST   servers/sort                    批量排序，{类型: {节点ID: 排序值}}
//	GET    servers/groups                  权限组列表
//	POST   servers/groups                  新建权限组
//	PUT    servers/groups/:id              修改权限组
//	DELETE servers/groups/:id              删除权限组
//	GET    servers/routes                  路由规则列表
//	POST   servers/routes                  新建路由规则
//	PUT    servers/routes/:id              修改路由规则
//	DELETE servers/routes/:id              删除路由规则
//	POST   servers/:type                   新建节点
//	PUT    servers/:type/:id               修改节点
//	DELETE servers/:type/:id               删除节点
//	POST   servers/:type/:id/show          显示 / 隐藏节点，{"show": 0|1}
//	POST   servers/:type/:id/copy          复制节点
func (m *Module) Register(r *server.Router) {
	a := m.Auth.AdminGroup(r.API, func() string { return m.Svc.Settings.String("secure_path") })
	a.GET("/servers", m.overview)
	a.POST("/servers/sort", m.sort)
	a.GET("/servers/groups", m.groups)
	a.POST("/servers/groups", m.saveGroup)
	a.PUT("/servers/groups/:id", m.saveGroup)
	a.DELETE("/servers/groups/:id", m.deleteGroup)
	a.GET("/servers/routes", m.routes)
	a.POST("/servers/routes", m.saveRoute)
	a.PUT("/servers/routes/:id", m.saveRoute)
	a.DELETE("/servers/routes/:id", m.deleteRoute)
	a.POST("/servers/:type", m.save)
	a.PUT("/servers/:type/:id", m.save)
	a.DELETE("/servers/:type/:id", m.delete)
	a.POST("/servers/:type/:id/show", m.show)
	a.POST("/servers/:type/:id/copy", m.copy)
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

// optionalID 新建路由没有 :id，返回 0；修改路由的 :id 必须是正整数。
func optionalID(c *gin.Context) (int64, bool) {
	raw := c.Param("id")
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, errInvalid)
		return 0, false
	}
	return id, true
}

func requiredID(c *gin.Context) (int64, bool) {
	if c.Param("id") == "" {
		httpx.Fail(c, errInvalid)
		return 0, false
	}
	return optionalID(c)
}

func (m *Module) overview(c *gin.Context) {
	v, err := m.Svc.Overview(c.Request.Context())
	respond(c, v, err)
}

func (m *Module) sort(c *gin.Context) {
	var in map[string]map[string]int64
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.Sort(c.Request.Context(), in))
}

func (m *Module) groups(c *gin.Context) {
	v, err := m.Svc.Groups(c.Request.Context())
	respond(c, v, err)
}

func (m *Module) saveGroup(c *gin.Context) {
	id, ok := optionalID(c)
	if !ok {
		return
	}
	var in struct {
		Name string `json:"name"`
	}
	if !bind(c, &in) {
		return
	}
	v, err := m.Svc.SaveGroup(c.Request.Context(), id, in.Name)
	respond(c, v, err)
}

func (m *Module) deleteGroup(c *gin.Context) {
	id, ok := requiredID(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.DeleteGroup(c.Request.Context(), id))
}

func (m *Module) routes(c *gin.Context) {
	v, err := m.Svc.Routes(c.Request.Context())
	respond(c, v, err)
}

func (m *Module) saveRoute(c *gin.Context) {
	id, ok := optionalID(c)
	if !ok {
		return
	}
	var in RouteInput
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.SaveRoute(c.Request.Context(), id, in))
}

func (m *Module) deleteRoute(c *gin.Context) {
	id, ok := requiredID(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.DeleteRoute(c.Request.Context(), id))
}

func (m *Module) save(c *gin.Context) {
	id, ok := optionalID(c)
	if !ok {
		return
	}
	body, err := io.ReadAll(io.LimitReader(c.Request.Body, maxBody+1))
	if err != nil || len(body) > maxBody {
		httpx.Fail(c, errInvalid)
		return
	}
	respond(c, true, m.Svc.Save(c.Request.Context(), trimType(c.Param("type")), id, body))
}

func (m *Module) delete(c *gin.Context) {
	id, ok := requiredID(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.Delete(c.Request.Context(), trimType(c.Param("type")), id))
}

func (m *Module) show(c *gin.Context) {
	id, ok := requiredID(c)
	if !ok {
		return
	}
	var in struct {
		Show *int `json:"show"`
	}
	if !bind(c, &in) {
		return
	}
	if in.Show == nil {
		httpx.Fail(c, errShowInvalid)
		return
	}
	respond(c, true, m.Svc.SetShow(c.Request.Context(), trimType(c.Param("type")), id, *in.Show))
}

func (m *Module) copy(c *gin.Context) {
	id, ok := requiredID(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.Copy(c.Request.Context(), trimType(c.Param("type")), id))
}
