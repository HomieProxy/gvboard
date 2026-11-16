// Package content 公告与知识库，对应原项目 NoticeController、KnowledgeController（用户端与管理端）。
package content

import (
	"context"
	"encoding/base64"
	"errors"
	"net/url"
	"strings"
	"unicode/utf8"

	"gorm.io/gorm"

	"gvboard/internal/hook"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/phpx"
	"gvboard/internal/setting"
	"gvboard/internal/subscription"
)

var (
	// ErrNoticeNotFound / ErrArticleNotFound 记录不存在（兼容层据此换成原项目文案）。
	ErrNoticeNotFound  = httpx.NotFoundErr("Notice not found")
	ErrArticleNotFound = httpx.NotFoundErr("Article does not exist")
	errInvalid         = httpx.Validation("Invalid parameter")
)

// Service 公告与知识库服务。
type Service struct {
	DB            *gorm.DB
	Settings      *setting.Service
	Subscriptions *subscription.Service
	// Translate 按语言翻译文案，未设置时原样返回。
	Translate func(locale, key string) string
	// Hooks 插件过滤器（knowledge.article），可为 nil。
	Hooks hook.Hooks
	now   func() int64
}

func (s *Service) nowUnix() int64 {
	if s.now != nil {
		return s.now()
	}
	return model.Now()
}

func (s *Service) tr(locale, key string) string {
	if s.Translate == nil {
		return key
	}
	return s.Translate(locale, key)
}

// ---------- 公告 ----------

// NoticePage 用户端公告分页，与原项目一致：默认每页 5 条，最多 100 条。
type NoticePage struct {
	Current  int `form:"current"`
	PageSize int `form:"page_size"`
}

// Notices 用户端显示中的公告，按创建时间倒序。
func (s *Service) Notices(ctx context.Context, p NoticePage) (*httpx.Page, error) {
	cur, size := p.Current, p.PageSize
	if cur < 1 {
		cur = 1
	}
	if size == 0 {
		size = 5
	}
	size = min(max(size, 1), 100)
	q := s.DB.WithContext(ctx).Model(&model.Notice{}).Where(shown).Session(&gorm.Session{})
	var total int64
	if err := q.Count(&total).Error; err != nil {
		return nil, err
	}
	list := []model.Notice{}
	err := q.Order("created_at DESC").Order("id DESC").Offset((cur - 1) * size).Limit(size).Find(&list).Error
	return &httpx.Page{Total: total, List: list}, err
}

// Notice 用户端公告详情，隐藏的公告视为不存在。
func (s *Service) Notice(ctx context.Context, id int64) (*model.Notice, error) {
	var n model.Notice
	err := s.DB.WithContext(ctx).Where(shown).Take(&n, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrNoticeNotFound
	}
	return &n, err
}

// AdminNotices 管理端全部公告，按 ID 倒序。
func (s *Service) AdminNotices(ctx context.Context) ([]model.Notice, error) {
	list := []model.Notice{}
	err := s.DB.WithContext(ctx).Order("id DESC").Find(&list).Error
	return list, err
}

// NoticeInput 公告保存参数。
type NoticeInput struct {
	Title   string   `json:"title"`
	Content string   `json:"content"`
	ImgURL  *string  `json:"img_url"`
	Tags    []string `json:"tags"`
	Show    *bool    `json:"show"`
}

func (in *NoticeInput) validate() error {
	in.Title, in.Content = strings.TrimSpace(in.Title), strings.TrimSpace(in.Content)
	if in.Title == "" {
		return httpx.Validation("标题不能为空")
	}
	if utf8.RuneCountInString(in.Title) > 255 {
		return httpx.Validation("标题不能超过 255 个字符")
	}
	if in.Content == "" {
		return httpx.Validation("内容不能为空")
	}
	if in.ImgURL != nil {
		v := strings.TrimSpace(*in.ImgURL)
		if v == "" {
			in.ImgURL = nil
		} else {
			if utf8.RuneCountInString(v) > 255 || !validURL(v) {
				return httpx.Validation("图片URL格式不正确")
			}
			in.ImgURL = &v
		}
	}
	if in.Tags != nil {
		tags := make([]string, 0, len(in.Tags))
		for _, t := range in.Tags {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
		in.Tags = tags
		if v, _ := model.StrList(tags).Value(); utf8.RuneCountInString(v.(string)) > 255 {
			return httpx.Validation("标签总长度过长")
		}
	}
	return nil
}

func validURL(s string) bool {
	u, err := url.Parse(s)
	return err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Host != ""
}

// SaveNotice 新建（id 为 0）或修改公告。
func (s *Service) SaveNotice(ctx context.Context, id int64, in NoticeInput) (*model.Notice, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	db := s.DB.WithContext(ctx)
	var n model.Notice
	if id > 0 {
		if err := db.Take(&n, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ErrNoticeNotFound
			}
			return nil, err
		}
	}
	n.Title, n.Content, n.ImgURL = in.Title, in.Content, in.ImgURL
	n.Tags = nil
	if len(in.Tags) > 0 {
		n.Tags = in.Tags
	}
	if in.Show != nil {
		n.Show = boolInt(*in.Show)
	}
	var err error
	if id > 0 {
		err = db.Select("title", "content", "img_url", "tags", "show", "updated_at").Save(&n).Error
	} else {
		err = db.Create(&n).Error
	}
	return &n, err
}

// ShowNotice 设置公告是否显示。
func (s *Service) ShowNotice(ctx context.Context, id int64, show bool) error {
	return updateShow(s.DB.WithContext(ctx), &model.Notice{}, id, show, ErrNoticeNotFound)
}

// DeleteNotice 删除公告。
func (s *Service) DeleteNotice(ctx context.Context, id int64) error {
	res := s.DB.WithContext(ctx).Delete(&model.Notice{}, id)
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrNoticeNotFound
	}
	return res.Error
}

// updateShow 修改 show。MySQL 在值未变化时 RowsAffected 为 0，所以先单独确认记录存在。
func updateShow(db *gorm.DB, m any, id int64, show bool, notFound error) error {
	var n int64
	if err := db.Model(m).Where("id = ?", id).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		return notFound
	}
	return db.Model(m).Where("id = ?", id).Update("show", boolInt(show)).Error
}

var shown = map[string]any{"show": 1}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ---------- 知识库 ----------

// ArticleBrief 知识库列表项。
type ArticleBrief struct {
	ID        int64  `json:"id"`
	Category  string `json:"category"`
	Title     string `json:"title"`
	UpdatedAt int64  `json:"updated_at"`
}

// Category 按分类分组的文章，分类按首篇文章出现顺序排列。
type Category struct {
	Category string         `json:"category"`
	Articles []ArticleBrief `json:"articles"`
}

// orderBySort 原项目按 sort 升序，MySQL 中 NULL 排最前；PostgreSQL 默认 NULL 排最后，这里统一为最前。
const orderBySort = "CASE WHEN sort IS NULL THEN 0 ELSE 1 END, sort, id"

// escapeLike 转义 LIKE 通配符，配合 ESCAPE '!'。
func escapeLike(s string) string {
	return strings.NewReplacer("!", "!!", "%", "!%", "_", "!_").Replace(s)
}

// Articles 用户端知识库列表：language 为空时不按语言过滤，keyword 匹配标题或正文（不区分大小写）。
func (s *Service) Articles(ctx context.Context, language, keyword string) ([]Category, error) {
	q := s.DB.WithContext(ctx).Model(&model.Knowledge{}).
		Select("id", "category", "title", "updated_at").Where(shown)
	if language != "" {
		q = q.Where("language = ?", language)
	}
	if keyword = strings.TrimSpace(keyword); keyword != "" {
		kw := "%" + escapeLike(strings.ToLower(keyword)) + "%"
		q = q.Where("(LOWER(title) LIKE ? ESCAPE '!' OR LOWER(body) LIKE ? ESCAPE '!')", kw, kw)
	}
	var rows []ArticleBrief
	if err := q.Order(orderBySort).Find(&rows).Error; err != nil {
		return nil, err
	}
	return group(rows), nil
}

func group(rows []ArticleBrief) []Category {
	out := []Category{}
	index := map[string]int{}
	for _, r := range rows {
		i, ok := index[r.Category]
		if !ok {
			i = len(out)
			index[r.Category] = i
			out = append(out, Category{Category: r.Category})
		}
		out[i].Articles = append(out[i].Articles, r)
	}
	return out
}

// Article 用户端文章详情。正文中的订阅变量按 subscriptionID 指定的订阅（0 为主订阅）替换；
// 用户有任一可用订阅时才显示访问限制区块的内容。
func (s *Service) Article(ctx context.Context, userID, id, subscriptionID int64, baseURL, locale string) (*model.Knowledge, error) {
	db := s.DB.WithContext(ctx)
	var k model.Knowledge
	if err := db.Where(shown).Take(&k, id).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, ErrArticleNotFound
		}
		return nil, err
	}
	var sub *model.Subscription
	var err error
	if subscriptionID > 0 {
		sub, err = s.Subscriptions.Find(ctx, db, userID, subscriptionID)
	} else {
		sub, err = s.Subscriptions.Primary(ctx, db, userID)
	}
	if err != nil {
		return nil, err
	}
	available, err := s.anyAvailable(ctx, userID)
	if err != nil {
		return nil, err
	}
	if !available {
		k.Body = lockAccess(k.Body, s.tr(locale, "You must have a valid subscription to view content in this area"))
	}
	link, err := s.Subscriptions.Linker.URL(ctx, sub, baseURL)
	if err != nil {
		return nil, err
	}
	k.Body = strings.NewReplacer(
		"{{siteName}}", s.Settings.String("app_name"),
		"{{subscribeUrl}}", link,
		"{{urlEncodeSubscribeUrl}}", phpx.URLEncode(link),
		"{{safeBase64SubscribeUrl}}", base64.RawURLEncoding.EncodeToString([]byte(link)),
		"{{subscribeToken}}", sub.Token,
	).Replace(k.Body)
	if s.Hooks != nil {
		v := hook.Apply(s.Hooks, ctx, hook.FilterKnowledgeArticle, hook.KnowledgeArticle{
			UserID: userID, ArticleID: k.ID, Locale: locale, Available: available, Body: k.Body,
		})
		k.Body = v.Body
	}
	return &k, nil
}

func (s *Service) anyAvailable(ctx context.Context, userID int64) (bool, error) {
	var subs []model.Subscription
	err := s.DB.WithContext(ctx).Where("user_id = ?", userID).
		Select("id", "banned", "transfer_enable", "expired_at").Find(&subs).Error
	if err != nil {
		return false, err
	}
	now := s.nowUnix()
	for i := range subs {
		if subscription.Available(&subs[i], now) {
			return true, nil
		}
	}
	return false, nil
}

const (
	accessStart = "<!--access start-->"
	accessEnd   = "<!--access end-->"
)

// lockAccess 把每个访问限制区块替换为提示。原项目缺少结束标记时会死循环，这里隐藏起始标记之后的全部内容。
func lockAccess(body, tip string) string {
	notice := `<div class="content-locked">` + tip + `</div>`
	var b strings.Builder
	for {
		i := strings.Index(body, accessStart)
		if i < 0 {
			b.WriteString(body)
			break
		}
		b.WriteString(body[:i])
		b.WriteString(notice)
		rest := body[i+len(accessStart):]
		j := strings.Index(rest, accessEnd)
		if j < 0 {
			break
		}
		body = rest[j+len(accessEnd):]
	}
	return b.String()
}

// AdminArticle 管理端知识库列表项。
type AdminArticle struct {
	ID        int64  `json:"id"`
	Language  string `json:"language"`
	Category  string `json:"category"`
	Title     string `json:"title"`
	Sort      *int64 `json:"sort"`
	Show      int    `json:"show"`
	UpdatedAt int64  `json:"updated_at"`
}

// AdminArticles 管理端全部文章（不含正文），按排序。
func (s *Service) AdminArticles(ctx context.Context) ([]AdminArticle, error) {
	list := []AdminArticle{}
	err := s.DB.WithContext(ctx).Model(&model.Knowledge{}).
		Select("id", "language", "category", "title", "sort", "show", "updated_at").
		Order(orderBySort).Find(&list).Error
	return list, err
}

// AdminArticle 管理端文章详情。
func (s *Service) AdminArticle(ctx context.Context, id int64) (*model.Knowledge, error) {
	var k model.Knowledge
	err := s.DB.WithContext(ctx).Take(&k, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrArticleNotFound
	}
	return &k, err
}

// Categories 全部分类，按首次出现顺序。
func (s *Service) Categories(ctx context.Context) ([]string, error) {
	var cats []string
	err := s.DB.WithContext(ctx).Model(&model.Knowledge{}).Order("id").Pluck("category", &cats).Error
	out := []string{}
	seen := map[string]bool{}
	for _, c := range cats {
		if !seen[c] {
			seen[c] = true
			out = append(out, c)
		}
	}
	return out, err
}

// ArticleInput 文章保存参数。
type ArticleInput struct {
	Category string `json:"category"`
	Language string `json:"language"`
	Title    string `json:"title"`
	Body     string `json:"body"`
	Show     *bool  `json:"show"`
}

func (in *ArticleInput) validate() error {
	in.Category, in.Language = strings.TrimSpace(in.Category), strings.TrimSpace(in.Language)
	in.Title, in.Body = strings.TrimSpace(in.Title), strings.TrimSpace(in.Body)
	switch {
	case in.Category == "":
		return httpx.Validation("分类不能为空")
	case utf8.RuneCountInString(in.Category) > 255:
		return httpx.Validation("分类不能超过 255 个字符")
	case in.Language == "":
		return httpx.Validation("语言不能为空")
	case utf8.RuneCountInString(in.Language) > 5:
		return httpx.Validation("语言格式不正确")
	case in.Title == "":
		return httpx.Validation("标题不能为空")
	case utf8.RuneCountInString(in.Title) > 255:
		return httpx.Validation("标题不能超过 255 个字符")
	case in.Body == "":
		return httpx.Validation("内容不能为空")
	}
	return nil
}

// SaveArticle 新建（id 为 0）或修改文章。
func (s *Service) SaveArticle(ctx context.Context, id int64, in ArticleInput) (*model.Knowledge, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	db := s.DB.WithContext(ctx)
	var k model.Knowledge
	if id > 0 {
		if err := db.Take(&k, id).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return nil, ErrArticleNotFound
			}
			return nil, err
		}
	}
	k.Category, k.Language, k.Title, k.Body = in.Category, in.Language, in.Title, in.Body
	if in.Show != nil {
		k.Show = boolInt(*in.Show)
	}
	var err error
	if id > 0 {
		err = db.Select("category", "language", "title", "body", "show", "updated_at").Save(&k).Error
	} else {
		err = db.Create(&k).Error
	}
	return &k, err
}

// ShowArticle 设置文章是否显示。
func (s *Service) ShowArticle(ctx context.Context, id int64, show bool) error {
	return updateShow(s.DB.WithContext(ctx), &model.Knowledge{}, id, show, ErrArticleNotFound)
}

// SortArticles 按给定顺序设置 sort 为 1、2、3…，与原项目一致不更新 updated_at。
func (s *Service) SortArticles(ctx context.Context, ids []int64) error {
	if len(ids) == 0 {
		return errInvalid
	}
	seen := map[int64]bool{}
	for _, id := range ids {
		if id <= 0 || seen[id] {
			return errInvalid
		}
		seen[id] = true
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var n int64
		if err := tx.Model(&model.Knowledge{}).Where("id IN ?", ids).Count(&n).Error; err != nil {
			return err
		}
		if n != int64(len(ids)) {
			return ErrArticleNotFound
		}
		for i, id := range ids {
			if err := tx.Model(&model.Knowledge{}).Where("id = ?", id).UpdateColumn("sort", i+1).Error; err != nil {
				return err
			}
		}
		return nil
	})
}

// DeleteArticle 删除文章。
func (s *Service) DeleteArticle(ctx context.Context, id int64) error {
	res := s.DB.WithContext(ctx).Delete(&model.Knowledge{}, id)
	if res.Error == nil && res.RowsAffected == 0 {
		return ErrArticleNotFound
	}
	return res.Error
}
