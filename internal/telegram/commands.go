package telegram

import (
	"context"
	"errors"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"

	"gvboard/internal/cache"
	"gvboard/internal/httpx"
	"gvboard/internal/i18n"
	"gvboard/internal/model"
	"gvboard/internal/subscribe"
	"gvboard/internal/subscription"
	"gvboard/internal/util"
)

const bindLockTTL = 10 * time.Second

var (
	errBindArgs     = httpx.BadRequest("Invalid parameter, please send it with your subscription link")
	errBindURL      = httpx.BadRequest("Invalid subscription link")
	errBindExpired  = httpx.BadRequest("The bind link has expired, please get a new one on the website")
	errUserNotFound = httpx.NotFoundErr("The user does not exist")
	errAccountBound = httpx.BadRequest("This account is already bound to a Telegram account")
	errChatBound    = httpx.BadRequest("This Telegram account is already bound to another account, please send /unbind first")
	errNotBound     = httpx.BadRequest("No user information found, please bind your account first")
	errBusy         = httpx.TooMany("Too many requests, please try again later.")
	errReplyOnSite  = httpx.BadRequest("Please reply to the ticket on the website")
)

var ticketRef = regexp.MustCompile(`#(\d+)`)

func (s *Service) builtinCommands() []Command {
	return []Command{
		{Name: "start", Description: "Show the welcome message and help", Private: true, Handle: s.cmdStart},
		{Name: "menu", Description: "Open the main menu", Private: true, Handle: s.showMenu},
		{Name: "bind", Description: "Bind your account with the subscription link", Private: true, Handle: s.cmdBind},
		{Name: "unbind", Description: "Unbind your account", Private: true, Handle: s.cmdUnbind},
		{Name: "traffic", Description: "Check traffic usage", Private: true, Handle: s.cmdTraffic},
		{Name: "getlatesturl", Description: "Get the latest website URL", Handle: s.cmdLatestURL},
	}
}

// cmdStart 欢迎信息；带 bind_ 参数时为网站一键绑定（t.me/机器人?start=bind_xxx）。
func (s *Service) cmdStart(ctx context.Context, c *Context) error {
	if len(c.Args) > 0 && strings.HasPrefix(c.Args[0], "bind_") {
		return s.bindByCode(ctx, c, strings.TrimPrefix(c.Args[0], "bind_"))
	}
	return s.showMenu(ctx, c)
}

// cmdBind /bind 订阅链接（原项目方式）。不消耗一次性订阅 token。
func (s *Service) cmdBind(ctx context.Context, c *Context) error {
	if len(c.Args) == 0 {
		return errBindArgs
	}
	u, err := url.Parse(c.Args[0])
	if err != nil {
		return errBindURL
	}
	token := u.Query().Get("token")
	if token == "" {
		return errBindURL
	}
	sub, err := s.Subscribe.Lookup(ctx, token)
	if errors.Is(err, subscribe.ErrToken) {
		return errBindURL
	}
	if err != nil {
		return err
	}
	return s.bindAndReply(ctx, c, sub.UserID)
}

// bindByCode 网站生成的一次性绑定码。
func (s *Service) bindByCode(ctx context.Context, c *Context, code string) error {
	if code == "" {
		return errBindExpired
	}
	v, err := s.Cache.GetDel(ctx, s.Cache.KeyOf(cache.KeyTelegramBind, code)).Result()
	if errors.Is(err, redis.Nil) {
		return errBindExpired
	}
	if err != nil {
		return err
	}
	userID, err := strconv.ParseInt(v, 10, 64)
	if err != nil {
		return errBindExpired
	}
	return s.bindAndReply(ctx, c, userID)
}

func (s *Service) bindAndReply(ctx context.Context, c *Context, userID int64) error {
	if err := s.Bind(ctx, userID, c.From.ID, c.Locale); err != nil {
		return err
	}
	if err := c.Reply(ctx, esc(c.T("Bind successfully", nil)), nil); err != nil {
		return err
	}
	return s.showMenu(ctx, c)
}

// Bind 绑定：一个 Telegram 账号只能绑定一个站点账号，站点账号已绑定时不覆盖（与原项目一致）。
func (s *Service) Bind(ctx context.Context, userID, telegramID int64, locale string) error {
	l, err := s.Cache.TryLock(ctx, "telegram_bind:"+strconv.FormatInt(telegramID, 10), bindLockTTL)
	if errors.Is(err, cache.ErrLockHeld) {
		return errBusy
	}
	if err != nil {
		return err
	}
	defer func() {
		if err := l.Unlock(context.WithoutCancel(ctx)); err != nil {
			s.log().Warn("释放绑定锁失败", zap.Error(err))
		}
	}()
	cur, err := s.userByTelegram(ctx, telegramID)
	if err != nil {
		return err
	}
	if cur != nil {
		if cur.ID == userID {
			return nil
		}
		return errChatBound
	}
	res := s.DB.WithContext(ctx).Model(&model.User{}).
		Where("id = ? AND telegram_id IS NULL", userID).Update("telegram_id", telegramID)
	if res.Error != nil {
		return res.Error
	}
	if res.RowsAffected == 0 {
		var n int64
		if err := s.DB.WithContext(ctx).Model(&model.User{}).Where("id = ?", userID).Count(&n).Error; err != nil {
			return err
		}
		if n == 0 {
			return errUserNotFound
		}
		return errAccountBound
	}
	s.rememberLocale(ctx, userID, locale)
	return nil
}

func (s *Service) cmdUnbind(ctx context.Context, c *Context) error {
	u, err := s.userByTelegram(ctx, c.From.ID)
	if err != nil {
		return err
	}
	if u == nil {
		return errNotBound
	}
	if err := s.DB.WithContext(ctx).Model(&model.User{}).Where("id = ?", u.ID).Update("telegram_id", nil).Error; err != nil {
		return err
	}
	return c.Reply(ctx, esc(c.T("Unbind successfully", nil)), nil)
}

// cmdTraffic 列出所有订阅的流量（原项目只有单个订阅）。
func (s *Service) cmdTraffic(ctx context.Context, c *Context) error {
	u, err := s.userByTelegram(ctx, c.From.ID)
	if err != nil {
		return err
	}
	if u == nil {
		return errNotBound
	}
	var subs []model.Subscription
	if err := s.DB.WithContext(ctx).Where("user_id = ?", u.ID).
		Order("is_primary DESC").Order("id").Find(&subs).Error; err != nil {
		return err
	}
	var b strings.Builder
	b.WriteString("🚥 " + esc(c.T("Traffic usage", nil)) + "\n" + separator)
	for i := range subs {
		sub := &subs[i]
		b.WriteString("\n\n<b>" + esc(subName(sub)) + "</b>\n")
		b.WriteString(c.T("telegram.traffic", i18n.Params{
			"total":  "<code>" + util.TrafficConvert(sub.TransferEnable) + "</code>",
			"upload": "<code>" + util.TrafficConvert(sub.U) + "</code>",
			"down":   "<code>" + util.TrafficConvert(sub.D) + "</code>",
			"remain": "<code>" + util.TrafficConvert(sub.TransferEnable-sub.U-sub.D) + "</code>",
		}))
	}
	return c.Reply(ctx, b.String(), nil)
}

func subName(sub *model.Subscription) string {
	if sub.Name != "" {
		return sub.Name
	}
	return "#" + strconv.FormatInt(sub.ID, 10)
}

func (s *Service) cmdLatestURL(ctx context.Context, c *Context) error {
	return c.Reply(ctx, esc(c.T("The latest URL of :app_name is: :url", i18n.Params{
		"app_name": s.Settings.String("app_name"), "url": s.Settings.String("app_url"),
	})), nil)
}

// replyTicket 管理员或客服回复工单提醒消息即回复工单（原项目 ReplyTicket）。
func (s *Service) replyTicket(ctx context.Context, c *Context) error {
	if !c.Private || s.Tickets == nil {
		return nil
	}
	m := ticketRef.FindStringSubmatch(c.Msg.ReplyToMessage.Text)
	if m == nil {
		return nil
	}
	id, err := strconv.ParseInt(m[1], 10, 64)
	if err != nil {
		return nil
	}
	u, err := s.userByTelegram(ctx, c.From.ID)
	if err != nil {
		return err
	}
	if u == nil {
		return errUserNotFound
	}
	if u.IsAdmin == 0 && u.IsStaff == 0 {
		return errReplyOnSite
	}
	if err := s.Tickets.ReplyByAdmin(ctx, u.ID, id, c.Msg.Text); err != nil {
		return err
	}
	if err := c.Reply(ctx, c.T("Ticket #:id has been replied", i18n.Params{"id": "<code>" + m[1] + "</code>"}), nil); err != nil {
		s.log().Warn("回复 Telegram 消息失败", zap.Error(err))
	}
	s.NotifyAdmins(ctx, true, func(locale string) string {
		return s.T(locale, "Ticket #:id has been replied by :email", i18n.Params{
			"id": "<code>" + m[1] + "</code>", "email": esc(u.Email),
		})
	})
	return nil
}

// handleJoinRequest 入群申请：已绑定且有可用订阅的用户自动通过，其余拒绝（原项目 chat_join_request）。
func (s *Service) handleJoinRequest(ctx context.Context, r *ChatJoinRequest) {
	ok, err := s.memberAllowed(ctx, r.From.ID)
	if err != nil {
		s.log().Error("处理入群申请失败", zap.Error(err))
		return
	}
	cl, err := s.Client()
	if err != nil {
		return
	}
	if ok {
		err = cl.ApproveChatJoinRequest(ctx, r.Chat.ID, r.From.ID)
	} else {
		err = cl.DeclineChatJoinRequest(ctx, r.Chat.ID, r.From.ID)
	}
	if err != nil {
		s.log().Warn("处理入群申请失败", zap.Error(err))
	}
}

func (s *Service) memberAllowed(ctx context.Context, telegramID int64) (bool, error) {
	u, err := s.userByTelegram(ctx, telegramID)
	if err != nil || u == nil || u.Banned != 0 {
		return false, err
	}
	var subs []model.Subscription
	if err := s.DB.WithContext(ctx).Where("user_id = ?", u.ID).Find(&subs).Error; err != nil {
		return false, err
	}
	now := s.nowTime().Unix()
	for i := range subs {
		if subscription.Available(&subs[i], now) {
			return true, nil
		}
	}
	return false, nil
}
