package theme

import (
	"encoding/json"
	"errors"
	"io"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

// maxBody 保存主题配置的请求体上限。
const maxBody = 1 << 20

// Module 主题管理路由。
type Module struct {
	Svc  *Manager
	Auth *auth.Service
}

// View 主题列表项。
type View struct {
	ID string `json:"id"`
	Manifest
	Builtin bool `json:"builtin"`
}

// Register 挂载管理端接口（{api}/{secure_path}）：
//
//	GET themes              主题列表与当前主题 {"themes":[...],"active":"default"}
//	PUT themes/active       切换主题 {"id":"default"}
//	GET themes/:id/config   主题配置（未保存的项为默认值）
//	PUT themes/:id/config   保存主题配置 {"字段名": 值, ...}，返回保存后的配置
func (m *Module) Register(r *server.Router) {
	a := m.Auth.AdminGroup(r.API, func() string { return m.Svc.Settings.String("secure_path") })
	a.GET("/themes", m.list)
	a.PUT("/themes/active", m.activate)
	a.GET("/themes/:id/config", m.config)
	a.PUT("/themes/:id/config", m.saveConfig)
}

func (m *Module) list(c *gin.Context) {
	themes := m.Svc.List()
	out := make([]View, 0, len(themes))
	for _, t := range themes {
		out = append(out, View{ID: t.ID, Manifest: t.Manifest, Builtin: t.Builtin})
	}
	httpx.OK(c, gin.H{"themes": out, "active": m.Svc.ActiveID()})
}

func (m *Module) activate(c *gin.Context) {
	var req struct {
		ID string `json:"id"`
	}
	if err := c.ShouldBindJSON(&req); err != nil {
		httpx.Fail(c, httpx.Validation("参数有误"))
		return
	}
	if err := m.Svc.Activate(c.Request.Context(), req.ID); err != nil {
		httpx.Fail(c, failure(err))
		return
	}
	httpx.OK(c, true)
}

func (m *Module) config(c *gin.Context) {
	t, err := m.Svc.Get(c.Param("id"))
	if err != nil {
		httpx.Fail(c, failure(err))
		return
	}
	httpx.OK(c, m.Svc.Config(t))
}

func (m *Module) saveConfig(c *gin.Context) {
	var values map[string]any
	dec := json.NewDecoder(io.LimitReader(c.Request.Body, maxBody))
	dec.UseNumber()
	if err := dec.Decode(&values); err != nil {
		httpx.Fail(c, httpx.Validation("参数有误"))
		return
	}
	out, err := m.Svc.SaveConfig(c.Request.Context(), c.Param("id"), values)
	if err != nil {
		httpx.Fail(c, failure(err))
		return
	}
	httpx.OK(c, out)
}

// failure 主题定义有误时把原因告诉管理员。
func failure(err error) error {
	if errors.Is(err, ErrManifest) {
		return httpx.Validation(err.Error())
	}
	return err
}
