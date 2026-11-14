// Package middleware 提供全局 HTTP 中间件。
package middleware

import (
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/gin-gonic/gin"
	"go.uber.org/zap"

	"gvboard/internal/httpx"
	"gvboard/internal/syslog"
)

// Recovery 捕获 panic，记录日志并返回统一的内部错误，不输出堆栈。
func Recovery(log *zap.Logger) gin.HandlerFunc {
	return func(c *gin.Context) {
		defer func() {
			if r := recover(); r != nil {
				if r == http.ErrAbortHandler {
					panic(r)
				}
				log.Error("请求处理发生 panic", zap.Any("panic", r), zap.Stack("stack"),
					zap.String(syslog.FieldMethod, c.Request.Method), zap.String(syslog.FieldURI, c.Request.URL.Path),
					zap.String(syslog.FieldIP, c.ClientIP()), zap.String(syslog.FieldHost, c.Request.Host))
				if !c.Writer.Written() {
					c.AbortWithStatusJSON(http.StatusInternalServerError, httpx.Body{Code: httpx.CodeInternal, Msg: "服务器内部错误"})
				} else {
					c.Abort()
				}
			}
		}()
		c.Next()
	}
}

// Logger 注入请求级日志并记录访问日志。只记录路径不记录查询串，避免 token 等敏感参数落盘。
func Logger(log *zap.Logger) gin.HandlerFunc {
	access := log.Named("access")
	return func(c *gin.Context) {
		start := time.Now()
		httpx.SetLogger(c, log.WithLazy(
			zap.String(syslog.FieldMethod, c.Request.Method),
			zap.String(syslog.FieldURI, c.Request.URL.Path),
			zap.String(syslog.FieldIP, c.ClientIP()),
			zap.String(syslog.FieldHost, c.Request.Host),
		))
		c.Next()
		access.Info("",
			zap.Int("status", c.Writer.Status()),
			zap.String("method", c.Request.Method),
			zap.String("path", c.Request.URL.Path),
			zap.String("ip", c.ClientIP()),
			zap.Duration("latency", time.Since(start)),
			zap.Int("size", c.Writer.Size()),
		)
	}
}

// BodyLimit 限制请求体大小。
func BodyLimit(max int64) gin.HandlerFunc {
	return func(c *gin.Context) {
		if max > 0 && c.Request.Body != nil {
			c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, max)
		}
		c.Next()
	}
}

// SecurityHeaders 设置通用安全响应头，并确保不输出暴露服务端技术栈的头。
func SecurityHeaders() gin.HandlerFunc {
	return func(c *gin.Context) {
		h := c.Writer.Header()
		h.Set("X-Content-Type-Options", "nosniff")
		h.Set("X-Frame-Options", "SAMEORIGIN")
		h.Set("Referrer-Policy", "strict-origin-when-cross-origin")
		h.Del("X-Powered-By")
		h.Del("Server")
		c.Next()
	}
}

// CORS 分离部署时的跨域处理，仅对给定的 API 前缀生效，且只放行白名单来源。
// 非白名单来源不输出任何 CORS 头，预检请求按普通请求继续路由（最终得到统一 404）。
func CORS(origins []string, prefixes ...string) gin.HandlerFunc {
	if len(origins) == 0 {
		return func(c *gin.Context) { c.Next() }
	}
	allowed := slices.Clone(origins)
	under := func(path string) bool {
		for _, p := range prefixes {
			if path == p || strings.HasPrefix(path, p+"/") {
				return true
			}
		}
		return false
	}
	return func(c *gin.Context) {
		origin := c.GetHeader("Origin")
		if origin == "" || !slices.Contains(allowed, origin) || !under(c.Request.URL.Path) {
			c.Next()
			return
		}
		h := c.Writer.Header()
		h.Set("Access-Control-Allow-Origin", origin)
		h.Add("Vary", "Origin")
		h.Set("Access-Control-Allow-Credentials", "true")
		if c.Request.Method == http.MethodOptions && c.GetHeader("Access-Control-Request-Method") != "" {
			h.Set("Access-Control-Allow-Methods", "GET, POST, PUT, PATCH, DELETE, OPTIONS")
			h.Set("Access-Control-Allow-Headers", "Authorization, Content-Type, Content-Language, X-Requested-With")
			h.Set("Access-Control-Max-Age", "86400")
			c.AbortWithStatus(http.StatusNoContent)
			return
		}
		c.Next()
	}
}
