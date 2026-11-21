package adminuser

import (
	"context"
	"slices"

	"gvboard/internal/balance"
	"gvboard/internal/model"
)

// BalanceLogQuery 余额变动日志查询（决策 D86）。Type 为空表示全部类型。
type BalanceLogQuery struct {
	Type     string `form:"type"`
	Current  int    `form:"current"`
	PageSize int    `form:"page_size"`
}

// BalanceLogRow 余额变动日志，附带操作管理员邮箱（管理员已删除时为空）。
type BalanceLogRow struct {
	model.BalanceLog
	OperatorEmail string `json:"operator_email"`
}

// BalanceLogPage 余额变动日志分页。
type BalanceLogPage struct {
	Data  []BalanceLogRow `json:"data"`
	Total int64           `json:"total"`
}

// BalanceLogs 用户的余额变动日志，按时间倒序，每页 10～100 条。
func (s *Service) BalanceLogs(ctx context.Context, userID int64, q BalanceLogQuery) (*BalanceLogPage, error) {
	if q.Type != "" && !slices.Contains(balance.Types, q.Type) {
		return nil, errInvalid
	}
	if _, err := s.user(ctx, s.DB, userID); err != nil {
		return nil, err
	}
	b := s.DB.WithContext(ctx).Model(&model.BalanceLog{}).Where("user_id = ?", userID)
	if q.Type != "" {
		b = b.Where("type = ?", q.Type)
	}
	out := &BalanceLogPage{Data: []BalanceLogRow{}}
	if err := b.Count(&out.Total).Error; err != nil {
		return nil, err
	}
	size := min(max(q.PageSize, 10), 100)
	cur := max(q.Current, 1)
	var rows []model.BalanceLog
	if err := b.Order("created_at DESC").Order("id DESC").Offset((cur - 1) * size).Limit(size).Find(&rows).Error; err != nil {
		return nil, err
	}
	var ids []int64
	for _, r := range rows {
		if r.OperatorID != nil && !slices.Contains(ids, *r.OperatorID) {
			ids = append(ids, *r.OperatorID)
		}
	}
	emails := map[int64]string{}
	if len(ids) > 0 {
		var ops []model.User
		if err := s.DB.WithContext(ctx).Select("id", "email").Where("id IN ?", ids).Find(&ops).Error; err != nil {
			return nil, err
		}
		for _, u := range ops {
			emails[u.ID] = u.Email
		}
	}
	for _, r := range rows {
		row := BalanceLogRow{BalanceLog: r}
		if r.OperatorID != nil {
			row.OperatorEmail = emails[*r.OperatorID]
		}
		out.Data = append(out.Data, row)
	}
	return out, nil
}
