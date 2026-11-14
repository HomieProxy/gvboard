// Package server 构建 HTTP 路由与服务。
package server

import (
	"os"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"gvboard/internal/config"
	"gvboard/internal/httpx"
	"gvboard/internal/middleware"
)

// LegacyPrefix 原 v2board 接口前缀。
const LegacyPrefix = "/api/v1"

// Router 各业务模块注册路由时使用的分组。
type Router struct {
	Engine *gin.Engine
	Config *config.Config

	// API 原生 API（自定义前缀），Gvboard 前端只使用这一组。
	API *gin.RouterGroup
	// NodeV1 / NodeV2 节点通信接口，路径被节点后端写死，始终挂载。
	NodeV1 *gin.RouterGroup // /api/v1/server
	NodeV2 *gin.RouterGroup // /api/v2/server
	// Legacy 原 /api/v1 用户、访客、管理接口兼容层；compat.v2board_api 关闭时为 nil。
	Legacy *gin.RouterGroup
	// LegacyRoot 旧订阅、旧回调等单独开关的原路径挂载点（/api/v1），始终非 nil，
	// 由各模块根据 compat.legacy_subscribe / compat.legacy_callback 自行决定是否注册。
	LegacyRoot *gin.RouterGroup

	fallbacks []Fallback
}

// Fallback 没有匹配的静态路由时依次尝试的动态路由（如运行时可修改的订阅路径），已处理请求时返回 true。
type Fallback func(c *gin.Context) bool

// AddFallback 注册动态路由，须在服务启动前调用。
func (r *Router) AddFallback(f Fallback) { r.fallbacks = append(r.fallbacks, f) }

func (r *Router) noRoute(c *gin.Context) {
	for _, f := range r.fallbacks {
		if f(c) {
			return
		}
	}
	httpx.NotFound(c)
}

// Module 业务模块需实现的路由注册接口。
type Module interface {
	Register(r *Router)
}

// NewRouter 创建路由并挂载全局中间件，extra 追加在内置中间件之后（必须在创建分组前挂载）。
func NewRouter(cfg *config.Config, log *zap.Logger, extra ...gin.HandlerFunc) (*Router, error) {
	if cfg.App.Debug {
		gin.SetMode(gin.DebugMode)
	} else {
		gin.SetMode(gin.ReleaseMode)
	}

	if cfg.Server.NotFoundPage != "" {
		b, err := os.ReadFile(cfg.Server.NotFoundPage)
		if err != nil {
			return nil, err
		}
		httpx.SetNotFoundPage(b)
	}

	e := gin.New()
	// 关闭自动重定向与 405，任何未匹配的请求都只会得到统一 404，避免通过重定向/405 探测路由。
	e.RedirectTrailingSlash = false
	e.RedirectFixedPath = false
	e.HandleMethodNotAllowed = false
	e.RemoveExtraSlash = false
	e.ContextWithFallback = true
	if err := e.SetTrustedProxies(cfg.Server.TrustedProxies); err != nil {
		return nil, err
	}

	corsPrefixes := []string{cfg.API.Prefix}
	if cfg.Compat.V2boardAPI {
		corsPrefixes = append(corsPrefixes, LegacyPrefix)
	}
	e.Use(
		middleware.Recovery(log),
		middleware.SecurityHeaders(),
		middleware.Logger(log),
		middleware.BodyLimit(cfg.Server.MaxBodyBytes),
		middleware.CORS(cfg.CORS.AllowOrigins, corsPrefixes...),
	)
	e.Use(extra...)

	r := &Router{
		Engine:     e,
		Config:     cfg,
		API:        e.Group(cfg.API.Prefix),
		NodeV1:     e.Group("/api/v1/server"),
		NodeV2:     e.Group("/api/v2/server"),
		LegacyRoot: e.Group(LegacyPrefix),
	}
	e.NoRoute(r.noRoute)
	e.NoMethod(httpx.NotFound)
	if cfg.Compat.V2boardAPI {
		r.Legacy = e.Group(LegacyPrefix)
	}

	r.API.GET("/ping", func(c *gin.Context) { httpx.OK(c, "pong") })
	return r, nil
}

// Mount 依次注册业务模块。
func (r *Router) Mount(mods ...Module) {
	for _, m := range mods {
		m.Register(r)
	}
}
