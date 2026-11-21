package plugin

import (
	"regexp"
	"slices"
	"unicode/utf8"

	sdk "gvboard/pkg/plugin"
)

const maxPages = 3

var (
	pageGroups = []string{sdk.NavSubscribe, sdk.NavFinance, sdk.NavMore, sdk.NavUser}
	// pageLocalePattern 语言代码，如 en-US、zh-TW
	pageLocalePattern = regexp.MustCompile(`^[a-z]{2}(-[A-Z]{2})?$`)
)

func (h *host) Page(p sdk.Page) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.registering("页面") {
		return
	}
	if p.Icon == "" {
		p.Icon = sdk.IconPuzzle
	}
	if p.Group == "" {
		p.Group = sdk.NavMore
	}
	switch {
	case !widgetKeyPattern.MatchString(p.Key):
		h.fail("页面标识 %q 无效", p.Key)
	case slices.ContainsFunc(h.e.pages, func(x sdk.Page) bool { return x.Key == p.Key }):
		h.fail("页面 %q 重复", p.Key)
	case len(h.e.pages) >= maxPages:
		h.fail("最多注册 %d 个页面", maxPages)
	case !validPageTitle(p.Title):
		h.fail("页面 %s 的标题不能为空且不能超过 24 个字符", p.Key)
	case !validPageTitles(p.Titles):
		h.fail("页面 %s 的多语言标题无效（语言代码如 en-US，标题不能为空且不能超过 24 个字符，最多 10 种）", p.Key)
	case !slices.Contains(widgetIcons, p.Icon):
		h.fail("页面 %s 的图标 %q 无效", p.Key, p.Icon)
	case !slices.Contains(pageGroups, p.Group):
		h.fail("页面 %s 的导航分组 %q 无效", p.Key, p.Group)
	case !widgetPathPattern.MatchString(p.Content):
		h.fail("页面 %s 的内容路径 %q 无效", p.Key, p.Content)
	default:
		h.e.pages = append(h.e.pages, p)
	}
}

func validPageTitle(s string) bool {
	return s != "" && utf8.RuneCountInString(s) <= 24
}

func validPageTitles(m map[string]string) bool {
	if len(m) > 10 {
		return false
	}
	for k, v := range m {
		if !pageLocalePattern.MatchString(k) || !validPageTitle(v) {
			return false
		}
	}
	return true
}

// PageView 用户端页面。
type PageView struct {
	Plugin string `json:"plugin"`
	sdk.Page
}

// Pages 已启用插件的用户端页面，按插件标识与注册顺序排列。
func (m *Manager) Pages() []PageView {
	out := []PageView{}
	for _, e := range m.list() {
		if !m.Enabled(e.code) {
			continue
		}
		e.mu.RLock()
		for _, p := range e.pages {
			out = append(out, PageView{Plugin: e.code, Page: p})
		}
		e.mu.RUnlock()
	}
	return out
}
