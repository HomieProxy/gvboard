package plugin

import (
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"path"
	"regexp"
	"slices"
	"sort"
	"strings"
	"sync/atomic"
	"time"
	"unicode/utf8"

	"github.com/dop251/goja"
	"github.com/gin-gonic/gin"
	"github.com/robfig/cron/v3"
	"go.uber.org/zap"

	"gvboard/internal/hook"
	sdk "gvboard/pkg/plugin"
)

// JS 插件执行限制。
const (
	jsMaxVMs        = 4 // 每个插件最多同时执行的运行时数量
	jsSetupTimeout  = 5 * time.Second
	jsEventTimeout  = 30 * time.Second
	jsRouteTimeout  = 30 * time.Second
	jsJobTimeout    = 50 * time.Second
	jsHookTimeout   = 10 * time.Second
	jsMaxStack      = 256
	jsMaxRoutes     = 50
	jsMaxHandlers   = 50
	jsMaxJobs       = 10
	jsMaxLogMessage = 2000
)

var (
	routePathPattern = regexp.MustCompile(`^/[A-Za-z0-9/_:*.-]{0,127}$`)
	errJSInterrupted = errors.New("执行超时或已取消")
	errJSAsync       = errors.New("不支持等待未完成的异步操作（插件接口均为同步调用）")
)

type jsFilterMeta struct {
	name     string
	priority int
}

type jsRouteMeta struct{ scope, method, path string }

type jsJobMeta struct{ name, spec string }

// jsPlugin 一个 JS 插件：实现 sdk.Plugin，复用 Go 插件的事件、过滤器、路由、定时任务机制。
type jsPlugin struct {
	m    *Manager
	pkg  *jsPackage
	http *jsHTTP
	data *jsData
	host sdk.Host

	pool    chan *jsVM
	created atomic.Int32
	closed  atomic.Bool
	shape   string

	hasEnable, hasDisable bool
}

var (
	_ sdk.Plugin   = (*jsPlugin)(nil)
	_ sdk.Enabler  = (*jsPlugin)(nil)
	_ sdk.Disabler = (*jsPlugin)(nil)
)

func (m *Manager) newJSPlugin(pkg *jsPackage) *jsPlugin {
	return &jsPlugin{
		m: m, pkg: pkg, pool: make(chan *jsVM, jsMaxVMs),
		http: newJSHTTP(pkg.domains, m.jsAllowPrivate),
		data: &jsData{m: m, code: pkg.manifest.Code, man: &pkg.manifest},
	}
}

func (p *jsPlugin) Info() sdk.Info      { return p.pkg.manifest.info() }
func (p *jsPlugin) Fields() []sdk.Field { return p.pkg.manifest.Config }

// Setup 在第一个运行时中执行入口脚本，按脚本中的注册调用宿主的注册接口。
func (p *jsPlugin) Setup(h sdk.Host) error {
	p.host = h
	p.created.Store(1)
	vm, err := p.newVM()
	if err != nil {
		return err
	}
	p.hasEnable, p.hasDisable = vm.onEnable != nil, vm.onDisable != nil
	names := make([]string, 0, len(vm.events))
	for name := range vm.events {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		for i := range vm.events[name] {
			h.On(name, p.onEvent(name, i))
		}
	}
	for i, f := range vm.filterMeta {
		h.AddFilter(f.name, f.priority, p.onFilter(i, f.name))
	}
	for i, r := range vm.routeMeta {
		var g gin.IRouter
		switch r.scope {
		case "guest":
			g = h.GuestRoutes()
		case "user":
			g = h.UserRoutes()
		default:
			g = h.AdminRoutes()
		}
		g.Handle(r.method, r.path, p.onRoute(i))
	}
	for i, j := range vm.jobMeta {
		h.Job(j.name, j.spec, p.onJob(i))
	}
	for _, w := range vm.widgets {
		h.Widget(w)
	}
	for _, pg := range vm.pages {
		h.Page(pg)
	}
	p.pool <- vm
	return nil
}

func (p *jsPlugin) Enable(ctx context.Context, _ sdk.Host) error {
	if !p.hasEnable {
		return nil
	}
	return p.call(ctx, jsHookTimeout, func(vm *jsVM) error {
		_, err := vm.settle(vm.onEnable(goja.Undefined()))
		return err
	})
}

func (p *jsPlugin) Disable(ctx context.Context, _ sdk.Host) error {
	if !p.hasDisable {
		return nil
	}
	return p.call(ctx, jsHookTimeout, func(vm *jsVM) error {
		_, err := vm.settle(vm.onDisable(goja.Undefined()))
		return err
	})
}

// close 卸载：之后的调用直接失败，空闲运行时被丢弃。
func (p *jsPlugin) close() {
	p.closed.Store(true)
	for {
		select {
		case <-p.pool:
		default:
			return
		}
	}
}

func (p *jsPlugin) acquire(ctx context.Context) (*jsVM, error) {
	if p.closed.Load() {
		return nil, errors.New("插件已卸载")
	}
	select {
	case vm := <-p.pool:
		return vm, nil
	default:
	}
	if p.created.Add(1) <= jsMaxVMs {
		vm, err := p.newVM()
		if err != nil {
			p.created.Add(-1)
			return nil, err
		}
		return vm, nil
	}
	p.created.Add(-1)
	select {
	case vm := <-p.pool:
		return vm, nil
	case <-ctx.Done():
		return nil, errors.New("插件繁忙")
	}
}

func (p *jsPlugin) release(vm *jsVM, broken bool) {
	if broken || p.closed.Load() {
		p.created.Add(-1)
		return
	}
	p.pool <- vm
}

// call 取一个空闲运行时执行 fn，超时或 ctx 取消时中断脚本。
func (p *jsPlugin) call(ctx context.Context, timeout time.Duration, fn func(vm *jsVM) error) (err error) {
	vm, err := p.acquire(ctx)
	if err != nil {
		return err
	}
	broken := false
	defer func() { p.release(vm, broken) }()
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	vm.ctx = ctx
	stop := context.AfterFunc(ctx, func() { vm.rt.Interrupt(errJSInterrupted) })
	defer func() {
		if !stop() {
			// 中断可能在 ClearInterrupt 之后才生效，不再复用该运行时
			broken = true
		}
		vm.rt.ClearInterrupt()
		vm.ctx = nil
	}()
	defer func() {
		if r := recover(); r != nil {
			broken = true
			err = fmt.Errorf("插件运行时异常：%v", r)
		}
	}()
	return fn(vm)
}

// jsVM 一个 goja 运行时（非并发安全，同一时刻只在一个调用中使用）。
type jsVM struct {
	p     *jsPlugin
	rt    *goja.Runtime
	ctx   context.Context
	setup bool

	events     map[string][]goja.Callable
	filters    []goja.Callable
	filterMeta []jsFilterMeta
	routes     []goja.Callable
	routeMeta  []jsRouteMeta
	jobs       []goja.Callable
	jobMeta    []jsJobMeta
	widgets    []sdk.Widget
	pages      []sdk.Page
	onEnable   goja.Callable
	onDisable  goja.Callable

	modules   map[string]goja.Value
	parseJSON goja.Callable
	stringify goja.Callable
}

func (vm *jsVM) shape() string {
	var b strings.Builder
	names := make([]string, 0, len(vm.events))
	for n := range vm.events {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		fmt.Fprintf(&b, "e:%s:%d;", n, len(vm.events[n]))
	}
	fmt.Fprintf(&b, "%v|%v|%v|%v|%v|%v|%v", vm.filterMeta, vm.routeMeta, vm.jobMeta, vm.widgets, vm.pages, vm.onEnable != nil, vm.onDisable != nil)
	return b.String()
}

// newVM 创建运行时并执行入口脚本。之后创建的运行时注册内容必须与第一个相同。
func (p *jsPlugin) newVM() (*jsVM, error) {
	rt := goja.New()
	rt.SetFieldNameMapper(goja.TagFieldNameMapper("json", true))
	rt.SetMaxCallStackSize(jsMaxStack)
	vm := &jsVM{p: p, rt: rt, events: map[string][]goja.Callable{}, modules: map[string]goja.Value{}}
	j := rt.Get("JSON").ToObject(rt)
	vm.parseJSON, _ = goja.AssertFunction(j.Get("parse"))
	vm.stringify, _ = goja.AssertFunction(j.Get("stringify"))
	if err := vm.install(); err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(context.Background(), jsSetupTimeout)
	defer cancel()
	vm.ctx, vm.setup = ctx, true
	stop := context.AfterFunc(ctx, func() { rt.Interrupt(errJSInterrupted) })
	_, err := func() (v goja.Value, err error) {
		defer func() {
			if r := recover(); r != nil {
				err = fmt.Errorf("插件运行时异常：%v", r)
			}
		}()
		return vm.load(p.pkg.manifest.Main)
	}()
	if !stop() {
		err = errJSInterrupted
	}
	rt.ClearInterrupt()
	vm.ctx, vm.setup = nil, false
	if err != nil {
		return nil, fmt.Errorf("执行入口脚本失败：%w", vm.jsErr(err))
	}
	s := vm.shape()
	if p.shape == "" {
		p.shape = s
	} else if s != p.shape {
		return nil, errors.New("插件每次加载注册的内容不一致（注册不能依赖随机数、时间或外部数据）")
	}
	return vm, nil
}

// jsErr 转换脚本错误为简短的错误信息。
func (vm *jsVM) jsErr(err error) error {
	var ie *goja.InterruptedError
	if errors.As(err, &ie) || errors.Is(err, errJSInterrupted) {
		return errJSInterrupted
	}
	var ex *goja.Exception
	if errors.As(err, &ex) {
		msg := ex.Value().String()
		if obj, ok := ex.Value().(*goja.Object); ok {
			if m := obj.Get("message"); m != nil && !goja.IsUndefined(m) {
				msg = m.String()
			}
		}
		return errors.New(truncate(msg, 500))
	}
	return err
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	return string([]rune(s)[:n]) + "…"
}

// settle 处理函数调用结果：已完成的 Promise 取其结果。
func (vm *jsVM) settle(v goja.Value, err error) (goja.Value, error) {
	if err != nil {
		return nil, vm.jsErr(err)
	}
	if v == nil {
		return goja.Undefined(), nil
	}
	if pr, ok := v.Export().(*goja.Promise); ok {
		switch pr.State() {
		case goja.PromiseStateFulfilled:
			return pr.Result(), nil
		case goja.PromiseStateRejected:
			r := pr.Result()
			if obj, ok := r.(*goja.Object); ok {
				if m := obj.Get("message"); m != nil && !goja.IsUndefined(m) {
					return nil, errors.New(truncate(m.String(), 500))
				}
			}
			return nil, errors.New(truncate(r.String(), 500))
		default:
			return nil, errJSAsync
		}
	}
	return v, nil
}

func (vm *jsVM) throw(err error) {
	panic(vm.rt.NewGoError(err))
}

func (vm *jsVM) throwf(format string, a ...any) {
	panic(vm.rt.NewTypeError(fmt.Sprintf(format, a...)))
}

// toJS 把 Go 值（经 JSON）转换为普通 JS 对象。
func (vm *jsVM) toJS(v any) goja.Value {
	b, err := json.Marshal(v)
	if err != nil {
		vm.throw(err)
	}
	return vm.parse(b)
}

func (vm *jsVM) parse(b []byte) goja.Value {
	if len(b) == 0 {
		return goja.Null()
	}
	v, err := vm.parseJSON(goja.Undefined(), vm.rt.ToValue(string(b)))
	if err != nil {
		vm.throw(err)
	}
	return v
}

// toJSON 把 JS 值转换为 JSON，undefined 返回 nil。
func (vm *jsVM) toJSON(v goja.Value) ([]byte, error) {
	if v == nil || goja.IsUndefined(v) {
		return nil, nil
	}
	s, err := vm.stringify(goja.Undefined(), v)
	if err != nil {
		return nil, vm.jsErr(err)
	}
	if goja.IsUndefined(s) {
		return nil, nil
	}
	return []byte(s.String()), nil
}

func (vm *jsVM) context() context.Context {
	if vm.ctx != nil {
		return vm.ctx
	}
	return context.Background()
}

// load 加载包内模块（CommonJS），结果按路径缓存。
func (vm *jsVM) load(name string) (goja.Value, error) {
	if v, ok := vm.modules[name]; ok {
		return v, nil
	}
	if strings.HasSuffix(name, ".json") {
		data, ok := vm.p.pkg.files[name]
		if !ok {
			return nil, fmt.Errorf("模块 %s 不存在", name)
		}
		v, err := vm.parseJSON(goja.Undefined(), vm.rt.ToValue(string(data)))
		if err != nil {
			return nil, fmt.Errorf("%s 格式错误：%w", name, vm.jsErr(err))
		}
		vm.modules[name] = v
		return v, nil
	}
	prog, ok := vm.p.pkg.programs[name]
	if !ok {
		return nil, fmt.Errorf("模块 %s 不存在", name)
	}
	module := vm.rt.NewObject()
	exports := vm.rt.NewObject()
	_ = module.Set("exports", exports)
	vm.modules[name] = exports
	wrapper, err := vm.rt.RunProgram(prog)
	if err != nil {
		delete(vm.modules, name)
		return nil, err
	}
	fn, _ := goja.AssertFunction(wrapper)
	if _, err := fn(goja.Undefined(), exports, vm.rt.ToValue(vm.requireFrom(path.Dir(name))), module, vm.rt.ToValue(name)); err != nil {
		delete(vm.modules, name)
		return nil, err
	}
	v := module.Get("exports")
	vm.modules[name] = v
	return v, nil
}

func (vm *jsVM) requireFrom(dir string) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		spec := call.Argument(0).String()
		name := path.Clean(path.Join(dir, spec))
		if name == ".." || strings.HasPrefix(name, "../") || strings.HasPrefix(spec, "/") {
			vm.throwf("模块路径 %q 无效", spec)
		}
		if _, ok := vm.p.pkg.files[name]; !ok {
			if _, ok := vm.p.pkg.files[name+".js"]; ok {
				name += ".js"
			}
		}
		v, err := vm.load(name)
		if err != nil {
			vm.throw(vm.jsErr(err))
		}
		return v
	}
}

func (vm *jsVM) fn(v goja.Value, what string) goja.Callable {
	f, ok := goja.AssertFunction(v)
	if !ok {
		vm.throwf("%s 必须是函数", what)
	}
	return f
}

func (vm *jsVM) mustSetup(what string) {
	if !vm.setup {
		vm.throwf("只能在插件加载时注册%s", what)
	}
}

// jsResponse plugin.response() 返回的原始响应。
type jsResponse struct {
	Status      int               `json:"status"`
	Headers     map[string]string `json:"headers"`
	Body        string            `json:"body"`
	ContentType string            `json:"content_type"`
}

// jsFail plugin.fail() 返回的错误响应。
type jsFail struct {
	Status  int    `json:"status"`
	Message string `json:"message"`
}

func (vm *jsVM) set(obj *goja.Object, name string, fn func(goja.FunctionCall) goja.Value) {
	_ = obj.Set(name, fn)
}

func (vm *jsVM) global(name string, fns map[string]func(goja.FunctionCall) goja.Value) error {
	obj := vm.rt.NewObject()
	for k, f := range fns {
		vm.set(obj, k, f)
	}
	return vm.rt.Set(name, obj)
}

// install 注入全局对象：plugin、kv、http、log、console、crypto、users、subscriptions、orders。
func (vm *jsVM) install() error {
	p := vm.p
	man := &p.pkg.manifest
	rt := vm.rt
	pl := rt.NewObject()
	_ = pl.Set("code", man.Code)
	_ = pl.Set("info", vm.toJS(man.info()))
	vm.set(pl, "config", func(goja.FunctionCall) goja.Value { return vm.toJS(p.host.Config()) })
	vm.set(pl, "on", func(call goja.FunctionCall) goja.Value {
		vm.mustSetup("事件")
		name := call.Argument(0).String()
		if !slices.Contains(hook.Events(), name) {
			vm.throwf("未知事件 %q", name)
		}
		if n := len(vm.events[name]); n >= jsMaxHandlers {
			vm.throwf("同一事件最多注册 %d 个处理函数", jsMaxHandlers)
		}
		vm.events[name] = append(vm.events[name], vm.fn(call.Argument(1), "事件处理函数"))
		return goja.Undefined()
	})
	vm.set(pl, "filter", func(call goja.FunctionCall) goja.Value {
		vm.mustSetup("过滤器")
		name := call.Argument(0).String()
		if !slices.Contains(hook.Filters(), name) {
			vm.throwf("未知过滤器 %q", name)
		}
		priority, fnArg := 0, call.Argument(1)
		if len(call.Arguments) > 2 {
			priority, fnArg = int(call.Argument(1).ToInteger()), call.Argument(2)
		}
		if len(vm.filters) >= jsMaxHandlers {
			vm.throwf("最多注册 %d 个过滤器", jsMaxHandlers)
		}
		vm.filters = append(vm.filters, vm.fn(fnArg, "过滤器函数"))
		vm.filterMeta = append(vm.filterMeta, jsFilterMeta{name: name, priority: priority})
		return goja.Undefined()
	})
	vm.set(pl, "route", func(call goja.FunctionCall) goja.Value {
		vm.mustSetup("路由")
		scope, method, rp := call.Argument(0).String(), strings.ToUpper(call.Argument(1).String()), call.Argument(2).String()
		if scope != "guest" && scope != "user" && scope != "admin" {
			vm.throwf("路由类型 %q 无效（guest / user / admin）", scope)
		}
		switch method {
		case http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete:
		default:
			vm.throwf("不支持的请求方法 %q", method)
		}
		if !routePathPattern.MatchString(rp) {
			vm.throwf("路由路径 %q 无效", rp)
		}
		if len(vm.routes) >= jsMaxRoutes {
			vm.throwf("最多注册 %d 个路由", jsMaxRoutes)
		}
		vm.routes = append(vm.routes, vm.fn(call.Argument(3), "路由处理函数"))
		vm.routeMeta = append(vm.routeMeta, jsRouteMeta{scope: scope, method: method, path: rp})
		return goja.Undefined()
	})
	vm.set(pl, "job", func(call goja.FunctionCall) goja.Value {
		vm.mustSetup("定时任务")
		name, spec := call.Argument(0).String(), call.Argument(1).String()
		if !jobNamePattern.MatchString(name) {
			vm.throwf("定时任务名称 %q 无效", name)
		}
		if _, err := cron.ParseStandard(spec); err != nil {
			vm.throwf("定时任务 %s 的 cron 表达式无效", name)
		}
		if len(vm.jobs) >= jsMaxJobs || slices.ContainsFunc(vm.jobMeta, func(j jsJobMeta) bool { return j.name == name }) {
			vm.throwf("定时任务 %q 重复或超过 %d 个", name, jsMaxJobs)
		}
		vm.jobs = append(vm.jobs, vm.fn(call.Argument(2), "定时任务函数"))
		vm.jobMeta = append(vm.jobMeta, jsJobMeta{name: name, spec: spec})
		return goja.Undefined()
	})
	vm.set(pl, "widget", func(call goja.FunctionCall) goja.Value {
		vm.mustSetup("卡片")
		var w sdk.Widget
		b, err := vm.toJSON(call.Argument(0))
		if err == nil {
			err = json.Unmarshal(b, &w)
		}
		if err != nil || b == nil {
			vm.throwf("卡片参数必须是对象")
		}
		if len(vm.widgets) >= maxWidgets {
			vm.throwf("最多注册 %d 张卡片", maxWidgets)
		}
		vm.widgets = append(vm.widgets, w)
		return goja.Undefined()
	})
	vm.set(pl, "page", func(call goja.FunctionCall) goja.Value {
		vm.mustSetup("页面")
		var pg sdk.Page
		b, err := vm.toJSON(call.Argument(0))
		if err == nil {
			err = json.Unmarshal(b, &pg)
		}
		if err != nil || b == nil {
			vm.throwf("页面参数必须是对象")
		}
		if len(vm.pages) >= maxPages {
			vm.throwf("最多注册 %d 个页面", maxPages)
		}
		vm.pages = append(vm.pages, pg)
		return goja.Undefined()
	})
	vm.set(pl, "onEnable", func(call goja.FunctionCall) goja.Value {
		vm.mustSetup("启用回调")
		vm.onEnable = vm.fn(call.Argument(0), "启用回调")
		return goja.Undefined()
	})
	vm.set(pl, "onDisable", func(call goja.FunctionCall) goja.Value {
		vm.mustSetup("停用回调")
		vm.onDisable = vm.fn(call.Argument(0), "停用回调")
		return goja.Undefined()
	})
	vm.set(pl, "response", func(call goja.FunctionCall) goja.Value {
		r := &jsResponse{Status: http.StatusOK}
		if b, err := vm.toJSON(call.Argument(0)); err != nil {
			vm.throw(err)
		} else if b != nil {
			if err := json.Unmarshal(b, r); err != nil {
				vm.throwf("响应参数无效")
			}
		}
		return rt.ToValue(r)
	})
	vm.set(pl, "fail", func(call goja.FunctionCall) goja.Value {
		return rt.ToValue(&jsFail{Status: int(call.Argument(0).ToInteger()), Message: call.Argument(1).String()})
	})
	if err := rt.Set("plugin", pl); err != nil {
		return err
	}

	kv := &kv{m: p.m, code: man.Code}
	if err := vm.global("kv", map[string]func(goja.FunctionCall) goja.Value{
		"get": func(call goja.FunctionCall) goja.Value {
			v, err := kv.Get(vm.context(), call.Argument(0).String())
			if errors.Is(err, sdk.ErrKVNotFound) {
				return goja.Null()
			}
			if err != nil {
				vm.throw(err)
			}
			return rt.ToValue(v)
		},
		"set": func(call goja.FunctionCall) goja.Value {
			if err := kv.Set(vm.context(), call.Argument(0).String(), call.Argument(1).String(), ttlArg(call.Argument(2))); err != nil {
				vm.throw(err)
			}
			return goja.Undefined()
		},
		"setnx": func(call goja.FunctionCall) goja.Value {
			ok, err := kv.SetNX(vm.context(), call.Argument(0).String(), call.Argument(1).String(), ttlArg(call.Argument(2)))
			if err != nil {
				vm.throw(err)
			}
			return rt.ToValue(ok)
		},
		"del": func(call goja.FunctionCall) goja.Value {
			if err := kv.Delete(vm.context(), call.Argument(0).String()); err != nil {
				vm.throw(err)
			}
			return goja.Undefined()
		},
	}); err != nil {
		return err
	}

	request := func(r httpRequest) goja.Value {
		resp, err := p.http.do(vm.context(), r)
		if err != nil {
			vm.throw(err)
		}
		obj := vm.toJS(resp)
		o := obj.ToObject(rt)
		_ = o.Set("json", func(goja.FunctionCall) goja.Value { return vm.parse([]byte(resp.Body)) })
		return o
	}
	if err := vm.global("http", map[string]func(goja.FunctionCall) goja.Value{
		"request": func(call goja.FunctionCall) goja.Value { return request(vm.httpOpts(call.Argument(0), "", "", nil)) },
		"get": func(call goja.FunctionCall) goja.Value {
			return request(vm.httpOpts(call.Argument(1), http.MethodGet, call.Argument(0).String(), nil))
		},
		"post": func(call goja.FunctionCall) goja.Value {
			return request(vm.httpOpts(call.Argument(2), http.MethodPost, call.Argument(0).String(), call.Argument(1)))
		},
	}); err != nil {
		return err
	}

	logger := p.m.log().Named("plugin." + man.Code)
	logFn := func(level string) func(goja.FunctionCall) goja.Value {
		return func(call goja.FunctionCall) goja.Value {
			parts := make([]string, 0, len(call.Arguments))
			for _, a := range call.Arguments {
				parts = append(parts, a.String())
			}
			msg := truncate(strings.Join(parts, " "), jsMaxLogMessage)
			switch level {
			case "warn":
				logger.Warn(msg)
			case "error":
				logger.Error(msg)
			default:
				logger.Info(msg)
			}
			return goja.Undefined()
		}
	}
	for _, name := range []string{"log", "console"} {
		if err := vm.global(name, map[string]func(goja.FunctionCall) goja.Value{
			"log": logFn("info"), "info": logFn("info"), "warn": logFn("warn"), "error": logFn("error"),
		}); err != nil {
			return err
		}
	}

	if err := vm.global("crypto", map[string]func(goja.FunctionCall) goja.Value{
		"sha256": func(call goja.FunctionCall) goja.Value {
			s := sha256.Sum256([]byte(call.Argument(0).String()))
			return rt.ToValue(hex.EncodeToString(s[:]))
		},
		"hmacSHA256": func(call goja.FunctionCall) goja.Value {
			h := hmac.New(sha256.New, []byte(call.Argument(0).String()))
			h.Write([]byte(call.Argument(1).String()))
			return rt.ToValue(hex.EncodeToString(h.Sum(nil)))
		},
		"randomHex": func(call goja.FunctionCall) goja.Value {
			n := int(call.Argument(0).ToInteger())
			if n <= 0 || n > 256 {
				vm.throwf("长度必须为 1～256")
			}
			b := make([]byte, n)
			_, _ = rand.Read(b)
			return rt.ToValue(hex.EncodeToString(b))
		},
		"base64Encode": func(call goja.FunctionCall) goja.Value {
			return rt.ToValue(base64.StdEncoding.EncodeToString([]byte(call.Argument(0).String())))
		},
		"base64Decode": func(call goja.FunctionCall) goja.Value {
			b, err := base64.StdEncoding.DecodeString(call.Argument(0).String())
			if err != nil {
				vm.throwf("base64 格式错误")
			}
			return rt.ToValue(string(b))
		},
	}); err != nil {
		return err
	}

	d := p.data
	result := func(v any, err error) goja.Value {
		if err != nil {
			vm.throw(err)
		}
		return vm.toJS(v)
	}
	if err := vm.global("users", map[string]func(goja.FunctionCall) goja.Value{
		"get": func(call goja.FunctionCall) goja.Value {
			return result(d.UserByID(vm.context(), call.Argument(0).ToInteger()))
		},
		"findByEmail": func(call goja.FunctionCall) goja.Value {
			return result(d.UserByEmail(vm.context(), call.Argument(0).String()))
		},
		"addBalance": func(call goja.FunctionCall) goja.Value {
			remark := ""
			if a := call.Argument(2); !goja.IsUndefined(a) && !goja.IsNull(a) {
				remark = a.String()
			}
			return result(d.AddBalance(vm.context(), call.Argument(0).ToInteger(), vm.integer(call.Argument(1)), remark))
		},
	}); err != nil {
		return err
	}
	if err := vm.global("subscriptions", map[string]func(goja.FunctionCall) goja.Value{
		"get": func(call goja.FunctionCall) goja.Value {
			return result(d.Subscription(vm.context(), call.Argument(0).ToInteger()))
		},
		"list": func(call goja.FunctionCall) goja.Value {
			return result(d.Subscriptions(vm.context(), call.Argument(0).ToInteger()))
		},
		"addTraffic": func(call goja.FunctionCall) goja.Value {
			return result(nil, d.AddTraffic(vm.context(), call.Argument(0).ToInteger(), vm.integer(call.Argument(1))))
		},
		"extend": func(call goja.FunctionCall) goja.Value {
			return result(d.Extend(vm.context(), call.Argument(0).ToInteger(), vm.integer(call.Argument(1))))
		},
	}); err != nil {
		return err
	}
	return vm.global("orders", map[string]func(goja.FunctionCall) goja.Value{
		"get": func(call goja.FunctionCall) goja.Value {
			return result(d.Order(vm.context(), call.Argument(0).String()))
		},
		"list": func(call goja.FunctionCall) goja.Value {
			return result(d.Orders(vm.context(), call.Argument(0).ToInteger(), int(call.Argument(1).ToInteger())))
		},
	})
}

// integer 金额、流量等必须为整数。
func (vm *jsVM) integer(v goja.Value) int64 {
	f := v.ToFloat()
	n := v.ToInteger()
	if float64(n) != f {
		vm.throwf("参数必须为整数")
	}
	return n
}

// ttlArg 过期秒数，缺省或 0 表示永不过期。
func ttlArg(v goja.Value) time.Duration {
	if v == nil || goja.IsUndefined(v) || goja.IsNull(v) {
		return 0
	}
	n := v.ToInteger()
	if n <= 0 {
		return 0
	}
	return time.Duration(n) * time.Second
}

// httpOpts 解析 http.request 的参数：{method, url, headers, body, json, timeout（毫秒）}。
func (vm *jsVM) httpOpts(opts goja.Value, method, url string, body goja.Value) httpRequest {
	r := httpRequest{Method: method, URL: url, Headers: map[string]string{}}
	var o struct {
		Method  string            `json:"method"`
		URL     string            `json:"url"`
		Headers map[string]string `json:"headers"`
		Body    *string           `json:"body"`
		JSON    json.RawMessage   `json:"json"`
		Timeout int64             `json:"timeout"`
	}
	if b, err := vm.toJSON(opts); err != nil {
		vm.throw(err)
	} else if b != nil && string(b) != "null" {
		if err := json.Unmarshal(b, &o); err != nil {
			vm.throwf("请求参数无效")
		}
	}
	if o.Method != "" {
		r.Method = o.Method
	}
	if o.URL != "" {
		r.URL = o.URL
	}
	for k, v := range o.Headers {
		r.Headers[k] = v
	}
	if o.Timeout > 0 {
		r.Timeout = time.Duration(o.Timeout) * time.Millisecond
	}
	switch {
	case o.Body != nil:
		r.Body = *o.Body
	case len(o.JSON) > 0:
		r.Body = string(o.JSON)
		r.Headers["Content-Type"] = "application/json"
	}
	if body != nil && !goja.IsUndefined(body) && !goja.IsNull(body) {
		if s, ok := body.Export().(string); ok {
			r.Body = s
		} else {
			b, err := vm.toJSON(body)
			if err != nil {
				vm.throw(err)
			}
			r.Body = string(b)
			if _, ok := r.Headers["Content-Type"]; !ok {
				r.Headers["Content-Type"] = "application/json"
			}
		}
	}
	return r
}

func (p *jsPlugin) onEvent(name string, i int) sdk.EventHandler {
	return func(ctx context.Context, ev sdk.Event) error {
		return p.call(ctx, jsEventTimeout, func(vm *jsVM) error {
			fns := vm.events[name]
			if i >= len(fns) {
				return errors.New("事件处理函数不存在")
			}
			obj := vm.rt.NewObject()
			_ = obj.Set("name", ev.Name)
			_ = obj.Set("time", ev.Time)
			_ = obj.Set("data", vm.parse(ev.Data))
			_, err := vm.settle(fns[i](goja.Undefined(), obj))
			return err
		})
	}
}

func (p *jsPlugin) onJob(i int) func(ctx context.Context) error {
	return func(ctx context.Context) error {
		return p.call(ctx, jsJobTimeout, func(vm *jsVM) error {
			_, err := vm.settle(vm.jobs[i](goja.Undefined()))
			return err
		})
	}
}

func (p *jsPlugin) onFilter(i int, name string) sdk.FilterFunc {
	return func(ctx context.Context, v any) (any, error) {
		in, err := filterEncode(name, v)
		if err != nil {
			return nil, err
		}
		out := v
		err = p.call(ctx, p.m.filterTimeout(), func(vm *jsVM) error {
			res, err := vm.settle(vm.filters[i](goja.Undefined(), vm.parse(in)))
			if err != nil {
				return err
			}
			if goja.IsUndefined(res) || goja.IsNull(res) {
				return nil
			}
			b, err := vm.toJSON(res)
			if err != nil {
				return err
			}
			out, err = filterDecode(name, v, b)
			return err
		})
		return out, err
	}
}

// jsContent subscribe.content 在 JS 中的形式（Body 为字符串）。
type jsContent struct {
	UserID         int64       `json:"user_id"`
	SubscriptionID int64       `json:"subscription_id"`
	Client         string      `json:"client"`
	ContentType    string      `json:"content_type"`
	Headers        [][2]string `json:"headers"`
	Body           string      `json:"body"`
}

func filterEncode(name string, v any) ([]byte, error) {
	switch c := v.(type) {
	case hook.SubscribeContent:
		return json.Marshal(jsContent{c.UserID, c.SubscriptionID, c.Client, c.ContentType, c.Headers, string(c.Body)})
	case hook.OrderAmount:
		if c.Items == nil {
			c.Items = []hook.AmountItem{}
		}
		return json.Marshal(c)
	case hook.PlanBadges:
		c.Plans = slices.Clone(c.Plans)
		for i := range c.Plans {
			if c.Plans[i].Badges == nil {
				c.Plans[i].Badges = []string{}
			}
		}
		return json.Marshal(c)
	}
	return json.Marshal(v)
}

// filterDecode 按原值类型解析 JS 返回的结果，未返回的字段保持原值。
func filterDecode(name string, old any, b []byte) (any, error) {
	if len(b) == 0 {
		return old, nil
	}
	switch o := old.(type) {
	case hook.OrderAmount:
		var n struct {
			Amount int64             `json:"amount"`
			Items  []hook.AmountItem `json:"items"`
		}
		// 复制一份：Unmarshal 会复用切片的底层数组，不能改动原值
		n.Amount, n.Items = o.Amount, slices.Clone(o.Items)
		if err := json.Unmarshal(b, &n); err != nil {
			return nil, err
		}
		o.Amount, o.Items = n.Amount, n.Items
		return o, nil
	case hook.PlanBadges:
		var n hook.PlanBadges
		if err := json.Unmarshal(b, &n); err != nil {
			return nil, err
		}
		return n, nil
	case hook.MailMessage:
		err := json.Unmarshal(b, &o)
		return o, err
	case hook.SubscribeContent:
		c := jsContent{o.UserID, o.SubscriptionID, o.Client, o.ContentType, o.Headers, string(o.Body)}
		if err := json.Unmarshal(b, &c); err != nil {
			return nil, err
		}
		o.ContentType, o.Headers, o.Body = c.ContentType, c.Headers, []byte(c.Body)
		return o, nil
	case hook.SubscribeServers:
		return decodeServers(o, b)
	case hook.KnowledgeArticle:
		n := struct {
			Body string `json:"body"`
		}{o.Body}
		if err := json.Unmarshal(b, &n); err != nil {
			return nil, err
		}
		o.Body = n.Body
		return o, nil
	}
	return nil, fmt.Errorf("过滤器 %s 不支持 JS 插件", name)
}

func (p *jsPlugin) onRoute(i int) gin.HandlerFunc {
	return func(c *gin.Context) {
		body, err := io.ReadAll(io.LimitReader(c.Request.Body, httpMaxRequest+1))
		if err != nil || len(body) > httpMaxRequest {
			sdk.Fail(c, http.StatusRequestEntityTooLarge, "请求内容过大")
			return
		}
		req := map[string]any{
			"method": c.Request.Method, "path": c.Request.URL.Path, "ip": c.ClientIP(), "body": string(body),
			"params": map[string]string{}, "query": map[string]string{}, "headers": map[string]string{}, "user": nil,
		}
		for _, prm := range c.Params {
			req["params"].(map[string]string)[prm.Key] = prm.Value
		}
		for k, v := range c.Request.URL.Query() {
			req["query"].(map[string]string)[k] = v[0]
		}
		for k, v := range c.Request.Header {
			if lk := strings.ToLower(k); lk != "authorization" && lk != "cookie" {
				req["headers"].(map[string]string)[lk] = v[0]
			}
		}
		if u := sdk.CurrentUser(c); u != nil {
			req["user"] = map[string]any{"id": u.ID, "email": u.Email, "is_admin": u.IsAdmin, "is_staff": u.IsStaff}
		}
		var reply func()
		err = p.call(c.Request.Context(), jsRouteTimeout, func(vm *jsVM) error {
			obj := vm.toJS(req).ToObject(vm.rt)
			_ = obj.Set("json", func(goja.FunctionCall) goja.Value { return vm.parse(body) })
			res, err := vm.settle(vm.routes[i](goja.Undefined(), obj))
			if err != nil {
				return err
			}
			switch r := res.Export().(type) {
			case *jsResponse:
				reply = func() { writeRaw(c, r) }
			case *jsFail:
				reply = func() { sdk.Fail(c, r.Status, r.Message) }
			default:
				b, err := vm.toJSON(res)
				if err != nil {
					return err
				}
				reply = func() { sdk.OK(c, json.RawMessage(nullIfEmpty(b))) }
			}
			return nil
		})
		if err != nil {
			p.m.log().Warn("插件路由执行失败", zap.String("plugin", p.pkg.manifest.Code), zap.String("path", c.Request.URL.Path), zap.Error(err))
			sdk.Fail(c, http.StatusInternalServerError, "插件执行失败")
			return
		}
		reply()
	}
}

func nullIfEmpty(b []byte) []byte {
	if len(b) == 0 {
		return []byte("null")
	}
	return b
}

func writeRaw(c *gin.Context, r *jsResponse) {
	status := r.Status
	if status < 200 || status > 599 {
		status = http.StatusOK
	}
	ct := r.ContentType
	if ct == "" {
		ct = "text/plain; charset=utf-8"
	}
	for k, v := range r.Headers {
		if strings.ContainsAny(k, "\r\n:") || strings.ContainsAny(v, "\r\n") || strings.EqualFold(k, "content-length") {
			continue
		}
		c.Header(k, v)
	}
	c.Data(status, ct, []byte(r.Body))
}
