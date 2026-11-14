package legacy

import (
	"net/http"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/setting"
)

// adminConfigFetch 全部配置或 key 指定的分组（分组不存在时返回全部）。
func (m *Module) adminConfigFetch(c *gin.Context) {
	data, err := m.SysConfig.Fetch("")
	if err != nil {
		m.fail(c, err)
		return
	}
	if key := params(c).str("key"); key != "" {
		if group, found := data[key]; found {
			ok(c, gin.H{key: group})
			return
		}
	}
	ok(c, data)
}

// adminConfigSave 只保存 Gvboard 认识的配置项，其余参数与原项目一样忽略。
func (m *Module) adminConfigSave(c *gin.Context) {
	in := params(c)
	values := map[string]any{}
	for k, v := range in {
		if d, found := setting.Lookup(k); found && d.Editable() {
			values[k] = v
		}
	}
	if len(values) == 0 {
		ok(c, true)
		return
	}
	m.respond(c, true, m.SysConfig.Save(c.Request.Context(), values))
}

func (m *Module) adminEmailTemplates(c *gin.Context) { ok(c, m.SysConfig.EmailTemplates()) }

func (m *Module) adminTestMail(c *gin.Context) {
	p := map[string]any{"app_name": m.Settings.String("app_name")}
	rec := m.SysConfig.TestMail(c.Request.Context(), auth.Current(c).Email,
		m.tr(c, "Test email from :app_name", p),
		m.tr(c, "This is a test email. If you received it, the email settings are working.", p))
	c.JSON(http.StatusOK, gin.H{"data": true, "log": rec})
}

// adminSetTelegramWebhook 按已保存的机器人令牌设置 Webhook 与菜单。
func (m *Module) adminSetTelegramWebhook(c *gin.Context) {
	_, err := m.Bot.Apply(c.Request.Context(), baseURL(c))
	m.respond(c, true, err)
}
