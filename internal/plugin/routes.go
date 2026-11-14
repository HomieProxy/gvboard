package plugin

import (
	"context"
	"fmt"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/scheduler"
)

type identityKey struct{}

// newEngine 插件独立的路由引擎：未匹配的路由返回统一 404，panic 返回 500，不做尾部斜杠重定向。
func (m *Manager) newEngine(code string) *gin.Engine {
	eng := gin.New()
	eng.RedirectTrailingSlash = false
	eng.RedirectFixedPath = false
	eng.ContextWithFallback = true
	eng.Use(func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				m.log().Error("插件路由 panic", zap.String("plugin", code), zap.String("path", c.Request.URL.Path), zap.Any("panic", r))
				if !c.Writer.Written() {
					httpx.Fail(c, httpx.Internal(fmt.Errorf("插件 %s panic: %v", code, r)))
				}
				c.Abort()
			}
		}()
		if id, ok := c.Request.Context().Value(identityKey{}).(*auth.Identity); ok {
			auth.SetCurrent(c, id)
		}
		c.Next()
	})
	eng.Use(m.Middleware...)
	eng.NoRoute(httpx.NotFound)
	eng.NoMethod(httpx.NotFound)
	return eng
}

// serve 把 {前缀}/plugin/{code}/* 的请求转交给插件的路由引擎，插件不存在、未启用或没有该类路由时返回统一 404。
func (m *Manager) serve(admin bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		code := c.Param("code")
		e := m.entry(code)
		if e == nil || !m.Enabled(code) {
			httpx.NotFound(c)
			return
		}
		eng := e.pub
		if admin {
			eng = e.admin
		}
		if eng == nil {
			httpx.NotFound(c)
			return
		}
		ctx := c.Request.Context()
		if id := auth.Current(c); id != nil {
			ctx = context.WithValue(ctx, identityKey{}, id)
		}
		req := c.Request.Clone(ctx)
		req.URL.Path, req.URL.RawPath = c.Param("path"), ""
		eng.ServeHTTP(c.Writer, req)
		c.Abort()
	}
}

// Jobs 定时任务：Go 插件的定时任务（任务名为 plugin.{code}.{name}，插件停用时跳过）、
// JS 插件定时任务的分发（每分钟）与插件 KV 过期清理。
func (m *Manager) Jobs() []scheduler.Job {
	jobs := []scheduler.Job{
		{Name: "plugin_kv_clean", Spec: "20 0 * * *", NoOverlap: true, Run: func(ctx context.Context) error {
			_, err := m.CleanKV(ctx)
			return err
		}},
		{Name: "plugin_js_jobs", Spec: "* * * * *", NoOverlap: true, Run: m.runJSJobs},
	}
	for _, e := range m.list() {
		if e.failed() || e.kind != model.PluginTypeGo {
			continue
		}
		for _, j := range e.jobs {
			code, run := e.code, j.run
			jobs = append(jobs, scheduler.Job{
				Name: "plugin." + code + "." + j.name, Spec: j.spec, NoOverlap: true,
				Run: func(ctx context.Context) (err error) {
					if !m.Enabled(code) {
						return nil
					}
					defer func() {
						if r := recover(); r != nil {
							err = fmt.Errorf("插件 %s 定时任务 panic: %v", code, r)
						}
					}()
					return run(ctx)
				},
			})
		}
	}
	return jobs
}

// routeInfo 插件路由（管理端展示）。
type routeInfo struct {
	Method string `json:"method"`
	Path   string `json:"path"`
	Scope  string `json:"scope"` // public（访客或用户）/ admin
}

func (e *entry) routes() []routeInfo {
	out := []routeInfo{}
	add := func(eng *gin.Engine, scope string) {
		if eng == nil {
			return
		}
		for _, r := range eng.Routes() {
			out = append(out, routeInfo{Method: r.Method, Path: r.Path, Scope: scope})
		}
	}
	add(e.pub, "public")
	add(e.admin, "admin")
	return out
}
