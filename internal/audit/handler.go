package audit

import (
	"errors"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

// Module 审计规则管理路由。节点拉取规则与上报命中在节点接口（nodeapi）中。
type Module struct {
	Svc  *Service
	Auth *auth.Service
}

// Register 挂载管理端 API（{api}/{secure_path}）：
//
//	GET    audit/rules          规则列表
//	POST   audit/rules          新建规则
//	PUT    audit/rules/:id      修改规则
//	DELETE audit/rules/:id      删除规则
//	POST   audit/test           测试目标命中哪些规则，{"target":"example.com:443"}
//	GET    audit/logs           审计日志，参数见 LogQuery
//	POST   audit/logs/clean     立即清理超过保留天数的审计日志
func (m *Module) Register(r *server.Router) {
	a := m.Auth.AdminGroup(r.API, func() string { return m.Svc.Settings.String("secure_path") })
	a.GET("/audit/rules", m.rules)
	a.POST("/audit/rules", m.saveRule)
	a.PUT("/audit/rules/:id", m.saveRule)
	a.DELETE("/audit/rules/:id", m.deleteRule)
	a.POST("/audit/test", m.test)
	a.GET("/audit/logs", m.logs)
	a.POST("/audit/logs/clean", m.clean)
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

// optionalID 新建时没有 :id，返回 0；有 :id 时必须是正整数。
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

func (m *Module) rules(c *gin.Context) {
	v, err := m.Svc.Rules(c.Request.Context())
	respond(c, v, err)
}

func (m *Module) saveRule(c *gin.Context) {
	id, ok := optionalID(c)
	if !ok {
		return
	}
	var in RuleInput
	if !bind(c, &in) {
		return
	}
	v, err := m.Svc.SaveRule(c.Request.Context(), id, in)
	respond(c, v, err)
}

func (m *Module) deleteRule(c *gin.Context) {
	id, ok := optionalID(c)
	if !ok {
		return
	}
	if id == 0 {
		httpx.Fail(c, errInvalid)
		return
	}
	respond(c, true, m.Svc.DeleteRule(c.Request.Context(), id))
}

func (m *Module) test(c *gin.Context) {
	var in struct {
		Target string `json:"target"`
	}
	if !bind(c, &in) {
		return
	}
	v, err := m.Svc.Test(c.Request.Context(), in.Target)
	respond(c, v, err)
}

func (m *Module) logs(c *gin.Context) {
	var q LogQuery
	if err := c.ShouldBindQuery(&q); err != nil {
		httpx.Fail(c, errInvalid)
		return
	}
	v, err := m.Svc.Logs(c.Request.Context(), q)
	respond(c, v, err)
}

func (m *Module) clean(c *gin.Context) {
	n, err := m.Svc.CleanLogs(c.Request.Context())
	respond(c, map[string]int64{"deleted": n}, err)
}
