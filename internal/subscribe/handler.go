package subscribe

import (
	"errors"
	"net/http"
	"strconv"
	"strings"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

// defaultSubscribePath 未设置订阅路径时的订阅地址（与订阅链接生成一致）。
const defaultSubscribePath = "/api/v1/client/subscribe"

// Module 订阅下发路由。
type Module struct {
	Svc  *Service
	Auth *auth.Service
}

// Register 挂载：
//
//	GET {subscribe_path}                         订阅下发（路径为运行时设置，修改后立即生效）
//	GET {api}/client/app/config                  App 内置 Clash 配置（token 认证）
//	GET {api}/client/app/version                 App 版本（token 认证）
//	GET {api}/user/subscriptions/:id/servers     订阅可用节点（登录，ETag）
//
// 管理端接口见 registerAdmin。
//
// compat.legacy_subscribe 开启时另挂原路径 /api/v1/client/subscribe、/api/v1/client/app/getConfig、/api/v1/client/app/getVersion。
// token 缺失或无效一律返回统一 404。
func (m *Module) Register(r *server.Router) {
	r.AddFallback(m.fallback)
	r.API.GET("/client/app/config", m.appConfig)
	r.API.GET("/client/app/version", m.appVersion(false))
	r.API.Group("/user", m.Auth.RequireUser()).GET("/subscriptions/:id/servers", m.servers)
	if r.Config.Compat.LegacySubscribe {
		r.LegacyRoot.GET("/client/subscribe", m.subscribe)
		r.LegacyRoot.GET("/client/app/getConfig", m.appConfig)
		r.LegacyRoot.GET("/client/app/getVersion", m.appVersion(true))
	}
	m.registerAdmin(r)
}

// fallback 匹配运行时的订阅路径（允许末尾带 /）。
func (m *Module) fallback(c *gin.Context) bool {
	if c.Request.Method != http.MethodGet && c.Request.Method != http.MethodHead {
		return false
	}
	path := m.Svc.Settings.String("subscribe_path")
	if path == "" {
		path = defaultSubscribePath
	}
	p := c.Request.URL.Path
	if p != path && p != path+"/" {
		return false
	}
	m.subscribe(c)
	return true
}

// input 对应原项目 $request->input()：去掉首尾空白，空串视为未传。
func input(c *gin.Context, key string) string {
	v, ok := c.GetQuery(key)
	if !ok {
		v = c.PostForm(key)
	}
	return strings.Trim(v, " \t\n\r\x00\x0B")
}

// resolve token 认证，失败时已写出 404 / 错误响应。
func (m *Module) resolve(c *gin.Context) (*Ctx, bool) {
	sub, err := m.Svc.Resolve(c.Request.Context(), input(c, "token"))
	if err != nil {
		if errors.Is(err, ErrToken) {
			m.Svc.countInvalid(c.Request.Context())
			httpx.NotFound(c)
		} else {
			httpx.Fail(c, err)
		}
		return nil, false
	}
	return &Ctx{Sub: sub}, true
}

func access(c *gin.Context, flag string) Access {
	return Access{
		IP:        c.ClientIP(),
		UserAgent: c.GetHeader("User-Agent"),
		Flag:      flag,
		Header:    c.GetHeader,
	}
}

// write 输出订阅内容，r 为 nil 时返回统一 404（风控拒绝）。
func write(c *gin.Context, r *Response) {
	if r == nil {
		httpx.NotFound(c)
		return
	}
	for _, h := range r.Headers {
		c.Header(h[0], h[1])
	}
	c.Data(http.StatusOK, r.ContentType, r.Body)
}

func (m *Module) subscribe(c *gin.Context) {
	x, ok := m.resolve(c)
	if !ok {
		return
	}
	rawFlag := input(c, "flag")
	flag := rawFlag
	if flag == "" {
		flag = c.GetHeader("User-Agent")
	}
	req := Request{Flag: flag, Host: c.Request.Host, BaseURL: httpx.BaseURL(c)}
	resp, err := m.Svc.Deliver(c.Request.Context(), x.Sub, req, access(c, rawFlag), false)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	write(c, resp)
}

func (m *Module) appConfig(c *gin.Context) {
	x, ok := m.resolve(c)
	if !ok {
		return
	}
	resp, err := m.Svc.Deliver(c.Request.Context(), x.Sub, Request{}, access(c, input(c, "flag")), true)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	write(c, resp)
}

// appVersion legacy 为 true 时按原项目格式 {"data": ...} 返回。
func (m *Module) appVersion(legacy bool) gin.HandlerFunc {
	return func(c *gin.Context) {
		if _, ok := m.resolve(c); !ok {
			return
		}
		v := m.Svc.AppVersion(c.GetHeader("User-Agent"))
		if legacy {
			c.JSON(http.StatusOK, gin.H{"data": v})
			return
		}
		httpx.OK(c, v)
	}
}

var errInvalidSubID = httpx.Validation("Invalid parameters")

func (m *Module) servers(c *gin.Context) {
	id, err := strconv.ParseInt(c.Param("id"), 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, errInvalidSubID)
		return
	}
	list, etag, err := m.Svc.UserServers(c.Request.Context(), auth.Current(c).ID, id)
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	if strings.Contains(c.GetHeader("If-None-Match"), etag) {
		c.Status(http.StatusNotModified)
		return
	}
	c.Header("ETag", `"`+etag+`"`)
	httpx.OK(c, list)
}
