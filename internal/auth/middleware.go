package auth

import (
	"crypto/subtle"
	"errors"

	"github.com/gin-gonic/gin"

	"gvboard/internal/httpx"
)

const identityKey = "gvb.identity"

// 用户端认证失败的错误（决策 D12：返回 401 / 403 JSON，便于前端跳转登录）。
var (
	errNotLoggedIn = httpx.Unauthorized("Not logged in or login expired")
	errSuspended   = httpx.Forbidden("Your account has been suspended")
)

// RequireUser 用户端接口认证。凭证从 Authorization 请求头读取。
func (s *Service) RequireUser() gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := s.Parse(c.Request.Context(), c.GetHeader("Authorization"))
		switch {
		case err == nil:
			c.Set(identityKey, id)
			c.Next()
		case errors.Is(err, ErrUnauthenticated):
			httpx.Fail(c, errNotLoggedIn)
		case errors.Is(err, ErrBanned):
			httpx.Fail(c, errSuspended)
		default:
			httpx.Fail(c, httpx.Internal(err))
		}
	}
}

// RequireAdmin 管理端接口认证。任何失败（含未登录、非管理员、内部错误）都返回统一 404，隐藏管理端存在（决策 D12）。
func (s *Service) RequireAdmin() gin.HandlerFunc {
	return func(c *gin.Context) {
		id, err := s.Parse(c.Request.Context(), c.GetHeader("Authorization"))
		if err != nil || !id.IsAdmin {
			httpx.NotFound(c)
			return
		}
		c.Set(identityKey, id)
		c.Next()
	}
}

// AdminParam 管理端路由组中安全路径参数的名称，各模块必须使用相同名称（路由树要求同一位置的参数名一致）。
const AdminParam = "secure"

// AdminGroup 管理端原生 API 路由组 {api}/{secure_path}（决策 D20）。
// 路径与当前设置的安全路径不一致、未设置安全路径、未登录或非管理员，一律返回统一 404。
// securePath 每次请求时读取，修改设置后立即生效。
// 认证通过后依次执行 AdminHooks（如操作审计）。
func (s *Service) AdminGroup(api *gin.RouterGroup, securePath func() string) *gin.RouterGroup {
	handlers := []gin.HandlerFunc{func(c *gin.Context) {
		want := securePath()
		if want == "" || subtle.ConstantTimeCompare([]byte(c.Param(AdminParam)), []byte(want)) != 1 {
			httpx.NotFound(c)
			return
		}
		c.Next()
	}, s.RequireAdmin()}
	return api.Group("/:"+AdminParam, append(handlers, s.AdminHooks...)...)
}

// SetCurrent 设置当前登录用户（插件路由在独立的路由引擎中执行，由插件管理器传递外层已认证的用户）。
func SetCurrent(c *gin.Context, id *Identity) {
	if id != nil {
		c.Set(identityKey, id)
	}
}

// Current 返回当前登录用户，未经过认证中间件时返回 nil。
func Current(c *gin.Context) *Identity {
	if v, ok := c.Get(identityKey); ok {
		if id, ok := v.(*Identity); ok {
			return id
		}
	}
	return nil
}
