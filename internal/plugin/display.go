package plugin

import (
	"context"
	"slices"
	"strings"
)

// 用户端显示形式。
const (
	formWidget = "widget"
	formBanner = "banner"
	formPage   = "page"
)

// displayKey 保存管理员关闭的显示形式，值为 "插件标识:形式" 列表。
const displayKey = "plugin_ui_off"

// shown 管理员是否开启了插件的该显示形式（默认开启）。
func (m *Manager) shown(code, form string) bool {
	return !slices.Contains(m.Settings.Strings(displayKey), code+":"+form)
}

// forms 插件已注册的显示形式。调用方需持有 e.mu 读锁。
func (e *entry) forms() []string {
	var out []string
	if len(e.widgets) > 0 {
		out = append(out, formWidget)
	}
	if len(e.banners) > 0 {
		out = append(out, formBanner)
	}
	if len(e.pages) > 0 {
		out = append(out, formPage)
	}
	return out
}

// display 插件已注册的显示形式及其开关。调用方需持有 e.mu 读锁。
func (m *Manager) display(e *entry) map[string]bool {
	out := map[string]bool{}
	for _, f := range e.forms() {
		out[f] = m.shown(e.code, f)
	}
	return out
}

// SetDisplay 设置插件各显示形式的开关；未注册的形式、未提交的形式保持不变。
func (m *Manager) SetDisplay(ctx context.Context, code string, in map[string]bool) error {
	e := m.entry(code)
	if e == nil {
		return errNotFound
	}
	e.mu.RLock()
	forms := e.forms()
	e.mu.RUnlock()
	off := slices.DeleteFunc(slices.Clone(m.Settings.Strings(displayKey)), func(s string) bool {
		f, ok := strings.CutPrefix(s, code+":")
		_, set := in[f]
		return ok && set && slices.Contains(forms, f)
	})
	for _, f := range forms {
		if v, ok := in[f]; ok && !v {
			off = append(off, code+":"+f)
		}
	}
	return m.Settings.Set(ctx, map[string]any{displayKey: off})
}
