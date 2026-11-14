package content

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/config"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/phpx"
	"gvboard/internal/server"
	"gvboard/internal/setting"
	"gvboard/internal/subscription"
	"gvboard/internal/testutil"
)

const testNow = 1_800_000_000

type env struct {
	*Service
	auth *auth.Service
	seq  int
}

func newEnv(t *testing.T, driver string) *env {
	t.Helper()
	db := testutil.DB(t, driver)
	c := testutil.Redis(t)
	st := setting.New(db, c, zap.NewNop())
	if err := st.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	subs := &subscription.Service{DB: db, Cache: c, Settings: st, Linker: &subscription.Linker{Settings: st, Cache: c}}
	return &env{
		Service: &Service{
			DB: db, Settings: st, Subscriptions: subs,
			Translate: func(_, key string) string { return "T(" + key + ")" },
			now:       func() int64 { return testNow },
		},
		auth: auth.New(db, c, "base64:Y29udGVudC10ZXN0LWtleS0wMTIzNDU2Nzg5YWJjZGVm"),
	}
}

func (e *env) set(t *testing.T, kv map[string]any) {
	t.Helper()
	if err := e.Settings.Set(context.Background(), kv); err != nil {
		t.Fatal(err)
	}
}

func (e *env) user(t *testing.T) (*model.User, *model.Subscription) {
	t.Helper()
	e.seq++
	var u *model.User
	var sub *model.Subscription
	if err := e.DB.Transaction(func(tx *gorm.DB) error {
		var err error
		u, sub, err = account.Create(tx, account.NewUser{Email: fmt.Sprintf("u%d@x.com", e.seq), Password: "password123"})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return u, sub
}

func wantErr(t *testing.T, err error, want error) {
	t.Helper()
	var he, we *httpx.Error
	if !errors.As(err, &he) || !errors.As(want, &we) || he.Msg != we.Msg {
		t.Fatalf("期望错误 %v，实际 %v", want, err)
	}
}

func wantValidation(t *testing.T, err error, msg string) {
	t.Helper()
	var he *httpx.Error
	if !errors.As(err, &he) || he.Msg != msg {
		t.Fatalf("期望错误 %q，实际 %v", msg, err)
	}
}

func ptr[T any](v T) *T { return &v }

func TestLockAccess(t *testing.T) {
	tip := `<div class="content-locked">X</div>`
	cases := []struct{ in, want string }{
		{"无限制", "无限制"},
		{"a<!--access start-->秘密<!--access end-->b", "a" + tip + "b"},
		{"<!--access start-->1<!--access end-->中<!--access start-->2<!--access end-->", tip + "中" + tip},
		// 缺少结束标记：原项目死循环，这里隐藏起始标记之后的全部内容
		{"a<!--access start-->秘密", "a" + tip},
		{"a<!--access end-->b", "a<!--access end-->b"},
	}
	for _, c := range cases {
		if got := lockAccess(c.in, "X"); got != c.want {
			t.Errorf("lockAccess(%q) = %q，期望 %q", c.in, got, c.want)
		}
	}
}

func TestContent(t *testing.T) {
	drivers := testutil.Drivers()
	if len(drivers) == 0 {
		t.Skip("未配置测试数据库，跳过")
	}
	for _, driver := range drivers {
		t.Run(driver, func(t *testing.T) {
			t.Run("公告", func(t *testing.T) { testNotices(t, newEnv(t, driver)) })
			t.Run("知识库列表与管理", func(t *testing.T) { testArticles(t, newEnv(t, driver)) })
			t.Run("文章详情", func(t *testing.T) { testArticle(t, newEnv(t, driver)) })
			t.Run("HTTP", func(t *testing.T) { testHTTP(t, newEnv(t, driver)) })
		})
	}
}

func testNotices(t *testing.T, e *env) {
	ctx := context.Background()
	for _, c := range []struct {
		in  NoticeInput
		msg string
	}{
		{NoticeInput{Content: "c"}, "标题不能为空"},
		{NoticeInput{Title: strings.Repeat("长", 256), Content: "c"}, "标题不能超过 255 个字符"},
		{NoticeInput{Title: "t", Content: "  "}, "内容不能为空"},
		{NoticeInput{Title: "t", Content: "c", ImgURL: ptr("not a url")}, "图片URL格式不正确"},
		{NoticeInput{Title: "t", Content: "c", ImgURL: ptr("javascript:alert(1)")}, "图片URL格式不正确"},
		{NoticeInput{Title: "t", Content: "c", Tags: []string{strings.Repeat("a", 300)}}, "标签总长度过长"},
	} {
		_, err := e.SaveNotice(ctx, 0, c.in)
		wantValidation(t, err, c.msg)
	}
	if _, err := e.SaveNotice(ctx, 999, NoticeInput{Title: "t", Content: "c"}); err != ErrNoticeNotFound {
		t.Fatalf("修改不存在的公告: %v", err)
	}

	var ids []int64
	for i := 1; i <= 7; i++ {
		n, err := e.SaveNotice(ctx, 0, NoticeInput{
			Title: fmt.Sprintf("公告%d", i), Content: "内容", Show: ptr(i != 4),
			ImgURL: ptr(" https://img.example.com/a.png "), Tags: []string{" 弹窗 ", "", "😀"},
		})
		if err != nil {
			t.Fatal(err)
		}
		ids = append(ids, n.ID)
	}
	var stored model.Notice
	e.DB.Take(&stored, ids[0])
	if *stored.ImgURL != "https://img.example.com/a.png" || strings.Join(stored.Tags, ",") != "弹窗,😀" {
		t.Fatalf("保存的图片或标签有误: %v %v", *stored.ImgURL, stored.Tags)
	}

	page, err := e.Notices(ctx, NoticePage{})
	if err != nil {
		t.Fatal(err)
	}
	list := page.List.([]model.Notice)
	if page.Total != 6 || len(list) != 5 || list[0].ID != ids[6] {
		t.Fatalf("默认分页应 5 条、按创建时间倒序: total=%d len=%d", page.Total, len(list))
	}
	for _, n := range list {
		if n.ID == ids[3] {
			t.Fatal("隐藏的公告不应出现")
		}
	}
	page, _ = e.Notices(ctx, NoticePage{Current: 2})
	if len(page.List.([]model.Notice)) != 1 {
		t.Fatal("第 2 页应只有 1 条")
	}
	page, _ = e.Notices(ctx, NoticePage{PageSize: 1000})
	if len(page.List.([]model.Notice)) != 6 {
		t.Fatal("page_size 超过上限时应按 100 处理")
	}

	if _, err := e.Notice(ctx, ids[3]); err != ErrNoticeNotFound {
		t.Fatalf("隐藏的公告详情应不存在: %v", err)
	}
	if err := e.ShowNotice(ctx, ids[3], true); err != nil {
		t.Fatal(err)
	}
	if err := e.ShowNotice(ctx, ids[3], true); err != nil {
		t.Fatalf("重复设置相同的值不应报不存在: %v", err)
	}
	if n, err := e.Notice(ctx, ids[3]); err != nil || n.Title != "公告4" {
		t.Fatalf("显示后应能查看: %v", err)
	}
	if err := e.ShowNotice(ctx, 999, true); err != ErrNoticeNotFound {
		t.Fatalf("不存在的公告: %v", err)
	}

	n, err := e.SaveNotice(ctx, ids[0], NoticeInput{Title: "改", Content: "新", ImgURL: ptr("")})
	if err != nil || n.ImgURL != nil || n.Tags != nil || n.Show != 1 {
		t.Fatalf("修改后图片、标签应清空，未传 show 时保持原值: %+v %v", n, err)
	}
	var updated model.Notice
	e.DB.Take(&updated, ids[0])
	var nullTags int64
	e.DB.Model(&model.Notice{}).Where("id = ? AND tags IS NULL AND img_url IS NULL", ids[0]).Count(&nullTags)
	if updated.Title != "改" || updated.ImgURL != nil || updated.Tags != nil || nullTags != 1 {
		t.Fatalf("修改未写入: %+v", updated)
	}

	all, _ := e.AdminNotices(ctx)
	if len(all) != 7 || all[0].ID != ids[6] {
		t.Fatal("管理端应返回全部公告并按 ID 倒序")
	}
	if err := e.DeleteNotice(ctx, ids[0]); err != nil {
		t.Fatal(err)
	}
	if err := e.DeleteNotice(ctx, ids[0]); err != ErrNoticeNotFound {
		t.Fatalf("重复删除: %v", err)
	}
}

func (e *env) article(t *testing.T, lang, cat, title, body string, show bool) int64 {
	t.Helper()
	k, err := e.SaveArticle(context.Background(), 0, ArticleInput{Category: cat, Language: lang, Title: title, Body: body, Show: &show})
	if err != nil {
		t.Fatal(err)
	}
	return k.ID
}

func testArticles(t *testing.T, e *env) {
	ctx := context.Background()
	for _, c := range []struct {
		in  ArticleInput
		msg string
	}{
		{ArticleInput{Language: "zh-CN", Title: "t", Body: "b"}, "分类不能为空"},
		{ArticleInput{Category: "c", Title: "t", Body: "b"}, "语言不能为空"},
		{ArticleInput{Category: "c", Language: "zh-CN-x", Title: "t", Body: "b"}, "语言格式不正确"},
		{ArticleInput{Category: "c", Language: "zh-CN", Body: "b"}, "标题不能为空"},
		{ArticleInput{Category: "c", Language: "zh-CN", Title: "t"}, "内容不能为空"},
	} {
		_, err := e.SaveArticle(ctx, 0, c.in)
		wantValidation(t, err, c.msg)
	}

	a := e.article(t, "zh-CN", "入门", "安装 Clash", "下载客户端", true)
	b := e.article(t, "zh-CN", "进阶", "规则 100%", "说明", true)
	c := e.article(t, "zh-CN", "入门", "导入订阅", "复制 CLASH 链接", true)
	e.article(t, "zh-CN", "入门", "隐藏文章", "clash", false)
	en := e.article(t, "en-US", "Start", "Install", "clash", true)

	// 未设置排序（NULL）的排在最前，各数据库一致
	if err := e.SortArticles(ctx, []int64{c, b}); err != nil {
		t.Fatal(err)
	}
	var before model.Knowledge
	e.DB.Take(&before, c)
	groups, err := e.Articles(ctx, "zh-CN", "")
	if err != nil {
		t.Fatal(err)
	}
	got := []string{}
	for _, g := range groups {
		for _, x := range g.Articles {
			got = append(got, fmt.Sprintf("%s/%d", g.Category, x.ID))
		}
	}
	want := []string{fmt.Sprintf("入门/%d", a), fmt.Sprintf("入门/%d", c), fmt.Sprintf("进阶/%d", b)}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("分组或排序有误: %v，期望 %v", got, want)
	}

	// 关键词不区分大小写，且 % 不作为通配符
	if groups, _ := e.Articles(ctx, "zh-CN", "clash"); len(groups) != 1 || len(groups[0].Articles) != 2 {
		t.Fatalf("关键词搜索应匹配标题与正文且不区分大小写: %+v", groups)
	}
	if groups, _ := e.Articles(ctx, "zh-CN", "100%"); len(groups) != 1 || groups[0].Articles[0].ID != b {
		t.Fatalf("%% 应按字面匹配: %+v", groups)
	}
	if groups, _ := e.Articles(ctx, "zh-CN", "%"); len(groups) != 1 {
		t.Fatalf("单独的 %% 只应匹配含 %% 的文章: %+v", groups)
	}
	if groups, _ := e.Articles(ctx, "", "clash"); len(groups) != 2 {
		t.Fatalf("不传语言时不按语言过滤: %+v", groups)
	}
	if groups, _ := e.Articles(ctx, "en-US", ""); len(groups) != 1 || groups[0].Articles[0].ID != en {
		t.Fatal("按语言过滤错误")
	}

	if err := e.SortArticles(ctx, []int64{c, b}); err != nil {
		t.Fatalf("排序不变时重复提交不应失败: %v", err)
	}
	var after model.Knowledge
	e.DB.Take(&after, c)
	if *after.Sort != 1 || after.UpdatedAt != before.UpdatedAt {
		t.Fatal("排序不应修改 updated_at")
	}
	if err := e.SortArticles(ctx, []int64{a, 999}); err != ErrArticleNotFound {
		t.Fatalf("包含不存在的文章: %v", err)
	}
	var untouched model.Knowledge
	e.DB.Take(&untouched, a)
	if untouched.Sort != nil {
		t.Fatal("排序失败时应整体回滚")
	}
	for _, ids := range [][]int64{nil, {a, a}, {0}} {
		if err := e.SortArticles(ctx, ids); err != errInvalid {
			t.Fatalf("非法排序参数 %v: %v", ids, err)
		}
	}

	cats, _ := e.Categories(ctx)
	if strings.Join(cats, ",") != "入门,进阶,Start" {
		t.Fatalf("分类: %v", cats)
	}
	list, _ := e.AdminArticles(ctx)
	if len(list) != 5 {
		t.Fatal("管理端应包含隐藏文章")
	}
	k, err := e.SaveArticle(ctx, a, ArticleInput{Category: "新", Language: "zh-CN", Title: "改", Body: "体"})
	if err != nil || k.Show != 1 || k.Title != "改" {
		t.Fatalf("修改文章: %+v %v", k, err)
	}
	if _, err := e.SaveArticle(ctx, 999, ArticleInput{Category: "c", Language: "zh-CN", Title: "t", Body: "b"}); err != ErrArticleNotFound {
		t.Fatalf("修改不存在的文章: %v", err)
	}
	if err := e.ShowArticle(ctx, a, false); err != nil {
		t.Fatal(err)
	}
	if d, err := e.AdminArticle(ctx, a); err != nil || d.Show != 0 {
		t.Fatal("管理端详情应包含隐藏文章")
	}
	if err := e.DeleteArticle(ctx, a); err != nil {
		t.Fatal(err)
	}
	if _, err := e.AdminArticle(ctx, a); err != ErrArticleNotFound {
		t.Fatalf("删除后: %v", err)
	}
}

func testArticle(t *testing.T, e *env) {
	ctx := context.Background()
	e.set(t, map[string]any{"app_name": "测试站", "show_subscribe_method": 0})
	u, primary := e.user(t)
	other, _ := e.user(t)
	second := &model.Subscription{
		UserID: u.ID, Name: "第二", UUID: "uuid-second", Token: "tokensecond", TransferEnable: 1 << 30,
		ExpiredAt: ptr(int64(testNow + 3600)),
	}
	if err := e.DB.Create(second).Error; err != nil {
		t.Fatal(err)
	}
	body := "{{siteName}}|{{subscribeUrl}}|{{urlEncodeSubscribeUrl}}|{{safeBase64SubscribeUrl}}|{{subscribeToken}}" +
		"<!--access start-->付费内容<!--access end-->"
	id := e.article(t, "zh-CN", "c", "t", body, true)
	hidden := e.article(t, "zh-CN", "c", "h", "x", false)

	expect := func(sub *model.Subscription, locked bool) string {
		link := "https://s.example.com/api/v1/client/subscribe?token=" + sub.Token
		safe := strings.NewReplacer("+", "-", "/", "_", "=", "").Replace(base64.StdEncoding.EncodeToString([]byte(link)))
		out := "测试站|" + link + "|" + phpx.URLEncode(link) + "|" + safe + "|" + sub.Token
		if locked {
			return out + `<div class="content-locked">T(You must have a valid subscription to view content in this area)</div>`
		}
		return out + "<!--access start-->付费内容<!--access end-->"
	}

	// 用户有可用订阅（第二个）：访问区块可见；未指定订阅时变量使用主订阅
	k, err := e.Article(ctx, u.ID, id, 0, "https://s.example.com", "zh-CN")
	if err != nil || k.Body != expect(primary, false) {
		t.Fatalf("主订阅变量: %v\n%s\n%s", err, k.Body, expect(primary, false))
	}
	k, _ = e.Article(ctx, u.ID, id, second.ID, "https://s.example.com", "zh-CN")
	if k.Body != expect(second, false) {
		t.Fatalf("指定订阅变量: %s", k.Body)
	}

	// 所有订阅都不可用时隐藏访问区块
	e.DB.Model(second).UpdateColumn("expired_at", testNow)
	k, _ = e.Article(ctx, u.ID, id, second.ID, "https://s.example.com", "zh-CN")
	if k.Body != expect(second, true) {
		t.Fatalf("无可用订阅时应隐藏: %s", k.Body)
	}

	if _, err := e.Article(ctx, other.ID, id, second.ID, "https://s.example.com", "zh-CN"); err != subscription.ErrNotFound {
		t.Fatalf("使用他人订阅应报订阅不存在: %v", err)
	}
	if _, err := e.Article(ctx, u.ID, hidden, 0, "https://s.example.com", "zh-CN"); err != ErrArticleNotFound {
		t.Fatalf("隐藏文章: %v", err)
	}
	if _, err := e.Article(ctx, u.ID, 999, 0, "https://s.example.com", "zh-CN"); err != ErrArticleNotFound {
		t.Fatalf("不存在的文章: %v", err)
	}
}

func testHTTP(t *testing.T, e *env) {
	ctx := context.Background()
	const secure = "contentadminpath123"
	e.set(t, map[string]any{"secure_path": secure})
	admin, _ := e.user(t)
	normal, _ := e.user(t)
	e.DB.Model(admin).Update("is_admin", 1)
	adminTok, _ := e.auth.Issue(ctx, admin.ID, "", "")
	userTok, _ := e.auth.Issue(ctx, normal.ID, "", "")

	cfg := &config.Config{Server: config.ServerConfig{Mode: config.ModeAll}, API: config.APIConfig{Prefix: "/api/t1"}}
	r, err := server.NewRouter(cfg, zap.NewNop())
	if err != nil {
		t.Fatal(err)
	}
	r.Mount(&Module{Svc: e.Service, Auth: e.auth})
	do := func(method, path, body, tok string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, path, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		if tok != "" {
			req.Header.Set("Authorization", tok)
		}
		w := httptest.NewRecorder()
		r.Engine.ServeHTTP(w, req)
		return w
	}
	notFound := do("GET", "/api/t1/definitely-not-exist", "", "")
	p := "/api/t1/" + secure
	for _, c := range []struct{ method, path, tok string }{
		{"GET", p + "/notices", userTok}, {"POST", p + "/knowledge", ""}, {"GET", "/api/t1/wrong/notices", adminTok},
	} {
		if w := do(c.method, c.path, "{}", c.tok); w.Code != http.StatusNotFound || w.Body.String() != notFound.Body.String() {
			t.Errorf("%s %s 应返回统一 404，实际 %d", c.method, c.path, w.Code)
		}
	}
	if w := do("GET", "/api/t1/user/notices", "", ""); w.Code != http.StatusUnauthorized {
		t.Fatalf("未登录访问用户端应 401: %d", w.Code)
	}

	if w := do("POST", p+"/notices", `{"title":"公告","content":"c","tags":["a"]}`, adminTok); w.Code != 200 {
		t.Fatalf("新建公告: %d %s", w.Code, w.Body)
	}
	var n model.Notice
	e.DB.Last(&n)
	if w := do("POST", fmt.Sprintf("%s/notices/%d/show", p, n.ID), `{}`, adminTok); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("缺少 show 应 422: %d", w.Code)
	}
	if w := do("POST", fmt.Sprintf("%s/notices/%d/show", p, n.ID), `{"show":true}`, adminTok); w.Code != 200 {
		t.Fatalf("显示公告: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/t1/user/notices?page_size=2", "", userTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"total":1`) {
		t.Fatalf("用户公告列表: %d %s", w.Code, w.Body)
	}
	if w := do("GET", fmt.Sprintf("/api/t1/user/notices/%d", n.ID), "", userTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"tags":["a"]`) {
		t.Fatalf("用户公告详情: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/t1/user/notices/999", "", userTok); w.Code != http.StatusNotFound {
		t.Fatalf("不存在的公告应 404: %d", w.Code)
	}

	if w := do("POST", p+"/knowledge", `{"category":"c","language":"zh-CN","title":"t","body":"{{subscribeToken}}","show":true}`, adminTok); w.Code != 200 {
		t.Fatalf("新建文章: %d %s", w.Code, w.Body)
	}
	var k model.Knowledge
	e.DB.Last(&k)
	if w := do("POST", p+"/knowledge/sort", fmt.Sprintf(`{"ids":[%d]}`, k.ID), adminTok); w.Code != 200 {
		t.Fatalf("排序: %d %s", w.Code, w.Body)
	}
	if w := do("GET", p+"/knowledge/categories", "", adminTok); w.Code != 200 || !strings.Contains(w.Body.String(), `["c"]`) {
		t.Fatalf("分类: %d %s", w.Code, w.Body)
	}
	if w := do("GET", "/api/t1/user/knowledge?language=zh-CN", "", userTok); w.Code != 200 || !strings.Contains(w.Body.String(), `"category":"c"`) {
		t.Fatalf("用户知识库列表: %d %s", w.Code, w.Body)
	}
	var primary model.Subscription
	e.DB.Where("user_id = ? AND is_primary = 1", normal.ID).Take(&primary)
	if w := do("GET", fmt.Sprintf("/api/t1/user/knowledge/%d", k.ID), "", userTok); w.Code != 200 || !strings.Contains(w.Body.String(), primary.Token) {
		t.Fatalf("用户文章详情: %d %s", w.Code, w.Body)
	}
	if w := do("GET", fmt.Sprintf("/api/t1/user/knowledge/%d?subscription_id=abc", k.ID), "", userTok); w.Code != http.StatusUnprocessableEntity {
		t.Fatalf("非法订阅 ID 应 422: %d", w.Code)
	}
	if w := do("DELETE", fmt.Sprintf("%s/knowledge/%d", p, k.ID), "", adminTok); w.Code != 200 {
		t.Fatalf("删除文章: %d", w.Code)
	}
}
