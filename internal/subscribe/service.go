// Package subscribe 订阅下发：按客户端生成各格式订阅内容（对应原项目 ClientController、App\Protocols）。
package subscribe

import (
	"context"
	"crypto/subtle"
	"errors"
	"strconv"
	"strings"
	"time"

	"github.com/redis/go-redis/v9"
	"go.uber.org/zap"
	"gorm.io/gorm"

	"gvboard/internal/cache"
	"gvboard/internal/hook"
	"gvboard/internal/model"
	"gvboard/internal/phpx"
	"gvboard/internal/setting"
	"gvboard/internal/subscription"
	"gvboard/internal/util"
)

// ErrToken token 缺失或无效，接口统一返回 404。
var ErrToken = errors.New("invalid subscribe token")

// Service 订阅下发服务。
type Service struct {
	DB       *gorm.DB
	Cache    *cache.Cache
	Settings *setting.Service
	Linker   *subscription.Linker
	Log      *zap.Logger
	Now      func() time.Time
	// Rand 端口随机数（测试时替换），为 nil 时使用 util.RandomInt。
	Rand func(lo, hi int64) int64
	// Protocols 客户端格式列表，为 nil 时使用已启用插件的格式 + 内置格式。
	Protocols []*Protocol
	// Hooks 插件过滤器（订阅节点列表与下发内容），可为 nil。
	Hooks hook.Hooks

	tpl   tplCache
	rules ruleCache
	logs  logBuffer
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) log() *zap.Logger {
	if s.Log != nil {
		return s.Log
	}
	return zap.NewNop()
}

func (s *Service) protocols() []*Protocol {
	if s.Protocols != nil {
		return s.Protocols
	}
	return append(extProtocols(), builtin()...)
}

// Resolve 按订阅链接显示方式校验 token 并找到订阅（对应原项目 Client 中间件）：
// 0 永久 token；1 一次性 token，使用后失效；2 时效 token，签名有效期内缓存。
func (s *Service) Resolve(ctx context.Context, token string) (*model.Subscription, error) {
	return s.resolve(ctx, token, true)
}

// Lookup 与 Resolve 相同，但不消耗一次性 token（原项目 Telegram /bind 的行为）。
func (s *Service) Lookup(ctx context.Context, token string) (*model.Subscription, error) {
	return s.resolve(ctx, token, false)
}

func (s *Service) resolve(ctx context.Context, token string, consume bool) (*model.Subscription, error) {
	if token == "" || token == "0" {
		return nil, ErrToken
	}
	switch s.Settings.Int("show_subscribe_method") {
	case subscription.LinkOneTime:
		key := s.Cache.KeyOf(cache.KeySubscribeOTPToken, token)
		var v string
		var err error
		if consume {
			v, err = s.Cache.GetDel(ctx, key).Result()
		} else {
			v, err = s.Cache.Get(ctx, key).Result()
		}
		if errors.Is(err, redis.Nil) {
			return nil, ErrToken
		}
		if err != nil {
			return nil, err
		}
		if consume {
			if err := s.Cache.Del(ctx, s.Cache.KeyOf(cache.KeySubscribeOTP, v)).Err(); err != nil {
				return nil, err
			}
		}
		token = v
	case subscription.LinkTOTP:
		v, err := s.totp(ctx, token)
		if err != nil {
			return nil, err
		}
		token = v
	}
	var sub model.Subscription
	err := s.DB.WithContext(ctx).Where("token = ?", token).Take(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, ErrToken
	}
	if err != nil {
		return nil, err
	}
	return &sub, nil
}

// totp 校验时效 token（base64url("订阅ID:签名")），返回订阅 token。
func (s *Service) totp(ctx context.Context, token string) (string, error) {
	key := s.Cache.KeyOf(cache.KeySubscribeTOTP, token)
	v, err := s.Cache.Get(ctx, key).Result()
	if err == nil && v != "" {
		return v, nil
	}
	if err != nil && !errors.Is(err, redis.Nil) {
		return "", err
	}
	raw, err := util.Base64DecodeURLSafe(token)
	if err != nil {
		return "", ErrToken
	}
	idStr, hash, ok := strings.Cut(string(raw), ":")
	if !ok || idStr == "" || idStr == "0" || hash == "" || hash == "0" {
		return "", ErrToken
	}
	id, err := strconv.ParseInt(idStr, 10, 64)
	if err != nil || id <= 0 {
		return "", ErrToken
	}
	var sub model.Subscription
	err = s.DB.WithContext(ctx).Select("token").Where("id = ?", id).Take(&sub).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return "", ErrToken
	}
	if err != nil {
		return "", err
	}
	expire := s.Settings.Int("show_subscribe_expire")
	want := subscription.TOTPHash(sub.Token, expire, s.now())
	if subtle.ConstantTimeCompare([]byte(hash), []byte(want)) != 1 {
		return "", ErrToken
	}
	step := time.Duration(subscription.TOTPStep(expire)) * time.Second
	if err := s.Cache.Set(ctx, key, sub.Token, step).Err(); err != nil {
		return "", err
	}
	return sub.Token, nil
}

// Available 订阅可用：订阅未封禁、有流量、未到期，且所属用户未被封禁（决策 D45）。
func (s *Service) Available(ctx context.Context, sub *model.Subscription) (bool, error) {
	if !subscription.Available(sub, s.now().Unix()) {
		return false, nil
	}
	var u model.User
	err := s.DB.WithContext(ctx).Select("banned").Where("id = ?", sub.UserID).Take(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return u.Banned == 0, nil
}

// Request 订阅请求信息。
type Request struct {
	// Flag 客户端标识：flag 参数，未传时为 User-Agent。
	Flag string
	// Host 请求的 Host，BaseURL 为协议 + Host。
	Host    string
	BaseURL string
}

// Subscribe 生成订阅内容（对应原项目 ClientController::subscribe）。订阅不可用时返回 200 空内容（决策 D45）。
func (s *Service) Subscribe(ctx context.Context, sub *model.Subscription, req Request) (*Response, error) {
	ok, err := s.Available(ctx, sub)
	if err != nil {
		return nil, err
	}
	if !ok {
		return text(""), nil
	}
	p, info := selectProtocol(s.protocols(), strings.ToLower(req.Flag))
	return s.render(ctx, sub, req, p, info)
}

// Deliver 带风控与请求日志的下发（决策 D46）：规则 → 频率限制 → 可用判断 → 防分享 → 生成内容。
// 返回 nil 表示应返回统一 404。app 为 true 时生成 App 内置 Clash 配置。
func (s *Service) Deliver(ctx context.Context, sub *model.Subscription, req Request, a Access, app bool) (*Response, error) {
	var p *Protocol
	var info bool
	client := clientApp
	if !app {
		p, info = selectProtocol(s.protocols(), strings.ToLower(req.Flag))
		client = p.Name
	}
	entry := s.newLogEntry(sub, a, client)
	v, hits := evaluate(s.activeRules(ctx), a, client)
	if v.action == "" && !v.allow && s.rateLimited(ctx, sub.ID, a.IP) {
		v.action, v.result = s.Settings.String("subscribe_rate_action"), model.SubscribeResultRateLimited
	}
	finish := func() {
		entry.Result, entry.RuleID = v.result, v.ruleID
		s.record(entry, hits)
	}
	if blocked, resp := blockResponse(v.action); blocked {
		finish()
		return resp, nil
	}
	ok, err := s.Available(ctx, sub)
	if err != nil {
		return nil, err
	}
	if !ok {
		v.result = model.SubscribeResultUnavailable
		finish()
		if app {
			return s.appRender(ctx, sub, false)
		}
		return text(""), nil
	}
	if !v.allow {
		if over, action := s.shareExceeded(ctx, sub.ID, a.IP); over {
			if blocked, resp := blockResponse(action); blocked {
				v.result = model.SubscribeResultShareBlock
				finish()
				return resp, nil
			}
			v.result = model.SubscribeResultShareWarn
		}
	}
	var resp *Response
	if app {
		resp, err = s.appRender(ctx, sub, true)
	} else {
		resp, err = s.render(ctx, sub, req, p, info)
	}
	if err != nil {
		return nil, err
	}
	finish()
	return resp, nil
}

// blockResponse 拦截动作对应的响应：empty 为 200 空内容，deny 为 nil（统一 404）。
func blockResponse(action string) (bool, *Response) {
	switch action {
	case model.RuleActionEmpty:
		return true, text("")
	case model.RuleActionDeny:
		return true, nil
	}
	return false, nil
}

func (s *Service) render(ctx context.Context, sub *model.Subscription, req Request, p *Protocol, info bool) (*Response, error) {
	servers, err := s.Servers(ctx, sub.GroupID)
	if err != nil {
		return nil, err
	}
	servers = s.filterServers(ctx, sub, p.Name, servers)
	if info && s.Settings.Bool("show_info_to_server_enable") && len(servers) > 0 {
		day, err := s.resetDay(ctx, sub)
		if err != nil {
			return nil, err
		}
		servers = addInfoServers(servers, sub, day)
	}
	if p.RemoteCert {
		markRemoteCert(servers)
	}
	resp, err := p.Handle(&Ctx{
		Context: ctx,
		svc:     s,
		Sub:     sub,
		Servers: servers,
		AppName: s.Settings.String("app_name"),
		AppURL:  s.Settings.String("app_url"),
		Host:    req.Host,
		BaseURL: req.BaseURL,
	})
	if err != nil {
		return nil, err
	}
	return s.filterContent(ctx, sub, p.Name, resp), nil
}

// filterServers 插件过滤器 subscribe.servers。
func (s *Service) filterServers(ctx context.Context, sub *model.Subscription, client string, servers []*phpx.Arr) []*phpx.Arr {
	if s.Hooks == nil {
		return servers
	}
	v := hook.Apply(s.Hooks, ctx, hook.FilterSubscribeServers, hook.SubscribeServers{
		UserID: sub.UserID, SubscriptionID: sub.ID, Client: client, Servers: servers,
	})
	return v.Servers
}

// filterContent 插件过滤器 subscribe.content。
func (s *Service) filterContent(ctx context.Context, sub *model.Subscription, client string, resp *Response) *Response {
	if s.Hooks == nil || resp == nil {
		return resp
	}
	v := hook.Apply(s.Hooks, ctx, hook.FilterSubscribeContent, hook.SubscribeContent{
		UserID: sub.UserID, SubscriptionID: sub.ID, Client: client,
		ContentType: resp.ContentType, Headers: resp.Headers, Body: resp.Body,
	})
	return &Response{ContentType: v.ContentType, Headers: v.Headers, Body: v.Body}
}

func (s *Service) resetDay(ctx context.Context, sub *model.Subscription) (*int, error) {
	var plan *model.Plan
	if sub.PlanID != nil {
		var p model.Plan
		err := s.DB.WithContext(ctx).Where("id = ?", *sub.PlanID).Take(&p).Error
		if err == nil {
			plan = &p
		} else if !errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, err
		}
	}
	return subscription.ResetDay(sub, plan, s.Settings.Int("reset_traffic_method"), s.now()), nil
}

// AppConfig App 内置 Clash 配置（原项目 AppController::getConfig）。订阅不可用时节点为空。
func (s *Service) AppConfig(ctx context.Context, sub *model.Subscription) (*Response, error) {
	ok, err := s.Available(ctx, sub)
	if err != nil {
		return nil, err
	}
	return s.appRender(ctx, sub, ok)
}

func (s *Service) appRender(ctx context.Context, sub *model.Subscription, available bool) (*Response, error) {
	var err error
	c := &Ctx{Context: ctx, svc: s, Sub: sub, AppName: s.Settings.String("app_name")}
	if available {
		if c.Servers, err = s.Servers(ctx, sub.GroupID); err != nil {
			return nil, err
		}
		c.Servers = s.filterServers(ctx, sub, clientApp, c.Servers)
	}
	resp, err := appConfig(c)
	if err != nil {
		return nil, err
	}
	return s.filterContent(ctx, sub, clientApp, resp), nil
}

// PlatformVersion 单个平台的 App 版本。
type PlatformVersion struct {
	Version     string `json:"version"`
	DownloadURL string `json:"download_url"`
}

// Versions 全部平台的 App 版本。
type Versions struct {
	WindowsVersion     string `json:"windows_version"`
	WindowsDownloadURL string `json:"windows_download_url"`
	MacosVersion       string `json:"macos_version"`
	MacosDownloadURL   string `json:"macos_download_url"`
	AndroidVersion     string `json:"android_version"`
	AndroidDownloadURL string `json:"android_download_url"`
}

// AppVersion App 版本信息（原项目 AppController::getVersion）：Tidalab / Tunnelab 4.0.0 客户端只返回对应平台。
func (s *Service) AppVersion(userAgent string) any {
	get := s.Settings.String
	if strings.Contains(userAgent, "tidalab/4.0.0") || strings.Contains(userAgent, "tunnelab/4.0.0") {
		if strings.Contains(userAgent, "Win64") {
			return PlatformVersion{get("windows_version"), get("windows_download_url")}
		}
		return PlatformVersion{get("macos_version"), get("macos_download_url")}
	}
	return Versions{
		get("windows_version"), get("windows_download_url"),
		get("macos_version"), get("macos_download_url"),
		get("android_version"), get("android_download_url"),
	}
}
