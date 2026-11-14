package subscribe

import (
	"embed"
	"fmt"
	"sync"

	"go.uber.org/zap"

	"gvboard/internal/phpx"
)

//go:embed rules/*
var rulesFS embed.FS

// 订阅模板名称。
const (
	tplClash      = "clash"
	tplStash      = "stash"
	tplAppClash   = "app_clash"
	tplSingbox    = "singbox"
	tplSingboxOld = "singbox_old"
	tplSurge      = "surge"
	tplSurfboard  = "surfboard"
)

type tplKind int

const (
	kindText tplKind = iota
	kindYAML
	kindJSON
)

type tplDef struct {
	file string // 内置默认模板（rules 目录，取自原项目 resources/rules）
	kind tplKind
}

var tplDefs = map[string]tplDef{
	tplClash:      {"default.clash.yaml", kindYAML},
	tplStash:      {"default.stash.yaml", kindYAML},
	tplAppClash:   {"app.clash.yaml", kindYAML},
	tplSingbox:    {"default.sing-box.json", kindJSON},
	tplSingboxOld: {"default.sing-box.old.json", kindJSON},
	tplSurge:      {"default.surge.conf", kindText},
	tplSurfboard:  {"default.surfboard.conf", kindText},
}

// SettingKey 模板对应的设置项（管理端编辑，留空使用默认模板）。
func SettingKey(name string) string { return "subscribe_template_" + name }

// tplCache 已解析的模板，源内容变化时重新解析。
type tplCache struct {
	mu sync.Mutex
	m  map[string]tplEntry
}

type tplEntry struct {
	src string
	val *phpx.Arr
}

// source 模板内容：管理端设置优先，否则为内置默认模板。
func (s *Service) source(name string) (src string, custom bool, err error) {
	def, ok := tplDefs[name]
	if !ok {
		return "", false, fmt.Errorf("未知订阅模板 %s", name)
	}
	if s.Settings != nil {
		if v := s.Settings.String(SettingKey(name)); v != "" {
			return v, true, nil
		}
	}
	b, err := rulesFS.ReadFile("rules/" + def.file)
	return string(b), false, err
}

func parseTemplate(kind tplKind, src string) (*phpx.Arr, bool) {
	if kind == kindJSON {
		return phpx.ParseJSONArr([]byte(src))
	}
	return phpx.ParseYAMLArr([]byte(src))
}

// structured 解析 YAML / JSON 模板，返回副本供本次请求修改。
// 自定义模板解析失败时记录警告并使用默认模板（原项目会直接报错）。
func (s *Service) structured(name string) (*phpx.Arr, error) {
	src, custom, err := s.source(name)
	if err != nil {
		return nil, err
	}
	s.tpl.mu.Lock()
	defer s.tpl.mu.Unlock()
	if s.tpl.m == nil {
		s.tpl.m = map[string]tplEntry{}
	}
	if e, ok := s.tpl.m[name]; ok && e.src == src {
		return e.val.Clone(), nil
	}
	kind := tplDefs[name].kind
	val, ok := parseTemplate(kind, src)
	if !ok && custom {
		s.log().Warn("自定义订阅模板解析失败，使用默认模板", zap.String("template", name))
		b, err := rulesFS.ReadFile("rules/" + tplDefs[name].file)
		if err != nil {
			return nil, err
		}
		val, ok = parseTemplate(kind, string(b))
	}
	if !ok {
		return nil, fmt.Errorf("默认订阅模板 %s 解析失败", name)
	}
	s.tpl.m[name] = tplEntry{src: src, val: val}
	return val.Clone(), nil
}

func (c *Ctx) yamlTemplate(name string) (*phpx.Arr, error) { return c.svc.structured(name) }

func (c *Ctx) jsonTemplate(name string) (*phpx.Arr, error) { return c.svc.structured(name) }

func (c *Ctx) textTemplate(name string) (string, error) {
	src, _, err := c.svc.source(name)
	return src, err
}
