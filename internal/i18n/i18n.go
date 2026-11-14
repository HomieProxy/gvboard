// Package i18n 多语言。语言包沿用原项目 resources/lang/*.json（英文原文为键）。
package i18n

import (
	"embed"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/gin-gonic/gin"
)

//go:embed locales/*.json
var localeFS embed.FS

// Params 文案参数，替换文案中的 :name 占位符。
type Params map[string]any

// Translator 翻译器。查找顺序与原项目（Laravel）一致：请求语言 → 默认语言 → 原文。
type Translator struct {
	fallback string
	dicts    map[string]map[string]string
	names    map[string]string // 小写语言名 → 规范语言名
}

// New 加载内置语言包。fallback 为默认语言，必须是已有语言包之一。
func New(fallback string) (*Translator, error) {
	t := &Translator{dicts: map[string]map[string]string{}, names: map[string]string{}}
	entries, err := localeFS.ReadDir("locales")
	if err != nil {
		return nil, err
	}
	for _, e := range entries {
		b, err := localeFS.ReadFile("locales/" + e.Name())
		if err != nil {
			return nil, err
		}
		dict := map[string]string{}
		if err := json.Unmarshal(b, &dict); err != nil {
			return nil, fmt.Errorf("语言包 %s 格式错误: %w", e.Name(), err)
		}
		name := strings.TrimSuffix(e.Name(), path.Ext(e.Name()))
		t.dicts[name] = dict
		t.names[strings.ToLower(name)] = name
	}
	canon, ok := t.names[strings.ToLower(fallback)]
	if !ok {
		return nil, fmt.Errorf("默认语言 %s 不受支持，可选：%s", fallback, strings.Join(t.Locales(), "、"))
	}
	t.fallback = canon
	return t, nil
}

// Locales 返回支持的语言列表。
func (t *Translator) Locales() []string {
	out := make([]string, 0, len(t.dicts))
	for k := range t.dicts {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Fallback 返回默认语言。
func (t *Translator) Fallback() string { return t.fallback }

// Resolve 把请求中的语言名规范化，不支持时返回默认语言。
func (t *Translator) Resolve(locale string) string {
	if canon, ok := t.names[strings.ToLower(strings.TrimSpace(locale))]; ok {
		return canon
	}
	return t.fallback
}

// T 翻译文案并替换参数。
func (t *Translator) T(locale, key string, params Params) string {
	s, ok := t.dicts[t.Resolve(locale)][key]
	if !ok {
		if s, ok = t.dicts[t.fallback][key]; !ok {
			s = key
		}
	}
	return replace(s, params)
}

// replace 与 Laravel 相同：按参数名长度从长到短替换 :name、:Name、:NAME 三种形式。
func replace(s string, params Params) string {
	if len(params) == 0 {
		return s
	}
	keys := make([]string, 0, len(params))
	for k := range params {
		keys = append(keys, k)
	}
	sort.Slice(keys, func(i, j int) bool { return len(keys[i]) > len(keys[j]) })
	pairs := make([]string, 0, len(keys)*6)
	for _, k := range keys {
		v := fmt.Sprint(params[k])
		pairs = append(pairs,
			":"+k, v,
			":"+upperFirst(k), upperFirst(v),
			":"+strings.ToUpper(k), strings.ToUpper(v),
		)
	}
	return strings.NewReplacer(pairs...).Replace(s)
}

func upperFirst(s string) string {
	r, n := utf8.DecodeRuneInString(s)
	if r == utf8.RuneError {
		return s
	}
	return string(unicode.ToUpper(r)) + s[n:]
}

const ctxKey = "gvb.locale"

// Middleware 从 Content-Language 请求头确定语言（与原项目一致）。
func (t *Translator) Middleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		c.Set(ctxKey, t.Resolve(c.GetHeader("Content-Language")))
		c.Next()
	}
}

// Locale 返回当前请求的语言。
func (t *Translator) Locale(c *gin.Context) string {
	if v, ok := c.Get(ctxKey); ok {
		return v.(string)
	}
	return t.fallback
}

// Tr 按当前请求语言翻译。
func (t *Translator) Tr(c *gin.Context, key string, params Params) string {
	return t.T(t.Locale(c), key, params)
}
