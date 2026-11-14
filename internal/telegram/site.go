package telegram

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/cache"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/util"
)

const bindCodeTTL = 10 * time.Minute

var errApply = httpx.BadRequest("Telegram request failed: :reason")

// Status 用户中心的 Telegram 信息。
type Status struct {
	Enabled     bool   `json:"enabled"`
	Username    string `json:"username"` // 机器人用户名，获取失败时为空
	Bound       bool   `json:"bound"`
	DiscussLink string `json:"discuss_link"`
	Prefs       Prefs  `json:"prefs"`
}

// Prefs 用户通知偏好。
type Prefs struct {
	NotifyOrder  bool `json:"notify_order"`
	NotifyTicket bool `json:"notify_ticket"`
	NotifyRemind bool `json:"notify_remind"`
}

// PrefsInput 修改通知偏好，未传的项不修改。
type PrefsInput struct {
	NotifyOrder  *bool `json:"notify_order"`
	NotifyTicket *bool `json:"notify_ticket"`
	NotifyRemind *bool `json:"notify_remind"`
}

// BindLink 网站一键绑定链接。
type BindLink struct {
	URL       string `json:"url"`
	ExpiresIn int    `json:"expires_in"` // 秒
}

// Status 用户的 Telegram 绑定状态与偏好。
func (s *Service) Status(ctx context.Context, userID int64) (*Status, error) {
	var u model.User
	err := s.DB.WithContext(ctx).Select("id", "telegram_id").Where("id = ?", userID).Take(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errUserNotFound
	}
	if err != nil {
		return nil, err
	}
	p, err := s.prefs(ctx, userID)
	if err != nil {
		return nil, err
	}
	st := &Status{
		Enabled: s.Enabled(), Bound: u.TelegramID != nil,
		DiscussLink: s.Settings.String("telegram_discuss_link"),
		Prefs:       Prefs{NotifyOrder: p.NotifyOrder != 0, NotifyTicket: p.NotifyTicket != 0, NotifyRemind: p.NotifyRemind != 0},
	}
	if st.Enabled {
		if me, err := s.BotInfo(ctx); err == nil {
			st.Username = me.Username
		} else {
			s.log().Warn("获取机器人信息失败", zap.Error(err))
		}
	}
	return st, nil
}

// NewBindLink 生成一键绑定链接（t.me 深链接，10 分钟内有效，使用一次即失效）。
func (s *Service) NewBindLink(ctx context.Context, userID int64) (*BindLink, error) {
	if !s.Enabled() {
		return nil, errDisabled
	}
	var u model.User
	if err := s.DB.WithContext(ctx).Select("id", "telegram_id").Where("id = ?", userID).Take(&u).Error; err != nil {
		return nil, err
	}
	if u.TelegramID != nil {
		return nil, errAccountBound
	}
	me, err := s.BotInfo(ctx)
	if err != nil {
		s.log().Warn("获取机器人信息失败", zap.Error(err))
		return nil, errUnavailable
	}
	code := util.RandomChar(24, false)
	if err := s.Cache.Set(ctx, s.Cache.KeyOf(cache.KeyTelegramBind, code), strconv.FormatInt(userID, 10), bindCodeTTL).Err(); err != nil {
		return nil, err
	}
	return &BindLink{URL: "https://t.me/" + me.Username + "?start=bind_" + code, ExpiresIn: int(bindCodeTTL.Seconds())}, nil
}

// UpdatePrefs 修改通知偏好。
func (s *Service) UpdatePrefs(ctx context.Context, userID int64, in PrefsInput) error {
	fields := map[string]any{}
	for col, v := range map[string]*bool{"notify_order": in.NotifyOrder, "notify_ticket": in.NotifyTicket, "notify_remind": in.NotifyRemind} {
		if v != nil {
			fields[col] = boolInt(*v)
		}
	}
	if len(fields) == 0 {
		return nil
	}
	return s.savePrefs(ctx, userID, fields)
}

func boolInt(b bool) int {
	if b {
		return 1
	}
	return 0
}

// ApplyResult 应用机器人设置的结果。
type ApplyResult struct {
	Mode       string `json:"mode"`
	Username   string `json:"username"`
	WebhookURL string `json:"webhook_url,omitempty"`
}

// Apply 按当前设置初始化机器人：校验 token、设置命令菜单，Webhook 模式下设置 Webhook（自动生成密钥），
// 长轮询模式下删除 Webhook（对应原项目 setTelegramWebhook）。baseURL 为接收 Webhook 的站点地址。
func (s *Service) Apply(ctx context.Context, baseURL string) (*ApplyResult, error) {
	cl, err := s.Client()
	if err != nil {
		return nil, err
	}
	fail := func(err error) error { return errApply.With(map[string]any{"reason": err.Error()}) }
	me, err := cl.GetMe(ctx)
	if err != nil {
		return nil, fail(err)
	}
	if err := s.Cache.SetJSON(ctx, s.Cache.KeyOf(cache.KeyTelegramBot, tokenDigest(cl.Token)), me, botInfoTTL); err != nil {
		s.log().Warn("缓存机器人信息失败", zap.Error(err))
	}
	if err := cl.SetMyCommands(ctx, s.MenuCommands(s.I18n.Fallback()), ""); err != nil {
		return nil, fail(err)
	}
	for lang, locale := range map[string]string{"zh": "zh-CN", "en": "en-US"} {
		if err := cl.SetMyCommands(ctx, s.MenuCommands(s.I18n.Resolve(locale)), lang); err != nil {
			return nil, fail(err)
		}
	}
	res := &ApplyResult{Mode: s.Settings.String("telegram_mode"), Username: me.Username}
	if res.Mode == "polling" {
		if err := cl.DeleteWebhook(ctx); err != nil {
			return nil, fail(err)
		}
		return res, nil
	}
	secret := s.Settings.String("telegram_webhook_secret")
	if secret == "" {
		secret = util.RandomChar(32, false)
		if err := s.Settings.Set(ctx, map[string]any{"telegram_webhook_secret": secret}); err != nil {
			return nil, err
		}
	}
	res.WebhookURL = strings.TrimRight(baseURL, "/") + s.APIPrefix + webhookPath + secret
	if err := cl.SetWebhook(ctx, res.WebhookURL, secret); err != nil {
		return nil, fail(err)
	}
	return res, nil
}

// Info Bot API 中的 Webhook 状态。
func (s *Service) Info(ctx context.Context) (*WebhookInfo, error) {
	cl, err := s.Client()
	if err != nil {
		return nil, err
	}
	w, err := cl.GetWebhookInfo(ctx)
	if err != nil {
		return nil, errApply.With(map[string]any{"reason": err.Error()})
	}
	return w, nil
}
