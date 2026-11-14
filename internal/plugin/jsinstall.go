package plugin

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"sync"
	"time"

	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/hook"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/phpx"
	sdk "gvboard/pkg/plugin"
)

var (
	errPackageChanged  = httpx.BadRequest("安装包与预览时不一致，请重新上传并确认")
	errBuiltinConflict = httpx.BadRequest("插件标识与程序内置插件冲突")
)

// Permission 权限及说明。
type Permission struct {
	Key         string `json:"key"`
	Description string `json:"description"`
}

// PackageInfo JS 插件安装包信息。
type PackageInfo struct {
	SHA256      string       `json:"sha256"`
	Size        int64        `json:"size"`
	HTTP        []string     `json:"http"`
	Permissions []Permission `json:"permissions"`
}

func packageInfo(p *jsPackage) *PackageInfo {
	all := Permissions()
	info := &PackageInfo{SHA256: p.sha256, Size: p.size, HTTP: append([]string{}, p.manifest.HTTP...), Permissions: []Permission{}}
	for _, k := range p.manifest.Permissions {
		info.Permissions = append(info.Permissions, Permission{Key: k, Description: all[k]})
	}
	return info
}

// PackageView 上传安装包后的预览：管理员确认可访问的域名与权限后再安装。
type PackageView struct {
	sdk.Info
	PackageInfo
	Fields []sdk.Field `json:"fields"`
	// InstalledVersion 已安装的版本，为空表示新安装。
	InstalledVersion string `json:"installed_version"`
}

func (m *Manager) checkPackage(ctx context.Context, raw []byte) (*jsPackage, *model.Plugin, error) {
	pkg, err := readPackage(raw)
	if err != nil {
		return nil, nil, httpx.Validation(err.Error())
	}
	code := pkg.manifest.Code
	if e := m.entry(code); e != nil && e.kind != model.PluginTypeJS {
		return nil, nil, errBuiltinConflict
	}
	row, err := m.row(ctx, m.DB, code)
	if err != nil {
		return nil, nil, err
	}
	if row != nil && row.Type != model.PluginTypeJS {
		return nil, nil, errBuiltinConflict
	}
	return pkg, row, nil
}

// Preview 解析安装包，不安装。
func (m *Manager) Preview(ctx context.Context, raw []byte) (*PackageView, error) {
	pkg, row, err := m.checkPackage(ctx, raw)
	if err != nil {
		return nil, err
	}
	v := &PackageView{Info: pkg.manifest.info(), PackageInfo: *packageInfo(pkg), Fields: pkg.manifest.Config}
	if v.Fields == nil {
		v.Fields = []sdk.Field{}
	}
	if row != nil {
		v.InstalledVersion = row.Version
	}
	return v, nil
}

// Install 安装或更新 JS 插件。sha256 必须与预览时一致（管理员已确认域名与权限）。
// 新安装的插件默认停用；更新时保留启用状态与配置，立即生效。
func (m *Manager) Install(ctx context.Context, raw []byte, sha string) (*View, error) {
	pkg, _, err := m.checkPackage(ctx, raw)
	if err != nil {
		return nil, err
	}
	if sha != pkg.sha256 {
		return nil, errPackageChanged
	}
	mf, err := json.Marshal(pkg.manifest)
	if err != nil {
		return nil, err
	}
	code := pkg.manifest.Code
	err = m.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		row, err := m.row(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), code)
		if err != nil {
			return err
		}
		switch {
		case row == nil:
			if err := tx.Create(&model.Plugin{Code: code, Type: model.PluginTypeJS, Version: pkg.manifest.Version}).Error; err != nil {
				return err
			}
		case row.Type != model.PluginTypeJS:
			return errBuiltinConflict
		default:
			if err := tx.Model(row).Update("version", pkg.manifest.Version).Error; err != nil {
				return err
			}
		}
		return tx.Clauses(clause.OnConflict{
			Columns:   []clause.Column{{Name: "plugin_code"}},
			DoUpdates: clause.AssignmentColumns([]string{"sha256", "size", "manifest", "archive", "updated_at"}),
		}).Create(&model.PluginPackage{
			PluginCode: code, SHA256: pkg.sha256, Size: pkg.size, Manifest: string(mf),
			Archive: base64.StdEncoding.EncodeToString(pkg.raw),
		}).Error
	})
	if err != nil {
		return nil, err
	}
	m.log().Info("安装 JS 插件", zap.String("plugin", code), zap.String("version", pkg.manifest.Version), zap.String("sha256", pkg.sha256))
	if err := m.changed(ctx); err != nil {
		return nil, err
	}
	return m.Get(ctx, code)
}

// syncJS 按数据库中的安装包加载、更新、卸载 JS 插件（安装包未变化时不重新加载）。
func (m *Manager) syncJS(ctx context.Context) error {
	m.jsMu.Lock()
	defer m.jsMu.Unlock()
	var pkgs []model.PluginPackage
	if err := m.DB.WithContext(ctx).Select("plugin_code", "sha256").Find(&pkgs).Error; err != nil {
		return fmt.Errorf("读取 JS 插件失败: %w", err)
	}
	want := make(map[string]string, len(pkgs))
	for _, p := range pkgs {
		want[p.PluginCode] = p.SHA256
	}
	for code, sha := range want {
		e := m.entry(code)
		if e != nil && (e.kind != model.PluginTypeJS || e.sha == sha) {
			continue
		}
		ne, err := m.loadJS(ctx, code)
		if err != nil {
			return err
		}
		m.putEntry(ne)
	}
	for _, e := range m.list() {
		if _, ok := want[e.code]; !ok && e.kind == model.PluginTypeJS {
			m.removeEntry(e.code)
		}
	}
	return nil
}

func (m *Manager) loadJS(ctx context.Context, code string) (*entry, error) {
	var row model.PluginPackage
	if err := m.DB.WithContext(ctx).Where("plugin_code = ?", code).Take(&row).Error; err != nil {
		return nil, fmt.Errorf("读取 JS 插件 %s 失败: %w", code, err)
	}
	failed := func(err error) *entry {
		e := &entry{code: code, kind: model.PluginTypeJS, sha: row.SHA256, info: sdk.Info{Code: code, Name: code},
			events: map[string][]sdk.EventHandler{}, filters: map[string][]filterReg{}}
		m.log().Error("JS 插件加载失败", zap.String("plugin", code), zap.Error(err))
		e.fail(err)
		return e
	}
	raw, err := base64.StdEncoding.DecodeString(row.Archive)
	if err != nil {
		return failed(errors.New("安装包数据损坏")), nil
	}
	pkg, err := readPackage(raw)
	if err != nil {
		return failed(err), nil
	}
	if pkg.manifest.Code != code {
		return failed(errors.New("安装包中的插件标识不一致")), nil
	}
	jp := m.newJSPlugin(pkg)
	e := m.setup(jp, pkg.manifest.info(), model.PluginTypeJS)
	e.sha, e.pkg = row.SHA256, packageInfo(pkg)
	if e.failed() {
		jp.close()
	}
	return e, nil
}

func (m *Manager) putEntry(e *entry) {
	m.mu.Lock()
	old := m.entries[e.code]
	m.entries[e.code] = e
	if old == nil {
		m.codes = append(m.codes, e.code)
		sort.Strings(m.codes)
	}
	m.mu.Unlock()
	if old != nil {
		old.unload()
	}
}

func (m *Manager) removeEntry(code string) {
	m.mu.Lock()
	old := m.entries[code]
	delete(m.entries, code)
	if i := sort.SearchStrings(m.codes, code); i < len(m.codes) && m.codes[i] == code {
		m.codes = append(m.codes[:i], m.codes[i+1:]...)
	}
	m.mu.Unlock()
	if old != nil {
		old.unload()
	}
}

// unload 释放 JS 插件的运行时。
func (e *entry) unload() {
	if jp, ok := e.plugin.(*jsPlugin); ok {
		jp.close()
	}
}

// runJSJobs 每分钟执行到期的 JS 插件定时任务（JS 插件可随时安装、更新，不能注册到启动时的调度器中）。
func (m *Manager) runJSJobs(ctx context.Context) error {
	now := m.now().Truncate(time.Minute)
	var wg sync.WaitGroup
	for _, e := range m.list() {
		if e.kind != model.PluginTypeJS || !m.Enabled(e.code) {
			continue
		}
		for _, j := range e.jobs {
			sched, err := cron.ParseStandard(j.spec)
			if err != nil || !sched.Next(now.Add(-time.Second)).Equal(now) {
				continue
			}
			wg.Add(1)
			go func(code string, j jobReg) {
				defer wg.Done()
				defer func() {
					if r := recover(); r != nil {
						m.log().Error("JS 插件定时任务 panic", zap.String("plugin", code), zap.String("job", j.name), zap.Any("panic", r))
					}
				}()
				if err := j.run(ctx); err != nil {
					m.log().Warn("JS 插件定时任务失败", zap.String("plugin", code), zap.String("job", j.name), zap.Error(err))
				}
			}(e.code, j)
		}
	}
	wg.Wait()
	return nil
}

// decodeServers 解析 JS 返回的节点列表（保持与原数据相同的有序数组结构）。
func decodeServers(o hook.SubscribeServers, b []byte) (any, error) {
	var x struct {
		Servers json.RawMessage `json:"servers"`
	}
	if err := json.Unmarshal(b, &x); err != nil {
		return nil, err
	}
	if len(x.Servers) == 0 || string(x.Servers) == "null" {
		return o, nil
	}
	v, err := phpx.JSONDecode(x.Servers)
	if err != nil {
		return nil, err
	}
	arr, ok := v.(*phpx.Arr)
	if !ok || (arr.Len() > 0 && !arr.IsList()) {
		return nil, errors.New("servers 必须为数组")
	}
	servers := make([]*phpx.Arr, 0, arr.Len())
	for _, s := range arr.Values() {
		sa, ok := s.(*phpx.Arr)
		if !ok {
			return nil, errors.New("节点必须为对象")
		}
		servers = append(servers, sa)
	}
	o.Servers = servers
	return o, nil
}
