package telegram

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"go.uber.org/zap"

	"gvboard/internal/cache"
	"gvboard/internal/httpx"
	"gvboard/internal/i18n"
	"gvboard/internal/model"
)

const updateDedupTTL = 24 * time.Hour

// Command 机器人命令。
type Command struct {
	Name string
	// Description 命令菜单中的说明（翻译键），为空时不出现在命令菜单中。
	Description string
	// Private 只在私聊中响应（与原项目一致，群聊中静默忽略）。
	Private bool
	Handle  func(ctx context.Context, c *Context) error
}

// Callback 内联按钮动作。按钮数据格式为 "名称:参数1:参数2"，只在私聊中响应。
type Callback struct {
	Name string
	// Bound 为 true 时要求已绑定账号，未绑定时提示绑定。
	Bound  bool
	Handle func(ctx context.Context, c *Context) error
}

// Context 一次命令、回复或按钮动作的上下文。
type Context struct {
	Svc    *Service
	Msg    *Message
	From   *User
	ChatID int64
	// ThreadID 话题群组中消息所在的话题，回复发到同一话题。
	ThreadID int64
	Private  bool
	Locale   string
	Args     []string

	// CallbackID 按钮动作的回调 ID，MessageID 为按钮所在消息。
	CallbackID string
	MessageID  int64
	answered   bool

	user *model.User
}

// T 按当前用户语言翻译。
func (c *Context) T(key string, params i18n.Params) string { return c.Svc.T(c.Locale, key, params) }

func htmlMessage(chatID int64, text string, kb *InlineKeyboard) OutMessage {
	return OutMessage{ChatID: chatID, Text: text, ParseMode: "HTML", Preview: &PreviewOptions{IsDisabled: true}, ReplyMarkup: kb}
}

// Reply 发送一条新消息到当前会话（HTML 格式，关闭链接预览）。
func (c *Context) Reply(ctx context.Context, text string, kb *InlineKeyboard) error {
	cl, err := c.Svc.Client()
	if err != nil {
		return err
	}
	m := htmlMessage(c.ChatID, text, kb)
	m.MessageThreadID = c.ThreadID
	return cl.SendMessage(ctx, m)
}

// Show 按钮动作中原地修改按钮所在的消息（无法修改时发送新消息），其他情况发送新消息。
func (c *Context) Show(ctx context.Context, text string, kb *InlineKeyboard) error {
	if c.MessageID == 0 {
		return c.Reply(ctx, text, kb)
	}
	cl, err := c.Svc.Client()
	if err != nil {
		return err
	}
	err = cl.EditMessageText(ctx, c.ChatID, c.MessageID, htmlMessage(c.ChatID, text, kb))
	if err == nil || IsNotModified(err) {
		return nil
	}
	if IsAPIError(err, 400) {
		// 图片消息（支付二维码）等不能改为文本，改为发送新消息。
		return c.Reply(ctx, text, kb)
	}
	return err
}

// Answer 应答按钮回调（只应答一次），text 不为空时显示提示，alert 为 true 时为需要确认的弹窗。
func (c *Context) Answer(ctx context.Context, text string, alert bool) {
	if c.CallbackID == "" || c.answered {
		return
	}
	c.answered = true
	cl, err := c.Svc.Client()
	if err != nil {
		return
	}
	if err := cl.AnswerCallbackQuery(ctx, c.CallbackID, text, alert); err != nil {
		c.Svc.log().Warn("应答 Telegram 回调失败", zap.Error(err))
	}
}

// User 当前 Telegram 账号绑定的站点用户，未绑定时返回 errNotBound。
func (c *Context) User(ctx context.Context) (*model.User, error) {
	if c.user != nil {
		return c.user, nil
	}
	u, err := c.Svc.userByTelegram(ctx, c.From.ID)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, errNotBound
	}
	c.user = u
	return u, nil
}

// AddCommand 注册命令，同名覆盖（供插件扩展）。
func (s *Service) AddCommand(cmd Command) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	name := strings.ToLower(cmd.Name)
	if _, ok := s.commands[name]; !ok {
		s.order = append(s.order, name)
	}
	s.commands[name] = cmd
}

// AddCallback 注册按钮动作，同名覆盖（供插件扩展）。
func (s *Service) AddCallback(cb Callback) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	s.callbacks[cb.Name] = cb
}

func (s *Service) init() {
	s.once.Do(func() {
		s.commands = map[string]Command{}
		for _, c := range s.builtinCommands() {
			s.commands[c.Name] = c
			s.order = append(s.order, c.Name)
		}
		s.callbacks = map[string]Callback{}
		for _, cb := range s.builtinCallbacks() {
			s.callbacks[cb.Name] = cb
		}
	})
}

func (s *Service) command(name string) (Command, bool) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.commands[strings.ToLower(name)]
	return c, ok
}

func (s *Service) callback(name string) (Callback, bool) {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	c, ok := s.callbacks[name]
	return c, ok
}

// MenuCommands 命令菜单（setMyCommands）。
func (s *Service) MenuCommands(locale string) []BotCommand {
	s.init()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]BotCommand, 0, len(s.order))
	for _, name := range s.order {
		c := s.commands[name]
		if c.Description != "" {
			out = append(out, BotCommand{Command: c.Name, Description: s.T(locale, c.Description, nil)})
		}
	}
	return out
}

// HandleUpdate 处理一条更新（Webhook 与长轮询共用）。Telegram 重发的同一更新只处理一次。
func (s *Service) HandleUpdate(ctx context.Context, u Update) {
	if !s.Enabled() {
		return
	}
	if u.UpdateID > 0 {
		ok, err := s.Cache.SetNX(ctx, s.Cache.KeyOf(cache.KeyTelegramUpdate, u.UpdateID), 1, updateDedupTTL).Result()
		if err != nil {
			s.log().Warn("Telegram 更新去重失败", zap.Error(err))
		} else if !ok {
			return
		}
	}
	switch {
	case u.Message != nil:
		s.handleMessage(ctx, u.Message)
	case u.ChatJoinRequest != nil:
		s.handleJoinRequest(ctx, u.ChatJoinRequest)
	case u.CallbackQuery != nil:
		s.handleCallback(ctx, u.CallbackQuery)
	}
}

func (s *Service) handleMessage(ctx context.Context, m *Message) {
	if m.From == nil || m.From.IsBot || m.Text == "" {
		return
	}
	c := &Context{
		Svc: s, Msg: m, From: m.From, ChatID: m.Chat.ID,
		Private: m.Chat.Type == "private", Locale: s.LocaleOf(m.From.LanguageCode),
	}
	if m.IsTopicMessage {
		c.ThreadID = m.MessageThreadID
	}
	// 回复某条消息时只处理工单回复（与原项目一致）。
	if m.ReplyToMessage != nil && m.ReplyToMessage.Text != "" {
		s.finish(ctx, c, s.replyTicket(ctx, c))
		return
	}
	if !strings.HasPrefix(m.Text, "/") {
		if c.Private {
			s.finish(ctx, c, s.handleText(ctx, c))
		}
		return
	}
	fields := strings.Fields(m.Text)
	name := strings.TrimPrefix(fields[0], "/")
	if at := strings.IndexByte(name, '@'); at >= 0 {
		target := name[at+1:]
		name = name[:at]
		me, err := s.BotInfo(ctx)
		if err != nil {
			s.log().Warn("获取机器人信息失败", zap.Error(err))
			return
		}
		if !strings.EqualFold(target, me.Username) {
			return
		}
	}
	cmd, ok := s.command(name)
	if !ok {
		if c.Private {
			s.finish(ctx, c, httpx.BadRequest("Unknown command, send /start to see the available commands"))
		}
		return
	}
	if cmd.Private && !c.Private {
		return
	}
	c.Args = fields[1:]
	s.finish(ctx, c, cmd.Handle(ctx, c))
}

// handleCallback 内联按钮动作，只处理私聊中的按钮。
func (s *Service) handleCallback(ctx context.Context, q *CallbackQuery) {
	c := &Context{Svc: s, From: &q.From, CallbackID: q.ID, Locale: s.LocaleOf(q.From.LanguageCode)}
	defer c.Answer(ctx, "", false)
	if q.Message == nil || q.Message.Chat.Type != "private" {
		return
	}
	c.Msg, c.ChatID, c.Private, c.MessageID = q.Message, q.Message.Chat.ID, true, q.Message.MessageID
	parts := strings.Split(q.Data, ":")
	cb, ok := s.callback(parts[0])
	if !ok {
		return
	}
	c.Args = parts[1:]
	if cb.Bound {
		if _, err := c.User(ctx); err != nil {
			s.finish(ctx, c, err)
			return
		}
	}
	s.finish(ctx, c, cb.Handle(ctx, c))
}

// finish 把业务错误回复给用户（按钮动作中弹窗提示）；其他错误记录日志并回复通用提示。
func (s *Service) finish(ctx context.Context, c *Context, err error) {
	if err == nil {
		return
	}
	var he *httpx.Error
	text := c.T("Request failed, please try again later", nil)
	if errors.As(err, &he) && he.Status < http.StatusInternalServerError {
		text = c.T(he.Msg, he.Params)
	} else {
		s.log().Error("处理 Telegram 消息失败", zap.Error(err))
	}
	if c.CallbackID != "" && !c.answered {
		c.Answer(ctx, text, true)
		return
	}
	if err := c.Reply(ctx, esc(text), nil); err != nil {
		s.log().Warn("回复 Telegram 消息失败", zap.Error(err))
	}
}
