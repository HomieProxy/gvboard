// Package ticket 工单，对应原项目 User/Admin TicketController 与 TicketService。
package ticket

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/cache"
	"gvboard/internal/hook"
	"gvboard/internal/httpx"
	"gvboard/internal/mail"
	"gvboard/internal/model"
	"gvboard/internal/scheduler"
	"gvboard/internal/setting"
	"gvboard/internal/subscription"
)

var (
	errNotFound       = httpx.NotFoundErr("Ticket does not exist")
	errInvalid        = httpx.Validation("Invalid parameter")
	errHasOpen        = httpx.BadRequest("There are other unresolved tickets")
	errNeedOrder      = httpx.BadRequest("Please purchase a plan before opening a ticket")
	errForbidden      = httpx.BadRequest("Opening tickets is not allowed at present")
	errUnknownStatus  = httpx.BadRequest("Unknown ticket status")
	errEmptySubject   = httpx.Validation("Ticket subject cannot be empty")
	errLongSubject    = httpx.Validation("Ticket subject cannot exceed 255 characters")
	errLevel          = httpx.Validation("Incorrect ticket level format")
	errEmptyMessage   = httpx.Validation("Message cannot be empty")
	errLongMessage    = httpx.Validation("Message is too long")
	errClosed         = httpx.BadRequest("The ticket is closed and cannot be replied")
	errWaitReply      = httpx.BadRequest("Please wait for the technical enginneer to reply")
	errNoWithdraw     = httpx.BadRequest("Unsupported withdrawal")
	errWithdrawMethod = httpx.BadRequest("Unsupported withdrawal method")
	errEmptyMethod    = httpx.Validation("The withdrawal method cannot be empty")
	errEmptyAccount   = httpx.Validation("The withdrawal account cannot be empty")
	errLongAccount    = httpx.Validation("The withdrawal account is too long")
	errUserNotFound   = httpx.NotFoundErr("The user does not exist")
)

const (
	withdrawSubject = "[Commission Withdrawal Request] This ticket is opened by the system"
	maxMessage      = 10000            // text 列最多 65535 字节，按 4 字节字符留足余量
	notifyTTL       = 30 * time.Minute // 回复邮件通知间隔，与原项目一致
	autoCloseIdle   = 86400            // 已回复且 24 小时无新消息的工单自动关闭
)

// MailDispatcher 邮件投递（mail.Dispatcher 实现）。
type MailDispatcher interface {
	Dispatch(ctx context.Context, p mail.Payload) error
}

// EventKind 工单事件类型。
type EventKind string

const (
	EventCreated  EventKind = "created"  // 用户新建工单
	EventReplied  EventKind = "replied"  // 用户回复工单
	EventWithdraw EventKind = "withdraw" // 用户申请佣金提现
)

// Event 用户侧工单事件，供 Telegram 通知、插件订阅（单元 13、16）。
type Event struct {
	Kind    EventKind
	Ticket  model.Ticket
	Message string
	UserID  int64
	IP      string
}

// Service 工单服务。
type Service struct {
	DB            *gorm.DB
	Cache         *cache.Cache
	Settings      *setting.Service
	Subscriptions *subscription.Service
	Mail          MailDispatcher
	// Translate 按语言翻译文案（提现工单的标题与内容按用户语言写入），未设置时原样返回。
	Translate func(locale, key string, params map[string]any) string
	// OnEvent 用户新建、回复工单或申请提现后调用（事务已提交），未设置时忽略。
	OnEvent func(ctx context.Context, e Event)
	// OnAdminReply 管理员回复后调用（事务已提交），用于 Telegram 通知用户，未设置时忽略。
	OnAdminReply func(ctx context.Context, t model.Ticket, message string)
	// Hooks 插件事件（新建、回复工单），可为 nil。
	Hooks hook.Hooks
	Log   *zap.Logger
	now   func() int64
}

func (s *Service) nowUnix() int64 {
	if s.now != nil {
		return s.now()
	}
	return model.Now()
}

func (s *Service) log() *zap.Logger {
	if s.Log == nil {
		return zap.NewNop()
	}
	return s.Log
}

func (s *Service) tr(locale, key string, params map[string]any) string {
	if s.Translate == nil {
		return key
	}
	return s.Translate(locale, key, params)
}

func (s *Service) emit(ctx context.Context, e Event) {
	if s.OnEvent != nil {
		s.OnEvent(ctx, e)
	}
	name := hook.EventTicketCreated
	if e.Kind == EventReplied {
		name = hook.EventTicketReplied
	}
	hook.Emit(s.Hooks, ctx, name, hook.Ticket{
		TicketID: e.Ticket.ID, UserID: e.Ticket.UserID, Subject: e.Ticket.Subject, Kind: string(e.Kind), Message: e.Message,
	})
}

// Message 工单消息，IsMe 表示是否为当前视角一方发送。
type Message struct {
	model.TicketMessage
	IsMe bool `json:"is_me"`
}

// Detail 工单详情。
type Detail struct {
	model.Ticket
	Messages []Message `json:"messages"`
}

func (s *Service) messages(ctx context.Context, t *model.Ticket, admin bool) ([]Message, error) {
	var rows []model.TicketMessage
	if err := s.DB.WithContext(ctx).Where("ticket_id = ?", t.ID).Order("id").Find(&rows).Error; err != nil {
		return nil, err
	}
	out := make([]Message, len(rows))
	for i, m := range rows {
		own := m.UserID == t.UserID
		out[i] = Message{TicketMessage: m, IsMe: own != admin}
	}
	return out, nil
}

func (s *Service) find(ctx context.Context, db *gorm.DB, id, userID int64) (*model.Ticket, error) {
	q := db.WithContext(ctx).Where("id = ?", id)
	if userID > 0 {
		q = q.Where("user_id = ?", userID)
	}
	var t model.Ticket
	if err := q.Take(&t).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errNotFound
		}
		return nil, err
	}
	return &t, nil
}

// ---------- 用户端 ----------

// List 用户的全部工单，按创建时间倒序。
func (s *Service) List(ctx context.Context, userID int64) ([]model.Ticket, error) {
	list := []model.Ticket{}
	err := s.DB.WithContext(ctx).Where("user_id = ?", userID).
		Order("created_at DESC").Order("id DESC").Find(&list).Error
	return list, err
}

// Get 用户的工单详情。
func (s *Service) Get(ctx context.Context, userID, id int64) (*Detail, error) {
	t, err := s.find(ctx, s.DB, id, userID)
	if err != nil {
		return nil, err
	}
	msgs, err := s.messages(ctx, t, false)
	return &Detail{Ticket: *t, Messages: msgs}, err
}

// CreateInput 新建工单参数。
type CreateInput struct {
	Subject        string `json:"subject"`
	Level          *int   `json:"level"`
	Message        string `json:"message"`
	SubscriptionID *int64 `json:"subscription_id"`
}

func checkMessage(m string) (string, error) {
	m = strings.TrimSpace(m)
	if m == "" {
		return "", errEmptyMessage
	}
	if utf8.RuneCountInString(m) > maxMessage {
		return "", errLongMessage
	}
	return m, nil
}

func (in *CreateInput) validate() error {
	in.Subject = strings.TrimSpace(in.Subject)
	if in.Subject == "" {
		return errEmptySubject
	}
	if utf8.RuneCountInString(in.Subject) > 255 {
		return errLongSubject
	}
	if in.Level == nil {
		return httpx.Validation("Ticket level cannot be empty")
	}
	if *in.Level < 0 || *in.Level > 2 {
		return errLevel
	}
	if in.SubscriptionID != nil && *in.SubscriptionID <= 0 {
		return errInvalid
	}
	var err error
	in.Message, err = checkMessage(in.Message)
	return err
}

// lockUser 锁定用户行，串行化同一用户的工单创建与回复。
func lockUser(tx *gorm.DB, userID int64) error {
	var u model.User
	err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Take(&u, userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return errUserNotFound
	}
	return err
}

// Create 新建工单。同一用户只能有一个未关闭的工单；ticket_status 为 1 时仅限有已完成订单的用户，为 2 时禁止新建。
func (s *Service) Create(ctx context.Context, userID int64, in CreateInput, ip string) (*model.Ticket, error) {
	if err := in.validate(); err != nil {
		return nil, err
	}
	t := model.Ticket{UserID: userID, Subject: in.Subject, Level: *in.Level, SubscriptionID: in.SubscriptionID}
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := lockUser(tx, userID); err != nil {
			return err
		}
		var open int64
		if err := tx.Model(&model.Ticket{}).Where("user_id = ? AND status = ?", userID, model.TicketStatusOpen).
			Count(&open).Error; err != nil {
			return err
		}
		if open > 0 {
			return errHasOpen
		}
		switch s.Settings.Int("ticket_status") {
		case 0:
		case 1:
			var paid int64
			if err := tx.Model(&model.Order{}).Where("user_id = ? AND status IN ?", userID,
				[]int{model.OrderStatusCompleted, model.OrderStatusDiscounted}).Limit(1).Count(&paid).Error; err != nil {
				return err
			}
			if paid == 0 {
				return errNeedOrder
			}
		case 2:
			return errForbidden
		default:
			return errUnknownStatus
		}
		if in.SubscriptionID != nil {
			if _, err := s.Subscriptions.Find(ctx, tx, userID, *in.SubscriptionID); err != nil {
				return err
			}
		}
		if err := tx.Create(&t).Error; err != nil {
			return err
		}
		return tx.Create(&model.TicketMessage{UserID: userID, TicketID: t.ID, Message: in.Message}).Error
	})
	if err != nil {
		return nil, err
	}
	s.emit(ctx, Event{Kind: EventCreated, Ticket: t, Message: in.Message, UserID: userID, IP: ip})
	return &t, nil
}

// Reply 用户回复。工单已关闭或最后一条消息是自己发送的（等待客服回复）时不能回复。
func (s *Service) Reply(ctx context.Context, userID, id int64, message, ip string) error {
	message, err := checkMessage(message)
	if err != nil {
		return err
	}
	var t *model.Ticket
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		if t, err = s.find(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), id, userID); err != nil {
			return err
		}
		if t.Status != model.TicketStatusOpen {
			return errClosed
		}
		var last model.TicketMessage
		err = tx.Where("ticket_id = ?", t.ID).Order("id DESC").Limit(1).Find(&last).Error
		if err != nil {
			return err
		}
		if last.ID > 0 && last.UserID == userID {
			return errWaitReply
		}
		return s.addMessage(tx, t, userID, message, false)
	})
	if err != nil {
		return err
	}
	s.emit(ctx, Event{Kind: EventReplied, Ticket: *t, Message: message, UserID: userID, IP: ip})
	return nil
}

// addMessage 写入消息并更新回复状态：发送者不是工单用户时为已回复。reopen 为 true 时同时重新打开工单。
func (s *Service) addMessage(tx *gorm.DB, t *model.Ticket, senderID int64, message string, reopen bool) error {
	if err := tx.Create(&model.TicketMessage{UserID: senderID, TicketID: t.ID, Message: message}).Error; err != nil {
		return err
	}
	t.ReplyStatus = model.TicketReplyPending
	if senderID != t.UserID {
		t.ReplyStatus = model.TicketReplied
	}
	if reopen {
		t.Status = model.TicketStatusOpen
	}
	t.UpdatedAt = s.nowUnix()
	return tx.Model(t).UpdateColumns(map[string]any{
		"status": t.Status, "reply_status": t.ReplyStatus, "updated_at": t.UpdatedAt,
	}).Error
}

// Close 关闭工单。userID 为 0 时不限制所属用户（管理端）。
func (s *Service) Close(ctx context.Context, userID, id int64) error {
	t, err := s.find(ctx, s.DB, id, userID)
	if err != nil {
		return err
	}
	return s.DB.WithContext(ctx).Model(t).Updates(map[string]any{"status": model.TicketStatusClosed}).Error
}

// WithdrawInput 佣金提现申请参数。
type WithdrawInput struct {
	Method  string `json:"withdraw_method"`
	Account string `json:"withdraw_account"`
}

// Withdraw 佣金提现申请：以系统名义为用户开一个高优先级工单，内容按用户语言写入。
func (s *Service) Withdraw(ctx context.Context, userID int64, in WithdrawInput, locale, ip string) (*model.Ticket, error) {
	in.Method, in.Account = strings.TrimSpace(in.Method), strings.TrimSpace(in.Account)
	switch {
	case in.Method == "":
		return nil, errEmptyMethod
	case in.Account == "":
		return nil, errEmptyAccount
	case utf8.RuneCountInString(in.Account) > 255:
		return nil, errLongAccount
	}
	if s.Settings.Bool("withdraw_close_enable") {
		return nil, errNoWithdraw
	}
	if !slices.Contains(s.Settings.Strings("commission_withdraw_method"), in.Method) {
		return nil, errWithdrawMethod
	}
	var u model.User
	err := s.DB.WithContext(ctx).Select("id", "commission_balance").Take(&u, userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errUserNotFound
	} else if err != nil {
		return nil, err
	}
	if limit := s.Settings.Float("commission_withdraw_limit"); limit > float64(u.CommissionBalance)/100 {
		e := httpx.BadRequest("The current required minimum withdrawal commission is :limit")
		e.Params = map[string]any{"limit": strconv.FormatFloat(limit, 'f', -1, 64)}
		return nil, e
	}
	t := model.Ticket{UserID: userID, Subject: s.tr(locale, withdrawSubject, nil), Level: 2}
	message := s.tr(locale, "Withdrawal method", nil) + "：" + in.Method + "\r\n" +
		s.tr(locale, "Withdrawal account", nil) + "：" + in.Account
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Create(&t).Error; err != nil {
			return err
		}
		return tx.Create(&model.TicketMessage{UserID: userID, TicketID: t.ID, Message: message}).Error
	})
	if err != nil {
		return nil, err
	}
	s.emit(ctx, Event{Kind: EventWithdraw, Ticket: t, Message: message, UserID: userID, IP: ip})
	return &t, nil
}

// ---------- 管理端 ----------

// AdminQuery 管理端工单筛选。
type AdminQuery struct {
	Status      *int   `form:"status"`
	ReplyStatus []int  `form:"reply_status"`
	Email       string `form:"email"`
	Current     int    `form:"current"`
	PageSize    int    `form:"page_size"`
}

// AdminTicket 管理端工单，附带用户邮箱与关联订阅名称。
type AdminTicket struct {
	model.Ticket
	Email            string  `json:"email"`
	SubscriptionName *string `json:"subscription_name"`
}

// AdminDetail 管理端工单详情。
type AdminDetail struct {
	AdminTicket
	Messages []Message `json:"messages"`
}

func (s *Service) decorate(ctx context.Context, list []model.Ticket) ([]AdminTicket, error) {
	var userIDs, subIDs []int64
	for _, t := range list {
		userIDs = append(userIDs, t.UserID)
		if t.SubscriptionID != nil {
			subIDs = append(subIDs, *t.SubscriptionID)
		}
	}
	db := s.DB.WithContext(ctx)
	emails := map[int64]string{}
	if len(userIDs) > 0 {
		var users []model.User
		if err := db.Select("id", "email").Where("id IN ?", userIDs).Find(&users).Error; err != nil {
			return nil, err
		}
		for _, u := range users {
			emails[u.ID] = u.Email
		}
	}
	names := map[int64]string{}
	if len(subIDs) > 0 {
		var subs []model.Subscription
		if err := db.Select("id", "name").Where("id IN ?", subIDs).Find(&subs).Error; err != nil {
			return nil, err
		}
		for _, sub := range subs {
			names[sub.ID] = sub.Name
		}
	}
	out := make([]AdminTicket, len(list))
	for i, t := range list {
		out[i] = AdminTicket{Ticket: t, Email: emails[t.UserID]}
		if t.SubscriptionID != nil {
			if n, ok := names[*t.SubscriptionID]; ok {
				out[i].SubscriptionName = &n
			}
		}
	}
	return out, nil
}

// AdminList 管理端工单列表，按更新时间倒序。email 精确匹配（不区分大小写），用户不存在时返回空列表。
func (s *Service) AdminList(ctx context.Context, q AdminQuery) (*httpx.Page, error) {
	cur, size := q.Current, q.PageSize
	if cur < 1 {
		cur = 1
	}
	size = min(max(size, 10), 100)
	db := s.DB.WithContext(ctx).Model(&model.Ticket{})
	if q.Status != nil {
		db = db.Where("status = ?", *q.Status)
	}
	if len(q.ReplyStatus) > 0 {
		db = db.Where("reply_status IN ?", q.ReplyStatus)
	}
	if email := strings.TrimSpace(q.Email); email != "" {
		db = db.Where("user_id IN (?)", s.DB.WithContext(ctx).Model(&model.User{}).Select("id").
			Where("LOWER(email) = ?", strings.ToLower(email)))
	}
	db = db.Session(&gorm.Session{})
	var total int64
	if err := db.Count(&total).Error; err != nil {
		return nil, err
	}
	var list []model.Ticket
	if err := db.Order("updated_at DESC").Order("id DESC").Offset((cur - 1) * size).Limit(size).Find(&list).Error; err != nil {
		return nil, err
	}
	out, err := s.decorate(ctx, list)
	return &httpx.Page{Total: total, List: out}, err
}

// AdminGet 管理端工单详情，IsMe 表示管理员（非工单用户）发送。
func (s *Service) AdminGet(ctx context.Context, id int64) (*AdminDetail, error) {
	t, err := s.find(ctx, s.DB, id, 0)
	if err != nil {
		return nil, err
	}
	views, err := s.decorate(ctx, []model.Ticket{*t})
	if err != nil {
		return nil, err
	}
	msgs, err := s.messages(ctx, t, true)
	return &AdminDetail{AdminTicket: views[0], Messages: msgs}, err
}

// ReplyByAdmin 管理员回复：重新打开工单，并按 30 分钟间隔给用户发邮件通知。也供 Telegram 回复使用。
func (s *Service) ReplyByAdmin(ctx context.Context, adminID, id int64, message string) error {
	message, err := checkMessage(message)
	if err != nil {
		return err
	}
	var t *model.Ticket
	err = s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var err error
		if t, err = s.find(ctx, tx.Clauses(clause.Locking{Strength: "UPDATE"}), id, 0); err != nil {
			return err
		}
		return s.addMessage(tx, t, adminID, message, true)
	})
	if err != nil {
		return err
	}
	s.notify(ctx, t, message)
	if s.OnAdminReply != nil && adminID != t.UserID {
		s.OnAdminReply(ctx, *t, message)
	}
	hook.Emit(s.Hooks, ctx, hook.EventTicketReplied, hook.Ticket{
		TicketID: t.ID, UserID: t.UserID, Subject: t.Subject, Kind: string(EventReplied), Message: message, ByAdmin: adminID != t.UserID,
	})
	return nil
}

func (s *Service) notify(ctx context.Context, t *model.Ticket, message string) {
	if s.Mail == nil || s.Cache == nil {
		return
	}
	var u model.User
	if err := s.DB.WithContext(ctx).Select("id", "email").Take(&u, t.UserID).Error; err != nil {
		s.log().Warn("工单通知读取用户失败", zap.Int64("ticket", t.ID), zap.Error(err))
		return
	}
	key := s.Cache.KeyOf(cache.KeyTicketNotify, t.UserID)
	ok, err := s.Cache.SetNX(ctx, key, 1, notifyTTL).Result()
	if err != nil || !ok {
		return
	}
	name := s.Settings.String("app_name")
	err = s.Mail.Dispatch(ctx, mail.Payload{
		Email:        u.Email,
		Subject:      "您在" + name + "的工单得到了回复",
		TemplateName: mail.TplNotify,
		TemplateValue: map[string]any{
			"name":    name,
			"url":     s.Settings.String("app_url"),
			"content": "主题：" + t.Subject + "\r\n回复内容：" + message,
		},
	})
	if err != nil {
		s.log().Warn("工单通知邮件投递失败", zap.Int64("ticket", t.ID), zap.Error(err))
	}
}

// AutoClose 关闭已回复且 24 小时没有新消息的工单（原项目 check:ticket）。
func (s *Service) AutoClose(ctx context.Context) error {
	now := s.nowUnix()
	return s.DB.WithContext(ctx).Model(&model.Ticket{}).
		Where("status = ? AND reply_status = ? AND updated_at <= ?",
			model.TicketStatusOpen, model.TicketReplied, now-autoCloseIdle).
		UpdateColumns(map[string]any{"status": model.TicketStatusClosed, "updated_at": now}).Error
}

// CheckJob 每分钟执行一次自动关闭。
func (s *Service) CheckJob() scheduler.Job {
	return scheduler.Job{Name: "check_ticket", Spec: "* * * * *", NoOverlap: true, Run: s.AutoClose}
}
