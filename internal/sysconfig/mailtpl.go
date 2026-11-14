package sysconfig

import (
	"context"
	"encoding/json"
	"fmt"

	"gvboard/internal/httpx"
	"gvboard/internal/mail"
)

// maxCustomTotal 自定义邮件模板合计大小上限（全部存在一个设置项中，每个实例都会加载）。
const maxCustomTotal = 2 << 20

var (
	errMailThemeMissing = httpx.Validation("邮件模板不存在")
	errMailNameMissing  = httpx.Validation("模板类型不存在")
)

// MailTemplateItem 模板列表中的一项。
type MailTemplateItem struct {
	mail.TemplateInfo
	Custom bool `json:"custom"`
}

// MailTheme 邮件模板风格。
type MailTheme struct {
	Theme     string             `json:"theme"`
	Builtin   bool               `json:"builtin"`
	Active    bool               `json:"active"`
	Templates []MailTemplateItem `json:"templates"`
}

// MailTemplateDetail 单个模板的内容。
type MailTemplateDetail struct {
	Content string `json:"content"`
	Default string `json:"default"` // 内置内容，新增风格为空
	Custom  bool   `json:"custom"`
}

// MailThemes 全部风格及其模板（决策 D71）。
func (s *Service) MailThemes() []MailTheme {
	active := s.Mailer.Theme()
	custom := s.Mailer.Custom()
	out := []MailTheme{}
	for _, theme := range s.Mailer.Themes() {
		t := MailTheme{Theme: theme, Builtin: s.Mailer.Builtin(theme), Active: theme == active}
		for _, info := range mail.Infos {
			_, ok := custom[theme][info.Name]
			t.Templates = append(t.Templates, MailTemplateItem{TemplateInfo: info, Custom: ok})
		}
		out = append(out, t)
	}
	return out
}

func (s *Service) checkTheme(theme, name string) error {
	if !s.themeExists(theme) {
		return errMailThemeMissing
	}
	if !mail.ValidName(name) {
		return errMailNameMissing
	}
	return nil
}

func (s *Service) themeExists(theme string) bool {
	if s.Mailer.Builtin(theme) {
		return true
	}
	_, ok := s.Mailer.Custom()[theme]
	return ok
}

// MailTemplate 返回模板内容。
func (s *Service) MailTemplate(theme, name string) (MailTemplateDetail, error) {
	if err := s.checkTheme(theme, name); err != nil {
		return MailTemplateDetail{}, err
	}
	content, builtin, custom := s.Mailer.Source(theme, name)
	return MailTemplateDetail{Content: content, Default: builtin, Custom: custom}, nil
}

// SaveMailTemplate 保存模板。内置风格提交空内容或与内置内容相同时恢复内置模板。
func (s *Service) SaveMailTemplate(ctx context.Context, theme, name, content string) error {
	if err := s.checkTheme(theme, name); err != nil {
		return err
	}
	custom := s.Mailer.Custom()
	if s.Mailer.Builtin(theme) {
		if _, builtin, _ := s.Mailer.Source(theme, name); content == "" || content == builtin {
			delete(custom[theme], name)
			if len(custom[theme]) == 0 {
				delete(custom, theme)
			}
			return s.writeCustom(ctx, custom)
		}
	} else if content == "" {
		return httpx.Validation("模板内容不能为空")
	}
	if err := mail.Check(name, content); err != nil {
		return httpx.Validation(err.Error())
	}
	if custom[theme] == nil {
		custom[theme] = map[string]string{}
	}
	custom[theme][name] = content
	return s.writeCustom(ctx, custom)
}

// CreateMailTheme 以 from 风格的当前内容为基础新增风格。
func (s *Service) CreateMailTheme(ctx context.Context, theme, from string) error {
	if !mail.ThemePattern.MatchString(theme) {
		return httpx.Validation("模板名称只能包含字母、数字、下划线、短横线，最长 32 位")
	}
	if s.themeExists(theme) {
		return httpx.Validation("模板名称已存在")
	}
	if !s.themeExists(from) {
		return errMailThemeMissing
	}
	custom := s.Mailer.Custom()
	n := 0
	for t := range custom {
		if !s.Mailer.Builtin(t) {
			n++
		}
	}
	if n >= mail.MaxCustomThemes {
		return httpx.Validation(fmt.Sprintf("自定义模板最多 %d 个", mail.MaxCustomThemes))
	}
	items := map[string]string{}
	for _, name := range mail.Names {
		content, _, _ := s.Mailer.Source(from, name)
		items[name] = content
	}
	custom[theme] = items
	return s.writeCustom(ctx, custom)
}

// DeleteMailTheme 删除自定义风格；内置风格不能删除，正在使用的风格需先切换。
func (s *Service) DeleteMailTheme(ctx context.Context, theme string) error {
	if s.Mailer.Builtin(theme) {
		return httpx.Validation("内置模板不能删除，可在各模板中恢复默认内容")
	}
	custom := s.Mailer.Custom()
	if _, ok := custom[theme]; !ok {
		return errMailThemeMissing
	}
	if s.Settings.String("email_template") == theme {
		return httpx.Validation("该模板正在使用，请先在邮件设置中切换到其它模板")
	}
	delete(custom, theme)
	return s.writeCustom(ctx, custom)
}

// PreviewMail 用示例数据渲染模板内容（不保存）。
func (s *Service) PreviewMail(name, content string) (string, error) {
	if !mail.ValidName(name) {
		return "", errMailNameMissing
	}
	if len(content) > mail.MaxTemplateSize {
		return "", httpx.Validation(fmt.Sprintf("模板内容不能超过 %d KB", mail.MaxTemplateSize>>10))
	}
	out, err := mail.RenderContent(name, content, mail.SampleVars(name, s.Settings.String("app_name"), s.Settings.String("app_url")))
	if err != nil {
		return "", httpx.Validation(err.Error())
	}
	return out, nil
}

func (s *Service) writeCustom(ctx context.Context, custom map[string]map[string]string) error {
	raw := ""
	if len(custom) > 0 {
		b, err := json.Marshal(custom)
		if err != nil {
			return err
		}
		raw = string(b)
	}
	if len(raw) > maxCustomTotal {
		return httpx.Validation(fmt.Sprintf("自定义模板合计不能超过 %d MB", maxCustomTotal>>20))
	}
	return s.Settings.Set(ctx, map[string]any{mail.CustomKey: raw})
}
