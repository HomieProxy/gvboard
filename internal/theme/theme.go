// Package theme 用户端主题：加载内置与外部主题、读取主题定义、保存主题配置（决策 D59、D60）。
//
// 主题目录结构：
//
//	<主题名>/theme.json   主题定义（格式同原项目 config.json）
//	<主题名>/index.html   页面模板，渲染时注入站点配置与主题配置
//	<主题名>/...          其它静态文件，从站点根路径访问（如 /assets/index-xxxx.js）
package theme

import (
	"context"
	"embed"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"gvboard/internal/httpx"
	"gvboard/internal/setting"
)

//go:embed all:builtin
var builtinFS embed.FS

const (
	// DefaultName 内置默认主题，当前主题不存在时使用。
	DefaultName  = "default"
	ManifestFile = "theme.json"
	IndexFile    = "index.html"

	// maxValueLen 单个配置值的长度上限（自定义 HTML 等）。
	maxValueLen = 64 << 10
)

var (
	namePattern  = regexp.MustCompile(`^[A-Za-z0-9_-]{1,64}$`)
	fieldPattern = regexp.MustCompile(`^[A-Za-z0-9_]{1,64}$`)
	fieldTypes   = []string{"input", "select", "textarea"}

	ErrNotFound = httpx.NotFoundErr("主题不存在")
	ErrManifest = errors.New("主题配置文件有误")
)

// Field 主题配置项定义。
type Field struct {
	Label         string          `json:"label"`
	Placeholder   string          `json:"placeholder,omitempty"`
	FieldName     string          `json:"field_name"`
	FieldType     string          `json:"field_type"`
	SelectOptions json.RawMessage `json:"select_options,omitempty"` // 保留原顺序
	DefaultValue  any             `json:"default_value,omitempty"`

	options map[string]any
}

// Manifest 主题定义（theme.json）。
type Manifest struct {
	Name        string  `json:"name"`
	Description string  `json:"description"`
	Version     string  `json:"version"`
	Images      string  `json:"images"`
	Configs     []Field `json:"configs"`
}

// Theme 一个可用的主题。
type Theme struct {
	Manifest
	// ID 目录名，即 frontend_theme 的取值。
	ID      string
	Builtin bool
	// Raw theme.json 原文（兼容层原样返回）。
	Raw   json.RawMessage
	Files fs.FS
}

// Index 读取页面模板。
func (t *Theme) Index() ([]byte, error) { return fs.ReadFile(t.Files, IndexFile) }

// Manager 主题管理。外部目录每次访问时读取，放入或修改主题后无需重启。
type Manager struct {
	Dir      string
	Settings *setting.Service

	builtin fs.FS
}

func New(dir string, s *setting.Service) *Manager {
	sub, _ := fs.Sub(builtinFS, "builtin")
	return &Manager{Dir: dir, Settings: s, builtin: sub}
}

// load 读取并校验一个主题目录。
func load(id string, files fs.FS, builtin bool) (*Theme, error) {
	raw, err := fs.ReadFile(files, ManifestFile)
	if err != nil {
		return nil, err
	}
	t := &Theme{ID: id, Builtin: builtin, Raw: raw, Files: files}
	if err := json.Unmarshal(raw, &t.Manifest); err != nil || t.Configs == nil {
		return nil, ErrManifest
	}
	seen := map[string]bool{}
	for i := range t.Configs {
		f := &t.Configs[i]
		if !fieldPattern.MatchString(f.FieldName) || seen[f.FieldName] {
			return nil, fmt.Errorf("%w：配置项名 %q 不合法或重复", ErrManifest, f.FieldName)
		}
		seen[f.FieldName] = true
		if f.FieldType == "" {
			f.FieldType = "input"
		}
		if !slices.Contains(fieldTypes, f.FieldType) {
			return nil, fmt.Errorf("%w：配置项 %s 的类型只能是 %v", ErrManifest, f.FieldName, fieldTypes)
		}
		if f.FieldType == "select" {
			if err := json.Unmarshal(f.SelectOptions, &f.options); err != nil || len(f.options) == 0 {
				return nil, fmt.Errorf("%w：配置项 %s 缺少选项", ErrManifest, f.FieldName)
			}
		}
	}
	if st, err := fs.Stat(files, IndexFile); err != nil || st.IsDir() {
		return nil, fmt.Errorf("%w：缺少 %s", ErrManifest, IndexFile)
	}
	return t, nil
}

// Get 读取主题，外部目录优先。主题不存在时返回 ErrNotFound，定义有误时返回 ErrManifest。
func (m *Manager) Get(id string) (*Theme, error) {
	if !namePattern.MatchString(id) {
		return nil, ErrNotFound
	}
	if m.Dir != "" {
		dir := filepath.Join(m.Dir, id)
		if st, err := os.Stat(dir); err == nil && st.IsDir() {
			return load(id, os.DirFS(dir), false)
		}
	}
	if m.builtin != nil {
		if sub, err := fs.Sub(m.builtin, id); err == nil {
			if _, err := fs.Stat(sub, ManifestFile); err == nil {
				return load(id, sub, true)
			}
		}
	}
	return nil, ErrNotFound
}

// Exists 主题存在且定义有效。
func (m *Manager) Exists(id string) bool {
	_, err := m.Get(id)
	return err == nil
}

// List 返回全部有效主题（按目录名排序），定义有误的主题跳过。
func (m *Manager) List() []*Theme {
	ids := map[string]bool{}
	if m.builtin != nil {
		if entries, err := fs.ReadDir(m.builtin, "."); err == nil {
			for _, e := range entries {
				ids[e.Name()] = e.IsDir()
			}
		}
	}
	if m.Dir != "" {
		if entries, err := os.ReadDir(m.Dir); err == nil {
			for _, e := range entries {
				ids[e.Name()] = ids[e.Name()] || e.IsDir()
			}
		}
	}
	names := make([]string, 0, len(ids))
	for id, dir := range ids {
		if dir && namePattern.MatchString(id) {
			names = append(names, id)
		}
	}
	slices.Sort(names)
	out := make([]*Theme, 0, len(names))
	for _, id := range names {
		if t, err := m.Get(id); err == nil {
			out = append(out, t)
		}
	}
	return out
}

// ActiveID 当前设置的主题名（不检查是否存在）。
func (m *Manager) ActiveID() string { return m.Settings.String("frontend_theme") }

// Active 当前主题；设置的主题不存在或有误时使用内置默认主题。
func (m *Manager) Active() (*Theme, error) {
	if t, err := m.Get(m.ActiveID()); err == nil {
		return t, nil
	}
	return m.Get(DefaultName)
}

// Activate 切换当前主题。
func (m *Manager) Activate(ctx context.Context, id string) error {
	if _, err := m.Get(id); err != nil {
		return err
	}
	return m.Settings.Set(ctx, map[string]any{"frontend_theme": id})
}

// Config 主题配置值：只含主题定义中的配置项，未保存的项取默认值（与原项目 ThemeService::init 一致）。
func (m *Manager) Config(t *Theme) map[string]any {
	saved := m.Settings.ThemeConfig(t.ID)
	out := make(map[string]any, len(t.Configs))
	for _, f := range t.Configs {
		if v, ok := saved[f.FieldName]; ok {
			out[f.FieldName] = v
		} else if f.DefaultValue != nil {
			out[f.FieldName] = f.DefaultValue
		} else {
			out[f.FieldName] = ""
		}
	}
	return out
}

// SaveConfig 按主题定义校验并保存配置：只保留定义中的配置项，未提交的项保存为空串（与原项目一致），
// 值只能是字符串、数字或布尔，下拉项必须是定义的选项之一。返回保存后的配置。
func (m *Manager) SaveConfig(ctx context.Context, id string, values map[string]any) (map[string]any, error) {
	t, err := m.Get(id)
	if err != nil {
		return nil, err
	}
	out := make(map[string]any, len(t.Configs))
	for _, f := range t.Configs {
		v, ok := values[f.FieldName]
		if !ok || v == nil {
			out[f.FieldName] = ""
			continue
		}
		switch x := v.(type) {
		case string:
			if len(x) > maxValueLen {
				return nil, httpx.Validation(fmt.Sprintf("配置项 %s 内容过长", f.Label))
			}
			if f.FieldType == "select" && x != "" {
				if _, ok := f.options[x]; !ok {
					return nil, httpx.Validation(fmt.Sprintf("配置项 %s 的取值不在选项中", f.Label))
				}
			}
		case bool, float64, int64, json.Number:
			if f.FieldType == "select" {
				if _, ok := f.options[strings.TrimSpace(fmt.Sprint(x))]; !ok {
					return nil, httpx.Validation(fmt.Sprintf("配置项 %s 的取值不在选项中", f.Label))
				}
			}
		default:
			return nil, httpx.Validation(fmt.Sprintf("配置项 %s 格式不正确", f.Label))
		}
		out[f.FieldName] = v
	}
	if err := m.Settings.SetThemeConfig(ctx, t.ID, out); err != nil {
		return nil, err
	}
	return out, nil
}
