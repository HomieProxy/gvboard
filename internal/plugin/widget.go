package plugin

import (
	"regexp"
	"slices"
	"unicode/utf8"

	"github.com/gin-gonic/gin"

	"gvboard/internal/httpx"
	sdk "gvboard/pkg/plugin"
)

const maxWidgets = 5

var (
	widgetKeyPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,31}$`)
	// widgetPathPattern 卡片的状态与按钮路径：不含 . 与路由参数，用户端据此请求，不能跳出插件路由前缀
	widgetPathPattern = regexp.MustCompile(`^/[A-Za-z0-9_/-]{0,127}$`)
	widgetSlots       = []string{sdk.SlotDashboard}
	widgetIcons       = []string{sdk.IconPuzzle, sdk.IconGift, sdk.IconCalendarCheck, sdk.IconStar, sdk.IconTrophy,
		sdk.IconTicket, sdk.IconSparkles, sdk.IconCoins, sdk.IconMegaphone}
)

func (h *host) Widget(w sdk.Widget) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.registering("卡片") {
		return
	}
	if w.Icon == "" {
		w.Icon = sdk.IconPuzzle
	}
	switch {
	case !widgetKeyPattern.MatchString(w.Key):
		h.fail("卡片标识 %q 无效", w.Key)
	case slices.ContainsFunc(h.e.widgets, func(x sdk.Widget) bool { return x.Key == w.Key }):
		h.fail("卡片 %q 重复", w.Key)
	case len(h.e.widgets) >= maxWidgets:
		h.fail("最多注册 %d 张卡片", maxWidgets)
	case !slices.Contains(widgetSlots, w.Slot):
		h.fail("卡片 %s 的位置 %q 无效", w.Key, w.Slot)
	case w.Title == "" || utf8.RuneCountInString(w.Title) > 32:
		h.fail("卡片 %s 的标题不能为空且不能超过 32 个字符", w.Key)
	case !slices.Contains(widgetIcons, w.Icon):
		h.fail("卡片 %s 的图标 %q 无效", w.Key, w.Icon)
	case !widgetPathPattern.MatchString(w.State):
		h.fail("卡片 %s 的状态路径 %q 无效", w.Key, w.State)
	default:
		h.e.widgets = append(h.e.widgets, w)
	}
}

// WidgetView 用户端卡片。
type WidgetView struct {
	Plugin string `json:"plugin"`
	sdk.Widget
}

// Widgets 已启用插件在指定位置的卡片，按插件标识与注册顺序排列。
func (m *Manager) Widgets(slot string) []WidgetView {
	out := []WidgetView{}
	for _, e := range m.list() {
		if !m.Enabled(e.code) {
			continue
		}
		e.mu.RLock()
		for _, w := range e.widgets {
			if w.Slot == slot {
				out = append(out, WidgetView{Plugin: e.code, Widget: w})
			}
		}
		e.mu.RUnlock()
	}
	return out
}

func (m *Manager) userWidgets(c *gin.Context) {
	slot := c.DefaultQuery("slot", sdk.SlotDashboard)
	if !slices.Contains(widgetSlots, slot) {
		httpx.Fail(c, errInvalid)
		return
	}
	httpx.OK(c, m.Widgets(slot))
}
