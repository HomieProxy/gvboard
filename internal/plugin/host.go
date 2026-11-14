package plugin

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"slices"
	"sync"

	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/hook"
	"gvboard/internal/payment"
	"gvboard/internal/subscribe"
	sdk "gvboard/pkg/plugin"
)

var jobNamePattern = regexp.MustCompile(`^[a-z0-9_]{1,32}$`)

type filterReg struct {
	code     string
	priority int
	fn       sdk.FilterFunc
}

type jobReg struct {
	name, spec string
	run        func(ctx context.Context) error
}

// entry 一个插件。注册内容只在 Setup 期间写入，之后只读。
type entry struct {
	code   string
	kind   string
	info   sdk.Info
	plugin sdk.Plugin
	fields []sdk.Field
	host   *host
	sha    string       // JS 插件安装包的 SHA-256
	pkg    *PackageInfo // JS 插件安装包信息

	mu      sync.RWMutex
	err     string
	events  map[string][]sdk.EventHandler
	filters map[string][]filterReg
	pub     *gin.Engine      // 访客与用户路由
	user    *gin.RouterGroup // 用户路由组（需登录）
	admin   *gin.Engine      // 管理端路由
	jobs    []jobReg
	pays    []string
	formats []*subscribe.Protocol
}

func (e *entry) failed() bool {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.err != ""
}

// fail 标记加载失败，撤销已注册的全部功能。
func (e *entry) fail(err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.err = err.Error()
	for _, name := range e.pays {
		payment.UnregisterExt(name)
	}
	for _, f := range e.formats {
		subscribe.UnregisterExt(f)
	}
	e.events, e.filters, e.pub, e.user, e.admin, e.jobs, e.pays, e.formats = nil, nil, nil, nil, nil, nil, nil, nil
}

// setup 创建插件条目并调用 Setup。
func (m *Manager) setup(p sdk.Plugin, info sdk.Info, kind string) *entry {
	e := &entry{
		code: info.Code, kind: kind, info: info, plugin: p,
		events: map[string][]sdk.EventHandler{}, filters: map[string][]filterReg{},
	}
	e.host = &host{m: m, e: e, setup: true, log: m.log().Named("plugin." + info.Code)}
	if c, ok := p.(sdk.Configurable); ok {
		fields, err := safeFields(c)
		if err == nil {
			err = validateFields(fields)
		}
		if err != nil {
			e.fail(fmt.Errorf("配置项定义错误: %w", err))
			return e
		}
		e.fields = fields
	}
	err := func() (err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("Setup panic: %v", r)
			}
		}()
		return p.Setup(e.host)
	}()
	e.host.mu.Lock()
	e.host.setup = false
	if err == nil && len(e.host.errs) > 0 {
		err = errors.Join(e.host.errs...)
	}
	e.host.mu.Unlock()
	if err != nil {
		m.log().Error("插件加载失败", zap.String("plugin", e.code), zap.Error(err))
		e.fail(err)
	}
	return e
}

func safeFields(c sdk.Configurable) (fs []sdk.Field, err error) {
	defer func() {
		if r := recover(); r != nil {
			err = fmt.Errorf("Fields panic: %v", r)
		}
	}()
	return c.Fields(), nil
}

// host 实现 sdk.Host。
type host struct {
	m     *Manager
	e     *entry
	log   *zap.Logger
	mu    sync.Mutex
	setup bool
	errs  []error
}

var _ sdk.Host = (*host)(nil)

func (h *host) Code() string       { return h.e.code }
func (h *host) Enabled() bool      { return h.m.Enabled(h.e.code) }
func (h *host) Config() sdk.Config { return h.m.config(h.e.code) }
func (h *host) KV() sdk.KV         { return &kv{m: h.m, code: h.e.code} }
func (h *host) DB() *gorm.DB       { return h.m.DB }
func (h *host) Log() *zap.Logger   { return h.log }
func (h *host) fail(format string, a ...any) {
	h.errs = append(h.errs, fmt.Errorf(format, a...))
}

// registering 检查是否处于 Setup 中，返回 false 时调用方直接返回。调用方需持有 h.mu。
func (h *host) registering(what string) bool {
	if !h.setup {
		h.log.Error("只能在 Setup 中注册" + what)
		return false
	}
	return true
}

func (h *host) On(event string, fn sdk.EventHandler) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.registering("事件") {
		return
	}
	if !slices.Contains(hook.Events(), event) {
		h.fail("未知事件 %q", event)
		return
	}
	if fn == nil {
		h.fail("事件 %s 的处理函数不能为空", event)
		return
	}
	h.e.events[event] = append(h.e.events[event], fn)
}

func (h *host) AddFilter(name string, priority int, fn sdk.FilterFunc) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.registering("过滤器") {
		return
	}
	if !slices.Contains(hook.Filters(), name) {
		h.fail("未知过滤器 %q", name)
		return
	}
	if fn == nil {
		h.fail("过滤器 %s 的函数不能为空", name)
		return
	}
	h.e.filters[name] = append(h.e.filters[name], filterReg{code: h.e.code, priority: priority, fn: fn})
}

// Setup 之外注册的路由写入一个不会被使用的引擎，避免与正在处理请求的路由表并发修改。

func (h *host) GuestRoutes() gin.IRouter {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.registering("路由") {
		return gin.New()
	}
	return h.public()
}

func (h *host) UserRoutes() gin.IRouter {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.registering("路由") {
		return gin.New()
	}
	if h.e.user == nil {
		h.e.user = h.public().Group("/", h.m.Auth.RequireUser())
	}
	return h.e.user
}

func (h *host) AdminRoutes() gin.IRouter {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.registering("路由") {
		return gin.New()
	}
	if h.e.admin == nil {
		h.e.admin = h.m.newEngine(h.e.code)
	}
	return h.e.admin
}

func (h *host) public() *gin.Engine {
	if h.e.pub == nil {
		h.e.pub = h.m.newEngine(h.e.code)
	}
	return h.e.pub
}

func (h *host) Job(name, spec string, run func(ctx context.Context) error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.registering("定时任务") {
		return
	}
	if !jobNamePattern.MatchString(name) || run == nil {
		h.fail("定时任务 %q 的名称无效或执行函数为空", name)
		return
	}
	if slices.ContainsFunc(h.e.jobs, func(j jobReg) bool { return j.name == name }) {
		h.fail("定时任务 %q 重复", name)
		return
	}
	if _, err := cron.ParseStandard(spec); err != nil {
		h.fail("定时任务 %s 的 cron 表达式无效: %v", name, err)
		return
	}
	h.e.jobs = append(h.e.jobs, jobReg{name: name, spec: spec, run: run})
}

func (h *host) Payment(name string, g sdk.PaymentGateway) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.registering("支付网关") {
		return
	}
	code := h.e.code
	if err := payment.RegisterExt(name, g, func() bool { return h.m.Enabled(code) }); err != nil {
		h.fail("%v", err)
		return
	}
	h.e.pays = append(h.e.pays, name)
	h.m.addCleanup(func() { payment.UnregisterExt(name) })
}

func (h *host) SubscribeFormat(f *sdk.SubscribeFormat) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if !h.registering("订阅格式") {
		return
	}
	code := h.e.code
	if err := subscribe.RegisterExt(f, func() bool { return h.m.Enabled(code) }); err != nil {
		h.fail("%v", err)
		return
	}
	h.e.formats = append(h.e.formats, f)
	h.m.addCleanup(func() { subscribe.UnregisterExt(f) })
}

func (m *Manager) addCleanup(f func()) {
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cleanups = append(m.cleanups, f)
}
