package theme

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/config"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/testutil"
)

const customManifest = `{
  "name": "custom", "description": "自定义", "version": "2.0.0", "images": "",
  "configs": [
    {"label": "颜色", "field_name": "color", "field_type": "select", "select_options": {"red": "红", "blue": "蓝"}, "default_value": "red"},
    {"label": "页脚", "field_name": "footer", "field_type": "textarea"},
    {"label": "标题", "field_name": "title"}
  ]
}`

func writeTheme(t *testing.T, dir, id, manifest string, index bool) {
	t.Helper()
	p := filepath.Join(dir, id)
	if err := os.MkdirAll(filepath.Join(p, "assets"), 0o755); err != nil {
		t.Fatal(err)
	}
	if manifest != "" {
		if err := os.WriteFile(filepath.Join(p, ManifestFile), []byte(manifest), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if index {
		if err := os.WriteFile(filepath.Join(p, IndexFile), []byte("<html><head><title>x</title></head><body>"+id+"</body></html>"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

// themeDir 外部主题目录：custom 有效；bad1 缺少页面模板；bad2 配置项类型错误；bad3 下拉项没有选项；bad4 不是 JSON。
func themeDir(t *testing.T) string {
	dir := t.TempDir()
	writeTheme(t, dir, "custom", customManifest, true)
	writeTheme(t, dir, "bad1", customManifest, false)
	writeTheme(t, dir, "bad2", `{"configs":[{"field_name":"a","field_type":"radio"}]}`, true)
	writeTheme(t, dir, "bad3", `{"configs":[{"field_name":"a","field_type":"select"}]}`, true)
	writeTheme(t, dir, "bad4", `{configs}`, true)
	writeTheme(t, dir, "bad.name", customManifest, true)
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func ids(themes []*Theme) string {
	out := make([]string, 0, len(themes))
	for _, t := range themes {
		out = append(out, t.ID)
	}
	return strings.Join(out, ",")
}

func TestLoad(t *testing.T) {
	m := New(themeDir(t), setting.NewStatic(nil))
	if got := ids(m.List()); got != "custom,default" {
		t.Fatalf("主题列表错误：%s", got)
	}
	def, err := m.Get(DefaultName)
	if err != nil || !def.Builtin || def.Description != "默认主题" || len(def.Configs) != 4 {
		t.Fatalf("内置默认主题错误：%+v %v", def, err)
	}
	if _, err := def.Index(); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"bad1", "bad2", "bad3", "bad4"} {
		if _, err := m.Get(id); !errors.Is(err, ErrManifest) {
			t.Errorf("%s 应为主题定义错误，实际 %v", id, err)
		}
	}
	for _, id := range []string{"nope", "bad.name", "../custom", "", "file.txt"} {
		if _, err := m.Get(id); !errors.Is(err, ErrNotFound) {
			t.Errorf("%q 应不存在，实际 %v", id, err)
		}
	}
	if !m.Exists("custom") || m.Exists("bad1") {
		t.Fatal("Exists 结果错误")
	}

	// 外部目录中的同名主题优先于内置主题。
	dir := t.TempDir()
	writeTheme(t, dir, DefaultName, customManifest, true)
	m = New(dir, setting.NewStatic(nil))
	if def, err := m.Get(DefaultName); err != nil || def.Builtin || def.Version != "2.0.0" {
		t.Fatalf("外部同名主题应优先：%+v %v", def, err)
	}
	// 外部目录不存在时只有内置主题。
	m = New(filepath.Join(dir, "missing"), setting.NewStatic(nil))
	if got := ids(m.List()); got != DefaultName {
		t.Fatalf("主题列表错误：%s", got)
	}
}

func eachDriver(t *testing.T, f func(t *testing.T, db *gorm.DB, st *setting.Service, m *Manager)) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, d := range drivers {
		t.Run(d, func(t *testing.T) {
			db := testutil.DB(t, d)
			st := setting.New(db, testutil.Redis(t), zap.NewNop())
			if err := st.Load(context.Background()); err != nil {
				t.Fatal(err)
			}
			f(t, db, st, New(themeDir(t), st))
		})
	}
}

func wantValidation(t *testing.T, err error, contains string) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Status != http.StatusUnprocessableEntity || !strings.Contains(he.Msg, contains) {
		t.Fatalf("期望包含 %q 的校验错误，实际 %v", contains, err)
	}
}

func TestConfig(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *gorm.DB, st *setting.Service, m *Manager) {
		ctx := context.Background()
		custom, _ := m.Get("custom")
		cfg := m.Config(custom)
		if len(cfg) != 3 || cfg["color"] != "red" || cfg["footer"] != "" || cfg["title"] != "" {
			t.Fatalf("默认配置错误：%v", cfg)
		}

		_, err := m.SaveConfig(ctx, "custom", map[string]any{"color": "green"})
		wantValidation(t, err, "颜色")
		_, err = m.SaveConfig(ctx, "custom", map[string]any{"title": map[string]any{"a": 1}})
		wantValidation(t, err, "格式不正确")
		_, err = m.SaveConfig(ctx, "custom", map[string]any{"footer": strings.Repeat("a", maxValueLen+1)})
		wantValidation(t, err, "过长")
		if _, err := m.SaveConfig(ctx, "nope", map[string]any{}); !errors.Is(err, ErrNotFound) {
			t.Fatalf("主题不存在应报错：%v", err)
		}
		if _, err := m.SaveConfig(ctx, "bad2", map[string]any{}); !errors.Is(err, ErrManifest) {
			t.Fatalf("主题定义有误应报错：%v", err)
		}

		out, err := m.SaveConfig(ctx, "custom", map[string]any{"color": "blue", "footer": "<b>页脚</b>", "extra": "x"})
		if err != nil || len(out) != 3 || out["color"] != "blue" || out["title"] != "" || out["extra"] != nil {
			t.Fatalf("保存结果错误：%v %v", out, err)
		}
		// 新实例从数据库读取，确认已持久化。
		st2 := setting.New(db, nil, zap.NewNop())
		if err := st2.Load(ctx); err != nil {
			t.Fatal(err)
		}
		cfg = New(m.Dir, st2).Config(custom)
		if cfg["color"] != "blue" || cfg["footer"] != "<b>页脚</b>" || cfg["title"] != "" {
			t.Fatalf("配置未持久化：%v", cfg)
		}
		if def, _ := m.Get(DefaultName); m.Config(def)["theme_color"] != "default" {
			t.Fatal("其它主题的配置不应受影响")
		}

		// 切换主题。
		if err := m.Activate(ctx, "bad1"); !errors.Is(err, ErrManifest) {
			t.Fatalf("不能切换到有误的主题：%v", err)
		}
		if err := m.Activate(ctx, "custom"); err != nil || m.ActiveID() != "custom" {
			t.Fatalf("切换失败：%v %s", err, m.ActiveID())
		}
		if a, err := m.Active(); err != nil || a.ID != "custom" {
			t.Fatalf("当前主题错误：%v %v", a, err)
		}
		// 设置的主题不存在（如原站主题名）时使用内置默认主题。
		if err := st.Set(ctx, map[string]any{"frontend_theme": "v2board"}); err != nil {
			t.Fatal(err)
		}
		if a, err := m.Active(); err != nil || a.ID != DefaultName || !a.Builtin {
			t.Fatalf("应回退到默认主题：%v %v", a, err)
		}
	})
}

func TestHTTP(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *gorm.DB, st *setting.Service, m *Manager) {
		ctx := context.Background()
		const secure = "themetest0123456789"
		if err := st.Set(ctx, map[string]any{"secure_path": secure}); err != nil {
			t.Fatal(err)
		}
		a := auth.New(db, testutil.Redis(t), "base64:c3lzY29uZmlnLXRlc3Qta2V5LTAxMjM0NTY3ODk=")
		var admin, normal *model.User
		db.Transaction(func(tx *gorm.DB) error {
			admin, _, _ = account.Create(tx, account.NewUser{Email: "admin@x.com", Password: "password123"})
			normal, _, _ = account.Create(tx, account.NewUser{Email: "n@x.com", Password: "password123"})
			return tx.Model(admin).Update("is_admin", 1).Error
		})
		adminTok, _ := a.Issue(ctx, admin.ID, "", "")
		userTok, _ := a.Issue(ctx, normal.ID, "", "")
		cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
		r, err := server.NewRouter(cfg, zap.NewNop())
		if err != nil {
			t.Fatal(err)
		}
		r.Mount(&Module{Svc: m, Auth: a})
		do := func(method, path, body, tok string) *httptest.ResponseRecorder {
			req := httptest.NewRequest(method, "/api/t1/"+secure+"/"+path, strings.NewReader(body))
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", tok)
			w := httptest.NewRecorder()
			r.Engine.ServeHTTP(w, req)
			return w
		}
		if w := do("GET", "themes", "", userTok); w.Code != http.StatusNotFound {
			t.Fatalf("普通用户应 404，实际 %d", w.Code)
		}
		w := do("GET", "themes", "", adminTok)
		body := w.Body.String()
		if w.Code != 200 || !strings.Contains(body, `"active":"default"`) || !strings.Contains(body, `"id":"custom"`) ||
			!strings.Contains(body, `"builtin":true`) || strings.Contains(body, "bad1") ||
			!strings.Contains(body, `"select_options":{"red":"红","blue":"蓝"}`) {
			t.Fatalf("主题列表错误：%d %s", w.Code, body)
		}
		if w := do("PUT", "themes/active", `{"id":"nope"}`, adminTok); w.Code != http.StatusNotFound {
			t.Fatalf("切换到不存在的主题应 404，实际 %d %s", w.Code, w.Body)
		}
		if w := do("PUT", "themes/active", `{"id":"bad2"}`, adminTok); w.Code != http.StatusUnprocessableEntity || !strings.Contains(w.Body.String(), "主题配置文件有误") {
			t.Fatalf("切换到有误的主题应 422：%d %s", w.Code, w.Body)
		}
		if w := do("PUT", "themes/active", `{"id":"custom"}`, adminTok); w.Code != 200 || st.String("frontend_theme") != "custom" {
			t.Fatalf("切换失败：%d %s", w.Code, w.Body)
		}
		if w := do("GET", "themes/custom/config", "", adminTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"color":"red"`) {
			t.Fatalf("读取配置错误：%s", w.Body)
		}
		if w := do("PUT", "themes/custom/config", `{"color":"pink"}`, adminTok); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("非法选项应 422，实际 %d", w.Code)
		}
		if w := do("PUT", "themes/custom/config", `[1]`, adminTok); w.Code != http.StatusUnprocessableEntity {
			t.Fatalf("格式错误应 422，实际 %d", w.Code)
		}
		if w := do("PUT", "themes/custom/config", `{"color":"blue","title":12}`, adminTok); w.Code != 200 ||
			!strings.Contains(w.Body.String(), `"title":12`) || st.ThemeConfig("custom")["color"] != "blue" {
			t.Fatalf("保存配置错误：%d %s", w.Code, w.Body)
		}
	})
}
