package sysconfig

import (
	"encoding/json"
	"io"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

// maxBody 保存配置的请求体上限（订阅模板较大）。
const maxBody = 4 << 20

// Translator 按请求语言翻译文案。
type Translator func(c *gin.Context, key string, params map[string]any) string

// Module 系统配置路由。
type Module struct {
	Svc       *Service
	Auth      *auth.Service
	Translate Translator
}

// Register 挂载管理端接口（{api}/{secure_path}）：
//
//	GET  config?group=            配置（按分组，group 为空时全部）
//	PUT  config                   保存配置，{"键": 值, ...}，只修改提交的项
//	GET  config/email-templates   可选的邮件模板
//	POST config/test-mail         向当前管理员发送测试邮件，返回邮件日志
//
// Telegram 机器人设置保存后调用 telegram/apply 生效（单元 13A）。
func (m *Module) Register(r *server.Router) {
	a := m.Auth.AdminGroup(r.API, func() string { return m.Svc.Settings.String("secure_path") })
	a.GET("/config", m.fetch)
	a.PUT("/config", m.save)
	a.GET("/config/email-templates", func(c *gin.Context) { httpx.OK(c, m.Svc.EmailTemplates()) })
	a.POST("/config/test-mail", m.testMail)
}

func (m *Module) fetch(c *gin.Context) {
	data, err := m.Svc.Fetch(c.Query("group"))
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, data)
}

func (m *Module) save(c *gin.Context) {
	var values map[string]any
	dec := json.NewDecoder(io.LimitReader(c.Request.Body, maxBody))
	dec.UseNumber()
	if err := dec.Decode(&values); err != nil {
		httpx.Fail(c, httpx.Validation("参数有误"))
		return
	}
	if err := m.Svc.Save(c.Request.Context(), values); err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, true)
}

func (m *Module) testMail(c *gin.Context) {
	params := map[string]any{"app_name": m.Svc.Settings.String("app_name")}
	rec := m.Svc.TestMail(c.Request.Context(), auth.Current(c).Email,
		m.Translate(c, "Test email from :app_name", params),
		m.Translate(c, "This is a test email. If you received it, the email settings are working.", params))
	httpx.OK(c, rec)
}
