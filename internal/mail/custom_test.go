package mail

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestCustomTemplates(t *testing.T) {
	custom := map[string]map[string]string{
		"default": {TplVerify: `<p>自定义验证码 {{.code}}</p>`},
		"brand":   {TplVerify: `<p>品牌 {{.code}}</p>`, TplNotify: `<p>{{nl2br .content}}</p>`, "unknown": "x"},
		"broken":  {TplVerify: `{{if}}`},
	}
	raw, _ := json.Marshal(custom)
	s := fakeSettings{CustomKey: string(raw), "email_template": "brand"}
	m := newMailer(t, s)

	if got := strings.Join(m.Themes(), ","); got != "brand,broken,classic,default" {
		t.Fatalf("风格列表 %s", got)
	}
	if m.Theme() != "brand" || !m.Builtin("default") || m.Builtin("brand") {
		t.Fatal("自定义风格识别错误")
	}
	out, _ := m.Render("default", TplVerify, map[string]any{"code": "1"})
	if out != "<p>自定义验证码 1</p>" {
		t.Errorf("内置风格的自定义内容应覆盖内置模板: %s", out)
	}
	if out, _ := m.Render("default", TplNotify, map[string]any{"name": "站"}); !strings.Contains(out, "站") {
		t.Error("未自定义的模板应使用内置内容")
	}
	if out, _ := m.Render("brand", TplNotify, map[string]any{"content": "<b>x</b>\ny"}); out != "<p>&lt;b&gt;x&lt;/b&gt;<br />\ny</p>" {
		t.Errorf("新增风格渲染错误: %s", out)
	}
	if out, _ := m.Render("broken", TplVerify, map[string]any{"name": "站"}); !strings.Contains(out, "站") {
		t.Error("无法解析的自定义模板应回退内置模板")
	}

	content, builtin, isCustom := m.Source("default", TplVerify)
	if !isCustom || content != custom["default"][TplVerify] || builtin == "" || builtin == content {
		t.Error("Source 返回错误")
	}
	if _, builtin, _ := m.Source("brand", TplVerify); builtin != "" {
		t.Error("新增风格没有内置内容")
	}

	// 设置变化后重新解析
	s[CustomKey] = ""
	if strings.Join(m.Themes(), ",") != "classic,default" || m.Theme() != DefaultTheme {
		t.Error("清空自定义模板后应只剩内置风格")
	}
	cp := m.Custom()
	cp["x"] = map[string]string{}
	if len(m.Custom()) != 0 {
		t.Error("Custom 应返回副本")
	}
}

func TestCheckTemplate(t *testing.T) {
	if err := Check(TplVerify, `<p>{{.code}}</p>`); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{`{{if}}`, `{{.code | nope}}`, `{{template "x"}}`, strings.Repeat("a", MaxTemplateSize+1)} {
		if Check(TplVerify, bad) == nil {
			t.Errorf("应校验失败: %.40q", bad)
		}
	}
	out, err := RenderContent(TplMailLogin, `<a href="{{.link}}">{{.name}}</a>`, SampleVars(TplMailLogin, "<站>", "https://a.com"))
	if err != nil || out != `<a href="https://a.com/#/login?verify=example">&lt;站&gt;</a>` {
		t.Errorf("预览渲染错误: %s %v", out, err)
	}
}
