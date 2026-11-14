package telegram

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"html"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/cache"
	"gvboard/internal/geoip"
	"gvboard/internal/httpx"
	"gvboard/internal/i18n"
	"gvboard/internal/invite"
	"gvboard/internal/model"
	"gvboard/internal/order"
	"gvboard/internal/payment"
	"gvboard/internal/queue"
	"gvboard/internal/setting"
	"gvboard/internal/subscribe"
	"gvboard/internal/subscription"
	"gvboard/internal/ticket"
)

const (
	defaultAPI  = "https://api.telegram.org"
	httpTimeout = 40 * time.Second // 需大于长轮询的 pollTimeout
	botInfoTTL  = time.Hour
	separator   = "———————————————"
)

var (
	errDisabled    = httpx.BadRequest("Telegram bot is not enabled")
	errUnavailable = httpx.BadRequest("Request failed, please try again later")
)

// PaymentMethods 已启用的支付方式（payment.Service 实现）。
type PaymentMethods interface {
	Methods(ctx context.Context) ([]payment.Method, error)
}

// Service Telegram 机器人服务。
type Service struct {
	DB        *gorm.DB
	Cache     *cache.Cache
	Settings  *setting.Service
	Queue     *queue.Queue
	I18n      *i18n.Translator
	Subscribe *subscribe.Service // /bind 解析订阅链接、判断订阅可用
	Tickets   *ticket.Service    // 管理员在 Telegram 中回复工单
	Geo       geoip.Locator      // 工单提醒中的用户位置，为空时显示未知
	APIPrefix string             // 原生 API 前缀，生成 Webhook 地址
	Log       *zap.Logger

	// 菜单功能（单元 13B）复用网站的业务服务，为 nil 时对应菜单不显示。
	Subscriptions *subscription.Service
	Orders        *order.Service
	Invites       *invite.Service
	Payments      PaymentMethods

	now func() time.Time

	once      sync.Once
	commands  map[string]Command
	order     []string
	callbacks map[string]Callback

	mu      sync.Mutex
	clients map[string]*http.Client
}

func (s *Service) nowTime() time.Time {
	if s.now != nil {
		return s.now()
	}
	return time.Now()
}

func (s *Service) log() *zap.Logger {
	if s.Log == nil {
		return zap.NewNop()
	}
	return s.Log
}

// Enabled 机器人已开启且配置了 token。
func (s *Service) Enabled() bool {
	return s.Settings.Bool("telegram_bot_enable") && s.Settings.String("telegram_bot_token") != ""
}

// Client 按当前设置创建 Bot API 客户端（设置修改后立即生效）。
func (s *Service) Client() (*Client, error) {
	token := s.Settings.String("telegram_bot_token")
	if token == "" {
		return nil, errDisabled
	}
	base := strings.TrimRight(s.Settings.String("telegram_api_url"), "/")
	if base == "" {
		base = defaultAPI
	}
	return &Client{Base: base, Token: token, HTTP: s.httpClient(s.Settings.String("telegram_proxy"))}, nil
}

// httpClient 按代理地址复用 HTTP 客户端。代理地址已在设置保存时校验。
func (s *Service) httpClient(proxy string) *http.Client {
	s.mu.Lock()
	defer s.mu.Unlock()
	if c, ok := s.clients[proxy]; ok {
		return c
	}
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.Proxy = nil
	if proxy != "" {
		if u, err := url.Parse(proxy); err == nil {
			tr.Proxy = http.ProxyURL(u)
		}
	}
	c := &http.Client{Timeout: httpTimeout, Transport: tr}
	if s.clients == nil {
		s.clients = map[string]*http.Client{}
	}
	s.clients[proxy] = c
	return c
}

// BotInfo 机器人信息（getMe），按 token 摘要缓存一小时。
func (s *Service) BotInfo(ctx context.Context) (*User, error) {
	c, err := s.Client()
	if err != nil {
		return nil, err
	}
	key := s.Cache.KeyOf(cache.KeyTelegramBot, tokenDigest(c.Token))
	var u User
	if ok, err := s.Cache.GetJSON(ctx, key, &u); err == nil && ok && u.Username != "" {
		return &u, nil
	}
	me, err := c.GetMe(ctx)
	if err != nil {
		return nil, err
	}
	if err := s.Cache.SetJSON(ctx, key, me, botInfoTTL); err != nil {
		s.log().Warn("缓存机器人信息失败", zap.Error(err))
	}
	return me, nil
}

// LocaleOf 由 Telegram 客户端语言确定回复语言：zh* 为简体中文，en* 为英文，其余使用站点默认语言。
func (s *Service) LocaleOf(code string) string {
	code = strings.ToLower(code)
	switch {
	case strings.HasPrefix(code, "zh"):
		return s.I18n.Resolve("zh-CN")
	case strings.HasPrefix(code, "en"):
		return s.I18n.Resolve("en-US")
	}
	return s.I18n.Fallback()
}

// T 翻译。
func (s *Service) T(locale, key string, params i18n.Params) string {
	return s.I18n.T(locale, key, params)
}

// userLocale 用户在机器人中最后使用的语言，未记录时为站点默认语言。
func (s *Service) userLocale(p *model.UserTelegram) string {
	if p != nil && p.Language != nil && *p.Language != "" {
		return s.I18n.Resolve(*p.Language)
	}
	return s.I18n.Fallback()
}

// prefs 读取通知偏好，未设置时返回默认值（全部开启）。
func (s *Service) prefs(ctx context.Context, userID int64) (*model.UserTelegram, error) {
	var p model.UserTelegram
	err := s.DB.WithContext(ctx).Where("user_id = ?", userID).Take(&p).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return &model.UserTelegram{UserID: userID, NotifyOrder: 1, NotifyTicket: 1, NotifyRemind: 1}, nil
	}
	if err != nil {
		return nil, err
	}
	return &p, nil
}

// rememberLocale 记录用户在机器人中的语言，用于之后的主动通知。
func (s *Service) rememberLocale(ctx context.Context, userID int64, locale string) {
	p, err := s.prefs(ctx, userID)
	if err != nil {
		s.log().Warn("读取 Telegram 偏好失败", zap.Error(err))
		return
	}
	if p.Language != nil && *p.Language == locale {
		return
	}
	if err := s.savePrefs(ctx, userID, map[string]any{"language": locale}); err != nil {
		s.log().Warn("保存 Telegram 语言失败", zap.Error(err))
	}
}

// savePrefs 修改偏好中的指定列，记录不存在时先以默认值创建。
func (s *Service) savePrefs(ctx context.Context, userID int64, fields map[string]any) error {
	db := s.DB.WithContext(ctx)
	var n int64
	if err := db.Model(&model.UserTelegram{}).Where("user_id = ?", userID).Count(&n).Error; err != nil {
		return err
	}
	if n == 0 {
		p := model.UserTelegram{UserID: userID, NotifyOrder: 1, NotifyTicket: 1, NotifyRemind: 1}
		if err := db.Create(&p).Error; err != nil {
			// 并发创建时唯一索引冲突，对方已创建则继续修改。
			if e := db.Model(&model.UserTelegram{}).Where("user_id = ?", userID).Count(&n).Error; e != nil || n == 0 {
				return err
			}
		}
	}
	return db.Model(&model.UserTelegram{}).Where("user_id = ?", userID).Updates(fields).Error
}

// userByTelegram 按 Telegram 用户 ID 查找已绑定的用户，未绑定返回 nil。
func (s *Service) userByTelegram(ctx context.Context, telegramID int64) (*model.User, error) {
	var u model.User
	err := s.DB.WithContext(ctx).Where("telegram_id = ?", telegramID).Order("id").Take(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &u, nil
}

// tokenDigest 区分不同机器人的缓存键，不暴露 token。
func tokenDigest(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:8])
}

// esc HTML 转义（消息使用 HTML 解析模式）。
func esc(s string) string { return html.EscapeString(s) }
