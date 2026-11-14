// Package passport 注册、登录、找回密码、邮箱验证码、快捷登录、邀请链接统计与公开站点配置，
// 对应原项目 Passport 与 Guest/Comm 控制器。
package passport

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/url"
	"strconv"
	"time"

	"github.com/redis/go-redis/v9"
	"gorm.io/gorm"

	"gvboard/internal/account"
	"gvboard/internal/auth"
	"gvboard/internal/cache"
	"gvboard/internal/captcha"
	"gvboard/internal/database"
	"gvboard/internal/hook"
	"gvboard/internal/httpx"
	"gvboard/internal/i18n"
	"gvboard/internal/mail"
	"gvboard/internal/model"
	"gvboard/internal/setting"
	"gvboard/internal/util"
)

// MailDispatcher 邮件投递（mail.Dispatcher 实现）。
type MailDispatcher interface {
	Dispatch(ctx context.Context, p mail.Payload) error
}

// Service 认证业务。
type Service struct {
	DB       *gorm.DB
	Cache    *cache.Cache
	Settings *setting.Service
	Auth     *auth.Service
	Mail     MailDispatcher
	Captcha  captcha.Verifier
	I18n     *i18n.Translator
	// Hooks 插件事件（注册、登录），可为 nil。
	Hooks hook.Hooks
	Now   func() time.Time
}

const (
	emailCodeTTL     = 300 * time.Second // 验证码有效期
	emailCodeResend  = 60 * time.Second  // 同一邮箱重新发送间隔
	emailCodeIPLimit = 3                 // 每个 IP 每分钟最多发送次数
	forgetLimit      = 3                 // 找回密码验证码错误次数上限（5 分钟内）
	forgetLimitTTL   = 300 * time.Second
)

// Client 请求方信息。
type Client struct {
	IP     string
	UA     string
	Locale string
}

// AuthResult 登录 / 注册成功的返回。
type AuthResult struct {
	Token   string `json:"token"`
	IsAdmin bool   `json:"is_admin"`
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) issue(ctx context.Context, u *model.User, cl Client) (*AuthResult, error) {
	token, err := s.Auth.Issue(ctx, u.ID, cl.IP, cl.UA)
	if err != nil {
		return nil, err
	}
	hook.Emit(s.Hooks, ctx, hook.EventUserLogin, hook.User{UserID: u.ID, Email: u.Email, IP: cl.IP})
	return &AuthResult{Token: token, IsAdmin: u.IsAdmin != 0}, nil
}

func (s *Service) getInt(ctx context.Context, key string) (int64, error) {
	n, err := s.Cache.Get(ctx, key).Int64()
	if errors.Is(err, redis.Nil) {
		return 0, nil
	}
	return n, err
}

// incr 计数加一并重置有效期（与原项目 Cache::put(count + 1, ttl) 一致）。
func (s *Service) incr(ctx context.Context, key string, ttl time.Duration) error {
	pipe := s.Cache.TxPipeline()
	pipe.Incr(ctx, key)
	pipe.Expire(ctx, key, ttl)
	_, err := pipe.Exec(ctx)
	return err
}

func (s *Service) checkCaptcha(ctx context.Context, response string) error {
	if !s.Settings.Bool("recaptcha_enable") {
		return nil
	}
	if !s.Captcha.Verify(ctx, s.Settings.String("recaptcha_key"), response) {
		return errCaptcha
	}
	return nil
}

// checkEmailPolicy 邮箱后缀白名单与 Gmail 别名限制。
func (s *Service) checkEmailPolicy(email string) error {
	if s.Settings.Bool("email_whitelist_enable") && !util.EmailSuffixVerify(email, s.Settings.Strings("email_whitelist_suffix")) {
		return errWhitelist
	}
	if s.Settings.Bool("email_gmail_limit_enable") && gmailAlias(email) {
		return errGmailAlias
	}
	return nil
}

// checkEmailCode 校验邮箱验证码（常量时间比较）。
func (s *Service) checkEmailCode(ctx context.Context, email, code string) (bool, error) {
	cached, err := s.Cache.Get(ctx, s.Cache.KeyOf(cache.KeyEmailVerifyCode, cacheEmail(email))).Result()
	if errors.Is(err, redis.Nil) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return cached != "" && subtle.ConstantTimeCompare([]byte(cached), []byte(code)) == 1, nil
}

func (s *Service) findUser(ctx context.Context, email string) (*model.User, error) {
	var u model.User
	err := database.WhereEmail(s.DB.WithContext(ctx), email).Take(&u).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, nil
	}
	return &u, err
}

// RegisterInput 注册参数。
type RegisterInput struct {
	Email      string `json:"email" form:"email"`
	Password   string `json:"password" form:"password"`
	InviteCode string `json:"invite_code" form:"invite_code"`
	EmailCode  string `json:"email_code" form:"email_code"`
	Captcha    string `json:"captcha" form:"captcha"`
}

// Register 注册。检查顺序与原项目一致。
func (s *Service) Register(ctx context.Context, in RegisterInput, cl Client) (*AuthResult, error) {
	if err := emailFormat(in.Email, true); err != nil {
		return nil, err
	}
	if err := passwordFormat(in.Password, 0); err != nil {
		return nil, err
	}

	ipLimit := s.Settings.Bool("register_limit_by_ip_enable")
	ipKey := s.Cache.KeyOf(cache.KeyRegisterIPRateLimit, cl.IP)
	expire := s.Settings.Int("register_limit_expire")
	if ipLimit {
		n, err := s.getInt(ctx, ipKey)
		if err != nil {
			return nil, err
		}
		if n >= s.Settings.Int("register_limit_count") {
			return nil, errRegisterLimit.With(map[string]any{"minute": expire})
		}
	}
	if err := s.checkCaptcha(ctx, in.Captcha); err != nil {
		return nil, err
	}
	if err := s.checkEmailPolicy(in.Email); err != nil {
		return nil, err
	}
	if s.Settings.Bool("stop_register") {
		return nil, errRegisterClosed
	}
	inviteForce := s.Settings.Bool("invite_force")
	if inviteForce && in.InviteCode == "" {
		return nil, errInviteRequired
	}
	emailVerify := s.Settings.Bool("email_verify")
	if emailVerify {
		if !sixDigits.MatchString(in.EmailCode) {
			return nil, errEmailCode
		}
		ok, err := s.checkEmailCode(ctx, in.Email, in.EmailCode)
		if err != nil {
			return nil, err
		}
		if !ok {
			return nil, errEmailCode
		}
	}

	now := s.now().Unix()
	var user *model.User
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		exist, err := s.findUserTx(tx, in.Email)
		if err != nil {
			return err
		}
		if exist {
			return errEmailExists
		}
		nu := account.NewUser{Email: in.Email, Password: in.Password, LastLoginAt: &now}
		if in.InviteCode != "" {
			inviterID, found, err := s.useInviteCode(tx, in.InviteCode)
			if err != nil {
				return err
			}
			if !found && inviteForce {
				return errInviteInvalid
			}
			if inviterID > 0 {
				nu.InviteUserID = &inviterID
			}
		}
		if planID := s.Settings.Int("try_out_plan_id"); planID > 0 {
			var p model.Plan
			err := tx.Take(&p, planID).Error
			if err == nil {
				exp := now + int64(s.Settings.Float("try_out_hour")*3600)
				nu.Plan, nu.ExpiredAt = &p, &exp
			} else if !errors.Is(err, gorm.ErrRecordNotFound) {
				return err
			}
		}
		user, _, err = account.Create(tx, nu)
		return err
	})
	if err != nil {
		var he *httpx.Error
		if errors.As(err, &he) {
			return nil, err
		}
		return nil, httpx.Internal(err)
	}

	if emailVerify {
		_ = s.Cache.Del(ctx, s.Cache.KeyOf(cache.KeyEmailVerifyCode, cacheEmail(in.Email))).Err()
	}
	if ipLimit {
		if err := s.incr(ctx, ipKey, time.Duration(expire)*time.Minute); err != nil {
			return nil, err
		}
	}
	hook.Emit(s.Hooks, ctx, hook.EventUserRegistered, hook.User{
		UserID: user.ID, Email: user.Email, InviteUserID: user.InviteUserID, IP: cl.IP,
	})
	return s.issue(ctx, user, cl)
}

func (s *Service) findUserTx(tx *gorm.DB, email string) (bool, error) {
	var n int64
	err := database.WhereEmail(tx.Model(&model.User{}), email).Count(&n).Error
	return n > 0, err
}

// useInviteCode 查找可用邀请码，返回邀请人 ID（邀请码所属用户为 0 时没有邀请人）与是否有效。
// 未开启"邀请码永不失效"时，邀请码使用后作废；用条件更新防止同一邀请码被并发重复使用。
func (s *Service) useInviteCode(tx *gorm.DB, code string) (int64, bool, error) {
	var ic model.InviteCode
	err := tx.Where("code = ? AND status = 0", code).Take(&ic).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, err
	}
	if !s.Settings.Bool("invite_never_expire") {
		res := tx.Model(&model.InviteCode{}).Where("id = ? AND status = 0", ic.ID).Update("status", 1)
		if res.Error != nil {
			return 0, false, res.Error
		}
		if res.RowsAffected == 0 {
			return 0, false, nil
		}
	}
	return ic.UserID, true, nil
}

// LoginInput 登录参数。
type LoginInput struct {
	Email    string `json:"email" form:"email"`
	Password string `json:"password" form:"password"`
}

// Login 密码登录。
func (s *Service) Login(ctx context.Context, in LoginInput, cl Client) (*AuthResult, error) {
	if err := emailFormat(in.Email, false); err != nil {
		return nil, err
	}
	if err := passwordFormat(in.Password, 0); err != nil {
		return nil, err
	}
	limit := s.Settings.Bool("password_limit_enable")
	limitKey := s.Cache.KeyOf(cache.KeyPasswordErrorLimit, cacheEmail(in.Email))
	expire := s.Settings.Int("password_limit_expire")
	if limit {
		n, err := s.getInt(ctx, limitKey)
		if err != nil {
			return nil, err
		}
		if n >= s.Settings.Int("password_limit_count") {
			return nil, errPasswordLimit.With(map[string]any{"minute": expire})
		}
	}
	u, err := s.findUser(ctx, in.Email)
	if err != nil {
		return nil, err
	}
	if u == nil {
		return nil, errLoginFailed
	}
	if !util.VerifyPassword(deref(u.PasswordAlgo), deref(u.PasswordSalt), in.Password, u.Password) {
		if limit {
			if err := s.incr(ctx, limitKey, time.Duration(expire)*time.Minute); err != nil {
				return nil, err
			}
		}
		return nil, errLoginFailed
	}
	if u.Banned != 0 {
		return nil, errSuspended
	}
	return s.issue(ctx, u, cl)
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}

// ResetInput 找回密码参数。
type ResetInput struct {
	Email     string `json:"email" form:"email"`
	EmailCode string `json:"email_code" form:"email_code"`
	Password  string `json:"password" form:"password"`
}

// ResetPassword 通过邮箱验证码重置密码，成功后删除全部会话。
func (s *Service) ResetPassword(ctx context.Context, in ResetInput) error {
	if err := emailFormat(in.Email, true); err != nil {
		return err
	}
	if err := passwordFormat(in.Password, 64); err != nil {
		return err
	}
	if in.EmailCode == "" {
		return errEmailCodeRequired
	}
	if !sixDigits.MatchString(in.EmailCode) {
		return errEmailCode
	}
	email := cacheEmail(in.Email)
	limitKey := s.Cache.KeyOf(cache.KeyForgetRequestLimit, email)
	n, err := s.getInt(ctx, limitKey)
	if err != nil {
		return err
	}
	if n >= forgetLimit {
		return errResetLimit
	}
	ok, err := s.checkEmailCode(ctx, in.Email, in.EmailCode)
	if err != nil {
		return err
	}
	if !ok {
		if err := s.incr(ctx, limitKey, forgetLimitTTL); err != nil {
			return err
		}
		return errEmailCode
	}
	u, err := s.findUser(ctx, in.Email)
	if err != nil {
		return err
	}
	if u == nil {
		return errNotRegistered
	}
	// 数据库排序规则可能把 á 与 a 视为相同，必须按字节确认是同一个邮箱（与原项目一致）。
	if cacheEmail(u.Email) != email {
		return errEmailCode
	}
	hash, err := util.HashPassword(in.Password)
	if err != nil {
		return err
	}
	if err := s.DB.WithContext(ctx).Model(&model.User{}).Where("id = ?", u.ID).Updates(map[string]any{
		"password": hash, "password_algo": nil, "password_salt": nil,
	}).Error; err != nil {
		return err
	}
	_ = s.Cache.Del(ctx, s.Cache.KeyOf(cache.KeyEmailVerifyCode, email)).Err()
	return s.Auth.RemoveAll(ctx, u.ID)
}

// 发送验证码的场景：注册时要求邮箱未注册，找回密码时要求已注册，不填不检查（与原项目 isforget 参数一致）。
const (
	SceneRegister = "register"
	SceneReset    = "reset"
)

// EmailCodeInput 发送邮箱验证码参数。
type EmailCodeInput struct {
	Email   string `json:"email" form:"email"`
	Scene   string `json:"scene" form:"scene"`
	Captcha string `json:"captcha" form:"captcha"`
}

// SendEmailCode 发送邮箱验证码。
func (s *Service) SendEmailCode(ctx context.Context, in EmailCodeInput, cl Client) error {
	if err := emailFormat(in.Email, true); err != nil {
		return err
	}
	if in.Scene != "" && in.Scene != SceneRegister && in.Scene != SceneReset {
		return errInvalidParams
	}

	ipKey := s.Cache.KeyOf(cache.KeySendEmailCodeIPLimit, cl.IP)
	n, err := s.getInt(ctx, ipKey)
	if err != nil {
		return err
	}
	if n >= emailCodeIPLimit {
		return errTooMany
	}
	// 与原项目 RateLimiter::hit 一致：只在窗口开始时设置有效期。
	hits, err := s.Cache.Incr(ctx, ipKey).Result()
	if err != nil {
		return err
	}
	if hits == 1 {
		if err := s.Cache.Expire(ctx, ipKey, time.Minute).Err(); err != nil {
			return err
		}
	}

	if err := s.checkCaptcha(ctx, in.Captcha); err != nil {
		return err
	}
	if err := s.checkEmailPolicy(in.Email); err != nil {
		return err
	}
	if in.Scene != "" {
		u, err := s.findUser(ctx, in.Email)
		if err != nil {
			return err
		}
		if in.Scene == SceneRegister && u != nil {
			return errEmailRegistered
		}
		if in.Scene == SceneReset && u == nil {
			return errNotRegistered
		}
	}

	email := cacheEmail(in.Email)
	lastKey := s.Cache.KeyOf(cache.KeyLastSendEmailVerifyTimestamp, email)
	ok, err := s.Cache.SetNX(ctx, lastKey, s.now().Unix(), emailCodeResend).Result()
	if err != nil {
		return err
	}
	if !ok {
		return errCodeSent
	}
	code := strconv.FormatInt(util.RandomInt(100000, 999999), 10)
	if err := s.Cache.Set(ctx, s.Cache.KeyOf(cache.KeyEmailVerifyCode, email), code, emailCodeTTL).Err(); err != nil {
		return err
	}
	appName := s.Settings.String("app_name")
	err = s.Mail.Dispatch(ctx, mail.Payload{
		Email:        in.Email,
		Subject:      appName + s.I18n.T(cl.Locale, "Email verification code", nil),
		TemplateName: mail.TplVerify,
		TemplateValue: map[string]any{
			"name": appName,
			"code": code,
			"url":  s.Settings.String("app_url"),
		},
	})
	if err != nil {
		_ = s.Cache.Del(ctx, lastKey).Err()
		return err
	}
	return nil
}

// CodeLogin 使用快捷登录码登录（对应原项目 token2Login 的 verify 参数）。
func (s *Service) CodeLogin(ctx context.Context, code string, cl Client) (*AuthResult, error) {
	userID, err := s.Auth.ConsumeTempToken(ctx, code)
	if err != nil {
		return nil, err
	}
	if userID == 0 {
		return nil, errTokenInvalid
	}
	var u model.User
	err = s.DB.WithContext(ctx).Take(&u, userID).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errUserNotExist
	}
	if err != nil {
		return nil, err
	}
	if u.Banned != 0 {
		return nil, errSuspended
	}
	return s.issue(ctx, &u, cl)
}

// QuickLoginURL 生成快捷登录链接，有效期 60 秒。baseURL 为未设置站点地址时使用的当前请求地址。
// 链接格式 {站点地址}/#/login?verify={code}&redirect={页面}，用户端前端须按此处理。
func (s *Service) QuickLoginURL(ctx context.Context, userID int64, redirect, baseURL string) (string, error) {
	code, err := s.Auth.CreateTempToken(ctx, userID)
	if err != nil {
		return "", err
	}
	if redirect == "" {
		redirect = "dashboard"
	}
	base := s.Settings.String("app_url")
	if base == "" {
		base = baseURL
	}
	return base + "/#/login?verify=" + url.QueryEscape(code) + "&redirect=" + url.QueryEscape(redirect), nil
}

// InviteVisit 邀请链接访问次数加一，邀请码不存在时忽略。
func (s *Service) InviteVisit(ctx context.Context, code string) error {
	if code == "" {
		return nil
	}
	return s.DB.WithContext(ctx).Model(&model.InviteCode{}).Where("code = ?", code).
		Update("pv", gorm.Expr("pv + 1")).Error
}
