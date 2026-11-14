package legacy

import (
	"bytes"
	"crypto/sha1"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"

	"gvboard/internal/content"
	"gvboard/internal/httpx"
	"gvboard/internal/invite"
	"gvboard/internal/model"
	"gvboard/internal/phpx"
	"gvboard/internal/ticket"
)

// ---------- 邀请 ----------

func (m *Module) inviteSave(c *gin.Context) {
	_, err := m.Invites.Generate(c.Request.Context(), uid(c))
	m.respond(c, true, err)
}

// inviteFetch codes 为未使用的邀请码（完整字段），stat 为 [邀请人数, 累计佣金, 确认中的佣金, 佣金比例, 可用佣金]。
func (m *Module) inviteFetch(c *gin.Context) {
	ctx := c.Request.Context()
	ov, err := m.Invites.Fetch(ctx, uid(c))
	if err != nil {
		m.fail(c, err)
		return
	}
	codes := []model.InviteCode{}
	if err := m.DB.WithContext(ctx).Where("user_id = ? AND status = 0", uid(c)).Order("id").Find(&codes).Error; err != nil {
		m.fail(c, err)
		return
	}
	st := ov.Stat
	ok(c, gin.H{
		"codes": codes,
		"stat":  []any{st.Invitees, st.TotalCommission, st.PendingCommission, st.CommissionRate, st.CommissionBalance},
	})
}

func (m *Module) inviteDetails(c *gin.Context) {
	in := params(c)
	d, err := m.Invites.Details(c.Request.Context(), uid(c), invite.Page{
		Current: int(in.intOr("current", 1)), PageSize: int(in.intOr("page_size", 10)),
	})
	if err != nil {
		m.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": d.Data, "total": d.Total})
}

// ---------- 公告 ----------

// noticeFetch 带 id 时返回公告详情（不存在时 404），否则分页返回，参数 current、pageSize（默认 5，1~100）。
func (m *Module) noticeFetch(c *gin.Context) {
	in := params(c)
	ctx := c.Request.Context()
	if in.has("id") {
		id, _ := in.int("id")
		n, err := m.Content.Notice(ctx, id)
		var he *httpx.Error
		if errors.As(err, &he) && he.Status == http.StatusNotFound {
			c.AbortWithStatusJSON(http.StatusNotFound, gin.H{"message": "Notice not found"})
			return
		}
		m.respond(c, n, err)
		return
	}
	size := min(max(in.intOr("pageSize", 5), 1), 100)
	page, err := m.Content.Notices(ctx, content.NoticePage{Current: int(in.intOr("current", 1)), PageSize: int(size)})
	if err != nil {
		m.fail(c, err)
		return
	}
	c.JSON(http.StatusOK, gin.H{"data": page.List, "total": page.Total})
}

// ---------- 工单 ----------

// ticketDetail 工单详情，消息列表的键为 message（原项目）。
type ticketDetail struct {
	model.Ticket
	Message []ticket.Message `json:"message"`
}

func (m *Module) ticketFetch(c *gin.Context) {
	in := params(c)
	ctx := c.Request.Context()
	if in.truthy("id") {
		id, _ := in.int("id")
		d, err := m.Tickets.Get(ctx, uid(c), id)
		if err != nil {
			m.fail(c, err)
			return
		}
		ok(c, ticketDetail{Ticket: d.Ticket, Message: d.Messages})
		return
	}
	list, err := m.Tickets.List(ctx, uid(c))
	m.respond(c, list, err)
}

func (m *Module) ticketSave(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	v.required("subject", "Ticket subject cannot be empty")
	if v.required("level", "Ticket level cannot be empty") {
		s := in.str("level")
		v.check("level", s == "0" || s == "1" || s == "2", "Incorrect ticket level format")
	}
	v.required("message", "Message cannot be empty")
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	level := int(in.intOr("level", 0))
	_, err := m.Tickets.Create(c.Request.Context(), uid(c), ticket.CreateInput{
		Subject: in.str("subject"), Level: &level, Message: in.str("message"),
	}, c.ClientIP())
	m.respond(c, true, err)
}

func (m *Module) ticketReply(c *gin.Context) {
	in := params(c)
	if !in.truthy("id") {
		m.fail(c, abort("Invalid parameter"))
		return
	}
	if !in.truthy("message") {
		m.fail(c, abort("Message cannot be empty"))
		return
	}
	id, _ := in.int("id")
	m.respond(c, true, m.Tickets.Reply(c.Request.Context(), uid(c), id, in.str("message"), c.ClientIP()))
}

func (m *Module) ticketClose(c *gin.Context) {
	in := params(c)
	if !in.truthy("id") {
		m.fail(c, abort("Invalid parameter"))
		return
	}
	id, _ := in.int("id")
	m.respond(c, true, m.Tickets.Close(c.Request.Context(), uid(c), id))
}

func (m *Module) ticketWithdraw(c *gin.Context) {
	in := params(c)
	v := newValidator(in)
	v.required("withdraw_method", "The withdrawal method cannot be empty")
	v.required("withdraw_account", "The withdrawal account cannot be empty")
	if err := v.err(); err != nil {
		m.fail(c, err)
		return
	}
	_, err := m.Tickets.Withdraw(c.Request.Context(), uid(c), ticket.WithdrawInput{
		Method: in.str("withdraw_method"), Account: in.str("withdraw_account"),
	}, m.I18n.Locale(c), c.ClientIP())
	m.respond(c, true, err)
}

// ---------- 节点、Telegram、配置 ----------

// serverFetch 主订阅可用的节点（原项目 getAvailableServers 的完整字段），ETag 算法与原项目一致。
func (m *Module) serverFetch(c *gin.Context) {
	ctx := c.Request.Context()
	sub, err := m.primary(c)
	if err != nil {
		m.fail(c, err)
		return
	}
	servers := []*phpx.Arr{}
	if sub != nil {
		avail, err := m.Subscribe.Available(ctx, sub)
		if err != nil {
			m.fail(c, err)
			return
		}
		if avail {
			list, err := m.Subscribe.Servers(ctx, sub.GroupID)
			if err != nil {
				m.fail(c, err)
				return
			}
			servers = append(servers, list...)
		}
	}
	keys := phpx.New()
	for _, r := range servers {
		keys.Push(r.Get("cache_key"))
	}
	sum := sha1.Sum(phpx.JSONEncode(keys, 0))
	etag := hex.EncodeToString(sum[:])
	if strings.Contains(c.GetHeader("If-None-Match"), etag) {
		c.AbortWithStatus(http.StatusNotModified)
		return
	}
	c.Header("ETag", `"`+etag+`"`)
	ok(c, servers)
}

func (m *Module) botInfo(c *gin.Context) {
	me, err := m.Bot.BotInfo(c.Request.Context())
	if err != nil {
		m.fail(c, err)
		return
	}
	ok(c, gin.H{"username": me.Username})
}

func (m *Module) userConfig(c *gin.Context) {
	ok(c, gin.H{
		"is_telegram":                    m.flag("telegram_bot_enable"),
		"telegram_discuss_link":          m.setting("telegram_discuss_link"),
		"stripe_pk":                      m.setting("stripe_pk_live"),
		"withdraw_methods":               m.Settings.Strings("commission_withdraw_method"),
		"withdraw_close":                 m.flag("withdraw_close_enable"),
		"currency":                       m.Settings.String("currency"),
		"currency_symbol":                m.Settings.String("currency_symbol"),
		"commission_distribution_enable": m.flag("commission_distribution_enable"),
		"commission_distribution_l1":     m.setting("commission_distribution_l1"),
		"commission_distribution_l2":     m.setting("commission_distribution_l2"),
		"commission_distribution_l3":     m.setting("commission_distribution_l3"),
	})
}

func (m *Module) stripeKey(c *gin.Context) {
	key, err := m.Payments.StripePublicKey(c.Request.Context(), params(c).intOr("id", 0))
	m.respond(c, key, err)
}

// ---------- 知识库、流量 ----------

// knowledgeFetch 带 id 时返回文章（订阅变量按主订阅替换），否则返回按分类分组的文章列表 {分类: [文章]}。
func (m *Module) knowledgeFetch(c *gin.Context) {
	in := params(c)
	ctx := c.Request.Context()
	if in.truthy("id") {
		id, _ := in.int("id")
		k, err := m.Content.Article(ctx, uid(c), id, 0, baseURL(c), m.I18n.Locale(c))
		m.respond(c, k, err)
		return
	}
	cats, err := m.Content.Articles(ctx, in.str("language"), in.str("keyword"))
	if err != nil {
		m.fail(c, err)
		return
	}
	if len(cats) == 0 {
		ok(c, []any{})
		return
	}
	ok(c, groupedArticles(cats))
}

// groupedArticles 按分类出现顺序输出的 JSON 对象（Laravel 集合 groupBy 的结果）。
type groupedArticles []content.Category

func (g groupedArticles) MarshalJSON() ([]byte, error) {
	var b bytes.Buffer
	b.WriteByte('{')
	for i, cat := range g {
		if i > 0 {
			b.WriteByte(',')
		}
		k, err := json.Marshal(cat.Category)
		if err != nil {
			return nil, err
		}
		v, err := json.Marshal(cat.Articles)
		if err != nil {
			return nil, err
		}
		b.Write(k)
		b.WriteByte(':')
		b.Write(v)
	}
	b.WriteByte('}')
	return b.Bytes(), nil
}

// missingMethod 原项目路由存在但控制器没有对应方法，访问时返回内部错误。
func (m *Module) missingMethod(c *gin.Context) {
	c.AbortWithStatusJSON(http.StatusInternalServerError, gin.H{"message": m.tr(c, msgInternal, nil)})
}

// trafficLog 主订阅本月的流量明细。
func (m *Module) trafficLog(c *gin.Context) {
	id, err := m.primaryID(c)
	if err != nil {
		m.fail(c, err)
		return
	}
	if id == 0 {
		ok(c, []any{})
		return
	}
	list, err := m.Traffic.TrafficLogs(c.Request.Context(), uid(c), &id)
	m.respond(c, list, err)
}
