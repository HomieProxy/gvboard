// Package invite 邀请与佣金：邀请码、邀请统计、佣金明细，以及佣金自动确认与发放（含三级分销），
// 对应原项目 User/InviteController 与 check:commission。
package invite

import (
	"context"
	"errors"
	"time"

	"go.uber.org/zap"
	"gorm.io/gorm"
	"gorm.io/gorm/clause"

	"gvboard/internal/httpx"
	"gvboard/internal/model"
	"gvboard/internal/setting"
	"gvboard/internal/util"
)

var (
	errUserNotExist = httpx.BadRequest("The user does not exist")
	errGenLimit     = httpx.BadRequest("The maximum number of creations has been reached")
)

// Service 邀请与佣金业务。
type Service struct {
	DB       *gorm.DB
	Settings *setting.Service
	Log      *zap.Logger
	Now      func() time.Time
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

// Code 邀请码。
type Code struct {
	ID        int64  `json:"id"`
	Code      string `json:"code"`
	PV        int64  `json:"pv"`
	CreatedAt int64  `json:"created_at"`
}

func toCode(c model.InviteCode) Code {
	return Code{ID: c.ID, Code: c.Code, PV: c.PV, CreatedAt: c.CreatedAt}
}

// Stat 邀请统计（原项目 stat 数组的 5 项）。
type Stat struct {
	Invitees          int64   `json:"invitees"`           // 已注册用户数
	TotalCommission   int64   `json:"total_commission"`   // 累计获得的佣金
	PendingCommission float64 `json:"pending_commission"` // 确认中的佣金
	CommissionRate    int64   `json:"commission_rate"`    // 佣金比例
	CommissionBalance int64   `json:"commission_balance"` // 可用佣金
}

// Overview 邀请页数据。
type Overview struct {
	Codes []Code `json:"codes"`
	Stat  Stat   `json:"stat"`
}

// Fetch 未使用的邀请码与邀请统计（对应原项目 invite/fetch）。
func (s *Service) Fetch(ctx context.Context, userID int64) (*Overview, error) {
	db := s.DB.WithContext(ctx)
	var u model.User
	if err := db.Select("id", "commission_rate", "commission_balance").Take(&u, userID).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return nil, errUserNotExist
		}
		return nil, err
	}
	var codes []model.InviteCode
	if err := db.Where("user_id = ? AND status = 0", userID).Order("id").Find(&codes).Error; err != nil {
		return nil, err
	}
	out := &Overview{Codes: make([]Code, 0, len(codes))}
	for _, c := range codes {
		out.Codes = append(out.Codes, toCode(c))
	}

	st := &out.Stat
	st.CommissionRate = s.Settings.Int("invite_commission")
	if u.CommissionRate != nil && *u.CommissionRate != 0 {
		st.CommissionRate = *u.CommissionRate
	}
	st.CommissionBalance = u.CommissionBalance
	if err := db.Model(&model.User{}).Where("invite_user_id = ?", userID).Count(&st.Invitees).Error; err != nil {
		return nil, err
	}
	if err := db.Model(&model.CommissionLog{}).Where("invite_user_id = ?", userID).
		Select("COALESCE(SUM(get_amount), 0)").Scan(&st.TotalCommission).Error; err != nil {
		return nil, err
	}
	// 与原项目一致：只统计已完成且待确认的订单；开启分销时按一级比例折算。
	var pending int64
	if err := db.Model(&model.Order{}).
		Where("status = ? AND commission_status = ? AND invite_user_id = ?", model.OrderStatusCompleted, model.CommissionStatusPending, userID).
		Select("COALESCE(SUM(commission_balance), 0)").Scan(&pending).Error; err != nil {
		return nil, err
	}
	st.PendingCommission = float64(pending)
	if s.Settings.Bool("commission_distribution_enable") {
		l1 := 0.0
		if p := s.Settings.FloatPtr("commission_distribution_l1"); p != nil {
			l1 = *p
		}
		st.PendingCommission *= l1 / 100
	}
	return out, nil
}

// Generate 生成邀请码（对应原项目 invite/save）。未使用的邀请码数量达到 invite_gen_limit 时拒绝。
// 锁定用户行串行化同一用户的并发请求，避免绕过数量上限；邀请码与已有邀请码不重复。
func (s *Service) Generate(ctx context.Context, userID int64) (*Code, error) {
	var out Code
	err := s.DB.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var u model.User
		if err := tx.Clauses(clause.Locking{Strength: "UPDATE"}).Select("id").Take(&u, userID).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return errUserNotExist
			}
			return err
		}
		var n int64
		if err := tx.Model(&model.InviteCode{}).Where("user_id = ? AND status = 0", userID).Count(&n).Error; err != nil {
			return err
		}
		if n >= s.Settings.Int("invite_gen_limit") {
			return errGenLimit
		}
		code, err := uniqueCode(tx)
		if err != nil {
			return err
		}
		c := model.InviteCode{UserID: userID, Code: code}
		if err := tx.Create(&c).Error; err != nil {
			return err
		}
		out = toCode(c)
		return nil
	})
	if err != nil {
		return nil, err
	}
	return &out, nil
}

func uniqueCode(tx *gorm.DB) (string, error) {
	for range 10 {
		code := util.RandomChar(8, false)
		var n int64
		if err := tx.Model(&model.InviteCode{}).Where("code = ?", code).Count(&n).Error; err != nil {
			return "", err
		}
		if n == 0 {
			return code, nil
		}
	}
	return "", errors.New("生成邀请码失败：连续重复")
}

// Page 分页参数。原项目 page_size 小于 10 时按 10 处理，这里另外限制最大 100。
type Page struct {
	Current  int `form:"current"`
	PageSize int `form:"page_size"`
}

func (p Page) normalize() (offset, limit int) {
	cur, size := p.Current, p.PageSize
	if cur < 1 {
		cur = 1
	}
	if size < 10 {
		size = 10
	}
	if size > 100 {
		size = 100
	}
	return (cur - 1) * size, size
}

// Detail 佣金明细。
type Detail struct {
	ID          int64  `json:"id"`
	TradeNo     string `json:"trade_no"`
	OrderAmount int64  `json:"order_amount"`
	GetAmount   int64  `json:"get_amount"`
	CreatedAt   int64  `json:"created_at"`
}

// Details 佣金明细分页结果。
type Details struct {
	Data  []Detail `json:"data"`
	Total int64    `json:"total"`
}

// Details 佣金明细（对应原项目 invite/details），只列出金额大于 0 的记录，按时间倒序。
func (s *Service) Details(ctx context.Context, userID int64, page Page) (*Details, error) {
	q := s.DB.WithContext(ctx).Model(&model.CommissionLog{}).Where("invite_user_id = ? AND get_amount > 0", userID)
	out := &Details{Data: []Detail{}}
	if err := q.Count(&out.Total).Error; err != nil {
		return nil, err
	}
	offset, limit := page.normalize()
	err := q.Select("id", "trade_no", "order_amount", "get_amount", "created_at").
		Order("created_at DESC").Order("id DESC").Offset(offset).Limit(limit).Find(&out.Data).Error
	if err != nil {
		return nil, err
	}
	return out, nil
}
