// Package plugin 插件管理（决策 D5、D56）：加载编译期插件、启用状态与配置（多实例同步）、
// 事件分发、过滤器、插件路由、定时任务、支付网关与订阅格式扩展，以及管理端接口。
package plugin

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/auth"
	"gvboard/internal/cache"
	"gvboard/internal/model"
	"gvboard/internal/queue"
	"gvboard/internal/setting"
	sdk "gvboard/pkg/plugin"
)

// codePattern 插件标识格式。
var codePattern = regexp.MustCompile(`^[a-z][a-z0-9_-]{1,31}$`)

// 插件状态。
const (
	StatusOK      = "ok"      // 正常
	StatusError   = "error"   // 加载失败，不能启用
	StatusMissing = "missing" // 数据库中有记录，但程序中已没有该插件
)

// changedChannel 插件启用状态或配置变更的广播频道。
const changedChannel = "plugin_changed"

// defaultFilterTimeout 单个过滤器的执行超时。
const defaultFilterTimeout = 3 * time.Second

// Manager 插件管理器。字段在 Load 前设置，之后不再修改。
type Manager struct {
	DB       *gorm.DB
	Cache    *cache.Cache // 多实例同步，可为 nil
	Auth     *auth.Service
	Settings *setting.Service
	Log      *zap.Logger
	// Middleware 插件路由额外执行的中间件（如语言识别）。
	Middleware []gin.HandlerFunc
	// FilterTimeout 单个过滤器的执行超时，为 0 时为 3 秒。
	FilterTimeout time.Duration
	Now           func() time.Time

	queue *queue.Queue // 为 nil 时事件同步执行（测试用）
	// jsAllowPrivate 允许 JS 插件访问内网地址（仅测试用）
	jsAllowPrivate bool
	jsMu           sync.Mutex
	mu             sync.RWMutex
	entries        map[string]*entry
	codes          []string // 按标识排序
	state          atomic.Pointer[map[string]*state]
	cleanups       []func()
}

// state 数据库中的插件状态。
type state struct {
	enabled     bool
	config      map[string]any // 已合并默认值
	installedAt int64
}

func (m *Manager) now() time.Time {
	if m.Now != nil {
		return m.Now()
	}
	return time.Now()
}

func (m *Manager) log() *zap.Logger {
	if m.Log != nil {
		return m.Log
	}
	return zap.NewNop()
}

func (m *Manager) filterTimeout() time.Duration {
	if m.FilterTimeout > 0 {
		return m.FilterTimeout
	}
	return defaultFilterTimeout
}

// Load 加载编译期插件：依次调用 Setup，在 v2_plugin 中登记新插件（默认停用），然后读取启用状态与配置。
// 插件标识无效或重复属于编程错误，返回错误；单个插件 Setup 失败只标记为加载失败。
func (m *Manager) Load(ctx context.Context, plugins []sdk.Plugin) error {
	m.mu.Lock()
	m.entries, m.codes = map[string]*entry{}, nil
	m.mu.Unlock()
	for _, p := range plugins {
		info, err := safeInfo(p)
		if err != nil {
			return err
		}
		if !codePattern.MatchString(info.Code) {
			return fmt.Errorf("插件标识 %q 无效：必须以小写字母开头，只能包含小写字母、数字、下划线、短横线，2～32 个字符", info.Code)
		}
		m.mu.RLock()
		_, dup := m.entries[info.Code]
		m.mu.RUnlock()
		if dup {
			return fmt.Errorf("插件标识 %q 重复", info.Code)
		}
		e := m.setup(p, info, model.PluginTypeGo)
		m.mu.Lock()
		m.entries[e.code] = e
		m.codes = append(m.codes, e.code)
		sort.Strings(m.codes)
		m.mu.Unlock()
	}
	if err := m.register(ctx); err != nil {
		return err
	}
	return m.Reload(ctx)
}

func safeInfo(p sdk.Plugin) (info sdk.Info, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("读取插件信息失败: %v", r)
		}
	}()
	if p == nil {
		return info, errors.New("插件不能为 nil")
	}
	return p.Info(), nil
}

// register 在 v2_plugin 中登记插件并更新版本号。
func (m *Manager) register(ctx context.Context) error {
	db := m.DB.WithContext(ctx)
	for _, e := range m.list() {
		var row model.Plugin
		err := db.Where("code = ?", e.code).Take(&row).Error
		if errors.Is(err, gorm.ErrRecordNotFound) {
			row = model.Plugin{Code: e.code, Type: e.kind, Version: e.info.Version}
			res := db.Clauses(clause.OnConflict{DoNothing: true}).Create(&row)
			if res.Error != nil {
				return fmt.Errorf("登记插件 %s 失败: %w", e.code, res.Error)
			}
			if res.RowsAffected == 1 {
				continue
			}
			// 其它实例同时登记
			err = db.Where("code = ?", e.code).Take(&row).Error
		}
		if err != nil {
			return err
		}
		if row.Type != e.kind {
			e.fail(fmt.Errorf("插件标识与已安装的 %s 插件冲突", row.Type))
			continue
		}
		if row.Version != e.info.Version {
			if err := db.Model(&row).Update("version", e.info.Version).Error; err != nil {
				return err
			}
		}
	}
	return nil
}

// Reload 从数据库同步 JS 插件，并重新读取启用状态与配置。
func (m *Manager) Reload(ctx context.Context) error {
	if err := m.syncJS(ctx); err != nil {
		return err
	}
	var rows []model.Plugin
	if err := m.DB.WithContext(ctx).Find(&rows).Error; err != nil {
		return fmt.Errorf("读取插件状态失败: %w", err)
	}
	st := make(map[string]*state, len(rows))
	for _, r := range rows {
		s := &state{enabled: r.Enabled == 1, installedAt: r.CreatedAt}
		var raw map[string]any
		if err := r.Config.Decode(&raw); err != nil {
			m.log().Warn("插件配置解析失败，使用默认配置", zap.String("plugin", r.Code), zap.Error(err))
		}
		var fields []sdk.Field
		if e := m.entry(r.Code); e != nil {
			fields = e.fields
		}
		s.config = withDefaults(fields, raw)
		st[r.Code] = s
	}
	m.state.Store(&st)
	return nil
}

// Watch 订阅其它实例的变更通知并重新加载，阻塞直到 ctx 取消。
func (m *Manager) Watch(ctx context.Context) {
	if m.Cache == nil {
		return
	}
	sub := m.Cache.Subscribe(ctx, m.Cache.Key(changedChannel))
	defer sub.Close()
	ch := sub.Channel()
	for {
		select {
		case <-ctx.Done():
			return
		case _, ok := <-ch:
			if !ok {
				return
			}
			if err := m.Reload(ctx); err != nil {
				m.log().Error("重新加载插件状态失败", zap.Error(err))
			}
		}
	}
}

// changed 本实例重新加载并通知其它实例。
func (m *Manager) changed(ctx context.Context) error {
	if err := m.Reload(ctx); err != nil {
		return err
	}
	if m.Cache != nil {
		if err := m.Cache.Publish(ctx, m.Cache.Key(changedChannel), "1").Err(); err != nil {
			m.log().Warn("广播插件变更失败", zap.Error(err))
		}
	}
	return nil
}

// Close 注销插件注册的支付网关与订阅格式，释放 JS 插件运行时。
func (m *Manager) Close() {
	m.mu.Lock()
	fns := m.cleanups
	m.cleanups = nil
	m.mu.Unlock()
	for _, f := range fns {
		f()
	}
	for _, e := range m.list() {
		e.unload()
	}
}

func (m *Manager) entry(code string) *entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.entries[code]
}

// list 全部插件，按标识排序。
func (m *Manager) list() []*entry {
	m.mu.RLock()
	defer m.mu.RUnlock()
	out := make([]*entry, 0, len(m.codes))
	for _, c := range m.codes {
		out = append(out, m.entries[c])
	}
	return out
}

func (m *Manager) stateOf(code string) *state {
	if p := m.state.Load(); p != nil {
		return (*p)[code]
	}
	return nil
}

// Enabled 插件存在、加载成功且已启用。
func (m *Manager) Enabled(code string) bool {
	e := m.entry(code)
	if e == nil || e.failed() {
		return false
	}
	s := m.stateOf(code)
	return s != nil && s.enabled
}

// config 插件当前配置的副本。
func (m *Manager) config(code string) sdk.Config {
	out := sdk.Config{}
	if s := m.stateOf(code); s != nil {
		for k, v := range s.config {
			out[k] = v
		}
		return out
	}
	if e := m.entry(code); e != nil {
		for k, v := range withDefaults(e.fields, nil) {
			out[k] = v
		}
	}
	return out
}
