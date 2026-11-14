package legacy

import (
	"errors"
	"net/url"

	"github.com/gin-gonic/gin"
	"gorm.io/gorm"

	"gvboard/internal/content"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
)

// id 参数 id 的整数值；缺失、为空或不是整数时 ok 为 false（原项目 find 查不到记录）。
func (in input) id() (int64, bool) {
	id, ok := in.int("id")
	return id, ok && id > 0
}

// exists 指定 ID 的记录是否存在。
func (m *Module) exists(c *gin.Context, mdl any, id int64) (bool, error) {
	err := m.DB.WithContext(c.Request.Context()).Select("id").Take(mdl, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	return err == nil, err
}

// fromNative 原生服务的业务错误（4xx）统一换成原项目的固定文案，内部错误照常处理。
func fromNative(err error, msg string) error {
	var he *httpx.Error
	if errors.As(err, &he) && he.Status < 500 {
		return abortRaw(msg)
	}
	return err
}

// ---------- 公告 ----------

func (m *Module) adminNoticeFetch(c *gin.Context) {
	list, err := m.Content.AdminNotices(c.Request.Context())
	m.respond(c, list, err)
}

func (m *Module) adminNoticeSave(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	v.required("title", "标题不能为空")
	v.required("content", "内容不能为空")
	if v.present("img_url") {
		u, err := url.Parse(in.str("img_url"))
		v.check("img_url", err == nil && u.Scheme != "" && u.Host != "", "图片URL格式不正确")
	}
	if v.present("tags") {
		v.check("tags", in.isArray("tags"), "标签格式不正确")
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	n := content.NoticeInput{Title: in.str("title"), Content: in.str("content"), Tags: in.strList("tags")}
	if in.raw("img_url") != nil {
		s := in.str("img_url")
		n.ImgURL = &s
	}
	var id int64
	if in.truthy("id") {
		var ok bool
		if id, ok = in.id(); !ok {
			m.fail(c, abortRaw("保存失败"))
			return
		}
	}
	_, err := m.Content.SaveNotice(c.Request.Context(), id, n)
	if errors.Is(err, content.ErrNoticeNotFound) {
		err = abortRaw("保存失败")
	}
	m.respond(c, true, err)
}

func (m *Module) adminNoticeShow(c *gin.Context) {
	in := params(c)
	if !in.truthy("id") {
		m.fail(c, abortRaw("参数有误"))
		return
	}
	var n model.Notice
	id, _ := in.id()
	if err := m.DB.WithContext(c.Request.Context()).Select("id", "show").Take(&n, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || id == 0 {
			err = abortRaw("公告不存在")
		}
		m.fail(c, err)
		return
	}
	m.respond(c, true, m.Content.ShowNotice(c.Request.Context(), id, n.Show == 0))
}

func (m *Module) adminNoticeDrop(c *gin.Context) {
	in := params(c)
	if !in.truthy("id") {
		m.fail(c, abortRaw("参数错误"))
		return
	}
	id, _ := in.id()
	err := m.Content.DeleteNotice(c.Request.Context(), id)
	if errors.Is(err, content.ErrNoticeNotFound) {
		err = abortRaw("公告不存在")
	}
	m.respond(c, true, err)
}

// ---------- 知识库 ----------

// adminArticle 原项目管理端知识库列表的字段。
type adminArticle struct {
	Title     string `json:"title"`
	ID        int64  `json:"id"`
	UpdatedAt int64  `json:"updated_at"`
	Category  string `json:"category"`
	Show      int    `json:"show"`
}

func (m *Module) adminKnowledgeFetch(c *gin.Context) {
	in := params(c)
	ctx := c.Request.Context()
	if in.truthy("id") {
		id, _ := in.id()
		k, err := m.Content.AdminArticle(ctx, id)
		if errors.Is(err, content.ErrArticleNotFound) {
			err = abortRaw("知识不存在")
		}
		m.respond(c, k, err)
		return
	}
	list, err := m.Content.AdminArticles(ctx)
	out := make([]adminArticle, len(list))
	for i, a := range list {
		out[i] = adminArticle{Title: a.Title, ID: a.ID, UpdatedAt: a.UpdatedAt, Category: a.Category, Show: a.Show}
	}
	m.respond(c, out, err)
}

func (m *Module) adminKnowledgeCategory(c *gin.Context) {
	list, err := m.Content.Categories(c.Request.Context())
	m.respond(c, list, err)
}

func (m *Module) adminKnowledgeSave(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	v.required("category", "分类不能为空")
	v.required("language", "语言不能为空")
	v.required("title", "标题不能为空")
	v.required("body", "内容不能为空")
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	a := content.ArticleInput{
		Category: in.str("category"), Language: in.str("language"), Title: in.str("title"), Body: in.str("body"),
	}
	var id int64
	if in.truthy("id") {
		var ok bool
		if id, ok = in.id(); !ok {
			m.fail(c, abortRaw("保存失败"))
			return
		}
	}
	_, err := m.Content.SaveArticle(c.Request.Context(), id, a)
	if errors.Is(err, content.ErrArticleNotFound) {
		err = abortRaw("保存失败")
	}
	m.respond(c, true, err)
}

func (m *Module) adminKnowledgeShow(c *gin.Context) {
	in := params(c)
	if !in.truthy("id") {
		m.fail(c, abortRaw("参数有误"))
		return
	}
	var k model.Knowledge
	id, _ := in.id()
	if err := m.DB.WithContext(c.Request.Context()).Select("id", "show").Take(&k, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) || id == 0 {
			err = abortRaw("知识不存在")
		}
		m.fail(c, err)
		return
	}
	m.respond(c, true, m.Content.ShowArticle(c.Request.Context(), id, k.Show == 0))
}

func (m *Module) adminKnowledgeSort(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	if v.required("knowledge_ids", "知识ID不能为空") {
		v.check("knowledge_ids", in.isArray("knowledge_ids"), "知识ID格式有误")
	}
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	ids, ok := in.intList("knowledge_ids")
	if !ok {
		m.fail(c, abortRaw("保存失败"))
		return
	}
	m.respond(c, true, fromNative(m.Content.SortArticles(c.Request.Context(), ids), "保存失败"))
}

func (m *Module) adminKnowledgeDrop(c *gin.Context) {
	in := params(c)
	if !in.truthy("id") {
		m.fail(c, abortRaw("参数有误"))
		return
	}
	id, _ := in.id()
	err := m.Content.DeleteArticle(c.Request.Context(), id)
	if errors.Is(err, content.ErrArticleNotFound) {
		err = abortRaw("知识不存在")
	}
	m.respond(c, true, err)
}

// ---------- 工单 ----------

func (m *Module) adminTicketFetch(c *gin.Context) {
	in := params(c)
	ctx := c.Request.Context()
	if in.truthy("id") {
		id, _ := in.id()
		d, err := m.Tickets.AdminGet(ctx, id)
		if err != nil {
			m.fail(c, fromNative(err, "工单不存在"))
			return
		}
		ok(c, ticketDetail{Ticket: d.Ticket, Message: d.Messages})
		return
	}
	current, size := in.page()
	db := m.DB.WithContext(ctx).Model(&model.Ticket{})
	if in.raw("status") != nil {
		db = db.Where("status = ?", in.str("status"))
	}
	if in.raw("reply_status") != nil {
		list := in.strList("reply_status")
		if list == nil {
			list = []string{in.str("reply_status")}
		}
		db = db.Where("reply_status IN ?", list)
	}
	if in.raw("email") != nil {
		var u model.User
		err := m.DB.WithContext(ctx).Select("id").Where("email = ?", in.str("email")).Take(&u).Error
		switch {
		case err == nil:
			db = db.Where("user_id = ?", u.ID)
		case !errors.Is(err, gorm.ErrRecordNotFound):
			m.fail(c, err)
			return
		}
	}
	db = db.Session(&gorm.Session{})
	var total int64
	if err := db.Count(&total).Error; err != nil {
		m.fail(c, err)
		return
	}
	list := []model.Ticket{}
	if err := db.Order("updated_at DESC").Order("id DESC").Offset((current - 1) * size).Limit(size).Find(&list).Error; err != nil {
		m.fail(c, err)
		return
	}
	paged(c, list, total)
}

func (m *Module) adminTicketReply(c *gin.Context) {
	in := params(c)
	if !in.truthy("id") {
		m.fail(c, abortRaw("参数错误"))
		return
	}
	if !in.truthy("message") {
		m.fail(c, abortRaw("消息不能为空"))
		return
	}
	id, _ := in.id()
	if found, err := m.exists(c, &model.Ticket{}, id); err != nil || !found {
		if err == nil {
			err = abortRaw("工单不存在")
		}
		m.fail(c, err)
		return
	}
	m.respond(c, true, m.Tickets.ReplyByAdmin(c.Request.Context(), uid(c), id, in.str("message")))
}

func (m *Module) adminTicketClose(c *gin.Context) {
	in := params(c)
	if !in.truthy("id") {
		m.fail(c, abortRaw("参数错误"))
		return
	}
	id, _ := in.id()
	m.respond(c, true, fromNative(m.Tickets.Close(c.Request.Context(), 0, id), "工单不存在"))
}
