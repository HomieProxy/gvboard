// Package usercenter 用户中心：用户信息、统计、修改密码、会话、Telegram 解绑、佣金划转、礼品卡兑换，
// 对应原项目 User/UserController。订阅相关操作见 subscription 包。
package usercenter

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"errors"
	"sort"
	"time"

	"gorm.io/gorm"

	"gvboard/internal/auth"
	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/setting"
	"gvboard/internal/subscription"
	"gvboard/internal/util"
)

var (
	errInvalidParams     = httpx.Validation("Invalid parameters")
	errUserNotExist      = httpx.BadRequest("The user does not exist")
	errOldPwdRequired    = httpx.Validation("Old password cannot be empty")
	errNewPwdRequired    = httpx.Validation("New password cannot be empty")
	errPasswordMin       = httpx.Validation("Password must be greater than 8 digits")
	errOldPwdWrong       = httpx.BadRequest("The old password is wrong")
	errTransferAmount    = httpx.Validation("The transfer amount parameter is wrong")
	errInsufficient      = httpx.BadRequest("Insufficient commission balance")
	errGiftcardRequired  = httpx.Validation("Giftcard cannot be empty")
	errGiftcardNotExist  = httpx.BadRequest("The gift card does not exist")
	errGiftcardNotYet    = httpx.BadRequest("The gift card is not yet valid")
	errGiftcardExpired   = httpx.BadRequest("The gift card has expired")
	errGiftcardLimit     = httpx.BadRequest("The gift card usage limit has been reached")
	errGiftcardUsed      = httpx.BadRequest("The gift card has already been used by this user")
	errGiftcardNotSuit   = httpx.BadRequest("Not suitable gift card type")
	errGiftcardUnknown   = httpx.BadRequest("Unknown gift card type")
	errPlanNotExist      = httpx.BadRequest("Subscription plan does not exist")
	errUnbindTelegramErr = httpx.BadRequest("Unbind telegram failed")
)

// Service 用户中心业务。
type Service struct {
	DB            *gorm.DB
	Settings      *setting.Service
	Auth          *auth.Service
	Subscriptions *subscription.Service
	Now           func() time.Time
}

func (s *Service) now() time.Time {
	if s.Now != nil {
		return s.Now()
	}
	return time.Now()
}

func (s *Service) user(ctx context.Context, id int64) (*model.User, error) {
	var u model.User
	err := s.DB.WithContext(ctx).Take(&u, id).Error
	if errors.Is(err, gorm.ErrRecordNotFound) {
		return nil, errUserNotExist
	}
	return &u, err
}

// Info 用户信息（订阅相关信息由订阅接口提供）。
type Info struct {
	ID                int64  `json:"id"`
	Email             string `json:"email"`
	Balance           int64  `json:"balance"`
	CommissionBalance int64  `json:"commission_balance"`
	Discount          *int64 `json:"discount"`
	CommissionRate    *int64 `json:"commission_rate"`
	TelegramBound     bool   `json:"telegram_bound"`
	SubscriptionLimit int64  `json:"subscription_limit"`
	AvatarURL         string `json:"avatar_url"`
	LastLoginAt       *int64 `json:"last_login_at"`
	CreatedAt         int64  `json:"created_at"`
}

// Info 对应原项目 user/info 中的用户级字段。
func (s *Service) Info(ctx context.Context, userID int64) (*Info, error) {
	u, err := s.user(ctx, userID)
	if err != nil {
		return nil, err
	}
	sum := md5.Sum([]byte(u.Email))
	return &Info{
		ID: u.ID, Email: u.Email, Balance: u.Balance, CommissionBalance: u.CommissionBalance,
		Discount: u.Discount, CommissionRate: u.CommissionRate, TelegramBound: u.TelegramID != nil,
		SubscriptionLimit: s.Subscriptions.Limit(u),
		AvatarURL:         "https://cravatar.cn/avatar/" + hex.EncodeToString(sum[:]) + "?s=64&d=identicon",
		LastLoginAt:       u.LastLoginAt, CreatedAt: u.CreatedAt,
	}, nil
}

// Stat 待支付订单数、未关闭工单数、邀请人数（对应原项目 user/getStat）。
type Stat struct {
	PendingOrders int64 `json:"pending_orders"`
	OpenTickets   int64 `json:"open_tickets"`
	Invitees      int64 `json:"invitees"`
}

func (s *Service) Stat(ctx context.Context, userID int64) (*Stat, error) {
	db := s.DB.WithContext(ctx)
	var st Stat
	if err := db.Model(&model.Order{}).Where("status = 0 AND user_id = ?", userID).Count(&st.PendingOrders).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&model.Ticket{}).Where("status = 0 AND user_id = ?", userID).Count(&st.OpenTickets).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&model.User{}).Where("invite_user_id = ?", userID).Count(&st.Invitees).Error; err != nil {
		return nil, err
	}
	return &st, nil
}

// ChangePassword 校验旧密码后修改为 bcrypt，并删除全部会话（与原项目一致，当前会话也需重新登录）。
func (s *Service) ChangePassword(ctx context.Context, userID int64, oldPwd, newPwd string) error {
	if oldPwd == "" {
		return errOldPwdRequired
	}
	if newPwd == "" {
		return errNewPwdRequired
	}
	if len([]rune(newPwd)) < 8 {
		return errPasswordMin
	}
	u, err := s.user(ctx, userID)
	if err != nil {
		return err
	}
	if !util.VerifyPassword(deref(u.PasswordAlgo), deref(u.PasswordSalt), oldPwd, u.Password) {
		return errOldPwdWrong
	}
	hash, err := util.HashPassword(newPwd)
	if err != nil {
		return err
	}
	if err := s.DB.WithContext(ctx).Model(u).Updates(map[string]any{
		"password": hash, "password_algo": nil, "password_salt": nil,
	}).Error; err != nil {
		return err
	}
	return s.Auth.RemoveAll(ctx, userID)
}

// Session 会话信息，不包含登录凭证。
type Session struct {
	ID      string `json:"id"`
	IP      string `json:"ip"`
	UA      string `json:"ua"`
	LoginAt int64  `json:"login_at"`
	Current bool   `json:"current"`
}

// Sessions 活动会话，按登录时间倒序。
func (s *Service) Sessions(ctx context.Context, id *auth.Identity) ([]Session, error) {
	m, err := s.Auth.Sessions(ctx, id.ID)
	if err != nil {
		return nil, err
	}
	out := make([]Session, 0, len(m))
	for sid, meta := range m {
		out = append(out, Session{ID: sid, IP: meta.IP, UA: meta.UA, LoginAt: meta.LoginAt, Current: sid == id.Session})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].LoginAt > out[j].LoginAt })
	return out, nil
}

// UnbindTelegram 解绑 Telegram。
func (s *Service) UnbindTelegram(ctx context.Context, userID int64) error {
	if err := s.DB.WithContext(ctx).Model(&model.User{}).Where("id = ?", userID).Update("telegram_id", nil).Error; err != nil {
		return errUnbindTelegramErr
	}
	return nil
}

// TransferCommission 佣金划转到余额，生成一条已完成的充值订单作为记录（与原项目一致）。
// 划转订单不产生邀请佣金（决策 D17）。
func (s *Service) TransferCommission(ctx context.Context, userID, amount int64) error {
	if amount < 1 {
		return errTransferAmount
	}
	return s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		res := tx.Model(&model.User{}).Where("id = ? AND commission_balance >= ?", userID, amount).Updates(map[string]any{
			"commission_balance": gorm.Expr("commission_balance - ?", amount),
			"balance":            gorm.Expr("balance + ?", amount),
		})
		if res.Error != nil {
			return res.Error
		}
		if res.RowsAffected != 1 {
			return errInsufficient
		}
		callback := "佣金划转 Commission transfer"
		return tx.Create(&model.Order{
			UserID: userID, Period: "deposit", TradeNo: util.GenerateOrderNo(),
			Type: 9, Status: 3, TotalAmount: 0, SurplusAmount: &amount, CallbackNo: &callback,
		}).Error
	})
}

func deref(p *string) string {
	if p == nil {
		return ""
	}
	return *p
}
