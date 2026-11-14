package i18n

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

func newT(t *testing.T) *Translator {
	t.Helper()
	tr, err := New("zh-CN")
	if err != nil {
		t.Fatal(err)
	}
	return tr
}

func TestTranslate(t *testing.T) {
	tr := newT(t)
	cases := []struct {
		locale, key string
		params      Params
		want        string
	}{
		// 参数替换
		{"zh-CN", "The service in :app_name is about to expire", Params{"app_name": "测试站"}, "在 测试站 的服务即将到期"},
		// 参数紧挨其他文字（原文 ":limitCNY"）
		{"zh-CN", "The current required minimum withdrawal commission is :limit", Params{"limit": 100}, "当前系统要求的最少提现佣金为：¥100CNY"},
		// 英文
		{"en-US", "The service in :app_name is about to expire", Params{"app_name": "Site"}, "The service in Site is about to expire"},
		// 语言名大小写不敏感
		{"EN-us", "The service in :app_name is about to expire", Params{"app_name": "Site"}, "The service in Site is about to expire"},
		// 不支持的语言回退默认语言（与原项目 fallback_locale 一致）
		{"ja-JP", "The service in :app_name is about to expire", Params{"app_name": "X"}, "在 X 的服务即将到期"},
		{"", "The service in :app_name is about to expire", Params{"app_name": "X"}, "在 X 的服务即将到期"},
		// 语言包中没有的文案原样返回
		{"zh-CN", "No such key :name", Params{"name": "a"}, "No such key a"},
	}
	for _, c := range cases {
		if got := tr.T(c.locale, c.key, c.params); got != c.want {
			t.Errorf("T(%q, %q) = %q，期望 %q", c.locale, c.key, got, c.want)
		}
	}
}

func TestReplaceLikeLaravel(t *testing.T) {
	// 长参数名优先，避免 :name 先替换掉 :name_full 的前缀；首字母大写、全大写占位符分别替换
	got := replace(":name_full / :name / :Name / :NAME", Params{"name": "abc", "name_full": "xyz"})
	if got != "xyz / abc / Abc / ABC" {
		t.Errorf("参数替换结果 %q", got)
	}
}

func TestDefaultLocale(t *testing.T) {
	if _, err := New("fr-FR"); err == nil {
		t.Error("不支持的默认语言应报错")
	}
	tr, err := New("en-us")
	if err != nil || tr.Fallback() != "en-US" {
		t.Errorf("默认语言应规范化为 en-US: %v %v", tr, err)
	}
	if strings.Join(tr.Locales(), ",") != "en-US,zh-CN" {
		t.Errorf("语言列表 %v", tr.Locales())
	}
}

func TestLanguagePackNoFingerprint(t *testing.T) {
	tr := newT(t)
	for name, dict := range tr.dicts {
		for k, v := range dict {
			if strings.Contains(strings.ToLower(k+v), "v2board") {
				t.Errorf("语言包 %s 含 v2board 字样: %s", name, k)
			}
		}
	}
}

func TestMiddleware(t *testing.T) {
	gin.SetMode(gin.TestMode)
	tr := newT(t)
	e := gin.New()
	e.Use(tr.Middleware())
	e.GET("/", func(c *gin.Context) {
		c.String(200, tr.Tr(c, "The service in :app_name is about to expire", Params{"app_name": "S"}))
	})
	for header, want := range map[string]string{
		"en-US": "The service in S is about to expire",
		"zh-CN": "在 S 的服务即将到期",
		"":      "在 S 的服务即将到期",
	} {
		req := httptest.NewRequest(http.MethodGet, "/", nil)
		if header != "" {
			req.Header.Set("Content-Language", header)
		}
		w := httptest.NewRecorder()
		e.ServeHTTP(w, req)
		if w.Body.String() != want {
			t.Errorf("Content-Language=%q 返回 %q，期望 %q", header, w.Body.String(), want)
		}
	}
}
