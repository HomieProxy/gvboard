package legacy

import (
	"encoding/base64"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func b64(s string) string { return base64.StdEncoding.EncodeToString([]byte(s)) }

func testAdminTheme(t *testing.T, e *env) {
	tok := e.admin()
	dir := filepath.Join(e.mod.Themes.Dir, "custom")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	os.WriteFile(filepath.Join(dir, "theme.json"), []byte(`{"name":"custom","configs":[{"label":"色","field_name":"color","field_type":"select","select_options":{"a":"A","b":"B"},"default_value":"a"},{"label":"页脚","field_name":"footer"}]}`), 0o644)
	os.WriteFile(filepath.Join(dir, "index.html"), []byte("<html></html>"), 0o644)
	bad := filepath.Join(e.mod.Themes.Dir, "bad")
	os.MkdirAll(bad, 0o755)
	os.WriteFile(filepath.Join(bad, "theme.json"), []byte(`{"configs":[]}`), 0o644)

	if l := e.get(ap("config/getThemeTemplate"), tok).list(t); len(l) != 2 || l[0] != "custom" || l[1] != "default" {
		t.Fatalf("主题名列表错误：%v", l)
	}
	themes := e.get(ap("theme/getThemes"), tok).obj(t)
	list, _ := themes["themes"].(map[string]any)
	def, _ := list["default"].(map[string]any)
	if themes["active"] != "default" || len(list) != 2 || def["description"] != "默认主题" || def["configs"] == nil {
		t.Fatalf("主题列表错误：%v", themes)
	}

	wantInvalid(t, e.post(ap("theme/getThemeConfig"), tok, url.Values{}), "name")
	wantInvalid(t, e.post(ap("theme/getThemeConfig"), tok, url.Values{"name": {"bad"}}), "name")
	cfg := e.post(ap("theme/getThemeConfig"), tok, url.Values{"name": {"custom"}}).obj(t)
	if len(cfg) != 2 || cfg["color"] != "a" || cfg["footer"] != "" {
		t.Fatalf("默认配置错误：%v", cfg)
	}

	wantInvalid(t, e.post(ap("theme/saveThemeConfig"), tok, url.Values{"name": {"nope"}, "config": {b64(`{"color":"b"}`)}}), "name")
	wantInvalid(t, e.post(ap("theme/saveThemeConfig"), tok, url.Values{"name": {"custom"}}), "config")
	for _, raw := range []string{"不是base64", b64("not json"), b64("{}"), b64(`"str"`)} {
		wantMsg(t, e.post(ap("theme/saveThemeConfig"), tok, url.Values{"name": {"custom"}, "config": {raw}}), http.StatusInternalServerError, "参数有误")
	}
	r := e.post(ap("theme/saveThemeConfig"), tok, url.Values{"name": {"custom"}, "config": {b64(`{"color":"z"}`)}})
	if r.code != http.StatusInternalServerError || !strings.Contains(r.raw, "色") {
		t.Fatalf("非法选项应报错：%d %s", r.code, r.raw)
	}
	saved := e.post(ap("theme/saveThemeConfig"), tok, url.Values{"name": {"custom"}, "config": {b64(`{"color":"b","extra":1}`)}}).obj(t)
	if len(saved) != 2 || saved["color"] != "b" || saved["footer"] != "" {
		t.Fatalf("保存结果错误：%v", saved)
	}
	// 不带填充的 base64 也能解析。
	raw := strings.TrimRight(b64(`{"footer":"<b>x</b>"}`), "=")
	saved = e.post(ap("theme/saveThemeConfig"), tok, url.Values{"name": {"custom"}, "config": {raw}}).obj(t)
	if saved["footer"] != "<b>x</b>" || saved["color"] != "" {
		t.Fatalf("保存结果错误：%v", saved)
	}
	if cfg := e.post(ap("theme/getThemeConfig"), tok, url.Values{"name": {"custom"}}).obj(t); cfg["footer"] != "<b>x</b>" {
		t.Fatalf("配置未保存：%v", cfg)
	}

	// 原系统配置保存：切换到不存在的主题报错，切换到存在的主题生效。
	r = e.post(ap("config/save"), tok, url.Values{"frontend_theme": {"nope"}})
	if r.code == http.StatusOK || !strings.Contains(r.raw, "主题不存在") {
		t.Fatalf("切换到不存在的主题应报错：%d %s", r.code, r.raw)
	}
	wantOK(t, e.post(ap("config/save"), tok, url.Values{"frontend_theme": {"custom"}}))
	if e.st.String("frontend_theme") != "custom" {
		t.Fatal("主题切换未生效")
	}
}
