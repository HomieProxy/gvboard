package legacy

import (
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"

	"github.com/gin-gonic/gin"

	"gvboard/internal/theme"
)

// adminThemeTemplate 主题名列表（原 config/getThemeTemplate）。
func (m *Module) adminThemeTemplate(c *gin.Context) {
	themes := m.Themes.List()
	names := make([]string, 0, len(themes))
	for _, t := range themes {
		names = append(names, t.ID)
	}
	ok(c, names)
}

// adminGetThemes {"themes":{主题名: theme.json 原文}, "active": 当前主题}。
func (m *Module) adminGetThemes(c *gin.Context) {
	themes := map[string]json.RawMessage{}
	for _, t := range m.Themes.List() {
		themes[t.ID] = t.Raw
	}
	ok(c, gin.H{"themes": themes, "active": m.Themes.ActiveID()})
}

// themeParam 校验 name：必填且为存在的主题。
func (m *Module) themeParam(c *gin.Context) (*theme.Theme, bool) {
	name := params(c).str("name")
	key := "validation.required"
	if name != "" {
		if t, err := m.Themes.Get(name); err == nil {
			return t, true
		}
		key = "validation.in"
	}
	m.fail(c, &invalidError{fields: []fieldError{{field: "name", key: key}}})
	return nil, false
}

func (m *Module) adminGetThemeConfig(c *gin.Context) {
	t, found := m.themeParam(c)
	if !found {
		return
	}
	ok(c, m.Themes.Config(t))
}

// adminSaveThemeConfig config 为 base64 编码的 JSON 对象，只保存主题定义中的配置项。
func (m *Module) adminSaveThemeConfig(c *gin.Context) {
	t, found := m.themeParam(c)
	if !found {
		return
	}
	raw := params(c).str("config")
	if raw == "" {
		m.fail(c, &invalidError{fields: []fieldError{{field: "config", key: "validation.required"}}})
		return
	}
	var values map[string]any
	if !decodeThemeConfig(raw, &values) || len(values) == 0 {
		m.fail(c, abortRaw("参数有误"))
		return
	}
	out, err := m.Themes.SaveConfig(c.Request.Context(), t.ID, values)
	if errors.Is(err, theme.ErrManifest) {
		err = abortRaw("主题配置文件有误")
	}
	m.respond(c, out, err)
}

func decodeThemeConfig(raw string, v *map[string]any) bool {
	raw = strings.TrimSpace(raw)
	b, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		if b, err = base64.RawStdEncoding.DecodeString(strings.TrimRight(raw, "=")); err != nil {
			return false
		}
	}
	return json.Unmarshal(b, v) == nil
}
