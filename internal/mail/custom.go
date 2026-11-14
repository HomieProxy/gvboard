package mail

import (
	"bytes"
	"encoding/json"
	"fmt"
	"html/template"
	"regexp"
)

// CustomKey 自定义邮件模板所在的设置项，内容为 JSON：{"风格": {"模板名": "内容"}}。
// 内置风格中的项覆盖对应的内置模板；其它风格为新增风格，必须包含全部模板。
const CustomKey = "email_templates_custom"

// 自定义模板限制。
const (
	MaxTemplateSize = 64 << 10
	MaxCustomThemes = 20
)

// Names 全部模板名，按管理端展示顺序。
var Names = []string{TplVerify, TplNotify, TplRemindExpire, TplRemindTraffic, TplMailLogin}

// TemplateInfo 模板说明（管理端展示）。
type TemplateInfo struct {
	Name  string   `json:"name"`
	Title string   `json:"title"`
	Vars  []string `json:"vars"`
}

// Infos 各模板的用途与可用变量。
var Infos = []TemplateInfo{
	{TplVerify, "邮箱验证码", []string{"name", "url", "code"}},
	{TplNotify, "通知（公告、群发邮件、工单回复、测试邮件）", []string{"name", "url", "content"}},
	{TplRemindExpire, "到期提醒", []string{"name", "url", "subscription"}},
	{TplRemindTraffic, "流量提醒", []string{"name", "url", "subscription"}},
	{TplMailLogin, "邮件链接登录", []string{"name", "url", "link"}},
}

// ThemePattern 自定义风格名规则。
var ThemePattern = regexp.MustCompile(`^[A-Za-z0-9_-]{1,32}$`)

// ValidName 是否为已知的模板名。
func ValidName(name string) bool {
	for _, n := range Names {
		if n == name {
			return true
		}
	}
	return false
}

type customSet struct {
	raw  string
	src  map[string]map[string]string
	tpls map[string]*template.Template
}

// customTemplates 解析设置中的自定义模板，设置内容未变化时使用缓存。无法解析的项忽略（保存时已校验）。
func (m *Mailer) customTemplates() *customSet {
	var raw string
	if m.settings != nil {
		raw = m.settings.String(CustomKey)
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.custom != nil && m.custom.raw == raw {
		return m.custom
	}
	cs := &customSet{raw: raw, src: map[string]map[string]string{}, tpls: map[string]*template.Template{}}
	if raw != "" {
		_ = json.Unmarshal([]byte(raw), &cs.src)
	}
	for theme, items := range cs.src {
		for name, content := range items {
			if !ValidName(name) {
				continue
			}
			if t, err := parse(theme+"/"+name, content); err == nil {
				cs.tpls[theme+"/"+name] = t
			}
		}
	}
	m.custom = cs
	return cs
}

// Custom 返回自定义模板的副本。
func (m *Mailer) Custom() map[string]map[string]string {
	out := map[string]map[string]string{}
	for theme, items := range m.customTemplates().src {
		cp := make(map[string]string, len(items))
		for k, v := range items {
			cp[k] = v
		}
		out[theme] = cp
	}
	return out
}

// Source 返回模板当前内容、内置内容（新增风格为空）与是否已自定义。
func (m *Mailer) Source(theme, name string) (content, builtin string, custom bool) {
	builtin = m.sources[theme+"/"+name]
	if c, ok := m.customTemplates().src[theme][name]; ok {
		return c, builtin, true
	}
	return builtin, builtin, false
}

// SampleVars 预览用的示例变量。
func SampleVars(name, appName, appURL string) map[string]any {
	if appName == "" {
		appName = "Gvboard"
	}
	if appURL == "" {
		appURL = "https://example.com"
	}
	v := map[string]any{"name": appName, "url": appURL}
	switch name {
	case TplVerify:
		v["code"] = "123456"
	case TplNotify:
		v["content"] = "这是一条通知内容的示例。\n第二行内容。"
	case TplRemindExpire, TplRemindTraffic:
		v["subscription"] = "标准套餐 #1"
	case TplMailLogin:
		v["link"] = appURL + "/#/login?verify=example"
	}
	return v
}

// Check 校验模板内容：能解析，并能用示例变量渲染。
func Check(name, content string) error {
	if len(content) > MaxTemplateSize {
		return fmt.Errorf("模板内容不能超过 %d KB", MaxTemplateSize>>10)
	}
	_, err := RenderContent(name, content, SampleVars(name, "", ""))
	return err
}

// RenderContent 用给定内容渲染模板（预览与校验）。
func RenderContent(name, content string, vars map[string]any) (string, error) {
	t, err := parse(name, content)
	if err != nil {
		return "", fmt.Errorf("模板语法错误：%w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, vars); err != nil {
		return "", fmt.Errorf("模板渲染失败：%w", err)
	}
	return buf.String(), nil
}
