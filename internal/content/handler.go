package content

import (
	"errors"
	"io"
	"strconv"

	"github.com/gin-gonic/gin"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/server"
)

// Module 公告与知识库路由。
type Module struct {
	Svc  *Service
	Auth *auth.Service
	// Locale 返回当前请求语言，用于翻译正文中插入的提示。
	Locale func(c *gin.Context) string
}

// Register 挂载原生 API。
//
// 用户端（需登录）：
//
//	GET {api}/user/notices                公告列表，参数 current、page_size（默认 5，最多 100）
//	GET {api}/user/notices/:id            公告详情
//	GET {api}/user/knowledge              知识库列表（按分类分组），参数 language、keyword
//	GET {api}/user/knowledge/:id          文章详情，参数 subscription_id（默认主订阅）
//
// 管理端：
//
//	GET    notices                 全部公告
//	POST   notices                 新建公告
//	PUT    notices/:id             修改公告
//	POST   notices/:id/show        显示 / 隐藏，{"show": bool}
//	DELETE notices/:id             删除公告
//	GET    knowledge               全部文章（不含正文）
//	GET    knowledge/categories    已有分类
//	GET    knowledge/:id           文章详情
//	POST   knowledge               新建文章
//	PUT    knowledge/:id           修改文章
//	POST   knowledge/:id/show      显示 / 隐藏，{"show": bool}
//	POST   knowledge/sort          排序，{"ids": [...]}
//	DELETE knowledge/:id           删除文章
func (m *Module) Register(r *server.Router) {
	u := r.API.Group("/user", m.Auth.RequireUser())
	u.GET("/notices", m.notices)
	u.GET("/notices/:id", m.notice)
	u.GET("/knowledge", m.articles)
	u.GET("/knowledge/:id", m.article)

	a := m.Auth.AdminGroup(r.API, func() string { return m.Svc.Settings.String("secure_path") })
	a.GET("/notices", m.adminNotices)
	a.POST("/notices", m.saveNotice)
	a.PUT("/notices/:id", m.saveNotice)
	a.POST("/notices/:id/show", m.showNotice)
	a.DELETE("/notices/:id", m.deleteNotice)
	a.GET("/knowledge", m.adminArticles)
	a.GET("/knowledge/categories", m.categories)
	a.GET("/knowledge/:id", m.adminArticle)
	a.POST("/knowledge", m.saveArticle)
	a.PUT("/knowledge/:id", m.saveArticle)
	a.POST("/knowledge/:id/show", m.showArticle)
	a.POST("/knowledge/sort", m.sortArticles)
	a.DELETE("/knowledge/:id", m.deleteArticle)
}

func bind(c *gin.Context, v any) bool {
	if err := c.ShouldBindJSON(v); err != nil && !errors.Is(err, io.EOF) {
		httpx.Fail(c, errInvalid)
		return false
	}
	return true
}

func respond(c *gin.Context, data any, err error) {
	if err != nil {
		httpx.Fail(c, err)
		return
	}
	httpx.OK(c, data)
}

// optionalID 新建路由没有 :id，返回 0；其余路由的 :id 必须是正整数。
func optionalID(c *gin.Context) (int64, bool) {
	raw := c.Param("id")
	if raw == "" {
		return 0, true
	}
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		httpx.Fail(c, errInvalid)
		return 0, false
	}
	return id, true
}

func requiredID(c *gin.Context) (int64, bool) {
	if c.Param("id") == "" {
		httpx.Fail(c, errInvalid)
		return 0, false
	}
	return optionalID(c)
}

func (m *Module) notices(c *gin.Context) {
	var p NoticePage
	if err := c.ShouldBindQuery(&p); err != nil {
		httpx.Fail(c, errInvalid)
		return
	}
	v, err := m.Svc.Notices(c.Request.Context(), p)
	respond(c, v, err)
}

func (m *Module) notice(c *gin.Context) {
	id, ok := requiredID(c)
	if !ok {
		return
	}
	v, err := m.Svc.Notice(c.Request.Context(), id)
	respond(c, v, err)
}

func (m *Module) articles(c *gin.Context) {
	v, err := m.Svc.Articles(c.Request.Context(), c.Query("language"), c.Query("keyword"))
	respond(c, v, err)
}

func (m *Module) article(c *gin.Context) {
	id, ok := requiredID(c)
	if !ok {
		return
	}
	var subID int64
	if raw := c.Query("subscription_id"); raw != "" {
		v, err := strconv.ParseInt(raw, 10, 64)
		if err != nil || v <= 0 {
			httpx.Fail(c, errInvalid)
			return
		}
		subID = v
	}
	locale := ""
	if m.Locale != nil {
		locale = m.Locale(c)
	}
	v, err := m.Svc.Article(c.Request.Context(), auth.Current(c).ID, id, subID, httpx.BaseURL(c), locale)
	respond(c, v, err)
}

func (m *Module) adminNotices(c *gin.Context) {
	v, err := m.Svc.AdminNotices(c.Request.Context())
	respond(c, v, err)
}

func (m *Module) saveNotice(c *gin.Context) {
	id, ok := optionalID(c)
	if !ok {
		return
	}
	var in NoticeInput
	if !bind(c, &in) {
		return
	}
	v, err := m.Svc.SaveNotice(c.Request.Context(), id, in)
	respond(c, v, err)
}

type showInput struct {
	Show *bool `json:"show"`
}

func (m *Module) bindShow(c *gin.Context) (int64, bool, bool) {
	id, ok := requiredID(c)
	if !ok {
		return 0, false, false
	}
	var in showInput
	if !bind(c, &in) {
		return 0, false, false
	}
	if in.Show == nil {
		httpx.Fail(c, errInvalid)
		return 0, false, false
	}
	return id, *in.Show, true
}

func (m *Module) showNotice(c *gin.Context) {
	id, show, ok := m.bindShow(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.ShowNotice(c.Request.Context(), id, show))
}

func (m *Module) deleteNotice(c *gin.Context) {
	id, ok := requiredID(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.DeleteNotice(c.Request.Context(), id))
}

func (m *Module) adminArticles(c *gin.Context) {
	v, err := m.Svc.AdminArticles(c.Request.Context())
	respond(c, v, err)
}

func (m *Module) categories(c *gin.Context) {
	v, err := m.Svc.Categories(c.Request.Context())
	respond(c, v, err)
}

func (m *Module) adminArticle(c *gin.Context) {
	id, ok := requiredID(c)
	if !ok {
		return
	}
	v, err := m.Svc.AdminArticle(c.Request.Context(), id)
	respond(c, v, err)
}

func (m *Module) saveArticle(c *gin.Context) {
	id, ok := optionalID(c)
	if !ok {
		return
	}
	var in ArticleInput
	if !bind(c, &in) {
		return
	}
	v, err := m.Svc.SaveArticle(c.Request.Context(), id, in)
	respond(c, v, err)
}

func (m *Module) showArticle(c *gin.Context) {
	id, show, ok := m.bindShow(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.ShowArticle(c.Request.Context(), id, show))
}

func (m *Module) sortArticles(c *gin.Context) {
	var in struct {
		IDs []int64 `json:"ids"`
	}
	if !bind(c, &in) {
		return
	}
	respond(c, true, m.Svc.SortArticles(c.Request.Context(), in.IDs))
}

func (m *Module) deleteArticle(c *gin.Context) {
	id, ok := requiredID(c)
	if !ok {
		return
	}
	respond(c, true, m.Svc.DeleteArticle(c.Request.Context(), id))
}
