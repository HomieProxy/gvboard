package telegram

import (
	"context"
	"encoding/json"
	"math"
	"strconv"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"

	"gvboard/internal/audit"
	"gvboard/internal/cache"
	"gvboard/internal/i18n"
	"gvboard/internal/model"
	"gvboard/internal/queue"
	"gvboard/internal/ticket"
)

// TaskSendTelegram 发送 Telegram 消息任务（原项目 SendTelegramJob）。
const TaskSendTelegram = "send_telegram"

// maxContent 通知中用户输入内容的最大字符数（Telegram 单条消息上限 4096）。
const maxContent = 3000

// NotifyKind 用户通知类型，对应偏好开关。
type NotifyKind string

const (
	NotifyOrder  NotifyKind = "notify_order"  // 订单支付成功
	NotifyTicket NotifyKind = "notify_ticket" // 工单有新回复
	NotifyRemind NotifyKind = "notify_remind" // 到期、流量提醒
)

// RegisterTask 注册发送任务。
func RegisterTask(q *queue.Queue, s *Service) {
	q.Register(queue.Spec{Type: TaskSendTelegram, Queue: queue.QueueSendTelegram, Tries: 3, Timeout: 30 * time.Second},
		func(ctx context.Context, raw []byte) error {
			var m OutMessage
			if err := json.Unmarshal(raw, &m); err != nil {
				s.log().Error("Telegram 任务参数无效", zap.Error(err))
				return nil
			}
			return s.deliver(ctx, m)
		})
}

// deliver 发送消息。用户屏蔽机器人（403）或消息无效（400）不重试。
func (s *Service) deliver(ctx context.Context, m OutMessage) error {
	if !s.Enabled() {
		return nil
	}
	cl, err := s.Client()
	if err != nil {
		return nil
	}
	err = cl.SendMessage(ctx, m)
	if IsAPIError(err, 400, 403) {
		s.log().Warn("Telegram 消息发送失败", zap.Error(err))
		return nil
	}
	return err
}

// Send 投递一条 HTML 消息到发送队列；未配置队列时直接发送。
func (s *Service) Send(ctx context.Context, chatID int64, text string) {
	s.sendThread(ctx, chatID, 0, text)
}

func (s *Service) sendThread(ctx context.Context, chatID, threadID int64, text string) {
	m := OutMessage{ChatID: chatID, MessageThreadID: threadID, Text: text, ParseMode: "HTML", Preview: &PreviewOptions{IsDisabled: true}}
	var err error
	if s.Queue == nil {
		err = s.deliver(ctx, m)
	} else {
		err = s.Queue.Enqueue(ctx, TaskSendTelegram, m, "")
	}
	if err != nil {
		s.log().Error("投递 Telegram 消息失败", zap.Error(err))
	}
}

// 管理员通知类别，各对应话题群组中的一个话题（决策 D72）。
const (
	TopicOrder  = "order"  // 收款
	TopicTicket = "ticket" // 工单、提现申请
	TopicAlert  = "alert"  // 节点掉线、流量重置失败、已取消订单收到付款等需要处理的告警
	TopicDaily  = "daily"  // 每日统计
)

// Topics 全部管理员通知类别。
var Topics = []string{TopicOrder, TopicTicket, TopicAlert, TopicDaily}

// AdminGroup 管理员话题群组 ID，未设置时返回 false。
func (s *Service) AdminGroup() (int64, bool) {
	id, err := strconv.ParseInt(s.Settings.String("telegram_admin_chat_id"), 10, 64)
	return id, err == nil && id != 0
}

// TopicID 通知类别对应的话题 ID，0 为 General 话题。
func (s *Service) TopicID(topic string) int64 {
	return s.Settings.Int("telegram_topic_" + topic)
}

// NotifyAdmins 通知管理员。设置了管理员话题群组时只发到群组中 topic 对应的话题（决策 D72，语言为站点默认语言）；
// 否则私聊已绑定 Telegram 的管理员，staff 为 true 时同时通知客服（原项目 sendMessageWithAdmin）。
// build 按接收者语言生成 HTML 文本。
func (s *Service) NotifyAdmins(ctx context.Context, topic string, staff bool, build func(locale string) string) {
	if !s.Enabled() {
		return
	}
	if chat, ok := s.AdminGroup(); ok {
		s.sendThread(ctx, chat, s.TopicID(topic), build(s.I18n.Fallback()))
		return
	}
	var rows []struct {
		TelegramID int64
		Language   *string
	}
	q := s.DB.WithContext(ctx).Table("v2_user AS u").
		Select("u.telegram_id, t.language").
		Joins("LEFT JOIN v2_user_telegram t ON t.user_id = u.id").
		Where("u.telegram_id IS NOT NULL")
	if staff {
		q = q.Where("(u.is_admin = 1 OR u.is_staff = 1)")
	} else {
		q = q.Where("u.is_admin = 1")
	}
	if err := q.Order("u.id").Scan(&rows).Error; err != nil {
		s.log().Error("查询管理员 Telegram 失败", zap.Error(err))
		return
	}
	for _, r := range rows {
		s.Send(ctx, r.TelegramID, build(s.userLocale(&model.UserTelegram{Language: r.Language})))
	}
}

// NotifyUser 按用户偏好通知单个用户，未绑定或已关闭该类通知时忽略。
func (s *Service) NotifyUser(ctx context.Context, userID int64, kind NotifyKind, build func(locale string) string) {
	if !s.Enabled() {
		return
	}
	var u model.User
	if err := s.DB.WithContext(ctx).Select("id", "telegram_id").Where("id = ?", userID).Take(&u).Error; err != nil || u.TelegramID == nil {
		return
	}
	p, err := s.prefs(ctx, userID)
	if err != nil {
		s.log().Error("读取 Telegram 偏好失败", zap.Error(err))
		return
	}
	on := map[NotifyKind]int{NotifyOrder: p.NotifyOrder, NotifyTicket: p.NotifyTicket, NotifyRemind: p.NotifyRemind}[kind]
	if on == 0 {
		return
	}
	s.Send(ctx, *u.TelegramID, build(s.userLocale(p)))
}

// OnPaid 支付回调确认收款：通知管理员（原项目）与下单用户（D51）。
func (s *Service) OnPaid(ctx context.Context, o *model.Order) {
	params := i18n.Params{"amount": money(o.TotalAmount), "trade_no": "<code>" + esc(o.TradeNo) + "</code>"}
	s.NotifyAdmins(ctx, TopicOrder, false, func(locale string) string { return s.T(locale, "telegram.paid_admin", params) })
	s.NotifyUser(ctx, o.UserID, NotifyOrder, func(locale string) string { return s.T(locale, "telegram.paid_user", params) })
}

// cancelledPaidTTL 同一订单的重复回调在该时间内只通知一次。
const cancelledPaidTTL = 24 * time.Hour

// OnCancelledPaid 已取消的订单收到支付回调：订单不做处理，发告警提醒管理员核实后补单（决策 D72）。
func (s *Service) OnCancelledPaid(ctx context.Context, o *model.Order, paymentName, callbackNo string) {
	if !s.Enabled() {
		return
	}
	ok, err := s.Cache.SetNX(ctx, s.Cache.KeyOf(cache.KeyCancelledPaidNotify, o.TradeNo), 1, cancelledPaidTTL).Result()
	if err != nil {
		s.log().Warn("已取消订单回调通知去重失败", zap.Error(err))
	} else if !ok {
		return
	}
	var u model.User
	s.DB.WithContext(ctx).Select("id", "email").Where("id = ?", o.UserID).Take(&u)
	amount := o.TotalAmount
	if o.HandlingAmount != nil {
		amount += *o.HandlingAmount
	}
	params := i18n.Params{
		"trade_no": "<code>" + esc(o.TradeNo) + "</code>", "email": "<code>" + esc(u.Email) + "</code>",
		"amount": money(amount), "payment": esc(paymentName), "callback_no": "<code>" + esc(callbackNo) + "</code>",
	}
	s.NotifyAdmins(ctx, TopicAlert, false, func(locale string) string { return s.T(locale, "telegram.cancelled_paid", params) })
}

// OnAuditBan 开启告警的审计规则封禁账号后通知管理员（决策 D81、D92）。
func (s *Service) OnAuditBan(ctx context.Context, b audit.BanInfo) {
	s.notifyAudit(ctx, "telegram.audit_ban", b)
}

// OnAuditAlert 开启告警的审计规则命中后通知管理员（决策 D92）。
func (s *Service) OnAuditAlert(ctx context.Context, b audit.BanInfo) {
	s.notifyAudit(ctx, "telegram.audit_alert", b)
}

func (s *Service) notifyAudit(ctx context.Context, key string, b audit.BanInfo) {
	if !s.Enabled() {
		return
	}
	params := i18n.Params{
		"email": "<code>" + esc(b.Email) + "</code>", "rule": esc(b.RuleName),
		"target": "<code>" + esc(b.Target) + "</code>", "server": esc(b.Server),
	}
	s.NotifyAdmins(ctx, TopicAlert, false, func(locale string) string {
		p := i18n.Params{"action": s.T(locale, "telegram.audit_action_"+b.Action, nil)}
		for k, v := range params {
			p[k] = v
		}
		return s.T(locale, key, p)
	})
}

// TestTopic 向管理员话题群组的 topic 话题直接发送一条测试消息，返回 Telegram 的错误（管理端配置检查用）。
func (s *Service) TestTopic(ctx context.Context, topic string) error {
	chat, ok := s.AdminGroup()
	if !ok {
		return errNoAdminGroup
	}
	cl, err := s.Client()
	if err != nil {
		return err
	}
	text := s.T(s.I18n.Fallback(), "telegram.topic_test", i18n.Params{"topic": s.T(s.I18n.Fallback(), "telegram.topic_"+topic, nil)})
	m := OutMessage{ChatID: chat, MessageThreadID: s.TopicID(topic), Text: text, ParseMode: "HTML", Preview: &PreviewOptions{IsDisabled: true}}
	if err := cl.SendMessage(ctx, m); err != nil {
		return errApply.With(map[string]any{"reason": err.Error()})
	}
	return nil
}

// OnTicket 用户新建、回复工单或申请提现时通知管理员与客服（原项目 TicketController::sendNotify）。
func (s *Service) OnTicket(ctx context.Context, e ticket.Event) {
	if !s.Enabled() {
		return
	}
	base := i18n.Params{
		"id":      e.Ticket.ID,
		"subject": "<code>" + esc(e.Ticket.Subject) + "</code>",
		"message": esc(truncate(e.Message, maxContent)),
	}
	if e.Kind == ticket.EventWithdraw {
		s.NotifyAdmins(ctx, TopicTicket, true, func(locale string) string { return s.T(locale, "telegram.ticket_short", base) })
		return
	}
	info, err := s.ticketUserInfo(ctx, e)
	if err != nil {
		s.log().Error("查询工单用户信息失败", zap.Error(err))
		return
	}
	s.NotifyAdmins(ctx, TopicTicket, true, func(locale string) string {
		p := i18n.Params{}
		for k, v := range base {
			p[k] = v
		}
		for k, v := range info {
			p[k] = v
		}
		if p["location"] == "" {
			p["location"] = s.T(locale, "Unknown location", nil)
		}
		if p["expired"] == "" {
			p["expired"] = s.T(locale, "Long-term", nil)
		}
		if p["plan"] == "" {
			p["plan"] = s.T(locale, "No plan", nil)
		}
		for _, k := range []string{"email", "location", "plan", "expired"} {
			p[k] = "<code>" + esc(p[k].(string)) + "</code>"
		}
		return s.T(locale, "telegram.ticket", p)
	})
}

// ticketUserInfo 工单用户信息。多订阅后流量、到期取工单关联的订阅，未关联时取主订阅。
func (s *Service) ticketUserInfo(ctx context.Context, e ticket.Event) (i18n.Params, error) {
	db := s.DB.WithContext(ctx)
	var u model.User
	if err := db.Where("id = ?", e.UserID).Take(&u).Error; err != nil {
		return nil, err
	}
	var subs []model.Subscription
	q := db.Where("user_id = ?", u.ID)
	if e.Ticket.SubscriptionID != nil {
		q = q.Where("id = ?", *e.Ticket.SubscriptionID)
	} else {
		q = q.Order("is_primary DESC").Order("id")
	}
	if err := q.Limit(1).Find(&subs).Error; err != nil {
		return nil, err
	}
	p := i18n.Params{
		"email": u.Email, "ip": esc(e.IP), "plan": "", "expired": "",
		"balance": money(u.Balance), "commission": money(u.CommissionBalance),
		"total": flow(0), "remain": flow(0), "upload": flow(0), "down": flow(0),
	}
	if s.Geo != nil && e.IP != "" {
		p["location"] = s.Geo.Locate(e.IP)
	} else {
		p["location"] = ""
	}
	if len(subs) == 1 {
		sub := subs[0]
		p["total"], p["remain"] = flow(sub.TransferEnable), flow(sub.TransferEnable-sub.U-sub.D)
		p["upload"], p["down"] = flow(sub.U), flow(sub.D)
		if sub.ExpiredAt != nil {
			p["expired"] = time.Unix(*sub.ExpiredAt, 0).Format(time.DateTime)
		}
		if sub.PlanID != nil {
			var plan model.Plan
			if err := db.Select("name").Where("id = ?", *sub.PlanID).Take(&plan).Error; err == nil {
				p["plan"] = plan.Name
			}
		}
	}
	return p, nil
}

// OnAdminReply 管理员回复工单后通知用户（D51）。
func (s *Service) OnAdminReply(ctx context.Context, t model.Ticket, message string) {
	s.NotifyUser(ctx, t.UserID, NotifyTicket, func(locale string) string {
		return s.T(locale, "telegram.ticket_reply", i18n.Params{
			"id": t.ID, "subject": "<code>" + esc(t.Subject) + "</code>", "message": esc(truncate(message, maxContent)),
		})
	})
}

// money 分转为元，格式与原项目 PHP 除法输出一致（10、10.5）。
func money(cents int64) string {
	return strconv.FormatFloat(float64(cents)/100, 'f', -1, 64)
}

// flow 原项目工单提醒的流量格式：不足 1GB 时以 MB 显示，保留两位小数。
func flow(b int64) string {
	g := float64(b) / (1 << 30)
	if g >= 1 {
		return strconv.FormatFloat(math.Round(g*100)/100, 'f', -1, 64) + "GB"
	}
	return strconv.FormatFloat(math.Round(float64(b)/(1<<20)*100)/100, 'f', -1, 64) + "MB"
}

func truncate(s string, n int) string {
	if utf8.RuneCountInString(s) <= n {
		return s
	}
	r := []rune(s)
	return string(r[:n]) + "…"
}
