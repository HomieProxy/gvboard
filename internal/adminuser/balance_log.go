package adminuser

import (
	"context"
	"slices"
	"strings"

	"gorm.io/gorm"

	"gvboard/internal/balance"
	"gvboard/internal/model"
)

// BalanceLogQuery 余额变动日志查询（决策 D86）。Type 为空表示全部类型；
// Email（模糊匹配）、Since、Until（Unix 秒，含两端）只用于全站余额明细。
type BalanceLogQuery struct {
	Type     string `form:"type"`
	Email    string `form:"email"`
	Since    int64  `form:"since"`
	Until    int64  `form:"until"`
	Current  int    `form:"current"`
	PageSize int    `form:"page_size"`
}

// BalanceLogRow 余额变动日志，附带用户邮箱与操作管理员邮箱（已删除时为空）。
type BalanceLogRow struct {
	model.BalanceLog
	Email         string `json:"email"`
	OperatorEmail string `json:"operator_email"`
}

// BalanceLogPage 余额变动日志分页；Income、Expense 为筛选范围内增加、减少金额的合计（分，Expense 为负数）。
type BalanceLogPage struct {
	Data    []BalanceLogRow `json:"data"`
	Total   int64           `json:"total"`
	Income  int64           `json:"income"`
	Expense int64           `json:"expense"`
}

// BalanceLogs 用户的余额变动日志，按时间倒序，每页 10～100 条。
func (s *Service) BalanceLogs(ctx context.Context, userID int64, q BalanceLogQuery) (*BalanceLogPage, error) {
	if _, err := s.user(ctx, s.DB, userID); err != nil {
		return nil, err
	}
	q.Email, q.Since, q.Until = "", 0, 0
	return s.balanceLogs(ctx, userID, q)
}

// AllBalanceLogs 全站余额变动日志（决策 D88）。
func (s *Service) AllBalanceLogs(ctx context.Context, q BalanceLogQuery) (*BalanceLogPage, error) {
	return s.balanceLogs(ctx, 0, q)
}

func (s *Service) balanceLogs(ctx context.Context, userID int64, q BalanceLogQuery) (*BalanceLogPage, error) {
	if q.Type != "" && !slices.Contains(balance.Types, q.Type) {
		return nil, errInvalid
	}
	if q.Since > 0 && q.Until > 0 && q.Since > q.Until {
		return nil, errInvalid
	}
	db := s.DB.WithContext(ctx)
	scope := func() *gorm.DB {
		b := db.Model(&model.BalanceLog{})
		if userID > 0 {
			b = b.Where("user_id = ?", userID)
		}
		if email := strings.ToLower(strings.TrimSpace(q.Email)); email != "" {
			b = b.Where("user_id IN (?)", db.Model(&model.User{}).Select("id").Where("LOWER(email) LIKE ?", "%"+email+"%"))
		}
		if q.Type != "" {
			b = b.Where("type = ?", q.Type)
		}
		if q.Since > 0 {
			b = b.Where("created_at >= ?", q.Since)
		}
		if q.Until > 0 {
			b = b.Where("created_at <= ?", q.Until)
		}
		return b
	}
	out := &BalanceLogPage{Data: []BalanceLogRow{}}
	if err := scope().Count(&out.Total).Error; err != nil {
		return nil, err
	}
	var sum struct {
		Income  int64
		Expense int64
	}
	if err := scope().Select("COALESCE(SUM(CASE WHEN amount > 0 THEN amount ELSE 0 END), 0) AS income, " +
		"COALESCE(SUM(CASE WHEN amount < 0 THEN amount ELSE 0 END), 0) AS expense").Scan(&sum).Error; err != nil {
		return nil, err
	}
	out.Income, out.Expense = sum.Income, sum.Expense
	size := min(max(q.PageSize, 10), 100)
	cur := max(q.Current, 1)
	var rows []model.BalanceLog
	if err := scope().Order("created_at DESC").Order("id DESC").Offset((cur - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, err
	}
	var ids []int64
	for _, r := range rows {
		if !slices.Contains(ids, r.UserID) {
			ids = append(ids, r.UserID)
		}
		if r.OperatorID != nil && !slices.Contains(ids, *r.OperatorID) {
			ids = append(ids, *r.OperatorID)
		}
	}
	emails := map[int64]string{}
	if len(ids) > 0 {
		var users []model.User
		if err := db.Select("id", "email").Where("id IN ?", ids).Find(&users).Error; err != nil {
			return nil, err
		}
		for _, u := range users {
			emails[u.ID] = u.Email
		}
	}
	for _, r := range rows {
		row := BalanceLogRow{BalanceLog: r, Email: emails[r.UserID]}
		if r.OperatorID != nil {
			row.OperatorEmail = emails[*r.OperatorID]
		}
		out.Data = append(out.Data, row)
	}
	return out, nil
}
