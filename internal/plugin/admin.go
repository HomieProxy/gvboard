package plugin

import (
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"sort"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/server"
	sdk "gvboard/pkg/plugin"
)

// 管理端错误。管理端只有中文界面，与其它管理端模块一致直接使用中文文案。
var (
	errNotFound    = httpx.NotFoundErr("插件不存在")
	errInvalid     = httpx.Validation("参数有误")
	errNotMissing  = httpx.BadRequest("只能删除已从程序中移除的插件")
	errMissing     = httpx.BadRequest("程序中已没有该插件")
	errNotDisabled = httpx.BadRequest("请先停用插件")
)

// enableTimeout 插件 Enable 回调的超时。
const enableTimeout = 30 * time.Second

// Features 插件注册的功能。
type Features struct {
	Events   []string    `json:"events"`
	Filters  []string    `json:"filters"`
	Routes   []routeInfo `json:"routes"`
	Jobs     []string    `json:"jobs"`
	Payments []string    `json:"payments"`
	Formats  []string    `json:"formats"`
	Widgets  []string    `json:"widgets"`
	Banners  []string    `json:"banners"`
	Pages    []string    `json:"pages"`
}

// View 管理端插件信息。
type View struct {
	sdk.Info
	Type        string         `json:"type"`
	Status      string         `json:"status"`
	Error       string         `json:"error,omitempty"`
	Enabled     bool           `json:"enabled"`
	InstalledAt int64          `json:"installed_at"`
	Fields      []sdk.Field    `json:"fields"`
	Config      map[string]any `json:"config"`
	Features    Features       `json:"features"`
	// Display 插件已注册的用户端显示形式（widget / banner / page）是否开启。
	Display map[string]bool `json:"display"`
	// Package JS 插件的安装包信息（可访问的域名、权限）。
	Package *PackageInfo `json:"package,omitempty"`
	// DBWarning 启用前需要管理员确认数据库访问警告（非内置 Go 插件且尚未确认过）。
	DBWarning bool `json:"db_warning"`
}

// builtinGo 随程序提供的 Go 插件，启用时不提示数据库访问警告。
var builtinGo = map[string]bool{"checkin": true, "first_order": true, "webhook": true, "lucky_wheel": true}

// errDBWarning 启用非内置 Go 插件前需要确认数据库访问警告。
var errDBWarning = httpx.Conflict("该插件可直接访问数据库，若直接修改用户余额将不会记录余额明细，请确认后再启用")

// needsDBAck 启用前是否需要确认数据库访问警告。
func (m *Manager) needsDBAck(e *entry) bool {
	return e.kind == model.PluginTypeGo && !builtinGo[e.code] && !slices.Contains(m.Settings.Strings("plugin_db_warning_ack"), e.code)
}

// ackDB 记录管理员已确认该插件的数据库访问警告。
func (m *Manager) ackDB(ctx context.Context, code string) error {
	acked := m.Settings.Strings("plugin_db_warning_ack")
	if slices.Contains(acked, code) {
		return nil
	}
	return m.Settings.Set(ctx, map[string]any{"plugin_db_warning_ack": append(slices.Clone(acked), code)})
}

// enableAcked 启用插件；需要确认数据库访问警告时 ack 必须为 true，启用成功后记录确认。
func (m *Manager) enableAcked(ctx context.Context, code string, ack bool) error {
	e := m.entry(code)
	need := e != nil && m.needsDBAck(e)
	if need && !ack {
		return errDBWarning
	}
	if err := m.Enable(ctx, code); err != nil {
		return err
	}
	if need {
		return m.ackDB(ctx, code)
	}
	return nil
}

func (m *Manager) view(e *entry, row *model.Plugin) View {
	v := View{Info: e.info, Type: e.kind, Status: StatusOK, Fields: e.fields, Config: m.config(e.code), Package: e.pkg, DBWarning: m.needsDBAck(e)}
	if v.Fields == nil {
		v.Fields = []sdk.Field{}
	}
	if row != nil {
		v.Enabled, v.InstalledAt = row.Enabled == 1, row.CreatedAt
	}
	e.mu.RLock()
	defer e.mu.RUnlock()
	if e.err != "" {
		v.Status, v.Error = StatusError, e.err
		v.Enabled = false
	}
	f := Features{Events: []string{}, Filters: []string{}, Jobs: []string{}, Payments: append([]string{}, e.pays...), Formats: []string{}, Widgets: []string{}, Banners: []string{}, Pages: []string{}}
	for name := range e.events {
		f.Events = append(f.Events, name)
	}
	for name := range e.filters {
		f.Filters = append(f.Filters, name)
	}
	sort.Strings(f.Events)
	sort.Strings(f.Filters)
	for _, j := range e.jobs {
		f.Jobs = append(f.Jobs, j.name+" ("+j.spec+")")
	}
	for _, p := range e.formats {
		f.Formats = append(f.Formats, p.Name)
	}
	for _, w := range e.widgets {
		f.Widgets = append(f.Widgets, w.Title+" ("+w.Key+")")
	}
	for _, b := range e.banners {
		f.Banners = append(f.Banners, cmp.Or(b.Title, b.Key)+" ("+b.Key+")")
	}
	for _, p := range e.pages {
		f.Pages = append(f.Pages, p.Title+" (/plugin/"+e.code+"/"+p.Key+")")
	}
	f.Routes = e.routes()
	v.Features = f
	v.Display = m.display(e)
	return v
}

// missingView 程序中已没有的插件（只有数据库记录）。
func missingView(row *model.Plugin) View {
	var cfg map[string]any
	_ = row.Config.Decode(&cfg)
	if cfg == nil {
		cfg = map[string]any{}
	}
	return View{
		Info: sdk.Info{Code: row.Code, Name: row.Code, Version: row.Version}, Type: row.Type, Status: StatusMissing,
		Enabled: row.Enabled == 1, InstalledAt: row.CreatedAt, Fields: []sdk.Field{}, Config: cfg, Display: map[string]bool{},
		Features: Features{Events: []string{}, Filters: []string{}, Routes: []routeInfo{}, Jobs: []string{}, Payments: []string{}, Formats: []string{}, Widgets: []string{}, Banners: []string{}, Pages: []string{}},
	}
}

// List 全部插件：程序中的插件按标识排序，之后是已移除的插件。
func (m *Manager) List(ctx context.Context) ([]View, error) {
	var rows []model.Plugin
	if err := m.DB.WithContext(ctx).Order("code").Find(&rows).Error; err != nil {
		return nil, err
	}
	byCode := map[string]*model.Plugin{}
	for i := range rows {
		byCode[rows[i].Code] = &rows[i]
	}
	out := []View{}
	for _, e := range m.list() {
		out = append(out, m.view(e, byCode[e.code]))
		delete(byCode, e.code)
	}
	for i := range rows {
		if r, ok := byCode[rows[i].Code]; ok {
			out = append(out, missingView(r))
		}
	}
	return out, nil
}

// Get 插件详情。
func (m *Manager) Get(ctx context.Context, code string) (*View, error) {
	row, err := m.row(ctx, m.DB, code)
	if err != nil {
		return nil, err
	}
	if e := m.entry(code); e != nil {
		v := m.view(e, row)
		return &v, nil
	}
	if row == nil {
		return nil, errNotFound
	}
	v := missingView(row)
	return &v, nil
}

func (m *Manager) row(ctx context.Context, db *gorm.DB, code string) (*model.Plugin, error) {
	var row model.Plugin
	err := db.WithContext(ctx).Where("code = ?", code).Take(&row).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &row, nil
}

// Enable 启用插件：检查必填配置，调用插件的 Enable 回调，成功后启用并通知其它实例。
func (m *Manager) Enable(ctx context.Context, code string) error {
	e := m.entry(code)
	if e == nil {
		if row, err := m.row(ctx, m.DB, code); err != nil {
			return err
		} else if row != nil {
			return errMissing
		}
		return errNotFound
	}
	e.mu.RLock()
	loadErr := e.err
	e.mu.RUnlock()
	if loadErr != "" {
		return httpx.BadRequest("插件加载失败，不能启用：" + loadErr)
	}
	if m.Enabled(code) {
		return nil
	}
	if err := checkRequired(e.fields, m.config(code)); err != nil {
		return httpx.Validation("请先完成插件配置：" + err.Error())
	}
	if en, ok := e.plugin.(sdk.Enabler); ok {
		if err := callTimeout(ctx, enableTimeout, func(ctx context.Context) error { return en.Enable(ctx, e.host) }); err != nil {
			return httpx.BadRequest("插件启用失败：" + err.Error())
		}
	}
	if err := m.DB.WithContext(ctx).Model(&model.Plugin{}).Where("code = ?", code).Update("enabled", 1).Error; err != nil {
		return err
	}
	return m.changed(ctx)
}

// Disable 停用插件，之后调用插件的 Disable 回调（错误只记录日志）。
func (m *Manager) Disable(ctx context.Context, code string) error {
	row, err := m.row(ctx, m.DB, code)
	if err != nil {
		return err
	}
	if row == nil {
		return errNotFound
	}
	if row.Enabled == 0 {
		return nil
	}
	if err := m.DB.WithContext(ctx).Model(row).Update("enabled", 0).Error; err != nil {
		return err
	}
	if err := m.changed(ctx); err != nil {
		return err
	}
	if e := m.entry(code); e != nil {
		if d, ok := e.plugin.(sdk.Disabler); ok {
			if err := callTimeout(ctx, enableTimeout, func(ctx context.Context) error { return d.Disable(ctx, e.host) }); err != nil {
				m.log().Warn("插件停用回调失败", zap.String("plugin", code), zap.Error(err))
			}
		}
	}
	return nil
}

// SaveConfig 保存配置（未提交的项保持原值），立即生效并通知其它实例。
func (m *Manager) SaveConfig(ctx context.Context, code string, in map[string]any) error {
	e := m.entry(code)
	if e == nil {
		return errNotFound
	}
	row, err := m.row(ctx, m.DB, code)
	if err != nil {
		return err
	}
	if row == nil {
		return errNotFound
	}
	var current map[string]any
	_ = row.Config.Decode(&current)
	cfg, err := normalize(e.fields, current, in)
	if err != nil {
		return httpx.Validation(err.Error())
	}
	b, err := json.Marshal(cfg)
	if err != nil {
		return err
	}
	if err := m.DB.WithContext(ctx).Model(row).Update("config", model.JSON(b)).Error; err != nil {
		return err
	}
	return m.changed(ctx)
}

// Delete 删除 JS 插件或已从程序中移除的 Go 插件：删除记录、安装包及其 KV 数据。
func (m *Manager) Delete(ctx context.Context, code string) error {
	if e := m.entry(code); e != nil && e.kind != model.PluginTypeJS {
		return errNotMissing
	}
	row, err := m.row(ctx, m.DB, code)
	if err != nil {
		return err
	}
	if row == nil {
		return errNotFound
	}
	if row.Enabled == 1 {
		return errNotDisabled
	}
	err = m.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("plugin_code = ?", code).Delete(&model.PluginKV{}).Error; err != nil {
			return err
		}
		if err := tx.Where("plugin_code = ?", code).Delete(&model.PluginPackage{}).Error; err != nil {
			return err
		}
		return tx.Delete(row).Error
	})
	if err != nil {
		return err
	}
	return m.changed(ctx)
}

func callTimeout(ctx context.Context, d time.Duration, fn func(ctx context.Context) error) error {
	ctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	ch := make(chan error, 1)
	go func() {
		defer func() {
			if r := recover(); r != nil {
				ch <- fmt.Errorf("panic: %v", r)
			}
		}()
		ch <- fn(ctx)
	}()
	select {
	case err := <-ch:
		return err
	case <-ctx.Done():
		return fmt.Errorf("执行超时（%s）", d)
	}
}

// Register 挂载路由：
//
//	ANY {api}/plugin/{code}/*                       插件访客 / 用户路由
//	GET    {api}/user/plugin-widgets                已启用插件的用户端卡片（可选 slot 只返回该建议位置）
//	GET    {api}/user/plugin-banners                已启用插件的用户端横幅
//	GET    {api}/user/plugin-pages                  已启用插件的用户端页面
//	ANY {api}/{secure_path}/plugin/{code}/*         插件管理端路由
//	GET    {api}/{secure_path}/plugins              插件列表
//	GET    {api}/{secure_path}/plugins/{code}       插件详情
//	POST   {api}/{secure_path}/plugins/{code}/enable  启用 {"acknowledge": true}（首次启用非内置 Go 插件时必填，否则 409）
//	POST   {api}/{secure_path}/plugins/{code}/disable 停用
//	PUT    {api}/{secure_path}/plugins/{code}/config  保存配置 {"config": {...}}
//	PUT    {api}/{secure_path}/plugins/{code}/display 用户端显示形式开关 {"widget": bool, "banner": bool, "page": bool}
//	DELETE {api}/{secure_path}/plugins/{code}       删除 JS 插件或已移除插件的记录
//	POST   {api}/{secure_path}/plugins/upload       上传 JS 插件 zip（表单 file）：不带 sha256 时只预览，
//	                                                带预览返回的 sha256 时安装 / 更新
func (m *Manager) Register(r *server.Router) {
	r.API.Any("/plugin/:code/*path", m.serve(false))
	r.API.GET("/user/plugin-widgets", m.Auth.RequireUser(), m.userWidgets)
	r.API.GET("/user/plugin-banners", m.Auth.RequireUser(), func(c *gin.Context) { httpx.OK(c, m.Banners()) })
	r.API.GET("/user/plugin-pages", m.Auth.RequireUser(), func(c *gin.Context) { httpx.OK(c, m.Pages()) })
	a := m.Auth.AdminGroup(r.API, func() string { return m.Settings.String("secure_path") })
	a.Any("/plugin/:code/*path", m.serve(true))
	a.GET("/plugins", func(c *gin.Context) { reply[[]View](c)(m.List(c.Request.Context())) })
	a.GET("/plugins/:code", func(c *gin.Context) { reply[*View](c)(m.Get(c.Request.Context(), c.Param("code"))) })
	a.POST("/plugins/:code/enable", func(c *gin.Context) {
		var in struct {
			Acknowledge bool `json:"acknowledge"`
		}
		if err := c.ShouldBindJSON(&in); err != nil && !errors.Is(err, io.EOF) {
			httpx.Fail(c, errInvalid)
			return
		}
		done(c, m.enableAcked(c.Request.Context(), c.Param("code"), in.Acknowledge))
	})
	a.POST("/plugins/:code/disable", func(c *gin.Context) { done(c, m.Disable(c.Request.Context(), c.Param("code"))) })
	a.PUT("/plugins/:code/config", func(c *gin.Context) {
		var in struct {
			Config map[string]any `json:"config"`
		}
		if c.ShouldBindJSON(&in) != nil || in.Config == nil {
			httpx.Fail(c, errInvalid)
			return
		}
		done(c, m.SaveConfig(c.Request.Context(), c.Param("code"), in.Config))
	})
	a.PUT("/plugins/:code/display", func(c *gin.Context) {
		var in map[string]bool
		if c.ShouldBindJSON(&in) != nil || in == nil {
			httpx.Fail(c, errInvalid)
			return
		}
		done(c, m.SetDisplay(c.Request.Context(), c.Param("code"), in))
	})
	a.DELETE("/plugins/:code", func(c *gin.Context) { done(c, m.Delete(c.Request.Context(), c.Param("code"))) })
	a.POST("/plugins/upload", m.upload)
}

func (m *Manager) upload(c *gin.Context) {
	c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, maxPackageSize+64<<10)
	fh, err := c.FormFile("file")
	if err != nil {
		httpx.Fail(c, httpx.Validation(fmt.Sprintf("请上传不超过 %d MB 的 zip 安装包", maxPackageSize>>20)))
		return
	}
	f, err := fh.Open()
	if err != nil {
		httpx.Fail(c, errInvalid)
		return
	}
	defer f.Close()
	raw, err := io.ReadAll(io.LimitReader(f, maxPackageSize+1))
	if err != nil {
		httpx.Fail(c, errInvalid)
		return
	}
	ctx := c.Request.Context()
	if sha := c.PostForm("sha256"); sha != "" {
		reply[*View](c)(m.Install(ctx, raw, sha))
		return
	}
	reply[*PackageView](c)(m.Preview(ctx, raw))
}

func reply[T any](c *gin.Context) func(T, error) {
	return func(v T, err error) {
		if err != nil {
			httpx.Fail(c, err)
			return
		}
		httpx.OK(c, v)
	}
}

func done(c *gin.Context, err error) {
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, true)
}
