package plugin

import (
	"slices"
	"unicode/utf8"

	sdk "gvboard/pkg/plugin"
)

const maxBanners = 3

func (h *host) Banner(b sdk.Banner) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.registering("横幅") {
		return
	}
	if b.Icon == "" {
		b.Icon = sdk.IconMegaphone
	}
	switch {
	case !widgetKeyPattern.MatchString(b.Key):
		h.fail("横幅标识 %q 无效", b.Key)
	case slices.ContainsFunc(h.e.banners, func(x sdk.Banner) bool { return x.Key == b.Key }):
		h.fail("横幅 %q 重复", b.Key)
	case len(h.e.banners) >= maxBanners:
		h.fail("最多注册 %d 个横幅", maxBanners)
	case utf8.RuneCountInString(b.Title) > 32:
		h.fail("横幅 %s 的标题不能超过 32 个字符", b.Key)
	case !slices.Contains(widgetIcons, b.Icon):
		h.fail("横幅 %s 的图标 %q 无效", b.Key, b.Icon)
	case !widgetPathPattern.MatchString(b.State):
		h.fail("横幅 %s 的状态路径 %q 无效", b.Key, b.State)
	default:
		h.e.banners = append(h.e.banners, b)
	}
}

// BannerView 用户端横幅。
type BannerView struct {
	Plugin string `json:"plugin"`
	sdk.Banner
}

// Banners 已启用插件的横幅，按插件标识与注册顺序排列。
func (m *Manager) Banners() []BannerView {
	out := []BannerView{}
	for _, e := range m.list() {
		if !m.Enabled(e.code) || !m.shown(e.code, formBanner) {
			continue
		}
		e.mu.RLock()
		for _, b := range e.banners {
			out = append(out, BannerView{Plugin: e.code, Banner: b})
		}
		e.mu.RUnlock()
	}
	return out
}
